package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/npjonath/cicord/internal/catalog"
	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/registry"
	"github.com/npjonath/cicord/internal/setup"
)

func init() {
	pluginSubcommands = append(pluginSubcommands, newPluginUpdateCmd)
}

// newPluginUpdateCmd is `cicord plugin update`: the CLI side of Store updates (spec/api.md
// §11). It edits cicord.lock and bot.yaml (or plugins/<name>/ for copies of official
// plugins) locally; commit the result like any other change.
func newPluginUpdateCmd() *cobra.Command {
	var dir, to string
	var force bool
	cmd := &cobra.Command{
		Use:   "update [<registry>/<plugin>]",
		Short: "Update a plugin to its latest version, or list available updates",
		Long: `Without argument, lists the plugins of the repository that have a newer version:
registry plugins (cicord.lock) and copies of the official plugins (plugins/<name>/).

With a plugin, updates every instance using it: the cicord.lock entry, the version range of
each instance when the new version is outside it, and the grants (set to what the new
version requests). Copies of official plugins (cicord/<name>) are rewritten from the plugins
bundled in this binary; a locally modified copy is only overwritten with --force.
Review the printed capability changes before committing.`,
		Example: `  cicord plugin update
  cicord plugin update acme/xp
  cicord plugin update acme/xp --to ^2.0.0
  cicord plugin update cicord/welcome`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			res := registry.NewResolver(abs, registry.DefaultCacheDir())
			res.Secret = os.LookupEnv
			repo, ds := config.Load(abs, config.LoadOptions{Resolver: res})
			if repo == nil || repo.Project == nil {
				printDiagnostics(cmd.ErrOrStderr(), ds)
				return exitError{1}
			}
			if len(args) == 0 {
				return listPluginUpdates(cmd, repo)
			}
			return updatePlugin(cmd, repo, args[0], to, force)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "repository root")
	cmd.Flags().StringVar(&to, "to", "", "version range to update to (default: the latest version, any major); written to every instance")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite a locally modified copy of an official plugin")
	return cmd
}

func listPluginUpdates(cmd *cobra.Command, repo *config.Repo) error {
	w := cmd.OutOrStdout()
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	n := 0
	fmt.Fprintln(tw, "PLUGIN\tCURRENT\tLATEST\tUPDATE")
	// Copies of official plugins.
	cat := catalog.Official()
	es, _ := cat.List()
	for _, e := range es {
		if !hasCopy(repo, e.Name) {
			continue
		}
		st, err := cat.CheckCopy(e.Name, filepath.Join(repo.Root, filepath.FromSlash(catalog.PluginDir(e.Name))))
		if err != nil {
			continue
		}
		if level := registry.UpdateLevel(st.Version, e.Version); level != "" {
			note := level
			if st.Modified {
				note += " (modified locally: --force)"
			}
			fmt.Fprintf(tw, "%s/%s\t%s\t%s\t%s\n", config.OfficialRegistryName, e.Name, st.Version, e.Version, note)
			n++
		}
	}
	// Registry plugins of cicord.lock.
	lock, err := registry.ReadLock(repo.Root)
	if err != nil {
		return err
	}
	ctx := registry.WithRegistryTokens(cmd.Context(), repo.Project.Spec.Registries, os.LookupEnv)
	// cicord.yaml registries and the implicit official one ("cicord/<plugin>" lock entries).
	regs := registry.WithOfficial(repo.Project.Spec.Registries)
	indexes := map[string]*registry.Index{}
	sources := make([]string, 0, len(lock.Plugins))
	for s := range lock.Plugins {
		sources = append(sources, s)
	}
	slices.Sort(sources)
	for _, src := range sources {
		le := lock.Plugins[src]
		regName, name, err := registry.CheckSource(src)
		if err != nil {
			continue
		}
		i := slices.IndexFunc(regs, func(r config.Registry) bool { return r.Name == regName })
		if i < 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: unknown registry %q\n", src, regName)
			continue
		}
		reg := regs[i]
		idx, ok := indexes[regName]
		if !ok {
			if idx, err = registry.FetchIndex(ctx, reg.URL); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", regName, registry.Redact(err.Error()))
			}
			indexes[regName] = idx
		}
		if idx == nil {
			continue
		}
		ie, ok := idx.Plugins[name]
		if !ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: no longer listed by its registry\n", src)
			continue
		}
		latest, err := registry.LatestVersion(ctx, reg.URL, ie.Repo)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", src, registry.Redact(err.Error()))
			continue
		}
		if level := registry.UpdateLevel(le.Version, latest); level != "" {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", src, le.Version, latest, level)
			n++
		}
	}
	if n == 0 {
		fmt.Fprintln(w, "Every plugin is up to date.")
		return nil
	}
	tw.Flush()
	fmt.Fprintln(w, "\nRun `cicord plugin update <plugin>` to review and apply an update.")
	return nil
}

func updatePlugin(cmd *cobra.Command, repo *config.Repo, source, to string, force bool) error {
	regName, name, err := registry.CheckSource(source)
	if err != nil {
		return err
	}
	if len(setup.PluginServers(repo, source)) == 0 {
		return fmt.Errorf("%s is not installed (use `cicord plugin add`)", source)
	}
	var spec setup.UpdateSpec
	trust := registry.TrustOfficial
	// "cicord/<plugin>": a copy of a bundled official plugin, unless it is locked from the
	// official registry (then it updates like any registry plugin).
	if regName == config.OfficialRegistryName && !setup.LockedSource(repo, source) {
		if to != "" {
			return fmt.Errorf("--to only applies to registry plugins: official plugins update to the version bundled in this binary")
		}
		cat := catalog.Official()
		e, ok := cat.Get(name)
		if !ok {
			return fmt.Errorf("no official plugin %q", name)
		}
		st, err := cat.CheckCopy(name, filepath.Join(repo.Root, filepath.FromSlash(catalog.PluginDir(name))))
		if err != nil {
			return err
		}
		if registry.UpdateLevel(st.Version, e.Version) == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date (%s).\n", source, st.Version)
			return nil
		}
		if st.Modified && !force {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s/ was modified locally; updating overwrites:\n", catalog.PluginDir(name))
			for _, p := range st.Changed {
				fmt.Fprintf(cmd.ErrOrStderr(), "  changed: %s\n", p)
			}
			for _, p := range st.Removed {
				fmt.Fprintf(cmd.ErrOrStderr(), "  deleted: %s\n", p)
			}
			return fmt.Errorf("refusing to overwrite local changes (use --force)")
		}
		files, err := cat.PluginFiles(name)
		if err != nil {
			return err
		}
		spec = setup.UpdateSpec{Source: source, Name: name, DisplayName: e.DisplayName, Version: e.Version,
			Capabilities: e.Capabilities, Intents: e.Intents, PrivilegedIntents: e.PrivilegedIntents, Network: e.Network,
			Schema: e.Schema, Files: files}
	} else {
		rng := to
		if rng == "" {
			rng = "*"
		}
		regs := registry.WithOfficial(repo.Project.Spec.Registries)
		i := slices.IndexFunc(regs, func(r config.Registry) bool { return r.Name == regName })
		if i < 0 {
			return fmt.Errorf("unknown registry %q", regName)
		}
		trust = registry.Trust(regs[i].URL)
		ctx := registry.WithRegistryTokens(cmd.Context(), repo.Project.Spec.Registries, os.LookupEnv)
		res, err := registry.ResolveLatest(ctx, repo.Project.Spec.Registries, registry.DefaultCacheDir(), source, rng)
		if err != nil {
			return err
		}
		// Read what it requests without evaluating its code.
		e, err := catalog.Describe(res.Dir, false)
		if err != nil {
			return fmt.Errorf("%s@%s: %w", source, res.Entry.Version, err)
		}
		if e.Name != name {
			return fmt.Errorf("the repository of %q contains plugin %q", source, e.Name)
		}
		lock := res.Entry
		spec = setup.UpdateSpec{Source: source, Name: name, DisplayName: e.DisplayName, Version: lock.Version,
			Capabilities: e.Capabilities, Intents: e.Intents, PrivilegedIntents: e.PrivilegedIntents, Network: e.Network,
			Schema: e.Schema, Lock: &lock, Range: to}
	}
	plan, err := setup.PlanUpdate(repo, spec)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if plan.Current.Version == spec.Version && (spec.Lock == nil || plan.Current.Commit == spec.Lock.Commit) && len(plan.Edits) <= 1 {
		fmt.Fprintf(w, "%s is up to date (%s).\n", source, spec.Version)
		return nil
	}
	printUpdatePlan(w, plan, trust)
	if !plan.ConfigValid() {
		for _, i := range plan.Instances {
			printDiagnostics(cmd.ErrOrStderr(), i.Diagnostics)
		}
		return fmt.Errorf("the config of an instance is not valid for %s %s: fix it first", source, spec.Version)
	}
	if err := setup.ApplyEdits(repo.Root, plan.Edits); err != nil {
		return err
	}
	fmt.Fprintf(w, "Updated %s to %s. Run `cicord validate`, then commit.\n", source, spec.Version)
	return nil
}

func printUpdatePlan(w io.Writer, plan *setup.UpdatePlan, trust string) {
	spec := plan.Spec
	fmt.Fprintf(w, "%s: %s → %s (%s)\n", spec.Source, plan.Current.Version, spec.Version, trust)
	if plan.RepoChanged {
		fmt.Fprintf(w, "  ! the registry now points to another repository: %s\n", registry.Redact(spec.Lock.Repo))
	}
	d := plan.Diff
	if d.Capabilities.Empty() && d.Intents.Empty() && d.Network.Empty() {
		fmt.Fprintln(w, "  Requests the same capabilities, intents and network access.")
	}
	for _, c := range d.Capabilities.Added {
		fmt.Fprintf(w, "  + capability %s\n", c)
	}
	for _, c := range d.Capabilities.Removed {
		fmt.Fprintf(w, "  - capability %s\n", c)
	}
	for _, in := range d.Intents.Added {
		note := ""
		if slices.Contains(d.PrivilegedIntents.Added, in) {
			note = " (privileged: enable it in the Discord developer portal)"
		}
		fmt.Fprintf(w, "  + intent %s%s\n", in, note)
	}
	for _, in := range d.Intents.Removed {
		fmt.Fprintf(w, "  - intent %s\n", in)
	}
	for _, h := range d.Network.Added {
		fmt.Fprintf(w, "  + network %s\n", h)
	}
	for _, h := range d.Network.Removed {
		fmt.Fprintf(w, "  - network %s\n", h)
	}
	seen := map[string]bool{}
	for _, i := range plan.Instances {
		k := i.Bot + "/" + i.Instance
		if seen[k] || i.NewRange == "" {
			continue
		}
		seen[k] = true
		fmt.Fprintf(w, "  %s: version %s → %s\n", k, orAny(i.Range), i.NewRange)
	}
	if f := plan.Files; f != nil {
		fmt.Fprintf(w, "  %s/: %d changed, %d added, %d deleted\n", catalog.PluginDir(spec.Name), len(f.Changed), len(f.Added), len(f.Removed))
	}
}

// hasCopy: an instance uses the copy of official plugin name in plugins/<name>/.
func hasCopy(repo *config.Repo, name string) bool {
	src := config.OfficialRegistryName + "/" + name
	for _, b := range repo.Bots {
		for _, p := range b.Spec.Plugins {
			if p.IsLocal() && setup.UsesPlugin(b, p, src) {
				return true
			}
		}
	}
	return false
}

func orAny(s string) string {
	if strings.TrimSpace(s) == "" {
		return "*"
	}
	return s
}
