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
)

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
