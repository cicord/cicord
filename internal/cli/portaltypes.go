package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/typegen"
	"github.com/npjonath/cicord/internal/version"
)

// routeTypesFile is the file written by `cicord portal typegen`, relative to the portal.
const routeTypesFile = "src/cicord-routes.d.ts"

func newPortalTypegenCmd() *cobra.Command {
	var out string
	var check bool
	cmd := &cobra.Command{
		Use:   "typegen [repo-dir]",
		Short: "Generate the types of the plugin routes for the portal (src/cicord-routes.d.ts)",
		Long: `Writes a TypeScript declaration file describing the portal routes of every plugin
installed in the repository (every plugin instance of every bot), keyed by plugin name:
the route keys and their access level, the path parameters, and the query, body and
response types declared by the plugin's route contract (plugin.yaml spec.routes.types).
Routes of plugins without a contract are typed as untyped JSON.

@cicord/client (client.routes) and @cicord/react (useRouteQuery, useRouteMutation) read it
through a module augmentation, so portal code calling a route that does not exist, or
with the wrong parameters, fails to type-check.

The default output is <portal>/src/cicord-routes.d.ts, <portal> being the directory of
cicord.yaml spec.portal.path (or ./portal). ` + "`cicord portal build`" + ` regenerates it before
building, and ` + "`cicord portal build --check`" + ` fails when it is out of date.`,
		Example: `  cicord portal typegen
  cicord portal typegen --check
  cicord portal typegen examples/showcase --out packages/create-cicord-portal/src/cicord-routes.d.ts`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			root, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			if out == "" {
				src, err := portalSourceDir(root)
				if err != nil {
					return err
				}
				out = filepath.Join(src, filepath.FromSlash(routeTypesFile))
			}
			if out, err = filepath.Abs(out); err != nil {
				return err
			}
			changed, err := writeRouteTypes(cmd.ErrOrStderr(), root, out, check)
			if err != nil {
				return err
			}
			rel := out
			if wd, err := os.Getwd(); err == nil {
				if r, err := filepath.Rel(wd, out); err == nil && filepath.IsLocal(r) {
					rel = r
				}
			}
			switch {
			case check && changed:
				fmt.Fprintf(cmd.ErrOrStderr(), "%s is out of date: run `cicord portal typegen`\n", rel)
				return exitError{1}
			case check:
				fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date\n", rel)
			case changed:
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", rel)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date\n", rel)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "output file (default <portal>/src/cicord-routes.d.ts)")
	cmd.Flags().BoolVar(&check, "check", false, "fail when the file is out of date instead of writing it")
	return cmd
}

// portalSourceDir returns the portal package directory of a repository: the nearest
// directory above cicord.yaml spec.portal.path with a package.json, else ./portal.
func portalSourceDir(root string) (string, error) {
	proj, err := readProject(root)
	if err != nil {
		return "", err
	}
	if proj.Spec.Portal != nil && proj.Spec.Portal.Path != "" {
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
	return "", errors.New("no custom portal found (cicord.yaml spec.portal.path, or ./portal): create one with `cicord portal init`, or pass --out")
}

// writeRouteTypes generates the route types of the repository at root into out. With
// check, it only compares. It reports whether the file changed (or would change).
func writeRouteTypes(stderr io.Writer, root, out string, check bool) (bool, error) {
	content, err := routeTypes(stderr, root)
	if err != nil {
		return false, err
	}
	cur, err := os.ReadFile(out)
	if err == nil && string(cur) == content {
		return false, nil
	}
	if check {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(out, []byte(content), 0o644)
}

// routeTypes builds every plugin of the repository at root and generates the declaration
// file of their portal routes.
func routeTypes(stderr io.Writer, root string) (string, error) {
	repo, ds := config.Load(root, config.LoadOptions{EngineVersion: version.Version, Resolver: defaultResolver(root)})
	if repo == nil || ds.HasErrors() {
		printDiagnostics(stderr, ds.Errors())
		return "", errors.New("the repository does not validate (cicord validate): cannot type its plugin routes")
	}
	plugins, err := routePlugins(repo)
	if err != nil {
		return "", err
	}
	return typegen.GenerateRoutes(plugins)
}

// routePlugins returns the plugins used by the instances of a repository with their
// portal routes and route contract.
func routePlugins(repo *config.Repo) ([]typegen.RoutePlugin, error) {
	var keys []string
	for k := range repo.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	used := map[string]bool{}
	for _, in := range repo.Instances {
		used[in.Plugin.Key] = true
	}
	var out []typegen.RoutePlugin
	for _, k := range keys {
		lp := repo.Plugins[k]
		if !used[k] {
			continue
		}
		rep, err := buildPluginReport(lp.Dir)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", lp.Manifest.Metadata.Name, err)
		}
		if len(rep.Errors) > 0 {
			return nil, fmt.Errorf("plugin %s does not build (cicord plugin build %s): %s", rep.Name, relOr(repo.Root, lp.Dir), rep.Errors[0])
		}
		rp := typegen.RoutePlugin{Name: rep.Name, Version: rep.Version, Source: lp.Source}
		for _, r := range rep.Routes {
			rp.Routes = append(rp.Routes, typegen.Route{Method: r.Method, Path: r.Path, Access: r.Access})
		}
		if len(rp.Routes) > 0 {
			if rp.Contract, err = readRouteContract(lp.Dir, lp.Manifest); err != nil {
				return nil, fmt.Errorf("plugin %s: %w", rep.Name, err)
			}
		}
		out = append(out, rp)
	}
	return out, nil
}

// readRouteContract reads the route contract of a plugin (spec.routes.types), "" when it
// declares none.
func readRouteContract(dir string, m *config.Plugin) (string, error) {
	if m.Spec.Routes == nil || m.Spec.Routes.Types == "" {
		return "", nil
	}
	rel := filepath.Clean(filepath.FromSlash(m.Spec.Routes.Types))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("spec.routes.types %q must be inside the plugin directory", m.Spec.Routes.Types)
	}
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return "", fmt.Errorf("spec.routes.types: %w", err)
	}
	if err := typegen.CheckRouteContract(string(data)); err != nil {
		return "", fmt.Errorf("%s: %w", m.Spec.Routes.Types, err)
	}
	return string(data), nil
}
