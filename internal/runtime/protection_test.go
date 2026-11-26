package runtime

import (
	"reflect"
	"testing"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

func TestProtectionInventoryFiltersByCallingBot(t *testing.T) {
	policy := plugin.ServerProtection{SelfHeal: true, DeletionProtection: true, Paused: true, Unmanaged: []plugin.ProtectionResource{}}
	spec := config.ServerSpec{Roles: map[string]config.RoleSpec{"moderator": {}}, Channels: map[string]config.Channel{"general": {}}}
	guild := discord.Server{ID: "1", OwnerID: "2"}
	roles := []discord.Role{{ID: "1", Permissions: discord.MustPermissions("ViewChannel")}, {ID: "3", Managed: true, Position: 15}, {ID: "4", Name: "Mod", Position: 7}, {ID: "5", Name: "Untracked"}}
	bot := discord.Member{ServerID: "1", User: discord.User{ID: "9"}, RoleIDs: []string{"3"}}
	channels := []discord.Channel{{ID: "10", ServerID: "1", Name: "general"}, {ID: "11", ServerID: "1", Name: "private", Overwrites: []discord.Overwrite{{Type: discord.OverwriteMember, ID: "9", Deny: discord.MustPermissions("ViewChannel")}}}, {ID: "12", ServerID: "1", Name: "staff case"}, {ID: "13", ServerID: "1", Name: "thread", Type: discord.ChannelPrivateThread}, {ID: "14", ServerID: "1", Name: "lobby"}}
	resources := []state.Resource{{ID: "4", Kind: state.KindRole, Key: "moderator", Owner: state.OwnerGit}, {ID: "10", Kind: state.KindChannel, Key: "general", Owner: state.OwnerGit}, {ID: "12", Kind: state.KindChannel, Owner: "plugin:bot/tickets"}, {ID: "14", Kind: state.KindChannel, Key: "removed-from-yaml", Owner: state.OwnerGit}}
	got := protectionInventory(policy, spec, guild, bot, roles, channels, resources)
	if !got.Known || got.TotalResources != 4 || got.BotTopPosition == nil || *got.BotTopPosition != 15 || !got.Paused || !got.SelfHeal || !got.DeletionProtection {
		t.Fatal(got)
	}
	want := []plugin.ProtectionResource{{Kind: "channel", ID: "14", Name: "lobby"}, {Kind: "role", ID: "5", Name: "Untracked"}}
	if !reflect.DeepEqual(got.Unmanaged, want) {
		t.Fatal(got.Unmanaged)
	}
	bot.RoleIDs = []string{"missing"}
	unknown := protectionInventory(policy, spec, guild, bot, roles, channels, resources)
	if unknown.Known || unknown.TotalResources != 0 || len(unknown.Unmanaged) > 0 || unknown.BotTopPosition != nil {
		t.Fatal(unknown)
	}
}
