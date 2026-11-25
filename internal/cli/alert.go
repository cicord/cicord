package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/alert"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/runtime"
)

func init() {
	extraCommands = append(extraCommands, newAlertCmd)
}

// alertWebhookSecret is the alert webhook URL: the environment, else the secret store.
const alertWebhookSecret = "CICORD_ALERT_WEBHOOK" //nolint:gosec // G101: the name of a secret, not its value

// alertWebhookFlagHelp is the --alert-webhook help. No default read from the environment:
// --help would print the URL, which is a secret.
const alertWebhookFlagHelp = "webhook URL of the built-in alerts (Discord webhook or JSON endpoint; default: $" + alertWebhookSecret +
	", else the secret store)"

// newAlertNotifier returns the notifier of a runtime, or nil when no alert webhook is
// configured (--alert-webhook, then CICORD_ALERT_WEBHOOK from the environment or the
// secret store). An invalid URL is logged and disables the alerts.
func newAlertNotifier(log *slog.Logger, flag string, resolve func(string) (string, bool), public, console string) *alert.Notifier {
	raw := strings.TrimSpace(flag)
	if raw == "" && resolve != nil {
		raw, _ = resolve(alertWebhookSecret)
	}
	if raw == "" {
		log.Info("alerts: no " + alertWebhookSecret + " (see https://cicord.dev/docs/self-hosting/monitoring/#alerts)")
		return nil
	}
	w, err := alert.NewWebhook(raw, nil)
	if err != nil {
		log.Error("alerts disabled", "err", err)
		return nil
	}
	log.Info("alerts enabled", "target", w.Describe())
	return alert.New(alert.Options{Sender: w, PublicURL: public, AdminURL: console, Logger: log.With("component", "alerts")})
}

// onHandler is the runtime.Options.OnHandler of n (nil without alerts).
func onHandler(n *alert.Notifier) func(in config.Instance, res plugin.HandlerResult) {
	if n == nil {
		return nil
	}
	return n.ObserveHandler
}

// runAlerts runs n on rt until stopping is closed (the runtime's shutdown starts: its
// last status changes are not alerts).
func runAlerts(n *alert.Notifier, rt *runtime.Runtime, stopping <-chan struct{}) {
	if n == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-stopping
		cancel()
	}()
	go n.Run(ctx, rt)
}

// ───────────────────────────────────────────────────────────── cicord alert

func newAlertCmd() *cobra.Command {
	var dataDir, dbFlag, public, admin string
	cmd := &cobra.Command{
		Use:   "alert",
		Short: "Send a test alert, or an alert from a script, to the runtime's alert webhook",
		Long: `Send alerts to the webhook of the built-in alerts of ` + "`cicord serve`" + `: CICORD_ALERT_WEBHOOK from the
environment, else from the secret store of --data-dir (cicord secret set CICORD_ALERT_WEBHOOK).
A Discord webhook URL (https://discord.com/api/webhooks/…) gets embeds; any other https URL a
JSON document. Exit 0 when the alert was sent, 1 when the webhook refused it or could not be
reached, 2 when no webhook is configured.

With Docker, run it inside the container (it has the data volume and CICORD_PUBLIC_URL):

  docker exec cicord cicord alert test --data-dir /data`,
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", ".cicord-data", "runtime state directory (its secret store holds "+alertWebhookSecret+")")
	cmd.PersistentFlags().StringVar(&dbFlag, "database-url", "", databaseURLFlagHelp)
	cmd.PersistentFlags().StringVar(&public, "public-url", os.Getenv("CICORD_PUBLIC_URL"), "public URL of the runtime: its host names the runtime in the alert (env CICORD_PUBLIC_URL)")
	cmd.PersistentFlags().StringVar(&admin, "admin-url", os.Getenv("CICORD_ADMIN_URL"), "URL of the admin console, for the alert's link (env CICORD_ADMIN_URL; default: --public-url)")

	send := func(c *cobra.Command, m alert.Message) error {
		w, err := alertWebhook(c.Context(), dataDir, dbFlag)
		if err != nil {
			fmt.Fprintln(c.ErrOrStderr(), err)
			return exitError{code: 2}
		}
		m.Runtime, m.Time = alert.RuntimeName(public), time.Now().UTC()
		if m.Since.IsZero() {
			m.Since = m.Time
		}
		if m.Link == "" {
			if base := strings.TrimRight(consoleURL(admin, public), "/"); base != "" {
				m.Link = base + "/admin/"
			}
		}
		ctx, cancel := context.WithTimeout(c.Context(), time.Minute)
		defer cancel()
		if err := w.Send(ctx, []alert.Message{m}); err != nil {
			fmt.Fprintln(c.ErrOrStderr(), "alert not sent:", err)
			return exitError{code: 1}
		}
		fmt.Fprintf(c.OutOrStdout(), "Alert sent to the %s.\n", w.Describe())
		return nil
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "test",
		Short: "Send a test alert (an alert drill: check that it reaches the right people)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return send(c, alert.Message{Key: "test", State: alert.StateTest, Severity: alert.SeverityInfo, Title: "Test alert",
				Detail: "Sent by `cicord alert test`. Alerts of this runtime reach this channel: bots or plugin instances that fail, " +
					"server syncs that fail, rejected revisions, paused servers, failed backups and rolled back updates."})
		},
	})

	var m alert.Message
	sendCmd := &cobra.Command{
		Use:   "send",
		Short: "Send an alert (from a script: backups, updates)",
		Example: `  docker exec cicord cicord alert send --data-dir /data --severity critical \
    --title "Backup failed" --detail "cicord.db is missing" --workflow backup`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			switch m.Severity {
			case alert.SeverityCritical, alert.SeverityWarning, alert.SeverityInfo:
			default:
				return fmt.Errorf("--severity must be critical, warning or info, not %q", m.Severity)
			}
			switch m.State {
			case alert.StateFiring, alert.StateResolved:
			default:
				return fmt.Errorf("--state must be firing or resolved, not %q", m.State)
			}
			if strings.TrimSpace(m.Title) == "" {
				return errors.New("--title is required")
			}
			if m.Key == "" {
				m.Key = "script/" + m.Workflow
			}
			return send(c, m)
		},
	}
	f := sendCmd.Flags()
	f.StringVar(&m.Title, "title", "", "title of the alert (required)")
	f.StringVar(&m.Detail, "detail", "", "what happened and what to do (never a secret)")
	f.StringVar(&m.Severity, "severity", alert.SeverityCritical, "critical | warning | info")
	f.StringVar(&m.State, "state", alert.StateFiring, "firing | resolved")
	f.StringVar(&m.Workflow, "workflow", "", "what was running, e.g. backup or update")
	f.StringVar(&m.Server, "server", "", "server key, when the alert is about one server")
	f.StringVar(&m.Key, "key", "", "identifier shared by an alert and its resolution (default script/<workflow>)")
	cmd.AddCommand(sendCmd)
	return cmd
}

// alertWebhook resolves the alert webhook: CICORD_ALERT_WEBHOOK, else the secret store of
// dataDir (when it has a runtime state).
func alertWebhook(ctx context.Context, dataDir, dbFlag string) (*alert.Webhook, error) {
	raw := strings.TrimSpace(os.Getenv(alertWebhookSecret))
	if raw == "" {
		s, done, err := openDataDirSecrets(ctx, dataDir, dbFlag)
		if err != nil {
			return nil, fmt.Errorf("no alert webhook: %s is not set and the secret store cannot be read: %w", alertWebhookSecret, err)
		}
		defer done()
		if raw, err = s.Get(ctx, alertWebhookSecret); err != nil || raw == "" {
			return nil, fmt.Errorf("no alert webhook: set %s, or store it with `cicord secret set %s`", alertWebhookSecret, alertWebhookSecret)
		}
	}
	return alert.NewWebhook(raw, nil)
}
