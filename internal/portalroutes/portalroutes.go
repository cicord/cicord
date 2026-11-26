// Package portalroutes generates the route types of a custom portal
// (<portal>/src/cicord-routes.d.ts): the portal routes of every plugin used by an instance
// of the repository, typed by their route contracts. `cicord portal typegen` (and its
// --check, run by the generated cicord-portal workflow) and the write-back of Store
// actions (install, update, uninstall) call the same code, so the file the runtime
// commits is byte for byte what the CLI expects.
//
// The output depends only on the repository: the plugins' names, versions, sources as
// written in bot.yaml, routes and contracts. It never embeds the version of the cicord
// binary that generated it, so an older pinned cicord in CI checks a file written by a
// newer runtime (and conversely) as long as typegen.GenerateRoutes keeps its format.
package portalroutes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/typegen"
)

// File is the route types file, relative to the portal sources.
const File = "src/cicord-routes.d.ts"

// ErrNoPortal: the repository has no custom portal sources.
var ErrNoPortal = errors.New("no custom portal found (cicord.yaml spec.portal.path, or ./portal): create one with `cicord portal init`, or pass --out")

// SourceDir returns the portal package directory of the repository at root: the nearest
// directory above cicord.yaml spec.portal.path (proj may be nil) with a package.json, else
// ./portal when it has one. ErrNoPortal otherwise.
func SourceDir(root string, proj *config.Project) (string, error) {
	if proj != nil && proj.Spec.Portal != nil && proj.Spec.Portal.Path != "" {
		rel, ok := config.ResolveRepoPath("cicord.yaml", proj.Spec.Portal.Path)
		if !ok {
			return "", fmt.Errorf("spec.portal.path %q must be inside the repository", proj.Spec.Portal.Path)
		}
		out := filepath.Join(root, filepath.FromSlash(rel))
		for d := filepath.Dir(out); strings.HasPrefix(d, root) && d != root; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "package.json")); err == nil {
				return d, nil
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "portal", "package.json")); err == nil {
		return filepath.Join(root, "portal"), nil
	}
	return "", ErrNoPortal
}

// Tracked returns the repository-relative (slash-separated) path of the route types file
// of the repository at root when its custom portal already has one, "" otherwise. Only an
// existing file is kept up to date: a portal that does not use typed routes is left alone.
func Tracked(root string, proj *config.Project) string {
	src, err := SourceDir(root, proj)
	if err != nil {
		return ""
	}
	p := filepath.Join(src, filepath.FromSlash(File))
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// Result is the regeneration of the route types file after a plugin change.
type Result struct {
	// Path of the route types file in the repository, slash-separated ("" when the portal
	// has none: nothing to keep up to date).
	Path string
	// Content is the regenerated file (nil when it is unchanged or could not be generated).
	Content []byte
	// Removed lists the routes ("<plugin> <key>") the change removes from the portal: portal
	// code still calling them no longer type-checks.
	Removed []string
	// Warnings explain why the file could not be regenerated.
	Warnings []string
}

// Regenerate generates the route types of the repository in dir (loaded as repo, with the
// plugin change applied) when its custom portal already has the file. The Store's
// write-back and `cicord plugin add` / `update` call it, so that the portal's CI check
// (`cicord portal typegen --check`) stays green.
func Regenerate(dir string, repo *config.Repo) Result {
	rel := Tracked(dir, repo.Project)
	if rel == "" {
		return Result{}
	}
	out := Result{Path: rel}
	cur, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		out.Warnings = []string{NotRegenerated(rel, err)}
		return out
	}
	next, err := Generate(repo)
	if err != nil {
		out.Warnings = []string{NotRegenerated(rel, err)}
		return out
	}
	if next == string(cur) {
		return out
	}
	out.Content = []byte(next)
	after := Keys(next)
	for k := range Keys(string(cur)) {
		if !after[k] {
			out.Removed = append(out.Removed, k)
		}
	}
	sort.Strings(out.Removed)
	return out
}

// NotRegenerated is the warning of a route types file that could not be regenerated.
func NotRegenerated(rel string, err error) string {
	return fmt.Sprintf("%s was not regenerated (%v): run `cicord portal typegen` and commit it, or the portal's CI check fails.", rel, err)
}

// Generate returns the route types file of a loaded repository.
func Generate(repo *config.Repo) (string, error) {
	plugins, err := Plugins(repo)
	if err != nil {
		return "", err
	}
	return typegen.GenerateRoutes(plugins)
}

// Plugins returns the plugins used by the instances of a repository with their portal
// routes (from their built definition) and route contract.
func Plugins(repo *config.Repo) ([]typegen.RoutePlugin, error) {
	keys := make([]string, 0, len(repo.Plugins))
	for k := range repo.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	used := map[string]bool{}
	for _, in := range repo.Instances {
		if !in.Disabled() && in.Plugin != nil {
			used[in.Plugin.Key] = true
		}
	}
	var out []typegen.RoutePlugin
	for _, k := range keys {
		lp := repo.Plugins[k]
		if !used[k] || lp == nil || lp.Manifest == nil {
			continue
		}
		name := lp.Manifest.Metadata.Name
		routes, err := Routes(lp.Dir, lp.Manifest)
		if err != nil {
			return nil, fmt.Errorf("plugin %s does not build (cicord plugin build %s): %w", name, relOr(repo.Root, lp.Dir), err)
		}
		rp := typegen.RoutePlugin{Name: name, Version: lp.Manifest.Metadata.Version, Source: lp.Source, Routes: routes}
		if len(rp.Routes) > 0 {
			if rp.Contract, err = ReadContract(lp.Dir, lp.Manifest); err != nil {
				return nil, fmt.Errorf("plugin %s: %w", name, err)
			}
		}
		out = append(out, rp)
	}
	return out, nil
}

// Routes bundles the plugin in dir and returns the portal routes of its definition.
func Routes(dir string, m *config.Plugin) ([]typegen.Route, error) {
	res, err := build.BundleAssets(dir, m.Spec.Entry, m.Spec.Assets)
	if err != nil {
		var be *build.Error
		if errors.As(err, &be) && len(be.Messages) > 0 {
			return nil, errors.New(be.Messages[0].String())
		}
		return nil, err
	}
	def, err := plugin.Inspect(res.Code)
	if err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}
	var out []typegen.Route
	for _, r := range def.Routes {
		out = append(out, typegen.Route{Method: r.Method, Path: r.Path, Access: r.Access})
	}
	return out, nil
}

// ReadContract reads the route contract of a plugin (spec.routes.types), "" when it
// declares none.
func ReadContract(dir string, m *config.Plugin) (string, error) {
	if m.Spec.Routes == nil || m.Spec.Routes.Types == "" {
		return "", nil
	}
	rel := filepath.Clean(filepath.FromSlash(m.Spec.Routes.Types))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("spec.routes.types %q must be inside the plugin directory", m.Spec.Routes.Types)
	}
	data, err := build.ReadPackageFile(dir, rel)
	if err != nil {
		return "", fmt.Errorf("spec.routes.types: %w", err)
	}
	if err := typegen.CheckRouteContract(string(data)); err != nil {
		return "", fmt.Errorf("%s: %w", m.Spec.Routes.Types, err)
	}
	return string(data), nil
}

var (
	pluginLine = regexp.MustCompile(`^    "([^"]+)": \{$`)
	routeLine  = regexp.MustCompile(`^      "([^"]+)": RouteEntry<`)
)

// Keys lists the routes typed by a generated file, as "<plugin> <route key>"
// ("tickets GET /tickets"): what a change of the file removes can be told to the people
// reviewing it, as portal code calling a removed route no longer type-checks.
func Keys(content string) map[string]bool {
	out := map[string]bool{}
	cur := ""
	for _, line := range strings.Split(content, "\n") {
		if m := pluginLine.FindStringSubmatch(line); m != nil {
			cur = m[1]
		} else if m := routeLine.FindStringSubmatch(line); m != nil && cur != "" {
			out[cur+" "+m[1]] = true
		}
	}
	return out
}

func relOr(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && filepath.IsLocal(r) {
		return filepath.ToSlash(r)
	}
	return p
}
