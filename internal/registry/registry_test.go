package registry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/config"
)

// defaultOfficialURLs is OfficialURLs as built, before TestMain resets it.
var defaultOfficialURLs = OfficialURLs

// TestMain keeps the tests off the network: none may fetch the real official registry
// (tests that need an official registry set OfficialURLs and OfficialKeys themselves).
func TestMain(m *testing.M) {
	OfficialURLs = nil
	os.Exit(m.Run())
}

func initRepo(t *testing.T, files map[string]string, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for p, c := range files {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
		wt.Add(p)
	}
	h, err := wt.Commit("release", &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}})
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

const manifest = `apiVersion: cicord.dev/v1alpha1
kind: Plugin
metadata:
  name: hello
  version: 1.2.0
spec:
  engine: ">=0.1.0"
  entry: ./src/index.ts
  capabilities: [discord.messages.send]
`

func TestResolveAndLock(t *testing.T) {
	plugin := initRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": "export default {}"}, "v1.1.0", "v1.2.0", "v2.0.0")
	reg := initRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n    verified: true\n"})
	regs := []config.Registry{{Name: "cicord", URL: reg}}
	cache := t.TempDir()
	ctx := context.Background()

	res, err := ResolveLatest(ctx, regs, cache, "cicord/hello", "^1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Entry.Version != "1.2.0" || !strings.HasPrefix(res.Entry.Integrity, "sha256-") || len(res.Entry.Commit) != 40 {
		t.Fatalf("resolution = %+v", res.Entry)
	}
	// "verified" is ignored: this registry is not the signed official one.
	if res.Entry.Capabilities[0] != "discord.messages.send" || res.Index.Verified {
		t.Errorf("capabilities/index = %+v %+v", res.Entry, res.Index)
	}

	root := t.TempDir()
	lock := &Lock{Plugins: map[string]LockEntry{"cicord/hello": res.Entry}}
	if err := WriteLock(root, lock); err != nil {
		t.Fatal(err)
	}

	// Resolve from the lock with an empty cache: downloads and verifies integrity.
	r := NewResolver(root, t.TempDir())
	dir, key, err := r.Resolve("cicord/hello", "^1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if key != "cicord/hello@1.2.0" {
		t.Errorf("key = %s", key)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml")); err != nil {
		t.Errorf("not materialized: %v", err)
	}
	if _, _, err := NewResolver(root, t.TempDir()).Resolve("cicord/hello", "^2.0.0"); err == nil {
		t.Errorf("range mismatch with the lock should fail")
	}
	if _, _, err := NewResolver(root, t.TempDir()).Resolve("cicord/other", "*"); err == nil {
		t.Errorf("missing lock entry should fail")
	}

	// Tampered integrity is rejected.
	bad := res.Entry
	bad.Integrity = "sha256-AAAA"
	if _, err := Materialize(ctx, t.TempDir(), "cicord/hello", bad); err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Errorf("expected integrity mismatch, got %v", err)
	}
}
