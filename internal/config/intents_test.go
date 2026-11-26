package config

import (
	"slices"
	"testing"
)

func TestGatewayIntents(t *testing.T) {
	r := &Repo{
		Servers: map[string]*Server{
			"plain":   {},
			"automod": {Spec: ServerSpec{AutoMod: map[string]AutoModSpec{"words": {}}}},
			"expr":    {Spec: ServerSpec{Stickers: map[string]StickerSpec{"wave": {}}}},
		},
		Bots: map[string]*Bot{
			"a": {Spec: BotSpec{Servers: []BotServer{{Key: "plain"}}, Intents: []string{"ServerMessages"}}},
			"b": {Spec: BotSpec{Servers: []BotServer{{Key: "automod"}, {Key: "expr"}}}},
		},
	}
	if got := r.GatewayIntents("a"); !slices.Equal(got, []string{"Servers", "ServerMessages"}) {
		t.Errorf("a = %v", got)
	}
	got := r.GatewayIntents("b")
	if !slices.Contains(got, "AutoModerationConfiguration") || !slices.Contains(got, "ServerExpressions") {
		t.Errorf("b = %v", got)
	}
	if got := r.GatewayIntents("unknown"); !slices.Equal(got, []string{"Servers"}) {
		t.Errorf("unknown = %v", got)
	}
}
