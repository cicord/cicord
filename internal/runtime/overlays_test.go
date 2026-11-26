package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state"
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
		{ID: "x", Reason: "x", Until: now.Add(time.Hour), Channels: map[string]reconcile.ChannelRestriction{"general": {Overwrites: map[string]reconcile.OverwritePatch{"@bot": {Deny: []string{"ViewChannel"}}}}}},
		{ID: "x", Reason: "x", Until: now.Add(time.Hour), Channels: map[string]reconcile.ChannelRestriction{"*": {Overwrites: map[string]reconcile.OverwritePatch{"@bot": {Deny: []string{"SendMessages", "ManageRoles"}}}}}},
	} {
		if err := value.Validate(now); err == nil {
			t.Fatalf("accepted %+v", value)
		}
	}
}

// MOD-12: one plugin's overlays cannot use the whole server budget.
func TestOverlayQuotaPerOrigin(t *testing.T) {
	var active []reconcile.Overlay
	for range overlayOriginLimit {
		if err := overlayQuota(active, "warden/moderation"); err != nil {
			t.Fatal(err)
		}
		active = append(active, reconcile.Overlay{Origin: "warden/moderation"})
	}
	if err := overlayQuota(active, "warden/moderation"); err == nil {
		t.Fatal("origin exceeded its quota")
	}
	for len(active) < overlayServerLimit {
		active = append(active, reconcile.Overlay{Origin: "warden/locks"})
	}
	// The server is full, but an origin with few overlays keeps its reserve.
	for range overlayOriginReserve {
		if err := overlayQuota(active, "warden/guard"); err != nil {
			t.Fatalf("reserve: %v", err)
		}
		active = append(active, reconcile.Overlay{Origin: "warden/guard"})
	}
	if err := overlayQuota(active, "warden/guard"); err == nil {
		t.Fatal("reserve exceeded")
	}
	if err := overlayQuota(active, "warden/locks"); err == nil {
		t.Fatal("full server accepted a heavy origin")
	}
}

// MOD-11: an overlay that would hide a managed channel from a non-Administrator
// bot is refused when applied, so it cannot block every later overlay sync.
func TestOverlayRefusesBotLockout(t *testing.T) {
	const key = "my-community"
	d := newDrill(t, false)
	ctx := context.Background()
	d.waitFor("base sync", func() bool { return d.server().Health == HealthHealthy })
	role, ok := d.w.RoleByName(d.serverID, d.bot.Username)
	if !ok {
		t.Fatal("no bot role")
	}
	perms := discord.MustPermissions("ViewChannel", "ManageChannels", "ManageRoles", "SendMessages", "ManageServer")
	if _, err := d.w.AdminUpdateRole(d.serverID, role.ID, discord.RoleParams{Permissions: &perms}); err != nil {
		t.Fatal(err)
	}
	apply := func(id string, deny ...string) error {
		o := reconcile.Overlay{ID: id, Until: time.Now().Add(time.Hour), Reason: "test",
			Channels: map[string]reconcile.ChannelRestriction{"general": {Overwrites: map[string]reconcile.OverwritePatch{"@everyone": {Deny: deny}}}}}
		return d.rt.updateOverlay(ctx, d.rt.Active().servers[key], "warden/guard", o.ID, &o, "test")
	}
	if err := apply("hide", "ViewChannel"); err == nil || !strings.Contains(err.Error(), "lock the bot out") {
		t.Fatalf("self-lockout accepted: %v", err)
	}
	if s, err := d.rt.overlayState(ctx, key); err != nil || len(s.Active) != 0 {
		t.Fatalf("refused overlay persisted: %+v %v", s, err)
	}
	if err := apply("raid", "SendMessages"); err != nil {
		t.Fatal(err)
	}
	d.waitFor("lock", d.locked)
	// The "*" neutral target goes through the same check. It never touches @bot,
	// so neutralizing View Channel on every other overwrite does not lock it out.
	star := reconcile.Overlay{ID: "star", Until: time.Now().Add(time.Hour), Reason: "test",
		Channels: map[string]reconcile.ChannelRestriction{"*": {Overwrites: map[string]reconcile.OverwritePatch{"*": {Neutral: []string{"ViewChannel", "SendMessages"}}}}}}
	if err := d.rt.updateOverlay(ctx, d.rt.Active().servers[key], "warden/guard", star.ID, &star, "test"); err != nil {
		t.Fatalf("star overlay: %v", err)
	}
}

// requestSync is throttled per installation: one accepted full reconciliation
// (and audit entry) per interval.
func TestRequestSyncThrottle(t *testing.T) {
	const key = "my-community"
	d := newDrill(t, false)
	ctx := context.Background()
	d.waitFor("base sync", func() bool { return d.server().Health == HealthHealthy })
	a := d.rt.Active()
	var irt *instanceRT
	for _, i := range a.instances {
		if i.cfg.Server == key {
			irt = i
			break
		}
	}
	if irt == nil {
		t.Fatal("no instance")
	}
	srt := a.servers[key]
	if err := d.rt.requestSync(ctx, irt.cfg, srt, "warden/x", "restore"); err != nil {
		t.Fatal(err)
	}
	if err := d.rt.requestSync(ctx, irt.cfg, srt, "warden/x", "again"); err == nil || !strings.Contains(err.Error(), "requested recently") {
		t.Fatalf("second request: %v", err)
	}
	entries, err := d.store.Audit().List(ctx, state.AuditFilter{Server: key, Limit: 100})
	n := 0
	for _, e := range entries {
		if e.Action == "server.sync.request" {
			n++
		}
	}
	if err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}
	prev := syncRequestInterval
	syncRequestInterval = 0
	t.Cleanup(func() { syncRequestInterval = prev })
	if err := d.rt.requestSync(ctx, irt.cfg, srt, "warden/x", "later"); err != nil {
		t.Fatal(err)
	}
}

// MOD-03: a lock with "*" neutral holds against a role allow in Discord, keeps the
// staff role's allow, and lifting it restores the role's allow.
func TestOverlayLockNeutralizesRoleAllowInDiscord(t *testing.T) {
	const key = "my-community"
	d := newDrill(t, false)
	ctx := context.Background()
	d.waitFor("base sync", func() bool { return d.server().Health == HealthHealthy })
	file := filepath.Join(d.dir, "servers/my-community.yaml")
	replaceIn(t, file, "          allow: [SendMessages]\n", "          allow: [SendMessages]\n        member:\n          allow: [ViewChannel, SendMessages]\n        moderator:\n          allow: [SendMessages]\n")
	if err := d.rt.Activate(ctx, source.Revision{ID: "roles", SHA: "roles", Source: "git", Dir: d.dir}, false); err != nil {
		t.Fatal(err)
	}
	allows := func(name string) bool {
		role, ok := d.w.RoleByName(d.serverID, name)
		if !ok {
			t.Fatalf("no role %s", name)
		}
		for _, o := range d.channel("general").Overwrites {
			if o.ID == role.ID {
				return o.Allow.Has(discord.MustPermissions("SendMessages"))
			}
		}
		return false
	}
	d.waitFor("role allows", func() bool { return allows("Member") && allows("Moderator") })
	lock := reconcile.Overlay{ID: "lock", Until: time.Now().Add(time.Hour), Reason: "raid",
		Channels: map[string]reconcile.ChannelRestriction{"general": {Overwrites: map[string]reconcile.OverwritePatch{
			"@everyone": {Deny: []string{"SendMessages"}}, "*": {Neutral: []string{"SendMessages"}}}}}}
	if err := d.rt.updateOverlay(ctx, d.rt.Active().servers[key], "warden/moderation", lock.ID, &lock, "test"); err != nil {
		t.Fatal(err)
	}
	d.waitFor("locked against the role", func() bool { return d.locked() && !allows("Member") && allows("Moderator") })
	if err := d.rt.RemoveOverlay(ctx, key, "warden/moderation", "lock", "operator"); err != nil {
		t.Fatal(err)
	}
	d.waitFor("restored", func() bool { return !d.locked() && allows("Member") && allows("Moderator") })
}
