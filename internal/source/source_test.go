package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSnapshots(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "codi.yaml"), "a: 1\n")
	writeFile(t, filepath.Join(dir, "node_modules/x.js"), "ignored")
	l, err := NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx := context.Background()
	r1, err := l.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r1.Dir, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("node_modules should not be snapshotted")
	}
	r2, _ := l.Fetch(ctx)
	if r1.SHA != r2.SHA || r1.Dir != r2.Dir {
		t.Errorf("same content should give same revision")
	}
	writeFile(t, filepath.Join(dir, "codi.yaml"), "a: 2\n")
	select {
	case <-l.Changes():
	case <-time.After(3 * time.Second):
		t.Fatal("no change notification")
	}
	r3, _ := l.Fetch(ctx)
	if r3.SHA == r1.SHA {
		t.Errorf("content change should give a new revision")
	}
	data, _ := os.ReadFile(filepath.Join(r1.Dir, "codi.yaml"))
	if string(data) != "a: 1\n" {
		t.Errorf("old snapshot mutated: %q", data)
	}
}

func TestGitSource(t *testing.T) {
	upstream := t.TempDir()
	repo, err := git.PlainInitWithOptions(upstream, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	commit := func(msg string, files map[string]string) {
		for p, c := range files {
			writeFile(t, filepath.Join(upstream, p), c)
			if _, err := wt.Add(p); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "dev@example.com", When: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}
	commit("first", map[string]string{"repo/codi.yaml": "v: 1\n", "repo/servers/s.yaml": "x"})

	g, err := NewGit(GitOptions{URL: upstream, Branch: "main", Path: "repo", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	ctx := context.Background()
	r1, err := g.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Message != "first" || len(r1.ID) != 7 {
		t.Errorf("rev = %+v", r1)
	}
	if data, _ := os.ReadFile(filepath.Join(r1.Dir, "servers/s.yaml")); string(data) != "x" {
		t.Errorf("snapshot content = %q", data)
	}
	commit("second", map[string]string{"repo/codi.yaml": "v: 2\n"})
	r2, err := g.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r2.SHA == r1.SHA || r2.Message != "second" {
		t.Errorf("second rev = %+v", r2)
	}
	if data, _ := os.ReadFile(filepath.Join(r1.Dir, "codi.yaml")); string(data) != "v: 1\n" {
		t.Errorf("first snapshot changed: %q", data)
	}
}

func TestLocalWatchesNestedNewDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "codi.yaml"), "a: 1\n")
	l, err := NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// mkdir -p in one burst, then wait for the debounced notification.
	if err := os.MkdirAll(filepath.Join(dir, "portal", "dist", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	drain := func(d time.Duration) bool {
		select {
		case <-l.Changes():
			return true
		case <-time.After(d):
			return false
		}
	}
	drain(2 * time.Second)
	time.Sleep(300 * time.Millisecond)
	drain(50 * time.Millisecond)
	// An edit deep inside the new tree must trigger a change.
	writeFile(t, filepath.Join(dir, "portal", "dist", "assets", "app.js"), "x")
	if !drain(3 * time.Second) {
		t.Fatal("no change notification for a file in a nested new directory")
	}
}
