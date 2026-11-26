package runtime

import (
	"github.com/cicord/cicord/internal/config"
	"strings"
	"testing"
)

func TestHostCredentialSentinelsNeverReachPluginResolver(t *testing.T) {
	repo := &config.Repo{Bots: map[string]*config.Bot{"second": {Spec: config.BotSpec{Token: config.TokenRef{SecretRef: "DISCORD_SECOND_SENTINEL"}}}}, Project: &config.Project{Spec: config.ProjectSpec{Registries: []config.Registry{{SecretRef: "REGISTRY_SENTINEL"}}}}}
	called := 0
	rt := &Runtime{active: &active{repo: repo}, opts: Options{HostSecretRefs: []string{"WIZARD_GIT_SENTINEL"}, Secrets: func(n string) (string, bool) { called++; return "operator-only sentinel value", true }}}
	declaration := &config.LoadedPlugin{Manifest: &config.Plugin{Spec: config.PluginSpec{Secrets: []config.PluginSecret{{Name: "FIELD", Hosts: []string{"api.example.org"}}}}}}
	in := config.Instance{Plugin: declaration}
	for _, name := range []string{"CICORD_PRIVACY_KEY", "CICORD_ALERT_WEBHOOK", "CICORD_REGISTRY_SIGNING_KEY", "GITHUB_TOKEN", "DISCORD_SECOND_SENTINEL", "REGISTRY_SENTINEL", "WIZARD_GIT_SENTINEL", "CICORD_PLUGIN_" + strings.Repeat("0", 64), "AWS_SECRET_ACCESS_KEY", "PGPASSWORD"} {
		in.SecretRefs = map[string]config.TokenRef{"FIELD": {SecretRef: name}}
		if v, _, ok := rt.instanceSecret(in, "FIELD"); ok || v != "" || called != 0 {
			t.Fatalf("reserved binding %s reached the operator resolver", name)
		}
	}
	in.SecretRefs = map[string]config.TokenRef{"FIELD": {SecretRef: "CICORD_APPROVED_PROVIDER"}}
	if _, _, ok := rt.instanceSecret(in, "FIELD"); !ok || called != 1 {
		t.Fatal("approved provider binding did not resolve")
	}
}
