package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/portalroutes"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/version"
)

const boardManifest = `apiVersion: cicord.dev/v1alpha1
kind: Plugin
metadata:
  name: board
  version: 1.0.0
  displayName: Board
spec:
  engine: ">=0.1.0"
  entry: ./src/index.ts
  capabilities: [portal.routes]
  routes:
    types: ./src/routes.ts
`

const boardContract = `export interface Entry { name: string; score: number }
export interface Routes {
  "GET /board": { response: Entry[] };
}
`

const boardSource = `import { definePlugin, defineRoutes } from "@cicord/sdk";
import type { Routes } from "./routes.ts";

export default definePlugin({
  routes: defineRoutes<Routes>({
    "GET /board": { access: "public", handler: () => [] },
  }),
});
`

// startPortalRepo deploys examples/basic with the community repository's portal layout
// (cicord.yaml spec.portal.path ./portal/dist, portal/src/cicord-routes.d.ts generated
// before any plugin has routes) and the registry "acme" listing "board", a plugin with a
// typed portal route.
func startPortalRepo(t *testing.T) (*env, string) {
	t.Helper()
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	plugin := gitRepo(t, map[string]string{"plugin.yaml": boardManifest, "src/index.ts": boardSource, "src/routes.ts": boardContract}, "v1.0.0")
	reg := gitRepo(t, map[string]string{"index.yaml": "plugins:\n  board:\n    repo: " + plugin + "\n    publisher: acme\n"})
	e := startWithRegistry(t, reg, func(dir string) {
		p := filepath.Join(dir, "cicord.yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(data), "spec:\n", "spec:\n  portal:\n    path: ./portal/dist\n", 1)
		for name, content := range map[string]string{
			"cicord.yaml":            s,
			"portal/package.json":    `{"name":"portal","private":true,"dependencies":{"@cicord/client":"file:vendor/cicord-client.tgz"}}`,
			"portal/dist/index.html": "<!doctype html><title>portal</title>",
		} {
			f := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// Up to date before the Store action (the onboarding plugin of examples/basic has routes).
		if err := os.MkdirAll(filepath.Join(dir, "portal", "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "portal", "src", "cicord-routes.d.ts"), []byte(typegenOf(t, dir)), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	return e, plugin
}

// typegenOf is what `cicord portal typegen` writes for the repository in dir: the same
// load (cicord.lock resolved from the cache, like the CLI's resolver) and generator.
func typegenOf(t *testing.T, dir string) string {
	t.Helper()
	repo, ds := config.Load(dir, config.LoadOptions{EngineVersion: version.Version, Resolver: registry.NewResolver(dir, registry.DefaultCacheDir())})
	if repo == nil || ds.HasErrors() {
		t.Fatalf("the repository does not validate: %v", ds.Errors())
	}
	out, err := portalroutes.Generate(repo)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStoreActionsKeepPortalRouteTypes: install, update and uninstall from the Store
// regenerate the portal's route types in the same write-back as cicord.lock and bot.yaml,
// so that the portal's CI (`cicord portal typegen --check`) stays green.
func TestStoreActionsKeepPortalRouteTypes(t *testing.T) {
	e, pluginDir := startPortalRepo(t)
	c := e.client()
	c.login(e.seed.Owner.ID)
	routes := filepath.Join(e.dir, "portal", "src", "cicord-routes.d.ts")
	before, err := os.ReadFile(routes)
	if err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		data, err := os.ReadFile(routes)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	var d storeDetailDTO
	c.expect(200, "GET", "/api/v1/store/acme/board", nil).json(t, &d)
	var inst struct {
		Instance string
		Mode     string
		Warnings []string
	}
	c.expect(200, "POST", "/api/v1/servers/"+serverKey+"/store/install", map[string]any{"source": "acme", "plugin": "board",
		"version": d.Version, "commit": d.Commit, "integrity": d.Integrity, "confirmUnofficial": true}).json(t, &inst)
	if inst.Mode != "commit" || len(inst.Warnings) != 0 {
		t.Fatalf("install = %+v", inst)
	}
	got := read()
	if !strings.Contains(got, "//   board 1.0.0 (acme/board): 1 route(s), typed") ||
		!strings.Contains(got, `"GET /board": RouteEntry<"public", cicord_board.Routes, "GET /board">;`) {
		t.Fatalf("route types after install:\n%s", got)
	}
	if want := typegenOf(t, e.dir); got != want {
		t.Fatalf("route types after install differ from cicord portal typegen:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, version.Version) && version.Version != "1.0.0" {
		t.Errorf("the route types embed the runtime version %s", version.Version)
	}

	// 1.1.0: only the version changes, which the header names (cicord-community#9).
	release(t, pluginDir, map[string]string{"plugin.yaml": strings.Replace(boardManifest, "version: 1.0.0", "version: 1.1.0", 1)}, "v1.1.0")
	c.expect(200, "POST", "/api/v1/store/refresh", nil)
	var rv updateReviewTestDTO
	c.expect(200, "GET", "/api/v1/store/acme/board/update", nil).json(t, &rv)
	if !rv.Available || rv.Detail.Version != "1.1.0" {
		t.Fatalf("review = %+v", rv)
	}
	// Another instance needs the update first: the version is shared by every instance.
	if r := c.do("POST", "/api/v1/servers/"+serverKey+"/store/install", map[string]any{"source": "acme", "plugin": "board",
		"instance": "board-2", "version": rv.Detail.Version, "commit": rv.Detail.Commit, "integrity": rv.Detail.Integrity,
		"confirmUnofficial": true}); r.status != 422 || !strings.Contains(string(r.body), "already locked at 1.0.0: update it first") {
		t.Fatalf("another instance before the update: %d %s", r.status, r.body)
	}
	var up struct {
		Version, Mode string
		Warnings      []string
	}
	c.expect(200, "POST", "/api/v1/store/acme/board/update", map[string]any{"version": rv.Detail.Version, "commit": rv.Detail.Commit,
		"integrity": rv.Detail.Integrity, "confirmUnofficial": true}).json(t, &up)
	if up.Version != "1.1.0" || up.Mode != "commit" || len(up.Warnings) != 0 {
		t.Fatalf("update = %+v", up)
	}
	lock, err := registry.ReadLock(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Plugins["acme/board"].Version != "1.1.0" {
		t.Fatalf("cicord.lock = %+v", lock.Plugins)
	}
	after := read()
	if !strings.Contains(after, "//   board 1.1.0 (acme/board): 1 route(s), typed") || strings.Contains(after, "board 1.0.0") {
		t.Fatalf("route types after update:\n%s", after)
	}
	if want := typegenOf(t, e.dir); after != want {
		t.Fatalf("route types after update differ from cicord portal typegen:\n%s\nwant:\n%s", after, want)
	}
	if diff := lineDiff(got, after); len(diff) != 1 {
		t.Errorf("an update that only bumps the version changes one line, got %q", diff)
	}

	// Uninstalling the last instance removes the plugin from the route types.
	c.expect(200, "DELETE", "/api/v1/servers/"+serverKey+"/plugins/moderator/board", nil)
	final := read()
	if strings.Contains(final, `"board"`) || final != typegenOf(t, e.dir) || final != string(before) {
		t.Fatalf("route types after uninstall:\n%s", final)
	}
}

// lineDiff lists the lines of b that are not at the same place in a (same line count).
func lineDiff(a, b string) []string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(la) != len(lb) {
		return lb
	}
	var out []string
	for i := range la {
		if la[i] != lb[i] {
			out = append(out, lb[i])
		}
	}
	return out
}
