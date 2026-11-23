package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/secrets"
	"github.com/npjonath/codi/internal/state/sqlite"
	"github.com/npjonath/codi/internal/store"
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
	e, _ := startStoreRepo(t)
	return e
}

// startStoreRepo is startStore, also returning the plugin repository (to release versions).
func startStoreRepo(t *testing.T) (*env, string) {
	t.Helper()
	return startStoreRepoWith(t, nil)
}

// startStoreRepoWith is startStoreRepo with another hook on the repository.
func startStoreRepoWith(t *testing.T, prepare func(dir string)) (*env, string) {
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
		if prepare != nil {
			prepare(dir)
		}
	}, func(o *runtime.Options) {
		o.Resolver = func(root string) config.PluginResolver { return registry.NewResolver(root, cache) }
	}, nil), plugin
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
	if res.Instance != "hello" || res.Mode != "commit" || res.Health != "Healthy" || res.Command != "hello" {
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

	// Uninstall: the instance and its codi.lock entry go; the registry can then be removed.
	var un struct {
		Removed bool
		Cleaned []string
		Mode    string
	}
	c.expect(200, "DELETE", "/api/v1/servers/"+serverKey+"/plugins/moderator/hello", nil).json(t, &un)
	if !un.Removed || un.Mode != "commit" || len(un.Cleaned) != 1 || un.Cleaned[0] != "codi.lock: acme/hello" {
		t.Fatalf("uninstall = %+v", un)
	}
	bot, _ = os.ReadFile(filepath.Join(e.dir, "bots", "moderator", "bot.yaml"))
	if strings.Contains(string(bot), "acme/hello") {
		t.Errorf("bot.yaml still has the instance:\n%s", bot)
	}
	if lock, _ := registry.ReadLock(e.dir); len(lock.Plugins) != 0 {
		t.Errorf("codi.lock = %+v", lock.Plugins)
	}
	e.waitFor("instance gone", func() bool { return len(e.rt.Instances(serverKey)) > 0 && !hasInstance(e, "hello") })
	c.expect(404, "DELETE", "/api/v1/servers/"+serverKey+"/plugins/moderator/hello", nil)
	c.expect(200, "DELETE", "/api/v1/registries/acme", nil)
}

func hasInstance(e *env, name string) bool {
	for _, i := range e.rt.Instances(serverKey) {
		if i.Config.Name == name {
			return true
		}
	}
	return false
}

func TestUninstallCopiedPlugin(t *testing.T) {
	e := start(t)
	c := e.client()
	c.login(e.seed.Owner.ID)
	mod := e.client()
	mod.login(e.moderator().ID)
	if r := mod.do("DELETE", "/api/v1/servers/"+serverKey+"/plugins/moderator/welcome", nil); r.status != 403 {
		t.Fatalf("moderator uninstall: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "plugins", "welcome", "plugin.yaml")); err != nil {
		t.Fatal(err)
	}
	var un struct {
		Removed bool
		Cleaned []string
		Mode    string
	}
	r := c.do("DELETE", "/api/v1/servers/"+serverKey+"/plugins/moderator/welcome", nil)
	if r.status != 200 {
		t.Fatalf("uninstall: %d %s", r.status, r.body)
	}
	r.json(t, &un)
	if !un.Removed || len(un.Cleaned) != 1 || un.Cleaned[0] != "plugins/welcome/" {
		t.Fatalf("uninstall = %+v", un)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "plugins", "welcome", "plugin.yaml")); !os.IsNotExist(err) {
		t.Errorf("plugin directory not deleted: %v", err)
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
		{"name": "internal", "url": "https://127.0.0.1/registry.git"},
		{"name": "metadata", "url": "https://169.254.169.254/latest"},
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

// Private registries: the token goes to the secret store, codi.yaml only names it, the
// index fetch carries it for the registry host, and it can be replaced or removed.
func TestPrivateRegistryTokens(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sec, err := secrets.Open(t.Context(), db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	const regURL = "https://8.8.8.8/private/registry.git"
	var mu sync.Mutex
	var sawAuth []bool
	ps := &store.Service{
		Secret: secrets.Resolver(sec, func(string) (string, bool) { return "", false }),
		Fetch: func(ctx context.Context, url string) (*registry.Index, error) {
			mu.Lock()
			defer mu.Unlock()
			auth := registry.AuthFor(ctx, url)
			sawAuth = append(sawAuth, auth != nil)
			if auth == nil {
				return nil, errors.New("clone: authentication required")
			}
			return &registry.Index{Plugins: map[string]registry.IndexEntry{"xp": {Repo: "https://8.8.8.8/private/xp.git"}}}, nil
		},
	}
	e := startFull(t, nil, nil, func(o *Options) {
		o.SecretStore, o.Secret, o.PluginStore = sec, ps.Secret, ps
	})
	c := e.client()
	c.login(e.seed.Owner.ID)

	// Without a token the private registry cannot be read: nothing is written.
	if r := c.do("POST", "/api/v1/registries", map[string]string{"name": "corp", "url": regURL}); r.status != 422 {
		t.Fatalf("add without token: %d %s", r.status, r.body)
	}
	c.expect(200, "POST", "/api/v1/registries", map[string]string{"name": "corp", "url": regURL, "token": "t0k3n"})
	data, _ := os.ReadFile(filepath.Join(e.dir, "codi.yaml"))
	if !strings.Contains(string(data), "secretRef: CODI_REGISTRY_CORP_TOKEN") || strings.Contains(string(data), "t0k3n") {
		t.Fatalf("codi.yaml:\n%s", data)
	}
	if v, err := sec.Get(t.Context(), "CODI_REGISTRY_CORP_TOKEN"); err != nil || v != "t0k3n" {
		t.Fatalf("secret = %q, %v", v, err)
	}
	e.waitFor("registry active", func() bool { r := e.rt.Repo(); return r != nil && len(r.Project.Spec.Registries) == 1 })

	var list struct {
		Sources []struct {
			Name, Credentials string
			Private           bool
			Error             any
		}
	}
	r := c.expect(200, "POST", "/api/v1/store/refresh", nil)
	r.json(t, &list)
	if len(list.Sources) != 2 || !list.Sources[1].Private || list.Sources[1].Credentials != "ok" || list.Sources[1].Error != nil {
		t.Fatalf("sources = %+v", list.Sources)
	}
	if strings.Contains(string(r.body), "t0k3n") {
		t.Fatal("the token leaked into the Store listing")
	}

	// Replace the token (no commit: codi.yaml already names the secret), then remove it.
	c.expect(200, "PUT", "/api/v1/registries/corp/token", map[string]string{"token": "n3w"})
	if v, _ := sec.Get(t.Context(), "CODI_REGISTRY_CORP_TOKEN"); v != "n3w" {
		t.Errorf("token not replaced: %q", v)
	}
	c.expect(200, "DELETE", "/api/v1/registries/corp/token", nil)
	if sec.Has(t.Context(), "CODI_REGISTRY_CORP_TOKEN") {
		t.Error("token not deleted")
	}
	data, _ = os.ReadFile(filepath.Join(e.dir, "codi.yaml"))
	if strings.Contains(string(data), "secretRef") {
		t.Errorf("secretRef left in codi.yaml:\n%s", data)
	}
	mod := e.client()
	mod.login(e.moderator().ID)
	if r := mod.do("PUT", "/api/v1/registries/corp/token", map[string]string{"token": "x"}); r.status != 403 {
		t.Errorf("moderator token: %d", r.status)
	}
}

func TestPaginatedTables(t *testing.T) {
	e := startStore(t)
	c := e.client()
	c.login(e.seed.Owner.ID)

	type pageDTO struct {
		Items []struct {
			Name, Source, Trust, Instance string
			Plugin                        struct{ Name string }
		}
		Total, Offset, Limit int
		Sort, Order          string
	}
	var p pageDTO
	c.expect(200, "GET", "/api/v1/store/plugins?limit=2&sort=name&order=desc", nil).json(t, &p)
	if p.Total != 7 || len(p.Items) != 2 || p.Limit != 2 || p.Order != "desc" || p.Items[0].Name != "welcome" {
		t.Fatalf("page = %+v", p)
	}
	c.expect(200, "GET", "/api/v1/store/plugins?trust=unofficial", nil).json(t, &p)
	if p.Total != 1 || p.Items[0].Source != "acme" || p.Limit != 50 {
		t.Fatalf("unofficial = %+v", p)
	}
	c.expect(200, "GET", "/api/v1/store/plugins?q=greet&offset=0", nil).json(t, &p)
	for _, it := range p.Items {
		if it.Name != "hello" && it.Name != "welcome" {
			t.Errorf("q=greet returned %s", it.Name)
		}
	}
	c.expect(200, "GET", "/api/v1/store/plugins?installed=true", nil).json(t, &p)
	if p.Total != 2 || p.Items[0].Name != "onboarding" || p.Items[1].Name != "welcome" {
		t.Errorf("installed = %+v", p)
	}
	for _, bad := range []string{"sort=nope", "order=up", "limit=0", "offset=-1"} {
		c.expect(400, "GET", "/api/v1/store/plugins?"+bad, nil)
	}
	var sum struct {
		Sources []struct {
			Name        string
			PluginCount int
			Plugins     []any
		}
	}
	c.expect(200, "GET", "/api/v1/store?summary=1", nil).json(t, &sum)
	if len(sum.Sources) != 2 || sum.Sources[0].PluginCount != 6 || sum.Sources[0].Plugins != nil {
		t.Errorf("summary = %+v", sum)
	}

	// Installed instances: paginated, with their origin.
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins?limit=1&sort=instance", nil).json(t, &p)
	if p.Total < 1 || len(p.Items) != 1 || p.Items[0].Source != "codi" || p.Items[0].Trust != "official" {
		t.Fatalf("instances = %+v", p)
	}
	var one struct {
		Instance, Bot, Source string
	}
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins/moderator/welcome", nil).json(t, &one)
	if one.Instance != "welcome" || one.Source != "codi" {
		t.Errorf("instance = %+v", one)
	}
	c.expect(404, "GET", "/api/v1/servers/"+serverKey+"/plugins/moderator/nope", nil)
}
