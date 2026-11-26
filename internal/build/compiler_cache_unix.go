//go:build linux || darwin

package build

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/tetratelabs/wazero"
	"golang.org/x/sys/unix"
)

// Only native code for the embedded compiler goes here. Never open a machine-code
// cache from a checkout, inside a plugin or below another user's writable directory.
func privateCacheDir(dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	dir = filepath.Clean(dir)
	if dir == "/" {
		return false
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return false
	}
	defer root.Close()
	path := "."
	uid := uint32(os.Geteuid()) //nolint:gosec // Unix UIDs are unsigned 32-bit identifiers.
	for _, part := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		path = filepath.Join(path, part)
		st, err := root.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if err = root.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return false
			}
			st, err = root.Lstat(path)
		}
		if err != nil || !st.IsDir() {
			return false
		}
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 && stat.Uid != uid {
			return false
		}
		trustedSticky := stat.Uid == 0 && st.Mode()&os.ModeSticky != 0
		if st.Mode().Perm()&022 != 0 && !trustedSticky {
			return false
		}
		for _, marker := range []string{".git", "plugin.yaml", "cicord.yaml"} {
			if _, err := root.Lstat(filepath.Join(path, marker)); !errors.Is(err, os.ErrNotExist) {
				return false
			}
		}
		if path == strings.TrimPrefix(dir, "/") && (stat.Uid != uid || st.Mode().Perm()&077 != 0) {
			return false
		}
	}
	return true
}

// Reject links, public files and foreign ownership even if a directory was made
// private after it was populated. Wazero's cache contains executable machine code.
func privateCacheContents(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer root.Close()
	count := 0
	return fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > 1024 {
			return fs.ErrInvalid
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || st.Mode().Perm()&077 != 0 || !st.IsDir() && (!st.Mode().IsRegular() || stat.Nlink != 1) {
			return fs.ErrPermission
		}
		return nil
	}) == nil
}

func compilerDiskCache() (wazero.CompilationCache, func(bool)) {
	none := func(bool) {}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, none
	}
	// A replaced dependency (or missing Go build information) cannot establish
	// that another process's machine code was produced by this engine.
	version := ""
	for _, d := range info.Deps {
		if d.Path == "github.com/tetratelabs/wazero" && d.Version != "" && d.Sum != "" && d.Replace == nil {
			version = d.Version
			break
		}
	}
	if version == "" {
		return nil, none
	}
	dir := os.Getenv("CICORD_COMPILER_CACHE_DIR")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, none
		}
		dir = filepath.Join(base, "cicord", "compiler")
	}
	if !privateCacheDir(dir) || !privateCacheContents(dir) {
		return nil, none
	}
	cache, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		return nil, none
	}
	// The marker is just an optimization. Wazero independently checks its
	// module/CPU/OS/architecture/version key on every load and publishes atomically.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, none
	}
	marker := fmt.Sprintf("ready-%s-%s-%s-%x", version, runtime.GOOS, runtime.GOARCH, sha256.Sum256(compilerGzip))
	if _, err := root.Lstat(marker); err == nil {
		_ = root.Close()
		return cache, none
	}
	lock, err := root.OpenFile("prepare.lock", os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW, 0600)
	if err != nil {
		_ = root.Close()
		return nil, none
	}
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		_ = root.Close()
		_ = lock.Close()
		return nil, none
	}
	if _, err := root.Lstat(marker); err == nil {
		_ = root.Close()
		_ = lock.Close()
		return cache, none
	}
	return cache, func(ready bool) {
		if ready {
			_ = root.WriteFile(marker, nil, 0600)
		}
		_ = root.Close()
		_ = lock.Close() // Process death also releases this advisory lock.
	}
}
