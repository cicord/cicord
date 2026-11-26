package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionedFiles(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"docs/nested", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"docs/a.md", "docs/nested/b.md", "docs/nested/c.txt", ".git/config"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(p), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := LoadInstanceFiles(root, []string{"docs/**/*.md"})
	if err != nil || len(files) != 2 || string(files["docs/a.md"]) != "docs/a.md" {
		t.Fatalf("snapshot: %v %v", files, err)
	}
	if err := os.Symlink(filepath.Join(root, ".git/config"), filepath.Join(root, "docs/linked.md")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"docs/*.md", "../*.md", ".git/**", "/etc/passwd"} {
		if _, err := LoadInstanceFiles(root, []string{p}); err == nil {
			t.Errorf("accepted %s", p)
		}
	}
}
func TestReviewedNetworkConfig(t *testing.T) {
	schema := map[string]any{"x-cicord-network-url": true}
	for _, u := range []string{"https://private.example.org", "https://ok.example.org:8080/x", "http://ok.example.org", "https://a:b@ok.example.org"} {
		if len(checkNetworkURLs(schema, schema, u, []string{"ok.example.org"}, "", 0)) == 0 {
			t.Error(u)
		}
	}
	if !networkAllows("https://ok.example.org/path", []string{"*.example.org"}) {
		t.Fatal("wildcard")
	}
}

func TestReservedPhysicalSecretReferences(t *testing.T) {
	repo := &Repo{Bots: map[string]*Bot{"one": {Spec: BotSpec{Token: TokenRef{SecretRef: "DISCORD_GATEWAY"}}}}}
	for _, name := range []string{"DISCORD_GATEWAY", "CICORD_SECRET_KEY", "CICORD_REGISTRY_PRIVATE_TOKEN", "CICORD_PLUGIN_" + strings.Repeat("A", 64)} {
		if !repo.ReservedPluginRef(name) {
			t.Errorf("host reference permitted: %s", name)
		}
	}
	if repo.ReservedPluginRef("CICORD_AI_ANTHROPIC") {
		t.Fatal("ordinary provider binding refused")
	}
}

func TestProviderBudgetSchemaDefault(t *testing.T) {
	_, schema, err := compilePluginConfigSchema("urn:test:budget", []byte(`{"type":"object","properties":{"tokens":{"type":"integer","default":500000}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	ApplyDefaults(schema, cfg)
	decl := PluginSecret{Budget: &ProviderBudget{DailyLimitConfig: "tokens"}}
	n, ok := decl.BudgetLimit(cfg)
	if !ok || n != 500000 {
		t.Fatalf("default (%T) was rejected", cfg["tokens"])
	}
}

func TestPluginConfigSchemaReferencesStayInDocument(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sibling.json")
	if err := os.WriteFile(file, []byte(`{"type":"string"}`), 0644); err != nil {
		t.Fatal(err)
	}
	base := "file://" + filepath.ToSlash(filepath.Join(dir, "config.json"))
	if _, _, err := compilePluginConfigSchema(base, []byte(`{"$ref":"sibling.json"}`)); err == nil {
		t.Fatal("schema compiler loaded undeclared sibling")
	}
	if _, _, err := compilePluginConfigSchema(base, []byte(`{"$defs":{"name":{"type":"string"}},"$ref":"#/$defs/name"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestValidationRejectsReservedCredentialBindings(t *testing.T) {
	dir := copyExample(t)
	bot := filepath.Join(dir, "bots", "moderator", "bot.yaml")
	manifest := filepath.Join(dir, "plugins", "welcome", "plugin.yaml")
	edit(t, manifest, "spec:\n", "spec:\n  secrets:\n    - {name: STAFF_HOOK, use: webhook.send, hosts: [discord.com, hooks.slack.com]}\n")
	edit(t, bot, "source: ../../plugins/welcome", "source: ../../plugins/welcome\n      secrets: {STAFF_HOOK: {secretRef: INIT_SENTINEL}}")
	prev := "INIT_SENTINEL"
	for _, name := range []string{"CICORD_PRIVACY_KEY", "CICORD_ALERT_WEBHOOK", "DISCORD_TOKEN_MODERATOR", "CICORD_REGISTRY_SIGNING_KEY", "CUSTOM_OPERATOR_GIT"} {
		edit(t, bot, "STAFF_HOOK: {secretRef: "+prev+"}", "STAFF_HOOK: {secretRef: "+name+"}")
		prev = name
		_, ds := Load(dir, LoadOptions{HostSecretRefs: []string{"CUSTOM_OPERATOR_GIT"}})
		if !ds.HasErrors() || !strings.Contains(ds.Err().Error(), "reserved for the host") {
			t.Fatalf("expected reserved-binding diagnostic for %s", name)
		}
	}
	// Only the plugin namespace is bindable: other process variables are refused.
	for _, name := range []string{"AWS_SECRET_ACCESS_KEY", "PGPASSWORD", "CICORD_"} {
		edit(t, bot, "STAFF_HOOK: {secretRef: "+prev+"}", "STAFF_HOOK: {secretRef: "+name+"}")
		prev = name
		_, ds := Load(dir, LoadOptions{})
		if !ds.HasErrors() || !strings.Contains(ds.Err().Error(), "must start with CICORD_") {
			t.Fatalf("expected namespace diagnostic for %s: %v", name, ds.Err())
		}
	}
	edit(t, bot, "STAFF_HOOK: {secretRef: "+prev+"}", "STAFF_HOOK: {secretRef: CICORD_STAFF_WEBHOOK}")
	_, ds := Load(dir, LoadOptions{})
	if ds.HasErrors() {
		t.Fatal(ds.Err())
	}
	edit(t, manifest, "[discord.com, hooks.slack.com]", "[other.example.org]")
	_, ds = Load(dir, LoadOptions{})
	if !ds.HasErrors() {
		t.Fatal("arbitrary webhook destination approved")
	}
}
