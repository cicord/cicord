package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/writeback"
)

func init() {
	defaultResolver = func(dir string) config.PluginResolver {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil
		}
		return registry.NewResolver(abs, registry.DefaultCacheDir())
	}
	pluginSubcommands = append(pluginSubcommands, newPluginAddCmd)
}

// yamlMapPlugin is the bot.yaml entry written by `codi plugin add`.
type yamlMapPlugin struct {
	Name    string   `yaml:"name"`
	Source  string   `yaml:"source"`
	Version string   `yaml:"version"`
	Grants  []string `yaml:"grants,omitempty"`
}

func newPluginAddCmd() *cobra.Command {
	var repoDir, bot, name string
	cmd := &cobra.Command{
		Use:   "add <registry>/<plugin>[@<range>]",
		Short: "Add a registry plugin to a bot and lock it in codi.lock",
		Example: `  codi plugin add acme/hello --bot moderator
  codi plugin add acme/xp@^1.2.0 --bot moderator --name xp`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, rng, _ := strings.Cut(args[0], "@")
			if rng == "" {
				rng = "*"
			}
			repo, ds := config.Load(repoDir, config.LoadOptions{})
			if repo == nil || repo.Project == nil {
				printDiagnostics(cmd.ErrOrStderr(), ds)
				return exitError{1}
			}
			b, ok := repo.Bots[bot]
			if !ok {
				return fmt.Errorf("unknown bot %q", bot)
			}
			defer registry.UseSerialStore(registry.FileSerialStore(registry.DefaultSerialFile()))()
			ctx := registry.WithRegistryTokens(cmd.Context(), repo.Project.Spec.Registries, os.LookupEnv)
			res, err := registry.ResolveLatest(ctx, repo.Project.Spec.Registries, registry.DefaultCacheDir(), source, rng)
			if err != nil {
				return err
			}
			lock, err := registry.ReadLock(repo.Root)
			if err != nil {
				return err
			}
			lock.Plugins[source] = res.Entry
			if err := registry.WriteLock(repo.Root, lock); err != nil {
				return err
			}
			if name == "" {
				_, name, _ = strings.Cut(source, "/")
			}
			versionRange := rng
			if rng == "*" {
				versionRange = "^" + res.Entry.Version
			}
			botFile := filepath.Join(repo.Root, filepath.FromSlash(b.File))
			data, err := os.ReadFile(botFile)
			if err != nil {
				return err
			}
			for _, p := range b.Spec.Plugins {
				if p.Name == name {
					return fmt.Errorf("bot %q already has a plugin instance named %q (use --name)", bot, name)
				}
			}
			out, err := writeback.AppendYAML(data, []writeback.PathElem{writeback.Key("spec"), writeback.Key("plugins")},
				yamlMapPlugin{Name: name, Source: source, Version: versionRange, Grants: res.Entry.Capabilities})
			if err != nil {
				return err
			}
			if err := os.WriteFile(botFile, out, 0o644); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			verified := ""
			if res.Index.Verified {
				verified = " ✓ verified"
			}
			fmt.Fprintf(w, "Added %s@%s%s to bot %s as %q\n", source, res.Entry.Version, verified, bot, name)
			if len(res.Entry.Capabilities) > 0 {
				fmt.Fprintf(w, "Granted capabilities (review them in %s):\n", b.File)
				for _, c := range res.Entry.Capabilities {
					fmt.Fprintf(w, "  - %s\n", c)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoDir, "dir", ".", "repository root")
	cmd.Flags().StringVar(&bot, "bot", "", "bot key")
	cmd.Flags().StringVar(&name, "name", "", "instance name (default: plugin name)")
	_ = cmd.MarkFlagRequired("bot")
	return cmd
}
