package runtime

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state/sqlite"
)

type statusEvent struct{ sha, state, desc string }

type statusLog struct {
	mu     sync.Mutex
	events []statusEvent
}

func (l *statusLog) report(_ context.Context, sha, state, desc string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, statusEvent{sha, state, desc})
}

func (l *statusLog) take() []statusEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.events
	l.events = nil
	return out
}

func TestCommitStatuses(t *testing.T) {
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("My Community", owner.ID)
	token, bot, appID := w.CreateBot("Warden")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyTree(t, "../../examples/basic", dir)
	replaceIn(t, filepath.Join(dir, "servers/my-community.yaml"), `discordId: "112233445566778899"`, `discordId: "`+srv.ID+`"`)
	replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), `applicationId: "998877665544332211"`, `applicationId: "`+appID+`"`)
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	statuses := &statusLog{}
	rt := New(Options{
		Source: src, Connector: w, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { return token, n == "DISCORD_TOKEN_MODERATOR" },
		PortalURL: "http://localhost:7420", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ResyncInterval: -1,
		CommitStatus: statuses.report,
	})
	defer rt.shutdown()
	ctx := context.Background()
	sha := func(c string) string { return strings.Repeat(c, 40) }
	gitRev := func(c string) source.Revision {
		return source.Revision{ID: sha(c)[:7], SHA: sha(c), Source: "git", Message: "test", Dir: dir}
	}

	// Applied to every server: pending, then success.
	if err := rt.Activate(ctx, gitRev("a"), false); err != nil {
		t.Fatal(err)
	}
	got := statuses.take()
	if len(got) != 2 || got[0].state != CommitPending || got[1].state != CommitSuccess || got[1].sha != sha("a") ||
		got[1].desc != "Applied to 1 server(s)" {
		t.Fatalf("applied: %+v", got)
	}
	// The same revision again is a no-op (no new status).
	if err := rt.Activate(ctx, gitRev("a"), false); err != nil || len(statuses.take()) != 0 {
		t.Fatal("re-activation of the active revision posted a status")
	}

	// Rejected: pending, then failure with the reason.
	replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), "channel: general", "channel: nowhere")
	if err := rt.Activate(ctx, gitRev("b"), false); err == nil {
		t.Fatal("invalid revision accepted")
	}
	got = statuses.take()
	if len(got) != 2 || got[0].state != CommitPending || got[1].state != CommitFailure || !strings.HasPrefix(got[1].desc, "Rejected: ") {
		t.Fatalf("rejected: %+v", got)
	}
	replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), "channel: nowhere", "channel: general")

	// A server the bot cannot reach: failure naming the server.
	replaceIn(t, filepath.Join(dir, "servers/my-community.yaml"), `discordId: "`+srv.ID+`"`, `discordId: "112233445566778800"`)
	if err := rt.Activate(ctx, gitRev("c"), false); err != nil {
		t.Fatal(err)
	}
	got = statuses.take()
	if len(got) != 2 || got[1].state != CommitFailure || !strings.HasPrefix(got[1].desc, "Server my-community: ") {
		t.Fatalf("server failure: %+v", got)
	}

	// Revisions that are not git commits get no status.
	if err := rt.Activate(ctx, source.Revision{ID: "local-1", SHA: sha("d"), Source: "local", Dir: dir}, false); err != nil {
		t.Fatal(err)
	}
	if err := rt.Activate(ctx, source.Revision{ID: "short", SHA: "abc", Source: "git", Dir: dir}, false); err != nil {
		t.Fatal(err)
	}
	if got := statuses.take(); len(got) != 0 {
		t.Fatalf("non-commit revisions: %+v", got)
	}
}
