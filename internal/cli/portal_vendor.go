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
	"strings"

	"github.com/spf13/cobra"
)

// ───────────────────────────────────────────────────────────── cicord portal vendor

func newPortalVendorCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "vendor [dir]",
		Short: "Update the vendored @cicord/* packages of a custom portal to this cicord version",
		Long: `Replaces the @cicord/* tarballs a custom portal vendors (vendor/, written by
` + "`cicord portal init`" + `) with the ones embedded in this cicord binary, and points
package.json and pnpm-workspace.yaml at them; the old tarballs are deleted. Run it after
upgrading cicord, then install and build the portal.

[dir] is the portal's directory (the one with package.json); by default the nearest one
above cicord.yaml spec.portal.path. --check changes nothing and fails when the vendored
packages are not this version's (CI). A portal that depends on the npm registry
(` + "`portal init --from-npm`" + `) is left alone.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ""
			if len(args) == 1 {
				dir = args[0]
			}
			return portalVendor(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, check)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "fail when the vendored packages are not this version's (changes nothing)")
	return cmd
}

// vendorRefRe matches a dependency on a vendored tarball in package.json or
// pnpm-workspace.yaml: "file:vendor/<file>.tgz".
var vendorRefRe = regexp.MustCompile(`file:vendor/([A-Za-z0-9._-]+\.tgz)`)

func portalVendor(stdout, stderr io.Writer, dir string, check bool) error {
	src, err := vendorSourceDir(dir)
	if err != nil {
		return err
	}
	kit, err := portalKit()
	if err != nil {
		return err
	}
	pkgs, err := readKitPackages(kit)
	if err != nil {
		return err
	}
	pjPath := filepath.Join(src, "package.json")
	pjRaw, err := os.ReadFile(pjPath)
	if err != nil {
		return err
	}
	var pj struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(pjRaw, &pj); err != nil {
		return fmt.Errorf("package.json: %w", err)
	}
	wsPath := filepath.Join(src, "pnpm-workspace.yaml")
	wsRaw, wsErr := os.ReadFile(wsPath)
	if wsErr != nil && !errors.Is(wsErr, os.ErrNotExist) {
		return wsErr
	}

	type change struct {
		pkg      kitPackage
		old      string // vendored file now
		oldVer   string
		sameName bool
	}
	var changes []change
	vendored := 0
	for _, p := range pkgs {
		spec := pj.Dependencies[p.Name]
		if spec == "" {
			spec = pj.DevDependencies[p.Name]
		}
		m := vendorRefRe.FindStringSubmatch(spec)
		if m == nil {
			if spec != "" && !strings.HasPrefix(spec, "file:") {
				fmt.Fprintf(stderr, "  %s comes from the npm registry (%s): bump it to ^%s yourself\n", p.Name, spec, p.Version)
			}
			continue
		}
		vendored++
		cur, err := os.ReadFile(filepath.Join(src, "vendor", m[1]))
		if err == nil && m[1] == p.File && bytes.Equal(cur, p.Data) {
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
		changes = append(changes, change{pkg: p, old: m[1], oldVer: oldVer, sameName: m[1] == p.File})
	}
	rel := relOr(findRepoRoot(src), src)
	if vendored == 0 {
		fmt.Fprintf(stdout, "%s vendors no @cicord/* package: nothing to do\n", rel)
		return nil
	}
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "%s: the vendored @cicord/* packages are this version's\n", rel)
		return nil
	}
	for _, c := range changes {
		fmt.Fprintf(stdout, "  %s %s → %s\n", c.pkg.Name, c.oldVer, c.pkg.Version)
	}
	if check {
		fmt.Fprintf(stderr, "%s vendors other @cicord/* packages than this cicord: run `cicord portal vendor` and commit\n", rel)
		return exitError{1}
	}
	pjText, wsText := string(pjRaw), string(wsRaw)
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
	if wsErr == nil {
		if err := os.WriteFile(wsPath, []byte(wsText), 0o644); err != nil { //nolint:gosec // G703: same portal directory as package.json
			return err
		}
	}
	// Delete the replaced tarballs nothing refers to any more.
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
	fmt.Fprintf(stdout, "Updated the vendored packages of %s. Next: install (pnpm install in %s), `cicord portal build`, commit.\n", rel, rel)
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
