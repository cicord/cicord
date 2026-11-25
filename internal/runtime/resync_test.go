package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
)

func TestShouldApply(t *testing.T) {
	on, off := true, false
	heal := &config.Server{Spec: config.ServerSpec{SyncPolicy: config.SyncPolicy{SelfHealP: &on}}}
	noHeal := &config.Server{Spec: config.ServerSpec{SyncPolicy: config.SyncPolicy{SelfHealP: &off}}}
	unset := &config.Server{}
	for _, c := range []struct {
		srv  *config.Server
		t    syncTrigger
		want bool
	}{
		{heal, triggerRevision, true}, {heal, triggerManual, true}, {heal, triggerResync, true},
		{noHeal, triggerRevision, true}, {noHeal, triggerManual, true}, {noHeal, triggerResync, false},
		{unset, triggerResync, false}, // selfHeal defaults to false
	} {
		if got := shouldApply(c.srv, c.t, false); got != c.want || shouldApply(c.srv, c.t, true) {
			t.Errorf("shouldApply(selfHeal %v, %d) = %v, want %v", c.srv.Spec.SyncPolicy.SelfHealP, c.t, got, c.want)
		}
	}
}

func (e *env) server(key string) ServerStatus {
	e.t.Helper()
	for _, s := range e.rt.Status().Servers {
		if s.Key == key {
			return s
		}
	}
	e.t.Fatalf("no server %s in the status", key)
	return ServerStatus{}
}

// Without selfHeal, the periodic resync only re-plans: a manual change in Discord is
// reported (OutOfSync) and stays until an explicit sync, which reverts it.
func TestResyncWithoutSelfHealDoesNotRevert(t *testing.T) {
	e := startExampleResync(t, nil, nil, 50*time.Millisecond)
	w := e.world
	general, ok := w.ChannelByName(e.serverID, "general")
	if !ok {
		t.Fatal("channel general not created")
	}
	syncs := w.CountCalls(e.bot.ID)
	name := "renamed-by-hand"
	if _, err := w.AdminUpdateChannel(general.ID, discord.ChannelParams{Name: &name}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("drift reported", func() bool {
		s := e.server("my-community")
		return s.Sync == SyncOutOfSync && strings.Contains(s.Message, "1 change(s) to apply")
	})
	time.Sleep(500 * time.Millisecond) // several resync periods and the re-plan after the event
	if ch, _ := w.Channel(general.ID); ch.Name != name {
		t.Fatalf("the resync reverted the drift: %q", ch.Name)
	}
	if n := w.CountCalls(e.bot.ID); n != syncs {
		t.Fatalf("the resync changed Discord: %d calls, was %d", n, syncs)
	}
	if s := e.server("my-community"); s.Sync != SyncOutOfSync || s.Health != HealthHealthy {
		t.Fatalf("status after resyncs = %+v", s)
	}

	// An explicit sync applies.
	if _, applied, err := e.rt.SyncServer(context.Background(), "my-community"); err != nil || applied == 0 {
		t.Fatalf("sync now = %d, %v", applied, err)
	}
	if ch, _ := w.Channel(general.ID); ch.Name != "general" {
		t.Fatalf("sync now did not revert: %q", ch.Name)
	}
	e.waitFor("synced", func() bool { s := e.server("my-community"); return s.Sync == SyncSynced && s.Message == "" })
}

// A change that waits for a boost level keeps the server Synced; when the server reaches
// the level, the change becomes pending (OutOfSync, with a message that says so) without
// selfHeal, and an explicit sync applies it.
func TestBoostReachedWithoutSelfHeal(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		replaceIn(t, filepath.Join(dir, "servers/my-community.yaml"), "    preferredLocale: en-US\n",
			"    preferredLocale: en-US\n    banner: ../assets/banner.jpg\n")
	}, -1)
	w := e.world
	s := e.server("my-community")
	if s.Sync != SyncSynced || s.Message != "1 change(s) wait for a higher boost level" {
		t.Fatalf("before the boost: %+v", s)
	}
	if err := w.SetBoosts(e.serverID, 7); err != nil { // level 2
		t.Fatal(err)
	}
	e.waitFor("pending banner", func() bool {
		s := e.server("my-community")
		return s.Sync == SyncOutOfSync && strings.HasPrefix(s.Message, "1 change(s) to apply")
	})
	if srv, _ := w.Server(e.serverID); srv.BannerURL != "" {
		t.Fatalf("banner applied without a sync: %s", srv.BannerURL)
	}
	if _, applied, err := e.rt.SyncServer(context.Background(), "my-community"); err != nil || applied == 0 {
		t.Fatalf("sync now = %d, %v", applied, err)
	}
	if srv, _ := w.Server(e.serverID); srv.BannerURL == "" {
		t.Fatal("banner not applied by the sync")
	}
	e.waitFor("synced", func() bool { s := e.server("my-community"); return s.Sync == SyncSynced && s.Message == "" })
}

// Drift seen in a gateway event tells the admin console to refresh that server (its plan,
// resources and drift), not only the global status.
func TestDriftEventNotifiesServer(t *testing.T) {
	e := startExample(t)
	ch, stop := e.rt.SubscribeChanges()
	defer stop()
	general, _ := e.world.ChannelByName(e.serverID, "general")
	name := "renamed-by-hand"
	if _, err := e.world.AdminUpdateChannel(general.ID, discord.ChannelParams{Name: &name}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case c := <-ch:
			if c == (Change{Topic: ChangeSync, Server: "my-community"}) {
				return
			}
		case <-deadline:
			t.Fatal("no server-scoped sync change after drift")
		}
	}
}

// A plan (periodic, or after a drift event) does not hide a failed sync: the server stays
// Degraded with the error while changes remain to apply.
func TestPlanKeepsFailedSync(t *testing.T) {
	r := &Runtime{}
	now := time.Now()
	rt := &serverRT{cfg: &config.Server{}, health: HealthDegraded, message: "create channel x: boom", lastSync: &now}
	pending := reconcile.Plan{Changes: []reconcile.Change{{Action: reconcile.ActionCreate, Kind: reconcile.KindChannel, Key: "x"}}}
	r.planned(rt, pending, nil)
	if rt.health != HealthDegraded || rt.message != "create channel x: boom" {
		t.Fatalf("after a plan with pending changes: %s %q", rt.health, rt.message)
	}
	r.planned(rt, reconcile.Plan{}, nil) // fixed in Discord meanwhile
	if rt.health != HealthHealthy || rt.message != "" {
		t.Fatalf("after an empty plan: %s %q", rt.health, rt.message)
	}
	rt.lastSyncOK = true
	r.planned(rt, pending, nil)
	if rt.health != HealthHealthy || !strings.HasPrefix(rt.message, "1 change(s) to apply (selfHeal is off") {
		t.Fatalf("pending changes after a good sync: %s %q", rt.health, rt.message)
	}
}

// The reconciler's log lines carry the server attribute once (its logger adds it).
func TestLogAttributesOnce(t *testing.T) {
	e := startExample(t)
	found := false
	for _, line := range strings.Split(e.logs.String(), "\n") {
		if strings.Contains(line, "reconcile: applied") {
			found = true
			if n := strings.Count(line, " server="); n != 1 {
				t.Errorf("server= %d times: %s", n, line)
			}
		}
	}
	if !found {
		t.Fatalf("no reconcile log line:\n%s", e.logs.String())
	}
}
