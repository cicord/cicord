package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/planfmt"
	"github.com/cicord/cicord/internal/runtime"
)

func init() {
	extraCommands = append(extraCommands, newPauseCmd, newResumeCmd)
}

// serverPath is the API path of a server resource.
func serverPath(server, rest string) string {
	return "/api/v1/servers/" + url.PathEscape(server) + rest
}

// serverArgs splits `<server> [dir]` (a URL after a bare --remote is the runtime URL).
func serverArgs(rf *remoteFlags, args []string) (server string, dir []string, err error) {
	args = rf.splitRemoteArgs(args)
	switch len(args) {
	case 0:
		return "", nil, errors.New("missing the server key")
	case 1, 2:
		return args[0], args[1:], nil
	}
	return "", nil, fmt.Errorf("too many arguments: %v", args)
}

func newPauseCmd() *cobra.Command {
	var rf remoteFlags
	var reason, output string
	cmd := &cobra.Command{
		Use:   "pause <server> [dir]",
		Short: "Pause enforcement on a server (emergency): nothing is applied until it is resumed",
		Long: `Pause enforcement on a server of the runtime (POST /api/v1/servers/{server}/pause).

While a server is paused, the runtime applies nothing to it: not a new revision, not Sync
now, not the periodic resync, not syncPolicy.selfHeal after a gateway event. Changes made
on Discord, such as a channel locked during a raid, stay. Plans and drift are still
computed: ` + "`cicord status`" + ` and the admin console show what differs from git. Plugins keep
running. The pause is runtime state (not git): it survives restarts and new revisions
until ` + "`cicord resume`" + `. Pausing a paused server changes nothing.

The runtime URL and token are those of ` + "`cicord status`" + `. The runtime token acts as an
admin of every server; in the admin console, moderators and admins of the server can pause.`,
		Example: `  cicord pause my-community --reason "raid: #general locked by hand"
  cicord pause my-community --remote=https://cicord.example.com`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			server, rest, err := serverArgs(&rf, args)
			if err != nil {
				return err
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			c, err := remoteClient(&rf, rest)
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]string{"reason": reason})
			var out struct {
				Paused  runtime.PauseInfo `json:"paused"`
				Changed bool              `json:"changed"`
			}
			if err := c.do(cmd.Context(), http.MethodPost, serverPath(server, "/pause"), bytes.NewReader(body), "application/json", &out); err != nil {
				return err
			}
			if output == "json" {
				return writeJSONOut(cmd.OutOrStdout(), out)
			}
			p := out.Paused
			if !out.Changed {
				fmt.Fprintf(cmd.OutOrStdout(), "Server %s was already paused by %s since %s.\n", server, p.Who(), p.At.Local().Format(time.RFC3339))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Enforcement paused on %s: nothing is applied until `cicord resume %s`.\n", server, server)
			return nil
		},
	}
	rf.register(cmd, true)
	cmd.Flags().StringVar(&reason, "reason", "", fmt.Sprintf("why enforcement is paused (shown in the admin console and the history, at most %d characters)", runtime.MaxPauseReason))
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text | json")
	return cmd
}

func newResumeCmd() *cobra.Command {
	var rf remoteFlags
	var yes, syncNow bool
	var output string
	cmd := &cobra.Command{
		Use:   "resume <server> [dir]",
		Short: "Resume enforcement on a paused server, after showing the pending plan",
		Long: `Resume enforcement on a paused server (POST /api/v1/servers/{server}/resume).

The pending plan is shown first: what the next sync applies to bring the server back to
git (it reverts the changes made on Discord during the pause). Confirm with 'yes', or pass
--yes. Resuming applies nothing by itself: the next revision, Sync now, or the periodic
resync and gateway events with syncPolicy.selfHeal do. --sync resumes and syncs now
(POST /api/v1/servers/{server}/sync with {"resume": true}).`,
		Example: `  cicord resume my-community
  cicord resume my-community --yes --sync --remote=https://cicord.example.com`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			server, rest, err := serverArgs(&rf, args)
			if err != nil {
				return err
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			c, err := remoteClient(&rf, rest)
			if err != nil {
				return err
			}
			ctx, stdout, stderr := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			var st struct {
				Paused    *runtime.PauseInfo `json:"paused"`
				Plan      *planfmt.Plan      `json:"plan"`
				PlanError string             `json:"planError"`
			}
			if err := c.do(ctx, http.MethodGet, serverPath(server, "/pause"), nil, "", &st); err != nil {
				return err
			}
			if st.Paused == nil {
				fmt.Fprintf(stderr, "Server %s is not paused.\n", server)
				if output == "json" {
					return writeJSONOut(stdout, map[string]any{"resumed": false})
				}
				return nil
			}
			p := st.Paused
			planOut := stdout
			if output == "json" {
				planOut = stderr
			}
			fmt.Fprintf(planOut, "Server %s is paused by %s since %s", server, p.Who(), p.At.Local().Format(time.RFC3339))
			if p.Reason != "" {
				fmt.Fprintf(planOut, " (%s)", p.Reason)
			}
			fmt.Fprintln(planOut, ".")
			switch {
			case st.Plan != nil:
				what := "The next sync"
				if syncNow {
					what = "The sync"
				}
				fmt.Fprintf(planOut, "%s applies this plan:\n\n", what)
				planfmt.Text(planOut, []planfmt.Plan{*st.Plan})
			default:
				fmt.Fprintf(planOut, "The pending plan cannot be computed: %s\n", st.PlanError)
			}
			if !yes {
				ok, err := confirm(cmd, "\nResume enforcement on "+server+"?")
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(stderr, "Resume cancelled: the server stays paused.")
					return exitError{1}
				}
			}
			if syncNow {
				var out struct {
					Applied int `json:"applied"`
				}
				body := bytes.NewReader([]byte(`{"resume": true}`))
				if err := c.do(ctx, http.MethodPost, serverPath(server, "/sync"), body, "application/json", &out); err != nil {
					return err
				}
				if output == "json" {
					return writeJSONOut(stdout, map[string]any{"resumed": true, "applied": out.Applied})
				}
				fmt.Fprintf(stdout, "Enforcement resumed on %s and synced: %d change(s) applied.\n", server, out.Applied)
				return nil
			}
			var out struct {
				Resumed bool `json:"resumed"`
			}
			if err := c.do(ctx, http.MethodPost, serverPath(server, "/resume"), nil, "", &out); err != nil {
				return err
			}
			if output == "json" {
				return writeJSONOut(stdout, out)
			}
			fmt.Fprintf(stdout, "Enforcement resumed on %s: the next revision or sync applies the plan.\n", server)
			return nil
		},
	}
	rf.register(cmd, true)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "resume without asking for confirmation")
	cmd.Flags().BoolVar(&syncNow, "sync", false, "sync the server right after resuming (applies the plan now)")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text | json")
	return cmd
}

// remoteClient is the API client of a remote command: the runtime URL of --remote or
// CICORD_RUNTIME_URL (dir is the optional project directory).
func remoteClient(rf *remoteFlags, dir []string) (*apiClient, error) {
	d, err := absDir(dir)
	if err != nil {
		return nil, err
	}
	base, err := rf.resolveURL(d)
	if err != nil {
		return nil, err
	}
	return newAPIClient(base)
}
