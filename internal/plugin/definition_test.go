package plugin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/npjonath/codi/internal/discord"
)

const fullPlugin = `
import { command, definePlugin } from "@codi/sdk";

export default definePlugin({
  async onStart(ctx) {},
  onStop() {},
  events: {
    messageCreate() {},
    reactionAdd() {},
    memberJoin() {},
  },
  commands: {
    poll: command({
      description: "Create a poll",
      permissions: ["ManageMessages", "SendPolls"],
      options: {
        title: { type: "string", description: "Title" },
        duration: { type: "integer", description: "Minutes", required: true, min: 1, max: 60,
                    choices: [{ name: "short", value: 5 }, { name: "long", value: 30 }] },
        channel: { type: "channel", description: "Where", required: true },
        anonymous: { type: "boolean", description: "Hide voters" },
        weight: { type: "number", description: "Weight", min: 0.5 },
        kind: { type: "string", description: "Kind", choices: [{ name: "Yes/No", value: "yn" }] },
      },
      run() {},
    }),
    ping: command({ description: "Pong", ephemeral: true, run() {} }),
  },
  components: { vote() {}, close() {} },
  modals: { feedback() {} },
  schedules: { digest: { cron: "0 9 * * 1", run() {} }, hourly: { cron: "@hourly", run() {} } },
  tasks: { remind() {} },
  subscriptions: { "economy.purchase": () => {} },
  routes: {
    "GET /answers/:id": { access: "member", handler: () => ({}) },
    "GET /answers/top": { access: "public", handler: () => ({}) },
    "POST /answers/:id/votes": { access: "moderator", handler: () => ({}) },
  },
});
`

func TestInspectDefinition(t *testing.T) {
	d, err := Inspect(bundleSource(t, fullPlugin))
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasOnStart || !d.HasOnStop {
		t.Fatal("onStart/onStop not detected")
	}
	if !reflect.DeepEqual(d.Events, []string{"memberJoin", "messageCreate", "reactionAdd"}) {
		t.Fatalf("events = %v", d.Events)
	}
	if got := d.RequiredIntents(); !reflect.DeepEqual(got, []string{"ServerMembers", "ServerMessageReactions", "ServerMessages"}) {
		t.Fatalf("intents = %v", got)
	}
	if len(d.Commands) != 2 || d.Commands[0].Name != "ping" || d.Commands[1].Name != "poll" {
		t.Fatalf("commands = %+v", d.Commands)
	}
	if !d.Commands[0].Ephemeral || d.Commands[1].Ephemeral {
		t.Fatal("ephemeral flags")
	}
	f := func(v float64) *float64 { return &v }
	wantOpts := []discord.CommandOption{
		// required first, in declaration order…
		{Type: discord.OptionInteger, Name: "duration", Description: "Minutes", Required: true, MinValue: f(1), MaxValue: f(60),
			Choices: []discord.CommandChoice{{Name: "short", Value: float64(5)}, {Name: "long", Value: float64(30)}}},
		{Type: discord.OptionChannel, Name: "channel", Description: "Where", Required: true},
		// …then optional ones, in declaration order.
		{Type: discord.OptionString, Name: "title", Description: "Title"},
		{Type: discord.OptionBoolean, Name: "anonymous", Description: "Hide voters"},
		{Type: discord.OptionNumber, Name: "weight", Description: "Weight", MinValue: f(0.5)},
		{Type: discord.OptionString, Name: "kind", Description: "Kind", Choices: []discord.CommandChoice{{Name: "Yes/No", Value: "yn"}}},
	}
	if got := d.Commands[1].Options; !reflect.DeepEqual(got, wantOpts) {
		t.Fatalf("options:\n got %+v\nwant %+v", got, wantOpts)
	}
	perms := d.Commands[1].DiscordCommand().DefaultMemberPermissions
	if perms == nil || *perms != discord.MustPermissions("ManageMessages", "SendPolls") {
		t.Fatalf("permissions = %v", perms)
	}
	if !reflect.DeepEqual(d.Components, []string{"close", "vote"}) || !reflect.DeepEqual(d.Modals, []string{"feedback"}) {
		t.Fatalf("components %v modals %v", d.Components, d.Modals)
	}
	if !reflect.DeepEqual(d.Schedules, []ScheduleDef{{"digest", "0 9 * * 1"}, {"hourly", "@hourly"}}) {
		t.Fatalf("schedules = %v", d.Schedules)
	}
	if !reflect.DeepEqual(d.Tasks, []string{"remind"}) || !reflect.DeepEqual(d.Subscriptions, []string{"economy.purchase"}) {
		t.Fatalf("tasks %v subs %v", d.Tasks, d.Subscriptions)
	}
	wantRoutes := []RouteDef{
		{"GET", "/answers/:id", "member"}, {"GET", "/answers/top", "public"}, {"POST", "/answers/:id/votes", "moderator"},
	}
	if !reflect.DeepEqual(d.Routes, wantRoutes) {
		t.Fatalf("routes = %v", d.Routes)
	}
}

func TestInspectRejectsInvalidDefinitions(t *testing.T) {
	src := `
import { command, definePlugin } from "@codi/sdk";
export default definePlugin({
  events: { memberJoined() {} },
  commands: {
    Bad: command({ description: "x", run() {} }),
    nodesc: command({ description: "", run() {} } as any),
    opts: command({
      description: "options",
      options: {
        len: { type: "string", description: "x", min: 3 },
        weird: { type: "float", description: "x" } as any,
        Upper: { type: "string", description: "x" },
        c: { type: "boolean", description: "x", choices: [{ name: "a", value: "a" }] },
        i: { type: "integer", description: "x", choices: [{ name: "a", value: 1.5 }] },
      },
      permissions: ["Fly"] as any,
      run() {},
    }),
    norun: { description: "x" } as any,
  },
  schedules: { s: { cron: "every monday", run() {} } },
  routes: {
    "FETCH /x": { access: "public", handler() { return 1 } },
    "GET /y": { access: "everyone", handler() { return 1 } },
    "GET /a/:id": { access: "public", handler() { return 1 } },
    "GET /a/:other": { access: "public", handler() { return 1 } },
  } as any,
  subscriptions: { nodot: () => {} },
  commandz: {},
} as any);
`
	_, err := Inspect(bundleSource(t, src))
	var de *DefinitionError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DefinitionError, got %v", err)
	}
	for _, want := range []string{
		`unknown event handler events.memberJoined`,
		`command "Bad": invalid name`,
		`command "nodesc": description is required`,
		`option "len": min is only supported on integer and number options`,
		`option "weird": invalid type float`,
		`option "Upper": invalid name`,
		`option "c": choices are only supported on string, integer and number options`,
		`choice "a": value must be an integer`,
		`unknown permission "Fly"`,
		`command "norun": run must be a function`,
		`schedules.s: invalid cron "every monday"`,
		`invalid route key "FETCH /x"`,
		`routes["GET /y"].access must be one of`,
		`match the same paths`,
		`subscriptions: invalid key "nodot"`,
		`unknown key "commandz"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestInspectModuleErrors(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"no default export": {`export const x = 1;`, "no default export"},
		"not definePlugin":  {`export default { events: {} };`, "result of definePlugin"},
		"throws":            {"import { definePlugin } from \"@codi/sdk\";\nconst x: any = null;\nx.boom();\nexport default definePlugin({});", "src/index.ts:3"},
		"sdk call at top level": {
			`import { definePlugin } from "@codi/sdk"; setTimeout(() => {}, 1); export default definePlugin({});`,
			"not available while the module is evaluated",
		},
		"require other module": {`(globalThis as any).require("left-pad"); export default 1;`, `cannot require "left-pad"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Inspect(bundleSource(t, tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestInspectInfiniteModuleIsInterrupted(t *testing.T) {
	old := InspectBudget
	InspectBudget = 50 * time.Millisecond
	defer func() { InspectBudget = old }()
	_, err := Inspect(bundleSource(t, `while (true) {} export default 1;`))
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestEventIntents(t *testing.T) {
	if got := EventIntents("memberJoin"); !reflect.DeepEqual(got, []string{"ServerMembers"}) {
		t.Fatal(got)
	}
	if got := EventIntents("reactionRemove"); !reflect.DeepEqual(got, []string{"ServerMessageReactions"}) {
		t.Fatal(got)
	}
	if EventIntents("nope") != nil {
		t.Fatal("unknown event")
	}
	for _, n := range EventNames() {
		if _, err := discord.ParseIntents(EventIntents(n)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	if EventName(discord.ReactionAddEvent{}) != "reactionAdd" || EventName(discord.ReadyEvent{}) != "" {
		t.Fatal("EventName")
	}
}

func TestCron(t *testing.T) {
	from := time.Date(2026, 11, 21, 10, 30, 0, 0, time.UTC) // a Saturday
	next, err := NextRun("0 9 * * 1", from)
	if err != nil || !next.Equal(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v, %v", next, err)
	}
	if next, _ := NextRun("@every 10m", from); !next.Equal(from.Add(10 * time.Minute)) {
		t.Fatalf("@every: %v", next)
	}
	for _, bad := range []string{"", "* * *", "61 * * * *", "CRON_TZ=Europe/Paris 0 9 * * *", "0 0 9 * * *"} {
		if ValidateCron(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestMatchRoute(t *testing.T) {
	d := &Definition{Routes: []RouteDef{
		{"GET", "/answers/:id", "member"}, {"GET", "/answers/top", "public"},
		{"POST", "/answers/:id/votes/:vote", "member"}, {"GET", "/", "public"},
	}}
	cases := []struct {
		method, path, want string
		params             map[string]string
	}{
		{"GET", "/answers/42", "/answers/:id", map[string]string{"id": "42"}},
		{"GET", "/answers/top", "/answers/top", map[string]string{}},
		{"get", "/answers/top/", "/answers/top", map[string]string{}},
		{"POST", "/answers/7/votes/up", "/answers/:id/votes/:vote", map[string]string{"id": "7", "vote": "up"}},
		{"GET", "/", "/", map[string]string{}},
		{"POST", "/answers/42", "", nil},
		{"GET", "/answers", "", nil},
		{"GET", "/answers/1/2", "", nil},
	}
	for _, c := range cases {
		r, params, ok := d.MatchRoute(c.method, c.path)
		if c.want == "" {
			if ok {
				t.Errorf("%s %s matched %v", c.method, c.path, r)
			}
			continue
		}
		if !ok || r.Path != c.want || !reflect.DeepEqual(params, c.params) {
			t.Errorf("%s %s = %v %v %v, want %s %v", c.method, c.path, r, params, ok, c.want, c.params)
		}
	}
}

func TestCustomIDRoundTrip(t *testing.T) {
	for _, local := range []string{"vote", "vote:42", "a:b:c", strings.Repeat("x", MaxLocalIDLength("welcome"))} {
		id, err := EncodeCustomID("welcome", local)
		if err != nil {
			t.Fatalf("%q: %v", local, err)
		}
		if len(id) > MaxCustomIDLength {
			t.Fatalf("%q encodes to %d bytes", local, len(id))
		}
		inst, got, ok := ParseCustomID(id)
		if !ok || inst != "welcome" || got != local {
			t.Fatalf("ParseCustomID(%q) = %q %q %v", id, inst, got, ok)
		}
	}
	if _, err := EncodeCustomID("welcome", strings.Repeat("x", MaxLocalIDLength("welcome")+1)); err == nil {
		t.Fatal("too long id accepted")
	}
	if _, err := EncodeCustomID("bad:name", "x"); err == nil {
		t.Fatal("instance with ':' accepted")
	}
	if _, err := EncodeCustomID("welcome", ""); err == nil {
		t.Fatal("empty local id accepted")
	}
	for _, foreign := range []string{"", "vote", "c:", "c:inst", "c::x", "c:inst:", "x:inst:vote"} {
		if _, _, ok := ParseCustomID(foreign); ok {
			t.Errorf("ParseCustomID(%q) should fail", foreign)
		}
	}
}

func TestParseColor(t *testing.T) {
	if v, err := ParseColor("#3498DB"); err != nil || v != 0x3498db {
		t.Fatal(v, err)
	}
	for _, bad := range []string{"3498db", "#349", "#zzzzzz", ""} {
		if _, err := ParseColor(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if colorString(0x3498db) != "#3498db" || colorString(0) != "" {
		t.Fatal("colorString")
	}
}
