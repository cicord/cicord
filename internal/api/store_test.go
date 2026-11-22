package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/runtime"
)

func gitRepo(t *testing.T, files map[string]string, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(p); err != nil {
			t.Fatal(err)
		}
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

const helloManifest = `apiVersion: codi.dev/v1alpha1
kind: Plugin
metadata:
  name: hello
  version: 1.0.0
  displayName: Hello
  description: Says hello
spec:
  engine: ">=0.1.0"
  entry: ./src/index.ts
  capabilities: [discord.messages.send]
`

const helloSource = `import { command, definePlugin } from "@codi/sdk";

export default definePlugin({
  commands: {
    hello: command({
      description: "Say hello",
      async run(_ctx, interaction) {
        await interaction.reply({ content: "hello" });
      },
    }),
  },
});
`

// startStore deploys examples/basic with an unofficial registry "acme" (a local git
// repository listing the plugin "hello") and plugin write-backs in commit mode.
func startStore(t *testing.T) *env {
	t.Helper()
	t.Setenv("CODI_CACHE_DIR", t.TempDir())
	plugin := gitRepo(t, map[string]string{"plugin.yaml": helloManifest, "src/index.ts": helloSource}, "v1.0.0")
	reg := gitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n    publisher: acme\n    verified: true\n    keywords: [greetings]\n"})
	cache := registry.DefaultCacheDir()
	return startFull(t, func(dir string) {
		p := filepath.Join(dir, "codi.yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(data), "defaultMode: pullRequest", "defaultMode: commit", 1)
		s += "  registries:\n    - name: acme\n      url: " + reg + "\n"
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}, func(o *runtime.Options) {
		o.Resolver = func(root string) config.PluginResolver { return registry.NewResolver(root, cache) }
	}, nil)
}

type storeSourcesDTO struct {
	Sources []struct {
		Name    string                    `json:"name"`
		Kind    string                    `json:"kind"`
		Trust   string                    `json:"trust"`
		Error   *struct{ Message string } `json:"error"`
		Plugins []struct {
			Name      string           `json:"name"`
			Verified  bool             `json:"verified"`
			Trust     string           `json:"trust"`
			Installed []map[string]any `json:"installed"`
		} `json:"plugins"`
	} `json:"sources"`
}

type storeDetailDTO struct {
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Commit       string           `json:"commit"`
	Integrity    string           `json:"integrity"`
	Trust        string           `json:"trust"`
	Capabilities []string         `json:"capabilities"`
	Commands     []map[string]any `json:"commands"`
}

func TestStoreBrowseAndInstallFromRegistry(t *testing.T) {
	e := startStore(t)
	c := e.client()
	c.login(e.seed.Owner.ID)

	var list storeSourcesDTO
	c.expect(200, "GET", "/api/v1/store", nil).json(t, &list)
	if len(list.Sources) != 2 || list.Sources[0].Name != "codi" || list.Sources[0].Trust != "official" || list.Sources[0].Kind != "bundled" {
		t.Fatalf("sources = %+v", list.Sources)
	}
	acme := list.Sources[1]
	if acme.Name != "acme" || acme.Trust != "unofficial" || acme.Error != nil || len(acme.Plugins) != 1 || acme.Plugins[0].Name != "hello" {
		t.Fatalf("acme = %+v", acme)
	}
	if acme.Plugins[0].Verified {
		t.Error("an unofficial registry cannot mark its plugins verified")
	}

	var d storeDetailDTO
	c.expect(200, "GET", "/api/v1/store/acme/hello", nil).json(t, &d)
	if d.Version != "1.0.0" || d.Commit == "" || !strings.HasPrefix(d.Integrity, "sha256-") || d.Trust != "unofficial" {
		t.Fatalf("detail = %+v", d)
	}
	if len(d.Capabilities) != 1 || d.Capabilities[0] != "discord.messages.send" {
		t.Errorf("capabilities = %v", d.Capabilities)
	}
	if d.Commands != nil {
		t.Errorf("unofficial plugin code must not be evaluated before install: commands = %v", d.Commands)
	}
	c.expect(404, "GET", "/api/v1/store/acme/nope", nil)
	c.expect(404, "GET", "/api/v1/store/nope/hello", nil)

	install := "/api/v1/servers/" + serverKey + "/store/install"
	req := map[string]any{"source": "acme", "plugin": "hello", "version": d.Version, "commit": d.Commit, "integrity": d.Integrity}
	if r := c.do("POST", install, req); r.status != 409 || !strings.Contains(string(r.body), "confirmation_required") {
		t.Fatalf("install without confirmation: %d %s", r.status, r.body)
	}
	req["confirmUnofficial"] = true
	req["commit"] = strings.Repeat("0", 40)
	if r := c.do("POST", install, req); r.status != 409 || !strings.Contains(string(r.body), `"changed"`) {
		t.Fatalf("install of another commit: %d %s", r.status, r.body)
	}
	req["commit"] = d.Commit
	var res struct {
		Instance, Mode, Health, Command string
	}
	c.expect(200, "POST", install, req).json(t, &res)
	if res.Instance != "hello" || res.Mode != "commit" || res.Health != "Healthy" {
		t.Fatalf("install = %+v", res)
	}

	bot, err := os.ReadFile(filepath.Join(e.dir, "bots", "moderator", "bot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bot), "source: acme/hello") || !strings.Contains(string(bot), "version: ^1.0.0") {
		t.Errorf("bot.yaml:\n%s", bot)
	}
	lock, err := registry.ReadLock(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if le := lock.Plugins["acme/hello"]; le.Commit != d.Commit || le.Integrity != d.Integrity {
		t.Errorf("codi.lock = %+v", lock.Plugins)
	}

	// Installed instances show up in the Store; a used registry cannot be removed.
	c.expect(200, "GET", "/api/v1/store", nil).json(t, &list)
	if got := list.Sources[1].Plugins[0].Installed; len(got) != 1 || got[0]["instance"] != "hello" {
		t.Errorf("installed = %v", got)
	}
	if r := c.do("DELETE", "/api/v1/registries/acme", nil); r.status != 409 || !strings.Contains(string(r.body), "in_use") {
		t.Fatalf("remove used registry: %d %s", r.status, r.body)
	}
	// Installing it twice under the same instance name conflicts.
	if r := c.do("POST", install, req); r.status != 409 {
		t.Fatalf("second install: %d %s", r.status, r.body)
	}
}

func TestStoreRegistryManagement(t *testing.T) {
	e := startStore(t)
	owner := e.client()
	owner.login(e.seed.Owner.ID)

	for _, body := range []map[string]string{
		{"name": "codi", "url": "https://example.org/r.git"},
		{"name": "Bad Name", "url": "https://example.org/r.git"},
		{"name": "local", "url": "file:///etc"},
		{"name": "local", "url": "/tmp/registry"},
		{"name": "acme", "url": "https://example.org/other.git"},
	} {
		if r := owner.do("POST", "/api/v1/registries", body); r.status != 400 {
			t.Errorf("add %v: %d %s", body, r.status, r.body)
		}
	}

	// Moderators are not project admins.
	mod := e.client()
	mod.login(e.moderator().ID)
	if r := mod.do("POST", "/api/v1/registries", map[string]string{"name": "x", "url": "https://example.org/x.git"}); r.status != 403 {
		t.Errorf("moderator add: %d %s", r.status, r.body)
	}
	if r := mod.do("GET", "/api/v1/store", nil); r.status != 403 {
		t.Errorf("moderator browse: %d %s", r.status, r.body)
	}

	// Removing an unused registry rewrites codi.yaml.
	var wr struct{ Mode string }
	owner.expect(200, "DELETE", "/api/v1/registries/acme", nil).json(t, &wr)
	if wr.Mode != "commit" {
		t.Errorf("mode = %q", wr.Mode)
	}
	data, _ := os.ReadFile(filepath.Join(e.dir, "codi.yaml"))
	if strings.Contains(string(data), "name: acme") {
		t.Errorf("codi.yaml still lists acme:\n%s", data)
	}
	e.waitFor("registry removed", func() bool {
		r := e.rt.Repo()
		return r != nil && len(r.Project.Spec.Registries) == 0
	})
	owner.expect(404, "DELETE", "/api/v1/registries/acme", nil)
}
