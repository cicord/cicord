package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/importer"
	"github.com/cicord/cicord/internal/planfmt"
	"github.com/cicord/cicord/internal/setup"
)

func init() {
	extraCommands = append(extraCommands, newInspectCmd)
}

func newInspectCmd() *cobra.Command {
	var lf localFlags
	var tokenEnv, key string
	cmd := &cobra.Command{
		Use:   "inspect <server-id> [dir]",
		Short: "Inspect an existing Discord server read-only before granting the bot more access",
		Long: `Evaluate an existing server without changing it. Invite the bot with View Channels only
(the wizard's "Inspect first" link, or permissions=1024), then run this command with the bot
token in the environment.

It imports the server into a new project in dir (cicord.yaml, servers/<key>.yaml,
bots/<bot>/bot.yaml, downloaded icons and emojis) and the resource map into --state,
then reports:

  - the categories and channels the bot can see, and the hidden ones it knows of
    (Discord leaves the channels a bot cannot view out of the list: the report counts
    the ones the server refers to, such as the category of a visible channel);
  - what it could not read and why (AutoMod, the widget and the welcome screen need
    Manage Server);
  - the permissions the bot would need to apply changes to this configuration, with the
    YAML paths that need each one, and the role order;
  - the plan of the imported configuration, which must be empty.

Every Discord call goes through a read-only client: a mutating call would be refused
before reaching Discord. Nothing is changed on Discord. dir must not contain a
cicord.yaml (use ` + "`cicord import`" + ` to add a server to an existing project).`,
		Example: `  DISCORD_TOKEN=… cicord inspect 112233445566778899 ./my-server
  cicord inspect 112233445566778899 ./my-server --key my-community --token-env DISCORD_TOKEN_EVAL`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := absDir(args[1:])
			if err != nil {
				return err
			}
			return runInspect(cmd, args[0], dir, key, tokenEnv, &lf)
		},
	}
	lf.register(cmd)
	cmd.Flags().StringVar(&tokenEnv, "token-env", "DISCORD_TOKEN", "environment variable holding the bot token (written as the bot's secretRef)")
	cmd.Flags().StringVar(&key, "key", "", "server key (default: derived from the server name)")
	return cmd
}

func runInspect(cmd *cobra.Command, serverID, dir, key, tokenEnv string, lf *localFlags) error {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if _, err := os.Stat(filepath.Join(dir, "cicord.yaml")); err == nil {
		return fmt.Errorf("%s already contains a cicord.yaml: inspect writes a new project, pick an empty directory (or use `cicord import` to add the server to this project)", dir)
	}
	if key != "" && !importer.KeyPattern(key) {
		return fmt.Errorf("--key %q is not a valid key (^[a-z][a-z0-9-]{0,62}$)", key)
	}
	token, ok := lookupSecret(tokenEnv)
	if !ok || strings.TrimSpace(token) == "" {
		return fmt.Errorf("the environment variable %s (bot token) is not set", tokenEnv)
	}
	sess, err := newConnector().Connect(ctx, token)
	if err != nil {
		return err
	}
	defer sess.Close(context.WithoutCancel(ctx))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	store, statePath, err := lf.openState(dir, false)
	if err != nil {
		return err
	}
	defer store.Close()
	self := sess.Self()
	rep, err := setup.Inspect(ctx, setup.InspectOptions{Client: sess, ServerID: serverID, BotUserID: self.ID, BotName: self.Username,
		ServerKey: key, BotKey: importer.Slug(self.Username, "bot"), ApplicationID: sess.ApplicationID(), SecretRef: tokenEnv,
		Dir: dir, Store: store, Download: newDownloader()})
	if rep == nil {
		return err
	}
	writeInspectReport(stdout, rep, dir, statePath)
	for _, w := range rep.Warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	ps, ferr := planfmt.FromReconcile(rep.Plan)
	if ferr != nil {
		return ferr
	}
	fmt.Fprintln(stdout, "Plan (computed read-only):")
	planfmt.Text(stdout, ps)
	fmt.Fprintln(stdout)
	if rep.Writes > 0 { // cannot happen: every mutating call is refused before Discord
		fmt.Fprintf(stdout, "Nothing was changed on Discord: %d read call(s); %d write attempt(s) were refused before reaching Discord.\n", rep.Reads, rep.Writes)
	} else {
		fmt.Fprintf(stdout, "Nothing was changed on Discord: %d read call(s), 0 write calls.\n", rep.Reads)
	}
	if errors.Is(err, setup.ErrNotEmptyPlan) || errors.Is(err, setup.ErrIncompleteView) {
		fmt.Fprintln(stderr, "error:", err)
		return exitError{1}
	}
	if err != nil {
		return err
	}
	if rep.Ready {
		fmt.Fprintln(stdout, "Next: run `cicord plan` in this directory, then `cicord apply` when you change the YAML.")
	} else {
		fmt.Fprintln(stdout, "You can stop here. To let Cicord manage the server, grant the permissions above, inspect again")
		fmt.Fprintln(stdout, "into a new directory (the configuration then includes what the bot could not see or read),")
		fmt.Fprintln(stdout, "and run `cicord plan` there.")
	}
	return nil
}

// writeInspectReport prints the access report of an inspection.
func writeInspectReport(w io.Writer, rep *setup.InspectReport, dir, statePath string) {
	fmt.Fprintf(w, "Inspected %q (%s) as %s, read-only.\n", rep.ServerName, rep.ServerID, rep.BotName)
	if rel, err := filepath.Rel(dir, statePath); err == nil && !strings.HasPrefix(rel, "..") {
		statePath = rel
	}
	fmt.Fprintf(w, "Configuration written to %s: cicord.yaml, servers/%s.yaml, bots/… and %d other file(s);\nresource map in %s.\n\n",
		dir, rep.Key, max(len(rep.Files)-3, 0), statePath)

	switch {
	case rep.Administrator:
		fmt.Fprintln(w, "Bot permissions: Administrator (it sees and can change everything).")
	default:
		fmt.Fprintf(w, "Bot permissions: %s from its role, plus what @everyone allows (not Administrator).\n", orNone(rep.RolePermissions))
	}
	fmt.Fprintln(w)

	var visible, hidden []setup.ChannelAccess
	for _, c := range append(append([]setup.ChannelAccess{}, rep.Categories...), rep.Channels...) {
		if c.Visible {
			visible = append(visible, c)
		} else {
			hidden = append(hidden, c)
		}
	}
	fmt.Fprintf(w, "Visible to the bot: %s.\n", countChannels(visible))
	writeChannelTree(w, rep.Categories, visible)
	switch {
	case rep.Administrator:
		fmt.Fprintln(w, "Hidden from the bot: none (Administrator sees every channel).")
	default:
		if len(hidden) > 0 {
			fmt.Fprintf(w, "Hidden from the bot, listed by name: %s. The bot cannot open them or read their messages:\n", countChannels(hidden))
			writeChannelTree(w, rep.Categories, hidden)
		}
		if n := len(rep.Unlisted); n > 0 {
			fmt.Fprintf(w, "Hidden from the bot, not listed: %d channel(s) the server refers to:\n", n)
			for _, u := range rep.Unlisted {
				fmt.Fprintf(w, "  %s (%s)\n", u.ID, strings.Join(u.ReferencedBy, ", "))
			}
		}
		fmt.Fprintln(w, "Discord omits the channels a bot cannot view from the channel list: private channels the bot")
		fmt.Fprintln(w, "is not allowed in may be missing above and are not in the configuration.")
	}
	fmt.Fprintln(w)

	if len(rep.Unreadable) == 0 {
		fmt.Fprintln(w, "Not read: nothing, every part of the server was read.")
	} else {
		fmt.Fprintln(w, "Not read (left out of the configuration, so Cicord does not manage them):")
		for _, u := range rep.Unreadable {
			if u.Needs != "" {
				fmt.Fprintf(w, "  %s: needs %s\n", u.Name, u.Needs)
			} else {
				fmt.Fprintf(w, "  %s: %s\n", u.Name, u.Error)
			}
		}
		fmt.Fprintln(w, "  Grant the permission and inspect again to include them.")
	}
	fmt.Fprintln(w)

	if rep.Ready {
		fmt.Fprintln(w, "To apply changes: the bot already has every permission this configuration needs.")
	} else {
		fmt.Fprintln(w, "To apply changes, the bot would also need:")
		for _, n := range rep.Needs {
			fmt.Fprintf(w, "  %s: %s\n", n.Permission, n.Why)
			fmt.Fprintf(w, "    needed by %s\n", shortList(n.Sources, 3))
		}
		if !rep.Administrator {
			fmt.Fprintln(w, "  View Channel and Manage Channels in each private channel to manage: in Discord, allow them to")
			fmt.Fprintln(w, `    the bot's role in the channel's permissions, then add "@bot" to its overwrites in the YAML.`)
		}
		if rep.Hierarchy.Status != setup.StatusOK {
			fmt.Fprintf(w, "  Role order: %s\n    %s\n", rep.Hierarchy.Message, rep.Hierarchy.Hint)
		}
		if rep.UpgradeInviteURL != "" {
			fmt.Fprintf(w, "  Invite link with exactly these permissions: %s\n", rep.UpgradeInviteURL)
			fmt.Fprintf(w, "  Invite link with Administrator:            %s\n", rep.AdminInviteURL)
		}
	}
	if rep.Ready || rep.Hierarchy.Status == setup.StatusOK {
		fmt.Fprintf(w, "  Role order: %s\n", rep.Hierarchy.Message)
	}
	fmt.Fprintln(w)
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func countChannels(cs []setup.ChannelAccess) string {
	cats := 0
	for _, c := range cs {
		if c.Type == "category" {
			cats++
		}
	}
	return fmt.Sprintf("%d categor%s, %d channel(s)", cats, map[bool]string{true: "y", false: "ies"}[cats == 1], len(cs)-cats)
}

// writeChannelTree prints the categories and channels of cs, grouped by category.
func writeChannelTree(w io.Writer, cats, cs []setup.ChannelAccess) {
	label := func(c setup.ChannelAccess) string {
		s := c.Name
		switch c.Type {
		case "category":
		case "voice", "stage":
			s += " (" + c.Type + ")"
		case "text":
			s = "#" + s
		default:
			s = "#" + s + " (" + c.Type + ")"
		}
		if c.Private {
			s += " [private]"
		}
		return s
	}
	in := map[string][]string{} // category ID → channel labels
	shown := map[string]bool{}
	for _, c := range cs {
		if c.Type == "category" {
			shown[c.ID] = true
			continue
		}
		in[c.CategoryID] = append(in[c.CategoryID], label(c))
	}
	if top := in[""]; len(top) > 0 {
		fmt.Fprintf(w, "  (no category): %s\n", strings.Join(top, ", "))
	}
	for _, cat := range cats {
		chans := in[cat.ID]
		if !shown[cat.ID] && len(chans) == 0 {
			continue
		}
		head := label(cat)
		if !shown[cat.ID] {
			head = cat.Name + " (category hidden)"
			if cat.Visible {
				head = cat.Name + " (category visible)"
			}
		}
		if len(chans) == 0 {
			fmt.Fprintf(w, "  %s\n", head)
			continue
		}
		fmt.Fprintf(w, "  %s: %s\n", head, strings.Join(chans, ", "))
	}
}

func shortList(s []string, n int) string {
	if len(s) <= n {
		return strings.Join(s, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(s[:n], ", "), len(s)-n)
}
