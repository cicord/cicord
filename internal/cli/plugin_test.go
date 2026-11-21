package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
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
	if code != 0 || !strings.Contains(out, "Plugin welcome 0.1.0") || !strings.Contains(out, "Events:\n  memberJoin") ||
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
		writeFile(t, filepath.Join(dir, "src", "codi-env.d.ts"), "stale")
		if code, _, _ := run(t, "plugin", "typegen", dir, "--check"); code != 1 {
			t.Errorf("%s: stale file passes --check", p)
		}
		if code, _, _ := run(t, "plugin", "typegen", dir); code != 0 {
			t.Errorf("%s: typegen failed", p)
		}
		got := readFile(t, filepath.Join(dir, "src", "codi-env.d.ts"))
		if got != readFile(t, filepath.Join("../../examples/basic/plugins", p, "src", "codi-env.d.ts")) {
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
	wf := readFile(t, filepath.Join(dir, ".github", "workflows", "codi.yml"))
	for _, want := range []string{
		"go install github.com/npjonath/codi/cmd/codi@latest", "codi validate", "codi plan --remote --comment",
		"codi status --wait --revision ${{ github.sha }}", "CODI_RUNTIME_URL: ${{ vars.CODI_RUNTIME_URL }}",
		"CODI_RUNTIME_TOKEN: ${{ secrets.CODI_RUNTIME_TOKEN }}", "pull-requests: write", "contents: read",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow misses %q:\n%s", want, wf)
		}
	}
	// the example repository ships the same workflow
	if ex := readFile(t, "../../examples/basic/.github/workflows/codi.yml"); ex != wf {
		t.Errorf("examples/basic workflow differs from the init template:\n%s", ex)
	}
	if code, _, errOut := run(t, "validate", dir); code != 0 {
		t.Errorf("validate init repo: %s", errOut)
	}
}
