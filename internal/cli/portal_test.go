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

// testKit is a portal kit at version v: the three portal packages and a template shaped like
// the reference portal's (a Vite+ override of vite).
func testKit(t *testing.T, v string) fstest.MapFS {
	return fstest.MapFS{
		"packages/cicord-client-" + v + ".tgz": {Data: fakeTgz(t, `{"name":"@cicord/client","version":"`+v+`"}`)},
		"packages/cicord-react-" + v + ".tgz":  {Data: fakeTgz(t, `{"name":"@cicord/react","version":"`+v+`","dependencies":{"@cicord/client":"`+v+`"}}`)},
		"packages/cicord-ui-" + v + ".tgz":     {Data: fakeTgz(t, `{"name":"@cicord/ui","version":"`+v+`"}`)},
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
}

// useVersion sets the version of this cicord.
func useVersion(t *testing.T, v string) {
	t.Helper()
	old := cicordVersion
	cicordVersion = func() string { return v }
	t.Cleanup(func() { cicordVersion = old })
}

type scaffoldedPortal struct {
	Name            string
	Dependencies    map[string]string
	DevDependencies map[string]string
	Pnpm            map[string]any
	Overrides       map[string]string
	ws              struct {
		Overrides           map[string]string `yaml:"overrides"`
		PeerDependencyRules map[string]any    `yaml:"peerDependencyRules"`
	}
	raw string
}

func readScaffoldedPortal(t *testing.T, dir string) scaffoldedPortal {
	t.Helper()
	var pj scaffoldedPortal
	pj.raw = readFile(t, filepath.Join(dir, "package.json"))
	if err := json.Unmarshal([]byte(pj.raw), &pj); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, "pnpm-workspace.yaml"))), &pj.ws); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pj.raw, `"tsc --noEmit && vp build"`) {
		t.Errorf("package.json scripts escaped:\n%s", pj.raw)
	}
	if pj.Pnpm != nil {
		t.Errorf("package.json keeps pnpm settings (ignored by pnpm 10+): %v", pj.Pnpm)
	}
	if pj.ws.Overrides["vite"] != "npm:@voidzero-dev/vite-plus-core@0.2.7" || pj.ws.PeerDependencyRules == nil {
		t.Errorf("vite override or peer rules lost: %+v", pj.ws)
	}
	return pj
}

// TestPortalInit: by default the portal depends on the npm packages at the exact version of
// this cicord, with no vendor/ and no @cicord/* override.
func TestPortalInit(t *testing.T) {
	useKit(t, testKit(t, "0.1.0"), nil)
	useVersion(t, "0.1.8")

	repo := t.TempDir()
	if code, _, stderr := run(t, "init", repo, "--project", "fans"); code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	t.Chdir(repo)
	code, out, stderr := run(t, "portal", "init")
	if code != 0 {
		t.Fatalf("portal init: %d %s", code, stderr)
	}
	if !strings.Contains(out, "path: ./portal/dist") || !strings.Contains(out, "@cicord/ui 0.1.8 from npm") {
		t.Errorf("next steps:\n%s", out)
	}
	for _, f := range []string{"portal/index.html", "portal/src/main.tsx", "portal/vite.config.ts", ".github/workflows/cicord-portal.yml"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "portal/vendor")); err == nil {
		t.Error("portal/vendor written in npm mode")
	}
	if gi := readFile(t, filepath.Join(repo, "portal/.gitignore")); strings.Contains(gi, "\ndist/") || !strings.Contains(gi, "node_modules/") {
		t.Errorf(".gitignore must keep dist/ committed:\n%s", gi)
	}
	pj := readScaffoldedPortal(t, filepath.Join(repo, "portal"))
	if pj.Name != "fans-portal" || pj.Dependencies["react"] != "^19.2.0" {
		t.Errorf("package.json = %+v", pj)
	}
	for _, name := range portalSDKPackages {
		if pj.Dependencies[name] != "0.1.8" {
			t.Errorf("%s = %q, want the exact version 0.1.8", name, pj.Dependencies[name])
		}
		if _, ok := pj.ws.Overrides[name]; ok {
			t.Errorf("pnpm-workspace.yaml overrides %s", name)
		}
		if _, ok := pj.Overrides[name]; ok {
			t.Errorf("package.json overrides %s", name)
		}
	}
	if code, out, _ := run(t, "portal", "upgrade", "--check"); code != 0 || !strings.Contains(out, "this cicord's version (0.1.8, npm)") {
		t.Errorf("upgrade --check of a fresh portal = %d %s", code, out)
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
	for _, want := range []string{"working-directory: portal", "pnpm install --frozen-lockfile", "git status --porcelain -- dist", `paths: ["portal/**", "plugins/**"`, "cicord portal typegen --check --out portal/src/cicord-routes.d.ts",
		"run: cicord portal upgrade --check portal", "The @cicord/* packages are this cicord's version",
		// The release binary (it embeds the portal kit), verified; not a `go install` build.
		`CICORD_TAG: "v0.1.8"`, "gh release download \"$tag\" --repo cicord/cicord", "sha256sum -c -",
		"gh attestation verify \"$archive\" --repo cicord/cicord"} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow misses %q:\n%s", want, wf)
		}
	}
	checkWorkflowYAML(t, wf)
	if strings.Contains(wf, "run: go install") || strings.Contains(wf, "setup-go") || strings.Contains(wf, "[[") || strings.Contains(wf, "portal vendor") {
		t.Errorf("workflow builds cicord from source, has a template leftover or runs portal vendor:\n%s", wf)
	}

	// A second run refuses to overwrite.
	if code, _, stderr := run(t, "portal", "init"); code == 0 || !strings.Contains(stderr, "not empty") {
		t.Errorf("second init = %d %s", code, stderr)
	}
}

// TestPortalInitVendor: --vendor writes the tarballs of the kit in vendor/, file:
// dependencies on them and the overrides that keep @cicord/react's @cicord/client there.
func TestPortalInitVendor(t *testing.T) {
	kit := testKit(t, "0.1.0")
	useKit(t, kit, nil)
	useVersion(t, "0.1.8")
	dir := filepath.Join(t.TempDir(), "portal")
	code, out, stderr := run(t, "portal", "init", dir, "--vendor")
	if code != 0 {
		t.Fatalf("portal init --vendor: %d %s", code, stderr)
	}
	if !strings.Contains(out, "vendored in") || strings.Contains(out, "development build") {
		t.Errorf("output:\n%s", out)
	}
	for _, name := range []string{"client", "react", "ui"} {
		f := "packages/cicord-" + name + "-0.1.0.tgz"
		if got, _ := os.ReadFile(filepath.Join(dir, "vendor", filepath.Base(f))); !bytes.Equal(got, kit[f].Data) {
			t.Errorf("vendor/%s differs from the kit", filepath.Base(f))
		}
	}
	pj := readScaffoldedPortal(t, dir)
	// pnpm reads overrides from pnpm-workspace.yaml: @cicord/react's dependency on
	// @cicord/client must resolve to the vendored tarball, not to the npm registry.
	for _, name := range []string{"client", "react", "ui"} {
		want := "file:vendor/cicord-" + name + "-0.1.0.tgz"
		if pj.Dependencies["@cicord/"+name] != want || pj.ws.Overrides["@cicord/"+name] != want || pj.Overrides["@cicord/"+name] != want {
			t.Errorf("@cicord/%s = %q / %q / %q", name, pj.Dependencies["@cicord/"+name], pj.ws.Overrides["@cicord/"+name], pj.Overrides["@cicord/"+name])
		}
	}
}

// TestPortalInitDevBuild: a development build has no npm packages: init vendors and says why.
func TestPortalInitDevBuild(t *testing.T) {
	useKit(t, testKit(t, "0.1.0"), nil)
	useVersion(t, "0.2.0-dev")
	dir := filepath.Join(t.TempDir(), "portal")
	code, out, stderr := run(t, "portal", "init", dir)
	if code != 0 {
		t.Fatalf("portal init: %d %s", code, stderr)
	}
	if !strings.Contains(out, "development build") || !strings.Contains(out, "cicord portal upgrade --to-npm") {
		t.Errorf("no note about the fallback:\n%s", out)
	}
	if pj := readScaffoldedPortal(t, dir); pj.Dependencies["@cicord/client"] != "file:vendor/cicord-client-0.1.0.tgz" {
		t.Errorf("@cicord/client = %q", pj.Dependencies["@cicord/client"])
	}
}

// TestPortalInitFromNPMDeprecated: --from-npm, the former opt-in, is accepted (npm is the default).
func TestPortalInitFromNPMDeprecated(t *testing.T) {
	useKit(t, testKit(t, "0.1.0"), nil)
	useVersion(t, "0.1.8")
	dir := filepath.Join(t.TempDir(), "portal")
	if code, _, stderr := run(t, "portal", "init", dir, "--from-npm"); code != 0 {
		t.Fatalf("portal init --from-npm: %d %s", code, stderr)
	}
	if pj := readScaffoldedPortal(t, dir); pj.Dependencies["@cicord/client"] != "0.1.8" {
		t.Errorf("@cicord/client = %q", pj.Dependencies["@cicord/client"])
	}
	if code, out, _ := run(t, "portal", "init", "--help"); code != 0 || strings.Contains(out, "from-npm") || !strings.Contains(out, "--vendor") {
		t.Errorf("init --help:\n%s", out)
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

// TestPortalUpgradeNPM: in npm mode, `portal upgrade` sets the exact version of this cicord
// in package.json (nothing else changes); --check fails until then. No portal kit needed.
func TestPortalUpgradeNPM(t *testing.T) {
	useKit(t, testKit(t, "0.1.0"), nil)
	useVersion(t, "0.1.8")
	dir := filepath.Join(t.TempDir(), "portal")
	if code, _, errOut := run(t, "portal", "init", dir); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	before := readFile(t, filepath.Join(dir, "package.json"))
	wsBefore := readFile(t, filepath.Join(dir, "pnpm-workspace.yaml"))

	useKit(t, nil, webui.ErrNoKit) // e.g. a `go install` build
	useVersion(t, "0.1.9")
	if code, out, errOut := run(t, "portal", "upgrade", dir, "--check"); code != 1 || !strings.Contains(out, "@cicord/client 0.1.8 → 0.1.9 (npm)") || !strings.Contains(errOut, "run `cicord portal upgrade`") {
		t.Fatalf("stale --check = %d %s %s", code, out, errOut)
	}
	if readFile(t, filepath.Join(dir, "package.json")) != before {
		t.Fatal("--check changed package.json")
	}
	code, out, errOut := run(t, "portal", "upgrade", dir)
	if code != 0 || !strings.Contains(out, "Updated the @cicord/* packages") || !strings.Contains(out, "Next: pnpm install") {
		t.Fatalf("upgrade = %d %s %s", code, out, errOut)
	}
	if got, want := readFile(t, filepath.Join(dir, "package.json")), strings.ReplaceAll(before, `": "0.1.8"`, `": "0.1.9"`); got != want {
		t.Errorf("package.json:\n%s\nwant:\n%s", got, want)
	}
	if readFile(t, filepath.Join(dir, "pnpm-workspace.yaml")) != wsBefore {
		t.Error("upgrade changed pnpm-workspace.yaml")
	}
	if code, out, _ := run(t, "portal", "upgrade", dir, "--check"); code != 0 || !strings.Contains(out, "(0.1.9, npm)") {
		t.Errorf("--check after upgrade = %d %s", code, out)
	}

	// A range (`portal init --from-npm` before npm was the default) is not this version.
	writeFile(t, filepath.Join(dir, "package.json"), strings.ReplaceAll(before, `": "0.1.8"`, `": "^0.1.9"`))
	if code, out, _ := run(t, "portal", "upgrade", dir, "--check"); code != 1 || !strings.Contains(out, "^0.1.9 → 0.1.9") {
		t.Errorf("--check of a range = %d %s", code, out)
	}
	// An @cicord/* override would win over the version.
	writeFile(t, filepath.Join(dir, "package.json"), strings.Replace(before, `"overrides": {`, `"overrides": {
    "@cicord/react": "file:vendor/cicord-react-0.1.8.tgz",`, 1))
	if code, _, errOut := run(t, "portal", "upgrade", dir); code != 1 || !strings.Contains(errOut, "package.json overrides @cicord/react") || !strings.Contains(errOut, "--to-npm") {
		t.Errorf("override of an npm package = %d %s", code, errOut)
	}
	// A development build cannot tell which npm version is its own.
	useVersion(t, "0.2.0-dev")
	if code, _, errOut := run(t, "portal", "upgrade", dir, "--check"); code == 0 || !strings.Contains(errOut, "development build") {
		t.Errorf("--check with a dev build = %d %s", code, errOut)
	}
}

// TestPortalUpgradeVendored: after a cicord upgrade, `portal upgrade` swaps the vendored
// tarballs for this binary's and rewrites their references in place; --check gates CI.
func TestPortalUpgradeVendored(t *testing.T) {
	useKit(t, testKit(t, "0.1.0"), nil)
	dir := filepath.Join(t.TempDir(), "portal")
	if code, _, errOut := run(t, "portal", "init", dir, "--vendor"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if code, out, _ := run(t, "portal", "upgrade", dir, "--check"); code != 0 || !strings.Contains(out, "vendored)") {
		t.Fatalf("fresh portal --check = %d %s", code, out)
	}

	useKit(t, testKit(t, "0.1.1"), nil)
	if code, out, errOut := run(t, "portal", "upgrade", dir, "--check"); code != 1 || !strings.Contains(out, "@cicord/client 0.1.0 → 0.1.1 (vendored)") || !strings.Contains(errOut, "run `cicord portal upgrade`") {
		t.Fatalf("stale --check = %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "cicord-client-0.1.1.tgz")); err == nil {
		t.Fatal("--check wrote a tarball")
	}
	if code, out, errOut := run(t, "portal", "upgrade", dir); code != 0 || !strings.Contains(out, "Updated") {
		t.Fatalf("upgrade = %d %s %s", code, out, errOut)
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
	if got := dirNames(t, filepath.Join(dir, "vendor")); got != "cicord-client-0.1.1.tgz cicord-react-0.1.1.tgz cicord-ui-0.1.1.tgz" {
		t.Errorf("vendor/ = %v", got)
	}
	if code, out, _ := run(t, "portal", "upgrade", dir, "--check"); code != 0 {
		t.Errorf("--check after upgrade = %d %s", code, out)
	}
	// A vendored portal needs the portal kit of a release binary.
	useKit(t, nil, webui.ErrNoKit)
	if code, _, errOut := run(t, "portal", "upgrade", dir, "--check"); code == 0 || !strings.Contains(errOut, "portal kit") {
		t.Errorf("--check without kit = %d %s", code, errOut)
	}

	// Nothing to do without @cicord/* dependencies (e.g. workspace:* in the monorepo).
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "package.json"), `{"dependencies":{"@cicord/client":"workspace:*","react":"^19.2.0"}}`)
	if code, out, errOut := run(t, "portal", "upgrade", other, "--check"); code != 0 || !strings.Contains(out, "nothing to do") || !strings.Contains(errOut, "left alone") {
		t.Errorf("workspace portal = %d %s %s", code, out, errOut)
	}
}

// TestPortalVendorAlias: `portal vendor`, the former name, is a hidden alias of `portal
// upgrade` that says it is deprecated: `cicord portal vendor --check portal` in existing
// workflows keeps passing, vendored or on npm.
func TestPortalVendorAlias(t *testing.T) {
	useKit(t, testKit(t, "0.1.0"), nil)
	useVersion(t, "0.1.8")
	for _, flags := range [][]string{{"--vendor"}, nil} {
		dir := filepath.Join(t.TempDir(), "portal")
		if code, _, errOut := run(t, append([]string{"portal", "init", dir}, flags...)...); code != 0 {
			t.Fatalf("init: %s", errOut)
		}
		if code, out, errOut := run(t, "portal", "vendor", "--check", dir); code != 0 || !strings.Contains(out, "this cicord's version") || !strings.Contains(errOut, "deprecated: use `cicord portal upgrade`") {
			t.Errorf("vendor --check %v = %d %s %s", flags, code, out, errOut)
		}
		useVersion(t, "0.1.9")
		if code, _, _ := run(t, "portal", "vendor", "--check", dir); code != 1 && flags == nil {
			t.Errorf("vendor --check of a stale npm portal = %d", code)
		}
		useVersion(t, "0.1.8")
	}
	if code, out, _ := run(t, "portal", "--help"); code != 0 || strings.Contains(out, "vendor") || !strings.Contains(out, "upgrade") {
		t.Errorf("portal --help:\n%s", out)
	}
	if code, out, _ := run(t, "portal", "vendor", "--help"); code != 0 || !strings.Contains(out, "--to-npm") {
		t.Errorf("portal vendor --help:\n%s", out)
	}
}

// communityPortalJSON and communityWorkspace are the vendored portal of cicord-community
// (written by `cicord portal init` 0.1.8, then edited by hand: description, scripts, knip,
// engines, packageManager).
const communityPortalJSON = `{
  "name": "cicord-community-portal",
  "version": "0.0.0",
  "private": true,
  "description": "The community portal of the Cicord Discord server (community.cicord.dev): activity and support tickets, built on the Cicord portal SDK.",
  "type": "module",
  "scripts": {
    "dev": "vp dev --mode mock",
    "build": "tsc --noEmit && vp build",
    "knip": "knip"
  },
  "dependencies": {
    "@cicord/client": "file:vendor/cicord-client-0.1.8.tgz",
    "@cicord/react": "file:vendor/cicord-react-0.1.8.tgz",
    "@cicord/ui": "file:vendor/cicord-ui-0.1.8.tgz",
    "@fontsource-variable/atkinson-hyperlegible-next": "^5.3.0",
    "react": "^19.3.1",
    "react-dom": "^19.3.1"
  },
  "devDependencies": {
    "knip": "^6.48.0",
    "typescript": "^7.0.2",
    "vite": "npm:@voidzero-dev/vite-plus-core@8.3.8000",
    "vite-plus": "1.7.0"
  },
  "overrides": {
    "@cicord/client": "file:vendor/cicord-client-0.1.8.tgz",
    "@cicord/react": "file:vendor/cicord-react-0.1.8.tgz",
    "@cicord/ui": "file:vendor/cicord-ui-0.1.8.tgz",
    "vite": "npm:@voidzero-dev/vite-plus-core@8.3.8000"
  },
  "engines": {
    "node": ">=26"
  },
  "packageManager": "pnpm@12.23.0"
}
`

const communityWorkspace = `# pnpm settings of the portal (pnpm reads overrides here, not in package.json).
overrides:
    '@cicord/client': file:vendor/cicord-client-0.1.8.tgz
    '@cicord/react': file:vendor/cicord-react-0.1.8.tgz
    '@cicord/ui': file:vendor/cicord-ui-0.1.8.tgz
    vite: npm:@voidzero-dev/vite-plus-core@8.3.8000
peerDependencyRules:
    allowAny:
        - vite
    allowedVersions:
        vite: '*'
`

// TestPortalUpgradeToNPM: --to-npm moves a vendored portal to npm in one command, changing
// nothing else in package.json and pnpm-workspace.yaml.
func TestPortalUpgradeToNPM(t *testing.T) {
	kit := testKit(t, "0.1.8")
	useKit(t, kit, nil)
	useVersion(t, "0.1.8")
	repo := t.TempDir()
	if code, _, stderr := run(t, "init", repo); code != 0 {
		t.Fatal(stderr)
	}
	dir := filepath.Join(repo, "portal")
	writeFile(t, filepath.Join(dir, "package.json"), communityPortalJSON)
	writeFile(t, filepath.Join(dir, "pnpm-workspace.yaml"), communityWorkspace)
	for _, n := range []string{"client", "react", "ui"} {
		f := "cicord-" + n + "-0.1.8.tgz"
		writeFile(t, filepath.Join(dir, "vendor", f), string(kit["packages/"+f].Data))
	}
	writeFile(t, filepath.Join(repo, ".github/workflows/cicord-portal.yml"), "env:\n  CICORD_TAG: \"v0.1.8\"\nrun: cicord portal vendor --check portal\n")
	t.Chdir(repo)
	// Today, with 0.1.8: the workflow's check passes.
	if code, out, errOut := run(t, "portal", "vendor", "--check", "portal"); code != 0 || !strings.Contains(out, "(0.1.8, vendored)") {
		t.Fatalf("vendor --check before the migration = %d %s %s", code, out, errOut)
	}
	// The next release.
	useKit(t, testKit(t, "0.1.9"), nil)
	useVersion(t, "0.1.9")

	if code, _, errOut := run(t, "portal", "upgrade", "--to-npm", "--check"); code == 0 || !strings.Contains(errOut, "cannot be combined") {
		t.Errorf("--to-npm --check = %d %s", code, errOut)
	}
	useVersion(t, "0.2.0-dev")
	if code, _, errOut := run(t, "portal", "upgrade", "--to-npm"); code == 0 || !strings.Contains(errOut, "development build") {
		t.Errorf("--to-npm with a dev build = %d %s", code, errOut)
	}
	useVersion(t, "0.1.9")

	code, out, errOut := run(t, "portal", "upgrade", "--to-npm")
	if code != 0 {
		t.Fatalf("--to-npm = %d %s %s", code, out, errOut)
	}
	for _, want := range []string{"@cicord/client file:vendor/cicord-client-0.1.8.tgz → 0.1.9 (npm)", "pnpm-workspace.yaml: deleted the override of @cicord/ui",
		"deleted vendor/cicord-react-0.1.8.tgz", "Next: pnpm install", `Also set CICORD_TAG to "v0.1.9"`, "rename it to `cicord portal upgrade --check`"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	wantPJ := strings.NewReplacer(
		`"@cicord/client": "file:vendor/cicord-client-0.1.8.tgz",
    "@cicord/react": "file:vendor/cicord-react-0.1.8.tgz",
    "@cicord/ui": "file:vendor/cicord-ui-0.1.8.tgz",
    "vite"`, `"vite"`,
		"file:vendor/cicord-client-0.1.8.tgz", "0.1.9", "file:vendor/cicord-react-0.1.8.tgz", "0.1.9", "file:vendor/cicord-ui-0.1.8.tgz", "0.1.9",
	).Replace(communityPortalJSON)
	if got := readFile(t, filepath.Join(dir, "package.json")); got != wantPJ {
		t.Errorf("package.json:\n%s\nwant:\n%s", got, wantPJ)
	}
	wantWS := `# pnpm settings of the portal (pnpm reads overrides here, not in package.json).
overrides:
    vite: npm:@voidzero-dev/vite-plus-core@8.3.8000
peerDependencyRules:
    allowAny:
        - vite
    allowedVersions:
        vite: '*'
`
	if got := readFile(t, filepath.Join(dir, "pnpm-workspace.yaml")); got != wantWS {
		t.Errorf("pnpm-workspace.yaml:\n%s\nwant:\n%s", got, wantWS)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor")); err == nil {
		t.Error("vendor/ left")
	}
	// The existing workflow's `portal vendor --check portal` passes with the new release.
	if code, out, errOut := run(t, "portal", "vendor", "--check", "portal"); code != 0 {
		t.Errorf("vendor --check after --to-npm = %d %s %s", code, out, errOut)
	}
	if code, out, _ := run(t, "portal", "upgrade", "--to-npm"); code != 0 || !strings.Contains(out, "already depends on the npm packages") {
		t.Errorf("second --to-npm = %d %s", code, out)
	}
}

// TestDeleteYAMLOverrides: the overrides key goes when none is left, its comment stays.
func TestDeleteYAMLOverrides(t *testing.T) {
	in := "# header\noverrides:\n  '@cicord/client': file:vendor/c.tgz\npeerDependencyRules:\n  allowAny:\n    - vite\n"
	got, removed, err := deleteYAMLOverrides([]byte(in), portalSDKPackages)
	if err != nil || strings.Join(removed, ",") != "@cicord/client" {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if want := "# header\npeerDependencyRules:\n  allowAny:\n    - vite\n"; string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func dirNames(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, " ")
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
