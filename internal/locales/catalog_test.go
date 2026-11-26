package locales

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "locales"), 0700); err != nil {
		t.Fatal(err)
	}
	for f, data := range map[string]string{"fr": `{"hello":"Bonjour {name}"}`, "en": `{"hello":"Hello {name}","help":"Help"}`} {
		if err := os.WriteFile(filepath.Join(dir, "locales", f+".json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Load(dir, "./locales")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Text("hello", map[string]any{"name": "{admin}", "admin": "staff"}, "fr-CA"); got != "Bonjour {admin}" {
		t.Fatal(got)
	}
	if got := c.Text("help", nil, "fr"); got != "Help" {
		t.Fatal(got)
	}
	if _, err := Load(dir, "../"); err == nil {
		t.Fatal("accepted parent")
	}
	if err := os.Symlink(filepath.Join(dir, "locales", "fr.json"), filepath.Join(dir, "locales", "de.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "./locales"); err == nil {
		t.Fatal("accepted symlink")
	}
}
