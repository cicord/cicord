//go:build linux || darwin

package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateCompilerCache(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "private")
	if !privateCacheDir(dir) || !privateCacheContents(dir) {
		t.Fatal("expected an owner-only compiler cache")
	}
	if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if privateCacheContents(dir) {
		t.Fatal("cache with a symlink accepted")
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if privateCacheDir(dir) {
		t.Fatal("shared directory accepted")
	}
	if err := os.WriteFile(filepath.Join(base, "plugin.yaml"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if privateCacheDir(filepath.Join(base, "plugin-cache")) {
		t.Fatal("reviewed plugin package can supply native code cache")
	}
}
