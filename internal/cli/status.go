package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/planfmt"
	"github.com/cicord/cicord/internal/runtime"
)

func init() {
	extraCommands = append(extraCommands, newStatusCmd, newSyncCmd)
}

type waitFlags struct {
	wait     bool
	revision string
	timeout  time.Duration
	interval time.Duration
}

func (w *waitFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolVar(&w.wait, "wait", false, "wait until the revision is active, every server Synced and Healthy, every bot and plugin Healthy")
	f.StringVar(&w.revision, "revision", "", "revision to wait for: a git SHA or a prefix of it (with --wait)")
	f.DurationVar(&w.timeout, "timeout", 5*time.Minute, "maximum time to wait (with --wait)")
	f.DurationVar(&w.interval, "interval", 0, "polling interval (with --wait; default 4s)")
}

func newStatusCmd() *cobra.Command {
	var rf remoteFlags
	var wf waitFlags
	var output string
	cmd := &cobra.Command{
		Use:   "status [dir]",
		Short: "Show the runtime's revision and the sync/health of servers and bots",
		Long: `Show the status of a Cicord runtime (GET /api/v1/status): the active revision, every
server (sync, health, drift) and every bot with its plugin instances.

The runtime URL is --remote=URL, else CICORD_RUNTIME_URL (https, or http on localhost);
the bearer token is CICORD_RUNTIME_TOKEN. cicord.yaml spec.runtime.url is never used to send
the token.

--wait polls until --revision (a SHA or a prefix; default: the active one) is active,
every server is Synced and Healthy and every bot and plugin instance is Healthy. It fails
at once (exit 1) when the revision is rejected, a server, bot or plugin instance is
Degraded or Missing (a plugin that failed to start, or crashes and restarts), or a server
is paused (cicord pause: nothing is applied to it); what failed is printed. It also fails
when the timeout expires. A server with blocked operations (deletionProtection,
hierarchy…) stays OutOfSync, so --wait times out on it: the blocked changes and the drift
are printed to explain why. Changes that only wait for a higher boost level are not
drift: they keep the server Synced.`,
		Example: `  cicord status
  cicord status --remote=https://cicord.example.com -o json
  cicord status --wait --revision $GITHUB_SHA --timeout 10m`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			args = rf.splitRemoteArgs(args)
			if len(args) > 1 {
				return fmt.Errorf("too many arguments: %v", args)
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			dir, err := absDir(args)
			if err != nil {
				return err
			}
			base, err := rf.resolveURL(dir)
			if err != nil {
				return err
			}
			c, err := newAPIClient(base)
			if err != nil {
				return err
			}
			if wf.wait {
				return waitStatus(cmd, c, wf, output)
			}
			if wf.revision != "" {
				return errors.New("--revision requires --wait")
			}
			st, err := getStatus(cmd.Context(), c)
			if err != nil {
				return err
			}
			if output == "json" {
				return writeJSONOut(cmd.OutOrStdout(), st)
			}
			printStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
	rf.register(cmd, true)
	wf.register(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text | json")
	return cmd
}

func newSyncCmd() *cobra.Command {
	var rf remoteFlags
	var wf waitFlags
	var output string
	cmd := &cobra.Command{
		Use:   "sync [dir]",
		Short: "Ask the runtime to fetch its source and sync now",
		Long: `Trigger the runtime (POST /api/v1/sync): it fetches its source (git, embedded repository
or local directory) and activates the new revision, if any. Use it after a push when no
webhook is configured.

With --wait, the command then waits like ` + "`cicord status --wait`" + `. Without --revision it
waits for the active revision to be Synced and Healthy, which may still be the previous
one if the fetch has not completed yet: pass --revision for an exact wait.`,
		Example: `  cicord sync
  cicord sync --wait --revision $(git rev-parse HEAD)`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			args = rf.splitRemoteArgs(args)
			if len(args) > 1 {
				return fmt.Errorf("too many arguments: %v", args)
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			if wf.revision != "" && !wf.wait {
				return errors.New("--revision requires --wait")
			}
			dir, err := absDir(args)
			if err != nil {
				return err
			}
			base, err := rf.resolveURL(dir)
			if err != nil {
				return err
			}
			c, err := newAPIClient(base)
			if err != nil {
				return err
			}
			var out struct {
				Status string `json:"status"`
			}
			if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/sync", nil, "", &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Sync requested on %s (%s)\n", base, out.Status)
			if !wf.wait {
				if output == "json" {
					return writeJSONOut(cmd.OutOrStdout(), out)
				}
				return nil
			}
			return waitStatus(cmd, c, wf, output)
		},
	}
	rf.register(cmd, true)
	wf.register(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format of the final status: text | json")
	return cmd
}

func getStatus(ctx context.Context, c *apiClient) (runtime.Status, error) {
	var st runtime.Status
	err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, "", &st)
	return st, err
}

type syncRecord struct {
	ID       int64           `json:"id"`
	Time     time.Time       `json:"time"`
	Revision string          `json:"revision"`
	Target   string          `json:"target"`
	Status   string          `json:"status"`
	Message  string          `json:"message"`
	Details  json.RawMessage `json:"details,omitempty"`
}

func getHistory(ctx context.Context, c *apiClient, target string, limit int) ([]syncRecord, error) {
	var out struct {
		Items []syncRecord `json:"items"`
	}
	p := fmt.Sprintf("/api/v1/history?limit=%d", limit)
	if target != "" {
		p += "&target=" + target
	}
	err := c.do(ctx, http.MethodGet, p, nil, "", &out)
	return out.Items, err
}

// revisionMatches reports whether want (a SHA or a prefix) designates the revision
// with short id / full sha.
func revisionMatches(want, id, sha string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return true
	}
	if sha != "" && strings.HasPrefix(strings.ToLower(sha), want) {
		return true
	}
	if id == "" {
		return false
	}
	id = strings.ToLower(id)
	return strings.HasPrefix(id, want) || (len(id) >= 7 && strings.HasPrefix(want, id))
}

// waitStatus polls the status until the condition of --wait holds.
func waitStatus(cmd *cobra.Command, c *apiClient, wf waitFlags, output string) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	interval := wf.interval
	if interval <= 0 {
		interval = pollInterval
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), wf.timeout)
	defer cancel()
	want := wf.revision
	label := "the active revision"
	if want != "" {
		label = "revision " + want
	}
	fmt.Fprintf(stderr, "Waiting for %s to be active, Synced and Healthy (timeout %s)…\n", label, wf.timeout)

	var (
		last     runtime.Status
		haveLast bool
		lastErr  error
		lastNote string
	)
	note := func(s string) {
		if s != lastNote {
			fmt.Fprintln(stderr, "  "+s)
			lastNote = s
		}
	}
	final := func(st runtime.Status) error {
		if output == "json" {
			return writeJSONOut(stdout, st)
		}
		printStatus(stdout, st)
		return nil
	}
	for {
		st, err := getStatus(ctx, c)
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
				return err
			}
			lastErr = err
			if ctx.Err() == nil {
				note(fmt.Sprintf("runtime not reachable yet: %v", err))
			}
		} else {
			last, haveLast, lastErr = st, true, nil
			// rejected revision?
			if want != "" {
				if hist, herr := getHistory(ctx, c, "revision", 50); herr == nil {
					for _, h := range hist {
						if revisionMatches(want, h.Revision, "") {
							if h.Status == "rejected" {
								fmt.Fprintf(stderr, "Revision %s was rejected: %s\n", h.Revision, h.Message)
								printDetails(stderr, h.Details)
								return exitError{1}
							}
						}
					}
				}
			}
			rev := st.Revision
			switch {
			case rev == nil:
				note("no active revision yet")
			case want != "" && !revisionMatches(want, rev.ID, rev.SHA):
				if superseded(ctx, c, want) {
					fmt.Fprintf(stderr, "Revision %s was activated and has since been superseded by %s.\n", want, rev.ID)
					return checkServers(ctx, stderr, c, st, final)
				}
				note(fmt.Sprintf("active revision is %s, waiting for %s", rev.ID, want))
			default:
				if hasFailures(st) {
					fmt.Fprintf(stderr, "Revision %s is active but not healthy:\n", rev.ID)
					printFailures(ctx, stderr, c, st)
					_ = final(st)
					return exitError{1}
				}
				if ready(st) {
					fmt.Fprintf(stderr, "Revision %s is active and every server is Synced and Healthy.\n", rev.ID)
					return final(st)
				}
				note(fmt.Sprintf("revision %s is active, waiting for servers: %s", rev.ID, pendingServers(st)))
			}
		}
		select {
		case <-ctx.Done():
			if cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}
			fmt.Fprintf(stderr, "Timed out after %s waiting for %s.\n", wf.timeout, label)
			if lastErr != nil {
				fmt.Fprintf(stderr, "Last error: %v\n", lastErr)
			}
			if haveLast {
				if last.LastError != "" {
					fmt.Fprintf(stderr, "Runtime last error: %s\n", last.LastError)
				}
				for _, s := range last.Servers {
					if strings.Contains(s.Message, "blocked") && s.Sync != runtime.SyncSynced {
						fmt.Fprintf(stderr, "Server %s stays OutOfSync: %s. Blocked changes need a fix in git or on Discord.\n", s.Key, s.Message)
					}
				}
				_ = final(last)
			}
			return exitError{1}
		case <-time.After(interval):
		}
	}
}

// superseded reports whether want was activated successfully (it is in the history)
// even though another revision is active now.
func superseded(ctx context.Context, c *apiClient, want string) bool {
	hist, err := getHistory(ctx, c, "revision", 100)
	if err != nil {
		return false
	}
	for _, h := range hist {
		if h.Status == "succeeded" && (revisionMatches(want, h.Revision, "")) {
			return true
		}
	}
	return false
}

func checkServers(ctx context.Context, stderr io.Writer, c *apiClient, st runtime.Status, final func(runtime.Status) error) error {
	if hasFailures(st) {
		printFailures(ctx, stderr, c, st)
		_ = final(st)
		return exitError{1}
	}
	return final(st)
}

// broken reports a health that fails --wait at once.
func broken(health string) bool {
	return health == runtime.HealthDegraded || health == runtime.HealthMissing
}

// hasFailures reports what fails --wait at once: a Degraded or Missing server, bot or
// plugin instance, or a paused server (its revision is not applied).
func hasFailures(st runtime.Status) bool {
	for _, s := range st.Servers {
		if broken(s.Health) || s.Paused != nil {
			return true
		}
	}
	for _, b := range st.Bots {
		if broken(b.Health) {
			return true
		}
		for _, p := range b.Plugins {
			if broken(p.Health) {
				return true
			}
		}
	}
	return false
}

// printFailures prints what hasFailures found, naming each server, bot and plugin
// instance, with the last sync of degraded servers.
func printFailures(ctx context.Context, stderr io.Writer, c *apiClient, st runtime.Status) {
	for _, s := range st.Servers {
		if s.Paused != nil {
			fmt.Fprintf(stderr, "  %s: %s\n", s.Key, pausedLine(s))
		}
		if !broken(s.Health) {
			continue
		}
		fmt.Fprintf(stderr, "  %s: %s %s: %s\n", s.Key, s.Sync, s.Health, s.Message)
		if hist, herr := getHistory(ctx, c, "server:"+s.Key, 1); herr == nil && len(hist) > 0 {
			h := hist[0]
			fmt.Fprintf(stderr, "    last sync (%s, revision %s): %s\n", h.Status, h.Revision, h.Message)
		}
	}
	for _, b := range st.Bots {
		pluginFailed := false
		for _, p := range b.Plugins {
			if broken(p.Health) {
				pluginFailed = true
				fmt.Fprintf(stderr, "  plugin %s (bot %s) on %s: %s: %s\n", p.Instance, b.Key, p.Server, p.Health, firstLineOf(p.Message))
			}
		}
		// a bot Degraded because of its plugins is already explained
		if broken(b.Health) && (!pluginFailed || b.Health != runtime.HealthDegraded) {
			fmt.Fprintf(stderr, "  bot %s: %s: %s\n", b.Key, b.Health, firstLineOf(b.Message))
		}
	}
}

// unconfirmedLine describes the CORE-04 alert of a server. It is advisory: it never
// fails --wait (a false alarm must not block or roll back an update).
func unconfirmedLine(u *runtime.UnconfirmedAudit) string {
	return fmt.Sprintf("WARNING: %d audited action(s) by bot %s that Cicord did not emit since %s (last: %s); its token may be compromised",
		u.Count, u.Bot, u.First.Local().Format(time.RFC3339), u.LastAction)
}

// pausedLine describes the pause of a server.
func pausedLine(s runtime.ServerStatus) string {
	p := s.Paused
	line := fmt.Sprintf("enforcement paused by %s since %s", p.Who(), p.At.Local().Format(time.RFC3339))
	if p.Reason != "" {
		line += fmt.Sprintf(" (%s)", p.Reason)
	}
	return line + fmt.Sprintf(": nothing is applied, %d drift; resume with `cicord resume %s`", len(s.Drift), s.Key)
}

func ready(st runtime.Status) bool {
	for _, s := range st.Servers {
		if s.Sync != runtime.SyncSynced || s.Health != runtime.HealthHealthy || s.Paused != nil {
			return false
		}
	}
	for _, b := range st.Bots {
		if b.Health != runtime.HealthHealthy {
			return false
		}
		for _, p := range b.Plugins {
			if !pluginReady(p.Health) {
				return false
			}
		}
	}
	return true
}

// pluginReady reports whether a plugin instance health lets --wait succeed. An instance
// disabled by configuration (enabled: false) is a declared, reviewed state, not a
// pending one: waiting on it would never end.
func pluginReady(health string) bool {
	return health == runtime.HealthHealthy || health == runtime.HealthDisabled
}

func pendingServers(st runtime.Status) string {
	var parts []string
	for _, b := range st.Bots {
		if b.Health != runtime.HealthHealthy {
			parts = append(parts, fmt.Sprintf("bot %s (%s)", b.Key, b.Health))
		}
		for _, p := range b.Plugins {
			if !pluginReady(p.Health) {
				parts = append(parts, fmt.Sprintf("plugin %s on %s (%s)", p.Instance, p.Server, p.Health))
			}
		}
	}
	for _, s := range st.Servers {
		if s.Sync != runtime.SyncSynced || s.Health != runtime.HealthHealthy {
			p := fmt.Sprintf("%s (%s, %s", s.Key, s.Sync, s.Health)
			if s.Message != "" {
				p += ": " + s.Message
			}
			if n := len(s.Drift); n > 0 {
				p += fmt.Sprintf(", %d drift", n)
			}
			parts = append(parts, p+")")
		}
	}
	return strings.Join(parts, ", ")
}

func printDetails(w io.Writer, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	// rejected revisions carry their diagnostics
	var diags []struct {
		Severity, File, Path, Message string
		Line                          int
	}
	if json.Unmarshal(raw, &diags) == nil && len(diags) > 0 {
		for _, d := range diags {
			loc := d.File
			if d.Line > 0 {
				loc = fmt.Sprintf("%s:%d", d.File, d.Line)
			}
			if d.Path != "" {
				fmt.Fprintf(w, "  %s: %s: %s: %s\n", loc, d.Severity, d.Path, d.Message)
			} else {
				fmt.Fprintf(w, "  %s: %s: %s\n", loc, d.Severity, d.Message)
			}
		}
		return
	}
	fmt.Fprintf(w, "  details: %s\n", raw)
}

func printStatus(w io.Writer, st runtime.Status) {
	mode := st.Mode
	if st.Simulator {
		mode += ", simulator"
	}
	fmt.Fprintf(w, "Runtime %s (%s)\n", st.Version, mode)
	if r := st.Revision; r != nil {
		msg := firstLineOf(r.Message)
		fmt.Fprintf(w, "Revision %s", r.ID)
		if msg != "" {
			fmt.Fprintf(w, "  %q", msg)
		}
		if r.Author != "" {
			fmt.Fprintf(w, " by %s", r.Author)
		}
		if !r.Time.IsZero() {
			fmt.Fprintf(w, ", %s", r.Time.Format(time.RFC3339))
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintln(w, "Revision: none active yet")
	}
	if st.LastError != "" {
		fmt.Fprintf(w, "Last error: %s\n", firstLineOf(st.LastError))
	}
	fmt.Fprintf(w, "\nServers (%d)\n", len(st.Servers))
	for _, s := range st.Servers {
		line := fmt.Sprintf("  %-20s %-10s %-11s", s.Key, s.Sync, s.Health)
		if s.LastSync != nil {
			line += " synced " + s.LastSync.Format(time.RFC3339)
		}
		if s.Bot != "" {
			line += " via " + s.Bot
		}
		if s.Paused != nil {
			line += " PAUSED"
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		if s.Paused != nil {
			fmt.Fprintf(w, "    %s\n", pausedLine(s))
		}
		if u := s.UnconfirmedAudit; u != nil {
			fmt.Fprintf(w, "    %s\n", unconfirmedLine(u))
		}
		if s.Message != "" {
			fmt.Fprintf(w, "    %s\n", s.Message)
		}
		for _, d := range s.Drift {
			fmt.Fprintf(w, "    drift: %s %s %s: expected %s, actual %s\n", d.Kind, d.Key, d.Field, planfmt.Value(d.Expected), planfmt.Value(d.Actual))
		}
	}
	fmt.Fprintf(w, "\nBots (%d)\n", len(st.Bots))
	for _, b := range st.Bots {
		who := b.Username
		if b.UserID != "" {
			who += " (" + b.UserID + ")"
		}
		line := fmt.Sprintf("  %-20s %-11s %s", b.Key, b.Health, who)
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		if b.Message != "" {
			fmt.Fprintf(w, "    %s\n", b.Message)
		}
		if len(b.Servers) > 0 {
			fmt.Fprintf(w, "    servers: %s\n", strings.Join(b.Servers, ", "))
		}
		plugins := append([]runtime.PluginStatus(nil), b.Plugins...)
		sort.SliceStable(plugins, func(i, j int) bool {
			if plugins[i].Instance != plugins[j].Instance {
				return plugins[i].Instance < plugins[j].Instance
			}
			return plugins[i].Server < plugins[j].Server
		})
		for _, p := range plugins {
			pl := fmt.Sprintf("    plugin %s (%s@%s) on %s: %s", p.Instance, p.Plugin, p.Version, p.Server, p.Health)
			if p.Message != "" {
				pl += ": " + firstLineOf(p.Message) // the JSON output has the stack trace
			}
			fmt.Fprintln(w, pl)
		}
	}
}
