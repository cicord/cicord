package runtime

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
)

// SPA-16: a voice channel has no topic to recover it by. A handler that created its
// channel and died before recording it is replayed by the next process; createChannel with
// the same provisionKey returns the channel already owned instead of creating a second one.
// Once the channel is deleted, the key provisions a new one.
func TestProvisionKeySurvivesRestart(t *testing.T) {
	for _, db := range restartDatabases(t) {
		t.Run(db.name, func(t *testing.T) {
			p := newRestartProject(t, func(dir string) {
				requireInWelcome(t, dir, []string{"discord.channels.manage", "portal.routes"}, nil)
				grantWelcome(t, dir, "discord.channels.manage", "portal.routes")
				replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  routes: {
      "POST /room": {access:"admin",async handler(ctx,req) {
        const ch=await ctx.server.createChannel({name:"room",type:"voice",provisionKey:"room:"+req.body.user});
        const found=await ctx.server.recoverOwnedChannel({provisionKey:"room:"+req.body.user});
        const other=await ctx.server.recoverOwnedChannel({provisionKey:"room:nobody"});
        if(req.body.crash)throw new Error("crash before saveRoom");
        return {id:ch.id,found:found.status==="owned"&&found.id===ch.id,other:other.status};
      }}
    },
    events: {`)
			})
			call := func(e *env, crash bool) (map[string]any, error) {
				body, _ := json.Marshal(map[string]any{"user": "123456789012345678", "crash": crash})
				resp, _, err := e.rt.HandleRoute(t.Context(), "my-community", "moderator", "welcome", "POST", "/room", plugin.RouteRequest{Body: body})
				if err != nil {
					return nil, err
				}
				var out map[string]any
				if resp.Status >= 400 {
					return nil, nil
				}
				return out, json.Unmarshal(resp.Body, &out)
			}
			rooms := func() []discord.Channel {
				var out []discord.Channel
				for _, ch := range p.world.Channels(p.serverID) {
					if ch.Name == "room" && ch.Type == discord.ChannelVoice {
						out = append(out, ch)
					}
				}
				return out
			}
			e, stop := p.boot(t, db.open(t))
			if _, err := call(e, true); err != nil {
				t.Logf("crashed handler: %v", err)
			}
			if n := len(rooms()); n != 1 {
				t.Fatalf("%d rooms after the crashed handler", n)
			}
			stop()

			e, stop = p.boot(t, db.open(t))
			got, err := call(e, false)
			if err != nil || got == nil {
				t.Fatalf("replay: %v %v", got, err)
			}
			if r := rooms(); len(r) != 1 || got["id"] != r[0].ID || got["found"] != true || got["other"] != "missing" {
				t.Fatalf("replay created another room: %v, rooms %+v", got, r)
			}
			if err := p.world.AdminDeleteChannel(rooms()[0].ID); err != nil {
				t.Fatal(err)
			}
			again, err := call(e, false)
			if err != nil || again == nil || again["id"] == got["id"] || len(rooms()) != 1 {
				t.Fatalf("after deletion: %v %v, rooms %+v", again, err, rooms())
			}
			stop()
		})
	}
}
