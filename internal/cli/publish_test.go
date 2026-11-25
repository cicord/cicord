package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

// gitTest runs git in dir with an isolated configuration (no user signing settings).
func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCmd(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func isolatedGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "dev")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "dev@example.org")
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitTest(t, dir, "init", "-q", "-b", "main")
	gitTest(t, dir, "add", "-A")
	gitTest(t, dir, "commit", "-q", "-m", "init")
}

func TestPluginPublish(t *testing.T) {
	isolatedGit(t)
	ctx := context.Background()
	plugin := t.TempDir()
	copyDir(t, "../../examples/basic/plugins/welcome", plugin)
	gitInit(t, plugin)
	remote := t.TempDir()
	gitTest(t, remote, "init", "-q", "--bare", "-b", "main")
	gitTest(t, plugin, "remote", "add", "origin", remote)
	gitTest(t, plugin, "push", "-q", "origin", "main")

	reg := t.TempDir()
	writeFile(t, filepath.Join(reg, "index.yaml"), "# Community plugins\nplugins:\n  other:\n    repo: https://example.org/other.git # keep me\n")
	writeFile(t, filepath.Join(reg, "index.yaml.sig"), "stale\n")
	gitInit(t, reg)

	// A dirty working tree is refused: the tag must point at what was validated.
	writeFile(t, filepath.Join(plugin, "notes.txt"), "wip")
	if code, _, errOut := run(t, "plugin", "publish", plugin); code == 0 || !strings.Contains(errOut, "uncommitted changes") {
		t.Errorf("dirty = %d %s", code, errOut)
	}
	os.Remove(filepath.Join(plugin, "notes.txt"))

	// --dry-run changes nothing.
	code, out, errOut := run(t, "plugin", "publish", plugin, "--dry-run", "--push", "--registry", reg)
	if code != 0 || !strings.Contains(out, "would tag v0.1.0") || !strings.Contains(out, "would add welcome") {
		t.Fatalf("dry-run = %d\n%s\n%s", code, out, errOut)
	}
	if tags := gitTest(t, plugin, "tag", "--list"); tags != "" {
		t.Errorf("dry-run created tags: %q", tags)
	}
	if !strings.Contains(readFile(t, filepath.Join(reg, "index.yaml")), "other") || strings.Contains(readFile(t, filepath.Join(reg, "index.yaml")), "welcome") {
		t.Error("dry-run wrote the index")
	}

	code, out, errOut = run(t, "plugin", "publish", plugin, "--push", "--registry", reg, "--publisher", "acme")
	if code != 0 {
		t.Fatalf("publish = %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"tagged v0.1.0", "pushed v0.1.0 to origin", "added welcome", "index.yaml.sig is now stale", "cicord registry sign"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "not an https:// URL") {
		t.Errorf("a local repo URL is flagged: %s", errOut)
	}
	if got := gitTest(t, remote, "tag", "--list"); got != "v0.1.0" {
		t.Errorf("remote tags = %q", got)
	}
	head := gitTest(t, plugin, "rev-parse", "HEAD")
	idxText := readFile(t, filepath.Join(reg, "index.yaml"))
	idx, err := registry.ParseIndex([]byte(idxText))
	if err != nil {
		t.Fatal(err)
	}
	e := idx.Plugins["welcome"]
	if e.Repo != remote || e.Version != "0.1.0" || e.Commit != head || e.Publisher != "acme" || e.Verified || e.DisplayName == "" {
		t.Errorf("entry = %+v", e)
	}
	if !strings.Contains(idxText, "# Community plugins") || !strings.Contains(idxText, "# keep me") {
		t.Errorf("comments lost:\n%s", idxText)
	}

	// The registry entry resolves to the tagged commit.
	gitTest(t, reg, "commit", "-q", "-am", "add welcome")
	res, err := registry.ResolveLatest(ctx, []config.Registry{{Name: "acme", URL: reg}}, t.TempDir(), "acme/welcome", "*")
	if err != nil || res.Entry.Version != "0.1.0" || res.Entry.Commit != head {
		t.Fatalf("resolve = %+v, %v", res, err)
	}

	// Same version again → refused; a lower version than an existing tag → refused.
	if code, _, errOut := run(t, "plugin", "publish", plugin); code == 0 || !strings.Contains(errOut, "tag v0.1.0 already exists") {
		t.Errorf("republish = %d %s", code, errOut)
	}
	gitTest(t, plugin, "tag", "v1.0.0")
	m := filepath.Join(plugin, "plugin.yaml")
	writeFile(t, m, strings.Replace(readFile(t, m), "version: 0.1.0", "version: 0.2.0", 1))
	gitTest(t, plugin, "commit", "-q", "-am", "0.2.0")
	if code, _, errOut := run(t, "plugin", "publish", plugin); code == 0 || !strings.Contains(errOut, "not greater than the existing tag v1.0.0") {
		t.Errorf("lower version = %d %s", code, errOut)
	}
	gitTest(t, plugin, "tag", "-d", "v1.0.0")

	// An existing entry keeps "verified" (set by maintainers) and its publisher; its repo
	// only changes with --replace-repo.
	writeFile(t, filepath.Join(reg, "index.yaml"), strings.Replace(idxText, "publisher: acme", "publisher: acme\n    verified: true", 1))
	if code, _, errOut := run(t, "plugin", "publish", plugin, "--registry", reg, "--repo", "https://example.org/fork.git"); code == 0 || !strings.Contains(errOut, "--replace-repo") {
		t.Errorf("repo change = %d %s", code, errOut)
	}
	if tags := gitTest(t, plugin, "tag", "--list"); tags != "v0.1.0" {
		t.Errorf("a refused publish must not tag: %q", tags)
	}
	if code, out, errOut := run(t, "plugin", "publish", plugin, "--registry", reg); code != 0 || !strings.Contains(out, "updated welcome") {
		t.Fatalf("update = %d\n%s\n%s", code, out, errOut)
	}
	idx, _ = registry.ParseIndex([]byte(readFile(t, filepath.Join(reg, "index.yaml"))))
	if e := idx.Plugins["welcome"]; e.Version != "0.2.0" || !e.Verified || e.Publisher != "acme" {
		t.Errorf("updated entry = %+v", e)
	}

	// Without --registry, the entry is printed.
	writeFile(t, m, strings.Replace(readFile(t, m), "version: 0.2.0", "version: 0.3.0", 1))
	gitTest(t, plugin, "commit", "-q", "-am", "0.3.0")
	code, out, _ = run(t, "plugin", "publish", plugin, "--repo", "https://example.org/welcome.git")
	if code != 0 || !strings.Contains(out, "repo: https://example.org/welcome.git") || !strings.Contains(out, "version: 0.3.0") {
		t.Errorf("print = %d\n%s", code, out)
	}
}

// The new version must be greater than every earlier release tag by semver 2.0 precedence:
// a release follows its pre-releases, rc.10 follows rc.2, build metadata does not count.
func TestOlderTag(t *testing.T) {
	vs := func(s ...string) []*semver.Version {
		var out []*semver.Version
		for _, x := range s {
			out = append(out, semver.MustParse(x))
		}
		return out
	}
	for _, c := range []struct {
		ver      string
		earlier  []*semver.Version
		conflict string
	}{
		{"1.0.0", vs("0.9.0", "1.0.0-rc.1", "1.0.0-rc.2"), ""},
		{"1.0.0-rc.10", vs("1.0.0-rc.2", "1.0.0-rc.9"), ""},
		{"1.0.0-rc.2", vs("1.0.0-rc.10"), "1.0.0-rc.10"},
		{"1.0.0-rc.3", vs("1.0.0"), "1.0.0"},
		{"1.0.0+build.2", vs("1.0.0+build.1"), "1.0.0+build.1"},
		{"1.1.0-rc.1", vs("1.0.0"), ""},
	} {
		got := notOlderTag(semver.MustParse(c.ver), c.earlier)
		if (got == nil && c.conflict != "") || (got != nil && got.Original() != c.conflict) {
			t.Errorf("notOlderTag(%s) = %v, want %q", c.ver, got, c.conflict)
		}
	}
}

func TestPublicRepoURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/xp.git":          "https://github.com/acme/xp.git",
		"ssh://git@github.com:22/acme/xp.git": "https://github.com/acme/xp.git",
		"ssh://gitlab.example/acme/xp":        "https://gitlab.example/acme/xp",
		"https://github.com/acme/xp.git":      "https://github.com/acme/xp.git",
		"/srv/git/xp":                         "/srv/git/xp",
	} {
		got, err := publicRepoURL(in)
		if err != nil || got != want {
			t.Errorf("publicRepoURL(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"https://x-access-token:ghp_x@github.com/acme/xp.git", "https://tok@github.com/acme/xp"} {
		if _, err := publicRepoURL(bad); err == nil {
			t.Errorf("credentials accepted in %q", bad)
		}
	}
}

// A repository with several plugins that is also their registry (the official layout):
// plugins/<name>/, index.yaml at the root, tags <name>/v<version>.
func TestPluginPublishMonorepo(t *testing.T) {
	isolatedGit(t)
	ctx := context.Background()
	mono := t.TempDir()
	copyDir(t, "../../examples/basic/plugins/welcome", filepath.Join(mono, "plugins", "welcome"))
	copyDir(t, "../../examples/basic/plugins/onboarding", filepath.Join(mono, "plugins", "onboarding"))
	writeFile(t, filepath.Join(mono, "index.yaml"), "plugins: {}\n")
	writeFile(t, filepath.Join(mono, "README.md"), "official plugins\n")
	gitInit(t, mono)
	gitTest(t, mono, "tag", "v5.0.0") // a bare tag is no release of the plugins
	remote := t.TempDir()
	gitTest(t, remote, "init", "-q", "--bare", "-b", "main")
	gitTest(t, mono, "remote", "add", "origin", remote)
	gitTest(t, mono, "push", "-q", "origin", "main")

	// A change elsewhere in the repository does not block a plugin; one inside it does.
	writeFile(t, filepath.Join(mono, "README.md"), "edited\n")
	writeFile(t, filepath.Join(mono, "plugins", "onboarding", "notes.txt"), "wip")
	code, out, errOut := run(t, "plugin", "publish", filepath.Join(mono, "plugins", "welcome"), "--push", "--registry", mono, "--publisher", "Cicord")
	if code != 0 {
		t.Fatalf("publish welcome = %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "tagged welcome/v0.1.0") || !strings.Contains(out, "added welcome") {
		t.Errorf("output:\n%s", out)
	}
	if code, _, errOut := run(t, "plugin", "publish", mono, "--path", "plugins/onboarding", "--registry", mono); code == 0 || !strings.Contains(errOut, "uncommitted changes") {
		t.Errorf("dirty plugin directory = %d %s", code, errOut)
	}
	os.Remove(filepath.Join(mono, "plugins", "onboarding", "notes.txt"))
	// --path from the repository root; the uncommitted index.yaml (previous publish) is
	// outside the plugin directory.
	if code, out, errOut := run(t, "plugin", "publish", mono, "--path", "plugins/onboarding", "--push", "--registry", mono); code != 0 || !strings.Contains(out, "tagged onboarding/v0.1.0") {
		t.Fatalf("publish onboarding = %d\n%s\n%s", code, out, errOut)
	}
	if code, _, errOut := run(t, "plugin", "publish", filepath.Join(mono, "plugins", "welcome")); code == 0 || !strings.Contains(errOut, "tag welcome/v0.1.0 already exists") {
		t.Errorf("republish = %d %s", code, errOut)
	}
	if code, _, errOut := run(t, "plugin", "publish", mono, "--path", "../welcome"); code == 0 || !strings.Contains(errOut, "invalid plugin path") {
		t.Errorf("--path ../ = %d %s", code, errOut)
	}
	if got := gitTest(t, remote, "tag", "--list"); got != "onboarding/v0.1.0\nwelcome/v0.1.0" {
		t.Errorf("remote tags = %q", got)
	}
	idx, err := registry.ParseIndex([]byte(readFile(t, filepath.Join(mono, "index.yaml"))))
	if err != nil {
		t.Fatal(err)
	}
	head := gitTest(t, mono, "rev-parse", "HEAD")
	for name, e := range idx.Plugins {
		if e.Repo != remote || e.Path != "plugins/"+name || e.Version != "0.1.0" || e.Commit != head {
			t.Errorf("%s = %+v", name, e)
		}
	}
	if len(idx.Plugins) != 2 || idx.Plugins["welcome"].Publisher != "Cicord" {
		t.Errorf("index = %+v", idx.Plugins)
	}

	// The registry (this same repository) resolves each plugin from its directory and tags.
	gitTest(t, mono, "commit", "-q", "-am", "publish")
	for _, name := range []string{"welcome", "onboarding"} {
		res, err := registry.ResolveLatest(ctx, []config.Registry{{Name: "acme", URL: mono}}, t.TempDir(), "acme/"+name, "*")
		if err != nil || res.Entry.Version != "0.1.0" || res.Entry.Path != "plugins/"+name || res.Manifest.Metadata.Name != name {
			t.Fatalf("resolve %s = %+v, %v", name, res, err)
		}
	}
}

// An update keeps the index's spelling of the same repository (publish reads the remote
// URL, often with .git): no noise in the index diff.
func TestPlanIndexUpdateKeepsRepoSpelling(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.yaml"), `plugins:
  xp:
    repo: https://github.com/acme/xp
    path: plugins/xp
    version: 0.1.0
    commit: 1111111111111111111111111111111111111111
`)
	plan, err := planIndexUpdate(dir, "xp", registry.IndexEntry{Repo: "https://github.com/acme/xp.git", Path: "plugins/xp",
		Version: "0.2.0", Commit: "2222222222222222222222222222222222222222"}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(plan.content)
	if !strings.Contains(got, "repo: https://github.com/acme/xp\n") || strings.Contains(got, "xp.git") || !strings.Contains(got, "version: 0.2.0") {
		t.Errorf("index =\n%s", got)
	}
}
