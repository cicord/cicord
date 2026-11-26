package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/runtime"
)

// TestStoreImplicitOfficialRegistry: with an official registry URL (a signed local
// registry here), source "cicord" lists its plugins without any cicord.yaml entry, and
// installing one locks "cicord/<name>", which the runtime resolves.
func TestStoreImplicitOfficialRegistry(t *testing.T) {
	// This fixture serves its registry from ordinary local Git repositories.
	t.Setenv("CICORD_ALLOW_PRIVATE_REGISTRIES", "true")
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	t.Setenv(registry.DevOfficialRegistryEnv, "")
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plugin := gitRepo(t, map[string]string{"plugin.yaml": helloManifest, "src/index.ts": helloSource}, "v1.0.0")
	index := "plugins:\n  hello:\n    repo: " + plugin + "\n    publisher: Cicord\n    verified: true\n    version: 1.0.0\n"
	const officialURL = "https://registry.cicord.example/official"
	sig, err := registry.SignIndex([]byte(index), registry.Envelope{Registry: officialURL, Serial: 1, Expires: time.Now().Add(time.Hour)}, key)
	if err != nil {
		t.Fatal(err)
	}
	reg := gitRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": string(sig)})
	oldURLs, oldKeys := registry.OfficialURLs, registry.OfficialKeys
	t.Cleanup(func() { registry.OfficialURLs, registry.OfficialKeys = oldURLs, oldKeys })
	t.Cleanup(registry.UseSerialStore(registry.FileSerialStore(filepath.Join(t.TempDir(), "serials.json"))))
	// The local copy is served from reg, an alias of the official URL it is signed for.
	registry.OfficialURLs, registry.OfficialKeys = []string{reg, officialURL}, []string{registry.EncodePublicKey(pub)}

	cache := registry.DefaultCacheDir()
	e := startFull(t, func(dir string) {
		p := filepath.Join(dir, "cicord.yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(data), "defaultMode: pullRequest", "defaultMode: commit", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}, func(o *runtime.Options) {
		o.Resolver = func(root string) config.PluginResolver { return registry.NewResolver(root, cache) }
	}, nil)
	c := e.client()
	c.login(e.seed.Owner.ID)

	var list struct {
		Sources []struct {
			Name, Kind, Trust, URL, Signature string
			Plugins                           []struct {
				Name, Trust, Repo string
				Verified          bool
			}
		}
	}
	c.expect(200, "GET", "/api/v1/store", nil).json(t, &list)
	cicord := list.Sources[0]
	if len(list.Sources) != 1 || cicord.Name != "cicord" || cicord.Signature != "verified" || cicord.URL != reg {
		t.Fatalf("sources = %+v", list.Sources)
	}
	var hello *struct {
		Name, Trust, Repo string
		Verified          bool
	}
	for i, p := range cicord.Plugins {
		if p.Name == "hello" {
			hello = &cicord.Plugins[i]
		}
	}
	if hello == nil || hello.Trust != "official" || !hello.Verified || hello.Repo != plugin {
		t.Fatalf("hello = %+v (plugins %+v)", hello, cicord.Plugins)
	}

	var d storeDetailDTO
	c.expect(200, "GET", "/api/v1/store/cicord/hello", nil).json(t, &d)
	if d.Version != "1.0.0" || d.Commit == "" || d.Trust != "official" || len(d.Commands) != 1 {
		t.Fatalf("detail = %+v", d)
	}

	install := "/api/v1/servers/" + serverKey + "/store/install"
	// Reviewed as the bundled kind (no commit): the origin changed → review again.
	if r := c.do("POST", install, map[string]any{"source": "cicord", "plugin": "hello"}); r.status != 409 || !strings.Contains(string(r.body), `"changed"`) {
		t.Fatalf("install without the reviewed commit: %d %s", r.status, r.body)
	}
	// Official: no confirmUnofficial needed.
	var res struct{ Instance, Mode, Health, Command string }
	c.expect(200, "POST", install, map[string]any{"source": "cicord", "plugin": "hello",
		"version": d.Version, "commit": d.Commit, "integrity": d.Integrity}).json(t, &res)
	if res.Instance != "hello" || res.Health != "Healthy" || res.Command != "hello" {
		t.Fatalf("install = %+v", res)
	}
	bot, err := os.ReadFile(filepath.Join(e.dir, "bots", "moderator", "bot.yaml"))
	if err != nil || !strings.Contains(string(bot), "source: cicord/hello") {
		t.Errorf("bot.yaml: %v\n%s", err, bot)
	}
	lock, err := registry.ReadLock(e.dir)
	if err != nil || lock.Plugins["cicord/hello"].Commit != d.Commit {
		t.Errorf("cicord.lock = %+v, %v", lock, err)
	}
	if data, _ := os.ReadFile(filepath.Join(e.dir, "cicord.yaml")); strings.Contains(string(data), "registries") {
		t.Errorf("cicord.yaml must not declare the official registry:\n%s", data)
	}
	// The instance is labelled official.
	var insts struct {
		Items []struct{ Instance, Source, Trust string }
	}
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins", nil).json(t, &insts)
	found := false
	for _, it := range insts.Items {
		if it.Instance == "hello" {
			found = it.Source == "cicord" && it.Trust == "official"
		}
	}
	if !found {
		t.Errorf("instances = %+v", insts.Items)
	}

	// Updates of "cicord/<name>" locked from the official registry resolve through it (no
	// cicord.yaml registry), as official: no unofficial confirmation, the reviewed commit.
	release(t, plugin, map[string]string{"plugin.yaml": strings.Replace(strings.Replace(helloManifest, "version: 1.0.0", "version: 1.1.0", 1),
		"capabilities: [discord.messages.send]", "capabilities: [discord.messages.send, storage.kv]", 1)}, "v1.1.0")
	c.expect(200, "POST", "/api/v1/store/refresh", nil)
	var rv updateReviewTestDTO
	c.expect(200, "GET", "/api/v1/store/cicord/hello/update", nil).json(t, &rv)
	if !rv.Available || rv.Update == nil || rv.Update.Kind != "registry" || rv.Detail.Version != "1.1.0" || rv.Detail.Trust != "official" ||
		len(rv.Diff.Capabilities.Added) != 1 || rv.Files != nil {
		t.Fatalf("review = %+v", rv)
	}
	update := "/api/v1/store/cicord/hello/update"
	if r := c.do("POST", update, map[string]any{"version": "1.1.0"}); r.status != 400 {
		t.Fatalf("update without the reviewed commit: %d %s", r.status, r.body)
	}
	var ur updateResultDTO
	c.expect(200, "POST", update, map[string]any{"version": rv.Detail.Version, "commit": rv.Detail.Commit, "integrity": rv.Detail.Integrity}).json(t, &ur)
	if ur.Version != "1.1.0" || len(ur.Instances) != 1 || ur.Instances[0].Health != "Healthy" {
		t.Fatalf("update = %+v", ur)
	}
	if lock, _ := registry.ReadLock(e.dir); lock.Plugins["cicord/hello"].Version != "1.1.0" {
		t.Errorf("cicord.lock = %+v", lock.Plugins)
	}
}
