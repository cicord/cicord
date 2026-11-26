package runtime

import (
	"fmt"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextMenuDispatch(t *testing.T) {
	s := startShowcase(t, showcaseOpts{prepare: func(dir string) {
		path := filepath.Join(dir, "plugins/tickets/src/index.ts")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := strings.Replace(string(b), "definePlugin({", `definePlugin({contextMenus:{"About user":{type:"user",permissions:["ManageMessages"],async run(ctx,i){await i.reply(i.target.displayName)}}},`, 1)
		if err = os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}})
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
