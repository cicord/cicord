package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/webui"
)

func fakeTgz(t *testing.T, manifest string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range map[string]string{"package/package.json": manifest, "package/dist/index.mjs": "export {}"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func useKit(t *testing.T, kit fs.FS, err error) {
	t.Helper()
	old := portalKit
	portalKit = func() (fs.FS, error) { return kit, err }
	t.Cleanup(func() { portalKit = old })
}

func TestPortalInit(t *testing.T) {
	kit := fstest.MapFS{
		"packages/cicord-client-0.1.0.tgz": {Data: fakeTgz(t, `{"name":"@cicord/client","version":"0.1.0"}`)},
		"packages/cicord-react-0.1.0.tgz":  {Data: fakeTgz(t, `{"name":"@cicord/react","version":"0.1.0","dependencies":{"@cicord/client":"0.1.0"}}`)},
		"packages/cicord-ui-0.1.0.tgz":     {Data: fakeTgz(t, `{"name":"@cicord/ui","version":"0.1.0"}`)},
		"template/package.json": {Data: []byte(`{"name":"portal","private":true,"version":"0.0.0","type":"module",
			"scripts":{"build":"tsc --noEmit && vp build","dev:runtime":"vp dev"},
			"dependencies":{"@cicord/client":"^0.1.0","@cicord/react":"^0.1.0","@cicord/ui":"^0.1.0","react":"^19.2.0"},
			"devDependencies":{"vite":"npm:@voidzero-dev/vite-plus-core@0.2.7","vite-plus":"0.2.7"},
			"pnpm":{"overrides":{"vite":"npm:@voidzero-dev/vite-plus-core@0.2.7"}}}`)},
		"template/.gitignore":     {Data: []byte("node_modules/\ndist/\n")},
		"template/index.html":     {Data: []byte("<html></html>")},
		"template/src/main.tsx":   {Data: []byte("// main")},
		"template/vite.config.ts": {Data: []byte("// vite")},
	}
	useKit(t, kit, nil)

	repo := t.TempDir()
	if code, _, stderr := run(t, "init", repo, "--project", "fans"); code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	t.Chdir(repo)
	code, out, stderr := run(t, "portal", "init")
	if code != 0 {
		t.Fatalf("portal init: %d %s", code, stderr)
	}
	if !strings.Contains(out, "path: ./portal/dist") {
		t.Errorf("next steps:\n%s", out)
	}
	for _, f := range []string{"portal/index.html", "portal/src/main.tsx", "portal/vite.config.ts",
		"portal/vendor/cicord-client-0.1.0.tgz", "portal/vendor/cicord-react-0.1.0.tgz", "portal/vendor/cicord-ui-0.1.0.tgz",
		".github/workflows/cicord-portal.yml"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "portal/vendor/cicord-react-0.1.0.tgz")); !bytes.Equal(got, kit["packages/cicord-react-0.1.0.tgz"].Data) {
		t.Error("vendored tarball differs from the kit")
	}
	if gi := readFile(t, filepath.Join(repo, "portal/.gitignore")); strings.Contains(gi, "\ndist/") || !strings.Contains(gi, "node_modules/") {
		t.Errorf(".gitignore must keep dist/ committed:\n%s", gi)
	}
	var pj struct {
		Name            string
		Dependencies    map[string]string
		DevDependencies map[string]string
		Pnpm            map[string]any
		Overrides       map[string]string
	}
	raw := readFile(t, filepath.Join(repo, "portal/package.json"))
	if err := json.Unmarshal([]byte(raw), &pj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"tsc --noEmit && vp build"`) {
		t.Errorf("package.json scripts escaped:\n%s", raw)
	}
	if pj.Pnpm != nil {
		t.Errorf("package.json keeps pnpm settings (ignored by pnpm 10+): %v", pj.Pnpm)
	}
	if pj.Name != "fans-portal" || pj.Dependencies["react"] != "^19.2.0" {
		t.Errorf("package.json = %+v", pj)
	}
	// pnpm reads overrides from pnpm-workspace.yaml: @cicord/react's dependency on
	// @cicord/client must resolve to the vendored tarball, not to the npm registry.
	var ws struct {
		Overrides           map[string]string `yaml:"overrides"`
		PeerDependencyRules map[string]any    `yaml:"peerDependencyRules"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(repo, "portal/pnpm-workspace.yaml"))), &ws); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client", "react", "ui"} {
		want := "file:vendor/cicord-" + name + "-0.1.0.tgz"
		if pj.Dependencies["@cicord/"+name] != want || ws.Overrides["@cicord/"+name] != want || pj.Overrides["@cicord/"+name] != want {
			t.Errorf("@cicord/%s = %q / %q / %q", name, pj.Dependencies["@cicord/"+name], ws.Overrides["@cicord/"+name], pj.Overrides["@cicord/"+name])
		}
	}
	if ws.Overrides["vite"] != "npm:@voidzero-dev/vite-plus-core@0.2.7" || ws.PeerDependencyRules == nil {
		t.Errorf("vite override or peer rules lost: %+v", ws)
	}
	wf := readFile(t, filepath.Join(repo, ".github/workflows/cicord-portal.yml"))
	// Actions pinned by commit SHA: organisations may require it (sha_pinning_required).
	for _, line := range strings.Split(wf, "\n") {
		if u, ok := strings.CutPrefix(strings.TrimSpace(line), "- uses: "); ok {
			if _, ref, _ := strings.Cut(u, "@"); len(strings.Fields(ref)) == 0 || len(strings.Fields(ref)[0]) != 40 {
				t.Errorf("action not pinned by SHA: %s", line)
			}
		}
	}
	for _, want := range []string{"working-directory: portal", "pnpm install --frozen-lockfile", "git status --porcelain -- dist", `paths: ["portal/**", "plugins/**"`, "cicord portal typegen --check --out portal/src/cicord-routes.d.ts", "cicord portal vendor --check portal",
		// The release binary (it embeds the portal kit), verified; not a `go install` build.
		`CICORD_TAG: "` + cicordInstallVersion() + `"`, "gh release download \"$tag\" --repo cicord/cicord", "sha256sum -c -",
		"gh attestation verify \"$archive\" --repo cicord/cicord"} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow misses %q:\n%s", want, wf)
		}
	}
	checkWorkflowYAML(t, wf)
	if strings.Contains(wf, "run: go install") || strings.Contains(wf, "setup-go") || strings.Contains(wf, "[[") {
		t.Errorf("workflow builds cicord from source or has a template leftover:\n%s", wf)
	}

	// A second run refuses to overwrite.
	if code, _, stderr := run(t, "portal", "init"); code == 0 || !strings.Contains(stderr, "not empty") {
		t.Errorf("second init = %d %s", code, stderr)
	}
}

func TestPortalInitWithoutKit(t *testing.T) {
	useKit(t, nil, webui.ErrNoKit)
	t.Chdir(t.TempDir())
	if code, _, stderr := run(t, "portal", "init"); code == 0 || !strings.Contains(stderr, "make ui build") {
		t.Fatalf("portal init without kit = %d %s", code, stderr)
	}
}

func TestPortalBuildNeedsPath(t *testing.T) {
	repo := t.TempDir()
	if code, _, stderr := run(t, "init", repo); code != 0 {
		t.Fatal(stderr)
	}
	if code, _, stderr := run(t, "portal", "build", repo); code == 0 || !strings.Contains(stderr, "spec.portal.path") {
		t.Fatalf("portal build = %d %s", code, stderr)
	}
}

func TestEmbeddedKitIsConsistent(t *testing.T) {
	kit, err := webui.PortalKit()
	if err != nil {
		t.Skip("portal kit not generated (make ui)")
	}
	pkgs, err := readKitPackages(kit)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range pkgs {
		names[p.Name] = true
	}
	for _, n := range []string{"@cicord/sdk", "@cicord/client", "@cicord/react", "@cicord/ui"} {
		if !names[n] {
			t.Errorf("kit misses %s (%v)", n, names)
		}
	}
}

// TestPortalVendor: after a cicord upgrade, `portal vendor` swaps the vendored tarballs
// for this binary's and rewrites their references in place; --check gates CI.
func TestPortalVendor(t *testing.T) {
	kitAt := func(v string) fstest.MapFS {
		return fstest.MapFS{
			"packages/cicord-client-" + v + ".tgz": {Data: fakeTgz(t, `{"name":"@cicord/client","version":"`+v+`"}`)},
			"packages/cicord-react-" + v + ".tgz":  {Data: fakeTgz(t, `{"name":"@cicord/react","version":"`+v+`"}`)},
			"packages/cicord-ui-" + v + ".tgz":     {Data: fakeTgz(t, `{"name":"@cicord/ui","version":"`+v+`"}`)},
			"template/package.json": {Data: []byte(`{"name":"portal","private":true,"version":"0.0.0","type":"module",
				"scripts":{"build":"vp build"},
				"dependencies":{"@cicord/client":"^0.1.0","@cicord/react":"^0.1.0","@cicord/ui":"^0.1.0","react":"^19.2.0"}}`)},
			"template/index.html": {Data: []byte("<html></html>")},
		}
	}
	useKit(t, kitAt("0.1.0"), nil)
	dir := filepath.Join(t.TempDir(), "portal")
	if code, _, errOut := run(t, "portal", "init", dir); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, out, _ := run(t, "portal", "vendor", dir, "--check"); code != 0 || !strings.Contains(out, "this version's") {
		t.Fatalf("fresh portal --check = %d %s", code, out)
	}

	useKit(t, kitAt("0.1.1"), nil)
	if code, out, errOut := run(t, "portal", "vendor", dir, "--check"); code == 0 || !strings.Contains(out, "@cicord/client 0.1.0 → 0.1.1") || !strings.Contains(errOut, "run `cicord portal vendor`") {
		t.Fatalf("stale --check = %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "cicord-client-0.1.1.tgz")); err == nil {
		t.Fatal("--check wrote a tarball")
	}
	if code, out, errOut := run(t, "portal", "vendor", dir); code != 0 || !strings.Contains(out, "Updated") {
		t.Fatalf("vendor = %d %s %s", code, out, errOut)
	}
	for _, f := range []string{"package.json", "pnpm-workspace.yaml"} {
		got := readFile(t, filepath.Join(dir, f))
		if strings.Contains(got, "0.1.0.tgz") || !strings.Contains(got, "file:vendor/cicord-react-0.1.1.tgz") {
			t.Errorf("%s:\n%s", f, got)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "package.json")), `"react": "^19.2.0"`) {
		t.Error("package.json: other dependencies changed")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "vendor"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "cicord-client-0.1.1.tgz cicord-react-0.1.1.tgz cicord-ui-0.1.1.tgz" {
		t.Errorf("vendor/ = %v", names)
	}
	if code, out, _ := run(t, "portal", "vendor", dir, "--check"); code != 0 {
		t.Errorf("--check after vendor = %d %s", code, out)
	}

	// A portal on the npm registry is left alone.
	npm := filepath.Join(t.TempDir(), "npm-portal")
	if code, _, errOut := run(t, "portal", "init", npm, "--from-npm"); code != 0 {
		t.Fatalf("init --from-npm: %s", errOut)
	}
	if code, out, _ := run(t, "portal", "vendor", npm); code != 0 || !strings.Contains(out, "nothing to do") {
		t.Errorf("npm portal = %d %s", code, out)
	}
}

// checkWorkflowYAML parses a generated GitHub workflow: every job has steps, and every
// step uses an action or runs a script.
func checkWorkflowYAML(t *testing.T, wf string) {
	t.Helper()
	var doc struct {
		On   map[string]any `yaml:"on"`
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(wf), &doc); err != nil {
		t.Fatalf("workflow is not valid YAML: %v\n%s", err, wf)
	}
	if len(doc.On) == 0 || len(doc.Jobs) == 0 {
		t.Fatalf("workflow has no triggers or jobs:\n%s", wf)
	}
	for name, job := range doc.Jobs {
		if len(job.Steps) == 0 {
			t.Errorf("job %s has no steps", name)
		}
		for i, st := range job.Steps {
			_, uses := st["uses"].(string)
			_, run := st["run"].(string)
			if uses == run {
				t.Errorf("job %s step %d: needs exactly one of uses and run: %v", name, i, st)
			}
		}
	}
}
