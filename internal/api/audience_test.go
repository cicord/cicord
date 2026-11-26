package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

func TestPortalAudienceRevocation(t *testing.T) {
	e := startFull(t, func(dir string) {
		patch := func(file, old, new string) {
			t.Helper()
			path := filepath.Join(dir, file)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), old) {
				t.Fatalf("fixture changed: %s", file)
			}
			writeTestFile(t, path, strings.Replace(string(content), old, new, 1))
		}
		patch("plugins/welcome/plugin.yaml", "\n  capabilities:\n", "\n  capabilities:\n    - portal.routes\n")
		patch("bots/moderator/bot.yaml", "        - portal.links", "        - portal.routes\n        - portal.links")
		patch("plugins/welcome/src/index.ts", "  events: {", `  routes: {"GET /audience": {access:"member",handler(ctx,req){
    return {visible:req.viewer.member.canView("staff-chat"),comparison:ctx.server.compareAudience("staff-chat","general")};
  }}},
  events: {`)
	}, nil, func(o *Options) { o.MemberCacheTTL = time.Hour })
	staff := e.moderator()
	ordinary := e.seed.Users[0]
	if staff.ID == ordinary.ID {
		ordinary = e.seed.Users[1]
	}
	if err := e.world.AddMember(e.serverID(), ordinary.ID); err != nil {
		t.Fatal(err)
	}
	a, b := e.client(), e.client()
	a.login(staff.ID)
	b.login(ordinary.ID)
	path := "/api/v1/portal/" + serverKey + "/plugins/moderator/welcome/audience"
	check := func(c *client, want bool) {
		t.Helper()
		var data struct {
			Visible    bool
			Comparison string
		}
		c.expect(200, "GET", path, nil).json(t, &data)
		if data.Visible != want || data.Comparison != "wider" {
			t.Fatalf("audience %+v; want visible=%v, wider", data, want)
		}
	}
	check(a, true)
	check(b, false)
	channelID, _ := e.rt.ResourceID(serverKey, state.KindChannel, "staff-chat")
	channel, ok := e.world.Channel(channelID)
	if !ok {
		t.Fatal("missing staff channel")
	}
	denied := append([]discord.Overwrite{}, channel.Overwrites...)
	denied = append(denied, discord.Overwrite{ID: staff.ID, Type: discord.OverwriteMember, Deny: discord.MustPermissions("ReadMessageHistory")})
	if _, err := e.world.AdminUpdateChannel(channel.ID, discord.ChannelParams{Overwrites: &denied}); err != nil {
		t.Fatal(err)
	}
	check(a, false)
	if _, err := e.world.AdminUpdateChannel(channel.ID, discord.ChannelParams{Overwrites: &channel.Overwrites}); err != nil {
		t.Fatal(err)
	}
	check(a, true)
	role, ok := e.world.RoleByName(e.serverID(), "Moderator")
	if !ok {
		t.Fatal("missing moderator role")
	}
	if err := e.world.AdminRemoveMemberRole(e.serverID(), staff.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	check(a, false)
}
