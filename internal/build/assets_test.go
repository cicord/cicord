package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedAssetPackage(t *testing.T) {
	dir := t.TempDir()
	assets := filepath.Join(dir, "assets")
	if err := os.Mkdir(assets, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "index.js"), []byte("export default {};"), 0644)
	name := filepath.Join(assets, "image")
	os.WriteFile(name, []byte{0, 255}, 0644)
	a, err := BundleAssets(dir, "index.js", "./assets")
	if err != nil || !strings.Contains(a.Code, AssetMarker) {
		t.Fatal(err)
	}
	os.WriteFile(name, []byte("new"), 0644)
	b, err := BundleAssets(dir, "index.js", "./assets")
	if err != nil || a.Code == b.Code {
		t.Fatal("asset-only change missing from artifact", err)
	}
	if _, err := ReadAssets(dir, "./../outside"); err == nil {
		t.Fatal("accepted traversal")
	}
	if err := os.Symlink(filepath.Join(dir, "index.js"), filepath.Join(assets, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAssets(dir, "./assets"); err == nil {
		t.Fatal("packaged a symlink")
	}
	os.Remove(filepath.Join(assets, "link"))
	os.WriteFile(name, make([]byte, MaxAssetBytes+1), 0644)
	if _, err := ReadAssets(dir, "./assets"); err == nil {
		t.Fatal("accepted oversized assets")
	}
}
