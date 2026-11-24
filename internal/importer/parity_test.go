package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/reconcile"
	"github.com/npjonath/cicord/internal/sim"
	"github.com/npjonath/cicord/internal/state/sqlite"
)

// TestImportParityRoundTrip: a server configured by hand with every parity block
// (Community, onboarding, the three message AutoMod triggers, welcome screen, gradient
// role, forum tags in non-alphabetical order…) is imported, validated and planned: the
// plan is empty.
func TestImportParityRoundTrip(t *testing.T) {
	ctx := context.Background()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Parity", owner.ID)
	token, bot, _ := w.CreateBot("cicord")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	if err := w.SetBoosts(srv.ID, 7); err != nil {
		t.Fatal(err)
	}
	cl := w.AdminClient(srv.ID)
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ptr := func(s string) *string { return &s }
	gamer, err := cl.CreateRole(ctx, srv.ID, discord.RoleParams{Name: ptr("Gamer"), Colors: &discord.RoleColors{Primary: 0x112233, Secondary: 0x445566}}, "")
	if err != nil {
		t.Fatal(err)
	}
	must(cl.UpdateRole(ctx, srv.ID, gamer.ID, discord.RoleParams{UnicodeEmoji: ptr("🎮")}, ""))
	var chans []discord.Channel
	for _, n := range []string{"rules", "mod-news", "general", "c1", "c2", "c3", "c4"} {
		c, err := cl.CreateChannel(ctx, srv.ID, discord.ChannelParams{Name: ptr(n), Type: discord.ChannelText}, "")
		if err != nil {
			t.Fatal(err)
		}
		chans = append(chans, c)
	}
	must(cl.UpdateServer(ctx, srv.ID, discord.ServerParams{VerificationLevel: ptr("low"), ExplicitContentFilter: ptr("allMembers"),
		RulesChannelID: &chans[0].ID, PublicUpdatesChannelID: &chans[1].ID, SafetyAlertsChannelID: &chans[1].ID,
		Features: &[]string{discord.FeatureCommunity}}))
	forum, err := cl.CreateChannel(ctx, srv.ID, discord.ChannelParams{Name: ptr("help"), Type: discord.ChannelForum,
		Tags: &[]discord.ForumTag{{Name: "Solved", Moderated: true}, {Name: "Question", Emoji: "❓"}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	must(cl.CreateAutoModRule(ctx, srv.ID, discord.AutoModRule{Name: "Words", Trigger: discord.AutoModKeyword, Keywords: []string{"darn"},
		Enabled: true, Actions: []discord.AutoModAction{{Type: discord.AutoModActionBlock}, {Type: discord.AutoModActionAlert, ChannelID: chans[1].ID}}}, ""))
	must(cl.CreateAutoModRule(ctx, srv.ID, discord.AutoModRule{Name: "Mentions", Trigger: discord.AutoModMentionSpam, MentionLimit: 5,
		Enabled: true, Actions: []discord.AutoModAction{{Type: discord.AutoModActionTimeout, DurationSeconds: 3600}}}, ""))
	must(cl.CreateAutoModRule(ctx, srv.ID, discord.AutoModRule{Name: "Presets", Trigger: discord.AutoModKeywordPreset,
		Presets: []string{"profanity"}, AllowList: []string{"heck"}, Enabled: false, ExemptRoles: []string{gamer.ID},
		Actions: []discord.AutoModAction{{Type: discord.AutoModActionBlock, CustomMessage: "No"}}}, ""))
	must(cl.UpdateWelcomeScreen(ctx, srv.ID, discord.WelcomeScreen{Enabled: true, Description: "Hi",
		Channels: []discord.WelcomeChannel{{ChannelID: chans[0].ID, Description: "Rules", Emoji: "📜"}}}, ""))
	var defaults []string
	for _, c := range chans {
		defaults = append(defaults, c.ID)
	}
	must(cl.UpdateOnboarding(ctx, srv.ID, discord.Onboarding{Enabled: true, Mode: "default", DefaultChannelIDs: defaults,
		Prompts: []discord.OnboardingPrompt{{Type: "multipleChoice", Title: "Games?", InOnboarding: true,
			Options: []discord.OnboardingOption{{Title: "Yes", Emoji: "🎮", RoleIDs: []string{gamer.ID}, ChannelIDs: []string{forum.ID}}}}}}, ""))
	must(cl.UpdateServerWidget(ctx, srv.ID, discord.ServerWidget{Enabled: true, ChannelID: chans[2].ID}, ""))

	sess, err := w.Connect(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := reconcile.FetchWith(ctx, sess, srv.ID, "", reconcile.FetchAll)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Generate(context.Background(), Options{ServerKey: "parity", Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"community:", "onboarding:", "welcomeScreen:", "trigger: mentionSpam", "trigger: keywordPreset",
		"enabled: false", "timeout: 1h", "secondary: '#445566'", "moderated: true", "widget:"} {
		if !strings.Contains(string(res.YAML), want) {
			t.Errorf("YAML misses %q:\n%s", want, res.YAML)
		}
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "servers"), 0o755)
	os.WriteFile(filepath.Join(dir, "cicord.yaml"), []byte("apiVersion: cicord.dev/v1alpha1\nkind: Project\nmetadata:\n  name: p\nspec:\n  engine: \">=0.0.0\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "servers", "parity.yaml"), res.YAML, 0o644)
	repo, diags := config.Load(dir, config.LoadOptions{})
	if repo == nil || diags.HasErrors() {
		t.Fatalf("imported YAML does not validate: %v\n%s", diags.Errors(), res.YAML)
	}
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, r := range res.Resources {
		if err := store.Resources().Put(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rec := reconcile.New(reconcile.Options{Server: repo.Servers["parity"], RepoRoot: dir, Client: sess,
		Resources: store.Resources(), KV: store.KV("reconcile:parity"), BotUserID: bot.ID})
	defer rec.Close()
	p, err := rec.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() || len(p.Blocked) != 0 {
		t.Fatalf("plan after import is not empty:\n%s\n%s", p, res.YAML)
	}
}
