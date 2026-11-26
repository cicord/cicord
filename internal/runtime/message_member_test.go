package runtime

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
)

// A received guild message carries its author's member (from the gateway payload); the
// same message fetched over REST does not (Discord's REST message objects have none).
// ctx.server.cicordBotIds names the project's bot (session user and applicationId).
func TestMessageMemberAndProjectBots(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		requireInWelcome(t, dir, []string{"discord.messages.read"}, []string{"ServerMessages", "MessageContent"})
		replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), "        - discord.dm.send", "        - discord.dm.send\n        - discord.messages.read")
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  events: {
      async messageCreate(ctx,m){
        if(m.content!=="probe")return;
        const fetched=await m.channel.message(m.id);
        await ctx.kv.set("probe",{received:m.member?.id??null,fetched:fetched?.member?.id??null,bots:ctx.server.cicordBotIds});
      },`)
	}, -1)
	member := e.world.CreateUser("participant")
	if err := e.world.AddMember(e.serverID, member.ID); err != nil {
		t.Fatal(err)
	}
	role, _ := e.world.RoleByName(e.serverID, "Member")
	e.waitFor("welcome grants the member role", func() bool { m, ok := e.world.Member(e.serverID, member.ID); return ok && m.HasRole(role.ID) })
	general, _ := e.world.ChannelByName(e.serverID, "general")
	if _, err := e.world.SendMessage(member.ID, general.ID, "probe"); err != nil {
		t.Fatal(err)
	}
	kv := e.rt.Store().KV("plugin:moderator/welcome/my-community")
	var got struct {
		Received, Fetched *string
		Bots              []string
	}
	e.waitFor("probe", func() bool {
		raw, err := kv.Get(t.Context(), "probe")
		return err == nil && json.Unmarshal(raw, &got) == nil
	})
	if got.Received == nil || *got.Received != member.ID || got.Fetched != nil {
		t.Fatalf("received member %v, fetched member %v", got.Received, got.Fetched)
	}
	if !slices.Contains(got.Bots, e.bot.ID) || !slices.Contains(got.Bots, e.appID) {
		t.Fatalf("cicordBotIds = %v, want %s and %s", got.Bots, e.bot.ID, e.appID)
	}
}
