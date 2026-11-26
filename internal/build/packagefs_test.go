package build

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Hidden package entries stay hidden whatever their case: a case-insensitive filesystem
// (macOS, Windows) resolves ".GIT/config" to ".git/config".
func TestPackageFSHidesMetadataInAnyCase(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{".git/config", ".cicord/state", "src/index.ts"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	p := packageFS{root}
	for _, name := range []string{".git/config", ".GIT/config", ".Cicord/state", "src/.CICORD-INTEGRITY"} {
		if f, err := p.Open(name); !errors.Is(err, fs.ErrNotExist) {
			if f != nil {
				f.Close()
			}
			t.Errorf("%s: %v", name, err)
		}
	}
	f, err := p.Open("src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}
