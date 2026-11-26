package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/secrets"
)

func TestPluginSecretWriteOnly(t *testing.T) {
	var store *secrets.Store
	var resolve func(string) (string, bool)
	e := startFull(t, func(dir string) {
		for p, sub := range map[string][2]string{
			"bots/moderator/bot.yaml":     {"source: ../../plugins/welcome", "source: ../../plugins/welcome\n      secrets: {API_KEY: {secretRef: FIXTURE_BINDING}}"},
			"plugins/welcome/plugin.yaml": {"spec:\n", "spec:\n  secrets:\n    - {name: API_KEY, required: true, hosts: [api.example.org]}\n"},
		} {
			path := filepath.Join(dir, p)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), sub[0]) {
				t.Fatal(p)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(data), sub[0], sub[1], 1)), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}, func(o *runtime.Options) {
		var err error
		store, err = secrets.Open(t.Context(), o.Store, make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		resolve = secrets.Resolver(store, o.Secrets)
		o.Secrets = resolve
	}, func(o *Options) { o.SecretStore = store; o.Secret = resolve })
	const marker = "synthetic value used only by the test"
	path := "/api/v1/servers/" + serverKey + "/plugins/moderator/welcome"
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	mod := e.client()
	mod.login(e.moderator().ID)
	var before pluginItemDTO
	admin.expect(200, "GET", path, nil).json(t, &before)
	if before.Health != "Degraded" || len(before.Secrets) != 1 || !before.Secrets[0].Bound {
		t.Fatalf("expected bound, missing credential")
	}
	body := map[string]string{"value": marker}
	mod.expect(403, "PUT", path+"/secrets/API_KEY", body)
	admin.expect(404, "PUT", path+"/secrets/UNDECLARED", body)
	admin.expect(204, "PUT", path+"/secrets/API_KEY", body)
	after := admin.expect(200, "GET", path, nil)
	var detail pluginItemDTO
	after.json(t, &detail)
	if detail.Health != "Healthy" || strings.Contains(string(after.body), marker) {
		t.Fatal("credential health or redaction")
	}
	if _, ok := resolve("FIXTURE_BINDING"); ok {
		t.Fatal("admin overwrote external binding")
	}
}

func TestAdminSecretBindingDoesNotProbeOperatorSentinels(t *testing.T) {
	s := &server{rt: runtime.New(runtime.Options{HostSecretRefs: []string{"OPERATOR_REPO_PAT"}})}
	probed := 0
	s.opts.Secret = func(string) (string, bool) { probed++; return "synthetic-probe-value", true }
	for _, physical := range []string{"CICORD_PRIVACY_KEY", "CICORD_ALERT_WEBHOOK", "OPERATOR_REPO_PAT"} {
		c := config.Instance{SecretRefs: map[string]config.TokenRef{"WEBHOOK_URL": {SecretRef: physical}}}
		bound, external := s.pluginSecretBinding(c, "WEBHOOK_URL")
		if bound || external || probed != 0 {
			t.Fatal("the admin path probed a host-owned credential")
		}
	}
}
