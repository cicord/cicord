package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestInteropOptionalIntents(t *testing.T) {
	sp := PluginSpec{Capabilities: []string{"storage.kv"}, Optional: []OptionalFeature{{Feature: "capture", When: "/capture/enabled", Intents: []string{"MessageContent"}, Capabilities: []string{"discord.messages.read"}}, {Feature: "roleRewards", Capabilities: []string{"plugins.call:roles.grant", "discord.roles.assign"}}}}
	l := loader{}
	grants := []string{"storage.kv", "discord.messages.read", "plugins.call:roles.*", "http.fetch"}
	features := l.features(sp, grants, "bot", map[string]any{"capture": map[string]any{"enabled": true}})
	if features["capture"] != "" || !strings.Contains(features["roleRewards"], "discord.roles.assign") {
		t.Fatal(features)
	}
	instance := Instance{Bot: "bot", Plugin: &LoadedPlugin{Manifest: &Plugin{Spec: sp}}, Features: features}
	repo := Repo{Instances: []Instance{instance}, Bots: map[string]*Bot{"bot": {}}}
	if !slices.Contains(repo.GatewayIntents("bot"), "MessageContent") {
		t.Fatal("missing optional intent")
	}
	repo.ApplyOptionalIntents("bot", discord.Intents(0))
	if slices.Contains(repo.GatewayIntents("bot"), "MessageContent") || !strings.Contains(features["capture"], "not enabled") {
		t.Fatal(features)
	}
	if caps := effectiveGrants(sp, grants); slices.Contains(caps, "http.fetch") || !slices.Contains(caps, "plugins.call:roles.grant") {
		t.Fatal(caps)
	}
	disabled := l.features(sp, grants, "bot", map[string]any{})
	if !strings.Contains(disabled["capture"], "/capture/enabled") {
		t.Fatal(disabled)
	}
}
