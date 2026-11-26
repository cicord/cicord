package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/registry"
)

const updManifest = `apiVersion: cicord.dev/v1alpha1
kind: Plugin
metadata:
  name: hello
  version: 1.0.0
spec:
  engine: ">=0.1.0"
  entry: ./src/index.ts
  capabilities: [discord.messages.send]
`

func tagRelease(t *testing.T, dir string, files map[string]string, tag string) {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for p, c := range files {
		writeFile(t, filepath.Join(dir, p), c)
		if _, err := wt.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	h, err := wt.Commit(tag, &git.CommitOptions{Author: &object.Signature{Name: "d", Email: "d@x", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateTag(tag, h, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPluginUpdate(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	plugin := commitRepo(t, map[string]string{"plugin.yaml": updManifest, "src/index.ts": `import {definePlugin} from "@cicord/sdk"; export default definePlugin({});`}, "v1.0.0")
	reg := commitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n"})
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, _, errOut := run(t, "registry", "add", "acme", reg, "--dir", dir); code != 0 {
		t.Fatalf("registry add: %s", errOut)
	}
	if code, _, errOut := run(t, "plugin", "add", "acme/hello", "--bot", "bot", "--dir", dir); code != 0 {
		t.Fatalf("plugin add: %s", errOut)
	}
	if code, out, errOut := run(t, "plugin", "update", "--dir", dir); code != 0 || !strings.Contains(out, "up to date") {
		t.Fatalf("list before a release: %d %s %s", code, out, errOut)
	}

	tagRelease(t, plugin, map[string]string{"plugin.yaml": strings.Replace(strings.Replace(updManifest, "1.0.0", "1.1.0", 1),
		"[discord.messages.send]", "[discord.messages.send, storage.kv]", 1)}, "v1.1.0")
	if code, out, errOut := run(t, "plugin", "update", "--dir", dir); code != 0 || !strings.Contains(out, "acme/hello") ||
		!strings.Contains(out, "1.1.0") || !strings.Contains(out, "minor") {
		t.Fatalf("list: %d %s %s", code, out, errOut)
	}
	code, out, errOut := run(t, "plugin", "update", "acme/hello", "--dir", dir)
	if code != 0 || !strings.Contains(out, "1.0.0 → 1.1.0") || !strings.Contains(out, "+ capability storage.kv") {
		t.Fatalf("update: %d %s %s", code, out, errOut)
	}
	lock, _ := registry.ReadLock(dir)
	if lock.Plugins["acme/hello"].Version != "1.1.0" {
		t.Errorf("cicord.lock = %+v", lock.Plugins)
	}
	bot, _ := os.ReadFile(filepath.Join(dir, "bots", "bot", "bot.yaml"))
	if !strings.Contains(string(bot), "storage.kv") || !strings.Contains(string(bot), "version: ^1.0.0") {
		t.Errorf("bot.yaml:\n%s", bot)
	}
	if code, out, _ := run(t, "plugin", "update", "acme/hello", "--dir", dir); code != 0 || !strings.Contains(out, "up to date") {
		t.Errorf("second update: %d %s", code, out)
	}

	// A new major version: the range is widened (or set with --to).
	tagRelease(t, plugin, map[string]string{"plugin.yaml": strings.Replace(updManifest, "1.0.0", "2.0.0", 1)}, "v2.0.0")
	if code, out, errOut := run(t, "plugin", "update", "acme/hello", "--to", "~1.1.0", "--dir", dir); code != 0 || !strings.Contains(out, "^1.0.0 → ~1.1.0") {
		t.Fatalf("update --to: %d %s %s", code, out, errOut)
	}
	code, out, errOut = run(t, "plugin", "update", "acme/hello", "--dir", dir)
	if code != 0 || !strings.Contains(out, "- capability storage.kv") || !strings.Contains(out, "~1.1.0 → ^2.0.0") {
		t.Fatalf("major update: %d %s %s", code, out, errOut)
	}
	bot, _ = os.ReadFile(filepath.Join(dir, "bots", "bot", "bot.yaml"))
	if strings.Contains(string(bot), "storage.kv") || !strings.Contains(string(bot), "version: ^2.0.0") {
		t.Errorf("bot.yaml after the major update:\n%s", bot)
	}
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Errorf("validate after update: %s", errOut)
	}
}

const boardManifest = `apiVersion: cicord.dev/v1alpha1
kind: Plugin
metadata:
  name: board
  version: 1.0.0
spec:
  engine: ">=0.1.0"
  entry: ./src/index.ts
  capabilities: [portal.routes]
  routes:
    types: ./src/routes.ts
`

const boardSource = `import { definePlugin, defineRoutes } from "@cicord/sdk";
import type { Routes } from "./routes.ts";
export default definePlugin({
  routes: defineRoutes<Routes>({
    "GET /board": { access: "public", handler: () => [] },
  }),
});
`

// `cicord plugin add` and `cicord plugin update` keep the custom portal's route types up to
// date like the admin's Store (community layout: spec.portal.path ./portal/dist and
// portal/src/cicord-routes.d.ts): the portal's CI check (`portal typegen --check`) passes.
func TestPluginAddUpdateKeepPortalRouteTypes(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	plugin := commitRepo(t, map[string]string{"plugin.yaml": boardManifest, "src/index.ts": boardSource,
		"src/routes.ts": "export interface Routes { \"GET /board\": { response: string[] } }\n"}, "v1.0.0")
	reg := commitRepo(t, map[string]string{"index.yaml": "plugins:\n  board:\n    repo: " + plugin + "\n"})
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, _, errOut := run(t, "registry", "add", "acme", reg, "--dir", dir); code != 0 {
		t.Fatalf("registry add: %s", errOut)
	}
	project := filepath.Join(dir, "cicord.yaml")
	writeFile(t, project, strings.Replace(readFile(t, project), "spec:\n", "spec:\n  portal:\n    path: ./portal/dist\n", 1))
	writeFile(t, filepath.Join(dir, "portal", "package.json"), `{"name":"portal","private":true}`)
	writeFile(t, filepath.Join(dir, "portal", "dist", "index.html"), "<!doctype html>")
	routes := filepath.Join(dir, "portal", "src", "cicord-routes.d.ts")
	check := func(when string) {
		t.Helper()
		if code, out, errOut := run(t, "portal", "typegen", dir, "--check", "--out", routes); code != 0 {
			t.Fatalf("portal typegen --check %s: %d %s %s", when, code, out, errOut)
		}
	}

	// Without the file: left alone (a portal that does not use typed routes).
	if code, out, errOut := run(t, "plugin", "add", "acme/board", "--bot", "bot", "--name", "first", "--dir", dir); code != 0 ||
		strings.Contains(out, "Regenerated") {
		t.Fatalf("plugin add without route types: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(routes); !os.IsNotExist(err) {
		t.Fatalf("route types created: %v", err)
	}
	if code, _, errOut := run(t, "portal", "typegen", dir); code != 0 {
		t.Fatal(errOut)
	}
	check("after typegen")

	// add: a second instance does not change the file (same plugin, same version).
	code, out, errOut := run(t, "plugin", "add", "acme/board", "--bot", "bot", "--name", "second", "--dir", dir)
	if code != 0 || strings.Contains(out, "Regenerated") {
		t.Fatalf("plugin add of the same version: %d %s %s", code, out, errOut)
	}
	if !strings.Contains(readFile(t, routes), "//   board 1.0.0 (acme/board): 1 route(s), typed") {
		t.Fatalf("route types:\n%s", readFile(t, routes))
	}

	// update: the header names the new version, and the check passes without a manual fix.
	tagRelease(t, plugin, map[string]string{"plugin.yaml": strings.Replace(boardManifest, "1.0.0", "1.1.0", 1)}, "v1.1.0")
	code, out, errOut = run(t, "plugin", "update", "acme/board", "--dir", dir)
	if code != 0 || !strings.Contains(out, "Regenerated portal/src/cicord-routes.d.ts (cicord portal typegen).") || errOut != "" {
		t.Fatalf("plugin update: %d %s %s", code, out, errOut)
	}
	got := readFile(t, routes)
	if !strings.Contains(got, "//   board 1.1.0 (acme/board): 1 route(s), typed") || strings.Contains(got, "board 1.0.0") {
		t.Fatalf("route types after update:\n%s", got)
	}
	check("after update")

	// The file cannot be generated (a route contract that cannot be read): the update is
	// applied, with a warning, and the file is left as it was.
	bad := filepath.Join(dir, "plugins", "bad")
	writeFile(t, filepath.Join(bad, "plugin.yaml"), strings.Replace(strings.Replace(boardManifest, "name: board", "name: bad", 1),
		"./src/routes.ts", "./src/missing.ts", 1))
	writeFile(t, filepath.Join(bad, "src", "index.ts"), strings.Replace(boardSource, `import type { Routes } from "./routes.ts";`,
		`type Routes = { "GET /board": { response: string[] } };`, 1))
	botFile := filepath.Join(dir, "bots", "bot", "bot.yaml")
	writeFile(t, botFile, readFile(t, botFile)+"    - name: bad\n      source: ../../plugins/bad\n      grants: [portal.routes]\n")
	tagRelease(t, plugin, map[string]string{"plugin.yaml": strings.Replace(boardManifest, "1.0.0", "1.2.0", 1)}, "v1.2.0")
	code, out, errOut = run(t, "plugin", "update", "acme/board", "--dir", dir)
	if code != 0 || strings.Contains(out, "Regenerated") ||
		!strings.Contains(errOut, "warning: portal/src/cicord-routes.d.ts was not regenerated") {
		t.Fatalf("plugin update with a broken contract: %d %s %s", code, out, errOut)
	}
	if lock, _ := registry.ReadLock(dir); lock.Plugins["acme/board"].Version != "1.2.0" {
		t.Errorf("the update was not applied: %+v", lock.Plugins)
	}
	if readFile(t, routes) != got {
		t.Error("the route types changed although they could not be generated")
	}
}

// An update keeps the grants the new version still declares in an optional feature (an
// instance configured for it keeps working), removes those it no longer declares, adds the
// newly required ones, and leaves the rest of bot.yaml as written.
func TestPluginUpdateKeepsOptionalGrants(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	v1 := strings.Replace(updManifest, "[discord.messages.send]", "[discord.messages.send, discord.roles.assign, scheduler]", 1)
	plugin := commitRepo(t, map[string]string{"plugin.yaml": v1, "src/index.ts": `import {definePlugin} from "@cicord/sdk"; export default definePlugin({});`}, "v1.0.0")
	reg := commitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin + "\n"})
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, _, errOut := run(t, "registry", "add", "acme", reg, "--dir", dir); code != 0 {
		t.Fatalf("registry add: %s", errOut)
	}
	if code, _, errOut := run(t, "plugin", "add", "acme/hello", "--bot", "bot", "--dir", dir); code != 0 {
		t.Fatalf("plugin add: %s", errOut)
	}
	// Written by hand: flow sequences, blank lines, aligned comments.
	botFile := filepath.Join(dir, "bots", "bot", "bot.yaml")
	bot := readFile(t, botFile)
	i := strings.Index(bot, "  plugins:")
	if i < 0 {
		t.Fatalf("bot.yaml:\n%s", bot)
	}
	bot = bot[:i] + `  plugins:
    # Says hello.
    - name: hello
      source: acme/hello
      version: ^1.0.0
      grants: [discord.messages.send, discord.roles.assign, scheduler]
      config:
        rules: [ { a: 1, b: 2 } ]   # aligned
        other: true                 # comments

    - name: hello-too
      source: acme/hello
      version: ^1.0.0
      grants: [ scheduler, discord.messages.send, discord.roles.assign ]
`
	writeFile(t, botFile, bot)
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Fatalf("validate: %s", errOut)
	}

	v2 := strings.Replace(strings.Replace(updManifest, "1.0.0", "2.0.0", 1), "[discord.messages.send]",
		"[discord.messages.send, storage.kv]\n  optional:\n    - feature: defaultRole\n      capabilities: [discord.roles.assign]", 1)
	tagRelease(t, plugin, map[string]string{"plugin.yaml": v2}, "v2.0.0")
	code, out, errOut := run(t, "plugin", "update", "acme/hello", "--dir", dir)
	if code != 0 {
		t.Fatalf("update: %d %s %s", code, out, errOut)
	}
	for _, want := range []string{
		"~ capability discord.roles.assign is now optional (feature defaultRole): kept where granted",
		"- capability scheduler (no longer declared)",
		"bot/hello: grants kept for optional features: discord.roles.assign (feature defaultRole)",
		"bot/hello: grants removed (no longer declared): scheduler",
		"bot/hello: grants added (required): storage.kv",
		"bot/hello-too: grants removed (no longer declared): scheduler",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("review lacks %q:\n%s", want, out)
		}
	}
	want := strings.NewReplacer(
		"version: ^1.0.0", "version: ^2.0.0",
		"grants: [discord.messages.send, discord.roles.assign, scheduler]", "grants: [discord.messages.send, discord.roles.assign, storage.kv]",
		"grants: [ scheduler, discord.messages.send, discord.roles.assign ]", "grants: [ discord.messages.send, discord.roles.assign, storage.kv ]",
	).Replace(bot)
	if got := readFile(t, botFile); got != want {
		t.Errorf("bot.yaml:\n%s\nwant:\n%s", got, want)
	}
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Errorf("validate after update: %s", errOut)
	}
}

// `cicord plugin update --all` updates every plugin with an update, in turn.
func TestPluginUpdateAll(t *testing.T) {
	t.Setenv("CICORD_CACHE_DIR", t.TempDir())
	src := `import {definePlugin} from "@cicord/sdk"; export default definePlugin({});`
	hiManifest := strings.Replace(updManifest, "name: hello", "name: hi", 1)
	hello := commitRepo(t, map[string]string{"plugin.yaml": updManifest, "src/index.ts": src}, "v1.0.0")
	hi := commitRepo(t, map[string]string{"plugin.yaml": hiManifest, "src/index.ts": src}, "v1.0.0")
	reg := commitRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + hello + "\n  hi:\n    repo: " + hi + "\n"})
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, _, errOut := run(t, "registry", "add", "acme", reg, "--dir", dir); code != 0 {
		t.Fatalf("registry add: %s", errOut)
	}
	for _, p := range []string{"acme/hello", "acme/hi"} {
		if code, _, errOut := run(t, "plugin", "add", p, "--bot", "bot", "--dir", dir); code != 0 {
			t.Fatalf("plugin add %s: %s", p, errOut)
		}
	}
	if code, out, errOut := run(t, "plugin", "update", "--all", "--dir", dir); code != 0 || !strings.Contains(out, "up to date") {
		t.Fatalf("--all before a release: %d %s %s", code, out, errOut)
	}
	tagRelease(t, hello, map[string]string{"plugin.yaml": strings.Replace(strings.Replace(updManifest, "1.0.0", "1.1.0", 1),
		"[discord.messages.send]", "[discord.messages.send, storage.kv]", 1)}, "v1.1.0")
	tagRelease(t, hi, map[string]string{"plugin.yaml": strings.Replace(hiManifest, "1.0.0", "1.0.1", 1)}, "v1.0.1")
	if code, _, errOut := run(t, "plugin", "update", "--all", "acme/hi", "--dir", dir); code == 0 || !strings.Contains(errOut, "--all") {
		t.Fatalf("--all with a plugin: %d %s", code, errOut)
	}
	code, out, errOut := run(t, "plugin", "update", "--all", "--dir", dir)
	if code != 0 || !strings.Contains(out, "1.0.0 → 1.1.0") || !strings.Contains(out, "1.0.0 → 1.0.1") {
		t.Fatalf("--all: %d %s %s", code, out, errOut)
	}
	lock, _ := registry.ReadLock(dir)
	if lock.Plugins["acme/hello"].Version != "1.1.0" || lock.Plugins["acme/hi"].Version != "1.0.1" {
		t.Errorf("cicord.lock = %+v", lock.Plugins)
	}
	if code, out, _ := run(t, "plugin", "update", "--dir", dir); code != 0 || !strings.Contains(out, "up to date") {
		t.Errorf("list after --all: %d %s", code, out)
	}
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Errorf("validate after --all: %s", errOut)
	}
}
