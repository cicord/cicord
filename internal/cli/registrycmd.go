package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/catalog"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/store"
	"github.com/npjonath/codi/internal/writeback"
)

func init() {
	extraCommands = append(extraCommands, newRegistryCmd)
}

var registryNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// newRegistryCmd manages plugin sources (ADR-0011): `codi registry add|remove|list|update|search`.
// It edits codi.yaml locally, like the admin UI does through write-back.
func newRegistryCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage plugin sources (registries) of codi.yaml",
		Long: `Plugin sources work like apt sources or Homebrew taps. The official CODI plugins
(source "codi") are always available; other registries are git repositories with an
index.yaml, declared in codi.yaml spec.registries. They are unofficial: install their
plugins only if you trust them.`,
	}
	cmd.PersistentFlags().StringVar(&dir, "dir", ".", "repository root")

	loadProject := func(cmd *cobra.Command) (*config.Repo, error) {
		repo, ds := config.Load(dir, config.LoadOptions{})
		if repo == nil || repo.Project == nil {
			printDiagnostics(cmd.ErrOrStderr(), ds)
			return nil, exitError{1}
		}
		return repo, nil
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the plugin sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := loadProject(cmd)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTRUST\tURL")
			fmt.Fprintf(tw, "%s\t%s\t%s\n", config.OfficialRegistryName, registry.TrustOfficial, "(bundled with codi)")
			for _, r := range repo.Project.Spec.Registries {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, registry.Trust(r.URL), r.URL)
			}
			return tw.Flush()
		},
	}

	var noCheck bool
	add := &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Add a registry to codi.yaml (https://, file:// or a local path)",
		Example: `  codi registry add acme https://github.com/acme/codi-plugins.git
  codi registry add dev ../my-registry`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, url := args[0], strings.TrimSpace(args[1])
			if !strings.Contains(url, "://") && !strings.Contains(url, "@") {
				// A local path: the runtime resolves it from its own directory, not this one.
				abs, err := filepath.Abs(url)
				if err != nil {
					return err
				}
				url = abs
			}
			switch {
			case !registryNameRe.MatchString(name):
				return fmt.Errorf("registry names use 1 to 32 lowercase letters, digits and dashes")
			case name == config.OfficialRegistryName:
				return fmt.Errorf("the name %q is reserved for the official CODI plugins", name)
			case registry.HasUserinfo(url):
				return fmt.Errorf("registry URLs must not contain credentials: codi.yaml is committed and shown in the admin UI")
			}
			repo, err := loadProject(cmd)
			if err != nil {
				return err
			}
			for _, r := range repo.Project.Spec.Registries {
				if r.Name == name {
					return fmt.Errorf("a registry named %q already exists", name)
				}
				if config.NormalizeRegistryURL(r.URL) == config.NormalizeRegistryURL(url) {
					return fmt.Errorf("this URL is already the registry %q", r.Name)
				}
			}
			if !noCheck {
				idx, err := registry.FetchIndex(cmd.Context(), url)
				if err != nil {
					se := store.ExplainFetch(err)
					return fmt.Errorf("%s %s\n  %s", se.Message, se.Hint, se.Details)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Registry %s lists %d plugin(s).\n", name, len(idx.Plugins))
			}
			if err := editProject(repo.Root, func(cur []byte) ([]byte, error) {
				return writeback.AppendYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("registries")},
					config.Registry{Name: name, URL: url})
			}); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Added %s (%s) to codi.yaml.\n", name, registry.Trust(url))
			if registry.Trust(url) != registry.TrustOfficial {
				fmt.Fprintln(w, "This source is unofficial: review the capabilities of its plugins before installing them.")
			}
			return nil
		},
	}
	add.Flags().BoolVar(&noCheck, "no-check", false, "do not fetch the index before adding")

	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an unused registry from codi.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			repo, err := loadProject(cmd)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(repo.Project.Spec.Registries, func(r config.Registry) bool { return r.Name == name }) {
				return fmt.Errorf("unknown registry %q", name)
			}
			var used []string
			for bk, b := range repo.Bots {
				for _, p := range b.Spec.Plugins {
					if strings.HasPrefix(p.Source, name+"/") {
						used = append(used, fmt.Sprintf("bot %s: plugin %s", bk, p.Name))
					}
				}
			}
			if lock, err := registry.ReadLock(repo.Root); err == nil {
				for src := range lock.Plugins {
					if strings.HasPrefix(src, name+"/") {
						used = append(used, registry.LockFile+": "+src)
					}
				}
			}
			if len(used) > 0 {
				slices.Sort(used)
				return fmt.Errorf("registry %q is still used:\n  %s", name, strings.Join(used, "\n  "))
			}
			if err := editProject(repo.Root, func(cur []byte) ([]byte, error) {
				return writeback.SetYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("registries"), writeback.Match("name", name)}, nil)
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from codi.yaml.\n", name)
			return nil
		},
	}

	update := &cobra.Command{
		Use:   "update",
		Short: "Fetch the index of every registry and report what it lists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := loadProject(cmd)
			if err != nil {
				return err
			}
			s := &store.Service{CacheDir: registry.DefaultCacheDir()}
			srcs, err := s.Sources(cmd.Context(), repo, true)
			if err != nil {
				return err
			}
			failed := false
			w := cmd.OutOrStdout()
			for _, src := range srcs[1:] { // [0] is the bundled source, nothing to fetch
				if src.Error != nil {
					failed = true
					fmt.Fprintf(w, "✗ %s: %s %s\n", src.Name, src.Error.Message, src.Error.Hint)
					continue
				}
				fmt.Fprintf(w, "✓ %s (%s): %d plugin(s)\n", src.Name, src.Trust, len(src.Plugins))
			}
			if len(srcs) == 1 {
				fmt.Fprintln(w, "No registry in codi.yaml: only the official plugins bundled with codi are available.")
			}
			if failed {
				return exitError{1}
			}
			return nil
		},
	}

	search := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the plugins of every source",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := loadProject(cmd)
			if err != nil {
				return err
			}
			q := ""
			if len(args) == 1 {
				q = strings.ToLower(args[0])
			}
			s := &store.Service{Catalog: catalog.Official(), CacheDir: registry.DefaultCacheDir()}
			srcs, err := s.Sources(cmd.Context(), repo, false)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PLUGIN\tTRUST\tDESCRIPTION")
			n := 0
			for _, src := range srcs {
				if src.Error != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %s\n", src.Name, src.Error.Message)
				}
				for _, p := range src.Plugins {
					hay := strings.ToLower(strings.Join(append([]string{p.Name, p.DisplayName, p.Description}, p.Keywords...), " "))
					if q != "" && !strings.Contains(hay, q) {
						continue
					}
					n++
					fmt.Fprintf(tw, "%s/%s\t%s\t%s\n", p.Source, p.Name, p.Trust, p.Description)
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if n == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No plugin found.")
			}
			return nil
		},
	}

	cmd.AddCommand(list, add, remove, update, search)
	return cmd
}

// editProject rewrites codi.yaml with edit and checks the result still loads.
func editProject(root string, edit func([]byte) ([]byte, error)) error {
	p := filepath.Join(root, "codi.yaml")
	cur, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	next, err := edit(cur)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, next, 0o644); err != nil {
		return err
	}
	if _, ds := config.Load(root, config.LoadOptions{}); ds.HasErrors() {
		_ = os.WriteFile(p, cur, 0o644)
		return errors.New("codi.yaml would become invalid: " + ds.Err().Error())
	}
	return nil
}
