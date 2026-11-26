package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/api"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/sim"
)

func TestPluginBuild(t *testing.T) {
	code, out, errOut := run(t, "plugin", "build", "../../examples/basic/plugins/onboarding", "-o", "json")
	if code != 0 {
		t.Fatalf("build = %d\n%s\n%s", code, out, errOut)
	}
	var r pluginReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Name != "onboarding" || len(r.Commands) == 0 || len(r.Routes) == 0 || r.BundleBytes == 0 {
		t.Errorf("report = %+v", r)
	}
	code, out, _ = run(t, "plugin", "build", "../../examples/basic/plugins/welcome")
	if code != 0 || !strings.Contains(out, "Plugin welcome 0.1.1") || !strings.Contains(out, "Events:\n  memberJoin") ||
		!strings.Contains(out, "Required intents: ServerMembers") {
		t.Errorf("build text = %d\n%s", code, out)
	}

	// a handler needing an intent the manifest does not declare is an error
	dir := t.TempDir()
	copyDir(t, "../../examples/basic/plugins/welcome", dir)
	m := filepath.Join(dir, "plugin.yaml")
	writeFile(t, m, strings.Replace(readFile(t, m), "  intents: [ServerMembers]\n", "", 1))
	code, out, _ = run(t, "plugin", "build", dir)
	if code != 1 || !strings.Contains(out, "error: a handler needs the ServerMembers intent") {
		t.Errorf("missing intent = %d\n%s", code, out)
	}
	// an intent no handler needs is a warning, except ServerMembers with discord.members.read
	// (role.members())
	ob := t.TempDir()
	copyDir(t, "../../examples/basic/plugins/onboarding", ob)
	om := filepath.Join(ob, "plugin.yaml")
	writeFile(t, om, strings.Replace(readFile(t, om), "  config:\n", "  intents: [ServerMembers]\n  config:\n", 1))
	if code, out, _ := run(t, "plugin", "build", ob); code != 0 || !strings.Contains(out, "warning: plugin.yaml declares the ServerMembers intent, which no handler needs") {
		t.Errorf("unused intent = %d\n%s", code, out)
	}
	writeFile(t, om, strings.Replace(readFile(t, om), "    - storage.kv\n", "    - storage.kv\n    - discord.members.read\n", 1))
	if code, out, _ := run(t, "plugin", "build", ob); code != 0 || strings.Contains(strings.ReplaceAll(out, "warning: legacy event topics are deprecated;", ""), "warning:") {
		t.Errorf("ServerMembers with discord.members.read = %d\n%s", code, out)
	}
	// a syntax error is reported with its location
	writeFile(t, filepath.Join(dir, "src", "index.ts"), "export default {\n")
	if code, out, _ := run(t, "plugin", "build", dir); code != 1 || !strings.Contains(out, "src/index.ts:") {
		t.Errorf("syntax error = %d\n%s", code, out)
	}
}

func TestPluginTypegen(t *testing.T) {
	for _, p := range []string{"welcome", "onboarding"} {
		dir := t.TempDir()
		copyDir(t, "../../examples/basic/plugins/"+p, dir)
		// the committed example files are up to date
		if code, _, errOut := run(t, "plugin", "typegen", dir, "--check"); code != 0 {
			t.Errorf("%s: typegen --check = %d: %s", p, code, errOut)
		}
		writeFile(t, filepath.Join(dir, "src", "cicord-env.d.ts"), "stale")
		if code, _, _ := run(t, "plugin", "typegen", dir, "--check"); code != 1 {
			t.Errorf("%s: stale file passes --check", p)
		}
		if code, _, _ := run(t, "plugin", "typegen", dir); code != 0 {
			t.Errorf("%s: typegen failed", p)
		}
		got := readFile(t, filepath.Join(dir, "src", "cicord-env.d.ts"))
		if got != readFile(t, filepath.Join("../../examples/basic/plugins", p, "src", "cicord-env.d.ts")) {
			t.Errorf("%s: generated file differs from the example:\n%s", p, got)
		}
	}
}

func TestPluginTest(t *testing.T) {
	code, out, errOut := run(t, "plugin", "test", "../../examples/basic/plugins/welcome")
	if code != 0 || !strings.Contains(out, "ok   instance welcome started") || !strings.Contains(out, "memberJoin: tester joined") ||
		!strings.Contains(out, "#test-channel") || !strings.Contains(out, "PASS") {
		t.Fatalf("plugin test welcome = %d\n%s\n%s", code, out, errOut)
	}
	// onboarding needs its steps
	code, _, errOut = run(t, "plugin", "test", "../../examples/basic/plugins/onboarding", "--script=false")
	if code != 1 || !strings.Contains(errOut, "--config") {
		t.Errorf("onboarding without config = %d: %s", code, errOut)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, cfg, "steps:\n  - id: games\n    title: Games\n    options:\n      - role: minecraft\n")
	code, out, errOut = run(t, "plugin", "test", "../../examples/basic/plugins/onboarding", "--config", cfg)
	if code != 0 || !strings.Contains(out, "commands registered: /onboarding") || !strings.Contains(out, "PASS") {
		t.Errorf("onboarding = %d\n%s\n%s", code, out, errOut)
	}
}

func TestInitWorkflow(t *testing.T) {
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "bot"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	wf := readFile(t, filepath.Join(dir, ".github", "workflows", "cicord.yml"))
	for _, want := range []string{
		"github.com/cicord/cicord/cmd/cicord@" + cicordInstallVersion(), "cicord validate", "cicord plan --remote --comment",
		"cicord status --wait --revision ${{ github.sha }}", "CICORD_RUNTIME_URL: ${{ vars.CICORD_RUNTIME_URL }}",
		"CICORD_RUNTIME_TOKEN: ${{ secrets.CICORD_RUNTIME_TOKEN }}", "pull-requests: write", "contents: read",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow misses %q:\n%s", want, wf)
		}
	}
	if strings.Contains(wf, "@latest") && cicordInstallVersion() != "latest" {
		t.Errorf("workflow is not pinned to this cicord:\n%s", wf)
	}
	checkWorkflowYAML(t, wf)
	// Actions pinned by commit SHA: organisations may require it (sha_pinning_required).
	for _, line := range strings.Split(wf, "\n") {
		if u, ok := strings.CutPrefix(strings.TrimSpace(line), "- uses: "); ok {
			if _, ref, _ := strings.Cut(u, "@"); len(strings.Fields(ref)) == 0 || len(strings.Fields(ref)[0]) != 40 {
				t.Errorf("action not pinned by SHA: %s", line)
			}
		}
	}
	// the example repository ships the same workflow (scripts/release.sh bumps its version)
	// The example is a known public snapshot or release. Generated workflows pin the
	// generating binary's revision, which can be a subsequent commit.
	example := readFile(t, "../../examples/basic/.github/workflows/cicord.yml")
	installs := regexp.MustCompile(`(?m)^      - run: go install [^\n]*github\.com/cicord/cicord/cmd/cicord@(v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?|[a-f0-9]{40})$`)
	if len(installs.FindAllString(example, -1)) != 3 || installs.ReplaceAllString(example, "      - run: install-cicord") != installs.ReplaceAllString(wf, "      - run: install-cicord") {
		t.Errorf("example workflow and generated workflow disagree: %s", wf)
	}
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Errorf("validate init repo: %s", errOut)
	}
	// `cicord dev --sim` seeds the simulator with the repository IDs: the placeholders of a
	// fresh repository must be accepted (the "Next steps" say to run it).
	repo, ds := config.Load(dir, config.LoadOptions{})
	if repo == nil || ds.HasErrors() {
		t.Fatalf("load: %v", ds)
	}
	if _, err := api.SeedSimWorld(sim.NewWorld(), repo); err != nil {
		t.Errorf("seed the simulator from a fresh repository: %v", err)
	}
}

// TestShowcasePlugins: every plugin of examples/showcase builds without warnings, has an
// up-to-date cicord-env.d.ts and passes `cicord plugin test` without a --config.
func TestShowcasePlugins(t *testing.T) {
	for _, name := range []string{"tickets", "polls", "levels", "moderation", "reaction-roles", "audit-log", "announcements"} {
		t.Run(name, func(t *testing.T) {
			dir := "../../examples/showcase/plugins/" + name
			code, out, errOut := run(t, "plugin", "build", dir)
			if code != 0 || strings.Contains(strings.ReplaceAll(out, "warning: legacy event topics are deprecated;", ""), "warning:") {
				t.Errorf("build = %d\n%s\n%s", code, out, errOut)
			}
			if code, _, errOut := run(t, "plugin", "typegen", dir, "--check"); code != 0 {
				t.Errorf("typegen --check: %s", errOut)
			}
			code, out, errOut = run(t, "plugin", "test", dir)
			if code != 0 || !strings.Contains(out, "PASS") {
				t.Errorf("plugin test = %d\n%s\n%s", code, out, errOut)
			}
		})
	}
}

func TestPluginNewInRepo(t *testing.T) {
	dir := t.TempDir()
	if code, _, errOut := run(t, "init", dir, "--server", "srv", "--bot", "helper"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	code, out, errOut := run(t, "plugin", "new", filepath.Join(dir, "plugins", "hello"))
	if code != 0 {
		t.Fatalf("plugin new: %s", errOut)
	}
	// the hint names the bot file and the source path relative to it
	if !strings.Contains(out, "bot.yaml (spec.plugins)") || !strings.Contains(out, "source: ../../plugins/hello") {
		t.Errorf("plugin new output:\n%s", out)
	}
	if code, _, errOut := run(t, "plugin", "typegen", filepath.Join(dir, "plugins", "hello"), "--check"); code != 0 {
		t.Errorf("fresh plugin typegen --check: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "hello", ".cicord", "cicord-sdk.d.ts")); err != nil {
		t.Error(err)
	}
}

func TestPluginBuildLiteralEventVersions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "contracts"), 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "plugin.yaml")
	base := "apiVersion: cicord.dev/v1alpha1\nkind: Plugin\nmetadata: {name: polls, version: 1.0.0}\nspec:\n  engine: '>=0.1.0'\n  entry: ./index.ts\n  capabilities: [events.publish]\n"
	writeFile(t, file, base+"  delivery: at-least-once\n  interop:\n    publishes: [{topic: closed, version: 2}]\n")
	writeFile(t, filepath.Join(dir, "contracts", "closed.v2.json"), `{"type":"object"}`)
	code := func(body string) {
		writeFile(t, filepath.Join(dir, "index.ts"), `import {definePlugin} from "@cicord/sdk";export default definePlugin({async onStart(ctx){`+body+`}});`)
	}
	for _, tc := range []struct {
		body string
		exit int
	}{{`await ctx.events.publish("closed",{}, {version:2})`, 0}, {`await ctx.events.publish("clsoed",{}, {version:2})`, 1}, {`await ctx.events.publish("closed",{}, {version:9})`, 1}, {`await ctx.events.publish(ctx.config.topic,{}, {version:ctx.config.version})`, 0}} {
		code(tc.body)
		n, out, err := run(t, "plugin", "build", dir)
		if n != tc.exit {
			t.Fatalf("build = %d (%s): %s %s", n, tc.body, out, err)
		}
	}
	writeFile(t, file, base)
	code(`await ctx.events.publish("arbitrary",{})`)
	if n, out, _ := run(t, "plugin", "build", dir); n != 0 {
		t.Fatal(out)
	}
}
