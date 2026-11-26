package runtime

import (
	"encoding/json"
	"fmt"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestContextMenuDispatch(t *testing.T) {
	s := startShowcase(t, showcaseOpts{prepare: func(dir string) {
		// polls declares no context menu of its own (tickets adds its own contextMenus key,
		// which would replace this one): it carries the test menu and its French label.
		plugin := filepath.Join(dir, "plugins/polls")
		if b, err := os.ReadFile(filepath.Join(plugin, "plugin.yaml")); err != nil || !strings.Contains(string(b), "\n  locales: ./locales") {
			t.Fatalf("polls declares no locales: %v", err)
		}
		fr := filepath.Join(plugin, "locales/fr.json")
		raw, err := os.ReadFile(fr)
		if err != nil {
			t.Fatal(err)
		}
		catalog := map[string]any{}
		if err := json.Unmarshal(raw, &catalog); err != nil {
			t.Fatal(err)
		}
		catalog["contextMenus.About user.name"] = "À propos du membre"
		if raw, err = json.Marshal(catalog); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fr, raw, 0644); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(plugin, "src/index.ts")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(b), "definePlugin({") != 1 || strings.Contains(string(b), "contextMenus") {
			t.Fatal("polls: update the context menu fixture")
		}
		source := strings.Replace(string(b), "definePlugin({", `definePlugin({contextMenus:{"About user":{type:"user",permissions:["ManageMessages"],async run(ctx,i){await i.reply(i.target.displayName)}}},`, 1)
		if err = os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}})
	registered := s.world.Commands(s.appID, s.serverID)
	if !slices.ContainsFunc(registered, func(c discord.Command) bool {
		return c.Type == discord.UserMenu && c.Name == "About user" && c.Names["fr"] == "À propos du membre"
	}) {
		t.Fatal("French display label missing from registered guild commands")
	}
	ch := s.channel("general")
	h := s.world.InvokeContextMenu(s.owner.ID, s.serverID, ch.ID, s.appID, "About user", discord.UserMenu, s.owner.ID)
	st := wait(t, h)
	if st.Message == nil || st.Message.Content != s.owner.DisplayName() {
		t.Fatalf("wrong target: %+v", st)
	}
}

func TestMenuLimitsPerBotAndServer(t *testing.T) {
	lp := &config.LoadedPlugin{Key: "menus"}
	repo := &config.Repo{Bots: map[string]*config.Bot{"bot": {File: "bots/bot/bot.yaml"}}}
	builds := map[string]*builtPlugin{}
	for i := 0; i < 16; i++ {
		name := fmt.Sprintf("plugin-%02d", i)
		copy := *lp
		copy.Key = name
		builds[name] = &builtPlugin{def: &plugin.Definition{ContextMenus: []plugin.CommandDef{{Name: name, Type: discord.UserMenu}, {Name: name, Type: discord.MessageMenu}}}}
		server := "a"
		if i == 15 {
			server = "b"
		}
		repo.Instances = append(repo.Instances, config.Instance{Bot: "bot", Server: server, Name: name, Plugin: &copy})
	}
	if ds := validateRepoCommands(repo, builds); ds.HasErrors() {
		t.Fatal(ds)
	}
	repo.Instances[15].Server = "a"
	ds := validateRepoCommands(repo, builds)
	if len(ds) != 2 || !strings.Contains(ds[0].Message, "plugin-00") || !strings.Contains(ds[0].Message, "plugin-15") {
		t.Fatal(ds)
	}
}

func TestLocalizedMenuCollisionBetweenInstallations(t *testing.T) {
	a := &config.LoadedPlugin{Key: "first"}
	b := &config.LoadedPlugin{Key: "second"}
	repo := &config.Repo{Bots: map[string]*config.Bot{"bot": {File: "bot.yaml"}}, Instances: []config.Instance{
		{Bot: "bot", Server: "one", Name: "staff", Plugin: a}, {Bot: "bot", Server: "one", Name: "safety", Plugin: b},
	}}
	menu := func(name, tag, label string) *builtPlugin {
		return &builtPlugin{def: &plugin.Definition{ContextMenus: []plugin.CommandDef{{
			Name: name, Type: discord.MessageMenu, CommandLabels: discord.CommandLabels{Names: map[string]string{tag: label}},
		}}}}
	}
	builds := map[string]*builtPlugin{"first": menu("Open ticket", "fr", "Signaler ce message"), "second": menu("Report message", "fr", "Signaler ce message")}
	ds := validateRepoCommands(repo, builds)
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "staff") || !strings.Contains(ds[0].Message, "safety") || !strings.Contains(ds[0].Message, "fr") {
		t.Fatalf("missing localized collision: %+v", ds)
	}
	builds["second"] = menu("Report message", "de", "Signaler ce message")
	if ds = validateRepoCommands(repo, builds); ds.HasErrors() {
		t.Fatalf("different locales: %+v", ds)
	}
	builds["second"] = menu("Report message", "fr", "Signaler ce message")
	repo.Instances[1].Server = "other"
	if ds = validateRepoCommands(repo, builds); ds.HasErrors() {
		t.Fatalf("different servers: %+v", ds)
	}
}
