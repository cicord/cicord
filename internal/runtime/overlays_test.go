package runtime

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/source"
)

// Two independent installations lock one channel. A new revision changes the base.
// Restart, lift one lock, then let the other expire while enforcement is paused.
func TestPersistentOverlayDrill(t *testing.T) {
	const key = "my-community"
	d := newDrill(t, false)
	ctx := context.Background()
	d.waitFor("base sync", func() bool { return d.server().Health == HealthHealthy })
	file := filepath.Join(d.dir, "servers/my-community.yaml")
	member := d.w.CreateUser("holder")
	if err := d.w.AddMember(d.serverID, member.ID); err != nil {
		t.Fatal(err)
	}
	replaceIn(t, file, "      name: Minecraft", "      name: Minecraft\n      members: [\""+member.ID+"\"]")
	activate := func(rev string) {
		t.Helper()
		if err := d.rt.Activate(ctx, source.Revision{ID: rev, SHA: rev, Source: "git", Dir: d.dir}, false); err != nil {
			t.Fatal(err)
		}
	}
	activate("members")
	role, ok := d.w.RoleByName(d.serverID, "Minecraft")
	if !ok {
		t.Fatal("no declared role")
	}
	holds := func() bool {
		c, _ := d.rt.Client(key)
		m, err := c.GetMember(ctx, d.serverID, member.ID)
		if err != nil {
			t.Fatal(err)
		}
		return m.HasRole(role.ID)
	}
	if !holds() {
		t.Fatal("missing base holder")
	}
	apply := func(origin string, end time.Time) error {
		sec := 18
		o := reconcile.Overlay{ID: "raid", Until: end, Reason: "lock " + origin,
			Channels: map[string]reconcile.ChannelRestriction{"general": {Slowmode: &sec, Overwrites: map[string]reconcile.OverwritePatch{"@everyone": {Deny: []string{"SendMessages"}}}}},
			Members:  map[string]reconcile.MemberRestriction{member.ID: {Suspend: true}}}
		return d.rt.updateOverlay(ctx, d.rt.Active().servers[key], origin, o.ID, &o, origin)
	}
	end := time.Now().Add(time.Hour)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, origin := range []string{"warden/guard", "warden/moderation"} {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- apply(origin, end) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	d.waitFor("restrictions", func() bool { return d.locked() && !holds() && d.channel("general").RateLimitPerUser == 18 })
	replaceIn(t, file, "      topic: General chat", "      topic: Revised base\n      slowmode: 3")
	activate("new-base")
	if !d.locked() || d.channel("general").Topic != "Revised base" {
		t.Fatal("revision lost the lock or base")
	}
	d.stop()
	d.start()
	p, err := d.rt.PlanServer(ctx, key)
	if err != nil || len(p.Overlays) != 2 || !p.Empty() {
		t.Fatalf("after restart: %+v %v", p, err)
	}
	if err = d.rt.RemoveOverlay(ctx, key, "warden/moderation", "raid", "operator"); err != nil {
		t.Fatal(err)
	}
	if !d.locked() || holds() {
		t.Fatal("lifting first overlay released the second")
	}
	// Shorten the surviving overlay to exercise the real durable task.
	if err = apply("warden/guard", time.Now().Add(1*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.rt.Pause(ctx, key, PauseInfo{By: "operator"}); err != nil {
		t.Fatal(err)
	}
	changed := "Manual topic during incident"
	if _, err = d.w.AdminUpdateChannel(d.channel("general").ID, discord.ChannelParams{Topic: &changed}); err != nil {
		t.Fatal(err)
	}
	d.waitFor("expiry persisted while paused", func() bool {
		s, err := d.rt.overlayState(ctx, key)
		return err == nil && len(s.Active) == 0 && len(s.Pending) > 0
	})
	if !d.locked() || holds() {
		t.Fatal("expiration wrote to Discord while paused")
	}
	if _, _, err = d.rt.Resume(ctx, key, "operator", ""); err != nil {
		t.Fatal(err)
	}
	c := d.channel("general")
	if d.locked() || !holds() || c.RateLimitPerUser != 3 || c.Topic != changed {
		t.Fatalf("resume changed unrelated fields or restored stale base: %s, slowmode %d, locked %v, holder %v", c.Topic, c.RateLimitPerUser, d.locked(), holds())
	}
}
func TestOverlayValidation(t *testing.T) {
	now := time.Now()
	for _, value := range []reconcile.Overlay{
		{ID: "x", Reason: "x", Until: now.Add(8 * 24 * time.Hour), Server: &reconcile.ServerRestriction{Verification: "high"}},
		{ID: "x", Reason: "x", Until: now.Add(time.Hour), Server: &reconcile.ServerRestriction{Verification: "none"}},
		{ID: "x", Reason: "x", Until: now.Add(time.Hour), Members: map[string]reconcile.MemberRestriction{"bad": {Suspend: true}}},
	} {
		if err := value.Validate(now); err == nil {
			t.Fatalf("accepted %+v", value)
		}
	}
}
