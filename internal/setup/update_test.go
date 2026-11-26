package setup

import (
	"reflect"
	"testing"

	"github.com/cicord/cicord/internal/config"
)

// welcome 0.1.1 required discord.roles.assign, discord.dm.send and portal.links; 0.2.0
// declares them in optional features (defaultRole, onboarding).
var welcome020 = UpdateSpec{
	Capabilities: []string{"storage.kv", "discord.messages.send", "discord.members.read", "discord.channels.read"},
	Intents:      []string{"ServerMembers"},
	Optional: []config.OptionalFeature{
		{Feature: "onboarding", Capabilities: []string{"discord.dm.send", "portal.links"}},
		{Feature: "defaultRole", Capabilities: []string{"discord.roles.assign"}},
		{Feature: "inviter", Capabilities: []string{"plugins.query:invite-tracker.inviterOf"}},
	},
}

func TestUpdateGrantsKeepsOptionalFeatures(t *testing.T) {
	old := []string{"discord.messages.send", "discord.channels.read", "discord.members.read",
		"discord.roles.assign", "discord.dm.send", "portal.links", "storage.kv"}
	got := UpdateGrants(old, welcome020)
	if !reflect.DeepEqual(got.Grants, old) || len(got.Added) != 0 || len(got.Removed) != 0 {
		t.Fatalf("grants = %+v", got)
	}
	want := []OptionalGrant{{"discord.roles.assign", []string{"defaultRole"}}, {"discord.dm.send", []string{"onboarding"}}, {"portal.links", []string{"onboarding"}}}
	if !reflect.DeepEqual(got.Kept, want) {
		t.Errorf("kept = %+v", got.Kept)
	}

	// A grant no longer declared is removed; a newly required capability is added after the
	// kept grants (their order unchanged); a wildcard grant covering an optional capability
	// is kept.
	got = UpdateGrants([]string{"http.fetch", "discord.roles.assign", "storage.kv", "plugins.query:invite-tracker.*"}, welcome020)
	wantGrants := []string{"discord.roles.assign", "storage.kv", "plugins.query:invite-tracker.*", "discord.messages.send", "discord.members.read", "discord.channels.read"}
	if !reflect.DeepEqual(got.Grants, wantGrants) {
		t.Errorf("grants = %v, want %v", got.Grants, wantGrants)
	}
	if !reflect.DeepEqual(got.Removed, []string{"http.fetch"}) ||
		!reflect.DeepEqual(got.Added, []string{"discord.channels.read", "discord.members.read", "discord.messages.send"}) {
		t.Errorf("added %v, removed %v", got.Added, got.Removed)
	}
	if len(got.Kept) != 2 || got.Kept[1].Name != "plugins.query:invite-tracker.*" || got.Kept[1].Features[0] != "inviter" {
		t.Errorf("kept = %+v", got.Kept)
	}
}

func TestPermissionDiffSeparatesOptionalFromRemoved(t *testing.T) {
	cur := InstalledVersion{Version: "0.1.1",
		Capabilities: []string{"discord.messages.send", "discord.roles.assign", "discord.dm.send", "portal.links", "storage.kv", "http.fetch"},
		Intents:      []string{"ServerMembers", "ServerVoiceStates"}}
	d := permissionDiff(cur, welcome020)
	if !reflect.DeepEqual(d.Capabilities.Removed, []string{"http.fetch"}) || len(d.Capabilities.Added) != 2 {
		t.Errorf("capabilities = %+v", d.Capabilities)
	}
	if len(d.MadeOptional) != 3 || d.MadeOptional[0].Name != "discord.dm.send" || d.MadeOptional[0].Features[0] != "onboarding" {
		t.Errorf("made optional = %+v", d.MadeOptional)
	}
	if !reflect.DeepEqual(d.Intents.Removed, []string{"ServerVoiceStates"}) || len(d.IntentsMadeOptional) != 0 {
		t.Errorf("intents = %+v / %+v", d.Intents, d.IntentsMadeOptional)
	}
}
