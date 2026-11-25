package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/registry"
)

// TestMain keeps the tests off the network: none may fetch the real official registry
// (tests that need an official registry set registry.OfficialURLs themselves).
func TestMain(m *testing.M) {
	registry.OfficialURLs = nil
	os.Exit(m.Run())
}

// release commits files to a plugin repository and tags the commit.
func release(t *testing.T, dir string, files map[string]string, tag string) {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for p, c := range files {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	h, err := wt.Commit("release "+tag, &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateTag(tag, h, nil); err != nil {
		t.Fatal(err)
	}
}

// helloEntry is the bot.yaml entry of the hello instance (the last plugin of the bot).
func helloEntry(t *testing.T, e *env) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dir, "bots", "moderator", "bot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, entry, ok := strings.Cut(string(data), "- name: hello")
	if !ok {
		t.Fatalf("no hello instance in bot.yaml:\n%s", data)
	}
	return entry
}

type updateInfoDTO struct {
	Kind, Current, Latest, Level string
	Modified                     bool
}

type updateReviewTestDTO struct {
	Available bool
	Update    *updateInfoDTO
	Detail    storeDetailDTO
	Current   struct {
		Version      string
		Commit       string
		Capabilities []string
	}
	Diff struct {
		Capabilities struct{ Added, Removed []string }
	}
	Instances []struct {
		Server, Bot, Instance, Range, NewRange string
		GrantsAdded, GrantsRemoved             []string
		Diagnostics                            []map[string]any
	}
	Files *struct {
		Changed, Added, Removed []string
	}
	Modified     bool
	ConfigValid  bool
	MissingAdmin []string
	Servers      []string
}

type updateResultDTO struct {
	Version, Mode, Revision string
	Instances               []struct{ Server, Bot, Instance, Health string }
}

func TestStoreUpdateRegistryPlugin(t *testing.T) {
	e, pluginDir := startStoreRepo(t)
	c := e.client()
	c.login(e.seed.Owner.ID)

	var d storeDetailDTO
	c.expect(200, "GET", "/api/v1/store/acme/hello", nil).json(t, &d)
	c.expect(200, "POST", "/api/v1/servers/"+serverKey+"/store/install", map[string]any{"source": "acme", "plugin": "hello",
		"version": d.Version, "commit": d.Commit, "integrity": d.Integrity, "confirmUnofficial": true})
	c.expect(404, "GET", "/api/v1/store/cicord/tickets/update", nil) // not installed

	// No update yet.
	var rv updateReviewTestDTO
	c.expect(200, "GET", "/api/v1/store/acme/hello/update", nil).json(t, &rv)
	if rv.Available || rv.Update != nil {
		t.Fatalf("review before a new release = %+v", rv)
	}

	// v1.1.0 requests one more capability.
	release(t, pluginDir, map[string]string{"plugin.yaml": strings.Replace(
		strings.Replace(helloManifest, "version: 1.0.0", "version: 1.1.0", 1),
		"capabilities: [discord.messages.send]", "capabilities: [discord.messages.send, storage.kv]", 1)}, "v1.1.0")

	// Detected after a refresh, on the Store and on the instance.
	var list storeSourcesDTO
	r := c.expect(200, "POST", "/api/v1/store/refresh", nil)
	r.json(t, &list)
	if !strings.Contains(string(r.body), `"update":{"kind":"registry","current":"1.0.0","latest":"1.1.0","level":"minor"}`) {
		t.Fatalf("refresh does not report the update: %s", r.body)
	}
	var pg struct {
		Items []struct {
			Name   string
			Update *updateInfoDTO
		}
		Total int
	}
	c.expect(200, "GET", "/api/v1/store/plugins?updates=true", nil).json(t, &pg)
	if pg.Total != 1 || pg.Items[0].Name != "hello" || pg.Items[0].Update.Latest != "1.1.0" {
		t.Fatalf("updates=true = %+v", pg)
	}
	var inst struct{ Update *updateInfoDTO }
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins/moderator/hello", nil).json(t, &inst)
	if inst.Update == nil || inst.Update.Current != "1.0.0" || inst.Update.Latest != "1.1.0" || inst.Update.Kind != "registry" {
		t.Fatalf("instance update = %+v", inst.Update)
	}

	// Review: the new capability is the diff, granted to the instance; the range stays.
	c.expect(200, "GET", "/api/v1/store/acme/hello/update", nil).json(t, &rv)
	if !rv.Available || rv.Detail.Version != "1.1.0" || rv.Current.Version != "1.0.0" || !rv.ConfigValid || len(rv.MissingAdmin) != 0 {
		t.Fatalf("review = %+v", rv)
	}
	if got := rv.Diff.Capabilities.Added; len(got) != 1 || got[0] != "storage.kv" || len(rv.Diff.Capabilities.Removed) != 0 {
		t.Fatalf("capability diff = %+v", rv.Diff.Capabilities)
	}
	if len(rv.Instances) != 1 || rv.Instances[0].Instance != "hello" || rv.Instances[0].Range != "^1.0.0" || rv.Instances[0].NewRange != "" ||
		len(rv.Instances[0].GrantsAdded) != 1 {
		t.Fatalf("instances = %+v", rv.Instances)
	}

	update := "/api/v1/store/acme/hello/update"
	req := map[string]any{"version": rv.Detail.Version, "commit": rv.Detail.Commit, "integrity": rv.Detail.Integrity}
	if r := c.do("POST", update, req); r.status != 409 || !strings.Contains(string(r.body), "confirmation_required") {
		t.Fatalf("update without confirmation: %d %s", r.status, r.body)
	}
	req["confirmUnofficial"] = true
	req["commit"] = d.Commit // the reviewed commit is not the latest
	if r := c.do("POST", update, req); r.status != 409 || !strings.Contains(string(r.body), `"changed"`) {
		t.Fatalf("update of another commit: %d %s", r.status, r.body)
	}
	req["commit"] = rv.Detail.Commit
	mod := e.client()
	mod.login(e.moderator().ID)
	if r := mod.do("POST", update, req); r.status != 403 {
		t.Fatalf("moderator update: %d %s", r.status, r.body)
	}

	var res updateResultDTO
	c.expect(200, "POST", update, req).json(t, &res)
	if res.Version != "1.1.0" || res.Mode != "commit" || len(res.Instances) != 1 || res.Instances[0].Health != "Healthy" {
		t.Fatalf("update = %+v", res)
	}
	lock, err := registry.ReadLock(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if le := lock.Plugins["acme/hello"]; le.Version != "1.1.0" || le.Commit != rv.Detail.Commit || le.Integrity != rv.Detail.Integrity {
		t.Errorf("cicord.lock = %+v", lock.Plugins)
	}
	bot := helloEntry(t, e)
	if !strings.Contains(bot, "storage.kv") || !strings.Contains(bot, "version: ^1.0.0") {
		t.Errorf("bot.yaml:\n%s", bot)
	}
	inst.Update = nil
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins/moderator/hello", nil).json(t, &inst)
	if inst.Update != nil {
		t.Errorf("still an update after updating: %+v", inst.Update)
	}

	// A new major version is outside ^1.0.0: the range is widened; storage.kv goes away.
	release(t, pluginDir, map[string]string{"plugin.yaml": strings.Replace(helloManifest, "version: 1.0.0", "version: 2.0.0", 1)}, "v2.0.0")
	c.expect(200, "POST", "/api/v1/store/refresh", nil)
	c.expect(200, "GET", "/api/v1/store/acme/hello/update", nil).json(t, &rv)
	if rv.Update == nil || rv.Update.Level != "major" || rv.Instances[0].NewRange != "^2.0.0" ||
		len(rv.Diff.Capabilities.Removed) != 1 || len(rv.Instances[0].GrantsRemoved) != 1 {
		t.Fatalf("major review = %+v", rv)
	}
	c.expect(200, "POST", update, map[string]any{"version": rv.Detail.Version, "commit": rv.Detail.Commit,
		"integrity": rv.Detail.Integrity, "confirmUnofficial": true}).json(t, &res)
	if res.Version != "2.0.0" || res.Instances[0].Health != "Healthy" {
		t.Fatalf("major update = %+v", res)
	}
	bot = helloEntry(t, e)
	if strings.Contains(bot, "storage.kv") || !strings.Contains(bot, "version: ^2.0.0") {
		t.Errorf("bot.yaml after the major update:\n%s", bot)
	}
	// Already the latest.
	if r := c.do("POST", update, map[string]any{"version": "2.0.0", "commit": rv.Detail.Commit, "integrity": rv.Detail.Integrity,
		"confirmUnofficial": true}); r.status != 409 || !strings.Contains(string(r.body), "up_to_date") {
		t.Fatalf("update when up to date: %d %s", r.status, r.body)
	}

	// v2.1.0 requires a config field the instance does not have: shown in the review,
	// refused with the diagnostics.
	release(t, pluginDir, map[string]string{
		"plugin.yaml":        strings.Replace(helloManifest, "version: 1.0.0", "version: 2.1.0", 1) + "  config:\n    schema: ./config.schema.json\n",
		"config.schema.json": `{"type": "object", "required": ["greeting"], "properties": {"greeting": {"type": "string"}}}`,
	}, "v2.1.0")
	c.expect(200, "POST", "/api/v1/store/refresh", nil)
	c.expect(200, "GET", "/api/v1/store/acme/hello/update", nil).json(t, &rv)
	if rv.Detail.Version != "2.1.0" || rv.ConfigValid || len(rv.Instances) != 1 || len(rv.Instances[0].Diagnostics) == 0 {
		t.Fatalf("review of an incompatible config = %+v", rv)
	}
	r = c.do("POST", update, map[string]any{"version": rv.Detail.Version, "commit": rv.Detail.Commit,
		"integrity": rv.Detail.Integrity, "confirmUnofficial": true})
	if r.status != 422 || !strings.Contains(string(r.body), `"invalid"`) || !strings.Contains(string(r.body), "diagnostics") {
		t.Fatalf("update with an invalid config: %d %s", r.status, r.body)
	}
	if lock, _ := registry.ReadLock(e.dir); lock.Plugins["acme/hello"].Version != "2.0.0" {
		t.Errorf("an invalid update was written: %+v", lock.Plugins)
	}
}

// A copy of an official plugin (plugins/welcome) older than the bundled version: detected,
// updated when unmodified, refused when modified unless forced.
func TestStoreUpdateBundledCopy(t *testing.T) {
	files, err := catalog.Official().PluginFiles("welcome")
	if err != nil {
		t.Fatal(err)
	}
	e := startFull(t, func(dir string) {
		p := filepath.Join(dir, "cicord.yaml")
		data, _ := os.ReadFile(p)
		_ = os.WriteFile(p, []byte(strings.Replace(string(data), "defaultMode: pullRequest", "defaultMode: commit", 1)), 0o644)
		pd := filepath.Join(dir, "plugins", "welcome")
		_ = os.RemoveAll(pd)
		for rel, data := range files {
			if rel == "plugin.yaml" {
				data = []byte(strings.Replace(string(data), "version: 0.1.1", "version: 0.0.9", 1))
			}
			full := filepath.Join(pd, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.WriteFile(full, data, 0o644)
		}
		// A file of the old version that the new one no longer ships.
		_ = os.WriteFile(filepath.Join(pd, "src", "old.ts"), []byte("export {};\n"), 0o644)
	}, nil, nil)
	c := e.client()
	c.login(e.seed.Owner.ID)

	var inst struct{ Update *updateInfoDTO }
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins/moderator/welcome", nil).json(t, &inst)
	if inst.Update == nil || inst.Update.Kind != "bundled" || inst.Update.Current != "0.0.9" || inst.Update.Latest != "0.1.1" || inst.Update.Level != "minor" {
		t.Fatalf("instance update = %+v", inst.Update)
	}
	// The extra file makes the copy differ from any released version: modified.
	var rv updateReviewTestDTO
	c.expect(200, "GET", "/api/v1/store/cicord/welcome/update", nil).json(t, &rv)
	if !rv.Available || !rv.Modified || rv.Files == nil || len(rv.Files.Removed) != 1 || rv.Files.Removed[0] != "src/old.ts" ||
		len(rv.Files.Changed) != 1 || rv.Files.Changed[0] != "plugin.yaml" {
		t.Fatalf("review = %+v", rv)
	}
	update := "/api/v1/store/cicord/welcome/update"
	if r := c.do("POST", update, map[string]any{"version": "0.0.1"}); r.status != 409 || !strings.Contains(string(r.body), `"changed"`) {
		t.Fatalf("update of another version: %d %s", r.status, r.body)
	}
	if r := c.do("POST", update, map[string]any{"version": "0.1.1"}); r.status != 409 || !strings.Contains(string(r.body), `"modified"`) {
		t.Fatalf("update of a modified copy: %d %s", r.status, r.body)
	}
	var res updateResultDTO
	c.expect(200, "POST", update, map[string]any{"version": "0.1.1", "integrity": rv.Detail.Integrity, "force": true}).json(t, &res)
	if res.Mode != "commit" || len(res.Instances) == 0 || res.Instances[0].Health != "Healthy" {
		t.Fatalf("update = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "plugins", "welcome", "src", "old.ts")); !os.IsNotExist(err) {
		t.Errorf("a file the new version does not ship was kept: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(e.dir, "plugins", "welcome", "plugin.yaml"))
	if !strings.Contains(string(data), "version: 0.1.1") {
		t.Errorf("plugin.yaml:\n%s", data)
	}
	inst.Update = nil
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins/moderator/welcome", nil).json(t, &inst)
	if inst.Update != nil {
		t.Errorf("still an update: %+v", inst.Update)
	}
}
