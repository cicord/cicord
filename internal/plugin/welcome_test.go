package plugin

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/locales"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

const welcomeDir = "../../examples/basic/plugins/welcome"

// welcomeGrants are the grants of examples/basic/bots/moderator/bot.yaml.
var welcomeGrants = []string{
	"discord.messages.send", "discord.channels.read", "discord.members.read",
	"discord.roles.assign", "discord.dm.send", "portal.links", "storage.kv",
}

// startWelcome returns the harness and the instance's storage (welcome 0.2 needs the
// advanced KV operations: entries and atomic commits).
func startWelcome(t *testing.T) (*harness, state.KV) {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	kv := store.KV("plugin:welcome")
	res, err := build.Bundle(welcomeDir, "./src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := locales.Load(welcomeDir, "locales")
	if err != nil {
		t.Fatal(err)
	}
	h := startCode(t, res.Code, func(e *Env) {
		e.PluginName, e.InstanceName = "welcome", "welcome"
		e.KV = kv
		e.Grants = welcomeGrants
		e.Locales = cat
		// The optional features granted by the bot.yaml grants (spec.optional).
		e.Features = map[string]string{"defaultRole": "", "onboarding": ""}
		// Effective config for my-community: base config + override, schema defaults applied.
		e.Config = map[string]any{
			"channel":      "general",
			"message":      "Hi {member}, welcome to {server}! Take a minute to read the rules.",
			"autoRole":     "member",
			"dmOnboarding": true,
			"trigger":      "join",
			"variants":     []any{},
			"card":         false,
			"showInviter":  false,
			"enabled":      true,
		}
	})
	return h, kv
}

func TestWelcomeDefinition(t *testing.T) {
	h, _ := startWelcome(t)
	d := h.def
	if !reflect.DeepEqual(d.Events, []string{"memberJoin", "memberLeave", "memberUpdate"}) {
		t.Fatalf("events = %v", d.Events)
	}
	if got := d.RequiredIntents(); !reflect.DeepEqual(got, []string{"ServerMembers"}) {
		t.Fatalf("intents = %v", got)
	}
	if len(d.Commands) != 2 || d.Commands[0].Name != "welcome-preview" || d.Commands[1].Name != "welcome-stats" {
		t.Fatalf("commands = %+v", d.Commands)
	}
	prev := d.Commands[0]
	if !prev.Ephemeral || !reflect.DeepEqual(prev.Permissions, []string{"ManageServer"}) {
		t.Fatalf("welcome-preview = %+v", prev)
	}
	want := []discord.CommandOption{{Type: discord.OptionUser, Name: "member", Description: "Member to greet (defaults to you)"}}
	if !reflect.DeepEqual(prev.Options, want) {
		t.Fatalf("options = %+v", prev.Options)
	}
	cmds := d.DiscordCommands()
	if cmds[0].DefaultMemberPermissions == nil || *cmds[0].DefaultMemberPermissions != discord.MustPermissions("ManageServer") {
		t.Fatalf("default permissions = %v", cmds[0].DefaultMemberPermissions)
	}
	if cmds[1].DefaultMemberPermissions != nil {
		t.Fatal("welcome-stats should be usable by everyone")
	}
}

func TestWelcomeMemberJoinEndToEnd(t *testing.T) {
	h, kv := startWelcome(t)
	join := discord.MemberJoinEvent{Member: discord.Member{
		ServerID: "10", User: discord.User{ID: "42", Username: "alice", GlobalName: "Alice"},
		JoinedAt: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC),
	}}
	if err := h.inst.HandleEvent(context.Background(), join); err != nil {
		t.Fatal(err)
	}

	sends := h.dc.Calls("SendMessage")
	if len(sends) != 2 {
		t.Fatalf("want 2 messages (welcome + DM), got %d: %v", len(sends), h.dc.Names())
	}
	// 1. welcome message in the channel resolved from the "general" key
	if ch := sends[0].Args[0]; ch != "100" {
		t.Fatalf("welcome sent to %v, want 100", ch)
	}
	if got := sends[0].Args[1].(discord.MessageCreate).Content; got != "Hi <@42>, welcome to My Community! Take a minute to read the rules." {
		t.Fatalf("welcome content = %q", got)
	}
	// 2. default role resolved from the "member" key
	roles := h.dc.Calls("AddMemberRole")
	if len(roles) != 1 || !reflect.DeepEqual(roles[0].Args, []any{"10", "42", "200", "Welcome default role"}) {
		t.Fatalf("AddMemberRole calls = %+v", roles)
	}
	// 3. DM with a link button to the portal
	if dms := h.dc.Calls("CreateDM"); len(dms) != 1 || dms[0].Args[0] != "42" {
		t.Fatalf("CreateDM calls = %+v", dms)
	}
	if sends[1].Args[0] != "dm-42" {
		t.Fatalf("DM sent to %v", sends[1].Args[0])
	}
	dm := sends[1].Args[1].(discord.MessageCreate)
	if !strings.Contains(dm.Content, "**My Community**") || !strings.Contains(dm.Content, "Pick your roles") {
		t.Fatalf("DM content = %q", dm.Content)
	}
	wantRow := []discord.Component{{Type: discord.ComponentActionRow, Components: []discord.Component{{
		Type: discord.ComponentButton, Style: discord.ButtonLink, Label: "Get started",
		URL: "https://portal.example.com/onboarding?member=42&ttl=0s",
	}}}}
	if !reflect.DeepEqual(dm.Components, wantRow) {
		t.Fatalf("DM components = %+v", dm.Components)
	}
	// 4. stats counter (the member number of the greeting receipt)
	if v, _ := kv.Get(context.Background(), "stats:joined"); string(v) != "1" {
		t.Fatalf("stats:joined = %s", v)
	}

	// A member who already has the role does not get it again.
	join.Member.User.ID = "43"
	join.Member.RoleIDs = []string{"200"}
	if err := h.inst.HandleEvent(context.Background(), join); err != nil {
		t.Fatal(err)
	}
	if n := len(h.dc.Calls("AddMemberRole")); n != 1 {
		t.Fatalf("AddMemberRole called %d times", n)
	}
	if v, _ := kv.Get(context.Background(), "stats:joined"); string(v) != "2" {
		t.Fatalf("stats:joined = %s", v)
	}
}

func TestWelcomeCommands(t *testing.T) {
	h, kv := startWelcome(t)
	_ = kv.Set(context.Background(), "stats:joined", json.RawMessage("7"), 0)

	if err := h.command("welcome-stats", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	opts := []discord.InteractionOption{{Name: "member", Type: discord.OptionUser, Value: "77"}}
	res := discord.Resolved{Users: map[string]discord.User{"77": {ID: "77", Username: "bob"}}}
	if err := h.command("welcome-preview", opts, res); err != nil {
		t.Fatal(err)
	}
	if err := h.command("welcome-preview", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	rs := h.responses()
	if len(rs) != 3 {
		t.Fatalf("responses = %+v", rs)
	}
	for k, want := range []string{
		"Members welcomed: 7.",
		"Hi <@77>, welcome to My Community! Take a minute to read the rules.",
		"Hi <@42>, welcome to My Community! Take a minute to read the rules.",
	} {
		r := rs[k]
		if r.Type != discord.ResponseMessage || r.Data.Content != want {
			t.Errorf("response %d = %+v, want %q", k, r, want)
		}
		// welcome-stats replies with a plain string: ephemeral comes from the command.
		if r.Data.Flags&discord.MessageFlagEphemeral == 0 {
			t.Errorf("response %d is not ephemeral", k)
		}
	}
}
