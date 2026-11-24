package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/registry"
)

// TestMain keeps the tests off the network: none may fetch the real official registry
// (tests that need an official registry set registry.OfficialURLs themselves).
func TestMain(m *testing.M) {
	registry.OfficialURLs = nil
	os.Exit(m.Run())
}

func commitRepo(t *testing.T, files map[string]string, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for p, c := range files {
		writeFile(t, filepath.Join(dir, p), c)
		if _, err := wt.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	h, err := wt.Commit("r", &git.CommitOptions{Author: &object.Signature{Name: "d", Email: "d@x", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range tags {
		if _, err := r.CreateTag(tag, h, nil); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRegistryCommands(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	reg := commitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: https://example.org/hello.git\n    description: Says hello\n    keywords: [greet]\n"})
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}

	if code, out, errOut := run(t, "registry", "add", "acme", reg, "--dir", dir); code != 0 || !strings.Contains(out, "lists 1 plugin") || !strings.Contains(out, "unofficial") {
		t.Fatalf("add: %d %s %s", code, out, errOut)
	}
	for _, args := range [][]string{{"cicord", reg}, {"Bad", reg}, {"acme2", reg}, {"acme", "https://example.org/x.git"}} {
		if code, _, _ := run(t, append([]string{"registry", "add", "--no-check", "--dir", dir}, args...)...); code == 0 {
			t.Errorf("add %v succeeded", args)
		}
	}
	if code, out, _ := run(t, "registry", "list", "--dir", dir); code != 0 || !regexp.MustCompile(`acme\s+unofficial`).MatchString(out) || !regexp.MustCompile(`cicord\s+official`).MatchString(out) {
		t.Errorf("list: %s", out)
	}
	if code, out, _ := run(t, "registry", "update", "--dir", dir); code != 0 || !strings.Contains(out, "✓ acme (unofficial): 1 plugin(s)") {
		t.Errorf("update: %s", out)
	}
	if code, out, _ := run(t, "registry", "search", "hello", "--dir", dir); code != 0 || !strings.Contains(out, "acme/hello") || strings.Contains(out, "cicord/welcome") {
		t.Errorf("search: %s", out)
	}

	// A registry used by cicord.lock cannot be removed.
	writeFile(t, filepath.Join(dir, "cicord.lock"), "lockVersion: 1\nplugins:\n  acme/hello:\n    version: 1.0.0\n    repo: x\n    commit: y\n    integrity: z\n")
	if code, _, errOut := run(t, "registry", "remove", "acme", "--dir", dir); code == 0 || !strings.Contains(errOut, "still used") {
		t.Errorf("remove used: %d %s", code, errOut)
	}
	if err := os.Remove(filepath.Join(dir, "cicord.lock")); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, "registry", "remove", "acme", "--dir", dir); code != 0 {
		t.Fatalf("remove: %s", errOut)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "cicord.yaml"))
	if strings.Contains(string(data), "acme") {
		t.Errorf("cicord.yaml:\n%s", data)
	}
}

// The default of a release build without the production key: the official URL is set, no
// official key. Nothing fails because of it, and nothing is fetched from it.
func TestRegistryCommandsWithoutOfficialKey(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	old := registry.OfficialURLs
	t.Cleanup(func() { registry.OfficialURLs = old })
	registry.OfficialURLs = []string{"https://127.0.0.1:1/cicord-plugins"} // a fetch would fail differently
	if registry.HasOfficialKeys() {
		t.Skip("this test binary embeds a development official key")
	}
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, out, _ := run(t, "registry", "list", "--dir", dir); code != 0 || !strings.Contains(out, "not verifiable by this build (no official key)") {
		t.Errorf("list: %d %s", code, out)
	}
	if code, out, errOut := run(t, "registry", "update", "--dir", dir); code != 0 || !strings.Contains(out, "• cicord: 9 official plugin(s) bundled with cicord") || strings.Contains(out, "✗") {
		t.Errorf("update: %d %s %s", code, out, errOut)
	}
	if code, out, _ := run(t, "registry", "search", "welcome", "--dir", dir); code != 0 || !strings.Contains(out, "cicord/welcome") {
		t.Errorf("search: %d %s", code, out)
	}
	code, _, errOut := run(t, "plugin", "add", "cicord/welcome", "--bot", "bot", "--dir", dir)
	if code == 0 || !strings.Contains(errOut, "embeds no official public key") || !strings.Contains(errOut, "welcome is bundled with cicord") {
		t.Errorf("add bundled: %d %s", code, errOut)
	}
	if code, _, errOut := run(t, "plugin", "add", "cicord/xp", "--bot", "bot", "--dir", dir); code == 0 || !strings.Contains(errOut, "only the official plugins bundled") {
		t.Errorf("add remote-only: %d %s", code, errOut)
	}
	if code, out, errOut := run(t, "plugin", "update", "--dir", dir); code != 0 {
		t.Errorf("plugin update: %d %s %s", code, out, errOut)
	}
}
