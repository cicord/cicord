package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
)

func init() {
	pluginSubcommands = append(pluginSubcommands, newPluginPurgeDataCmd)
}

func newPluginPurgeDataCmd() *cobra.Command {
	var rf remoteFlags
	var yes bool
	var output string
	cmd := &cobra.Command{
		Use:   "purge-data <server> <bot>/<instance> [dir]",
		Short: "Delete the data an uninstalled plugin installation left on a server",
		Long: `Delete everything an uninstalled plugin installation left on a server of the runtime:
its records, rankings, search index documents, queued and dead tasks, event deliveries and DM
receipts (POST /api/v1/servers/{server}/orphaned-data/purge).

Removing an installation from git keeps its stored data, so reinstalling it later finds it
again. While that data mentions an erased member, the member data report of the server says
"orphaned" and stays incomplete. This command lists what the installation left
(GET /api/v1/servers/{server}/orphaned-data) and asks for confirmation ('yes', or --yes).
It cannot be undone. The runtime refuses an installation that is still in the active
revision, disabled ones included: remove it from git first.

The runtime URL and token are those of ` + "`cicord status`" + `. In the admin console, server
admins find the same action under Server settings.`,
		Example: `  cicord plugin purge-data my-community moderator/old-tickets
  cicord plugin purge-data my-community moderator/old-tickets --yes --remote=https://cicord.example.com`,
		Args: cobra.RangeArgs(2, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			args = rf.splitRemoteArgs(args)
			if len(args) < 2 || len(args) > 3 {
				return errors.New("expected <server> <bot>/<instance> [dir]")
			}
			server, instance := args[0], args[1]
			if bot, name, ok := strings.Cut(instance, "/"); !ok || bot == "" || name == "" || strings.Contains(name, "/") {
				return fmt.Errorf("expected <bot>/<instance>, got %q", instance)
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			c, err := remoteClient(&rf, args[2:])
			if err != nil {
				return err
			}
			ctx, stdout := cmd.Context(), cmd.OutOrStdout()
			var list struct {
				Items []runtime.OrphanedInstance `json:"items"`
			}
			if err := c.do(ctx, http.MethodGet, serverPath(server, "/orphaned-data"), nil, "", &list); err != nil {
				return err
			}
			var found *runtime.OrphanedInstance
			names := make([]string, 0, len(list.Items))
			for n := range list.Items {
				names = append(names, list.Items[n].Instance)
				if list.Items[n].Instance == instance {
					found = &list.Items[n]
				}
			}
			if found == nil {
				if len(names) == 0 {
					return fmt.Errorf("no uninstalled installation holds data on %s", server)
				}
				return fmt.Errorf("%s holds no orphaned data on %s; uninstalled installations with data: %s", instance, server, strings.Join(names, ", "))
			}
			if output != "json" {
				fmt.Fprintf(stdout, "%s left on %s: %s.\n", instance, server, usageText(found.ScopeUsage))
			}
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Delete all data of %s on %s? This cannot be undone.", instance, server))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("nothing deleted")
				}
			}
			body, _ := json.Marshal(map[string]string{"instance": instance, "confirm": instance})
			var usage state.ScopeUsage
			if err := c.do(ctx, http.MethodPost, serverPath(server, "/orphaned-data/purge"), bytes.NewReader(body), "application/json", &usage); err != nil {
				return err
			}
			if output == "json" {
				return writeJSONOut(stdout, usage)
			}
			fmt.Fprintf(stdout, "Deleted the data of %s on %s: %s.\n", instance, server, usageText(usage))
			return nil
		},
	}
	rf.register(cmd, true)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking for confirmation")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text | json")
	return cmd
}

func usageText(u state.ScopeUsage) string {
	return fmt.Sprintf("%d record(s), %d ranking entry(s), %d index document(s), %d task(s)", u.Records, u.Rankings, u.Index, u.Tasks)
}
