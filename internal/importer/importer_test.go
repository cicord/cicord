package importer

import (
	"context"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/reconcile"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Général Chat":          "general-chat",
		"  Notifs événements ":  "notifs-evenements",
		"League of Legends":     "league-of-legends",
		"2024 events":           "channel-2024-events",
		"🎮":                     "channel",
		"Straße":                "strasse",
		"a--b__c":               "a-b-c",
		strings.Repeat("x", 80): strings.Repeat("x", 63),
	} {
		if got := Slug(in, "channel"); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
		if !KeyPattern(Slug(in, "channel")) {
			t.Errorf("Slug(%q) is not a valid key", in)
		}
	}
	ns := newNamespace()
	long := strings.Repeat("y", 63)
	for i, want := range []string{"general", "general-2", "general-3"} {
		if got := ns.take("general"); got != want {
			t.Errorf("take #%d = %q", i, got)
		}
	}
	ns.take(long)
	if got := ns.take(long); len(got) > 63 || !strings.HasSuffix(got, "-2") || !KeyPattern(got) {
		t.Errorf("long dedupe = %q", got)
	}
}

func TestGenerate(t *testing.T) {
	sid := "100000000000000000"
	snap := reconcile.Snapshot{
		Server: discord.Server{ID: sid, Name: "Ma Commu", VerificationLevel: "low", DefaultNotifications: "onlyMentions", SystemChannelID: "20"},
		Roles: []discord.Role{
			{ID: sid, Name: "@everyone", Permissions: discord.MustPermissions("ViewChannel")},
			{ID: "11", Name: "Modo", Position: 2, Color: 0xe67e22, Hoist: true},
			{ID: "12", Name: "Bot", Position: 3, Managed: true},
			{ID: "13", Name: "modo", Position: 1},
		},
		Channels: []discord.Channel{
			{ID: "30", Name: "Général", Type: discord.ChannelCategory, Position: 0,
				Overwrites: []discord.Overwrite{{ID: sid, Deny: discord.MustPermissions("SendMessages")}, {ID: "12", Allow: discord.MustPermissions("SendMessages")}}},
			{ID: "20", Name: "general", Type: discord.ChannelText, ParentID: "30", Position: 0,
				Overwrites: []discord.Overwrite{{ID: sid, Deny: discord.MustPermissions("SendMessages")}}},
			{ID: "21", Name: "free", Type: discord.ChannelText, ParentID: "30", Position: 1},
			{ID: "22", Name: "Vocal", Type: discord.ChannelVoice, ParentID: "30", Position: 2, Bitrate: 64000, UserLimit: 5,
				Overwrites: []discord.Overwrite{{ID: "11", Type: discord.OverwriteRole, Allow: discord.MustPermissions("Connect")}}},
		},
	}
	res, err := Generate(context.Background(), Options{Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "ma-commu" {
		t.Errorf("key = %q", res.Key)
	}
	sp := res.Server
	if _, ok := sp.Roles["modo"]; !ok {
		t.Errorf("roles = %v", sp.Roles)
	}
	if _, ok := sp.Roles["modo-2"]; !ok {
		t.Errorf("deduped role missing: %v", sp.Roles)
	}
	if len(sp.Roles) != 2 || strings.Join(sp.Hierarchy, ",") != "modo,modo-2" {
		t.Errorf("hierarchy = %v (managed role must be skipped)", sp.Hierarchy)
	}
	// category key "general" is taken: the channel gets general-2 (shared namespace)
	if _, ok := sp.Categories["general"]; !ok {
		t.Errorf("categories = %v", sp.Categories)
	}
	if c, ok := sp.Channels["general-2"]; !ok || c.Category != "general" || len(c.Overwrites) != 0 || c.SyncWithCategory != nil {
		t.Errorf("synced channel = %+v", c)
	}
	if c := sp.Channels["free"]; c.SyncWithCategory == nil || *c.SyncWithCategory {
		t.Errorf("channel without overwrites in a category with overwrites must not sync: %+v", c)
	}
	if c := sp.Channels["vocal"]; c.UserLimit != 5 || c.Bitrate != 0 || c.Overwrites["modo"].Allow[0] != "Connect" {
		t.Errorf("voice = %+v", c)
	}
	if sp.Profile.SystemChannel != "general-2" || sp.Profile.Name != "Ma Commu" {
		t.Errorf("profile = %+v", sp.Profile)
	}
	if len(res.Resources) != 6 {
		t.Errorf("resources = %+v", res.Resources)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, `role "Bot"`) || !strings.Contains(joined, `overwrite for role "Bot"`) {
		t.Errorf("warnings = %v", res.Warnings)
	}
	y := string(res.YAML)
	for _, want := range []string{`discordId: "100000000000000000"`, `"@everyone":`, "deny: [SendMessages]", "hierarchy: [modo, modo-2]", "syncWithCategory: false"} {
		if !strings.Contains(y, want) {
			t.Errorf("YAML misses %q:\n%s", want, y)
		}
	}
}
