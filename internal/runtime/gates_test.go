package runtime

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state/sqlite"
)

const ticketsStart = `ctx.log.info("tickets ready", { staffRoles: ctx.config.staffRoles.length });`

// A plugin that fails at startup fails the GitHub commit status of the revision, naming
// the plugin instance, its server and its bot; the bot and the instance are Degraded. A
// revision that fixes it goes green again.
func TestCommitStatusPluginStartFailure(t *testing.T) {
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Cicord Showcase", owner.ID)
	token, bot, appID := w.CreateBot("Cicord")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyTree(t, "../../examples/showcase", dir)
	replaceIn(t, filepath.Join(dir, "servers/showcase.yaml"), `discordId: "223344556677889900"`, `discordId: "`+srv.ID+`"`)
	replaceIn(t, filepath.Join(dir, "bots/cicord/bot.yaml"), `applicationId: "887766554433221100"`, `applicationId: "`+appID+`"`)
	index := filepath.Join(dir, "plugins/tickets/src/index.ts")
	replaceIn(t, index, ticketsStart, `throw new Error("tickets: database unreachable");`)
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	statuses := &statusLog{}
	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	rt := New(Options{
		Source: src, Connector: w, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { return token, n == "DISCORD_TOKEN_CICORD" },
		PortalURL: "http://localhost:7499", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ResyncInterval: -1,
		CommitStatus: statuses.report,
	})
	defer rt.shutdown()
	ctx := context.Background()
	gitRev := func(c string) source.Revision {
		sha := strings.Repeat(c, 40)
		return source.Revision{ID: sha[:7], SHA: sha, Source: "git", Message: "test", Dir: dir}
	}

	if err := rt.Activate(ctx, gitRev("a"), false); err != nil {
		t.Fatal(err)
	}
	got := statuses.take()
	if len(got) != 2 || got[1].state != CommitFailure ||
		!strings.HasPrefix(got[1].desc, "Plugin tickets on showcase (bot cicord) failed to start: ") ||
		!strings.Contains(got[1].desc, "database unreachable") || len([]rune(got[1].desc)) > maxCommitDescription {
		t.Fatalf("broken plugin: %+v", got)
	}
	t.Logf("GitHub status: %s %q", got[1].state, got[1].desc)
	b := rt.Status().Bots[0]
	if b.Health != HealthDegraded || b.Message != "plugin instance tickets on showcase is degraded" {
		t.Fatalf("bot = %s %q", b.Health, b.Message)
	}
	for _, p := range b.Plugins {
		if p.Instance == "tickets" && (p.Health != HealthDegraded || !strings.Contains(p.Message, "database unreachable")) {
			t.Fatalf("tickets = %+v", p)
		}
	}

	// The fix is a new revision: green again.
	replaceIn(t, index, `throw new Error("tickets: database unreachable");`, ticketsStart)
	if err := rt.Activate(ctx, gitRev("b"), false); err != nil {
		t.Fatal(err)
	}
	got = statuses.take()
	if len(got) != 2 || got[1].state != CommitSuccess || got[1].desc != "Applied to 1 server(s)" {
		t.Fatalf("fixed: %+v", got)
	}
	if b := rt.Status().Bots[0]; b.Health != HealthHealthy {
		t.Fatalf("bot after the fix = %s %q", b.Health, b.Message)
	}
}

func TestCommitVerdict(t *testing.T) {
	if st, d := commitVerdict(nil, 2); st != CommitSuccess || d != "Applied to 2 server(s)" {
		t.Errorf("no failure: %s %q", st, d)
	}
	if st, d := commitVerdict([]string{"Server a: boom", "Bot b is not connected: x"}, 2); st != CommitFailure || d != "2 problems; Server a: boom" {
		t.Errorf("two failures: %s %q", st, d)
	}
	long := strings.Repeat("é", 200)
	if got := truncateRunes(long, maxCommitDescription); len([]rune(got)) != maxCommitDescription || !strings.HasSuffix(got, "…") {
		t.Errorf("truncate: %d runes", len([]rune(got)))
	}
}

// A blocked change is named in the server message; one that only waits for a boost level
// is not blocked, and a paused server says why its changes are not applied.
func TestPlanStatusMessages(t *testing.T) {
	srv := &config.Server{}
	boost := reconcile.Blocked{Action: "update", Kind: "server", Key: "profile", Reason: "boost: the banner needs boost level 2"}
	del := reconcile.Blocked{Action: "delete", Kind: "channel", Key: "old", Reason: "deletionProtection is on"}
	plan := reconcile.Plan{Blocked: []reconcile.Blocked{boost, del, {Action: "delete", Kind: "role", Key: "x", Reason: "r"}}}
	if h, m := planStatus(srv, plan, 0, false, nil); h != HealthHealthy ||
		m != "2 change(s) blocked (delete channel old: deletionProtection is on and 1 more), 1 wait for a higher boost level" {
		t.Errorf("blocked: %s %q", h, m)
	}
	if _, m := planStatus(srv, reconcile.Plan{Blocked: []reconcile.Blocked{boost}}, 0, false, nil); m != "1 change(s) wait for a higher boost level" {
		t.Errorf("boost only: %q", m)
	}
	if _, m := planStatus(srv, reconcile.Plan{}, 2, true, nil); m != "2 change(s) to apply (enforcement is paused: nothing is applied until it is resumed)" {
		t.Errorf("paused: %q", m)
	}
}
