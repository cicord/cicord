package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/npjonath/cicord/internal/webui"
)

func TestPluginNewTestSetup(t *testing.T) {
	kit := minimalKit(t)
	useKit(t, kit, nil)
	dir := filepath.Join(t.TempDir(), "hello")
	code, out, stderr := run(t, "plugin", "new", dir)
	if code != 0 {
		t.Fatalf("plugin new: %s", stderr)
	}
	if !strings.Contains(out, "npm install && npm test") {
		t.Errorf("no test hint:\n%s", out)
	}
	for _, f := range []string{"package.json", "test/hello.test.ts", ".gitignore", "vendor/cicord-sdk-0.1.0.tgz", "src/index.ts"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "vendor", "cicord-sdk-0.1.0.tgz")); !bytes.Equal(got, kit["packages/cicord-sdk-0.1.0.tgz"].Data) {
		t.Error("vendored @cicord/sdk differs from the kit")
	}
	var pj struct {
		Name            string
		Type            string
		Scripts         map[string]string
		DevDependencies map[string]string
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "package.json"))), &pj); err != nil {
		t.Fatal(err)
	}
	if pj.Name != "hello" || pj.Type != "module" || pj.DevDependencies["@cicord/sdk"] != "file:vendor/cicord-sdk-0.1.0.tgz" ||
		!strings.HasPrefix(pj.Scripts["test"], "node --test") {
		t.Errorf("package.json = %+v", pj)
	}
	test := readFile(t, filepath.Join(dir, "test", "hello.test.ts"))
	for _, want := range []string{`from "@cicord/sdk/testing"`, `from "../src/index.ts"`, `from "node:test"`, `runCommand(plugin, t, "hello"`} {
		if !strings.Contains(test, want) {
			t.Errorf("test misses %q:\n%s", want, test)
		}
	}
	if gi := readFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(gi, "node_modules/") {
		t.Errorf(".gitignore = %q", gi)
	}
	// The plugin still builds with its test setup around it.
	if code, out, _ := run(t, "plugin", "build", dir); code != 0 {
		t.Errorf("plugin build: %s", out)
	}
	// A second run refuses to overwrite.
	if code, _, stderr := run(t, "plugin", "new", dir); code == 0 || !strings.Contains(stderr, "already exists") {
		t.Errorf("second plugin new = %d %s", code, stderr)
	}
}

func TestFromNPM(t *testing.T) {
	useKit(t, minimalKit(t), nil)
	plugin := filepath.Join(t.TempDir(), "hello")
	if code, _, stderr := run(t, "plugin", "new", plugin, "--from-npm"); code != 0 {
		t.Fatal(stderr)
	}
	if pj := readFile(t, filepath.Join(plugin, "package.json")); !strings.Contains(pj, `"@cicord/sdk": "^0.1.0"`) {
		t.Errorf("plugin package.json:\n%s", pj)
	}
	if _, err := os.Stat(filepath.Join(plugin, "vendor")); err == nil {
		t.Error("vendor/ written with --from-npm")
	}

	t.Setenv("CICORD_FROM_NPM", "true") // same as --from-npm
	portal := filepath.Join(t.TempDir(), "portal")
	if code, _, stderr := run(t, "portal", "init", portal); code != 0 {
		t.Fatal(stderr)
	}
	var pj struct {
		Dependencies map[string]string
		Overrides    map[string]string
		Pnpm         struct{ Overrides map[string]string }
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(portal, "package.json"))), &pj); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"@cicord/client", "@cicord/react", "@cicord/ui"} {
		if pj.Dependencies[n] != "^0.1.0" || pj.Overrides[n] != "" || pj.Pnpm.Overrides[n] != "" {
			t.Errorf("%s = %q (overrides %q %q)", n, pj.Dependencies[n], pj.Overrides[n], pj.Pnpm.Overrides[n])
		}
	}
	if _, err := os.Stat(filepath.Join(portal, "vendor")); err == nil {
		t.Error("vendor/ written with CICORD_FROM_NPM")
	}
}

func TestPluginNewWithoutKit(t *testing.T) {
	useKit(t, nil, webui.ErrNoKit)
	dir := filepath.Join(t.TempDir(), "hello")
	code, out, stderr := run(t, "plugin", "new", dir)
	if code != 0 {
		t.Fatalf("plugin new without kit: %s", stderr)
	}
	if !strings.Contains(out, "No unit test setup") || !strings.Contains(out, "make ui build") {
		t.Errorf("no hint:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		t.Error("package.json written without the SDK tarball")
	}
}

// TestPluginNewTestsRunWithNode scaffolds a plugin outside the repository with the kit
// embedded in this binary and runs its unit tests for real: `npm install --offline`
// (the only dependency is the vendored @cicord/sdk tarball) then `npm test`.
func TestPluginNewTestsRunWithNode(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	kit, err := webui.PortalKit()
	if err != nil {
		t.Skip("portal kit not generated (make ui)")
	}
	if pkgs, _ := readKitPackages(kit); !hasPackage(pkgs, sdkPackage) {
		t.Skip("the embedded kit has no @cicord/sdk (make ui)")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skip("npm not installed")
	}
	dir := filepath.Join(t.TempDir(), "hello")
	if code, _, stderr := run(t, "plugin", "new", dir); code != 0 {
		t.Fatal(stderr)
	}
	for _, args := range [][]string{{"install", "--offline", "--no-audit", "--no-fund"}, {"test"}} {
		c := exec.Command(npm, args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "npm_config_update_notifier=false")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("npm %s: %v\n%s", strings.Join(args, " "), err, out)
		} else if args[0] == "test" && !strings.Contains(string(out), "pass 2") {
			t.Errorf("npm test:\n%s", out)
		}
	}
}

func hasPackage(pkgs []kitPackage, name string) bool {
	for _, p := range pkgs {
		if p.Name == name {
			return true
		}
	}
	return false
}
