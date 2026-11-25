package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

func memberJoin(id string) discord.MemberJoinEvent {
	return discord.MemberJoinEvent{Member: discord.Member{ServerID: "10", User: discord.User{ID: id, Username: "u" + id}}}
}

func contentOf(r discord.InteractionResponse) string {
	if r.Data == nil {
		return ""
	}
	return r.Data.Content
}

func TestTypedCommandOptions(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    opts: command({
      description: "all option types",
      options: {
        s: { type: "string", description: "s", required: true },
        i: { type: "integer", description: "i" },
        n: { type: "number", description: "n" },
        b: { type: "boolean", description: "b" },
        u: { type: "user", description: "u" },
        c: { type: "channel", description: "c" },
        r: { type: "role", description: "r" },
        m: { type: "mentionable", description: "m" },
        a: { type: "attachment", description: "a" },
        missing: { type: "string", description: "not sent" },
      },
      async run(ctx, it) {
        const o = it.options;
        await it.reply(JSON.stringify({
          s: o.s, i: o.i, iType: typeof o.i, n: o.n, b: o.b,
          user: [o.u?.id, o.u?.username, o.u?.displayName, o.u?.mention, o.u?.bot],
          channel: [o.c?.id, o.c?.key, o.c?.name, o.c?.type, o.c?.mention, typeof (o.c as any)?.send],
          role: [o.r?.id, o.r?.key, o.r?.name, o.r?.color, o.r?.mention],
          mentionable: (o.m as any)?.mention,
          attachment: [o.a?.filename, o.a?.size],
          missing: o.missing === undefined && !("missing" in o),
          frozen: Object.isFrozen(o),
          caller: [it.user.id, it.member?.displayName, it.member?.hasRole("member"), it.channel?.key, it.command],
        }));
      },
    }),
  },
});`, nil)
	opts := []discord.InteractionOption{
		{Name: "s", Type: discord.OptionString, Value: "hello"},
		{Name: "i", Type: discord.OptionInteger, Value: float64(42)},
		{Name: "n", Type: discord.OptionNumber, Value: 1.5},
		{Name: "b", Type: discord.OptionBoolean, Value: true},
		{Name: "u", Type: discord.OptionUser, Value: "77"},
		{Name: "c", Type: discord.OptionChannel, Value: "100"},
		{Name: "r", Type: discord.OptionRole, Value: "200"},
		{Name: "m", Type: discord.OptionMentionable, Value: "201"},
		{Name: "a", Type: discord.OptionAttachment, Value: "900"},
	}
	res := discord.Resolved{
		Users:       map[string]discord.User{"77": {ID: "77", Username: "bob", GlobalName: "Bobby", Bot: true}},
		Channels:    map[string]discord.Channel{"100": {ID: "100", ServerID: "10", Name: "general-chat", Type: discord.ChannelText}},
		Roles:       map[string]discord.Role{"200": {ID: "200", Name: "Member", Color: 0x3498db}, "201": {ID: "201", Name: "Moderator"}},
		Attachments: map[string]discord.Attachment{"900": {ID: "900", Filename: "a.png", Size: 12}},
	}
	if err := h.command("opts", opts, res); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(contentOf(h.responses()[0])), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"s": "hello", "i": float64(42), "iType": "number", "n": 1.5, "b": true,
		"user":        []any{"77", "bob", "Bobby", "<@77>", true},
		"channel":     []any{"100", "general", "general-chat", "text", "<#100>", "function"},
		"role":        []any{"200", "member", "Member", "#3498db", "<@&200>"},
		"mentionable": "<@&201>",
		"attachment":  []any{"a.png", float64(12)},
		"missing":     true, "frozen": true,
		"caller": []any{"42", "alice", true, "general", "opts"},
	}
	for k, w := range want {
		if !reflect.DeepEqual(got[k], w) {
			t.Errorf("%s = %#v, want %#v", k, got[k], w)
		}
	}
}

func TestAutoDefer(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
export default definePlugin({
  commands: {
    slow: command({ description: "slow", ephemeral: true, async run(ctx, it) {
      await sleep(300);
      await it.reply("done");
      await it.followUp({ content: "public", ephemeral: false });
    } }),
    slowpublic: command({ description: "slow", async run(ctx, it) {
      await sleep(300);
      await it.reply({ content: "late", ephemeral: true });
    } }),
    fast: command({ description: "fast", async run(ctx, it) { await it.reply("quick"); } }),
    state: command({ description: "state", async run(ctx, it) {
      const before = [it.replied, it.deferred];
      await it.defer();
      const mid = [it.replied, it.deferred];
      await it.reply("x");
      await it.followUp(JSON.stringify({ before, mid, after: [it.replied, it.deferred] }));
    } }),
  },
});`, func(e *Env) { e.Limits.AutoDeferAfter = 50 * time.Millisecond })

	if err := h.command("slow", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	rs := h.responses()
	if len(rs) != 1 || rs[0].Type != discord.ResponseDeferredMessage || rs[0].Data.Flags&discord.MessageFlagEphemeral == 0 {
		t.Fatalf("want one ephemeral deferral, got %+v", rs)
	}
	edits := h.dc.Calls("EditOriginalResponse")
	if len(edits) != 1 || edits[0].Args[2].(discord.MessageCreate).Content != "done" {
		t.Fatalf("reply after auto-defer must edit the original response: %+v", edits)
	}
	fu := h.dc.Calls("CreateFollowup")
	if len(fu) != 1 || fu[0].Args[2].(discord.MessageCreate).Flags&discord.MessageFlagEphemeral != 0 {
		t.Fatalf("follow-up with ephemeral:false must not be ephemeral: %+v", fu)
	}

	h.dc = resetCalls(h)
	if err := h.command("slowpublic", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	rs = h.responses()
	if len(rs) != 1 || rs[0].Type != discord.ResponseDeferredMessage || rs[0].Data.Flags != 0 {
		t.Fatalf("non-ephemeral command must defer publicly: %+v", rs)
	}

	h.dc = resetCalls(h)
	if err := h.command("fast", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // the auto-defer timer must have been stopped
	rs = h.responses()
	if len(rs) != 1 || rs[0].Type != discord.ResponseMessage || contentOf(rs[0]) != "quick" || rs[0].Data.Flags != 0 {
		t.Fatalf("fast reply: %+v", rs)
	}

	h.dc = resetCalls(h)
	if err := h.command("state", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	fu = h.dc.Calls("CreateFollowup")
	if got := fu[0].Args[2].(discord.MessageCreate).Content; got != `{"before":[false,false],"mid":[false,true],"after":[true,true]}` {
		t.Fatalf("replied/deferred = %s", got)
	}
}

// resetCalls clears the recorded calls (the instance keeps its client).
func resetCalls(h *harness) *fakeDiscord {
	h.dc.mu.Lock()
	h.dc.calls = nil
	h.dc.mu.Unlock()
	return h.dc
}

func TestInteractionErrors(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
export default definePlugin({
  commands: {
    boom: command({ description: "boom", async run() { throw new Error("kaboom"); } }),
    deferboom: command({ description: "x", async run(ctx, it) { await it.defer(); throw new Error("late"); } }),
    twice: command({ description: "x", async run(ctx, it) {
      await it.reply("one");
      try { await it.reply("two"); } catch (e) { await it.followUp("caught: " + (e as Error).message); }
    } }),
    hang: command({ description: "x", async run() { await sleep(5000); } }),
  },
});`, func(e *Env) {
		e.Limits.AutoDeferAfter = 40 * time.Millisecond
		e.Limits.HandlerTimeout = 200 * time.Millisecond
	})

	err := h.command("boom", nil, discord.Resolved{})
	var se *ScriptError
	if !errors.As(err, &se) || se.Message != "kaboom" || !strings.Contains(err.Error(), "src/index.ts:6") {
		t.Fatalf("got %v", err)
	}
	rs := h.responses()
	if len(rs) != 1 || contentOf(rs[0]) != GenericErrorMessage || rs[0].Data.Flags&discord.MessageFlagEphemeral == 0 {
		t.Fatalf("generic ephemeral error expected: %+v", rs)
	}
	if e := h.logged("plugin handler failed"); !strings.Contains(e.Attrs["error"].(string), "kaboom") {
		t.Fatalf("log = %v", e.Attrs)
	}

	resetCalls(h)
	if err := h.command("deferboom", nil, discord.Resolved{}); err == nil {
		t.Fatal("expected an error")
	}
	if edits := h.dc.Calls("EditOriginalResponse"); len(edits) != 1 || edits[0].Args[2].(discord.MessageCreate).Content != GenericErrorMessage {
		t.Fatalf("deferred error must edit the original response: %+v", edits)
	}

	resetCalls(h)
	if err := h.command("twice", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if fu := h.dc.Calls("CreateFollowup"); len(fu) != 1 || !strings.Contains(fu[0].Args[2].(discord.MessageCreate).Content, "already replied") {
		t.Fatalf("second reply must fail: %+v", fu)
	}

	resetCalls(h)
	start := time.Now()
	err = h.command("hang", nil, discord.Resolved{})
	if !errors.Is(err, ErrHandlerTimeout) {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("timeout took %s", d)
	}
	rs = h.responses()
	if len(rs) != 1 || rs[0].Type != discord.ResponseDeferredMessage {
		t.Fatalf("hang should have been auto-deferred: %+v", rs)
	}
	if edits := h.dc.Calls("EditOriginalResponse"); len(edits) != 1 || edits[0].Args[2].(discord.MessageCreate).Content != GenericErrorMessage {
		t.Fatalf("timeout must edit the deferred response: %+v", edits)
	}
}

func TestCapabilityDenied(t *testing.T) {
	h := start(t, `
import { CapabilityError, command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: {
    async memberJoin(ctx, member) {
      await member.addRole("member"); // roles.assign not granted: uncaught
    },
  },
  commands: {
    probe: command({ description: "x", async run(ctx, it) {
      const out: string[] = [];
      try { ctx.server.channel("general"); } catch (e) {
        out.push(String(e instanceof CapabilityError) + ":" + (e as CapabilityError).capability + ":" + (e as Error).name);
      }
      try { await ctx.kv.get("x"); } catch (e) { out.push((e as CapabilityError).capability); }
      try { await ctx.http.fetch("https://example.com"); } catch (e) { out.push((e as CapabilityError).capability); }
      const ch = ctx.server.role("member"); // no capability needed
      out.push(ch.name);
      await it.reply(out.join("|")); // interactions need no capability
    } }),
  },
});`, func(e *Env) { e.Grants = []string{capMessagesSend} })

	if err := h.command("probe", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != "true:discord.channels.read:CapabilityError|storage.kv|http.fetch|Member" {
		t.Fatalf("got %q", got)
	}
	err := h.inst.HandleEvent(context.Background(), memberJoin("42"))
	var ce *CapabilityError
	if !errors.As(err, &ce) || ce.Capability != capRolesAssign {
		t.Fatalf("got %v", err)
	}
	if len(h.dc.Calls("AddMemberRole")) != 0 {
		t.Fatal("the call must not reach Discord")
	}
	// Host-side entry points check grants too.
	if _, err := h.inst.HandleRoute(context.Background(), "GET", "/", RouteRequest{}); !errors.As(err, &ce) || ce.Capability != capPortalRoutes {
		t.Fatalf("HandleRoute: %v", err)
	}
	if err := h.inst.RunTask(context.Background(), "x", nil); !errors.As(err, &ce) || ce.Capability != capScheduler {
		t.Fatalf("RunTask: %v", err)
	}
	if err := h.inst.Deliver(context.Background(), "a.b", nil); !errors.As(err, &ce) || ce.Capability != capEventsSubscribe {
		t.Fatalf("Deliver: %v", err)
	}
}

func TestSyncBudgetInterruptAndRecovery(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
let n = 0;
export default definePlugin({
  events: {
    memberJoin() { while (true) { n++; } },
    async memberLeave(ctx) {
      await ctx.kv.get("x");
      for (;;) {} // runaway after an await
    },
  },
  commands: {
    ok: command({ description: "x", async run(ctx, it) { await it.reply("still alive " + (n > 0)); } }),
  },
});`, func(e *Env) { e.Limits.SyncBudget = 30 * time.Millisecond })

	start := time.Now()
	err := h.inst.HandleEvent(context.Background(), memberJoin("42"))
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("interrupt took %s", d)
	}
	err = h.inst.HandleEvent(context.Background(), discord.MemberLeaveEvent{ServerID: "10", User: discord.User{ID: "42"}})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("after await: got %v", err)
	}
	// The VM recovers and keeps serving.
	if err := h.command("ok", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != "still alive true" {
		t.Fatalf("got %q", got)
	}
}

func TestHandlerTimeoutCutsLaterEffects(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
export default definePlugin({
  events: {
    async memberJoin(ctx) {
      await sleep(400);
      await ctx.server.channel("general").send("too late");
    },
    async memberLeave(ctx) {
      await ctx.server.channel("general").send("blocked"); // the fake blocks until cancelled
    },
  },
});`, func(e *Env) { e.Limits.HandlerTimeout = 100 * time.Millisecond })

	err := h.inst.HandleEvent(context.Background(), memberJoin("42"))
	if !errors.Is(err, ErrHandlerTimeout) {
		t.Fatalf("got %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := len(h.dc.Calls("SendMessage")); n != 0 {
		t.Fatalf("a timed-out handler must not send messages (got %d)", n)
	}

	cancelled := make(chan error, 1)
	h.dc.hook = func(ctx context.Context, name string, _ []any) error {
		if name == "SendMessage" {
			<-ctx.Done()
			cancelled <- ctx.Err()
			return ctx.Err()
		}
		return nil
	}
	err = h.inst.HandleEvent(context.Background(), discord.MemberLeaveEvent{ServerID: "10", User: discord.User{ID: "42"}})
	if !errors.Is(err, ErrHandlerTimeout) {
		t.Fatalf("got %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight host call was not cancelled")
	}

	// Caller cancellation also ends the handler.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := h.inst.HandleEvent(ctx, memberJoin("43")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestHandlersInterleaveAtAwaits(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
export default definePlugin({
  events: {
    async memberJoin(ctx, m) {
      ctx.log.info("start", { id: m.id });
      await sleep(m.id === "1" ? 150 : 10);
      ctx.log.info("end", { id: m.id });
    },
  },
});`, nil)
	a := h.inst.DispatchEvent(context.Background(), memberJoin("1"))
	b := h.inst.DispatchEvent(context.Background(), memberJoin("2"))
	if err := <-a; err != nil {
		t.Fatal(err)
	}
	if err := <-b; err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, e := range *h.logs.entries {
		if e.Msg == "start" || e.Msg == "end" {
			seq = append(seq, e.Msg+e.Attrs["id"].(string))
		}
	}
	if !reflect.DeepEqual(seq, []string{"start1", "start2", "end2", "end1"}) {
		t.Fatalf("sequence = %v", seq)
	}
}

func TestComponentsAndModals(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    poll: command({ description: "x", async run(ctx, it) {
      await it.reply({
        content: "Vote!",
        embeds: [{ title: "Poll", color: "#ff0000", timestamp: new Date(0), fields: [{ name: "a", value: "b", inline: true }] }],
        components: [
          { type: "row", components: [
            { type: "button", id: "vote:42", label: "Yes", style: "success" },
            { type: "link", url: "https://example.com", label: "Docs" },
          ] },
          { type: "row", components: [{ type: "select", id: "pick", options: [{ value: "a", label: "A" }], maxValues: 1 }] },
        ],
        allowedMentions: { users: true },
      });
    } }),
    ask: command({ description: "x", async run(ctx, it) {
      await it.showModal({ id: "feedback", title: "Feedback", fields: [
        { id: "text", label: "Your feedback", style: "paragraph", maxLength: 500 },
        { id: "mail", label: "Email", required: false },
      ] });
    } }),
  },
  components: {
    async vote(ctx, it) {
      await it.update({ content: "voted " + it.componentId + " on " + it.message.id });
      await it.followUp({ content: "thanks", ephemeral: true });
    },
    async pick(ctx, it) { ctx.log.info("picked", { values: [...it.values] }); },
  },
  modals: {
    async feedback(ctx, it) { await it.reply({ content: it.modalId + ":" + it.fields.text, ephemeral: true }); },
  },
});`, nil)

	if err := h.command("poll", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	mc := h.responses()[0].Data
	if mc.Components[0].Components[0].CustomID != "c:inst:vote:42" || mc.Components[1].Components[0].CustomID != "c:inst:pick" {
		t.Fatalf("custom ids not namespaced: %+v", mc.Components)
	}
	if mc.Components[0].Components[0].Style != discord.ButtonSuccess || mc.Components[0].Components[1].Style != discord.ButtonLink {
		t.Fatalf("styles: %+v", mc.Components[0])
	}
	if e := mc.Embeds[0]; e.Color != 0xff0000 || e.Timestamp == nil || !e.Timestamp.Equal(time.Unix(0, 0)) || !e.Fields[0].Inline {
		t.Fatalf("embed: %+v", e)
	}
	if mc.AllowedMentions == nil || !mc.AllowedMentions.Users || mc.AllowedMentions.Roles {
		t.Fatalf("allowed mentions: %+v", mc.AllowedMentions)
	}

	// Route the click back as the bot host would.
	inst, local, ok := ParseCustomID(mc.Components[0].Components[0].CustomID)
	if !ok || inst != "inst" {
		t.Fatal("ParseCustomID")
	}
	click := discord.Interaction{ID: "c1", Token: "t", Type: discord.InteractionComponent, ServerID: "10", ChannelID: "100",
		User: discord.User{ID: "42"}, CustomID: "c:inst:vote:42", Message: &discord.Message{ID: "555", ChannelID: "100", ServerID: "10"}}
	resetCalls(h)
	if err := h.inst.HandleInteraction(context.Background(), click, "component", local); err != nil {
		t.Fatal(err)
	}
	rs := h.responses()
	if len(rs) != 1 || rs[0].Type != discord.ResponseUpdateMessage || contentOf(rs[0]) != "voted vote:42 on 555" {
		t.Fatalf("update: %+v", rs)
	}
	if fu := h.dc.Calls("CreateFollowup"); len(fu) != 1 || fu[0].Args[2].(discord.MessageCreate).Flags != discord.MessageFlagEphemeral {
		t.Fatalf("follow-up: %+v", fu)
	}

	// A component handler that does not answer is acknowledged silently.
	resetCalls(h)
	sel := click
	sel.ID, sel.Values = "c2", []string{"a"}
	if err := h.inst.HandleInteraction(context.Background(), sel, "component", "pick"); err != nil {
		t.Fatal(err)
	}
	if rs := h.responses(); len(rs) != 1 || rs[0].Type != discord.ResponseDeferredUpdate {
		t.Fatalf("silent ack: %+v", rs)
	}
	if e := h.logged("picked"); !reflect.DeepEqual(e.Attrs["values"], []any{"a"}) {
		t.Fatalf("values = %v", e.Attrs)
	}

	// Unknown component: generic error.
	resetCalls(h)
	sel.ID = "c3"
	if err := h.inst.HandleInteraction(context.Background(), sel, "component", "gone"); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("got %v", err)
	}

	// Modal
	resetCalls(h)
	if err := h.command("ask", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	m := h.responses()[0]
	if m.Type != discord.ResponseModal || m.Modal.CustomID != "c:inst:feedback" || len(m.Modal.Components) != 2 {
		t.Fatalf("modal: %+v", m)
	}
	in := m.Modal.Components[0].Components[0]
	if in.CustomID != "text" || in.TextStyle != 2 || !in.Required || *in.MaxLength != 500 || m.Modal.Components[1].Components[0].Required {
		t.Fatalf("text inputs: %+v", m.Modal.Components)
	}
	resetCalls(h)
	submit := discord.Interaction{ID: "m1", Token: "t", Type: discord.InteractionModalSubmit, ServerID: "10", ChannelID: "100",
		User: discord.User{ID: "42"}, CustomID: "c:inst:feedback", ModalFields: map[string]string{"text": "great", "mail": ""}}
	if err := h.inst.HandleInteraction(context.Background(), submit, "modal", "feedback"); err != nil {
		t.Fatal(err)
	}
	if r := h.responses()[0]; contentOf(r) != "feedback:great" || r.Data.Flags != discord.MessageFlagEphemeral {
		t.Fatalf("modal reply: %+v", r)
	}
}

func TestMessageInputValidation(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    bad: command({ description: "x", async run(ctx, it) {
      const errs: string[] = [];
      const ch = ctx.server.channel("general");
      const tries: any[] = [
        { content: "" },
        { content: "x".repeat(2001) },
        { content: "x", ephemeral: true },
        { embeds: [{ color: "red" }] },
        { components: [{ type: "row", components: [{ type: "button", label: "no id" }] }] },
        { components: [{ type: "row", components: [{ type: "button", id: "x".repeat(200), label: "long" }] }] },
        { components: [{ type: "row", components: [{ type: "select", id: "s", options: [] }] }] },
      ];
      for (const m of tries) {
        try { await ch.send(m); errs.push("sent!"); } catch (e) { errs.push((e as Error).name + ": " + (e as Error).message); }
      }
      await it.reply({ content: errs.join("\n") });
    } }),
  },
});`, nil)
	if err := h.command("bad", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(contentOf(h.responses()[0]), "\n")
	want := []string{
		"message is empty", "max 2000", "ephemeral is only supported in interaction replies",
		"invalid color", "component id must not be empty", "too long", "1 to 25 options",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for k, w := range want {
		if !strings.HasPrefix(got[k], "TypeError: ") || !strings.Contains(got[k], w) {
			t.Errorf("%d: %q does not contain %q", k, got[k], w)
		}
	}
	if len(h.dc.Calls("SendMessage")) != 0 {
		t.Fatal("invalid messages must not be sent")
	}
}

func TestEntitiesAndServer(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: {
    async messageCreate(ctx, msg) {
      await msg.reply("pong " + msg.content + " from " + msg.author.username + " in " + msg.channel.key);
      await msg.react("👍");
      await msg.edit("edited");
      await msg.delete("cleanup");
      const t = await msg.channel.createThread("talk", { fromMessage: msg.id });
      await t.send("in thread " + t.type);
      const hist = await msg.channel.messages({ limit: 2 });
      ctx.log.info("history", { ids: hist.map((m) => m.id), created: msg.createdAt.toISOString() });
    },
    async reactionAdd(ctx, r) {
      ctx.log.info("reaction", { emoji: r.emoji, user: r.user.id, message: r.message.id, channel: r.message.channel.key ?? null });
    },
    async memberUpdate(ctx, m, prev) {
      ctx.log.info("update", { nick: m.nickname ?? null, prev: prev?.displayName ?? null, roles: m.roles.map((r) => r.key ?? r.id) });
    },
  },
  commands: {
    mod: command({ description: "x", async run(ctx, it) {
      const m = await ctx.server.member("77");
      const none = await ctx.server.member("78");
      await m!.setNickname("Bobby", "rename");
      await m!.setNickname(null);
      await m!.timeout(60_000, "calm down");
      await m!.removeRole({ id: "201" });
      await m!.addRole("member");
      await m!.kick("bye");
      await m!.ban({ reason: "spam", deleteMessagesSeconds: 3600 });
      const unmanaged = ctx.server.channel({ id: "102" });
      const errs: string[] = [];
      for (const f of [() => ctx.server.channel("nope"), () => ctx.server.channel({ id: "999" }), () => ctx.server.role("nope"),
                       () => ctx.server.channel({ id: 5 } as any)]) {
        try { f(); } catch (e) { errs.push((e as Error).message); }
      }
      const evil = Object.assign(Object.create(Object.getPrototypeOf(unmanaged)), { id: "555" });
      try { await evil.send("x"); } catch (e) { errs.push((e as Error).message); }
      await it.reply(JSON.stringify({
        member: [m!.id, m!.displayName, m!.hasRole("member"), m!.hasRole({ id: "201" }), m!.roles.length, m!.joinedAt.getUTCFullYear()],
        none: none === undefined, unmanaged: [unmanaged.name, unmanaged.key ?? null], errs,
        server: [ctx.server.id, ctx.server.key, ctx.server.name], bot: [ctx.bot.key, ctx.bot.user.id],
        plugin: ctx.plugin, configFrozen: Object.isFrozen(ctx.config), greeting: (ctx.config as any).greeting,
      }));
    } }),
  },
});`, nil)
	h.dc.members["77"] = discord.Member{ServerID: "10", User: discord.User{ID: "77", Username: "bob"}, RoleIDs: []string{"201"},
		JoinedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)}
	h.dc.channels["555"] = discord.Channel{ID: "555", ServerID: "other-server"}

	if err := h.command("mod", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(contentOf(h.responses()[0])), &got); err != nil {
		t.Fatal(err, contentOf(h.responses()[0]))
	}
	want := map[string]any{
		"member":       []any{"77", "bob", true, false, float64(1), float64(2024)},
		"none":         true,
		"unmanaged":    []any{"unmanaged", nil},
		"server":       []any{"10", "my-community", "My Community"},
		"bot":          []any{"moderator", "1"},
		"plugin":       map[string]any{"name": "test", "version": "0.1.0", "instance": "inst"},
		"configFrozen": true, "greeting": "hi",
	}
	for k, w := range want {
		if !reflect.DeepEqual(got[k], w) {
			t.Errorf("%s = %#v, want %#v", k, got[k], w)
		}
	}
	errs := got["errs"].([]any)
	for k, w := range []string{`unknown channel "nope"`, `unknown channel { id: "999" }`, `unknown role "nope"`, "ids must be strings", "does not belong to this server"} {
		if !strings.Contains(errs[k].(string), w) {
			t.Errorf("err %d = %q, want %q", k, errs[k], w)
		}
	}
	upd := h.dc.Calls("UpdateMember")
	if len(upd) != 3 || *upd[0].Args[2].(discord.MemberParams).Nick != "Bobby" || *upd[1].Args[2].(discord.MemberParams).Nick != "" {
		t.Fatalf("UpdateMember: %+v", upd)
	}
	if until := upd[2].Args[2].(discord.MemberParams).TimeoutUntil; until == nil || time.Until(*until) < 50*time.Second {
		t.Fatalf("timeout: %v", until)
	}
	if c := h.dc.Calls("RemoveMemberRole"); len(c) != 1 || c[0].Args[2] != "201" {
		t.Fatalf("RemoveMemberRole: %+v", c)
	}
	if c := h.dc.Calls("BanMember"); len(c) != 1 || c[0].Args[2] != 3600 || c[0].Args[3] != "spam" {
		t.Fatalf("BanMember: %+v", c)
	}
	if len(h.dc.Calls("KickMember")) != 1 {
		t.Fatal("kick")
	}

	// messageCreate
	resetCalls(h)
	msg := discord.Message{ID: "800", ChannelID: "100", ServerID: "10", Content: "ping", Author: discord.User{ID: "1", Username: "cicord"},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := h.inst.HandleEvent(context.Background(), discord.MessageCreateEvent{Message: msg}); err != nil {
		t.Fatal(err)
	}
	sends := h.dc.Calls("SendMessage")
	if len(sends) != 2 || sends[0].Args[1].(discord.MessageCreate).ReplyTo != "800" ||
		sends[0].Args[1].(discord.MessageCreate).Content != "pong ping from cicord in general" {
		t.Fatalf("reply: %+v", sends)
	}
	if !strings.HasPrefix(sends[1].Args[1].(discord.MessageCreate).Content, "in thread thread") {
		t.Fatalf("thread send: %+v", sends[1])
	}
	if c := h.dc.Calls("AddReaction"); len(c) != 1 || c[0].Args[2] != "👍" {
		t.Fatalf("react: %+v", c)
	}
	if len(h.dc.Calls("EditMessage")) != 1 || len(h.dc.Calls("DeleteMessage")) != 1 {
		t.Fatal("edit/delete")
	}
	if e := h.logged("history"); !reflect.DeepEqual(e.Attrs["ids"], []any{"901", "902"}) || e.Attrs["created"] != "2026-01-01T00:00:00.000Z" {
		t.Fatalf("history: %v", e.Attrs)
	}

	// reactionAdd: partial message
	if err := h.inst.HandleEvent(context.Background(), discord.ReactionAddEvent{ReactionEvent: discord.ReactionEvent{
		ServerID: "10", ChannelID: "100", MessageID: "800", UserID: "42", Emoji: "🎉"}}); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("reaction"); e.Attrs["emoji"] != "🎉" || e.Attrs["user"] != "42" || e.Attrs["message"] != "800" || e.Attrs["channel"] != "general" {
		t.Fatalf("reaction: %v", e.Attrs)
	}

	// memberUpdate
	if err := h.inst.HandleEvent(context.Background(), discord.MemberUpdateEvent{
		Member:   discord.Member{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}, Nick: "Al", RoleIDs: []string{"200", "999"}},
		Previous: &discord.Member{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}},
	}); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("update"); e.Attrs["nick"] != "Al" || e.Attrs["prev"] != "alice" || !reflect.DeepEqual(e.Attrs["roles"], []any{"member", "999"}) {
		t.Fatalf("update: %v", e.Attrs)
	}

	// Events of another server are refused.
	if err := h.inst.HandleEvent(context.Background(), discord.MemberJoinEvent{Member: discord.Member{ServerID: "11"}}); err == nil {
		t.Fatal("cross-server event accepted")
	}
}

func TestOwnedChannels(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    ticket: command({ description: "x", async run(ctx, it) {
      const ch = await ctx.server.createChannel({ name: "ticket-1", category: "support", topic: "help",
        overwrites: { "@everyone": { deny: ["ViewChannel"] }, ["member:" + it.user.id]: { allow: ["ViewChannel", "SendMessages"] }, moderator: { allow: ["ViewChannel"] } } });
      await ch.send("welcome to your ticket");
      let err = "";
      try { await ctx.server.deleteChannel("general"); } catch (e) { err = (e as Error).message; }
      await ctx.server.deleteChannel({ id: ch.id }, "closed");
      await it.reply(ch.id + "|" + err);
    } }),
  },
});`, nil)
	if err := h.command("ticket", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	cc := h.dc.Calls("CreateChannel")
	if len(cc) != 1 {
		t.Fatal("CreateChannel not called")
	}
	p := cc[0].Args[1].(discord.ChannelParams)
	if *p.Name != "ticket-1" || *p.ParentID != "300" || *p.Topic != "help" || p.Type != discord.ChannelText {
		t.Fatalf("params: %+v", p)
	}
	ows := *p.Overwrites
	wantOws := []discord.Overwrite{
		{ID: "10", Type: discord.OverwriteRole, Deny: discord.MustPermissions("ViewChannel")},
		{ID: "42", Type: discord.OverwriteMember, Allow: discord.MustPermissions("ViewChannel", "SendMessages")},
		{ID: "201", Type: discord.OverwriteRole, Allow: discord.MustPermissions("ViewChannel")},
	}
	if !reflect.DeepEqual(ows, wantOws) {
		t.Fatalf("overwrites: %+v", ows)
	}
	parts := strings.Split(contentOf(h.responses()[0]), "|")
	if !strings.Contains(parts[1], "not owned by this plugin instance") {
		t.Fatalf("deleting a managed channel must fail: %q", parts[1])
	}
	if d := h.dc.Calls("DeleteChannel"); len(d) != 1 || d[0].Args[0] != parts[0] {
		t.Fatalf("DeleteChannel: %+v", d)
	}
	if list, _ := h.resources.List(context.Background(), "my-community"); len(list) != 0 {
		t.Fatalf("ownership record not removed: %+v", list)
	}
	if s := h.dc.Calls("SendMessage"); len(s) != 1 || s[0].Args[0] != parts[0] {
		t.Fatalf("send in owned channel: %+v", s)
	}
}

func TestOwnedChannelIsRecorded(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: { async memberJoin(ctx) { await ctx.server.createChannel({ name: "x", type: "voice" }); } },
});`, nil)
	if err := h.inst.HandleEvent(context.Background(), memberJoin("1")); err != nil {
		t.Fatal(err)
	}
	list, _ := h.resources.List(context.Background(), "my-community")
	if len(list) != 1 || list[0].Owner != "plugin:moderator/inst" || list[0].Kind != state.KindChannel ||
		list[0].Key != "moderator/inst/"+list[0].ID {
		t.Fatalf("resources: %+v", list)
	}
}

func TestKVSchedulerEventsPortal(t *testing.T) {
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    kv: command({ description: "x", async run(ctx, it) {
      await ctx.kv.set("user:1", { name: "a", tags: ["x"] });
      await ctx.kv.set("user:2", null);
      await ctx.kv.set("user:3", 3, { ttlSeconds: 60 });
      await ctx.kv.set("other", true);
      const n1 = await ctx.kv.increment("count");
      const n2 = await ctx.kv.increment("count", 5);
      const page1 = await ctx.kv.list({ prefix: "user:", limit: 2 });
      const page2 = await ctx.kv.list({ prefix: "user:", limit: 2, cursor: page1.cursor });
      await ctx.kv.delete("other");
      let err = "";
      try { await ctx.kv.set("bad", undefined as any); } catch (e) { err = (e as Error).message; }
      await it.reply(JSON.stringify({
        get: await ctx.kv.get("user:1"), nul: await ctx.kv.get("user:2"), missing: (await ctx.kv.get("nope")) === undefined,
        other: (await ctx.kv.get("other")) === undefined, n1, n2, page1, page2, err,
      }));
    } }),
    later: command({ description: "x", async run(ctx, it) {
      const { id } = await ctx.schedule.after(60_000, "remind", { who: it.user.id });
      const t2 = await ctx.schedule.after(1000, "remind");
      await ctx.schedule.cancel(t2.id);
      let err = "";
      try { await ctx.schedule.after(1, "nope"); } catch (e) { err = (e as Error).message; }
      await ctx.events.publish("purchase", { item: "sword" });
      const url = await ctx.portal.link("/profile", { member: it.member, expiresInSeconds: 120 });
      await it.reply(id + "|" + err + "|" + url);
    } }),
  },
  tasks: {
    async remind(ctx, payload) { ctx.log.info("remind", { payload: payload ?? null }); },
  },
  schedules: { daily: { cron: "@daily", async run(ctx) { ctx.log.info("daily"); } } },
  subscriptions: { "economy.purchase": async (ctx, payload) => { ctx.log.info("delivered", { payload }); } },
});`, nil)

	if err := h.command("kv", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(contentOf(h.responses()[0])), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"get": map[string]any{"name": "a", "tags": []any{"x"}}, "nul": nil, "missing": true, "other": true,
		"n1": float64(1), "n2": float64(6),
		"page1": map[string]any{"items": []any{map[string]any{"key": "user:1", "value": map[string]any{"name": "a", "tags": []any{"x"}}},
			map[string]any{"key": "user:2", "value": nil}}, "cursor": "user:2"},
		"page2": map[string]any{"items": []any{map[string]any{"key": "user:3", "value": float64(3)}}},
		"err":   "kv.set: value must be JSON (got undefined)",
	}
	for k, w := range want {
		if !reflect.DeepEqual(got[k], w) {
			t.Errorf("%s = %#v, want %#v", k, got[k], w)
		}
	}

	resetCalls(h)
	if err := h.command("later", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(contentOf(h.responses()[0]), "|")
	tasks := h.tasks.All()
	if len(tasks) != 1 || tasks[0].ID != parts[0] || tasks[0].Scope != "plugin:moderator/inst/my-community" || tasks[0].Name != "remind" ||
		string(tasks[0].Payload) != `{"who":"42"}` || time.Until(tasks[0].RunAt) < 50*time.Second {
		t.Fatalf("tasks: %+v", tasks)
	}
	if !strings.Contains(parts[1], `unknown task "nope"`) {
		t.Fatalf("unknown task: %q", parts[1])
	}
	if parts[2] != "https://portal.example.com/profile?member=42&ttl=2m0s" {
		t.Fatalf("portal link: %q", parts[2])
	}
	select {
	case ev := <-h.published:
		if ev.Topic != "test.purchase" || ev.Payload != `{"item":"sword"}` {
			t.Fatalf("published: %+v", ev)
		}
	default:
		t.Fatal("nothing published")
	}

	// The bot host runs due tasks, schedules and deliveries.
	if err := h.inst.RunTask(context.Background(), "remind", tasks[0].Payload); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("remind"); !reflect.DeepEqual(e.Attrs["payload"], map[string]any{"who": "42"}) {
		t.Fatalf("task payload: %v", e.Attrs)
	}
	if err := h.inst.RunTask(context.Background(), "remind", nil); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("remind"); e.Attrs["payload"] != nil {
		t.Fatalf("empty payload: %v", e.Attrs)
	}
	if err := h.inst.RunSchedule(context.Background(), "daily"); err != nil {
		t.Fatal(err)
	}
	h.logged("daily")
	if err := h.inst.RunSchedule(context.Background(), "weekly"); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("unknown schedule: %v", err)
	}
	if err := h.inst.Deliver(context.Background(), "economy.purchase", json.RawMessage(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("delivered"); !reflect.DeepEqual(e.Attrs["payload"], map[string]any{"n": float64(1)}) {
		t.Fatalf("delivered: %v", e.Attrs)
	}
}

func TestRoutesWithParams(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  routes: {
    "GET /answers/:id": { access: "member", handler: (ctx, req) => ({ id: req.params.id, q: req.query.sort ?? null, viewer: req.viewer?.user.id ?? null }) },
    "GET /answers/top": { access: "public", handler: () => [1, 2, 3] },
    "POST /answers/:id": { access: "member", async handler(ctx, req) {
      return { status: 201, body: { created: req.params.id, body: req.body }, headers: { "x-cicord": "1" } };
    } },
    "DELETE /answers/:id": { access: "admin", handler() {} },
    "GET /fail": { access: "public", handler() { throw new Error("nope"); } },
  },
});`, nil)
	ctx := context.Background()
	viewer := &RouteViewer{User: discord.User{ID: "42", Username: "alice"}, Member: &discord.Member{User: discord.User{ID: "42"}}}

	r, err := h.inst.HandleRoute(ctx, "GET", "/answers/7", RouteRequest{Query: map[string]string{"sort": "new"}, Viewer: viewer})
	if err != nil || r.Status != 200 || string(r.Body) != `{"id":"7","q":"new","viewer":"42"}` {
		t.Fatalf("GET param: %+v %v", r, err)
	}
	r, err = h.inst.HandleRoute(ctx, "GET", "/answers/top", RouteRequest{})
	if err != nil || r.Status != 200 || string(r.Body) != `[1,2,3]` {
		t.Fatalf("GET static: %+v %v", r, err)
	}
	r, err = h.inst.HandleRoute(ctx, "POST", "/answers/9", RouteRequest{Body: json.RawMessage(`{"text":"hi"}`)})
	if err != nil || r.Status != 201 || string(r.Body) != `{"created":"9","body":{"text":"hi"}}` || r.Headers["x-cicord"] != "1" {
		t.Fatalf("POST: %+v %v", r, err)
	}
	r, err = h.inst.HandleRoute(ctx, "DELETE", "/answers/9", RouteRequest{})
	if err != nil || r.Status != 204 || r.Body != nil {
		t.Fatalf("DELETE: %+v %v", r, err)
	}
	if _, err := h.inst.HandleRoute(ctx, "GET", "/nothing", RouteRequest{}); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("unknown route: %v", err)
	}
	if _, err := h.inst.HandleRoute(ctx, "GET", "/fail", RouteRequest{}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("failing route: %v", err)
	}
	route, _, _ := h.inst.Definition().MatchRoute("DELETE", "/answers/1")
	if route.Access != "admin" {
		t.Fatalf("access = %q", route.Access)
	}
}

func TestLifecycle(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
let started = false;
export default definePlugin({
  async onStart(ctx) { await ctx.kv.set("started", true); started = true; },
  async onStop(ctx) { await ctx.kv.set("stopped", started); },
  events: { memberJoin(ctx) { ctx.log.info("join"); } },
});`, nil)
	if v, _ := h.kv.Get(context.Background(), "started"); string(v) != "true" {
		t.Fatalf("onStart did not run: %s", v)
	}
	if err := h.inst.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.kv.Get(context.Background(), "stopped"); string(v) != "true" {
		t.Fatalf("onStop did not run: %s", v)
	}
	if err := h.inst.HandleEvent(context.Background(), memberJoin("1")); !errors.Is(err, ErrStopped) {
		t.Fatalf("after stop: %v", err)
	}
	if err := h.inst.Stop(context.Background()); err != nil {
		t.Fatal("Stop must be idempotent")
	}
}

func TestStartErrors(t *testing.T) {
	code := bundleSource(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({ async onStart() { throw new Error("cannot start"); } });`)
	def, err := Inspect(code)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := NewInstance(code, def, testEnv(newFakeDiscord()))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Stop(context.Background())
	if err := inst.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot start") {
		t.Fatalf("got %v", err)
	}

	// nil definition: extracted at Start.
	code = bundleSource(t, `import { definePlugin } from "@cicord/sdk"; export default definePlugin({ events: { memberLeave() {} } });`)
	inst2, err := NewInstance(code, nil, testEnv(newFakeDiscord()))
	if err != nil {
		t.Fatal(err)
	}
	defer inst2.Stop(context.Background())
	if err := inst2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := inst2.Definition(); d == nil || !slices.Equal(d.Events, []string{"memberLeave"}) {
		t.Fatalf("definition = %+v", d)
	}
	if err := inst2.HandleEvent(context.Background(), memberJoin("1")); err != nil {
		t.Fatalf("events without handler are ignored: %v", err)
	}

	if _, err := NewInstance(code, nil, Env{InstanceName: "Bad Name"}); err == nil {
		t.Fatal("invalid instance name accepted")
	}
}

func TestUnhandledRejectionAndTimersAndConsole(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: {
    async memberJoin(ctx) {
      ctx.server.channel("general").send(""); // not awaited: unhandled rejection
      console.log("hello", { a: 1 });
      let ticks = 0;
      await new Promise<void>((resolve) => {
        const id = setInterval(() => { if (++ticks === 3) { clearInterval(id); resolve(); } }, 5);
      });
      const t = setTimeout(() => ctx.log.info("never"), 5);
      clearTimeout(t);
      queueMicrotask(() => ctx.log.info("micro"));
      ctx.log.info("ticks", { ticks });
    },
  },
});`, nil)
	if err := h.inst.HandleEvent(context.Background(), memberJoin("1")); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("ticks"); e.Attrs["ticks"] != float64(3) {
		t.Fatalf("ticks = %v", e.Attrs)
	}
	h.logged("micro")
	if e := h.logged(`hello {"a":1}`); e.Level.String() != "INFO" {
		t.Fatal(e)
	}
	time.Sleep(30 * time.Millisecond)
	if len(h.logs.Find("never")) != 0 {
		t.Fatal("cleared timeout fired")
	}
	if e := h.logged("unhandled promise rejection in plugin code"); !strings.Contains(e.Attrs["error"].(string), "message is empty") {
		t.Fatalf("rejection log: %v", e.Attrs)
	}
}

// TestMessageContentGating: a plugin without the MessageContent intent never sees message
// content, even when another plugin of the same bot declares it (intents are unioned per
// bot), except for its own messages, messages mentioning the bot and DMs.
func TestMessageContentGating(t *testing.T) {
	h := start(t, `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: {
    async messageCreate(ctx, msg) {
      const e = msg.embeds[0];
      ctx.log.info("seen", { content: msg.content, attachments: msg.attachments.length, embeds: msg.embeds.length,
        embed: e ? [e.description, e.footer?.text, e.color, e.fields?.[0]?.value].join("|") : "" });
    },
  },
});`, func(e *Env) { e.MessageContent = false })
	cases := []struct {
		name, content, author string
		want                  string
	}{
		{"other author", "secret", "42", ""},
		{"mention", "hey <@1> look", "42", "hey <@1> look"},
		{"nick mention", "hey <@!1>", "42", "hey <@!1>"},
		{"own message", "mine", "1", "mine"},
	}
	for k, c := range cases {
		msg := discord.Message{ID: fmt.Sprint(900 + k), ChannelID: "100", ServerID: "10", Content: c.content,
			Author:      discord.User{ID: c.author, Username: "u"},
			Attachments: []discord.Attachment{{ID: "5", Filename: "a.png", URL: "https://cdn/a.png", Size: 1}},
			Embeds: []discord.Embed{{Description: "reply", Color: 0x5865f2, Footer: &discord.EmbedFooter{Text: "— Ann (via portal)"},
				Fields: []discord.EmbedField{{Name: "n", Value: "v"}}}}}
		if err := h.inst.HandleEvent(context.Background(), discord.MessageCreateEvent{Message: msg}); err != nil {
			t.Fatal(err)
		}
		seen := h.logs.Find("seen")
		if len(seen) != k+1 {
			t.Fatalf("%s: %d logs", c.name, len(seen))
		}
		e := seen[k]
		if fmt.Sprint(e.Attrs["content"]) != c.want {
			t.Errorf("%s: content = %q, want %q", c.name, e.Attrs["content"], c.want)
		}
		wantAtt := "1"
		if c.want == "" {
			wantAtt = "0"
		}
		if fmt.Sprint(e.Attrs["attachments"]) != wantAtt {
			t.Errorf("%s: attachments = %v, want %s", c.name, e.Attrs["attachments"], wantAtt)
		}
		// Embeds are gated like the content, and keep the shape plugins send.
		wantEmbed := "reply|— Ann (via portal)|#5865f2|v"
		if c.want == "" {
			wantEmbed = ""
		}
		if fmt.Sprint(e.Attrs["embeds"]) != wantAtt || fmt.Sprint(e.Attrs["embed"]) != wantEmbed {
			t.Errorf("%s: embeds = %v %q, want %s %q", c.name, e.Attrs["embeds"], e.Attrs["embed"], wantAtt, wantEmbed)
		}
	}
}
