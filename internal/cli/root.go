// Package cli implements the `cicord` command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/npjonath/cicord/internal/version"
)

// exitError carries a process exit code without printing an extra message.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// Execute runs the CLI and returns the process exit code.
func Execute(args []string) int {
	return ExecuteContext(context.Background(), args, os.Stdout, os.Stderr)
}

// ExecuteContext is Execute with explicit context and output streams (used by tests).
func ExecuteContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			return ee.code
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "cicord",
		Short:         "Cicord — GitOps for Discord",
		Long:          "Cicord declares Discord servers and bots in git, reconciles them continuously and runs TypeScript plugins.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newVersionCmd(),
		newInitCmd(),
		newValidateCmd(),
		newPluginCmd(),
	)
	for _, c := range extraCommands {
		root.AddCommand(c())
	}
	return root
}

// extraCommands lets other files register commands (plan, apply, serve, dev…).
var extraCommands []func() *cobra.Command

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Cicord version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "cicord %s (%s)\n", version.Version, version.Commit)
		},
	}
}
