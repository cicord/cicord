package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/portalroutes"
	"github.com/cicord/cicord/internal/version"
)

// routeTypesFile is the file written by `cicord portal typegen`, relative to the portal.
const routeTypesFile = portalroutes.File

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
	return portalroutes.SourceDir(root, proj)
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
// file of their portal routes (the code the write-back of Store actions runs too).
func routeTypes(stderr io.Writer, root string) (string, error) {
	repo, ds := config.Load(root, config.LoadOptions{EngineVersion: version.Version, Resolver: defaultResolver(root)})
	if repo == nil || ds.HasErrors() {
		printDiagnostics(stderr, ds.Errors())
		return "", errors.New("the repository does not validate (cicord validate): cannot type its plugin routes")
	}
	return portalroutes.Generate(repo)
}

// refreshRouteTypes keeps the route types of the repository's custom portal in line after
// a plugin change made by the CLI (`cicord plugin add`, `cicord plugin update`), like the
// write-back of the admin's Store: only a file the portal already has, only when its
// content changes, a warning (not an error) when it cannot be generated.
func refreshRouteTypes(stdout, stderr io.Writer, dir string) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	proj, err := readProject(root)
	if err != nil {
		return
	}
	rel := portalroutes.Tracked(root, proj)
	if rel == "" {
		return
	}
	var r portalroutes.Result
	repo, ds := config.Load(root, config.LoadOptions{EngineVersion: version.Version, Resolver: defaultResolver(root)})
	if repo == nil || ds.HasErrors() {
		r = portalroutes.Result{Path: rel, Warnings: []string{portalroutes.NotRegenerated(rel,
			errors.New("the repository does not validate: run `cicord validate`"))}}
	} else {
		r = portalroutes.Regenerate(root, repo)
	}
	if r.Content != nil {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), r.Content, 0o644); err != nil {
			r.Warnings = append(r.Warnings, portalroutes.NotRegenerated(rel, err))
		} else {
			fmt.Fprintf(stdout, "Regenerated %s (cicord portal typegen).\n", rel)
			if len(r.Removed) > 0 {
				fmt.Fprintf(stdout, "Routes no longer available to the portal: %s. Portal code still calling them no longer type-checks.\n",
					strings.Join(r.Removed, ", "))
			}
		}
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
}
