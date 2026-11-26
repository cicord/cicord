package api

import (
	"slices"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

func TestSimulatorActorViews(t *testing.T) {
	e := start(t)
	c := e.client()
	ctx := t.Context()
	sid := e.serverID()
	actor := e.world.CreateUser("participant")
	if err := e.world.AddMember(sid, actor.ID); err != nil {
		t.Fatal(err)
	}
	outsider := e.world.CreateUser("unrelated")
	if err := e.world.AddMember(sid, outsider.ID); err != nil {
		t.Fatal(err)
	}
	name := "private-parity-ticket"
	ticket, err := e.world.AdminCreateChannel(sid, discord.ChannelParams{Name: &name, Type: discord.ChannelText, Overwrites: &[]discord.Overwrite{
		{ID: sid, Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel", "ReadMessageHistory")},
		{ID: actor.ID, Type: discord.OverwriteMember, Allow: discord.MustPermissions("ViewChannel", "ReadMessageHistory")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.world.AdminClient(sid).SendMessage(ctx, ticket.ID, discord.MessageCreate{Content: "private staff reply"}); err != nil {
		t.Fatal(err)
	}
	channels := func(user string) []string {
		t.Helper()
		var out struct {
			Servers []struct{ Channels []struct{ ID string } }
		}
		c.expect(200, "GET", "/api/v1/sim/state?userId="+user, nil).json(t, &out)
		var ids []string
		for _, s := range out.Servers {
			for _, ch := range s.Channels {
				ids = append(ids, ch.ID)
			}
		}
		return ids
	}
	if !slices.Contains(channels(actor.ID), ticket.ID) || !slices.Contains(channels(e.seed.Owner.ID), ticket.ID) || slices.Contains(channels(outsider.ID), ticket.ID) {
		t.Fatal("actor-specific channel list mismatch")
	}
	c.expect(404, "GET", "/api/v1/sim/state?userId=unknown", nil)
	path := "/api/v1/sim/channels/" + ticket.ID + "/messages?userId="
	c.expect(403, "GET", path+outsider.ID, nil)
	var history struct{ Items []messageDTO }
	c.expect(200, "GET", path+actor.ID, nil).json(t, &history)
	if len(history.Items) != 1 || history.Items[0].Content != "private staff reply" {
		t.Fatal(history)
	}
	general, _ := e.world.ChannelByName(sid, "general")
	prefix := "/api/v1/sim/commands?serverId=" + sid + "&channelId=" + general.ID + "&userId="
	cmds := func(user string) []string {
		t.Helper()
		var out struct{ Items []struct{ Name string } }
		c.expect(200, "GET", prefix+user, nil).json(t, &out)
		var names []string
		for _, item := range out.Items {
			names = append(names, item.Name)
		}
		return names
	}
	if !slices.Contains(cmds(e.seed.Owner.ID), "welcome-preview") || slices.Contains(cmds(actor.ID), "welcome-preview") || !slices.Contains(cmds(actor.ID), "welcome-stats") {
		t.Fatal("moderator/member command list mismatch")
	}
	until := time.Now().Add(time.Minute)
	if _, err := e.world.AdminUpdateMember(sid, actor.ID, discord.MemberParams{TimeoutUntil: &until}); err != nil {
		t.Fatal(err)
	}
	c.expect(200, "GET", path+actor.ID, nil)
	if got := cmds(actor.ID); len(got) != 0 {
		t.Fatal(got)
	}
	c.expect(400, "GET", "/api/v1/sim/commands?userId="+actor.ID, nil)
}
