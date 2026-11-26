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

const MaxAssetBytes = 5 << 20
const MaxAssetEntries = 1024
const AssetMarker = "\n//# cicord-assets-v1 "

func AssetPath(path string) bool {
	return fs.ValidPath(path) && path != "." && len(path) <= 512 && !strings.ContainsAny(path, "\\:\x00\r\n")
}

// ReadAssets snapshots regular files beneath the plugin root. Root confines opens even
// across symlink races. Directories and symlinks have a separate entry quota.
func ReadAssets(dir, spec string) (map[string][]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readAssets(root, spec)
}

func readAssets(root *os.Root, spec string) (map[string][]byte, error) {
	rel, ok := strings.CutPrefix(spec, "./")
	if !ok || !AssetPath(rel) {
		return nil, fmt.Errorf("assets: invalid asset directory %q", spec)
	}
	for p := rel; p != "."; p = filepath.Dir(p) {
		st, err := root.Lstat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("assets: %q must be a directory, without symlinks", p)
		}
	}
	assets, err := root.OpenRoot(rel)
	if err != nil {
		return nil, err
	}
	defer assets.Close()
	out := map[string][]byte{}
	remaining, entries := int64(MaxAssetBytes), 0
	var walk func(string) error
	walk = func(dir string) error {
		d, err := assets.Open(dir)
		if err != nil {
			return err
		}
		defer d.Close()
		for {
			list, err := d.ReadDir(64)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			for _, e := range list {
				entries++
				if entries > MaxAssetEntries {
					return errors.New("assets: too many entries")
				}
				name := filepath.Join(dir, e.Name())
				if !AssetPath(filepath.ToSlash(name)) {
					return errors.New("assets: invalid filename")
				}
				if e.IsDir() {
					if err := walk(name); err != nil {
						return err
					}
					continue
				}
				if !e.Type().IsRegular() {
					return fmt.Errorf("assets: %q is not a regular file", name)
				}
				data, err := readAsset(assets, name, remaining)
				if err != nil {
					return err
				}
				out[filepath.ToSlash(name)] = data
				remaining -= int64(len(data))
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
	}
	return out, walk(".")
}
func readAsset(root *os.Root, name string, remaining int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > remaining {
		return nil, errors.New("assets: regular files limited to 5 MiB per plugin")
	}
	b, err := io.ReadAll(io.LimitReader(f, remaining+1))
	if int64(len(b)) > remaining {
		return nil, errors.New("assets: total exceeds 5 MiB")
	}
	return b, err
}
