package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

const subcommandPlugin = `
import { command, definePlugin, subcommand } from "@cicord/sdk";
const fruits = ["apple", "apricot", "banana", "cherry"];
export default definePlugin({
  commands: {
    fruit: command({
      description: "Fruits",
      ephemeral: true,
      subcommands: {
        pick: subcommand({
          description: "Pick a fruit",
          options: {
            name: {
              type: "string", description: "Fruit", required: true, minLength: 2, maxLength: 20,
              autocomplete(ctx, it, focused) {
                return fruits.filter((f) => f.startsWith(focused.value))
                  .map((f) => ({ name: f + " (" + it.subcommand + "/" + (it.options.count ?? "-") + ")", value: f }));
              },
            },
            count: { type: "integer", description: "How many", autocomplete: () => [{ name: "one", value: 1 }, { name: "bad", value: 1.5 }] },
            slow: { type: "string", description: "Slow", autocomplete: () => new Promise(() => {}) },
            boom: { type: "string", description: "Boom", autocomplete() { throw new Error("no fruit") } },
            many: { type: "string", description: "Many", autocomplete: () => Array.from({ length: 26 }, (_, i) => ({ name: "n" + i, value: "v" + i })) },
          },
          async run(ctx, it) {
            await it.reply(JSON.stringify({ command: it.command, group: it.group ?? null, subcommand: it.subcommand, name: it.options.name }));
          },
        }),
      },
      groups: {
        admin: {
          description: "Admin",
          subcommands: {
            purge: subcommand({ description: "Purge", async run(ctx, it) { await it.reply(it.group + " " + it.subcommand) } }),
          },
        },
      },
    }),
    plain: command({ description: "Plain", run(ctx, it) { return it.reply("plain " + (it.subcommand ?? "none")) } }),
  },
});`

func subcommandOpts(path []string, leaf ...discord.InteractionOption) []discord.InteractionOption {
	if len(path) == 0 {
		return leaf
	}
	typ := discord.OptionSubcommand
	if len(path) == 2 {
		typ = discord.OptionSubcommandGroup
	}
	return []discord.InteractionOption{{Name: path[0], Type: typ, Options: subcommandOpts(path[1:], leaf...)}}
}

func TestInspectSubcommands(t *testing.T) {
	d, err := Inspect(bundleSource(t, subcommandPlugin))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := d.Command("fruit")
	if !ok || !c.HasSubcommands() || !c.Ephemeral {
		t.Fatalf("command = %+v", c)
	}
	if got := c.Leaves(); !reflect.DeepEqual(got, []string{"fruit pick", "fruit admin purge"}) {
		t.Errorf("leaves = %v", got)
	}
	two, twenty := 2, 20
	pick := c.Options[0]
	if pick.Type != discord.OptionSubcommand || pick.Name != "pick" || len(pick.Options) != 5 {
		t.Fatalf("pick = %+v", pick)
	}
	if name := pick.Options[0]; name.Name != "name" || !name.Required || !name.Autocomplete || !reflect.DeepEqual(name.MinLength, &two) || !reflect.DeepEqual(name.MaxLength, &twenty) {
		t.Errorf("name option = %+v", name)
	}
	if g := c.Options[1]; g.Type != discord.OptionSubcommandGroup || g.Name != "admin" || len(g.Options) != 1 || g.Options[0].Name != "purge" {
		t.Errorf("group = %+v", g)
	}
	if leaf, ok := c.Leaf([]string{"fruit", "admin", "purge"}); !ok || len(leaf) != 0 {
		t.Errorf("leaf = %v %v", leaf, ok)
	}
	for _, bad := range [][]string{{"fruit"}, {"fruit", "admin"}, {"fruit", "nope"}, {"plain"}} {
		if _, ok := c.Leaf(bad); ok {
			t.Errorf("Leaf(%v) ok", bad)
		}
	}
	// The command goes to Discord nested.
	dc := c.DiscordCommand()
	if len(dc.Options) != 2 || len(dc.Options[1].Options) != 1 {
		t.Errorf("discord command = %+v", dc)
	}
	p, _ := d.Command("plain")
	if p.HasSubcommands() || !reflect.DeepEqual(p.Leaves(), []string{"plain"}) {
		t.Errorf("plain = %+v", p)
	}
}

func TestInspectRejectsInvalidSubcommands(t *testing.T) {
	src := `
import { command, definePlugin, subcommand } from "@cicord/sdk";
const sub = subcommand({ description: "s", run() {} });
const many: any = {};
for (let i = 0; i < 26; i++) many["s" + i] = sub;
export default definePlugin({
  commands: {
    both: { description: "x", run() {}, options: { a: { type: "string", description: "a" } }, subcommands: { a: sub } } as any,
    toomany: { description: "x", subcommands: many } as any,
    dup: { description: "x", subcommands: { a: sub }, groups: { a: { description: "g", subcommands: { b: sub } } } } as any,
    emptygroup: { description: "x", groups: { g: { description: "g", subcommands: {} } } } as any,
    norun: { description: "x", subcommands: { a: { description: "s" } } } as any,
    badsub: { description: "x", subcommands: { Bad: sub, nodesc: { run() {} } } } as any,
    empty: { description: "x", subcommands: {} } as any,
    opts: command({
      description: "x",
      options: {
        lenint: { type: "integer", description: "x", minLength: 1 },
        lenbad: { type: "string", description: "x", maxLength: 0 },
        lenorder: { type: "string", description: "x", minLength: 5, maxLength: 2 },
        acbool: { type: "boolean", description: "x", autocomplete: () => [] },
        acchoices: { type: "string", description: "x", choices: [{ name: "a", value: "a" }], autocomplete: () => [] },
        acnotfn: { type: "string", description: "x", autocomplete: true },
      } as any,
      run() {},
    }),
  },
});
`
	_, err := Inspect(bundleSource(t, src))
	var de *DefinitionError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DefinitionError, got %v", err)
	}
	for _, want := range []string{
		`command "both": a command with subcommands has no run`,
		`command "both": a command with subcommands has no options`,
		`command "toomany": at most 25 subcommands and groups (got 26)`,
		`command "dup": group "a": a subcommand has the same name`,
		`command "emptygroup": group "g": a group needs subcommands`,
		`command "norun": subcommand "a": run must be a function`,
		`command "badsub": subcommand "Bad": invalid name`,
		`command "badsub": subcommand "nodesc": description is required`,
		`command "empty": subcommands/groups declared but empty`,
		`option "lenint": minLength is only supported on string options`,
		`option "lenbad": maxLength must be between 1 and 6000`,
		`option "lenorder": minLength is greater than maxLength`,
		`option "acbool": autocomplete is only supported on string, integer and number options`,
		`option "acchoices": autocomplete and choices are mutually exclusive`,
		`option "acnotfn": autocomplete must be a function, got boolean`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func (h *harness) interaction(typ discord.InteractionType, name string, opts []discord.InteractionOption) discord.Interaction {
	return discord.Interaction{ID: "it-" + name, Token: "tok", ApplicationID: "app", Type: typ,
		ServerID: "10", ChannelID: "100", User: discord.User{ID: "42", Username: "alice"},
		Member:      &discord.Member{ServerID: "10", User: discord.User{ID: "42", Username: "alice"}},
		CommandName: name, Options: opts}
}

func TestSubcommandDispatch(t *testing.T) {
	h := start(t, subcommandPlugin, nil)
	ctx := context.Background()
	it := h.interaction(discord.InteractionCommand, "fruit", subcommandOpts([]string{"pick"}, discord.InteractionOption{Name: "name", Type: discord.OptionString, Value: "kiwi"}))
	if err := h.inst.HandleInteraction(ctx, it, "command", "fruit"); err != nil {
		t.Fatal(err)
	}
	r := h.responses()
	var got map[string]any
	if len(r) != 1 || json.Unmarshal([]byte(contentOf(r[0])), &got) != nil {
		t.Fatalf("responses = %+v", r)
	}
	if !reflect.DeepEqual(got, map[string]any{"command": "fruit", "group": nil, "subcommand": "pick", "name": "kiwi"}) {
		t.Errorf("interaction = %v", got)
	}
	if r[0].Data.Flags&discord.MessageFlagEphemeral == 0 {
		t.Error("the command's ephemeral default does not apply to its subcommands")
	}

	resetCalls(h)
	it = h.interaction(discord.InteractionCommand, "fruit", subcommandOpts([]string{"admin", "purge"}))
	if err := h.inst.HandleInteraction(ctx, it, "command", "fruit"); err != nil {
		t.Fatal(err)
	}
	if c := contentOf(h.responses()[0]); c != "admin purge" {
		t.Errorf("group reply = %q", c)
	}

	resetCalls(h)
	if err := h.command("plain", nil, discord.Resolved{}); err != nil || contentOf(h.responses()[0]) != "plain none" {
		t.Errorf("plain = %v %+v", err, h.responses())
	}

	// The command itself (no subcommand) has no handler: generic error reply.
	resetCalls(h)
	err := h.inst.HandleInteraction(ctx, h.interaction(discord.InteractionCommand, "fruit", nil), "command", "fruit")
	if !errors.Is(err, ErrNoHandler) || contentOf(h.responses()[0]) != GenericErrorMessage {
		t.Errorf("err = %v responses = %+v", err, h.responses())
	}
}

func TestAutocompleteDispatch(t *testing.T) {
	h := start(t, subcommandPlugin, func(e *Env) { e.Limits.AutocompleteTimeout = 200 * time.Millisecond })
	ctx := context.Background()
	ac := func(focused string, value any, others ...discord.InteractionOption) (discord.InteractionResponse, error) {
		resetCalls(h)
		leaf := append([]discord.InteractionOption{{Name: focused, Type: discord.OptionString, Value: value, Focused: true}}, others...)
		err := h.inst.HandleInteraction(ctx, h.interaction(discord.InteractionAutocomplete, "fruit", subcommandOpts([]string{"pick"}, leaf...)), "autocomplete", "fruit")
		r := h.responses()
		if len(r) != 1 {
			t.Fatalf("%s: responses = %+v (err %v)", focused, r, err)
		}
		if r[0].Type != discord.ResponseAutocomplete || r[0].Data != nil {
			t.Fatalf("%s: response = %+v", focused, r[0])
		}
		return r[0], err
	}
	r, err := ac("name", "ap", discord.InteractionOption{Name: "count", Type: discord.OptionInteger, Value: 3.0})
	if err != nil {
		t.Fatal(err)
	}
	want := []discord.CommandChoice{{Name: "apple (pick/3)", Value: "apple"}, {Name: "apricot (pick/3)", Value: "apricot"}}
	if !reflect.DeepEqual(r.Choices, want) {
		t.Errorf("choices = %+v", r.Choices)
	}
	// No auto-defer: only the autocomplete result was sent.
	if n := len(h.dc.Calls("EditOriginalResponse")) + len(h.dc.Calls("CreateFollowup")); n != 0 {
		t.Errorf("%d other calls", n)
	}

	for _, tc := range []struct{ option, want string }{
		{"count", "must be a integer"},      // 1.5 for an integer option
		{"many", "26 choices (at most 25)"}, // too many
		{"boom", "no fruit"},                // throws
		{"slow", "timed out"},               // AutocompleteTimeout
	} {
		r, err := ac(tc.option, "")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.option, err, tc.want)
		}
		if r.Choices == nil || len(r.Choices) != 0 {
			t.Errorf("%s: choices = %#v, want an empty list", tc.option, r.Choices)
		}
	}

	// An option without autocomplete is not dispatched.
	resetCalls(h)
	it := h.interaction(discord.InteractionAutocomplete, "fruit", subcommandOpts([]string{"admin", "purge"}, discord.InteractionOption{Name: "x", Value: "", Focused: true}))
	if err := h.inst.HandleInteraction(ctx, it, "autocomplete", "fruit"); !errors.Is(err, ErrNoHandler) {
		t.Errorf("err = %v", err)
	}
	// …but it is still answered (an empty list, not "loading options failed").
	if r := h.responses(); len(r) != 1 || r[0].Type != discord.ResponseAutocomplete || r[0].Choices == nil || len(r[0].Choices) != 0 {
		t.Errorf("responses = %+v", r)
	}
}

func TestChannelMessageOp(t *testing.T) {
	src := `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    get: command({
      description: "get",
      options: { id: { type: "string", description: "id", required: true } },
      async run(ctx, it) {
        const m = await ctx.server.channel({ id: "100" }).message(it.options.id);
        await it.reply(m ? m.id + ":" + m.content + ":" + m.author.username + ":" + m.channel.id : "none");
      },
    }),
  },
});`
	h := start(t, src, nil)
	get := func(id string) string {
		resetCalls(h)
		if err := h.command("get", []discord.InteractionOption{{Name: "id", Type: discord.OptionString, Value: id}}, discord.Resolved{}); err != nil {
			t.Fatal(err)
		}
		return contentOf(h.responses()[0])
	}
	if got := get("901"); got != "901:hello:alice:100" {
		t.Errorf("message = %q", got)
	}
	if got := get("999"); got != "none" {
		t.Errorf("unknown message = %q", got)
	}
	if c := h.dc.Calls("GetMessage"); len(c) != 1 || c[0].Args[0] != "100" || c[0].Args[1] != "999" {
		t.Errorf("calls = %+v", c)
	}
	resetCalls(h)
	err := h.command("get", []discord.InteractionOption{{Name: "id", Type: discord.OptionString, Value: "nope"}}, discord.Resolved{})
	if err == nil || !strings.Contains(err.Error(), `invalid message id "nope"`) || contentOf(h.responses()[0]) != GenericErrorMessage {
		t.Errorf("invalid id: %v %+v", err, h.responses())
	}

	// Without discord.messages.read.
	h2 := start(t, src, func(e *Env) { e.Grants = []string{capChannelsRead} })
	if err := h2.command("get", []discord.InteractionOption{{Name: "id", Type: discord.OptionString, Value: "901"}}, discord.Resolved{}); err == nil || !strings.Contains(err.Error(), capMessagesRead) {
		t.Errorf("err = %v", err)
	}
	if n := len(h2.dc.Calls("GetMessage")); n != 0 {
		t.Errorf("GetMessage called %d times without the capability", n)
	}
}

func TestServerEvents(t *testing.T) {
	src := `
import { definePlugin } from "@cicord/sdk";
export default definePlugin({
  events: {
    serverJoin(ctx, server) { ctx.log.info("joined", { id: server.id, name: server.name }); },
    serverLeave(ctx, server) { ctx.log.info("left", { id: server.id }); },
  },
});`
	h := start(t, src, nil)
	if got := h.def.RequiredIntents(); len(got) != 0 {
		t.Errorf("intents = %v", got)
	}
	if err := h.inst.HandleEvent(context.Background(), discord.ServerJoinEvent{Server: discord.Server{ID: "10", Name: "My Community"}}); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("joined"); e.Attrs["id"] != "10" || e.Attrs["name"] != "My Community" {
		t.Errorf("joined = %+v", e)
	}
	if err := h.inst.HandleEvent(context.Background(), discord.ServerLeaveEvent{ServerID: "10"}); err != nil {
		t.Fatal(err)
	}
	if e := h.logged("left"); e.Attrs["id"] != "10" {
		t.Errorf("left = %+v", e)
	}
	if err := h.inst.HandleEvent(context.Background(), discord.ServerLeaveEvent{ServerID: "11"}); err == nil {
		t.Error("event of another server accepted")
	}
}
