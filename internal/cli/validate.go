package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/version"
)

func newValidateCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "validate [dir]",
		Short: "Validate a CODI repository (schemas + semantic checks), offline",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			repo, ds := loadRepo(dir)
			if output == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if ds == nil {
					ds = config.Diagnostics{}
				}
				_ = enc.Encode(map[string]any{"valid": !ds.HasErrors(), "diagnostics": ds})
			} else {
				printDiagnostics(cmd.ErrOrStderr(), ds)
				if !ds.HasErrors() && repo != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "✓ %s: %d server(s), %d bot(s), %d plugin instance(s)\n",
						repo.Project.Metadata.Name, len(repo.Servers), len(repo.Bots), len(repo.Instances))
				}
			}
			if ds.HasErrors() {
				return exitError{1}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text | json")
	return cmd
}

func loadRepo(dir string) (*config.Repo, config.Diagnostics) {
	return config.Load(dir, config.LoadOptions{EngineVersion: version.Version, Resolver: defaultResolver(dir)})
}

func printDiagnostics(w io.Writer, ds config.Diagnostics) {
	for _, d := range ds {
		fmt.Fprintln(w, d.String())
	}
}

// defaultResolver returns the registry resolver for a repo (nil until the registry exists).
var defaultResolver = func(dir string) config.PluginResolver { return nil }
