package plugin

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestVoiceStateUpdateEvent(t *testing.T) {
	if got := EventIntents("voiceStateUpdate"); !reflect.DeepEqual(got, []string{"ServerVoiceStates"}) {
		t.Fatalf("intents = %v", got)
	}
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: {
    async voiceStateUpdate(ctx, u) {
      ctx.log.info("voice", { user: u.user.id, name: u.user.displayName, member: u.member ? u.member.displayName : null,
        roles: u.member ? u.member.roles.map((r) => r.key ?? r.id) : null,
        channel: u.channel ? [u.channel.id, u.channel.type, u.channel.categoryId ?? null] : null,
        previous: u.previous ? u.previous.id : null, flags: [u.selfMute, u.selfDeaf, u.streaming, u.video],
        frozen: Object.isFrozen(u) });
    },
  },
});`, nil)
	if !slices.Equal(h.def.RequiredIntents(), []string{"ServerVoiceStates"}) {
		t.Fatalf("required intents = %v", h.def.RequiredIntents())
	}
	h.inst.env.Resolver.(*fakeResolver).channels["400"] = discord.Channel{ID: "400", ServerID: "10", Name: "Hub", Type: discord.ChannelVoice, ParentID: "300"}
	member := discord.Member{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}, Nick: "Al", RoleIDs: []string{"200"}}
	prev := discord.VoiceState{ServerID: "10", ChannelID: "401", UserID: "42"}
	ev := discord.VoiceStateUpdateEvent{
		State:    discord.VoiceState{ServerID: "10", ChannelID: "400", UserID: "42", SelfMute: true, SelfVideo: true, Member: &member},
		Previous: &prev,
	}
	if EventName(ev) != "voiceStateUpdate" {
		t.Fatal(EventName(ev))
	}
	if err := h.inst.HandleEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	got := h.logged("voice").Attrs
	want := map[string]any{"user": "42", "name": "alice", "member": "Al", "roles": []any{"member"},
		"channel": []any{"400", "voice", "300"}, "previous": "401", "flags": []any{true, false, false, true}, "frozen": true}
	for k, w := range want {
		if !reflect.DeepEqual(got[k], w) {
			t.Errorf("%s = %#v, want %#v", k, got[k], w)
		}
	}
	// A leave without the member: the user only, no channel.
	leave := discord.VoiceStateUpdateEvent{State: discord.VoiceState{ServerID: "10", UserID: "43", Bot: true}}
	if err := h.inst.HandleEvent(context.Background(), leave); err != nil {
		t.Fatal(err)
	}
	got = h.logged("voice").Attrs
	if got["user"] != "43" || got["member"] != nil || got["channel"] != nil || got["previous"] != nil {
		t.Errorf("leave = %#v", got)
	}
}

const voiceOpsPlugin = `
import { command, definePlugin } from "@cicord/sdk";
const attempt = async (f: () => Promise<unknown>) => {
  try { await f(); return "ok"; } catch (e) { return (e as Error).name + ": " + (e as Error).message; }
};
export default definePlugin({
  commands: {
    voice: command({ description: "x", async run(ctx, it) {
      const room = await ctx.server.createChannel({ name: "Room", type: "voice", category: "support", userLimit: 4, bitrate: 96000 });
      const edited = await ctx.server.editChannel(room, { name: "Alice's room", userLimit: 0,
        overwrites: { "@everyone": { deny: ["Connect"] }, ["member:" + it.user.id]: { allow: ["Connect", "MoveMembers"] } } });
      await ctx.server.editChannel({ id: room.id }, { overwrites: { "@everyone": { clear: ["Connect"] }, ["member:" + it.user.id]: null } });
      const errs = [
        await attempt(() => ctx.server.createChannel({ name: "t", userLimit: 2 })),
        await attempt(() => ctx.server.createChannel({ name: "v", type: "voice", userLimit: 100 })),
        await attempt(() => ctx.server.createChannel({ name: "v", type: "voice", bitrate: 7999 })),
        await attempt(() => ctx.server.editChannel("general", { name: "x" })),
        await attempt(() => ctx.server.editChannel(room, { overwrites: { "@everyone": { allow: ["Connect"], clear: ["Connect"] } } })),
        await attempt(() => ctx.server.editChannel(room, { name: "" })),
        await attempt(() => it.member!.moveTo(null as never)),
      ];
      await it.member!.moveTo(room, "to your room");
      await it.member!.disconnect();
      const members = await ctx.server.voiceMembers(room);
      await it.reply(JSON.stringify({ room: room.id, type: room.type, category: room.categoryId ?? null, edited: edited.name,
        errs, members: members.map((m) => [m.id, m.displayName]) }));
    } }),
  },
});`

func voiceReply(t *testing.T, h *harness) map[string]any {
	t.Helper()
	if err := h.command("voice", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(contentOf(h.responses()[0])), &got); err != nil {
		t.Fatal(err, contentOf(h.responses()[0]))
	}
	return got
}

func TestVoiceChannelOps(t *testing.T) {
	var states []discord.VoiceState
	known := false
	h := start(t, voiceOpsPlugin, func(env *Env) {
		env.ServerVoiceStates = true
		env.VoiceStates = func() ([]discord.VoiceState, bool) { return states, known }
	})
	// The room's members: one carried by the voice state, one fetched (no member in the state).
	h.dc.hook = func(_ context.Context, name string, args []any) error {
		if name == "CreateChannel" && !known {
			known = true
			states = []discord.VoiceState{
				{ServerID: "10", ChannelID: "5001", UserID: "77"},
				{ServerID: "10", ChannelID: "5001", UserID: "9", Member: &discord.Member{ServerID: "10", User: discord.User{ID: "9", Username: "ann"}, Nick: "Annie"}},
				{ServerID: "10", ChannelID: "999", UserID: "100"},
			}
		}
		return nil
	}
	h.dc.members["77"] = discord.Member{ServerID: "10", User: discord.User{ID: "77", Username: "bob"}}
	got := voiceReply(t, h)

	cc := h.dc.Calls("CreateChannel")
	p := cc[0].Args[1].(discord.ChannelParams)
	if p.Type != discord.ChannelVoice || *p.UserLimit != 4 || *p.Bitrate != 96000 || *p.ParentID != "300" || p.Overwrites != nil {
		t.Fatalf("create params = %+v", p)
	}
	if got["room"] != "5001" || got["type"] != "voice" || got["category"] != "300" || got["edited"] != "Alice's room" {
		t.Errorf("reply = %v", got)
	}

	// Overwrites are patched: the second edit clears Connect from @everyone (an empty role
	// overwrite stays) and removes the member's.
	up := h.dc.Calls("UpdateChannel")
	if len(up) != 2 {
		t.Fatalf("UpdateChannel calls = %d", len(up))
	}
	first := up[0].Args[1].(discord.ChannelParams)
	if *first.Name != "Alice's room" || *first.UserLimit != 0 || !reflect.DeepEqual(*first.Overwrites, []discord.Overwrite{
		{ID: "10", Type: discord.OverwriteRole, Deny: discord.MustPermissions("Connect")},
		{ID: "42", Type: discord.OverwriteMember, Allow: discord.MustPermissions("Connect", "MoveMembers")},
	}) {
		t.Errorf("first edit = %+v %+v", first, *first.Overwrites)
	}
	second := up[1].Args[1].(discord.ChannelParams)
	if second.Name != nil || !reflect.DeepEqual(*second.Overwrites, []discord.Overwrite{{ID: "10", Type: discord.OverwriteRole}}) {
		t.Errorf("second edit = %+v", *second.Overwrites)
	}
	if !strings.HasPrefix(up[0].Args[2].(string), "cicord: edited by plugin test") {
		t.Errorf("reason = %q", up[0].Args[2])
	}

	errs := got["errs"].([]any)
	for k, want := range []string{
		"TypeError: createChannel(): userLimit is only for voice channels",
		"TypeError: createChannel(): userLimit must be an integer between 0 (no limit) and 99",
		"TypeError: createChannel(): bitrate must be an integer between 8000 and 384000",
		"not owned by this plugin instance",
		"TypeError: editChannel(): overwrite \"@everyone\": a permission is in more than one of allow, deny and clear",
		"TypeError: editChannel(): name must be 1-100 characters",
		"TypeError: moveTo: a voice channel is required",
	} {
		if !strings.Contains(errs[k].(string), want) {
			t.Errorf("error %d = %q, want %q", k, errs[k], want)
		}
	}

	um := h.dc.Calls("UpdateMember")
	if len(um) != 2 {
		t.Fatalf("UpdateMember calls = %+v", um)
	}
	if mp := um[0].Args[2].(discord.MemberParams); um[0].Args[1] != "42" || *mp.VoiceChannelID != "5001" || um[0].Args[3] != "to your room" {
		t.Errorf("move = %+v", um[0].Args)
	}
	if mp := um[1].Args[2].(discord.MemberParams); *mp.VoiceChannelID != "" {
		t.Errorf("disconnect = %+v", mp)
	}
	if !reflect.DeepEqual(got["members"], []any{[]any{"9", "Annie"}, []any{"77", "bob"}}) {
		t.Errorf("voice members = %v", got["members"])
	}
}

func TestVoiceCapabilitiesAndIntent(t *testing.T) {
	src := `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    voice: command({ description: "x", async run(ctx, it) {
      const out: string[] = [];
      for (const f of [() => it.member!.moveTo("general"), () => ctx.server.voiceMembers("general")]) {
        try { await f(); out.push("ok"); } catch (e) { out.push((e as Error).name + ": " + (e as Error).message); }
      }
      await it.reply(JSON.stringify({ errs: out }));
    } }),
  },
});`
	// Without discord.voice.move, and without the ServerVoiceStates intent.
	h := start(t, src, func(env *Env) {
		env.Grants = slices.DeleteFunc(slices.Clone(allGrants), func(c string) bool { return c == capVoiceMove })
	})
	errs := voiceReply(t, h)["errs"].([]any)
	if !strings.Contains(errs[0].(string), `CapabilityError: missing capability "discord.voice.move"`) ||
		!strings.Contains(errs[1].(string), "needs the ServerVoiceStates intent") {
		t.Errorf("errs = %v", errs)
	}
	// The intent declared, but the voice states not received yet: an error, never "nobody".
	h = start(t, src, func(env *Env) {
		env.ServerVoiceStates = true
		env.VoiceStates = func() ([]discord.VoiceState, bool) { return nil, false }
	})
	errs = voiceReply(t, h)["errs"].([]any)
	if !strings.Contains(errs[1].(string), "not known yet") {
		t.Errorf("errs = %v", errs)
	}
	if got := CapabilityPermissions(capVoiceMove); got != discord.MustPermissions("ViewChannel", "Connect", "MoveMembers") {
		t.Errorf("voice.move permissions = %v", got.Names())
	}
}
