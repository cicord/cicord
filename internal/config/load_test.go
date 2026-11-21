package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exampleDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs("../../examples/basic")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLoadExample(t *testing.T) {
	repo, ds := Load(exampleDir(t), LoadOptions{EngineVersion: "0.1.0"})
	if ds.HasErrors() {
		t.Fatalf("unexpected errors:\n%v", ds.Err())
	}
	if repo.Project.Metadata.Name != "ma-commu" {
		t.Errorf("project name = %q", repo.Project.Metadata.Name)
	}
	srv := repo.Servers["ma-commu"]
	if srv == nil || len(srv.Spec.Roles) < 3 || len(srv.Spec.Channels) < 5 || len(srv.Spec.Categories) < 3 {
		t.Fatalf("server not loaded correctly: %+v", srv)
	}
	if !srv.Spec.SyncPolicy.DeletionProtection() || srv.Spec.SyncPolicy.Prune() {
		t.Errorf("sync policy defaults wrong")
	}
	if !srv.Spec.Channels["general"].SyncsWithCategory() {
		t.Errorf("general should sync with its category")
	}
	var inst Instance
	for _, in := range repo.Instances {
		if in.Name == "welcome" {
			inst = in
		}
	}
	if inst.Name == "" {
		t.Fatalf("welcome instance missing: %+v", repo.Instances)
	}
	if inst.ID() != "moderator/welcome/ma-commu" {
		t.Errorf("instance id = %q", inst.ID())
	}
	if got := inst.Config["message"]; !strings.HasPrefix(got.(string), "Salut") {
		t.Errorf("override not applied: %v", got)
	}
	if inst.Config["channel"] != "general" || inst.Config["dmOnboarding"] != true {
		t.Errorf("config = %v", inst.Config)
	}
	// privileged intent warning
	var warned bool
	for _, d := range ds {
		if d.Severity == SeverityWarning && strings.Contains(d.Message, "ServerMembers") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a privileged intent warning, got %v", ds)
	}
}

// copyExample copies examples/basic into a temp dir so tests can mutate it.
func copyExample(t *testing.T) string {
	t.Helper()
	src := exampleDir(t)
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func edit(t *testing.T, file, old, new string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", file, old)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name  string
		file  string
		old   string
		new   string
		want  string
		wantL int // expected line (0 = don't check)
	}{
		{"unknown category", "servers/ma-commu.yaml", "category: community\n      topic", "category: nope\n      topic", `unknown category "nope"`, 0},
		{"unknown role in hierarchy", "servers/ma-commu.yaml", "hierarchy: [admin, modo, member", "hierarchy: [admin, modo, ghost", `unknown role "ghost"`, 0},
		{"unknown overwrite role", "servers/ma-commu.yaml", "        modo:\n          allow: [ViewChannel]", "        ghost:\n          allow: [ViewChannel]", `unknown role "ghost"`, 0},
		{"bad permission", "servers/ma-commu.yaml", "permissions: [Administrator]", "permissions: [Godmode]", "/spec/roles/admin/permissions/0", 0},
		{"key mismatch", "servers/ma-commu.yaml", "  name: ma-commu", "  name: other", "must match the file name", 0},
		{"missing grant", "bots/moderator/bot.yaml", "        - storage.kv\n", "", "not granted: storage.kv", 0},
		{"unknown server", "bots/moderator/bot.yaml", "servers: [ma-commu]", "servers: [ghost]", `unknown server "ghost"`, 0},
		{"config schema", "bots/moderator/bot.yaml", "dmOnboarding: true", "dmOnboarding: yes-please", "dmOnboarding", 0},
		{"config ref", "bots/moderator/bot.yaml", "channel: general", "channel: nowhere", `unknown channel "nowhere"`, 0},
		{"override ref", "bots/moderator/bot.yaml", "            message:", "            autoRole: ghost\n            message:", `[server ma-commu] unknown role "ghost"`, 0},
		{"secret in token", "bots/moderator/bot.yaml", "secretRef: DISCORD_TOKEN_MODERATOR", "secretRef: my-token", "/spec/token/secretRef", 0},
		{"engine", "codi.yaml", `engine: ">=0.1.0 <0.2.0"`, `engine: ">=9.0.0"`, "requires CODI", 0},
		{"plugin capability unknown", "plugins/welcome/plugin.yaml", "    - storage.kv", "    - storage.everything", "/spec/capabilities", 0},
		{"wrong kind", "servers/ma-commu.yaml", "kind: Server", "kind: Guild", `expected kind "Server"`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyExample(t)
			edit(t, filepath.Join(dir, tc.file), tc.old, tc.new)
			_, ds := Load(dir, LoadOptions{EngineVersion: "0.1.0"})
			if !ds.HasErrors() {
				t.Fatalf("expected an error containing %q, got none", tc.want)
			}
			var found bool
			for _, d := range ds.Errors() {
				if strings.Contains(d.String(), tc.want) {
					found = true
					if d.Line == 0 && strings.HasSuffix(d.File, ".yaml") {
						t.Errorf("diagnostic without line: %s", d)
					}
				}
			}
			if !found {
				t.Fatalf("no error containing %q in:\n%v", tc.want, ds.Err())
			}
		})
	}
}

func TestMergePatchAndDefaults(t *testing.T) {
	base := map[string]any{"a": 1.0, "nested": map[string]any{"x": "1", "y": "2"}, "gone": true}
	patch := map[string]any{"nested": map[string]any{"y": "3"}, "gone": nil, "new": "n"}
	got := MergePatch(cloneMap(base), patch).(map[string]any)
	if got["a"] != 1.0 || got["new"] != "n" || got["nested"].(map[string]any)["x"] != "1" || got["nested"].(map[string]any)["y"] != "3" {
		t.Errorf("merge = %v", got)
	}
	if _, ok := got["gone"]; ok {
		t.Errorf("null should delete")
	}
	if base["nested"].(map[string]any)["y"] != "2" {
		t.Errorf("base mutated")
	}

	schema := map[string]any{"properties": map[string]any{
		"msg":  map[string]any{"type": "string", "default": "hi"},
		"set":  map[string]any{"type": "string", "default": "nope"},
		"opts": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"default": 3.0}}},
	}}
	cfg := map[string]any{"set": "yes"}
	ApplyDefaults(schema, cfg)
	if cfg["msg"] != "hi" || cfg["set"] != "yes" || cfg["opts"].(map[string]any)["n"] != 3.0 {
		t.Errorf("defaults = %v", cfg)
	}
}

func TestTruncatedStringWarning(t *testing.T) {
	dir := copyExample(t)
	edit(t, filepath.Join(dir, "bots/moderator/bot.yaml"), `message: "Bienvenue {member} sur {server} ! 👋"`, `message: Bienvenue, passe dans #général`)
	_, ds := Load(dir, LoadOptions{})
	for _, d := range ds {
		if d.Severity == SeverityWarning && strings.Contains(d.Message, "YAML reads as a comment") && d.Line > 0 {
			return
		}
	}
	t.Fatalf("expected a truncation warning, got %v", ds)
}
