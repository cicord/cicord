package catalog

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every bundled plugin version must be recorded in released, so that copies installed
// from this binary are recognized as unmodified by later binaries.
func TestReleasedIntegrity(t *testing.T) {
	c := Official()
	es, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		sum, err := c.Integrity(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(released[e.Name][e.Version], sum) {
			t.Errorf("the files of %s %s are not recorded as released: add %q to released[%q][%q] in copy.go (keep the older values), or bump its version",
				e.Name, e.Version, sum, e.Name, e.Version)
		}
	}
}

func writeCopy(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for p, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCheckCopy(t *testing.T) {
	c := Official()
	cur, err := c.PluginFiles("welcome")
	if err != nil {
		t.Fatal(err)
	}
	// The current version, as installed: not modified, nothing to change.
	st, err := c.CheckCopy("welcome", writeCopy(t, cur))
	if err != nil {
		t.Fatal(err)
	}
	if st.Modified || st.Version != "0.1.1" || len(st.Changed)+len(st.Added)+len(st.Removed) != 0 {
		t.Fatalf("current copy = %+v", st)
	}
	// An older version number only: not modified.
	old := map[string][]byte{}
	for p, d := range cur {
		old[p] = d
	}
	old["plugin.yaml"] = []byte(strings.Replace(string(cur["plugin.yaml"]), "version: 0.1.1", "version: 0.0.9", 1))
	if st, _ = c.CheckCopy("welcome", writeCopy(t, old)); st.Modified || st.Version != "0.0.9" || len(st.Changed) != 1 || st.Changed[0] != "plugin.yaml" {
		t.Fatalf("older copy = %+v", st)
	}
	// Local edits and extra files: modified.
	old["src/index.ts"] = append(old["src/index.ts"], []byte("\n// local change\n")...)
	old["src/extra.ts"] = []byte("export {};\n")
	st, _ = c.CheckCopy("welcome", writeCopy(t, old))
	if !st.Modified || len(st.Removed) != 1 || st.Removed[0] != "src/extra.ts" {
		t.Fatalf("modified copy = %+v", st)
	}
}
