package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/writeback"
)

// recordingWriter records the write-backs of the API (and applies them).
type recordingWriter struct {
	inner writeback.Writer
	mu    sync.Mutex
	reqs  []writeback.Request
}

func (w *recordingWriter) Write(ctx context.Context, req writeback.Request) (writeback.Result, error) {
	w.mu.Lock()
	w.reqs = append(w.reqs, req)
	w.mu.Unlock()
	return w.inner.Write(ctx, req)
}

func (w *recordingWriter) writes() []writeback.Request {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]writeback.Request(nil), w.reqs...)
}

const hiSource = `import { command, definePlugin } from "@cicord/sdk";

export default definePlugin({
  commands: {
    hi: command({
      description: "Say hi",
      async run(_ctx, interaction) {
        await interaction.reply({ content: "hi" });
      },
    }),
  },
});
`

type updatesReviewTestDTO struct {
	Items []struct {
		Source, Plugin string
		updateReviewTestDTO
		SecretReview string
	}
	Unavailable []struct{ Source, Plugin, Message string }
}

// Several plugins updated in one change: two registry plugins in the same bot.yaml and
// cicord.lock, and a copy of an official plugin. Every check of a single update applies
// to each plugin, and a refusal of any of them writes nothing.
func TestStoreUpdatesBatch(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	hiManifest := strings.NewReplacer("name: hello", "name: hi", "displayName: Hello", "displayName: Hi").Replace(helloManifest)
	helloDir := gitRepo(t, map[string]string{"plugin.yaml": helloManifest, "src/index.ts": helloSource}, "v1.0.0")
	hiDir := gitRepo(t, map[string]string{"plugin.yaml": hiManifest, "src/index.ts": hiSource}, "v1.0.0")
	reg := gitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + helloDir + "\n    publisher: acme\n" +
		"  hi:\n    repo: " + hiDir + "\n    publisher: acme\n"})
	files, err := catalog.Official().PluginFiles("welcome")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingWriter{}
	e := startWithRegistryOpts(t, reg, func(dir string) {
		// An old copy of welcome, modified (a file the bundled version does not ship).
		pd := filepath.Join(dir, "plugins", "welcome")
		_ = os.RemoveAll(pd)
		for rel, data := range files {
			if rel == "plugin.yaml" {
				data = []byte(strings.Replace(string(data), "version: 0.2.0", "version: 0.0.9", 1))
			}
			full := filepath.Join(pd, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.WriteFile(full, data, 0o644)
		}
		_ = os.WriteFile(filepath.Join(pd, "src", "old.ts"), []byte("export {};\n"), 0o644)
	}, func(o *Options) {
		rec.inner = o.Writer
		o.Writer = rec
	})
	c := e.client()
	c.login(e.seed.Owner.ID)
	for _, name := range []string{"hello", "hi"} {
		var d storeDetailDTO
		c.expect(200, "GET", "/api/v1/store/acme/"+name, nil).json(t, &d)
		c.expect(200, "POST", "/api/v1/servers/"+serverKey+"/store/install", map[string]any{"source": "acme", "plugin": name,
			"version": d.Version, "commit": d.Commit, "integrity": d.Integrity, "confirmUnofficial": true})
	}
	release(t, helloDir, map[string]string{"plugin.yaml": strings.Replace(
		strings.Replace(helloManifest, "version: 1.0.0", "version: 1.1.0", 1),
		"capabilities: [discord.messages.send]", "capabilities: [discord.messages.send, storage.kv]", 1)}, "v1.1.0")
	release(t, hiDir, map[string]string{"plugin.yaml": strings.Replace(hiManifest, "version: 1.0.0", "version: 1.0.1", 1)}, "v1.0.1")
	c.expect(200, "POST", "/api/v1/store/refresh", nil)

	var rv updatesReviewTestDTO
	c.expect(200, "GET", "/api/v1/store/updates", nil).json(t, &rv)
	var got []string
	for _, it := range rv.Items {
		got = append(got, it.Source+"/"+it.Plugin+"@"+it.Detail.Version)
	}
	if strings.Join(got, " ") != "acme/hello@1.1.0 acme/hi@1.0.1 cicord/welcome@0.2.0" || len(rv.Unavailable) != 0 {
		t.Fatalf("updates review = %v (unavailable %+v)", got, rv.Unavailable)
	}
	if !rv.Items[2].Modified || len(rv.Items[0].Diff.Capabilities.Added) != 1 || rv.Items[0].Diff.Capabilities.Added[0] != "storage.kv" {
		t.Fatalf("review details = %+v", rv.Items)
	}
	items := func() []map[string]any {
		var out []map[string]any
		for _, it := range rv.Items {
			out = append(out, map[string]any{"source": it.Source, "plugin": it.Plugin, "version": it.Detail.Version,
				"commit": it.Detail.Commit, "integrity": it.Detail.Integrity, "confirmSecretChanges": it.SecretReview})
		}
		return out
	}
	base := len(rec.writes()) // the installs
	lockBefore, _ := os.ReadFile(filepath.Join(e.dir, registry.LockFile))
	unchanged := func(what string) {
		t.Helper()
		if n := len(rec.writes()) - base; n != 0 {
			t.Fatalf("%s: %d write(s)", what, n)
		}
		if now, _ := os.ReadFile(filepath.Join(e.dir, registry.LockFile)); string(now) != string(lockBefore) {
			t.Fatalf("%s: cicord.lock changed", what)
		}
	}

	refuse := func(what string, status int, code string, body map[string]any) {
		t.Helper()
		if r := c.do("POST", "/api/v1/store/updates", body); r.status != status || !strings.Contains(string(r.body), code) {
			t.Fatalf("%s: %d %s", what, r.status, r.body)
		}
		unchanged(what)
	}
	refuse("empty", 400, `"invalid"`, map[string]any{"updates": []any{}})
	twice := items()
	refuse("listed twice", 400, "listed twice", map[string]any{"updates": append(twice, twice[0]), "confirmUnofficial": true})
	refuse("unofficial not confirmed", 409, "confirmation_required", map[string]any{"updates": items()})
	refuse("modified copy not forced", 409, `"modified"`, map[string]any{"updates": items(), "confirmUnofficial": true})
	stale := items()
	stale[2]["force"] = true
	stale[1]["commit"] = "0000000000000000000000000000000000000000"
	refuse("one reviewed commit is not the latest", 409, `"changed"`, map[string]any{"updates": stale, "confirmUnofficial": true})
	mod := e.client()
	mod.login(e.moderator().ID)
	all := items()
	all[2]["force"] = true
	if r := mod.do("POST", "/api/v1/store/updates", map[string]any{"updates": all, "confirmUnofficial": true}); r.status != 403 {
		t.Fatalf("moderator: %d %s", r.status, r.body)
	}
	unchanged("moderator")

	var res struct {
		updateResultDTO
		Updated []struct{ Source, From, To string }
	}
	c.expect(200, "POST", "/api/v1/store/updates", map[string]any{"updates": all, "confirmUnofficial": true}).json(t, &res)
	ws := rec.writes()[base:]
	if len(ws) != 1 {
		t.Fatalf("%d write-backs, want one", len(ws))
	}
	if res.Mode != "commit" || res.Version != "" || len(res.Updated) != 3 || res.Updated[0].Source != "acme/hello" || res.Updated[0].From != "1.0.0" ||
		res.Updated[0].To != "1.1.0" || res.Updated[2].Source != "cicord/welcome" || res.Updated[2].To != "0.2.0" {
		t.Fatalf("result = %+v", res)
	}
	for _, i := range res.Instances {
		if i.Health != "Healthy" {
			t.Errorf("instance %s/%s: %s", i.Bot, i.Instance, i.Health)
		}
	}
	msg := ws[0].Message
	for _, want := range []string{"plugin: update 3 plugins\n", "\nacme/hello 1.0.0 → 1.1.0\nNewly granted: storage.kv.\n", "\nacme/hi 1.0.0 → 1.0.1\n",
		"\ncicord/welcome 0.0.9 → 0.2.0\n", "\nUpdated on " + serverKey + "."} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message lacks %q:\n%s", want, msg)
		}
	}
	lock, err := registry.ReadLock(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Plugins["acme/hello"].Version != "1.1.0" || lock.Plugins["acme/hi"].Version != "1.0.1" {
		t.Errorf("cicord.lock = %+v", lock.Plugins)
	}
	if !strings.Contains(helloEntry(t, e), "storage.kv") {
		t.Errorf("hello grants not updated:\n%s", helloEntry(t, e))
	}
	if _, err := os.Stat(filepath.Join(e.dir, "plugins", "welcome", "src", "old.ts")); !os.IsNotExist(err) {
		t.Errorf("welcome copy not replaced: %v", err)
	}
	c.expect(200, "GET", "/api/v1/store/updates", nil).json(t, &rv)
	if len(rv.Items) != 0 {
		t.Errorf("updates left after updating all: %+v", rv.Items)
	}
}
