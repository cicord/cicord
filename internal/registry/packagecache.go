package registry

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// openCacheParent rejects redirected registry/plugin directories and anchors all
// later staging, rename and removal operations inside the configured cache.
func openCacheParent(cacheDir, dir string) (*os.Root, error) {
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel, err := filepath.Rel(cacheDir, filepath.Dir(dir))
	if err != nil || !filepath.IsLocal(rel) {
		return nil, errors.New("invalid package cache path")
	}
	var prefix string
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		if err := root.Mkdir(prefix, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, errors.New("cannot open registry cache directory")
		}
		info, err := root.Lstat(prefix)
		if err != nil || !info.IsDir() {
			return nil, errors.New("registry cache directory is missing or redirected")
		}
	}
	return root.OpenRoot(rel)
}

func integrityHeader(h hash.Hash, name string, size int64) {
	fmt.Fprintf(h, "%s\x00%d\x00", name, size)
}
func integrityDigest(h hash.Hash) string {
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}

var errCacheDrift = errors.New("cached package differs from the lock")

// verifyCache hashes regular files in the same sorted slash-path order used for
// lock creation. It never trusts a marker in place of the actual package.
func verifyCache(ctx context.Context, parent *os.Root, dir, want string, legacy bool) bool {
	if want == "" {
		return false
	}
	info, err := parent.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	root, err := parent.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer root.Close()
	var names []string
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errCacheDrift
		}
		if legacy && name == ".cicord-integrity" {
			st, err := d.Info()
			if err != nil {
				return err
			}
			// Only the exact old generated marker at the package root is metadata.
			if st.Size() == int64(len(want)+1) {
				f, err := root.Open(name)
				if err != nil {
					return err
				}
				b, err := io.ReadAll(io.LimitReader(f, int64(len(want))+2))
				_ = f.Close()
				if err != nil {
					return err
				}
				if bytes.Equal(b, []byte(want+"\n")) {
					return nil
				}
			}
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return false
	}
	slices.Sort(names)
	h := sha256.New()
	for _, name := range names {
		if ctx.Err() != nil {
			return false
		}
		if err := hashCacheFile(root, h, name); err != nil {
			return false
		}
	}
	return integrityDigest(h) == want
}

func hashCacheFile(root *os.Root, h hash.Hash, name string) error {
	rel := filepath.FromSlash(name)
	st, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errCacheDrift
	}
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(st, opened) {
		return errCacheDrift
	}
	integrityHeader(h, name, opened.Size())
	n, err := io.Copy(h, io.LimitReader(f, opened.Size()))
	if err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if n != opened.Size() || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return errCacheDrift
	}
	return nil
}

// serialize the short publication step; origin access and staging remain independent.
var cachePublish sync.Mutex

func installCache(ctx context.Context, parent *os.Root, name, want string, files map[string][]byte) error {
	if verifyCache(ctx, parent, name, want, false) {
		return nil
	}
	stage := ".staging-" + rand.Text()
	if err := parent.Mkdir(stage, 0700); err != nil {
		return err
	}
	defer func() { _ = parent.RemoveAll(stage) }()
	tmp, err := parent.OpenRoot(stage)
	if err != nil {
		return err
	}
	for path, data := range files {
		if err = ctx.Err(); err != nil {
			break
		}
		rel := filepath.FromSlash(path)
		if err = tmp.MkdirAll(filepath.Dir(rel), 0755); err != nil {
			break
		}
		if err = tmp.WriteFile(rel, data, 0644); err != nil {
			break
		}
	}
	_ = tmp.Close()
	if err != nil {
		return err
	}
	cachePublish.Lock()
	defer cachePublish.Unlock()
	if verifyCache(ctx, parent, name, want, false) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	old := ".replaced-" + rand.Text()
	err = parent.Rename(name, old)
	hasOld := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err = parent.Rename(stage, name); err != nil {
		// Another process may have published the identical pin while this one was staging.
		if verifyCache(ctx, parent, name, want, false) {
			if hasOld {
				_ = parent.RemoveAll(old)
			}
			return nil
		}
		if hasOld {
			_ = parent.Rename(old, name)
		}
		return err
	}
	if hasOld {
		return parent.RemoveAll(old)
	}
	return nil
}
