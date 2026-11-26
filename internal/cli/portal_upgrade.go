package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/version"
)

// ──────────────────────────────────────────── cicord portal upgrade (and vendor, its alias)

// portalSDKPackages are the @cicord/* packages a portal depends on, released with cicord at
// the same version.
var portalSDKPackages = []string{"@cicord/client", "@cicord/react", "@cicord/ui"}

func newPortalUpgradeCmd() *cobra.Command {
	return portalUpgradeCommand("upgrade")
}

// newPortalVendorCmd is `cicord portal vendor`, the name of `portal upgrade` before the
// portals moved to npm: kept, hidden, so that the workflows running
// `cicord portal vendor --check <dir>` keep passing.
func newPortalVendorCmd() *cobra.Command {
	cmd := portalUpgradeCommand("vendor")
	cmd.Hidden = true
	cmd.Short = "Deprecated alias of `cicord portal upgrade`"
	return cmd
}

func portalUpgradeCommand(use string) *cobra.Command {
	var check, toNPM bool
	cmd := &cobra.Command{
		Use:   use + " [dir]",
		Short: "Set the @cicord/* packages of a custom portal to this cicord's version",
		Long: `Sets @cicord/client, @cicord/react and @cicord/ui of a custom portal to the version of
this cicord, the way the portal gets them:
  - from npm (the default of ` + "`cicord portal init`" + `): their versions in package.json become
    this cicord's exact version;
  - vendored (` + "`portal init --vendor`" + `): the tarballs in vendor/ are replaced with the ones
    embedded in this binary, package.json and pnpm-workspace.yaml point at them, and the
    old ones are deleted.
Nothing else changes. Run it after upgrading cicord, then pnpm install, ` + "`cicord portal build`" + `,
and commit.

[dir] is the portal's directory (the one with package.json); by default the nearest one
above cicord.yaml spec.portal.path. --check changes nothing and fails when the packages
are not this cicord's version (CI). --to-npm moves a vendored portal to npm: package.json
depends on this version, the @cicord/* overrides of pnpm-workspace.yaml and package.json
are deleted, and so is vendor/.

` + "`cicord portal vendor`" + ` is a deprecated alias of this command.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if use != "upgrade" {
				fmt.Fprintf(cmd.ErrOrStderr(), "`cicord portal %s` is deprecated: use `cicord portal upgrade` (same arguments)\n", use)
			}
			if check && toNPM {
				return errors.New("--check and --to-npm cannot be combined")
			}
			dir := ""
			if len(args) == 1 {
				dir = args[0]
			}
			return portalUpgrade(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, check, toNPM)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "fail when the @cicord/* packages are not this cicord's version (changes nothing)")
	cmd.Flags().BoolVar(&toNPM, "to-npm", false, "move a vendored portal to the npm packages (deletes vendor/ and the @cicord/* overrides)")
	return cmd
}

// cicordVersion is the version of this cicord, without a leading v (replaced in tests).
var cicordVersion = func() string { return strings.TrimPrefix(version.Version, "v") }

// Overridden by CLI tests to model tagged releases and unpublished source builds.
var isReleaseVersion = version.Published
var sourceRevision = version.Revision

// vendorRefRe matches a dependency on a vendored tarball in package.json or
// pnpm-workspace.yaml: "file:vendor/<file>.tgz".
var vendorRefRe = regexp.MustCompile(`file:vendor/([A-Za-z0-9._-]+\.tgz)`)

// portalManifest is what `portal upgrade` reads from a portal's package.json.
type portalManifest struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Overrides       map[string]any    `json:"overrides"`
}

func (m portalManifest) spec(name string) (field, spec string) {
	if s, ok := m.Dependencies[name]; ok {
		return "dependencies", s
	}
	if s, ok := m.DevDependencies[name]; ok {
		return "devDependencies", s
	}
	return "", ""
}

// isNPMSpec reports whether spec resolves from the npm registry: a version or a range.
func isNPMSpec(spec string) bool {
	_, err := semver.NewConstraint(spec)
	return err == nil
}

// sdkDep is one @cicord/* dependency of a portal.
type sdkDep struct {
	name, field, spec string
	vendored          string // the vendor/ file, when vendored
}

func portalUpgrade(stdout, stderr io.Writer, dir string, check, toNPM bool) error {
	src, err := vendorSourceDir(dir)
	if err != nil {
		return err
	}
	rel := relOr(findRepoRoot(src), src)
	pjPath := filepath.Join(src, "package.json")
	pjRaw, err := os.ReadFile(pjPath)
	if err != nil {
		return err
	}
	var pj portalManifest
	if err := json.Unmarshal(pjRaw, &pj); err != nil {
		return fmt.Errorf("package.json: %w", err)
	}
	wsPath := filepath.Join(src, "pnpm-workspace.yaml")
	wsRaw, wsErr := os.ReadFile(wsPath)
	if wsErr != nil && !errors.Is(wsErr, os.ErrNotExist) {
		return wsErr
	}
	var ws struct {
		Overrides map[string]any `yaml:"overrides"`
	}
	if wsErr == nil {
		if err := yaml.Unmarshal(wsRaw, &ws); err != nil {
			return fmt.Errorf("pnpm-workspace.yaml: %w", err)
		}
	}

	var npm, vendored []sdkDep
	for _, name := range portalSDKPackages {
		field, spec := pj.spec(name)
		switch m := vendorRefRe.FindStringSubmatch(spec); {
		case spec == "":
		case m != nil:
			vendored = append(vendored, sdkDep{name: name, field: field, spec: spec, vendored: m[1]})
		case isNPMSpec(spec):
			npm = append(npm, sdkDep{name: name, field: field, spec: spec})
		default:
			fmt.Fprintf(stderr, "  %s is %q (neither a version nor vendor/): left alone\n", name, spec)
		}
	}
	if len(npm)+len(vendored) == 0 {
		fmt.Fprintf(stdout, "%s depends on no @cicord/* package from npm or vendor/: nothing to do\n", rel)
		return nil
	}
	v := cicordVersion()
	if toNPM {
		return portalToNPM(stdout, src, rel, v, pjRaw, wsRaw, wsErr == nil, append(npm, vendored...))
	}

	// npm: the exact version of this cicord. An @cicord/* override would win over it.
	var problems []string
	pjNew := pjRaw
	if len(npm) > 0 && !isReleaseVersion(v) {
		return fmt.Errorf("this cicord (%s) is a development build, whose @cicord/* packages are not on npm: use a release to upgrade or check %s", v, rel)
	}
	for _, d := range npm {
		for file, ov := range map[string]map[string]any{"package.json": pj.Overrides, "pnpm-workspace.yaml": ws.Overrides} {
			if o, ok := ov[d.name]; ok {
				problems = append(problems, fmt.Sprintf("%s overrides %s (%v): run `cicord portal upgrade --to-npm`", file, d.name, o))
			}
		}
		if d.spec == v {
			continue
		}
		fmt.Fprintf(stdout, "  %s %s → %s (npm)\n", d.name, d.spec, v)
		if pjNew, _, err = jsonSetString(pjNew, []string{d.field}, d.name, v); err != nil {
			return fmt.Errorf("package.json: %w", err)
		}
	}
	slices.Sort(problems)
	for _, p := range problems {
		fmt.Fprintf(stderr, "  %s\n", p)
	}
	if len(problems) > 0 {
		return exitError{1}
	}

	// vendored: the tarballs embedded in this binary.
	var vchanges []vendorChange
	if len(vendored) > 0 {
		if vchanges, err = planVendored(stdout, src, vendored); err != nil {
			return err
		}
	}
	if bytes.Equal(pjNew, pjRaw) && len(vchanges) == 0 {
		mode := "npm"
		if len(vendored) > 0 {
			mode = "vendored"
		}
		fmt.Fprintf(stdout, "%s: the @cicord/* packages are this cicord's version (%s, %s)\n", rel, v, mode)
		return nil
	}
	if check {
		fmt.Fprintf(stderr, "%s: the @cicord/* packages are not this cicord's version (%s): run `cicord portal upgrade` and commit\n", rel, v)
		return exitError{1}
	}
	if err := applyVendored(src, pjPath, string(pjNew), wsPath, string(wsRaw), wsErr == nil, vchanges); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Updated the @cicord/* packages of %s to %s.\n", rel, v)
	printUpgradeNext(stdout, src, rel, false)
	return nil
}

// portalToNPM moves a portal to the npm packages: package.json depends on version v, the
// @cicord/* overrides are deleted, and so are the vendored tarballs nothing refers to.
func portalToNPM(stdout io.Writer, src, rel, v string, pjRaw, wsRaw []byte, hasWS bool, deps []sdkDep) error {
	if !isReleaseVersion(v) {
		return fmt.Errorf("this cicord (%s) is a development build, whose @cicord/* packages are not on npm: use a release for --to-npm", v)
	}
	pj, ws := pjRaw, wsRaw
	var err error
	for _, d := range deps {
		if d.spec != v {
			fmt.Fprintf(stdout, "  %s %s → %s (npm)\n", d.name, d.spec, v)
			if pj, _, err = jsonSetString(pj, []string{d.field}, d.name, v); err != nil {
				return fmt.Errorf("package.json: %w", err)
			}
		}
	}
	for _, name := range portalSDKPackages {
		var deleted bool
		if pj, deleted, err = jsonDeleteMember(pj, []string{"overrides"}, name); err != nil {
			return fmt.Errorf("package.json: %w", err)
		}
		if deleted {
			fmt.Fprintf(stdout, "  package.json: deleted the override of %s\n", name)
		}
	}
	if o, ok, _ := jsonFindObject(pj, "overrides"); ok && len(o.Members) == 0 {
		if pj, _, err = jsonDeleteMember(pj, nil, "overrides"); err != nil {
			return fmt.Errorf("package.json: %w", err)
		}
	}
	if !json.Valid(pj) {
		return errors.New("package.json: the edit made it invalid (please report it)")
	}
	if hasWS {
		var removed []string
		if ws, removed, err = deleteYAMLOverrides(ws, portalSDKPackages); err != nil {
			return fmt.Errorf("pnpm-workspace.yaml: %w", err)
		}
		for _, name := range removed {
			fmt.Fprintf(stdout, "  pnpm-workspace.yaml: deleted the override of %s\n", name)
		}
	}
	// The vendored tarballs nothing refers to any more.
	still := map[string]bool{}
	for _, m := range vendorRefRe.FindAllStringSubmatch(string(pj)+"\n"+string(ws), -1) {
		still[m[1]] = true
	}
	var stale []string
	for _, m := range vendorRefRe.FindAllStringSubmatch(string(pjRaw)+"\n"+string(wsRaw), -1) {
		if !still[m[1]] && !slices.Contains(stale, m[1]) {
			stale = append(stale, m[1])
		}
	}
	if bytes.Equal(pj, pjRaw) && bytes.Equal(ws, wsRaw) && len(stale) == 0 {
		fmt.Fprintf(stdout, "%s already depends on the npm packages of this cicord (%s)\n", rel, v)
		return nil
	}
	if err := os.WriteFile(filepath.Join(src, "package.json"), pj, 0o644); err != nil { //nolint:gosec // G703: the package.json of the portal the user designated (dir or spec.portal.path)
		return err
	}
	if hasWS && !bytes.Equal(ws, wsRaw) {
		if err := os.WriteFile(filepath.Join(src, "pnpm-workspace.yaml"), ws, 0o644); err != nil { //nolint:gosec // G703: same portal directory as package.json
			return err
		}
	}
	for _, f := range stale {
		if err := os.Remove(filepath.Join(src, "vendor", f)); err != nil && !errors.Is(err, os.ErrNotExist) { //nolint:gosec // G703: f matches vendorRefRe (a file name, no separator) in the designated portal's vendor/
			return err
		}
		fmt.Fprintf(stdout, "  deleted vendor/%s\n", f)
	}
	if entries, err := os.ReadDir(filepath.Join(src, "vendor")); err == nil && len(entries) == 0 {
		_ = os.Remove(filepath.Join(src, "vendor"))
	}
	fmt.Fprintf(stdout, "%s now depends on the @cicord/* packages of npm, at %s.\n", rel, v)
	printUpgradeNext(stdout, src, rel, true)
	return nil
}

// printUpgradeNext prints what to do after `portal upgrade`: install, build, commit, and
// the cicord version of the portal workflow when it lags behind.
func printUpgradeNext(w io.Writer, src, rel string, toNPM bool) {
	fmt.Fprintf(w, "Next: pnpm install (in %s: it updates pnpm-lock.yaml), `cicord portal build`, then commit %s (pnpm-lock.yaml and dist/ included", rel, rel)
	if toNPM {
		fmt.Fprint(w, ", and the deletion of vendor/")
	}
	fmt.Fprintln(w, ").")
	root := findRepoRoot(src)
	if root == "" {
		return
	}
	wf, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "cicord-portal.yml"))
	if err != nil {
		return
	}
	want, err := cicordInstallVersion()
	if err != nil {
		return
	}
	if m := regexp.MustCompile(`CICORD_(TAG|REF): "([^"]*)"`).FindSubmatch(wf); m != nil && string(m[2]) != want {
		label := "REF"
		if isReleaseVersion(cicordVersion()) {
			label = "TAG"
		}
		if string(m[1]) == label {
			fmt.Fprintf(w, "Also set CICORD_%s to %q in .github/workflows/cicord-portal.yml (now %q).\n", label, want, m[2])
		} else {
			fmt.Fprintf(w, "Update the installation steps in .github/workflows/cicord-portal.yml to match this build (%s). New portals generate those steps with cicord portal init.\n", want)
		}
	}
	if toNPM && bytes.Contains(wf, []byte("cicord portal vendor --check")) {
		fmt.Fprintln(w, "Its `cicord portal vendor --check` step keeps working; rename it to `cicord portal upgrade --check`.")
	}
}

// deleteYAMLOverrides deletes the overrides of names from a pnpm-workspace.yaml (and the
// overrides key when none is left), keeping the comments and the rest of the file.
func deleteYAMLOverrides(raw []byte, names []string) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return raw, nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return raw, nil, nil
	}
	root := doc.Content[0]
	var removed []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "overrides" || root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		ov := root.Content[i+1]
		var kept []*yaml.Node
		for j := 0; j+1 < len(ov.Content); j += 2 {
			if slices.Contains(names, ov.Content[j].Value) {
				removed = append(removed, ov.Content[j].Value)
				continue
			}
			kept = append(kept, ov.Content[j], ov.Content[j+1])
		}
		ov.Content = kept
		if len(kept) == 0 { // no override left: delete the key, keeping its comment
			if i+2 < len(root.Content) && root.Content[i].HeadComment != "" {
				next := root.Content[i+2]
				next.HeadComment = strings.TrimSpace(root.Content[i].HeadComment + "\n" + next.HeadComment)
			}
			root.Content = slices.Delete(root.Content, i, i+2)
		}
		break
	}
	if len(removed) == 0 {
		return raw, nil, nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(yamlIndent(raw))
	if err := enc.Encode(&doc); err != nil {
		return raw, nil, err
	}
	if err := enc.Close(); err != nil {
		return raw, nil, err
	}
	return out.Bytes(), removed, nil
}

// yamlIndent is the indentation of the first indented line of a YAML file (4, yaml.v3's
// default and that of the files `portal init` writes, when none is).
func yamlIndent(raw []byte) int {
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if n := len(line) - len(trimmed); n > 0 && trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "- ") {
			return n
		}
	}
	return 4
}

// ─────────────────────────────────────────────────────────── vendored packages

type vendorChange struct {
	pkg      kitPackage
	old      string // vendored file now
	oldVer   string
	sameName bool
}

// planVendored compares the vendored @cicord/* tarballs with the ones embedded in this
// binary (the portal kit) and prints the differences.
func planVendored(stdout io.Writer, src string, deps []sdkDep) ([]vendorChange, error) {
	kit, err := portalKit()
	if err != nil {
		return nil, err
	}
	pkgs, err := readKitPackages(kit)
	if err != nil {
		return nil, err
	}
	var changes []vendorChange
	for _, d := range deps {
		i := slices.IndexFunc(pkgs, func(p kitPackage) bool { return p.Name == d.name })
		if i < 0 {
			return nil, fmt.Errorf("this cicord binary embeds no %s package", d.name)
		}
		p := pkgs[i]
		cur, err := os.ReadFile(filepath.Join(src, "vendor", d.vendored))
		if err == nil && d.vendored == p.File && bytes.Equal(cur, p.Data) {
			continue // up to date
		}
		oldVer := "?"
		if err == nil {
			if man, err := tgzPackageJSON(cur); err == nil {
				var v struct{ Version string }
				if json.Unmarshal(man, &v) == nil && v.Version != "" {
					oldVer = v.Version
				}
			}
		}
		fmt.Fprintf(stdout, "  %s %s → %s (vendored)\n", p.Name, oldVer, p.Version)
		changes = append(changes, vendorChange{pkg: p, old: d.vendored, oldVer: oldVer, sameName: d.vendored == p.File})
	}
	return changes, nil
}

// applyVendored writes package.json (pjText, the npm dependencies already set) and
// pnpm-workspace.yaml with the vendored tarballs swapped for this binary's, and deletes the
// old tarballs nothing refers to any more.
func applyVendored(src, pjPath, pjText, wsPath, wsText string, hasWS bool, changes []vendorChange) error {
	for _, c := range changes {
		if err := os.WriteFile(filepath.Join(src, "vendor", c.pkg.File), c.pkg.Data, 0o644); err != nil {
			return err
		}
		pjText = strings.ReplaceAll(pjText, "file:vendor/"+c.old, "file:vendor/"+c.pkg.File)
		wsText = strings.ReplaceAll(wsText, "file:vendor/"+c.old, "file:vendor/"+c.pkg.File)
	}
	if err := os.WriteFile(pjPath, []byte(pjText), 0o644); err != nil { //nolint:gosec // G703: the package.json of the portal the user designated (dir or spec.portal.path)
		return err
	}
	if hasWS && len(changes) > 0 {
		if err := os.WriteFile(wsPath, []byte(wsText), 0o644); err != nil { //nolint:gosec // G703: same portal directory as package.json
			return err
		}
	}
	still := map[string]bool{}
	for _, m := range vendorRefRe.FindAllStringSubmatch(pjText+"\n"+wsText, -1) {
		still[m[1]] = true
	}
	for _, c := range changes {
		if !c.sameName && !still[c.old] {
			if err := os.Remove(filepath.Join(src, "vendor", c.old)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// vendorSourceDir is dir, or the portal sources of the repository in the working
// directory (portalSourceDir).
func vendorSourceDir(dir string) (string, error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(abs, "package.json")); err != nil {
			return "", fmt.Errorf("%s has no package.json", dir)
		}
		return abs, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root := findRepoRoot(wd)
	if root == "" {
		return "", errors.New("not in a Cicord repository (no cicord.yaml): pass the portal directory")
	}
	return portalSourceDir(root)
}
