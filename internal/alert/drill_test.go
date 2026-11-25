package alert

// The alert drill (https://cicord.dev/docs/self-hosting/monitoring/#alert-drill): on the
// Discord simulator, the tickets plugin of examples/showcase throws when a member submits
// the ticket form, while the HTTP API keeps answering /healthz. The webhook receives one
// alert naming the runtime, the server, the bot, the plugin instance and the failing
// handler, without the plugin's error text, then one resolved message once a fixed
// revision is active.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/api"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// drillThrow is inserted at the top of the ticket form's modal handler. Its message holds
// user input on purpose: it must never reach the alert.
const drillThrow = `
      throw new Error("drill: ticket creation failed for " + interaction.user.username + " (" + (interaction.fields["subject"] ?? "") + ")");`

func TestAlertDrillTicketCreationFails(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the showcase plugins")
	}
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Cicord Showcase", owner.ID)
	token, bot, appID := w.CreateBot("Cicord")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyDir(t, "../../examples/showcase", dir)
	replaceOnce(t, filepath.Join(dir, "servers/showcase.yaml"), `discordId: "223344556677889900"`, `discordId: "`+srv.ID+`"`)
	replaceOnce(t, filepath.Join(dir, "bots/cicord/bot.yaml"), `applicationId: "887766554433221100"`, `applicationId: "`+appID+`"`)
	ticketsSrc := filepath.Join(dir, "plugins/tickets/src/index.ts")
	original, err := os.ReadFile(ticketsSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ticketsSrc, []byte(breakTicketForm(t, string(original))), 0o644); err != nil {
		t.Fatal(err)
	}

	// The webhook receiver (a generic JSON webhook).
	var (
		mu       sync.Mutex
		received []Message
		payloads [][]byte
	)
	hook := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p struct{ Alerts []Message }
		_ = json.Unmarshal(b, &p)
		mu.Lock()
		received = append(received, p.Alerts...)
		payloads = append(payloads, b)
		mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()
	got := func() []Message {
		mu.Lock()
		defer mu.Unlock()
		return append([]Message(nil), received...)
	}
	sender, err := NewWebhook(hook.URL+"/alerts", hook.Client())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	n := New(Options{Sender: sender, PublicURL: "https://community.example.org", AdminURL: "https://cicord.tailnet.ts.net", Logger: logger,
		Poll: 100 * time.Millisecond, ResolveAfter: 500 * time.Millisecond, MinInterval: 50 * time.Millisecond})

	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rt := runtime.New(runtime.Options{
		Source: src, Connector: w, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(name string) (string, bool) { return token, name == "DISCORD_TOKEN_CICORD" },
		PortalURL: "http://localhost:7499", Logger: logger, ResyncInterval: -1, OnHandler: n.ObserveHandler,
	})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = rt.Run(ctx) }()
	go func() { defer wg.Done(); n.Run(ctx, rt) }()
	defer func() { cancel(); wg.Wait() }()
	httpSrv := httptest.NewServer(api.New(api.Options{Runtime: rt, Mode: "dev", Logger: logger}))
	defer httpSrv.Close()

	waitUntil(t, "first revision", func() bool { _, ok := rt.Revision(); return ok })
	for _, p := range rt.Status().Bots[0].Plugins {
		if p.Health != runtime.HealthHealthy {
			t.Fatalf("plugin %s: %s %s", p.Instance, p.Health, p.Message)
		}
	}

	// A member opens tickets: the form's handler throws every time.
	desk, ok := w.ChannelByName(srv.ID, "help-desk")
	if !ok {
		t.Fatal("no #help-desk")
	}
	alice := w.CreateUser("alice-drill")
	if err := w.AddMember(srv.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if st := w.InvokeCommand(owner.ID, srv.ID, desk.ID, appID, "ticket-panel", nil).Wait(); st.Failed {
		t.Fatalf("/ticket-panel: %+v", st)
	}
	openID, err := plugin.EncodeCustomID("tickets", "open")
	if err != nil {
		t.Fatal(err)
	}
	var panel discord.Message
	waitUntil(t, "ticket panel", func() bool {
		for _, m := range w.Messages(desk.ID) {
			for _, row := range m.Components {
				for _, c := range row.Components {
					if c.CustomID == openID {
						panel = m
						return true
					}
				}
			}
		}
		return false
	})
	for range 5 {
		click := w.ClickButton(alice.ID, panel, openID)
		if st := click.Wait(); st.Modal == nil {
			t.Fatalf("the open button must show the form: %+v", st)
		}
		click.SubmitModal(map[string]string{"subject": "private-subject-text", "description": "details"}).Wait()
	}
	if _, ok := w.ChannelByName(srv.ID, "ticket-1"); ok {
		t.Fatal("the drill expects ticket creation to fail")
	}
	waitUntil(t, "the alert", func() bool { return len(got()) >= 1 })

	// The HTTP process stays up: only the service is degraded.
	res, err := http.Get(httpSrv.URL + "/healthz")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("/healthz during the drill: %v %v", err, res)
	}
	res.Body.Close()

	time.Sleep(time.Second) // more failures, polls and changes: still one alert
	alerts := got()
	if len(alerts) != 1 {
		t.Fatalf("alerts = %v, want exactly one", states(alerts))
	}
	a := alerts[0]
	if a.State != StateFiring || a.Title != "Plugin handler failing" || a.Runtime != "community.example.org" ||
		a.Server != "showcase" || a.ServerName != "Cicord Showcase" || a.Bot != "cicord" || a.Instance != "tickets" ||
		!strings.HasPrefix(a.Plugin, "tickets@") || a.Workflow != "modal open" ||
		a.Link != "https://cicord.tailnet.ts.net/admin/servers/showcase/plugins" || !strings.Contains(a.Detail, "uncaught Error") {
		t.Errorf("alert = %+v", a)
	}
	mu.Lock()
	for _, p := range payloads {
		for _, leak := range []string{"alice-drill", "private-subject-text", "drill: ticket creation failed", token} {
			if strings.Contains(string(p), leak) {
				t.Errorf("the alert payload contains %q: %s", leak, p)
			}
		}
	}
	if testing.Verbose() {
		t.Logf("alert payload: %s", payloads[0])
	}
	mu.Unlock()

	// The fix: a revision without the throw.
	if err := os.WriteFile(ticketsSrc, original, 0o644); err != nil {
		t.Fatal(err)
	}
	rt.Trigger()
	waitUntil(t, "the resolved message", func() bool { return len(got()) >= 2 })
	time.Sleep(time.Second)
	alerts = got()
	if len(alerts) != 2 || alerts[1].State != StateResolved || alerts[1].Key != a.Key || alerts[1].Workflow != "modal open" {
		t.Fatalf("alerts = %v, want one firing then one resolved", states(alerts))
	}
	// Tickets work again.
	click := w.ClickButton(alice.ID, panel, openID)
	click.Wait()
	click.SubmitModal(map[string]string{"subject": "Works again"}).Wait()
	waitUntil(t, "ticket channel", func() bool { _, ok := w.ChannelByName(srv.ID, "ticket-1"); return ok })
}

// breakTicketForm inserts drillThrow at the top of modals.open of the tickets plugin.
func breakTicketForm(t *testing.T, src string) string {
	t.Helper()
	const anchor = "async open(ctx, interaction) {"
	i := strings.Index(src, "modals: {")
	if i < 0 {
		t.Fatal("tickets plugin: no modals section (update the drill)")
	}
	j := strings.Index(src[i:], anchor)
	if j < 0 || strings.Count(src[i:], anchor) != 1 {
		t.Fatal("tickets plugin: modals.open not found exactly once (update the drill)")
	}
	at := i + j + len(anchor)
	return src[:at] + drillThrow + src[at:]
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			if info.Name() == "node_modules" || info.Name() == ".cicord" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func replaceOnce(t *testing.T, file, old, repl string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), old) != 1 {
		t.Fatalf("%s: %q not found once", file, old)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
