package runtime

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cicord/cicord/internal/plugin"
)

// The probe uses only public SDK methods; the runtime supplies a real plugin VM and
// the normal Discord gateway / REST boundary backed by the simulator.
func TestModerationSDK(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		extra := []string{"discord.members.moderate", "discord.messages.manage", "discord.messages.read", "portal.routes"}
		requireInWelcome(t, dir, extra, []string{"ServerModeration", "MessageContent"})
		grants := "        - discord.dm.send"
		for _, cap := range extra {
			grants += "\n        - " + cap
		}
		replaceIn(t, filepath.Join(dir, "bots/moderator/bot.yaml"), "        - discord.dm.send", grants)
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  routes: {
      "POST /probe": {access:"admin",async handler(ctx,req) {
         const {user,member,verb}=req.body;
         if(verb==="ban") {
           await ctx.server.ban(user,{reason:"test case"});
           const bans=await ctx.server.bans({limit:1});
           return {id:bans[0].user.id, reason:bans[0].reason};
         }
         if(verb==="unban") {
           await ctx.server.unban(user,"expiry");await ctx.server.unban(user,"replay");
           return {total:(await ctx.server.bans()).length};
         }
         if(verb==="roles") {
           const m=await ctx.server.member(member);
           await m.setRoles(["member"],"test");await m.removeTimeout("test");
           return {role:m.hasRole("member")};
         }
         const channel=ctx.server.channel("general"); const filter={limit:250,author:member,contains:"sample"};
         if(verb==="preview")return channel.previewPurge(filter);return channel.purge(filter);
      }}
    },
    events: {
      async banAdd(ctx,e){await ctx.kv.set("added",e.user.id)},
      async banRemove(ctx,e){await ctx.kv.set("removed",e.user.id)},`)
	}, -1)
	outsider := e.world.CreateUser("outside")
	member := e.world.CreateUser("participant")
	if err := e.world.AddMember(e.serverID, member.ID); err != nil {
		t.Fatal(err)
	}
	role, _ := e.world.RoleByName(e.serverID, "Member")
	e.waitFor("welcome grants the member role", func() bool { m, ok := e.world.Member(e.serverID, member.ID); return ok && m.HasRole(role.ID) })
	general, _ := e.world.ChannelByName(e.serverID, "general")
	for n := range 205 {
		text := "sample"
		if n%2 == 0 {
			text = "keep"
		}
		if _, err := e.world.SendMessage(member.ID, general.ID, text); err != nil {
			t.Fatal(err)
		}
	}
	call := func(verb string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"verb": verb, "member": member.ID, "user": outsider.ID})
		resp, _, err := e.rt.HandleRoute(t.Context(), "my-community", "moderator", "welcome", "POST", "/probe", plugin.RouteRequest{Body: body})
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if r := call("ban"); r["id"] != outsider.ID || r["reason"] != "cicord: moderator/welcome: test case" {
		t.Fatalf("ban: %+v", r)
	}
	if r := call("unban"); r["total"] != float64(0) {
		t.Fatalf("unban: %+v", r)
	}
	if r := call("roles"); r["role"] != true {
		t.Fatalf("roles: %+v", r)
	}
	if r := call("preview"); len(r["ids"].([]any)) != 102 {
		t.Fatalf("preview: %+v", r)
	}
	if r := call("purge"); r["deleted"] != float64(102) {
		t.Fatalf("purge: %+v", r)
	}
	for _, key := range []string{"added", "removed"} {
		e.waitFor(key, func() bool {
			v, err := e.rt.Store().KV("plugin:moderator/welcome/my-community").Get(t.Context(), key)
			return err == nil && string(v) == "\""+outsider.ID+"\""
		})
	}
}
