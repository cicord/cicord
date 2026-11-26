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

// A config switch (optional feature `when`) turned on while the feature's capability is not
// granted is a warning: the feature stays disabled, which the config does not show.
func TestSwitchedOnFeatureWithoutGrantWarns(t *testing.T) {
	spec := PluginSpec{Optional: []OptionalFeature{
		{Feature: "content", When: "/cacheContent", Capabilities: []string{"discord.messages.read"}},
		{Feature: "always", Capabilities: []string{"render"}},
	}}
	for _, c := range []struct {
		cfg    map[string]any
		grants []string
		warn   bool
	}{
		{map[string]any{"cacheContent": true}, nil, true},
		{map[string]any{"cacheContent": true}, []string{"discord.messages.read"}, false},
		{map[string]any{"cacheContent": false}, nil, false},
		{map[string]any{}, nil, false},
	} {
		l := &loader{}
		l.checkSwitchedFeatures("bot.yaml", 3, "/spec/plugins/0/grants", "audit-log", spec, c.grants, c.cfg)
		if got := len(l.sink.ds) == 1 && l.sink.ds[0].Severity == SeverityWarning; got != c.warn || (!c.warn && len(l.sink.ds) > 0) {
			t.Errorf("%v %v: %v", c.cfg, c.grants, l.sink.ds)
		}
	}
	if got := (&loader{}).features(spec, nil, "bot", map[string]any{"cacheContent": true}); got["content"] != "discord.messages.read not granted" || got["always"] != "render not granted" {
		t.Errorf("features = %v", got)
	}
}
