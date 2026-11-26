package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionedFiles(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"docs/nested", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"docs/a.md", "docs/nested/b.md", "docs/nested/c.txt", ".git/config"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(p), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := LoadInstanceFiles(root, []string{"docs/**/*.md"})
	if err != nil || len(files) != 2 || string(files["docs/a.md"]) != "docs/a.md" {
		t.Fatalf("snapshot: %v %v", files, err)
	}
	if err := os.Symlink(filepath.Join(root, ".git/config"), filepath.Join(root, "docs/linked.md")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"docs/*.md", "../*.md", ".git/**", "/etc/passwd"} {
		if _, err := LoadInstanceFiles(root, []string{p}); err == nil {
			t.Errorf("accepted %s", p)
		}
	}
}
func TestReviewedNetworkConfig(t *testing.T) {
	schema := map[string]any{"x-cicord-network-url": true}
	for _, u := range []string{"https://private.example.org", "https://ok.example.org:8080/x", "http://ok.example.org", "https://a:b@ok.example.org"} {
		if len(checkNetworkURLs(schema, schema, u, []string{"ok.example.org"}, "", 0)) == 0 {
			t.Error(u)
		}
	}
	if !networkAllows("https://ok.example.org/path", []string{"*.example.org"}) {
		t.Fatal("wildcard")
	}
}
