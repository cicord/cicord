package cli

import (
	"os"
	"path/filepath"
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
			policy, err := readTestSyncPolicy(dir)
			if (err != nil) != tc.errorExpected {
				t.Fatalf("readTestSyncPolicy() = %v, %v", policy, err)
			}
			if err != nil {
				return
			}
			repo := t.TempDir()
			if err := writeTestRepo(repo, "pilot", &config.Plugin{}, nil, nil, policy); err != nil {
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
