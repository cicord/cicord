package disgoadapter

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/gateway"

	"github.com/cicord/cicord/internal/discord"
)

// The adapter reads these three gateway properties when serving an audience
// request. Drive its real raw dispatch handler in the same order as disgo.
type audienceGateway struct {
	gateway.Gateway
	status gateway.Status
	id     string
	seq    int
}

func (g *audienceGateway) Status() gateway.Status     { return g.status }
func (g *audienceGateway) SessionID() *string         { return &g.id }
func (g *audienceGateway) LastSequenceReceived() *int { return &g.seq }

func TestAudienceGatewayRecovery(t *testing.T) {
	s := testSession()
	gw := &audienceGateway{status: gateway.StatusReady, id: "first"}
	s.gw = gw
	dispatch := func(kind, payload string) {
		gw.seq++
		s.onGatewayEvent(gw, gateway.EventTypeRaw, gw.seq, gateway.EventRaw{
			EventType: gateway.EventType(kind), Payload: strings.NewReader(payload),
		})
	}
	const ready = `{"guilds":[{"id":"1","unavailable":true}]}`
	const ch = `{"id":"10","type":0,"guild_id":"1","permission_overwrites":[]}`
	const denied = `{"id":"10","type":0,"guild_id":"1","permission_overwrites":[{"id":"1","type":0,"deny":"1024","allow":"0"}]}`
	const guild = `{"id":"1","owner_id":"2","roles":[{"id":"1","permissions":"66560"}],"channels":[`
	check := func(known, readable bool) {
		t.Helper()
		snap := s.AudienceSnapshot("1")
		a := discord.NewAudience(snap.Channels["10"], snap.Roles)
		bits, ok := a.Permissions("3", nil, snap.OwnerID, false)
		if ok != known || bits.Has(discord.MustPermissions("ViewChannel", "ReadMessageHistory")) != readable {
			t.Fatalf("session=%s seq=%d got known=%v bits=%v want known=%v read=%v", gw.id, gw.seq, ok, bits, known, readable)
		}
	}
	dispatch("READY", ready)
	check(false, false)
	dispatch("GUILD_CREATE", guild+ch+"]}")
	check(true, true)
	gw.status = gateway.StatusDisconnected
	check(false, false)
	gw.status = gateway.StatusResuming
	dispatch("CHANNEL_UPDATE", denied)
	check(false, false)
	gw.status = gateway.StatusReady
	dispatch("RESUMED", "{}")
	check(true, false)

	dispatch("CHANNEL_UPDATE", ch)
	check(true, true)
	// A fresh READY at the library may become visible before the adapter has
	// handled it. Guild ACLs in the old session must already be inaccessible.
	gw.id = "second"
	if owner := discord.CurrentGuildOwner(s, "1"); owner != "" {
		t.Fatalf("former session still authorized owner %q", owner)
	}
	check(false, false)
	dispatch("READY", ready)
	check(false, false)
	dispatch("GUILD_CREATE", strings.Replace(guild, `"owner_id":"2"`, `"owner_id":"4"`, 1)+denied+"]}")
	check(true, false)
	if owner := discord.CurrentGuildOwner(s, "1"); owner != "4" {
		t.Fatalf("reconnected guild owner = %q", owner)
	}
	dispatch("GUILD_DELETE", `{"id":"1","unavailable":true}`)
	check(false, false)
	dispatch("GUILD_CREATE", guild+ch+"]}")
	check(true, true)
	dispatch("CHANNEL_UPDATE", `{"id":"10","type":0,"guild_id":"1"}`)
	check(false, false)
	dispatch("GUILD_CREATE", `{"id":"1","owner_id":"2","roles":[]}`)
	check(false, false)
}
