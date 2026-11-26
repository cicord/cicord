package reconcile

import (
	"slices"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
)

// MOD-03: an @everyone deny alone does not lock a channel where a role allows
// Send Messages. The "*" neutral target strips that allow from every existing
// overwrite except @everyone, @bot and staff, and removing the overlay restores it.
func TestOverlayLockNeutralizesRoleAllows(t *testing.T) {
	const staffUser = "123456789012345678"
	base := &config.Server{}
	base.Spec.Roles = map[string]config.RoleSpec{"verified": {}, "moderator": {}, "admin": {}, "other": {}}
	base.Spec.Access = &config.Access{Admins: &config.Principals{Roles: []string{"admin"}}, Moderators: &config.Principals{Roles: []string{"moderator"}, Users: []string{staffUser}}}
	send := []string{"SendMessages"}
	base.Spec.Categories = map[string]config.Category{"community": {Name: "Community", Overwrites: map[string]config.OverwriteSpec{"verified": {Allow: []string{"ViewChannel", "SendMessages"}}}}}
	base.Spec.Channels = map[string]config.Channel{
		"general": {Name: "general", Overwrites: map[string]config.OverwriteSpec{
			"verified":                 {Allow: []string{"ViewChannel", "SendMessages"}},
			"moderator":                {Allow: send},
			"admin":                    {Allow: send},
			"@bot":                     {Allow: send},
			"member:" + staffUser:      {Allow: send},
			"member:2" + staffUser[1:]: {Allow: send},
		}},
		"lounge": {Name: "lounge", Category: "community"},
	}
	lock := Overlay{Origin: "b/moderation", ID: "lock", Until: time.Now().Add(time.Hour), Reason: "raid", Channels: map[string]ChannelRestriction{"*": {Overwrites: map[string]OverwritePatch{
		"@everyone": {Deny: send},
		"*":         {Neutral: send},
	}}}}
	if err := lock.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	eff, _, _ := OverlayState{Active: []Overlay{lock}}.effective(base, time.Now())
	can := func(ch, target string) bool {
		p, _ := discord.ParsePermissions(eff.Spec.Channels[ch].Overwrites[target].Allow)
		return p.Has(discord.MustPermissions("SendMessages"))
	}
	ows := eff.Spec.Channels["general"].Overwrites
	if can("general", "verified") || !slices.Contains(ows["verified"].Allow, "ViewChannel") || can("general", "member:2"+staffUser[1:]) {
		t.Fatalf("role allow survives the lock: %+v", ows)
	}
	for _, kept := range []string{"moderator", "admin", "@bot", "member:" + staffUser} {
		if !can("general", kept) {
			t.Fatalf("%s lost its allow: %+v", kept, ows)
		}
	}
	if _, created := ows["other"]; created || len(ows) != 7 {
		t.Fatalf("lock created overwrites: %+v", ows)
	}
	if !slices.Contains(ows["@everyone"].Deny, "SendMessages") {
		t.Fatal("missing @everyone deny")
	}
	if can("lounge", "verified") {
		t.Fatalf("category-synced channel not locked: %+v", eff.Spec.Channels["lounge"].Overwrites)
	}
	if !slices.Contains(base.Spec.Channels["general"].Overwrites["verified"].Allow, "SendMessages") {
		t.Fatal("overlay mutated the revision")
	}
	// Lifting or expiry recomputes from the revision: the allow comes back.
	restored, _, _ := OverlayState{}.effective(base, time.Now())
	if p, _ := discord.ParsePermissions(restored.Spec.Channels["general"].Overwrites["verified"].Allow); !p.Has(discord.MustPermissions("SendMessages")) {
		t.Fatal("removal did not restore the allow")
	}
	expired := lock
	expired.Until = time.Now().Add(-time.Second)
	after, _, _ := OverlayState{Active: []Overlay{expired}}.effective(base, time.Now())
	if p, _ := discord.ParsePermissions(after.Spec.Channels["general"].Overwrites["verified"].Allow); !p.Has(discord.MustPermissions("SendMessages")) {
		t.Fatal("expired overlay still applies")
	}
	bad := lock
	bad.Channels = map[string]ChannelRestriction{"general": {Overwrites: map[string]OverwritePatch{"*": {Deny: send}}}}
	if err := bad.Validate(time.Now()); err == nil {
		t.Fatal(`deny on "*" accepted`)
	}
}
