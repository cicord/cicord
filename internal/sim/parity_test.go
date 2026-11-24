package sim

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

var (
	pngBytes = []byte("\x89PNG\r\n\x1a\nimage")
	gifBytes = []byte("GIF89a-image")
	oggBytes = []byte("OggS-sound")
)

func errContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}

func (f *fixture) text(t *testing.T, name string) discord.Channel {
	t.Helper()
	c, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr(name), Type: discord.ChannelText}, "")
	isErr(t, err, nil)
	return c
}

// enableCommunity turns COMMUNITY on the proper way and returns the rules/updates channels.
func (f *fixture) enableCommunity(t *testing.T) (rules, updates discord.Channel) {
	t.Helper()
	rules, updates = f.text(t, "rules"), f.text(t, "mod-updates")
	_, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{
		Features: ptr([]string{discord.FeatureCommunity}), RulesChannelID: ptr(rules.ID), PublicUpdatesChannelID: ptr(updates.ID),
		VerificationLevel: ptr("low"), ExplicitContentFilter: ptr("allMembers"),
	})
	isErr(t, err, nil)
	return rules, updates
}

func TestNewServerDefaults(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	s, _ := f.sess.GetServer(ctx, f.srv.ID)
	if s.ExplicitContentFilter != "disabled" || s.AFKTimeout != 300 || s.PreferredLocale != "en-US" ||
		s.PremiumTier != 0 || len(s.Features) != 0 {
		t.Fatalf("server = %+v", s)
	}
}

func TestBoostGating(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	_, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Banner: ptr(pngBytes)})
	isErr(t, err, discord.ErrBadRequest)
	errContains(t, err, "banner requires boost level 2; server is level 0")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Splash: ptr(pngBytes)})
	errContains(t, err, "requires boost level 1")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Icon: ptr(gifBytes)})
	errContains(t, err, "animated icon requires boost level 1")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Icon: ptr(pngBytes)})
	isErr(t, err, nil)

	isErr(t, f.w.SetBoosts(f.srv.ID, 7), nil)
	s, _ := f.w.Server(f.srv.ID)
	if s.PremiumTier != 2 || s.PremiumSubscriptionCount != 7 || !s.HasFeature(discord.FeatureBanner) || !s.HasFeature(discord.FeatureRoleIcons) || s.HasFeature(discord.FeatureAnimatedBanner) {
		t.Fatalf("server = %+v", s)
	}
	s, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Banner: ptr(pngBytes), Splash: ptr(pngBytes), Icon: ptr(gifBytes)})
	isErr(t, err, nil)
	if s.BannerURL == "" || s.SplashURL == "" || !strings.Contains(s.IconURL, "/a_") {
		t.Fatalf("server = %+v", s)
	}
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Banner: ptr(gifBytes)})
	errContains(t, err, "animated banner requires boost level 3")
	s, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Banner: ptr([]byte{})})
	isErr(t, err, nil)
	if s.BannerURL != "" {
		t.Fatal("banner not removed")
	}

	// boosts lost: features go, a partner flag added by SetFeatures stays
	isErr(t, f.w.SetFeatures(f.srv.ID, []string{discord.FeatureBanner}, nil), nil)
	isErr(t, f.w.SetBoosts(f.srv.ID, 0), nil)
	s, _ = f.w.Server(f.srv.ID)
	if s.PremiumTier != 0 || !s.HasFeature(discord.FeatureBanner) || s.HasFeature(discord.FeatureRoleIcons) {
		t.Fatalf("server = %+v", s)
	}
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Banner: ptr(pngBytes)})
	isErr(t, err, nil) // partnered: BANNER without boosts
}

func TestServerSettingsValidation(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	voice, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("AFK"), Type: discord.ChannelVoice}, "")
	isErr(t, err, nil)
	flags := discord.SuppressJoinNotifications | discord.SuppressGuildReminderNotifications
	s, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{
		AFKChannelID: ptr(voice.ID), AFKTimeout: ptr(900), PreferredLocale: ptr("fr"), PremiumProgressBar: ptr(true),
		SystemChannelFlags: &flags, ExplicitContentFilter: ptr("membersWithoutRoles"),
	})
	isErr(t, err, nil)
	if s.AFKChannelID != voice.ID || s.AFKTimeout != 900 || s.PreferredLocale != "fr" || !s.PremiumProgressBar ||
		s.SystemChannelFlags != flags || s.ExplicitContentFilter != "membersWithoutRoles" {
		t.Fatalf("server = %+v", s)
	}
	for _, p := range []discord.ServerParams{
		{AFKTimeout: ptr(120)},
		{AFKChannelID: ptr(f.general.ID)},
		{SystemChannelID: ptr(voice.ID)},
		{PreferredLocale: ptr("xx")},
		{ExplicitContentFilter: ptr("everyone")},
		{Features: ptr([]string{"PARTNERED"})},
		{Features: ptr([]string{discord.FeatureDiscoverable})},
	} {
		_, err := f.sess.UpdateServer(ctx, f.srv.ID, p)
		isErr(t, err, discord.ErrBadRequest)
	}
	// deleting the AFK channel clears the reference
	isErr(t, f.sess.DeleteChannel(ctx, voice.ID, ""), nil)
	if s, _ := f.w.Server(f.srv.ID); s.AFKChannelID != "" {
		t.Fatal("afk channel not cleared")
	}
}

func TestCommunityPrerequisites(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	rules, updates := f.text(t, "rules"), f.text(t, "updates")

	_, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{RulesChannelID: ptr(rules.ID)})
	errContains(t, err, "requires the Community feature")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{SafetyAlertsChannelID: ptr(rules.ID)})
	errContains(t, err, "requires the Community feature")
	_, err = f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("news"), Type: discord.ChannelAnnouncement}, "")
	errContains(t, err, "announcement channels require the Community feature")
	_, err = f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("Stage"), Type: discord.ChannelStage}, "")
	errContains(t, err, "require the Community feature")

	community := ptr([]string{discord.FeatureCommunity})
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Features: community})
	errContains(t, err, "rules channel")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Features: community, RulesChannelID: ptr(rules.ID), PublicUpdatesChannelID: ptr(updates.ID)})
	errContains(t, err, "verification level")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Features: community, RulesChannelID: ptr(rules.ID), PublicUpdatesChannelID: ptr(updates.ID), VerificationLevel: ptr("low")})
	errContains(t, err, "explicit content filter")
	if s, _ := f.w.Server(f.srv.ID); s.RulesChannelID != "" || s.HasFeature(discord.FeatureCommunity) {
		t.Fatalf("failed update applied: %+v", s)
	}

	s, err := f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Features: community, RulesChannelID: ptr(rules.ID),
		PublicUpdatesChannelID: ptr(updates.ID), VerificationLevel: ptr("low"), ExplicitContentFilter: ptr("allMembers"),
		SafetyAlertsChannelID: ptr(updates.ID)})
	isErr(t, err, nil)
	if !s.HasFeature(discord.FeatureCommunity) || !s.HasFeature(discord.FeatureNews) || s.SafetyAlertsChannelID != updates.ID {
		t.Fatalf("server = %+v", s)
	}
	_, err = f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("news"), Type: discord.ChannelAnnouncement}, "")
	isErr(t, err, nil)

	// kept while enabled
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{VerificationLevel: ptr("none")})
	errContains(t, err, "verification level")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{ExplicitContentFilter: ptr("disabled")})
	errContains(t, err, "explicit content filter")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{RulesChannelID: ptr("")})
	errContains(t, err, "rules channel")
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Name: ptr("Renamed")})
	isErr(t, err, nil)

	// non-mutable flags are kept when features are replaced; disabling removes NEWS
	isErr(t, f.w.SetFeatures(f.srv.ID, []string{"PARTNERED"}, nil), nil)
	s, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Features: ptr([]string{discord.FeatureInvitesDisabled})})
	isErr(t, err, nil)
	if s.HasFeature(discord.FeatureCommunity) || s.HasFeature(discord.FeatureNews) || !s.HasFeature("PARTNERED") || !s.HasFeature(discord.FeatureInvitesDisabled) {
		t.Fatalf("features = %v", s.Features)
	}
}

func TestRoleStyleGating(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	_, err := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("a"), UnicodeEmoji: ptr("🔥")}, "")
	errContains(t, err, "a role icon requires boost level 2")
	_, err = f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("a"), Icon: ptr(pngBytes)}, "")
	errContains(t, err, "a role icon requires boost level 2")
	_, err = f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("a"), Colors: &discord.RoleColors{Primary: 1, Secondary: 2}}, "")
	errContains(t, err, "a gradient role color requires boost level 2")
	r, err := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("a"), Colors: &discord.RoleColors{Primary: 0xff0000}}, "")
	isErr(t, err, nil)
	if r.Color != 0xff0000 || r.Colors.Primary != 0xff0000 {
		t.Fatalf("role = %+v", r)
	}

	isErr(t, f.w.SetBoosts(f.srv.ID, 7), nil)
	r, err = f.sess.UpdateRole(ctx, f.srv.ID, r.ID, discord.RoleParams{Icon: ptr(pngBytes), Colors: &discord.RoleColors{Primary: 1, Secondary: 2}}, "")
	isErr(t, err, nil)
	if !strings.Contains(r.IconURL, "/role-icons/"+r.ID+"/") || r.Color != 1 || r.Colors.Secondary != 2 {
		t.Fatalf("role = %+v", r)
	}
	r, err = f.sess.UpdateRole(ctx, f.srv.ID, r.ID, discord.RoleParams{UnicodeEmoji: ptr("🔥")}, "")
	isErr(t, err, nil)
	if r.IconURL != "" || r.UnicodeEmoji != "🔥" {
		t.Fatalf("emoji does not replace the icon: %+v", r)
	}
	r, err = f.sess.UpdateRole(ctx, f.srv.ID, r.ID, discord.RoleParams{Color: ptr(5)}, "")
	isErr(t, err, nil)
	if r.Colors != (discord.RoleColors{Primary: 5}) {
		t.Fatalf("solid color keeps the gradient: %+v", r.Colors)
	}
	_, err = f.sess.UpdateRole(ctx, f.srv.ID, r.ID, discord.RoleParams{Colors: &discord.RoleColors{Primary: 1, Secondary: 2, Tertiary: 3}}, "")
	errContains(t, err, "holographic")
	_, err = f.sess.UpdateRole(ctx, f.srv.ID, r.ID, discord.RoleParams{Colors: &discord.RoleColors{Primary: holoPrimary, Secondary: holoSecondary, Tertiary: holoTertiary}}, "")
	isErr(t, err, nil)
}

func TestChannelParityFields(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	v, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("Voice"), Type: discord.ChannelVoice,
		RTCRegion: ptr("rotterdam"), VideoQuality: ptr("full")}, "")
	isErr(t, err, nil)
	if v.RTCRegion != "rotterdam" || v.VideoQuality != "full" {
		t.Fatalf("voice = %+v", v)
	}
	v, err = f.sess.UpdateChannel(ctx, v.ID, discord.ChannelParams{RTCRegion: ptr("")}, "")
	isErr(t, err, nil)
	if v.RTCRegion != "" {
		t.Fatal("region not reset to automatic")
	}
	_, err = f.sess.UpdateChannel(ctx, v.ID, discord.ChannelParams{RTCRegion: ptr("mars")}, "")
	isErr(t, err, discord.ErrBadRequest)
	_, err = f.sess.UpdateChannel(ctx, v.ID, discord.ChannelParams{VideoQuality: ptr("hd")}, "")
	isErr(t, err, discord.ErrBadRequest)

	_, err = f.sess.UpdateChannel(ctx, f.general.ID, discord.ChannelParams{DefaultAutoArchive: ptr(100)}, "")
	isErr(t, err, discord.ErrBadRequest)
	g, err := f.sess.UpdateChannel(ctx, f.general.ID, discord.ChannelParams{DefaultAutoArchive: ptr(4320), DefaultThreadSlowmode: ptr(30)}, "")
	isErr(t, err, nil)
	if g.DefaultAutoArchive != 4320 || g.DefaultThreadSlowmode != 30 {
		t.Fatalf("text = %+v", g)
	}

	fo, err := f.sess.CreateChannel(ctx, f.srv.ID, discord.ChannelParams{Name: ptr("Help"), Type: discord.ChannelForum,
		Topic: ptr(strings.Repeat("g", 4000)), Tags: &[]discord.ForumTag{{Name: "bug", Emoji: "🐛"}, {Name: "idea", Moderated: true}},
		DefaultReaction: ptr("👍"), DefaultSortOrder: ptr("creationDate"), DefaultLayout: ptr("gallery")}, "")
	isErr(t, err, nil)
	if len(fo.Tags) != 2 || fo.Tags[0].ID == "" || fo.DefaultReaction != "👍" || fo.DefaultSortOrder != "creationDate" || fo.DefaultLayout != "gallery" {
		t.Fatalf("forum = %+v", fo)
	}
	bug := fo.Tags[0]
	fo, err = f.sess.UpdateChannel(ctx, fo.ID, discord.ChannelParams{Tags: &[]discord.ForumTag{{ID: bug.ID, Name: "bugs"}}}, "")
	isErr(t, err, nil)
	if len(fo.Tags) != 1 || fo.Tags[0].ID != bug.ID || fo.Tags[0].Name != "bugs" {
		t.Fatalf("tags = %+v", fo.Tags)
	}
	_, err = f.sess.UpdateChannel(ctx, fo.ID, discord.ChannelParams{Tags: &[]discord.ForumTag{{ID: "123", Name: "x"}}}, "")
	isErr(t, err, discord.ErrBadRequest)
	many := make([]discord.ForumTag, 21)
	for i := range many {
		many[i].Name = fmt.Sprint("t", i)
	}
	_, err = f.sess.UpdateChannel(ctx, fo.ID, discord.ChannelParams{Tags: &many}, "")
	isErr(t, err, discord.ErrBadRequest)
	_, err = f.sess.UpdateChannel(ctx, fo.ID, discord.ChannelParams{Tags: &[]discord.ForumTag{{Name: strings.Repeat("x", 21)}}}, "")
	isErr(t, err, discord.ErrBadRequest)
	_, err = f.sess.UpdateChannel(ctx, f.general.ID, discord.ChannelParams{Topic: ptr(strings.Repeat("g", 1025))}, "")
	isErr(t, err, discord.ErrBadRequest)
	// copies do not alias the world
	fo.Tags[0].Name = "mutated"
	if c, _ := f.w.Channel(fo.ID); c.Tags[0].Name != "bugs" {
		t.Fatal("tags aliased")
	}
}

func TestWidget(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	ws, err := f.sess.UpdateServerWidget(ctx, f.srv.ID, discord.ServerWidget{Enabled: true, ChannelID: f.general.ID}, "")
	isErr(t, err, nil)
	if got, _ := f.sess.GetServerWidget(ctx, f.srv.ID); got != ws || !got.Enabled {
		t.Fatalf("widget = %+v", got)
	}
	_, err = f.sess.UpdateServerWidget(ctx, f.srv.ID, discord.ServerWidget{ChannelID: "1"}, "")
	isErr(t, err, discord.ErrBadRequest)
	_, err = f.w.UserClient(f.alice.ID).UpdateServerWidget(ctx, f.srv.ID, discord.ServerWidget{}, "")
	isErr(t, err, discord.ErrForbidden)
	isErr(t, f.sess.DeleteChannel(ctx, f.general.ID, ""), nil)
	if f.w.Widget(f.srv.ID).ChannelID != "" {
		t.Fatal("widget channel not cleared")
	}
}

func TestAutoModValidation(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	role, _ := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("trusted")}, "")
	block := discord.AutoModAction{Type: discord.AutoModActionBlock, CustomMessage: "no"}
	kw := discord.AutoModRule{Name: "Bad words", Trigger: discord.AutoModKeyword, Keywords: []string{"foo*"}, Regex: []string{`b(a|e)r`},
		AllowList: []string{"food"}, Enabled: true, ExemptRoles: []string{role.ID}, ExemptChannels: []string{f.general.ID},
		Actions: []discord.AutoModAction{block, {Type: discord.AutoModActionAlert, ChannelID: f.general.ID},
			{Type: discord.AutoModActionTimeout, DurationSeconds: 60}}}
	r, err := f.sess.CreateAutoModRule(ctx, f.srv.ID, kw, "")
	isErr(t, err, nil)
	if r.ID == "" || len(f.w.AutoModRules(f.srv.ID)) != 1 {
		t.Fatalf("rule = %+v", r)
	}

	bad := func(mod func(*discord.AutoModRule), want string) {
		t.Helper()
		x := kw
		x.Actions = append([]discord.AutoModAction{}, kw.Actions...)
		mod(&x)
		_, err := f.sess.CreateAutoModRule(ctx, f.srv.ID, x, "")
		isErr(t, err, discord.ErrBadRequest)
		errContains(t, err, want)
	}
	bad(func(x *discord.AutoModRule) { x.Name = "" }, "rule name")
	bad(func(x *discord.AutoModRule) { x.Trigger = "nope" }, "trigger")
	bad(func(x *discord.AutoModRule) { x.Keywords = []string{strings.Repeat("k", 61)} }, "keyword_filter")
	bad(func(x *discord.AutoModRule) { x.Keywords = make([]string, 1001) }, "keyword_filter")
	bad(func(x *discord.AutoModRule) { x.Regex = []string{"("} }, "regex_patterns")
	bad(func(x *discord.AutoModRule) { x.Regex = make([]string, 11) }, "regex_patterns")
	bad(func(x *discord.AutoModRule) { x.Presets = []string{"profanity"} }, "presets")
	bad(func(x *discord.AutoModRule) { x.AllowList = make([]string, 101) }, "allow_list")
	bad(func(x *discord.AutoModRule) { x.MentionLimit = 5 }, "mention")
	bad(func(x *discord.AutoModRule) { x.Actions = nil }, "at least one action")
	bad(func(x *discord.AutoModRule) { x.Actions[0].CustomMessage = strings.Repeat("m", 151) }, "custom_message")
	bad(func(x *discord.AutoModRule) { x.Actions[1].ChannelID = "1" }, "alert channel")
	bad(func(x *discord.AutoModRule) { x.Actions[2].DurationSeconds = discord.AutoModMaxTimeoutSeconds + 1 }, "duration")
	bad(func(x *discord.AutoModRule) {
		x.Actions = []discord.AutoModAction{{Type: discord.AutoModActionBlockInteraction}}
	}, "not allowed")
	bad(func(x *discord.AutoModRule) { x.ExemptRoles = []string{"1"} }, "exempt_roles")
	bad(func(x *discord.AutoModRule) { x.ExemptChannels = make([]string, 51) }, "exempt_channels")

	// presets, mention spam, spam, member profile
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Presets", Trigger: discord.AutoModKeywordPreset,
		Presets: []string{"profanity", "slurs"}, AllowList: make1000(), Actions: []discord.AutoModAction{block}}, "")
	isErr(t, err, nil)
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Mentions", Trigger: discord.AutoModMentionSpam,
		Actions: []discord.AutoModAction{block}}, "")
	errContains(t, err, "mention_total_limit")
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Mentions", Trigger: discord.AutoModMentionSpam,
		MentionLimit: 10, MentionRaidProtection: true, Actions: []discord.AutoModAction{block, {Type: discord.AutoModActionTimeout, DurationSeconds: 600}}}, "")
	isErr(t, err, nil)
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Spam", Trigger: discord.AutoModSpam,
		Actions: []discord.AutoModAction{{Type: discord.AutoModActionTimeout, DurationSeconds: 60}}}, "")
	errContains(t, err, "not allowed")
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Spam", Trigger: discord.AutoModSpam,
		Actions: []discord.AutoModAction{block}}, "")
	isErr(t, err, nil)
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Spam 2", Trigger: discord.AutoModSpam,
		Actions: []discord.AutoModAction{block}}, "")
	errContains(t, err, "maximum number of spam rules reached (1)")
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Names", Trigger: discord.AutoModMemberProfile,
		Keywords: []string{"admin"}, Actions: []discord.AutoModAction{block}}, "")
	errContains(t, err, "not allowed")
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "Names", Trigger: discord.AutoModMemberProfile,
		Keywords: []string{"admin"}, Actions: []discord.AutoModAction{{Type: discord.AutoModActionBlockInteraction}}}, "")
	isErr(t, err, nil)

	// 6 keyword rules at most
	for i := 2; i <= 6; i++ {
		x := kw
		x.Name = fmt.Sprint("kw", i)
		_, err := f.sess.CreateAutoModRule(ctx, f.srv.ID, x, "")
		isErr(t, err, nil)
	}
	_, err = f.sess.CreateAutoModRule(ctx, f.srv.ID, kw, "")
	errContains(t, err, "maximum number of keyword rules reached (6)")

	// update: full replace, trigger immutable
	upd := kw
	upd.Name, upd.Keywords, upd.Enabled = "Renamed", []string{"bar"}, false
	got, err := f.sess.UpdateAutoModRule(ctx, f.srv.ID, r.ID, upd, "")
	isErr(t, err, nil)
	if got.Name != "Renamed" || got.Enabled || !slices.Equal(got.Keywords, []string{"bar"}) {
		t.Fatalf("rule = %+v", got)
	}
	upd.Trigger = discord.AutoModSpam
	_, err = f.sess.UpdateAutoModRule(ctx, f.srv.ID, r.ID, upd, "")
	errContains(t, err, "cannot be changed")

	// deleting the role and channel drops the exemptions
	isErr(t, f.sess.DeleteRole(ctx, f.srv.ID, role.ID, ""), nil)
	for _, x := range f.w.AutoModRules(f.srv.ID) {
		if x.ID == r.ID && (len(x.ExemptRoles) != 0 || len(x.ExemptChannels) != 1) {
			t.Fatalf("exemptions = %v %v", x.ExemptRoles, x.ExemptChannels)
		}
	}
	isErr(t, f.sess.DeleteAutoModRule(ctx, f.srv.ID, r.ID, ""), nil)
	isErr(t, f.sess.DeleteAutoModRule(ctx, f.srv.ID, r.ID, ""), discord.ErrNotFound)
}

func make1000() []string {
	out := make([]string, 1000)
	for i := range out {
		out[i] = fmt.Sprint("w", i)
	}
	return out
}

func TestAutoModTimeoutNeedsModerateMembers(t *testing.T) {
	f := newFixture(t, fixtureOpts{perms: discord.MustPermissions("ManageServer", "ViewChannel")})
	_, err := f.sess.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "x", Trigger: discord.AutoModKeyword, Keywords: []string{"a"},
		Actions: []discord.AutoModAction{{Type: discord.AutoModActionTimeout, DurationSeconds: 60}}}, "")
	isErr(t, err, discord.ErrForbidden)
	_, err = f.w.UserClient(f.alice.ID).ListAutoModRules(ctx, f.srv.ID)
	isErr(t, err, discord.ErrForbidden)
}

func TestOnboarding(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	role, _ := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("gamer")}, "")
	var ids []string
	for i := 0; i < 6; i++ {
		ids = append(ids, f.text(t, fmt.Sprint("c", i)).ID)
	}
	o := discord.Onboarding{Enabled: true, DefaultChannelIDs: ids, Prompts: []discord.OnboardingPrompt{{
		Title: "What do you play?", InOnboarding: true,
		Options: []discord.OnboardingOption{{Title: "Games", RoleIDs: []string{role.ID}, ChannelIDs: []string{f.general.ID}}},
	}}}
	_, err := f.sess.UpdateOnboarding(ctx, f.srv.ID, o, "")
	errContains(t, err, "onboarding requires the Community feature")
	f.enableCommunity(t) // adds 2 text channels (rules, updates)

	_, err = f.sess.UpdateOnboarding(ctx, f.srv.ID, o, "")
	errContains(t, err, "at least 7 default channels") // 6 default channels
	o.Mode = "advanced"                                // + #general from the question = 7
	got, err := f.sess.UpdateOnboarding(ctx, f.srv.ID, o, "")
	isErr(t, err, nil)
	pr := got.Prompts[0]
	if pr.ID == "" || pr.Options[0].ID == "" || pr.Type != "multipleChoice" || got.Mode != "advanced" {
		t.Fatalf("onboarding = %+v", got)
	}
	if s, _ := f.w.Server(f.srv.ID); !s.HasFeature(discord.FeatureGuildOnboarding) {
		t.Fatal("GUILD_ONBOARDING not set")
	}
	// IDs are kept on the next PUT
	o2 := got
	o2.Prompts[0].Title = "Games?"
	got2, err := f.sess.UpdateOnboarding(ctx, f.srv.ID, o2, "")
	isErr(t, err, nil)
	if got2.Prompts[0].ID != pr.ID || got2.Prompts[0].Options[0].ID != pr.Options[0].ID {
		t.Fatal("ids changed")
	}

	// chattable channels: @everyone cannot send in 3 of them → only 4 chattable
	for _, id := range ids[:3] {
		_, err := f.sess.UpdateChannel(ctx, id, discord.ChannelParams{Overwrites: &[]discord.Overwrite{
			{ID: f.srv.ID, Type: discord.OverwriteRole, Deny: discord.MustPermissions("SendMessages")}}}, "")
		isErr(t, err, nil)
	}
	_, err = f.sess.UpdateOnboarding(ctx, f.srv.ID, o2, "")
	errContains(t, err, "@everyone can send messages")

	// validation
	o3 := o2
	o3.Enabled = false
	o3.Prompts = []discord.OnboardingPrompt{{Title: "Q", Options: []discord.OnboardingOption{{Title: "empty"}}}}
	_, err = f.sess.UpdateOnboarding(ctx, f.srv.ID, o3, "")
	errContains(t, err, "at least one role or channel")
	o3.Prompts[0].Options[0].RoleIDs = []string{"1"}
	_, err = f.sess.UpdateOnboarding(ctx, f.srv.ID, o3, "")
	errContains(t, err, "unknown role")
	o3.Prompts = nil
	o3.Mode = "fancy"
	_, err = f.sess.UpdateOnboarding(ctx, f.srv.ID, o3, "")
	isErr(t, err, discord.ErrBadRequest)
	o3.Mode = ""
	got, err = f.sess.UpdateOnboarding(ctx, f.srv.ID, o3, "")
	isErr(t, err, nil)
	if got.Mode != "default" || got.Enabled {
		t.Fatalf("onboarding = %+v", got)
	}
	if s, _ := f.w.Server(f.srv.ID); s.HasFeature(discord.FeatureGuildOnboarding) {
		t.Fatal("GUILD_ONBOARDING kept")
	}
}

func TestWelcomeScreen(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	ws := discord.WelcomeScreen{Enabled: true, Description: "Hi", Channels: []discord.WelcomeChannel{{ChannelID: f.general.ID, Description: "Chat here", Emoji: "💬"}}}
	_, err := f.sess.UpdateWelcomeScreen(ctx, f.srv.ID, ws, "")
	errContains(t, err, "welcome screen requires the Community feature")
	f.enableCommunity(t)
	got, err := f.sess.UpdateWelcomeScreen(ctx, f.srv.ID, ws, "")
	isErr(t, err, nil)
	if !got.Enabled || len(got.Channels) != 1 {
		t.Fatalf("welcome = %+v", got)
	}
	if s, _ := f.w.Server(f.srv.ID); !s.HasFeature(discord.FeatureWelcomeScreenEnabled) {
		t.Fatal("WELCOME_SCREEN_ENABLED not set")
	}
	ws.Description = strings.Repeat("d", 141)
	_, err = f.sess.UpdateWelcomeScreen(ctx, f.srv.ID, ws, "")
	isErr(t, err, discord.ErrBadRequest)
	ws.Description = "Hi"
	ws.Channels = make([]discord.WelcomeChannel, 6)
	_, err = f.sess.UpdateWelcomeScreen(ctx, f.srv.ID, ws, "")
	isErr(t, err, discord.ErrBadRequest)
	// disabling community disables the welcome screen
	_, err = f.sess.UpdateServer(ctx, f.srv.ID, discord.ServerParams{Features: ptr([]string{})})
	isErr(t, err, nil)
	if f.w.WelcomeScreen(f.srv.ID).Enabled {
		t.Fatal("welcome screen still enabled")
	}
}

func TestExpressions(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	role, _ := f.sess.CreateRole(ctx, f.srv.ID, discord.RoleParams{Name: ptr("vip")}, "")
	e, err := f.sess.CreateEmoji(ctx, f.srv.ID, discord.EmojiParams{Name: "cicord_logo", Image: pngBytes, RoleIDs: &[]string{role.ID}}, "")
	isErr(t, err, nil)
	if e.ID == "" || e.Animated || len(e.RoleIDs) != 1 {
		t.Fatalf("emoji = %+v", e)
	}
	for _, p := range []discord.EmojiParams{
		{Name: "x", Image: pngBytes},
		{Name: "bad-name", Image: pngBytes},
		{Name: "big", Image: append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, discord.MaxEmojiImageBytes)...)},
		{Name: "text", Image: []byte("hello")},
		{Name: "roles", Image: pngBytes, RoleIDs: &[]string{"1"}},
	} {
		_, err := f.sess.CreateEmoji(ctx, f.srv.ID, p, "")
		isErr(t, err, discord.ErrBadRequest)
	}
	e, err = f.sess.UpdateEmoji(ctx, f.srv.ID, e.ID, discord.EmojiParams{Name: "cicord", RoleIDs: &[]string{}}, "")
	isErr(t, err, nil)
	if e.Name != "cicord" || len(e.RoleIDs) != 0 {
		t.Fatalf("emoji = %+v", e)
	}
	// slots: 50 static (49 more), animated counted apart
	for i := 0; i < 49; i++ {
		_, err := f.sess.CreateEmoji(ctx, f.srv.ID, discord.EmojiParams{Name: fmt.Sprint("e", i), Image: pngBytes}, "")
		isErr(t, err, nil)
	}
	_, err = f.sess.CreateEmoji(ctx, f.srv.ID, discord.EmojiParams{Name: "full", Image: pngBytes}, "")
	errContains(t, err, "maximum number of emojis reached (50)")
	a, err := f.sess.CreateEmoji(ctx, f.srv.ID, discord.EmojiParams{Name: "party", Image: gifBytes}, "")
	isErr(t, err, nil)
	if !a.Animated {
		t.Fatal("gif emoji not animated")
	}
	isErr(t, f.w.SetBoosts(f.srv.ID, 2), nil)
	_, err = f.sess.CreateEmoji(ctx, f.srv.ID, discord.EmojiParams{Name: "full", Image: pngBytes}, "")
	isErr(t, err, nil)
	isErr(t, f.sess.DeleteEmoji(ctx, f.srv.ID, e.ID, ""), nil)
	if len(f.w.Emojis(f.srv.ID)) != 51 {
		t.Fatalf("emojis = %d", len(f.w.Emojis(f.srv.ID)))
	}
	_, err = f.w.UserClient(f.alice.ID).CreateEmoji(ctx, f.srv.ID, discord.EmojiParams{Name: "nope", Image: pngBytes}, "")
	isErr(t, err, discord.ErrForbidden)

	// stickers
	isErr(t, f.w.SetBoosts(f.srv.ID, 0), nil)
	st, err := f.sess.CreateSticker(ctx, f.srv.ID, discord.StickerParams{Name: "Wave", Description: "Hello there", Tags: "wave", File: pngBytes}, "")
	isErr(t, err, nil)
	if st.Format != "png" {
		t.Fatalf("sticker = %+v", st)
	}
	_, err = f.sess.CreateSticker(ctx, f.srv.ID, discord.StickerParams{Name: "Anim", Tags: "x", File: []byte(`{"v":"5.5"}`)}, "")
	errContains(t, err, "lottie")
	for _, p := range []discord.StickerParams{
		{Name: "W", Tags: "x", File: pngBytes},
		{Name: "Wave2", Description: "x", Tags: "x", File: pngBytes},
		{Name: "Wave2", File: pngBytes},
		{Name: "Wave2", Tags: "x", File: []byte("jpeg?")},
	} {
		_, err := f.sess.CreateSticker(ctx, f.srv.ID, p, "")
		isErr(t, err, discord.ErrBadRequest)
	}
	for i := 0; i < 4; i++ {
		_, err := f.sess.CreateSticker(ctx, f.srv.ID, discord.StickerParams{Name: fmt.Sprint("s", i, "x"), Tags: "x", File: gifBytes}, "")
		isErr(t, err, nil)
	}
	_, err = f.sess.CreateSticker(ctx, f.srv.ID, discord.StickerParams{Name: "sixth", Tags: "x", File: pngBytes}, "")
	errContains(t, err, "maximum number of stickers reached (5)")
	st, err = f.sess.UpdateSticker(ctx, f.srv.ID, st.ID, discord.StickerParams{Name: "Waving", Tags: "wave"}, "")
	isErr(t, err, nil)
	if st.Name != "Waving" || st.Format != "png" {
		t.Fatalf("sticker = %+v", st)
	}
	isErr(t, f.sess.DeleteSticker(ctx, f.srv.ID, st.ID, ""), nil)

	// soundboard
	snd, err := f.sess.CreateSoundboardSound(ctx, f.srv.ID, discord.SoundParams{Name: "Airhorn", Sound: oggBytes, Volume: 0.5, Emoji: "📯"}, "")
	isErr(t, err, nil)
	_, err = f.sess.CreateSoundboardSound(ctx, f.srv.ID, discord.SoundParams{Name: "wav", Sound: []byte("RIFFxxxxWAVE"), Volume: 1}, "")
	errContains(t, err, "mp3 or ogg")
	_, err = f.sess.CreateSoundboardSound(ctx, f.srv.ID, discord.SoundParams{Name: "loud", Sound: oggBytes, Volume: 2}, "")
	isErr(t, err, discord.ErrBadRequest)
	for i := 0; i < 7; i++ {
		_, err := f.sess.CreateSoundboardSound(ctx, f.srv.ID, discord.SoundParams{Name: fmt.Sprint("snd", i), Sound: []byte("ID3mp3"), Volume: 1}, "")
		isErr(t, err, nil)
	}
	_, err = f.sess.CreateSoundboardSound(ctx, f.srv.ID, discord.SoundParams{Name: "ninth", Sound: oggBytes, Volume: 1}, "")
	errContains(t, err, "maximum number of soundboard sounds reached (8)")
	snd, err = f.sess.UpdateSoundboardSound(ctx, f.srv.ID, snd.ID, discord.SoundParams{Name: "Horn", Volume: 1}, "")
	isErr(t, err, nil)
	if snd.Name != "Horn" || snd.Volume != 1 || snd.Emoji != "" {
		t.Fatalf("sound = %+v", snd)
	}
	isErr(t, f.sess.DeleteSoundboardSound(ctx, f.srv.ID, snd.ID, ""), nil)
	if len(f.w.SoundboardSounds(f.srv.ID)) != 7 || len(f.w.Stickers(f.srv.ID)) != 4 {
		t.Fatal("counts")
	}
}

func TestAdminClientCoversParity(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	adm := f.w.AdminClient(f.srv.ID)
	_, err := adm.CreateAutoModRule(ctx, f.srv.ID, discord.AutoModRule{Name: "hand made", Trigger: discord.AutoModSpam,
		Actions: []discord.AutoModAction{{Type: discord.AutoModActionBlock}}}, "")
	isErr(t, err, nil)
	_, err = adm.UpdateServerWidget(ctx, f.srv.ID, discord.ServerWidget{Enabled: true}, "")
	isErr(t, err, nil)
	if !f.w.Widget(f.srv.ID).Enabled || len(f.w.AutoModRules(f.srv.ID)) != 1 {
		t.Fatal("admin changes not applied")
	}
	if n := f.w.CountCalls(f.owner.ID, "CreateAutoModRule", "UpdateServerWidget"); n != 2 {
		t.Fatalf("calls = %d", n)
	}
}
