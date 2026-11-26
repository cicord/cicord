package runtime

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state/sqlite"
)

type env struct {
	t        *testing.T
	world    *sim.World
	rt       *Runtime
	dir      string
	serverID string
	appID    string
	bot      discord.User
	cancel   context.CancelFunc
	logs     *syncBuffer
}

func copyTree(t *testing.T, src, dst string) {
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

func replaceIn(t *testing.T, file, old, new string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s: %q not found", file, old)
	}
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(string(data), old, new)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// startExample deploys examples/basic on a simulated Discord.
func startExample(t *testing.T) *env {
	t.Helper()
	return startExampleWith(t, nil)
}

// startExampleWith is startExample with a hook run on the world before the runtime starts.
func startExampleWith(t *testing.T, prepare func(w *sim.World, appID string)) *env {
	t.Helper()
	return startExampleResync(t, prepare, nil, -1)
}

// startExampleResync is startExampleWith with an edit of the copied repository before the
// runtime starts and a periodic resync interval (negative: none).
func startExampleResync(t *testing.T, prepare func(w *sim.World, appID string), edit func(dir string), resync time.Duration) *env {
	t.Helper()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("My Community", owner.ID)
	token, bot, appID := w.CreateBot("Warden")
	if prepare != nil {
		prepare(w, appID)
	}
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	copyTree(t, "../../examples/basic", dir)
	replaceIn(t, filepath.Join(dir, "servers/my-community.yaml"), `discordId: "112233445566778899"`, `discordId: "`+srv.ID+`"`)
	replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), `applicationId: "998877665544332211"`, `applicationId: "`+appID+`"`)
	if edit != nil {
		edit(dir)
	}

	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	var out io.Writer = logs
	if testing.Verbose() {
		out = io.MultiWriter(logs, os.Stderr)
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rt := New(Options{
		PrivacyDir: t.TempDir(),
		Source:     src, Connector: w, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { return token, n == "DISCORD_TOKEN_MODERATOR" },
		PortalURL: "http://localhost:7420", Logger: logger, ResyncInterval: resync,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rt.Run(ctx); close(done) }()
	e := &env{t: t, world: w, rt: rt, dir: dir, serverID: srv.ID, appID: appID, bot: bot, cancel: cancel, logs: logs}
	t.Cleanup(func() {
		cancel()
		<-done
		src.Close()
		store.Close()
	})
	e.waitFor("first revision", func() bool { _, ok := rt.Revision(); return ok })
	return e
}

func (e *env) waitFor(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %s; status: %+v", what, e.rt.Status())
}

func TestExampleEndToEnd(t *testing.T) {
	e := startExample(t)
	w := e.world

	st := e.rt.Status()
	if len(st.Servers) != 1 || st.Servers[0].Health != HealthHealthy || st.Servers[0].Sync != SyncSynced {
		t.Fatalf("server status = %+v", st.Servers)
	}
	for _, p := range st.Bots[0].Plugins {
		if p.Health != HealthHealthy {
			t.Fatalf("plugin %s: %s %s", p.Instance, p.Health, p.Message)
		}
	}

	// The reconciler created the declared channels and roles.
	general, ok := w.ChannelByName(e.serverID, "general")
	if !ok {
		t.Fatalf("channel general not created: %+v", w.Channels(e.serverID))
	}
	memberRole, ok := w.RoleByName(e.serverID, "Member")
	if !ok {
		t.Fatal("role Member not created")
	}
	// Slash commands of both plugins were registered.
	names := map[string]bool{}
	for _, c := range w.Commands(e.appID, e.serverID) {
		names[c.Name] = true
	}
	if !names["welcome-preview"] || !names["onboarding"] {
		t.Fatalf("commands = %v", names)
	}

	// A member joins: welcome message, default role, DM with a portal link.
	alice := w.CreateUser("alice")
	if err := w.AddMember(e.serverID, alice.ID); err != nil {
		t.Fatal(err)
	}
	e.waitFor("welcome message", func() bool {
		for _, m := range w.Messages(general.ID) {
			if strings.Contains(m.Content, "<@"+alice.ID+">") && strings.HasPrefix(m.Content, "Hi ") {
				return true
			}
		}
		return false
	})
	e.waitFor("default role", func() bool {
		m, _ := w.Member(e.serverID, alice.ID)
		return m.HasRole(memberRole.ID)
	})
	var link string
	e.waitFor("onboarding DM", func() bool {
		for _, m := range w.DMs(alice.ID, e.bot.ID) {
			for _, row := range m.Components {
				for _, c := range row.Components {
					if c.Style == discord.ButtonLink {
						link = c.URL
						return true
					}
				}
			}
		}
		return false
	})
	if !strings.HasPrefix(link, "http://localhost:7420/onboarding?") || !strings.Contains(link, "cicord_token=") {
		t.Errorf("link = %s", link)
	}

	// Slash command with an ephemeral reply.
	h := w.InvokeCommand(alice.ID, e.serverID, general.ID, e.appID, "welcome-preview", nil)
	if err := h.Err(); err != nil {
		// welcome-preview requires ManageServer: alice cannot use it.
		t.Logf("expected permission error for a regular member: %v", err)
	}
	srvInfo, _ := w.Server(e.serverID)
	owner := srvInfo.OwnerID
	h = w.InvokeCommand(owner, e.serverID, general.ID, e.appID, "welcome-preview", nil)
	if err := h.Err(); err != nil {
		t.Fatal(err)
	}
	s := h.Wait()
	if s.Failed || s.Message == nil || !s.Ephemeral || !strings.Contains(s.Message.Content, "<@"+owner+">") {
		t.Fatalf("welcome-preview state = %+v", s)
	}

	// Portal route of the onboarding plugin.
	m, _ := w.Member(e.serverID, alice.ID)
	resp, route, err := e.rt.HandleRoute(context.Background(), "my-community", "moderator", "onboarding", "GET", "/steps",
		plugin.RouteRequest{Viewer: &plugin.RouteViewer{User: alice, Member: &m}})
	if err != nil {
		t.Fatal(err)
	}
	if route.Access != "member" || resp.Status != 200 && resp.Status != 0 {
		t.Fatalf("route = %+v resp = %d %s", route, resp.Status, resp.Body)
	}
	var steps map[string]any
	if err := json.Unmarshal(resp.Body, &steps); err != nil {
		t.Fatalf("body = %s", resp.Body)
	}
	t.Logf("GET /steps → %.200s", resp.Body)

	// Hot reload: edit the config; the new revision is activated without reconnecting.
	rev1, _ := e.rt.Revision()
	replaceIn(t, filepath.Join(e.dir, "bots/moderator/bot.yaml"), "Hi {member}", "Yo {member}")
	e.waitFor("new revision", func() bool { r, _ := e.rt.Revision(); return r.SHA != rev1.SHA })
	bob := w.CreateUser("bob")
	if err := w.AddMember(e.serverID, bob.ID); err != nil {
		t.Fatal(err)
	}
	e.waitFor("welcome message after reload", func() bool {
		for _, m := range w.Messages(general.ID) {
			if strings.HasPrefix(m.Content, "Yo <@"+bob.ID+">") {
				return true
			}
		}
		return false
	})
	if n := w.OpenSessions(); n != 1 {
		t.Errorf("open sessions after reload = %d, want 1 (no reconnect)", n)
	}

	// An invalid revision is rejected and the previous one keeps running.
	rev2, _ := e.rt.Revision()
	replaceIn(t, filepath.Join(e.dir, "bots/moderator/bot.yaml"), "channel: general", "channel: nowhere")
	e.waitFor("rejection", func() bool { return e.rt.Status().LastError != "" })
	if r, _ := e.rt.Revision(); r.SHA != rev2.SHA {
		t.Errorf("invalid revision was activated")
	}
}

// TestPrivilegedIntentsFromLiveFlags: the runtime checks the privileged intents against
// the live application flags. Enabled: no reminder is logged. Missing: the bot is not
// connected and its status names the intent and the developer portal page.
func TestPrivilegedIntentsFromLiveFlags(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		e := startExample(t)
		if logs := e.logs.String(); strings.Contains(logs, "privileged intent") {
			t.Errorf("a privileged intent line was logged although every intent is enabled:\n%s", logs)
		}
		if st := e.rt.Status(); st.Bots[0].Health != HealthHealthy {
			t.Errorf("bot status = %+v", st.Bots[0])
		}
		// The live check also reaches the validation API (no reminder).
		_, _, ds, err := e.rt.LoadAndBuild(e.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range ds {
			if d.Code == config.CodePrivilegedIntent {
				t.Errorf("unexpected diagnostic %s", d)
			}
		}
	})
	t.Run("missing", func(t *testing.T) {
		e := startExampleWith(t, func(w *sim.World, appID string) {
			if err := w.SetPrivilegedIntents(appID, discord.MustIntents("MessageContent")); err != nil {
				t.Fatal(err)
			}
		})
		bs := e.rt.Status().Bots[0]
		if bs.Health != HealthMissing || !strings.Contains(bs.Message, "ServerMembers") ||
			!strings.Contains(bs.Message, "https://discord.com/developers/applications/"+e.appID+"/bot") {
			t.Fatalf("bot status = %+v", bs)
		}
		if logs := e.logs.String(); !strings.Contains(logs, "level=ERROR") || !strings.Contains(logs, "bot privileged intents not enabled") {
			t.Errorf("no error logged:\n%s", logs)
		}
		_, _, ds, err := e.rt.LoadAndBuild(e.dir)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range ds {
			if d.Code == config.CodePrivilegedIntent {
				found = true
				if !strings.Contains(d.Message, "not enabled for the application") || strings.Contains(d.Message, "make sure") {
					t.Errorf("diagnostic = %s", d)
				}
			}
		}
		if !found {
			t.Errorf("no privileged intent diagnostic: %v", ds)
		}
	})
}
