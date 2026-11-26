package build

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// packageFS exposes ordinary source files and directories to the compiler.
// The os.Root remains authoritative when a directory changes during a read.
type packageFS struct{ root *os.Root }

func (p packageFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") {
		return nil, fs.ErrInvalid
	}
	parts := strings.Split(name, "/")
	for j, part := range parts {
		switch strings.ToLower(part) { // case-insensitive filesystems also resolve ".GIT"
		case ".git", ".cicord", ".cicord-integrity":
			return nil, fs.ErrNotExist
		}
		st, err := p.root.Lstat(filepath.Join(parts[:j+1]...))
		if err != nil {
			return nil, err
		}
		if !st.IsDir() && !st.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: plugin files must be regular files or directories", name)
		}
	}
	return p.root.Open(filepath.FromSlash(name))
}

// CheckPackageFile validates a manifest path without reading the file's contents.
func CheckPackageFile(dir, name string) error {
	return withPackageFile(dir, name, func(fs.File) error { return nil })
}
func withPackageFile(dir, name string, read func(fs.File) error) error {
	rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	if !AssetPath(rel) || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return fmt.Errorf("path %q must be inside the plugin directory", name)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := (packageFS{root}).Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("expected a regular file inside the plugin directory")
	}
	return read(f)
}

// ReadPackageFile bounds and confines manifest-directed metadata reads outside the compiler.
func ReadPackageFile(dir, name string) ([]byte, error) {
	const maxMetadataBytes = 5 << 20
	var result []byte
	err := withPackageFile(dir, name, func(f fs.File) error {
		b, err := io.ReadAll(io.LimitReader(f, maxMetadataBytes+1))
		if len(b) > maxMetadataBytes {
			return errors.New("plugin metadata exceeds 5 MiB")
		}
		result = b
		return err
	})
	return result, err
}
