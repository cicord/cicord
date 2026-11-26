package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// SEC-P-05: plugin secretRefs share the CICORD_ namespace with the host. Every
// variable the host reads from its environment must be reserved, so a bot.yaml
// binding can never hand a runtime credential or setting to a plugin.
func TestHostEnvironmentIsReserved(t *testing.T) {
	read := regexp.MustCompile(`(?:Getenv|LookupEnv)\("(CICORD_[A-Z0-9_]+)"\)|Env\s*=\s*"(CICORD_[A-Z0-9_]+)"|\(env (CICORD_[A-Z0-9_]+)\)`)
	root := filepath.Join("..", "..")
	found := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "dist", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range read.FindAllStringSubmatch(string(raw), -1) {
			for _, name := range m[1:] {
				if name != "" {
					found[name] = path
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 10 {
		t.Fatalf("scan found only %d host variables: %v", len(found), found)
	}
	for name, path := range found {
		if !(*Repo)(nil).ReservedPluginRef(name) {
			t.Errorf("%s (read in %s) is not reserved: a plugin secretRef could bind it", name, path)
		}
	}
}
