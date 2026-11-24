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
		Pnpm            struct{ Overrides map[string]string }
		Overrides       map[string]string
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(repo, "portal/package.json"))), &pj); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, filepath.Join(repo, "portal/package.json")); !strings.Contains(raw, `"tsc --noEmit && vp build"`) || !strings.Contains(raw, "peerDependencyRules") {
		t.Errorf("package.json scripts escaped or peer rules missing:\n%s", raw)
	}
	if pj.Name != "fans-portal" || pj.Dependencies["react"] != "^19.2.0" {
		t.Errorf("package.json = %+v", pj)
	}
	for _, name := range []string{"client", "react", "ui"} {
		want := "file:vendor/cicord-" + name + "-0.1.0.tgz"
		if pj.Dependencies["@cicord/"+name] != want || pj.Pnpm.Overrides["@cicord/"+name] != want || pj.Overrides["@cicord/"+name] != want {
			t.Errorf("@cicord/%s = %q / %q / %q", name, pj.Dependencies["@cicord/"+name], pj.Pnpm.Overrides["@cicord/"+name], pj.Overrides["@cicord/"+name])
		}
	}
	if pj.Pnpm.Overrides["vite"] != "npm:@voidzero-dev/vite-plus-core@0.2.7" {
		t.Errorf("vite override lost: %v", pj.Pnpm.Overrides)
	}
	wf := readFile(t, filepath.Join(repo, ".github/workflows/cicord-portal.yml"))
	for _, want := range []string{"working-directory: portal", "pnpm install --frozen-lockfile", "git status --porcelain -- dist", `paths: ["portal/**", "plugins/**"`, "cicord portal typegen --check --out portal/src/cicord-routes.d.ts"} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow misses %q:\n%s", want, wf)
		}
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
