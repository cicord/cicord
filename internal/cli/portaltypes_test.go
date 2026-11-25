package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The reference portal's src/cicord-routes.d.ts is generated from examples/showcase: it must
// stay up to date with the showcase plugins (their routes and route contracts).
func TestReferencePortalRouteTypesUpToDate(t *testing.T) {
	out := filepath.Join("..", "..", "packages", "create-cicord-portal", "src", "cicord-routes.d.ts")
	code, stdout, stderr := run(t, "portal", "typegen", filepath.Join("..", "..", "examples", "showcase"), "--out", out, "--check")
	if code != 0 {
		t.Fatalf("%s\n%s\nregenerate with: go run ./cmd/cicord portal typegen examples/showcase --out packages/create-cicord-portal/src/cicord-routes.d.ts", stdout, stderr)
	}
	got := readFile(t, out)
	for _, want := range []string{
		`"GET /leaderboard": RouteEntry<"public", cicord_levels.Routes, "GET /leaderboard">;`,
		`"GET /polls/:id": RouteEntry<"public", cicord_polls.Routes, "GET /polls/:id">;`,
		`"GET /tickets": RouteEntry<"moderator", cicord_tickets.Routes, "GET /tickets">;`,
		"export interface LeaderboardEntry {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestPortalTypegen(t *testing.T) {
	repo := t.TempDir()
	if code, _, stderr := run(t, "init", repo, "--project", "fans"); code != 0 {
		t.Fatal(stderr)
	}
	// No custom portal: --out is needed.
	if code, _, stderr := run(t, "portal", "typegen", repo); code == 0 || !strings.Contains(stderr, "--out") {
		t.Fatalf("without portal = %d %s", code, stderr)
	}
	writeFile(t, filepath.Join(repo, "portal", "package.json"), `{"dependencies":{"@cicord/client":"file:vendor/x.tgz"}}`)
	out := filepath.Join(repo, "portal", "src", "cicord-routes.d.ts")

	// A plugin with routes and a contract on the bot.
	plugin := filepath.Join(repo, "plugins", "votes")
	writeFile(t, filepath.Join(plugin, "plugin.yaml"), `apiVersion: cicord.dev/v1alpha1
kind: Plugin
metadata: { name: votes, version: 1.2.0 }
spec:
  engine: ">=0.0.0"
  entry: ./src/index.ts
  capabilities: [portal.routes]
  routes:
    types: ./src/routes.ts
`)
	writeFile(t, filepath.Join(plugin, "src", "routes.ts"), `export interface Count { n: number }
export interface Routes {
  "GET /count/:key": { response: Count };
  "POST /count/:key": { body: { by: number }; response: Count };
}
`)
	writeFile(t, filepath.Join(plugin, "src", "index.ts"), `import { definePlugin, defineRoutes } from "@cicord/sdk";
import type { Routes } from "./routes.ts";
export default definePlugin({
  routes: defineRoutes<Routes>({
    "GET /count/:key": { access: "public", handler: () => ({ n: 1 }) },
    "POST /count/:key": { access: "member", handler: (_ctx, req) => ({ n: req.body?.by ?? 0 }) },
  }),
});
`)
	bot := filepath.Join(repo, "bots", "bot", "bot.yaml")
	writeFile(t, bot, strings.Replace(readFile(t, bot), "  plugins: []", "  plugins:\n    - name: votes\n      source: ../../plugins/votes\n      grants: [portal.routes]", 1))
	if !strings.Contains(readFile(t, bot), "name: votes") {
		t.Fatalf("bot.yaml not patched:\n%s", readFile(t, bot))
	}

	if code, stdout, stderr := run(t, "portal", "typegen", repo); code != 0 || !strings.Contains(stdout, "wrote") {
		t.Fatalf("typegen = %d %s %s", code, stdout, stderr)
	}
	got := readFile(t, out)
	for _, want := range []string{
		"//   votes 1.2.0 (../../plugins/votes): 2 route(s), typed",
		"declare namespace cicord_votes {\n  export interface Count { n: number }",
		`"GET /count/:key": RouteEntry<"public", cicord_votes.Routes, "GET /count/:key">;`,
		`"POST /count/:key": RouteEntry<"member", cicord_votes.Routes, "POST /count/:key">;`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if code, stdout, _ := run(t, "portal", "typegen", repo, "--check"); code != 0 || !strings.Contains(stdout, "up to date") {
		t.Errorf("--check after generate = %d %s", code, stdout)
	}

	// A route change makes the file out of date.
	writeFile(t, filepath.Join(plugin, "src", "routes.ts"), `export interface Routes { "GET /count/:key": { response: number } }`)
	if code, _, stderr := run(t, "portal", "typegen", repo, "--check"); code != 1 || !strings.Contains(stderr, "out of date") {
		t.Errorf("--check after change = %d %s", code, stderr)
	}

	// A contract that cannot be copied is an error, here and in `cicord plugin build`.
	writeFile(t, filepath.Join(plugin, "src", "routes.ts"), "import type { X } from \"./x.ts\";\nexport interface Routes { \"GET /count/:key\": { response: X } }\n")
	if code, _, stderr := run(t, "portal", "typegen", repo); code == 0 || !strings.Contains(stderr, "imports and re-exports are not allowed") {
		t.Errorf("bad contract = %d %s", code, stderr)
	}
	if code, stdout, _ := run(t, "plugin", "build", plugin); code != 1 || !strings.Contains(stdout, "./src/routes.ts: imports and re-exports are not allowed") {
		t.Errorf("plugin build with a bad contract = %d %s", code, stdout)
	}
}

// minimalKit is a portal kit with the four @cicord/* tarballs and a tiny template.
func minimalKit(t *testing.T) fstest.MapFS {
	return fstest.MapFS{
		"packages/cicord-client-0.1.0.tgz": {Data: fakeTgz(t, `{"name":"@cicord/client","version":"0.1.0"}`)},
		"packages/cicord-react-0.1.0.tgz":  {Data: fakeTgz(t, `{"name":"@cicord/react","version":"0.1.0","dependencies":{"@cicord/client":"0.1.0"}}`)},
		"packages/cicord-ui-0.1.0.tgz":     {Data: fakeTgz(t, `{"name":"@cicord/ui","version":"0.1.0"}`)},
		"packages/cicord-sdk-0.1.0.tgz":    {Data: fakeTgz(t, `{"name":"@cicord/sdk","version":"0.1.0"}`)},
		"template/package.json": {Data: []byte(`{"name":"portal","private":true,"version":"0.0.0","type":"module",
			"scripts":{"build":"vp build"},
			"dependencies":{"@cicord/client":"^0.1.0","@cicord/react":"^0.1.0","@cicord/ui":"^0.1.0"}}`)},
		"template/src/main.tsx":           {Data: []byte("// main")},
		"template/src/cicord-routes.d.ts": {Data: []byte("// template types of the monorepo examples")},
	}
}

func TestPortalInitWritesRouteTypes(t *testing.T) {
	useKit(t, minimalKit(t), nil)
	// Outside a repository: an empty registry replaces the template's file.
	dir := filepath.Join(t.TempDir(), "site")
	if code, _, stderr := run(t, "portal", "init", dir); code != 0 {
		t.Fatal(stderr)
	}
	got := readFile(t, filepath.Join(dir, "src", "cicord-routes.d.ts"))
	if !strings.Contains(got, "No plugin of the repository has portal routes.") || strings.Contains(got, "template") {
		t.Errorf("route types outside a repo:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "cicord-sdk-0.1.0.tgz")); err == nil {
		t.Error("@cicord/sdk must not be vendored into portals")
	}
}
