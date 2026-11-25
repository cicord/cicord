package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// reconcileWrites are the simulator methods that change a server's configuration.
var reconcileWrites = []string{
	"CreateChannel", "UpdateChannel", "DeleteChannel", "SetChannelPositions", "CreateRole", "UpdateRole",
	"DeleteRole", "SetRolePositions", "UpdateServer", "UpdateMember", "UpdateWelcomeScreen", "UpdateOnboarding",
	"CreateAutoModRule", "UpdateAutoModRule", "DeleteAutoModRule",
}

// drill is examples/basic on the simulator, with #general declared open to @everyone, so
// that a moderator who locks it during a raid creates drift.
type drill struct {
	t        *testing.T
	w        *sim.World
	dir      string
	store    *sqlite.Store
	token    string
	bot      discord.User
	serverID string
	statuses *statusLog
	rt       *Runtime
	stop     func()
}

func newDrill(t *testing.T, selfHeal bool) *drill {
	t.Helper()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("My Community", owner.ID)
	token, bot, appID := w.CreateBot("Warden")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyTree(t, "../../examples/basic", dir)
	file := filepath.Join(dir, "servers/my-community.yaml")
	replaceIn(t, file, `discordId: "112233445566778899"`, `discordId: "`+srv.ID+`"`)
	replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), `applicationId: "998877665544332211"`, `applicationId: "`+appID+`"`)
	replaceIn(t, file, "    selfHeal: false\n", fmt.Sprintf("    selfHeal: %v\n", selfHeal))
	replaceIn(t, file, "      topic: General chat\n",
		"      topic: General chat\n      overwrites:\n        \"@everyone\":\n          allow: [SendMessages]\n")
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	d := &drill{t: t, w: w, dir: dir, store: store, token: token, bot: bot, serverID: srv.ID, statuses: &statusLog{}}
	d.start()
	return d
}

// start runs a runtime on the drill's world and store (a restart keeps the store).
func (d *drill) start() {
	t := d.t
	t.Helper()
	src, err := source.NewLocal(d.dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	if testing.Verbose() {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	d.rt = New(Options{
		Source: src, Connector: d.w, Store: d.store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { return d.token, n == "DISCORD_TOKEN_MODERATOR" },
		PortalURL: "http://localhost:7420", Logger: logger, ResyncInterval: -1,
		CommitStatus: d.statuses.report,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = d.rt.Run(ctx); close(done) }()
	var once bool
	d.stop = func() {
		if !once {
			once = true
			cancel()
			<-done
			src.Close()
		}
	}
	t.Cleanup(d.stop)
	d.waitFor("first revision", func() bool { _, ok := d.rt.Revision(); return ok })
}

func (d *drill) waitFor(what string, cond func() bool) {
	d.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	d.t.Fatalf("timeout waiting for %s; status: %+v", what, d.rt.Status())
}

// commitState describes what recheckCommitStatus looks at, for a failing test.
func (d *drill) commitState() string {
	d.rt.syncMu.Lock()
	defer d.rt.syncMu.Unlock()
	a := d.rt.Active()
	out := fmt.Sprintf("commit=%s/%s active=%s", d.rt.commit.sha, d.rt.commit.state, a.rev.SHA)
	for key, s := range a.servers {
		s.mu.Lock()
		_, paused := d.rt.Paused(key)
		out += fmt.Sprintf(" server %s applied=%v health=%s paused=%v;", key, s.applied, s.health, paused)
		s.mu.Unlock()
	}
	out += fmt.Sprintf(" bots=%v", d.rt.botFailures(a.repo))
	for _, i := range a.instances {
		i.mu.Lock()
		out += fmt.Sprintf(" %s=%s", i.id(), i.health)
		i.mu.Unlock()
	}
	return out
}

func (d *drill) server() ServerStatus {
	for _, s := range d.rt.Status().Servers {
		if s.Key == "my-community" {
			return s
		}
	}
	d.t.Fatal("no server my-community")
	return ServerStatus{}
}

func (d *drill) channel(name string) discord.Channel {
	d.t.Helper()
	c, ok := d.w.ChannelByName(d.serverID, name)
	if !ok {
		d.t.Fatalf("no channel %s", name)
	}
	return c
}

// locked reports whether @everyone is denied SendMessages in #general.
func (d *drill) locked() bool {
	for _, o := range d.channel("general").Overwrites {
		if o.ID == d.serverID && o.Deny.Has(discord.MustPermissions("SendMessages")) {
			return true
		}
	}
	return false
}

// lock is the moderator locking #general in Discord.
func (d *drill) lock() {
	d.t.Helper()
	ows := []discord.Overwrite{{ID: d.serverID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")}}
	if _, err := d.w.AdminUpdateChannel(d.channel("general").ID, discord.ChannelParams{Overwrites: &ows}); err != nil {
		d.t.Fatal(err)
	}
}

func (d *drill) writes() int { return d.w.CountCalls(d.bot.ID, reconcileWrites...) }

func (d *drill) mustStayLocked(step string) {
	d.t.Helper()
	if !d.locked() {
		d.t.Fatalf("%s reverted the lock of #general while paused", step)
	}
}

// The emergency drill: during a raid a moderator locks #general in Discord and pauses
// enforcement. The lock survives a periodic resync, a gateway event, a new revision and Sync
// now, and a restart; plans and drift are still reported. An explicit resume restores
// enforcement: the next sync reverts the lock, and the commit status goes green.
func TestPauseDrill(t *testing.T) {
	for _, selfHeal := range []bool{false, true} {
		t.Run(fmt.Sprintf("selfHeal=%v", selfHeal), func(t *testing.T) { runDrill(t, selfHeal) })
	}
}

func runDrill(t *testing.T, selfHeal bool) {
	d := newDrill(t, selfHeal)
	ctx := context.Background()
	d.waitFor("synced", func() bool { s := d.server(); return s.Sync == SyncSynced && s.Health == HealthHealthy })
	if d.locked() {
		t.Fatal("#general starts locked")
	}
	changes, stopChanges := d.rt.SubscribeChanges()
	defer stopChanges()

	// The raid: lock, then pause right away. With selfHeal the lock armed a debounced
	// self-heal sync (2 s): the pause must cancel it.
	d.lock()
	if !selfHeal {
		d.waitFor("drift reported", func() bool { return d.server().Sync == SyncOutOfSync })
	}
	p, created, err := d.rt.Pause(ctx, "my-community", PauseInfo{By: "discord:42", ByName: "moderator", Reason: "raid"})
	if err != nil || !created || p.Reason != "raid" || p.At.IsZero() {
		t.Fatalf("pause = %+v %v %v", p, created, err)
	}
	if again, created, err := d.rt.Pause(ctx, "my-community", PauseInfo{By: "discord:43"}); err != nil || created || again.By != "discord:42" {
		t.Fatalf("second pause = %+v %v %v", again, created, err)
	}
	base := d.writes()
	time.Sleep(2500 * time.Millisecond) // past the self-heal debounce
	d.mustStayLocked("the gateway event of the lock")

	// Plans and drift are still computed: the moderator sees what differs from git.
	d.waitFor("paused and OutOfSync", func() bool {
		s := d.server()
		return s.Paused != nil && s.Sync == SyncOutOfSync && strings.Contains(s.Message, "enforcement is paused")
	})
	drift, err := d.rt.Drift(ctx, "my-community")
	if err != nil || !slices.ContainsFunc(drift, func(e DriftEntry) bool { return e.Kind == "channel" && e.Key == "general" && e.Field == "overwrites" }) {
		t.Fatalf("drift = %+v, %v", drift, err)
	}
	if s := d.server(); s.Paused.ByName != "moderator" || s.Paused.Reason != "raid" {
		t.Fatalf("paused = %+v", s.Paused)
	}
	sawStatus := false
	for len(changes) > 0 {
		if c := <-changes; c.Topic == ChangeStatus {
			sawStatus = true
		}
	}
	if !sawStatus {
		t.Error("the pause sent no status change (SSE)")
	}

	// 1. the periodic resync
	d.rt.resyncServers(ctx)
	d.mustStayLocked("the periodic resync")

	// 2. a gateway event on the managed channel
	topic := "raid en cours"
	if _, err := d.w.AdminUpdateChannel(d.channel("general").ID, discord.ChannelParams{Topic: &topic}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	d.mustStayLocked("a gateway event")
	if d.channel("general").Topic != topic {
		t.Fatal("a gateway event reverted the topic while paused")
	}

	// 3. a new revision (a git commit): activated, nothing applied, the commit status fails
	file := filepath.Join(d.dir, "servers/my-community.yaml")
	replaceIn(t, file, "      userLimit: 10\n", "      userLimit: 12\n")
	sha := strings.Repeat("b", 40)
	d.statuses.take()
	if err := d.rt.Activate(ctx, source.Revision{ID: sha[:7], SHA: sha, Source: "git", Message: "raise the voice limit", Dir: d.dir}, false); err != nil {
		t.Fatal(err)
	}
	if rev, _ := d.rt.Revision(); rev.SHA != sha {
		t.Fatalf("active revision = %s", rev.ID)
	}
	d.mustStayLocked("a new revision")
	if d.channel("Voice").UserLimit != 10 {
		t.Fatal("the new revision was applied to a paused server")
	}
	if got := d.statuses.take(); len(got) != 2 || got[1].state != CommitFailure || got[1].desc != "Server my-community paused by moderator: not applied" {
		t.Fatalf("commit status of the paused revision: %+v", got)
	}
	recs, _ := d.store.Syncs().List(ctx, "server:my-community", 5)
	if len(recs) == 0 || recs[0].Status != SyncRecordSkipped || !strings.Contains(recs[0].Message, "paused by moderator") {
		t.Fatalf("history after the paused revision: %+v", recs)
	}

	// 4. Sync now
	if _, _, err := d.rt.SyncServer(ctx, "my-community"); !errors.As(err, new(*PausedError)) || !errors.Is(err, reconcile.ErrPaused) ||
		!strings.Contains(err.Error(), "paused on server my-community by moderator") {
		t.Fatalf("sync now while paused = %v", err)
	}
	d.mustStayLocked("Sync now")
	if n := d.writes(); n != base {
		t.Fatalf("%d configuration write(s) while paused", n-base)
	}

	// Resume: enforcement is back; the next sync reverts the lock and applies the revision.
	prev, resumed, err := d.rt.Resume(ctx, "my-community", "discord:42", "moderator")
	if err != nil || !resumed || prev.Reason != "raid" {
		t.Fatalf("resume = %+v %v %v", prev, resumed, err)
	}
	if s := d.server(); s.Paused != nil {
		t.Fatalf("still paused: %+v", s.Paused)
	}
	if _, again, _ := d.rt.Resume(ctx, "my-community", "discord:42", "moderator"); again {
		t.Fatal("resumed twice")
	}
	if selfHeal {
		d.rt.resyncServers(ctx) // the periodic resync applies with selfHeal
	} else {
		d.rt.resyncServers(ctx)
		if !d.locked() {
			t.Fatal("without selfHeal the resync applied")
		}
		if _, n, err := d.rt.SyncServer(ctx, "my-community"); err != nil || n == 0 {
			t.Fatalf("sync now after resume = %d, %v", n, err)
		}
	}
	if d.locked() {
		t.Fatal("the lock survived the sync after resume")
	}
	if d.channel("Voice").UserLimit != 12 {
		t.Fatal("the revision was not applied after resume")
	}
	// With selfHeal, the sync that applies may be the self-heal one armed by the resume, still
	// reporting in its own goroutine when the resync returns: wait for the report.
	var got []statusEvent
	deadline := time.Now().Add(15 * time.Second)
	for len(got) == 0 && time.Now().Before(deadline) {
		got = append(got, d.statuses.take()...)
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) == 0 {
		t.Fatalf("no commit status after resume and sync: %s", d.commitState())
	}
	if len(got) != 1 || got[0].state != CommitSuccess || got[0].sha != sha {
		t.Fatalf("commit status after resume and sync: %+v", got)
	}
	d.waitFor("synced", func() bool { s := d.server(); return s.Sync == SyncSynced && s.Message == "" })
	recs, _ = d.store.Syncs().List(ctx, "server:my-community", 10)
	var statuses []string
	for _, r := range recs {
		statuses = append(statuses, r.Status)
	}
	if !slices.Contains(statuses, SyncRecordPaused) || !slices.Contains(statuses, SyncRecordResumed) {
		t.Fatalf("history: %v", statuses)
	}
}

// A pause is runtime state in the store: it survives a restart, and the first activation
// after it applies nothing.
func TestPauseSurvivesRestart(t *testing.T) {
	d := newDrill(t, true)
	ctx := context.Background()
	d.waitFor("synced", func() bool { return d.server().Sync == SyncSynced })
	if _, _, err := d.rt.Pause(ctx, "my-community", PauseInfo{By: "runtime-token", Reason: "raid"}); err != nil {
		t.Fatal(err)
	}
	d.lock()
	d.stop()
	base := d.writes()

	d.start()
	s := d.server()
	if s.Paused == nil || s.Paused.Reason != "raid" || s.Paused.Who() != "runtime-token" {
		t.Fatalf("after a restart: %+v", s)
	}
	time.Sleep(2500 * time.Millisecond)
	d.mustStayLocked("a restart")
	if n := d.writes(); n != base {
		t.Fatalf("%d configuration write(s) after the restart", n-base)
	}
	raw, err := d.store.KV(pauseKVScope).Get(ctx, "my-community")
	if err != nil || !strings.Contains(string(raw), `"reason":"raid"`) {
		t.Fatalf("stored pause = %s, %v", raw, err)
	}
	if _, _, err := d.rt.Resume(ctx, "my-community", "runtime-token", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.KV(pauseKVScope).Get(ctx, "my-community"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("pause still stored after resume: %v", err)
	}
}

// A pause during a sync's plan stops it before the apply (the reconciler's backstop).
func TestReconcilerRefusesWhilePaused(t *testing.T) {
	d := newDrill(t, false)
	ctx := context.Background()
	d.waitFor("synced", func() bool { return d.server().Sync == SyncSynced })
	d.lock()
	a := d.rt.Active()
	rec := a.servers["my-community"].rec
	if _, _, err := d.rt.Pause(ctx, "my-community", PauseInfo{By: "x"}); err != nil {
		t.Fatal(err)
	}
	if p, _, err := rec.Sync(ctx); !errors.Is(err, reconcile.ErrPaused) || len(p.Changes) == 0 {
		t.Fatalf("reconciler sync while paused = %d change(s), %v", len(p.Changes), err)
	}
	if _, err := rec.Apply(ctx, reconcile.Plan{}); !errors.Is(err, reconcile.ErrPaused) {
		t.Fatalf("reconciler apply while paused = %v", err)
	}
	d.mustStayLocked("the reconciler")
}
