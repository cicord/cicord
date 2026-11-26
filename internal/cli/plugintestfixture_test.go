package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"gopkg.in/yaml.v3"
)

func TestPluginTestServerFixture(t *testing.T) {
	for _, tc := range []struct {
		name, fixture string
		errorExpected bool
	}{
		{"default", "", false},
		{"explicit", "syncPolicy:\n  selfHeal: true\n  deletionProtection: false\n", false},
		{"unsupported", "syncPolicy:\n  prune: true\n", true},
		{"other server data", "discordId: spoof\n", true},
		{"missing switch", "syncPolicy: {}\n", true},
		{"empty list", "privateChannels: []", true},
		{"duplicate channel", "privateChannels: [audit, audit]", true},
		{"excess channels", "privateChannels:\n" + strings.Repeat("  - audit\n", 33), true},
		{"second document", "syncPolicy: { selfHeal: false }\n---\nsyncPolicy: { selfHeal: true }\n", true},
		{"limit", strings.Repeat(" ", 4097), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.fixture != "" {
				if err := os.Mkdir(filepath.Join(dir, "test"), 0755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(dir, "test", "server.yaml"), tc.fixture)
			}
			fixture, err := readTestServerFixture(dir)
			if (err != nil) != tc.errorExpected {
				t.Fatalf("readTestServerFixture() = %v, %v", fixture, err)
			}
			if err != nil {
				return
			}
			repo := t.TempDir()
			if err := writeTestRepo(repo, "pilot", &config.Plugin{}, nil, nil, fixture); err != nil {
				t.Fatal(err)
			}
			var server config.Server
			if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(repo, "servers", "test.yaml"))), &server); err != nil {
				t.Fatal(err)
			}
			if server.Spec.SyncPolicy.SelfHeal() != (tc.name == "explicit") || server.Spec.SyncPolicy.DeletionProtection() != (tc.name != "explicit") {
				t.Errorf("unexpected synthetic policy %+v", server.Spec.SyncPolicy)
			}
		})
	}
}

func TestPluginTestPrivateChannels(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "test"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "test", "server.yaml"), "privateChannels: [audit]\n")
	fixture, err := readTestServerFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]map[string]bool{"channel": {"audit": true, "public": true}}
	repo := t.TempDir()
	if err := writeTestRepo(repo, "pilot", &config.Plugin{}, nil, refs, fixture); err != nil {
		t.Fatal(err)
	}
	var server config.Server
	if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(repo, "servers", "test.yaml"))), &server); err != nil {
		t.Fatal(err)
	}
	audit := server.Spec.Channels["audit"].Overwrites
	everyone := audit["@everyone"]
	if len(audit) != 1 || len(everyone.Allow) != 0 || !slices.Equal(everyone.Deny, []string{"ViewChannel", "ReadMessageHistory"}) {
		t.Fatalf("unexpected audit channel overwrites: %#v", audit)
	}
	for _, name := range []string{"general", "public"} {
		if len(server.Spec.Channels[name].Overwrites) != 0 {
			t.Errorf("unexpected overwrites for %s", name)
		}
	}
	for _, bad := range []string{"missing", "general", "*", "category"} {
		refs["category"] = map[string]bool{"category": true}
		refs["channel"]["category"] = true
		err := writeTestRepo(t.TempDir(), "pilot", &config.Plugin{}, nil, refs, &testServerFixture{PrivateChannels: []string{bad}})
		if err == nil || !strings.Contains(err.Error(), "configured synthetic text channel") {
			t.Errorf("privateChannels %q: %v", bad, err)
		}
	}
}
