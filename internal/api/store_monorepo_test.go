package api

import (
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/registry"
)

// A registry that is also the repository of its plugins (the layout of the official
// registry): plugins/<name>/ directories, index.yaml at the root with `path`, per-plugin
// tags <name>/v<semver>. A bare tag (v9.9.9) belongs to no plugin.
func TestStoreMonorepoRegistry(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	byeManifest := strings.NewReplacer("name: hello", "name: bye", "displayName: Hello", "displayName: Bye").Replace(helloManifest)
	byeSource := strings.NewReplacer("hello:", "bye:", "Say hello", "Say bye").Replace(helloSource)
	mono := gitRepo(t, map[string]string{
		"README.md":                    "official plugins",
		"plugins/hello/plugin.yaml":    helloManifest,
		"plugins/hello/src/index.ts":   helloSource,
		"plugins/hello/test/x.test.ts": "// unit tests are part of the plugin directory",
		"plugins/bye/plugin.yaml":      byeManifest,
		"plugins/bye/src/index.ts":     byeSource,
	}, "hello/v1.0.0", "bye/v1.0.0", "v9.9.9")
	release(t, mono, map[string]string{"index.yaml": "plugins:\n" +
		"  hello:\n    repo: " + mono + "\n    path: plugins/hello\n    publisher: acme\n" +
		"  bye:\n    repo: " + mono + "\n    path: plugins/bye\n"}, "index-1")
	e := startWithRegistry(t, mono, nil)
	c := e.client()
	c.login(e.seed.Owner.ID)

	var d storeDetailDTO
	c.expect(200, "GET", "/api/v1/store/acme/hello", nil).json(t, &d)
	if d.Version != "1.0.0" || d.Commit == "" {
		t.Fatalf("hello detail = %+v (the bare tag v9.9.9 is not a release of hello)", d)
	}
	install := "/api/v1/servers/" + serverKey + "/store/install"
	var res struct{ Instance, Health string }
	c.expect(200, "POST", install, map[string]any{"source": "acme", "plugin": "hello", "version": d.Version, "commit": d.Commit,
		"integrity": d.Integrity, "confirmUnofficial": true}).json(t, &res)
	if res.Instance != "hello" || res.Health != "Healthy" {
		t.Fatalf("install hello = %+v", res)
	}
	var bye storeDetailDTO
	c.expect(200, "GET", "/api/v1/store/acme/bye", nil).json(t, &bye)
	c.expect(200, "POST", install, map[string]any{"source": "acme", "plugin": "bye", "version": bye.Version, "commit": bye.Commit,
		"integrity": bye.Integrity, "confirmUnofficial": true}).json(t, &res)
	if res.Instance != "bye" || res.Health != "Healthy" {
		t.Fatalf("install bye = %+v", res)
	}
	lock, err := registry.ReadLock(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if le := lock.Plugins["acme/hello"]; le.Path != "plugins/hello" || le.Version != "1.0.0" || le.Integrity != d.Integrity {
		t.Fatalf("cicord.lock = %+v", lock.Plugins)
	}

	// A release of hello only: hello has an update, bye (same repository) has none.
	release(t, mono, map[string]string{"plugins/hello/plugin.yaml": strings.Replace(helloManifest, "version: 1.0.0", "version: 1.1.0", 1)}, "hello/v1.1.0")
	r := c.expect(200, "POST", "/api/v1/store/refresh", nil)
	if !strings.Contains(string(r.body), `"update":{"kind":"registry","current":"1.0.0","latest":"1.1.0","level":"minor"}`) {
		t.Fatalf("refresh does not report the update of hello: %s", r.body)
	}
	var pg struct {
		Items []struct{ Name string }
		Total int
	}
	c.expect(200, "GET", "/api/v1/store/plugins?updates=true", nil).json(t, &pg)
	if pg.Total != 1 || pg.Items[0].Name != "hello" {
		t.Fatalf("updates = %+v", pg)
	}

	var rv updateReviewTestDTO
	c.expect(200, "GET", "/api/v1/store/acme/hello/update", nil).json(t, &rv)
	if !rv.Available || rv.Detail.Version != "1.1.0" {
		t.Fatalf("review = %+v", rv)
	}
	var up updateResultDTO
	c.expect(200, "POST", "/api/v1/store/acme/hello/update", map[string]any{"version": rv.Detail.Version, "commit": rv.Detail.Commit,
		"integrity": rv.Detail.Integrity, "confirmUnofficial": true}).json(t, &up)
	if up.Version != "1.1.0" || len(up.Instances) != 1 || up.Instances[0].Health != "Healthy" {
		t.Fatalf("update = %+v", up)
	}
	if lock, err = registry.ReadLock(e.dir); err != nil {
		t.Fatal(err)
	}
	if le := lock.Plugins["acme/hello"]; le.Version != "1.1.0" || le.Path != "plugins/hello" || le.Integrity != rv.Detail.Integrity {
		t.Errorf("cicord.lock after update = %+v", lock.Plugins)
	}
	if le := lock.Plugins["acme/bye"]; le.Version != "1.0.0" {
		t.Errorf("bye changed: %+v", le)
	}
}
