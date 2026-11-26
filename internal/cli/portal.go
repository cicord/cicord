package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/typegen"
	"github.com/cicord/cicord/internal/version"
	"github.com/cicord/cicord/internal/webui"
)

func init() {
	extraCommands = append(extraCommands, newPortalCmd)
}

// portalKit returns the embedded portal kit (replaced in tests).
var portalKit = webui.PortalKit

func newPortalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "portal",
		Short: "Create, build and serve a custom community portal",
		Long: `A custom portal is a React app (a fork of the reference portal) committed in the
repository. Its build output is committed too and served by the runtime at / instead of
the default portal (cicord.yaml spec.portal.path). See https://cicord.dev/docs/portal/custom-portal/.`,
	}
	cmd.AddCommand(newPortalInitCmd(), newPortalBuildCmd(), newPortalTypegenCmd(), newPortalUpgradeCmd(), newPortalVendorCmd())
	return cmd
}

// ───────────────────────────────────────────────────────────── cicord portal init

type kitPackage struct {
	Name, Version, File string
	Data                []byte
}

func readKitPackages(kit fs.FS) ([]kitPackage, error) {
	files, err := fs.Glob(kit, "packages/*.tgz")
	if err != nil {
		return nil, err
	}
	var out []kitPackage
	for _, f := range files {
		data, err := fs.ReadFile(kit, f)
		if err != nil {
			return nil, err
		}
		manifest, err := tgzPackageJSON(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		var m struct{ Name, Version string }
		if err := json.Unmarshal(manifest, &m); err != nil || m.Name == "" {
			return nil, fmt.Errorf("%s: invalid package.json", f)
		}
		out = append(out, kitPackage{Name: m.Name, Version: m.Version, File: path.Base(f), Data: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// tgzPackageJSON returns package/package.json of an npm tarball.
func tgzPackageJSON(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no package/package.json")
		}
		if err != nil {
			return nil, err
		}
		if h.Name == "package/package.json" {
			return io.ReadAll(io.LimitReader(tr, 1<<20))
		}
	}
}

// portalPackageJSON is the package.json written by `cicord portal init`.
type portalPackageJSON struct {
	Name            string            `json:"name"`
	Private         bool              `json:"private"`
	Version         string            `json:"version"`
	Type            string            `json:"type"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	// Pnpm is read from the template only: pnpm 10+ ignores package.json "pnpm" settings
	// in favour of pnpm-workspace.yaml, where init writes them (portalPnpmSettings).
	Pnpm *portalPnpmSettings `json:"pnpm,omitempty"`
	// Overrides is npm's equivalent of pnpm's overrides.
	Overrides map[string]string `json:"overrides,omitempty"`
}

// portalPnpmSettings are the pnpm settings of a portal, written to its
// pnpm-workspace.yaml. In a vendored portal, the @cicord/* overrides make @cicord/react's
// dependency on @cicord/client resolve to the vendored tarball, not to the npm registry.
type portalPnpmSettings struct {
	Overrides           map[string]string `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	PeerDependencyRules map[string]any    `json:"peerDependencyRules,omitempty" yaml:"peerDependencyRules,omitempty"`
}

func newPortalInitCmd() *cobra.Command {
	var force, vendor, fromNPM bool
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a custom portal from the reference portal (default dir: portal)",
		Long: `Copies the reference community portal into [dir] (default "portal"). It depends on
@cicord/client, @cicord/react and @cicord/ui from npm, at the exact version of this cicord
("@cicord/client": "<version>"): every release publishes them, with provenance. When [dir]
is inside a Cicord repository, also writes .github/workflows/cicord-portal.yml, which checks
that the committed build matches the sources and that the packages are this cicord's
version, and src/cicord-routes.d.ts, the types of the repository's plugin routes.

--vendor (offline or air-gapped builds) vendors the packages embedded in this binary in
<dir>/vendor instead (file: dependencies, and pnpm overrides keeping them there): installing
needs no registry access for them. A development build of cicord, whose version is not on
npm, always vendors. ` + "`cicord portal upgrade`" + ` keeps either kind of portal on the version
of cicord (https://cicord.dev/docs/portal/custom-portal/).

Then: cd <dir> && pnpm install && pnpm build, set spec.portal.path: ./<dir>/dist in
cicord.yaml, and commit <dir> (pnpm-lock.yaml and dist/ included).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "portal"
			if len(args) == 1 {
				dir = args[0]
			}
			return portalInit(cmd.OutOrStdout(), dir, force, vendor)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "write into a non-empty directory / overwrite files")
	cmd.Flags().BoolVar(&vendor, "vendor", false, "vendor the @cicord/* packages embedded in this binary instead of depending on npm (offline builds)")
	// npm became the default: --from-npm is still accepted, and does nothing.
	cmd.Flags().BoolVar(&fromNPM, "from-npm", false, "depend on the npm packages (the default)")
	_ = cmd.Flags().MarkDeprecated("from-npm", "npm is the default; --vendor vendors the embedded packages")
	return cmd
}

// fromNPMDefault is the default of `cicord plugin new --from-npm`: CICORD_FROM_NPM=true (or 1).
func fromNPMDefault() bool {
	v := strings.ToLower(os.Getenv("CICORD_FROM_NPM"))
	return v == "1" || v == "true"
}

func portalInit(w io.Writer, dir string, force, vendor bool) error {
	kit, err := portalKit()
	if err != nil {
		return err
	}
	v := cicordVersion()
	devNote := ""
	if !vendor && !isReleaseVersion(v) {
		vendor = true
		devNote = fmt.Sprintf("Note: this cicord (%s) is a development build, whose @cicord/* packages are not on npm:\n"+
			"the ones embedded in it are vendored in %s/vendor instead. With a cicord release,\n"+
			"`cicord portal upgrade --to-npm %s` moves the portal to npm.\n", v, dir, dir)
	}
	var pkgs []kitPackage
	if vendor {
		if pkgs, err = readKitPackages(kit); err != nil {
			return err
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(abs); err == nil && len(entries) > 0 && !force {
		return fmt.Errorf("%s is not empty (use --force to write into it anyway)", dir)
	}
	repoRoot := findRepoRoot(filepath.Dir(abs))
	name := sanitizeKey(filepath.Base(abs))
	if repoRoot != "" {
		if p, err := readProject(repoRoot); err == nil && p.Metadata.Name != "" {
			name = p.Metadata.Name + "-portal"
		}
	}

	var written []string
	write := func(rel string, data []byte) error {
		out := filepath.Join(abs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	}

	// Template files (package.json and .gitignore are rewritten below).
	tmpl, err := fs.Sub(kit, "template")
	if err != nil {
		return err
	}
	err = fs.WalkDir(tmpl, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "package.json" || p == ".gitignore" {
			return err
		}
		data, err := fs.ReadFile(tmpl, p)
		if err != nil {
			return err
		}
		return write(p, data)
	})
	if err != nil {
		return err
	}

	raw, err := fs.ReadFile(tmpl, "package.json")
	if err != nil {
		return err
	}
	var pj portalPackageJSON
	if err := json.Unmarshal(raw, &pj); err != nil {
		return fmt.Errorf("template package.json: %w", err)
	}
	pj.Name, pj.Private, pj.Version, pj.Type = name, true, "0.0.0", "module"
	if pj.Dependencies == nil {
		pj.Dependencies = map[string]string{}
	}
	settings := portalPnpmSettings{Overrides: map[string]string{}}
	if pj.Pnpm != nil {
		maps.Copy(settings.Overrides, pj.Pnpm.Overrides)
		settings.PeerDependencyRules = pj.Pnpm.PeerDependencyRules
	}
	pj.Pnpm = nil
	for _, name := range portalSDKPackages {
		_, dep := pj.Dependencies[name]
		_, devDep := pj.DevDependencies[name]
		if !dep && !devDep {
			continue
		}
		// npm: the exact version of this cicord, released with it (no range: the portal's
		// SDK is the runtime's, `cicord portal upgrade --check` verifies it).
		spec := v
		if vendor {
			i := slices.IndexFunc(pkgs, func(p kitPackage) bool { return p.Name == name })
			if i < 0 {
				return fmt.Errorf("this cicord binary embeds no %s package", name)
			}
			p := pkgs[i]
			if err := write("vendor/"+p.File, p.Data); err != nil {
				return err
			}
			spec = "file:vendor/" + p.File
			// @cicord/react depends on @cicord/client by version: resolve it to the vendored
			// tarball too.
			settings.Overrides[name] = spec
		} else {
			// The template's own overrides of @cicord/* (if any) would win over the version.
			delete(settings.Overrides, name)
		}
		if devDep {
			pj.DevDependencies[name] = spec
		} else {
			pj.Dependencies[name] = spec
		}
	}
	pj.Overrides = settings.Overrides
	if strings.HasPrefix(settings.Overrides["vite"], "npm:") && settings.PeerDependencyRules == nil {
		// Vite+ replaces vite with its core build: let plugins that peer-depend on vite accept it.
		settings.PeerDependencyRules = map[string]any{"allowAny": []string{"vite"}, "allowedVersions": map[string]string{"vite": "*"}}
	}
	ws, err := yaml.Marshal(settings)
	if err != nil {
		return err
	}
	if err := write("pnpm-workspace.yaml", append([]byte("# pnpm settings of the portal (pnpm reads overrides here, not in package.json).\n"), ws...)); err != nil {
		return err
	}
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pj); err != nil {
		return err
	}
	if err := write("package.json", data.Bytes()); err != nil {
		return err
	}
	if err := write(".gitignore", []byte(portalGitignore)); err != nil {
		return err
	}
	// The template's route types describe the monorepo examples: replace them with the
	// plugins of this repository (none outside a repository).
	types := ""
	if repoRoot != "" {
		if types, err = routeTypes(io.Discard, repoRoot); err != nil {
			fmt.Fprintf(w, "  warning: plugin route types not generated (%v): run `cicord portal typegen` once fixed\n", err)
			types = ""
		}
	}
	if types == "" {
		if types, err = typegen.GenerateRoutes(nil); err != nil {
			return err
		}
	}
	if err := write(routeTypesFile, []byte(types)); err != nil {
		return err
	}

	rel := dir
	if repoRoot != "" {
		if r, err := filepath.Rel(repoRoot, abs); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	for _, f := range written {
		if !strings.HasPrefix(f, "src/") && !strings.HasPrefix(f, "test/") && !strings.HasPrefix(f, "public/") {
			fmt.Fprintln(w, "  created", filepath.ToSlash(filepath.Join(dir, f)))
		}
	}
	fmt.Fprintf(w, "  created %s/src, %s/test, %s/public (%d files in total)\n", dir, dir, dir, len(written))
	if repoRoot != "" {
		wf := filepath.Join(repoRoot, ".github", "workflows", "cicord-portal.yml")
		if _, err := os.Stat(wf); err == nil && !force {
			fmt.Fprintln(w, "  kept   .github/workflows/cicord-portal.yml (exists)")
		} else {
			var buf bytes.Buffer
			ref := cicordInstallVersion()
			if !isReleaseVersion(v) {
				if data, err := fs.ReadFile(kit, "source-ref"); err == nil {
					if candidate := strings.TrimSpace(string(data)); version.ValidSourceRef(candidate) {
						ref = candidate
					}
				}
			}
			if err := portalWorkflow.Execute(&buf, map[string]any{"Dir": rel, "Cicord": ref, "Version": v, "Source": !isReleaseVersion(v)}); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(wf), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(wf, buf.Bytes(), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(w, "  created .github/workflows/cicord-portal.yml")
		}
	}
	source := "@cicord/client, @cicord/react and @cicord/ui " + v + " from npm"
	if vendor {
		source = "the @cicord/* packages vendored in " + filepath.ToSlash(filepath.Join(dir, "vendor"))
	}
	fmt.Fprintf(w, "  depends on %s\n", source)
	if devNote != "" {
		fmt.Fprint(w, "\n"+devNote)
	}
	fmt.Fprintf(w, `
Next steps:
  cd %[1]s && pnpm install && pnpm build    (or: cicord portal build)
  add to cicord.yaml:
    spec:
      portal:
        path: ./%[2]s/dist
  cicord dev --sim                            the runtime serves %[2]s/dist at /
  pnpm dev:runtime                          (in %[1]s) hot-reloading dev server against cicord dev
Commit %[2]s, dist/ and pnpm-lock.yaml included: the runtime serves the committed build.
`, dir, rel)
	return nil
}

const portalGitignore = `node_modules/
*.local
# dist/ is NOT ignored: the Cicord runtime serves the committed build (cicord.yaml
# spec.portal.path); CI checks that it matches the sources (https://cicord.dev/docs/portal/custom-portal/).
`

var portalWorkflow = template.Must(template.New("wf").Delims("[[", "]]").Parse(`# Generated by ` + "`cicord portal init`" + `.
# The runtime serves the committed portal build (cicord.yaml spec.portal.path). This job
# rebuilds it from the sources and fails when the committed build differs, so reviewing
# [[.Dir]]/src is enough to know what the runtime will serve.
name: cicord-portal
on:
  pull_request:
    paths: ["[[.Dir]]/**", "plugins/**", "bots/**", "cicord.lock"]
  push:
    branches: [main]
    paths: ["[[.Dir]]/**", "plugins/**", "bots/**", "cicord.lock"]

permissions:
  contents: read

jobs:
  build:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: [[.Dir]]
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: pnpm/action-setup@73e46992e05b19abd31f2f3c20e0e7370d762115 # v6.1.1
        with:
          version: 12
      - uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version: 26
          cache: pnpm
          cache-dependency-path: [[.Dir]]/pnpm-lock.yaml
[[if .Source]]      - uses: actions/setup-go@d8cbb8045d7f43706b9177cceb36c1f91a6644d5 # v7.1.0
        with:
          go-version: stable
          cache: false
      - name: Build the pinned Cicord snapshot and its portal kit
        working-directory: ${{ runner.temp }}
        env:
          CICORD_REF: "[[.Cicord]]"
          CICORD_VERSION: "[[.Version]]"
        run: |
          set -euo pipefail
          src="$(go mod download -json "github.com/cicord/cicord@$CICORD_REF" | jq -er .Dir)"
          cp -R "$src" cicord-source
          chmod -R u+w cicord-source
          chmod u+x cicord-source/scripts/sync-ui.sh
          make -C cicord-source all VERSION="$CICORD_VERSION" SOURCE_REF="$CICORD_REF"
          echo "$PWD/cicord-source/bin" >> "$GITHUB_PATH"
[[else]]      # The cicord release whose @cicord/* packages the portal depends on: bump it with the
      # runtime, after "cicord portal upgrade". "portal upgrade --check" compares the portal
      # with this binary: the exact npm version in package.json, or for a vendored portal the
      # tarballs of the portal kit that the release binary embeds (a "go install" build does
      # not). Its checksum and its build provenance (the release workflow of cicord/cicord,
      # from that tag) are verified.
      - name: Install cicord ([[.Cicord]], release binary)
        working-directory: ${{ runner.temp }}
        env:
          CICORD_TAG: "[[.Cicord]]"
          GH_TOKEN: ${{ github.token }}
        run: |
          set -euo pipefail
          tag="$CICORD_TAG"
          case "$RUNNER_ARCH" in X64) arch=amd64 ;; ARM64) arch=arm64 ;; *) echo "::error::no cicord release for $RUNNER_ARCH"; exit 1 ;; esac
          archive="cicord_${tag#v}_linux_${arch}.tar.gz"
          gh release download "$tag" --repo cicord/cicord --pattern "$archive" --pattern checksums.txt --clobber
          awk -v f="$archive" '$2 == f' checksums.txt | sha256sum -c -
          gh attestation verify "$archive" --repo cicord/cicord \
            --signer-workflow cicord/cicord/.github/workflows/release.yml --source-ref "refs/tags/$tag"
          mkdir -p cicord-bin && tar -xzf "$archive" -C cicord-bin cicord
          echo "$PWD/cicord-bin" >> "$GITHUB_PATH"
          cicord-bin/cicord version
[[end]]      - name: The plugin route types match the plugins (src/cicord-routes.d.ts)
        working-directory: .
        run: cicord portal typegen --check --out [[.Dir]]/src/cicord-routes.d.ts
      - name: The @cicord/* packages are this cicord's version (package.json, vendor/)
        working-directory: .
        run: cicord portal upgrade --check [[.Dir]]
      - run: pnpm install --frozen-lockfile
      - run: pnpm test
      - run: pnpm build
      - name: The committed build matches the sources
        run: |
          if [ -n "$(git status --porcelain -- dist)" ]; then
            git status --short -- dist
            echo "::error::[[.Dir]]/dist is out of date: run 'cicord portal build' and commit it"
            exit 1
          fi
`))

// findRepoRoot returns the nearest directory from dir upwards containing cicord.yaml.
func findRepoRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "cicord.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ───────────────────────────────────────────────────────────── cicord portal build

func newPortalBuildCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "build [repo-dir]",
		Short: "Build the custom portal into cicord.yaml spec.portal.path (needs Node and pnpm or npm)",
		Long: `Runs the package manager of the portal (pnpm when it has pnpm-lock.yaml or pnpm is
installed, else npm): install when node_modules is missing, then the build script. The
portal sources are the nearest directory above spec.portal.path with a package.json.

--check installs from the lockfile, rebuilds and fails when the output differs from what
was there before (the committed build is out of date).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return portalBuild(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, check)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "fail when the build output changes (CI)")
	return cmd
}

func portalBuild(ctx context.Context, stdout, stderr io.Writer, dir string, check bool) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	proj, err := readProject(root)
	if err != nil {
		return err
	}
	if proj.Spec.Portal == nil || proj.Spec.Portal.Path == "" {
		return errors.New("cicord.yaml has no spec.portal.path (e.g. ./portal/dist): nothing to build")
	}
	rel, ok := config.ResolveRepoPath("cicord.yaml", proj.Spec.Portal.Path)
	if !ok {
		return fmt.Errorf("spec.portal.path %q must be inside the repository", proj.Spec.Portal.Path)
	}
	out := filepath.Join(root, filepath.FromSlash(rel))
	src := ""
	for d := filepath.Dir(out); strings.HasPrefix(d, root); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "package.json")); err == nil {
			src = d
			break
		}
		if d == root {
			break
		}
	}
	if src == "" {
		return fmt.Errorf("no package.json above %s: create the portal with `cicord portal init`", rel)
	}
	pm := packageManager(src)
	if pm == "" {
		return errors.New("neither pnpm nor npm is installed: install Node.js 26 and pnpm (npm install -g pnpm)")
	}
	if usesCicordClient(src) {
		// The types of the plugin routes follow the plugins of the revision.
		changed, err := writeRouteTypes(stderr, root, filepath.Join(src, filepath.FromSlash(routeTypesFile)), check)
		if err != nil {
			return fmt.Errorf("plugin route types: %w", err)
		}
		typesRel := filepath.ToSlash(relOr(root, filepath.Join(src, filepath.FromSlash(routeTypesFile))))
		if check && changed {
			fmt.Fprintf(stderr, "%s is out of date: run `cicord portal typegen` and commit it\n", typesRel)
			return exitError{1}
		}
		if changed {
			fmt.Fprintf(stdout, "wrote %s\n", typesRel)
		}
	}
	before, _ := hashTree(out)
	run := func(args ...string) error {
		fmt.Fprintf(stdout, "$ %s %s   (in %s)\n", pm, strings.Join(args, " "), relOr(root, src))
		c := exec.CommandContext(ctx, pm, args...) //nolint:gosec // G204: pm is "pnpm" or "npm" (packageManager), args are fixed by portalBuild
		c.Dir, c.Stdout, c.Stderr = src, stdout, stderr
		return c.Run()
	}
	if _, err := os.Stat(filepath.Join(src, "node_modules")); err != nil || check {
		args := []string{"install"}
		if check {
			if pm == "pnpm" {
				args = append(args, "--frozen-lockfile")
			} else {
				args = []string{"ci"}
			}
		}
		if err := run(args...); err != nil {
			return fmt.Errorf("%s install failed: %w", pm, err)
		}
	}
	if err := run("run", "build"); err != nil {
		return fmt.Errorf("portal build failed: %w", err)
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); err != nil {
		return fmt.Errorf("the build did not write %s/index.html: does the portal build into spec.portal.path?", rel)
	}
	after, _ := hashTree(out)
	if check && before != after {
		fmt.Fprintf(stderr, "%s is out of date: commit the new build\n", rel)
		return exitError{1}
	}
	fmt.Fprintf(stdout, "✓ portal built into %s\n", rel)
	return nil
}

// usesCicordClient reports whether the portal package depends on @cicord/client or @cicord/react
// (the consumers of src/cicord-routes.d.ts).
func usesCicordClient(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pj struct{ Dependencies, DevDependencies map[string]string }
	if json.Unmarshal(data, &pj) != nil {
		return false
	}
	for _, name := range []string{"@cicord/client", "@cicord/react"} {
		if _, ok := pj.Dependencies[name]; ok {
			return true
		}
		if _, ok := pj.DevDependencies[name]; ok {
			return true
		}
	}
	return false
}

func relOr(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}

func packageManager(dir string) string {
	has := func(name string) bool { _, err := exec.LookPath(name); return err == nil }
	if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err == nil && has("npm") {
		return "npm"
	}
	if has("pnpm") {
		return "pnpm"
	}
	if has("npm") {
		return "npm"
	}
	return ""
}

// hashTree hashes the files of a directory (paths and contents).
func hashTree(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // G122: hashes the user's own build output, not a tree shared with an untrusted writer
		if err != nil {
			return err
		}
		r, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(r), len(data))
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

// cicordInstallVersion is the cicord release the generated workflows install: the tag
// of a published binary, or the immutable source ref of a snapshot.
func cicordInstallVersion() string {
	v := cicordVersion()
	if !isReleaseVersion(v) {
		return sourceRevision()
	}
	return "v" + v
}
