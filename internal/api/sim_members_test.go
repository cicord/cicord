package api

import (
	"net/url"
	"slices"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestSimMemberVoiceAndOwnReaction(t *testing.T) {
	e := start(t)
	c := e.client()
	user := e.seed.Owner.ID
	guild := e.serverID()
	name := "Lobby"
	lobby, err := e.world.AdminCreateChannel(guild, discord.ChannelParams{Name: &name, Type: discord.ChannelVoice})
	if err != nil {
		t.Fatal(err)
	}
	voice := "/api/v1/sim/servers/" + guild + "/members/" + user + "/voice"
	c.expect(404, "PUT", "/api/v1/sim/servers/999/members/"+user+"/voice", map[string]string{"channelId": lobby.ID})
	c.expect(204, "PUT", voice, map[string]string{"channelId": lobby.ID})
	var snapshot struct {
		Servers []struct {
			Members []struct {
				User           userDTO
				VoiceChannelID string
			}
		}
	}
	c.expect(200, "GET", "/api/v1/sim/state", nil).json(t, &snapshot)
	found := false
	for _, m := range snapshot.Servers[0].Members {
		if m.User.ID == user && m.VoiceChannelID == lobby.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("state omitted the joined member or their voice channel")
	}
	c.expect(204, "DELETE", voice, nil)
	if slices.ContainsFunc(e.world.VoiceStates(guild), func(s discord.VoiceState) bool { return s.UserID == user }) {
		t.Fatal("member still connected")
	}

	channel, _ := e.world.ChannelByName(guild, e.repo.Servers[serverKey].Spec.Channels["general"].Name)
	c.expect(400, "PUT", voice, map[string]string{"channelId": channel.ID})
	var msg messageDTO
	c.expect(201, "POST", "/api/v1/sim/messages", map[string]string{"userId": user, "channelId": channel.ID, "content": "Choose a role"}).json(t, &msg)
	route := "/api/v1/sim/channels/" + channel.ID + "/messages/" + msg.ID + "/reactions/" + url.PathEscape("📬") + "/" + user
	c.expect(204, "PUT", route, nil)
	if !slices.Contains(e.world.Reactions(msg.ID)["📬"], user) {
		t.Fatal("reaction absent")
	}
	c.expect(204, "DELETE", route, nil)
	if slices.Contains(e.world.Reactions(msg.ID)["📬"], user) {
		t.Fatal("reaction still present")
	}
}
