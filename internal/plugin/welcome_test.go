package plugin

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/build"
	"github.com/npjonath/cicord/internal/discord"
)

// welcomeGrants are the grants of examples/basic/bots/moderator/bot.yaml.
var welcomeGrants = []string{
	"discord.messages.send", "discord.channels.read", "discord.members.read",
	"discord.roles.assign", "discord.dm.send", "portal.links", "storage.kv",
}

func startWelcome(t *testing.T) *harness {
	t.Helper()
	res, err := build.Bundle("../../examples/basic/plugins/welcome", "./src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	return startCode(t, res.Code, func(e *Env) {
		e.PluginName, e.InstanceName = "welcome", "welcome"
		e.Grants = welcomeGrants
		// Effective config for ma-commu: base config + override, schema defaults applied.
		e.Config = map[string]any{
			"channel":      "general",
			"message":      "Salut {member}, bienvenue sur {server} ! Passe lire le règlement.",
			"autoRole":     "member",
			"dmOnboarding": true,
		}
	})
}

func TestWelcomeDefinition(t *testing.T) {
	h := startWelcome(t)
	d := h.def
	if !reflect.DeepEqual(d.Events, []string{"memberJoin"}) {
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
	h := startWelcome(t)
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
	if got := sends[0].Args[1].(discord.MessageCreate).Content; got != "Salut <@42>, bienvenue sur Ma Commu ! Passe lire le règlement." {
		t.Fatalf("welcome content = %q", got)
	}
	// 2. default role resolved from the "member" key
	roles := h.dc.Calls("AddMemberRole")
	if len(roles) != 1 || !reflect.DeepEqual(roles[0].Args, []any{"10", "42", "200", "welcome: default role"}) {
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
	if !strings.Contains(dm.Content, "**Ma Commu**") || !strings.Contains(dm.Content, "rôles") {
		t.Fatalf("DM content = %q", dm.Content)
	}
	wantRow := []discord.Component{{Type: discord.ComponentActionRow, Components: []discord.Component{{
		Type: discord.ComponentButton, Style: discord.ButtonLink, Label: "Commencer",
		URL: "https://portal.example.com/onboarding?member=42&ttl=0s",
	}}}}
	if !reflect.DeepEqual(dm.Components, wantRow) {
		t.Fatalf("DM components = %+v", dm.Components)
	}
	// 4. stats counter and structured log
	if v, _ := h.kv.Get(context.Background(), "stats:joined"); string(v) != "1" {
		t.Fatalf("stats:joined = %s", v)
	}
	e := h.logged("member welcomed")
	if e.Attrs["member"] != "42" || e.Attrs["total"] != float64(1) || e.Attrs["plugin"] != "welcome" ||
		e.Attrs["instance"] != "welcome" || e.Attrs["server"] != "ma-commu" || e.Attrs["handler"] != "event" {
		t.Fatalf("log attrs = %v", e.Attrs)
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
	if v, _ := h.kv.Get(context.Background(), "stats:joined"); string(v) != "2" {
		t.Fatalf("stats:joined = %s", v)
	}
}

func TestWelcomeCommands(t *testing.T) {
	h := startWelcome(t)
	_ = h.kv.Set(context.Background(), "stats:joined", json.RawMessage("7"), 0)

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
		"7 membres accueillis.",
		"Salut <@77>, bienvenue sur Ma Commu ! Passe lire le règlement.",
		"Salut <@42>, bienvenue sur Ma Commu ! Passe lire le règlement.",
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
