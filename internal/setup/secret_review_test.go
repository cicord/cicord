package setup

import (
	"github.com/cicord/cicord/internal/config"
	"testing"
)

func TestBoundCredentialReview(t *testing.T) {
	const approved = "api.anthropic.com"
	const second = "support.example.org"
	network := []string{approved, second, "*.news.example.org"}
	budget := &config.ProviderBudget{Provider: "anthropic-messages", DailyLimitConfig: "daily"}
	for _, tc := range []struct {
		name                  string
		before, after         []string
		old, new              *config.ProviderBudget
		oldLimit, newLimit    int
		wantChange, wantWider bool
	}{
		{"already-network-allowed host", []string{approved}, []string{approved, second}, nil, nil, 0, 0, true, true},
		{"budget removed", []string{approved}, []string{approved}, budget, nil, 100, 0, true, true},
		{"raised budget via new default", []string{approved}, []string{approved}, budget, budget, 100, 200, true, true},
		{"lowered bound", []string{approved}, []string{approved}, budget, budget, 100, 50, true, false},
		{"same", []string{approved}, []string{approved}, budget, budget, 100, 100, false, false},
		{"wildcard narrowed", []string{"*.news.example.org"}, []string{"en.news.example.org"}, nil, nil, 0, 0, true, false},
		{"previously not reachable", []string{"other.example.org"}, []string{second}, nil, nil, 0, 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := config.PluginSecret{Name: "KEY", Hosts: tc.before, Budget: tc.old}
			after := config.PluginSecret{Name: "KEY", Hosts: tc.after, Budget: tc.new}
			old := &config.Instance{NetworkAllow: network, Plugin: &config.LoadedPlugin{Manifest: &config.Plugin{Spec: config.PluginSpec{Secrets: []config.PluginSecret{before}}}}, Config: map[string]any{"daily": float64(tc.oldLimit)}}
			pi := config.PluginInstance{Secrets: map[string]config.TokenRef{"KEY": {SecretRef: "BOUND_NAME"}}}
			spec := UpdateSpec{Network: network, SecretDeclarations: []config.PluginSecret{after}}
			got := boundChanges(old, pi, spec, map[string]any{"daily": float64(tc.newLimit)})
			if (len(got) > 0) != tc.wantChange || len(got) > 0 && got[0].Widened != tc.wantWider {
				t.Fatalf("unexpected review %+v", got)
			}
		})
	}
}
