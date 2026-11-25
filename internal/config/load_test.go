package config

import (
	"fmt"
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
	if repo.Project.Metadata.Name != "my-community" {
		t.Errorf("project name = %q", repo.Project.Metadata.Name)
	}
	srv := repo.Servers["my-community"]
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
	if inst.ID() != "moderator/welcome/my-community" {
		t.Errorf("instance id = %q", inst.ID())
	}
	if got := inst.Config["message"]; !strings.HasPrefix(got.(string), "Hi ") {
		t.Errorf("override not applied: %v", got)
	}
	if inst.Config["channel"] != "general" || inst.Config["dmOnboarding"] != true {
		t.Errorf("config = %v", inst.Config)
	}
	// privileged intent warning
	var warned bool
	for _, d := range ds {
		if d.Severity == SeverityWarning && d.Code == CodePrivilegedIntent && strings.Contains(d.Message, "ServerMembers") &&
			strings.Contains(d.Message, "make sure it is enabled") {
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

// A pre-release engine is checked by semver precedence (0.2.0-rc.1 sorts between 0.1.x and
// 0.2.0): it satisfies the example's ">=0.1.0 <0.2.0" project range and the plugins'
// ">=0.1.0", and not a range starting at the release it precedes.
func TestEnginePrerelease(t *testing.T) {
	engineErrors := func(dir, engine string) []string {
		_, ds := Load(dir, LoadOptions{EngineVersion: engine})
		var out []string
		for _, d := range ds.Errors() {
			if strings.Contains(d.String(), "requires Cicord") {
				out = append(out, d.String())
			}
		}
		return out
	}
	for _, v := range []string{"0.2.0-rc.1", "0.1.1-rc.2", "0.1.1+build.5"} {
		if errs := engineErrors(exampleDir(t), v); len(errs) > 0 {
			t.Errorf("engine %s: %v", v, errs)
		}
	}
	dir := copyExample(t)
	edit(t, filepath.Join(dir, "cicord.yaml"), `engine: ">=0.1.0 <0.2.0"`, `engine: ">=0.2.0 <0.3.0"`)
	edit(t, filepath.Join(dir, "plugins/welcome/plugin.yaml"), `engine: ">=0.1.0"`, `engine: ">=0.2.0"`)
	errs := strings.Join(engineErrors(dir, "0.2.0-rc.1"), "\n")
	for _, want := range []string{"this repository requires Cicord >=0.2.0 <0.3.0, running 0.2.0-rc.1", "plugin requires Cicord >=0.2.0, running 0.2.0-rc.1"} {
		if !strings.Contains(errs, want) {
			t.Errorf("no %q in:\n%s", want, errs)
		}
	}
	if errs := engineErrors(dir, "0.2.0"); len(errs) > 0 {
		t.Errorf("engine 0.2.0: %v", errs)
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
		{"unknown category", "servers/my-community.yaml", "category: community\n      topic", "category: nope\n      topic", `unknown category "nope"`, 0},
		{"unknown role in hierarchy", "servers/my-community.yaml", "hierarchy: [admin, moderator, member", "hierarchy: [admin, moderator, ghost", `unknown role "ghost"`, 0},
		{"unknown overwrite role", "servers/my-community.yaml", "        moderator:\n          allow: [ViewChannel]", "        ghost:\n          allow: [ViewChannel]", `unknown role "ghost"`, 0},
		{"bad permission", "servers/my-community.yaml", "permissions: [Administrator]", "permissions: [Godmode]", "/spec/roles/admin/permissions/0", 0},
		{"key mismatch", "servers/my-community.yaml", "  name: my-community", "  name: other", "must match the file name", 0},
		{"missing grant", "bots/moderator/bot.yaml", "        - storage.kv\n", "", "not granted: storage.kv", 0},
		{"unknown server", "bots/moderator/bot.yaml", "servers: [my-community]", "servers: [ghost]", `unknown server "ghost"`, 0},
		{"config schema", "bots/moderator/bot.yaml", "dmOnboarding: true", "dmOnboarding: yes-please", "dmOnboarding", 0},
		{"config ref", "bots/moderator/bot.yaml", "channel: general", "channel: nowhere", `unknown channel "nowhere"`, 0},
		{"override ref", "bots/moderator/bot.yaml", "            message:", "            autoRole: ghost\n            message:", `[server my-community] unknown role "ghost"`, 0},
		{"secret in token", "bots/moderator/bot.yaml", "secretRef: DISCORD_TOKEN_MODERATOR", "secretRef: my-token", "/spec/token/secretRef", 0},
		{"engine", "cicord.yaml", `engine: ">=0.1.0 <0.2.0"`, `engine: ">=9.0.0"`, "requires Cicord", 0},
		{"plugin capability unknown", "plugins/welcome/plugin.yaml", "    - storage.kv", "    - storage.everything", "/spec/capabilities", 0},
		{"wrong kind", "servers/my-community.yaml", "kind: Server", "kind: Guild", `expected kind "Server"`, 0},
		{"role member not a snowflake", "servers/my-community.yaml", "permissions: [Administrator]", "permissions: [Administrator]\n      members: [\"12345\"]", "/spec/roles/admin/members/0", 0},
		{"role member duplicated", "servers/my-community.yaml", "permissions: [Administrator]", "permissions: [Administrator]\n      members: [\"123456789012345678\", \"123456789012345678\"]", "/spec/roles/admin/members", 0},
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

func TestRoleMembers(t *testing.T) {
	dir := copyExample(t)
	edit(t, filepath.Join(dir, "servers/my-community.yaml"), "permissions: [Administrator]", "permissions: [Administrator]\n      members: [\"123456789012345678\", \"98765432109876543\"]")
	repo, ds := Load(dir, LoadOptions{EngineVersion: "0.1.0"})
	if ds.HasErrors() {
		t.Fatal(ds.Err())
	}
	got := repo.Servers["my-community"].Spec.Roles["admin"].Members
	if len(got) != 2 || got[0] != "123456789012345678" || got[1] != "98765432109876543" {
		t.Errorf("members = %v", got)
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
	edit(t, filepath.Join(dir, "bots/moderator/bot.yaml"), `message: "Welcome {member} to {server}! 👋"`, `message: Welcome, come to #general`)
	_, ds := Load(dir, LoadOptions{})
	for _, d := range ds {
		if d.Severity == SeverityWarning && strings.Contains(d.Message, "YAML reads as a comment") && d.Line > 0 {
			return
		}
	}
	t.Fatalf("expected a truncation warning, got %v", ds)
}

// Discord onboarding rules that depend on @everyone's permissions are reported before
// Discord refuses them (DEFAULT_CHANNEL_REQUIRES_EVERYONE_ACCESS, chattable channels).
func TestOnboardingEveryoneAccess(t *testing.T) {
	extra := "    c1: {name: c1, type: text, category: community}\n    c2: {name: c2, type: text, category: community}\n    c3: {name: c3, type: text, category: community}\n    staff-chat:\n"
	onboarding := "  onboarding:\n    enabled: true\n    defaultChannels: [rules, announcements, general, voice, c1, c2, c3]\n    prompts:\n      - {key: games, title: Games, options: [{title: Minecraft, roles: [minecraft]}]}\n\n  syncPolicy:"
	load := func(everyone string) []string {
		dir := copyExample(t)
		f := filepath.Join(dir, "servers/my-community.yaml")
		edit(t, f, "    staff-chat:\n", extra)
		edit(t, f, "  syncPolicy:", onboarding)
		edit(t, f, "permissions: [ViewChannel, ReadMessageHistory, AddReactions, UseApplicationCommands]", everyone)
		_, ds := Load(dir, LoadOptions{EngineVersion: "0.1.0"})
		var out []string
		for _, d := range ds.Errors() {
			out = append(out, d.String())
		}
		return out
	}
	errs := strings.Join(load("permissions: [ViewChannel, ReadMessageHistory]"), "\n")
	for _, want := range []string{`@everyone cannot connect to voice channel "voice"`, "needs 5 default channels where @everyone can send messages; 0 here"} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
	// rules and announcements deny SendMessages (category "info"): 5 chattable remain.
	if errs := load("permissions: [ViewChannel, ReadMessageHistory, SendMessages, Connect]"); len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestRegistriesValidation(t *testing.T) {
	dir := copyExample(t)
	p := filepath.Join(dir, "cicord.yaml")
	data, _ := os.ReadFile(p)
	data = append(data, []byte("  registries:\n    - name: cicord\n      url: https://example.org/a.git\n    - name: acme\n      url: https://example.org/b.git\n    - name: acme\n      url: https://EXAMPLE.org/b\n")...)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, ds := Load(dir, LoadOptions{})
	got := fmt.Sprint(ds.Err())
	for _, want := range []string{`"cicord" is reserved`, `duplicate registry name "acme"`, "repeats the URL"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// A registry token can never be another secret (here, a bot token).
	dir = copyExample(t)
	p = filepath.Join(dir, "cicord.yaml")
	data, _ = os.ReadFile(p)
	data = append(data, []byte("  registries:\n    - name: steal\n      url: https://evil.example/r.git\n      secretRef: DISCORD_TOKEN_MODERATOR\n")...)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ds := Load(dir, LoadOptions{}); !strings.Contains(fmt.Sprint(ds.Err()), "/spec/registries/0/secretRef") {
		t.Errorf("secretRef outside the registry namespace accepted: %v", ds.Err())
	}
}
