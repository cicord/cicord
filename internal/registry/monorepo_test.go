package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
)

// A monorepo registry: plugins in subdirectories (index `path`), tags <name>/v<semver>.
func TestMonorepoResolveLockAndUpdate(t *testing.T) {
	alpha := strings.Replace(manifest, "name: hello", "name: alpha", 1)
	beta := strings.Replace(manifest, "name: hello", "name: beta", 1)
	files := map[string]string{
		"README.md":                   "root files are not part of a plugin",
		"plugins/alpha/plugin.yaml":   alpha,
		"plugins/alpha/src/index.ts":  "export default {}",
		"plugins/beta/plugin.yaml":    beta,
		"plugins/beta/src/index.ts":   "export default {}",
		"plugins/beta/test/b.test.ts": "// tests",
	}
	mono := initRepo(t, files, "alpha/v1.0.0", "alpha/v1.1.0", "beta/v1.0.0", "beta/v2.0.0-rc.1", "v9.9.9", "alpha/not-semver")
	index := "plugins:\n" +
		"  alpha:\n    repo: " + mono + "\n    path: plugins/alpha\n" +
		"  beta:\n    repo: " + mono + "\n    path: plugins/beta\n" +
		"  escape:\n    repo: " + mono + "\n    path: ../etc\n" +
		"  file:\n    repo: " + mono + "\n    path: README.md\n" +
		"  whole:\n    repo: " + mono + "\n"
	reg := initRepo(t, map[string]string{"index.yaml": index})
	regs := []config.Registry{{Name: "acme", URL: reg}}
	ctx := context.Background()

	res, err := ResolveLatest(ctx, regs, t.TempDir(), "acme/alpha", "*")
	if err != nil {
		t.Fatal(err)
	}
	// The decoy bare tag v9.9.9 and the other plugin's tags are ignored.
	if res.Entry.Version != "1.1.0" || res.Entry.Path != "plugins/alpha" || res.Manifest.Metadata.Name != "alpha" {
		t.Fatalf("alpha = %+v", res.Entry)
	}
	// The integrity covers the plugin directory only, with paths relative to it: the same
	// plugin hashes the same in its own repository.
	want := Integrity(map[string][]byte{"plugin.yaml": []byte(alpha), "src/index.ts": []byte("export default {}")})
	if res.Entry.Integrity != want {
		t.Errorf("integrity = %s, want %s", res.Entry.Integrity, want)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "README.md")); err == nil {
		t.Error("files outside the plugin directory were exported")
	}
	b, err := ResolveLatest(ctx, regs, t.TempDir(), "acme/beta", "*")
	if err != nil || b.Entry.Version != "1.0.0" {
		t.Fatalf("beta = %+v, %v (the prerelease is not the latest)", b, err)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "test", "b.test.ts")); err != nil {
		t.Errorf("beta tests not exported: %v", err)
	}

	// An unpathed entry only sees bare tags (and needs plugin.yaml at the root).
	if _, err := ResolveLatest(ctx, regs, t.TempDir(), "acme/whole", "*"); err == nil || !strings.Contains(err.Error(), "acme/whole@9.9.9 has no plugin.yaml in the repository root") {
		t.Errorf("whole = %v", err)
	}
	for _, name := range []string{"escape", "file"} {
		if _, err := ResolveLatest(ctx, regs, t.TempDir(), "acme/"+name, "*"); err == nil {
			t.Errorf("%s: resolved", name)
		}
	}

	// From the lock with an empty cache: the same subtree, the same integrity.
	root := t.TempDir()
	if err := WriteLock(root, &Lock{Plugins: map[string]LockEntry{"acme/alpha": res.Entry}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, LockFile)); !strings.Contains(string(data), "path: plugins/alpha") {
		t.Errorf("cicord.lock:\n%s", data)
	}
	dir, _, err := NewResolver(root, t.TempDir()).Resolve("acme/alpha", "^1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml")); err != nil {
		t.Errorf("not materialized at the plugin root: %v", err)
	}
	bad := res.Entry
	bad.Path = "../plugins/alpha"
	if _, err := Materialize(ctx, t.TempDir(), "acme/alpha", bad); err == nil || !strings.Contains(err.Error(), "invalid plugin path") {
		t.Errorf("hostile lock path: %v", err)
	}
	bad.Path = "plugins/beta" // another directory of the same commit: another content
	if _, err := Materialize(ctx, t.TempDir(), "acme/alpha", bad); err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Errorf("other directory: %v", err)
	}

	// Latest versions per plugin of one repository (ls-remote).
	for prefix, wantV := range map[string]string{"alpha/": "1.1.0", "beta/": "1.0.0", "": "9.9.9"} {
		if v, err := LatestVersion(ctx, reg, mono, prefix); err != nil || v != wantV {
			t.Errorf("LatestVersion(%q) = %q, %v, want %s", prefix, v, err, wantV)
		}
	}
	if _, err := LatestVersion(ctx, reg, mono, "gamma/"); err == nil || !strings.Contains(err.Error(), "gamma/v<version>") {
		t.Errorf("no release of gamma: %v", err)
	}
}

func TestPluginPathAndTags(t *testing.T) {
	for _, p := range []string{"", "plugins/polls", "a", "x_1/y.z-2"} {
		if err := CheckPluginPath(p); err != nil {
			t.Errorf("CheckPluginPath(%q) = %v", p, err)
		}
	}
	for _, p := range []string{".", "..", "../x", "a/../b", "/abs", "a/", "a//b", `a\b`, "./a", "a/.git", ".github/x", "a b", strings.Repeat("a", 256)} {
		if err := CheckPluginPath(p); err == nil {
			t.Errorf("CheckPluginPath(%q) accepted", p)
		}
	}
	e := IndexEntry{Path: "plugins/polls"}
	if e.TagPrefix("polls") != "polls/" || (IndexEntry{}).TagPrefix("polls") != "" || ReleaseTag("polls/", "1.0.0") != "polls/v1.0.0" {
		t.Error("tag prefix")
	}
	tags := []string{"v3.0.0", "polls/v1.0.0", "polls/v1.2.0", "polls/v2.0.0-rc.1", "polls-extra/v5.0.0", "levels/v4.0.0", "polls/x/v9.0.0"}
	if got := HighestVersion(tags, "polls/", "*"); got != "1.2.0" {
		t.Errorf("polls = %q", got)
	}
	if got := HighestVersion(tags, "", "*"); got != "3.0.0" {
		t.Errorf("bare = %q", got)
	}
	if got := HighestVersion(tags, "polls/", "^2.0.0-0"); got != "2.0.0-rc.1" {
		t.Errorf("prerelease range = %q", got)
	}
}

// The official registry is the cicord-plugins monorepo; without an official key in the
// build, its index is not fetched at all (no network) and fails closed.
func TestOfficialRegistryWithoutKey(t *testing.T) {
	if len(defaultOfficialURLs) == 0 || defaultOfficialURLs[0] != "https://github.com/cicord/cicord-plugins" {
		t.Fatalf("OfficialURLs = %v", defaultOfficialURLs)
	}
	oldURLs, oldKeys, oldDev := OfficialURLs, OfficialKeys, devOfficialKey
	t.Cleanup(func() { OfficialURLs, OfficialKeys, devOfficialKey = oldURLs, oldKeys, oldDev })
	// An unreachable official URL: a fetch attempt would fail with another error.
	OfficialURLs, OfficialKeys, devOfficialKey = []string{"https://127.0.0.1:1/cicord-plugins"}, nil, ""
	if HasOfficialKeys() {
		t.Fatal("HasOfficialKeys")
	}
	_, err := FetchIndex(context.Background(), OfficialURLs[0])
	if !errors.Is(err, ErrNoOfficialKey) || !errors.Is(err, ErrBadSignature) {
		t.Fatalf("FetchIndex = %v", err)
	}
	if _, err := ResolveLatest(context.Background(), nil, t.TempDir(), "cicord/polls", "*"); !errors.Is(err, ErrNoOfficialKey) {
		t.Errorf("ResolveLatest = %v", err)
	}
}
