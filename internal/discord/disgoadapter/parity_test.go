package disgoadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"reflect"
	"strings"
	"testing"

	"github.com/npjonath/cicord/internal/discord"
)

var ctx = context.Background()

func TestServerParityMapping(t *testing.T) {
	raw := `{"id":"1","name":"S","banner":"a_b1","splash":"s1","explicit_content_filter":2,"system_channel_flags":5,
	  "public_updates_channel_id":"20","safety_alerts_channel_id":"21","afk_channel_id":"22","afk_timeout":300,
	  "preferred_locale":"fr","premium_progress_bar_enabled":true,"features":["COMMUNITY","BANNER"],
	  "premium_tier":2,"premium_subscription_count":9}`
	var g wireGuild
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatal(err)
	}
	s := serverFromWire(g)
	if s.BannerURL != "https://cdn.discordapp.com/banners/1/a_b1.gif" || s.SplashURL != "https://cdn.discordapp.com/splashes/1/s1.png" ||
		s.ExplicitContentFilter != "allMembers" || s.SystemChannelFlags != discord.SuppressJoinNotifications|discord.SuppressGuildReminderNotifications ||
		s.PublicUpdatesChannelID != "20" || s.SafetyAlertsChannelID != "21" || s.AFKChannelID != "22" || s.AFKTimeout != 300 ||
		s.PreferredLocale != "fr" || !s.PremiumProgressBar || !s.HasFeature("COMMUNITY") || s.PremiumTier != 2 || s.PremiumSubscriptionCount != 9 {
		t.Errorf("server = %+v", s)
	}

	flags := discord.SuppressPremiumSubscriptions
	body, err := serverParamsBody(discord.ServerParams{
		Banner: ptr([]byte("\x89PNG....")), Splash: ptr([]byte{}), ExplicitContentFilter: ptr("membersWithoutRoles"),
		SystemChannelFlags: &flags, PublicUpdatesChannelID: ptr(""), SafetyAlertsChannelID: ptr("21"), AFKChannelID: ptr(""),
		AFKTimeout: ptr(60), PreferredLocale: ptr("en-US"), PremiumProgressBar: ptr(false), Features: &[]string{"COMMUNITY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := jsonOf(t, body).(map[string]any)
	if !strings.HasPrefix(got["banner"].(string), "data:image/png;base64,") {
		t.Errorf("banner = %v", got["banner"])
	}
	delete(got, "banner")
	want := map[string]any{"splash": nil, "explicit_content_filter": 1.0, "system_channel_flags": 2.0,
		"public_updates_channel_id": nil, "safety_alerts_channel_id": "21", "afk_channel_id": nil, "afk_timeout": 60.0,
		"preferred_locale": "en-US", "premium_progress_bar_enabled": false, "features": []any{"COMMUNITY"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v", got)
	}
	if _, err := serverParamsBody(discord.ServerParams{ExplicitContentFilter: ptr("everyone")}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("bad filter err = %v", err)
	}
}

func TestRoleParityMapping(t *testing.T) {
	raw := `{"id":"3","name":"vip","color":16711680,"permissions":"0","icon":"ic","unicode_emoji":"🔥",
	  "colors":{"primary_color":16711680,"secondary_color":255,"tertiary_color":null}}`
	var w wireRole
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatal(err)
	}
	r := roleFromWire(w)
	if r.IconURL != "https://cdn.discordapp.com/role-icons/3/ic.png" || r.UnicodeEmoji != "🔥" ||
		r.Colors != (discord.RoleColors{Primary: 0xff0000, Secondary: 0xff}) {
		t.Errorf("role = %+v", r)
	}
	body := jsonOf(t, roleParamsBody(discord.RoleParams{Color: ptr(1), Colors: &discord.RoleColors{Primary: 1, Secondary: 2},
		Icon: ptr([]byte{}), UnicodeEmoji: ptr("")}))
	want := map[string]any{"colors": map[string]any{"primary_color": 1.0, "secondary_color": 2.0, "tertiary_color": nil},
		"icon": nil, "unicode_emoji": nil}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("role body = %v", body)
	}
}

func TestChannelParityMapping(t *testing.T) {
	raw := `{"id":"10","name":"forum","type":15,"rtc_region":null,"video_quality_mode":0,"default_auto_archive_duration":4320,
	  "default_thread_rate_limit_per_user":30,"default_sort_order":1,"default_forum_layout":2,
	  "default_reaction_emoji":{"emoji_id":null,"emoji_name":"👍"},
	  "available_tags":[{"id":"70","name":"bug","moderated":true,"emoji_id":null,"emoji_name":"🐛"},{"id":"71","name":"x","moderated":false,"emoji_id":"123456789012345678","emoji_name":null}]}`
	var w wireChannel
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatal(err)
	}
	c := channelFromWire(w)
	if c.RTCRegion != "" || c.VideoQuality != "" || c.DefaultAutoArchive != 4320 || c.DefaultThreadSlowmode != 30 ||
		c.DefaultSortOrder != "creationDate" || c.DefaultLayout != "gallery" || c.DefaultReaction != "👍" ||
		!reflect.DeepEqual(c.Tags, []discord.ForumTag{{ID: "70", Name: "bug", Moderated: true, Emoji: "🐛"}, {ID: "71", Name: "x", Emoji: "123456789012345678"}}) {
		t.Errorf("channel = %+v", c)
	}
	var v wireChannel
	_ = json.Unmarshal([]byte(`{"id":"11","type":2,"rtc_region":"rotterdam","video_quality_mode":2,"default_sort_order":0}`), &v)
	if vc := channelFromWire(v); vc.RTCRegion != "rotterdam" || vc.VideoQuality != "full" || vc.DefaultSortOrder != "latestActivity" {
		t.Errorf("voice = %+v", vc)
	}

	b, err := channelUpdateBody(discord.ChannelParams{RTCRegion: ptr(""), VideoQuality: ptr("auto"), DefaultAutoArchive: ptr(60),
		DefaultThreadSlowmode: ptr(0), DefaultReaction: ptr("party:123456789012345678"), DefaultSortOrder: ptr("latestActivity"),
		DefaultLayout: ptr("list"), Tags: &[]discord.ForumTag{{ID: "70", Name: "bug", Emoji: "🐛"}, {Name: "new", Moderated: true}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"rtc_region": nil, "video_quality_mode": 1.0, "default_auto_archive_duration": 60.0,
		"default_thread_rate_limit_per_user": 0.0, "default_sort_order": 0.0, "default_forum_layout": 1.0,
		"default_reaction_emoji": map[string]any{"emoji_id": "123456789012345678", "emoji_name": "party"},
		"available_tags": []any{
			map[string]any{"id": "70", "name": "bug", "moderated": false, "emoji_id": nil, "emoji_name": "🐛"},
			map[string]any{"name": "new", "moderated": true, "emoji_id": nil, "emoji_name": nil},
		}}
	if got := jsonOf(t, b); !reflect.DeepEqual(got, want) {
		t.Errorf("update body = %v", got)
	}
	b2, _ := channelUpdateBody(discord.ChannelParams{DefaultReaction: ptr(""), DefaultSortOrder: ptr("")})
	if got := jsonOf(t, b2); !reflect.DeepEqual(got, map[string]any{"default_reaction_emoji": nil, "default_sort_order": nil}) {
		t.Errorf("clear body = %v", got)
	}
	if _, err := channelUpdateBody(discord.ChannelParams{VideoQuality: ptr("hd")}); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("bad video quality err = %v", err)
	}
}

func TestWidgetREST(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /guilds/1/widget", 200, `{"enabled":true,"channel_id":"5"}`)
	f.json("PATCH /guilds/1/widget", 200, `{"enabled":false,"channel_id":null}`)
	s := f.connect(t)
	w, err := s.GetServerWidget(ctx, "1")
	if err != nil || w != (discord.ServerWidget{Enabled: true, ChannelID: "5"}) {
		t.Fatalf("widget = %+v, %v", w, err)
	}
	w, err = s.UpdateServerWidget(ctx, "1", discord.ServerWidget{}, "r")
	if err != nil || w.Enabled {
		t.Fatalf("update = %+v, %v", w, err)
	}
	if req := f.last(); req.Method != "PATCH" || !reflect.DeepEqual(req.Body, map[string]any{"enabled": false, "channel_id": nil}) || req.Reason != "r" {
		t.Errorf("request = %+v", req)
	}
}

func TestAutoModREST(t *testing.T) {
	f := newFakeDiscord(t)
	resp := `{"id":"90","guild_id":"1","name":"bad words","event_type":1,"trigger_type":1,
	  "trigger_metadata":{"keyword_filter":["foo*"],"regex_patterns":[],"allow_list":["food"]},
	  "actions":[{"type":1,"metadata":{"custom_message":"no"}},{"type":2,"metadata":{"channel_id":"7"}},{"type":3,"metadata":{"duration_seconds":60}}],
	  "enabled":true,"exempt_roles":["3"],"exempt_channels":[]}`
	f.json("GET /guilds/1/auto-moderation/rules", 200, "["+resp+`,{"id":"91","name":"presets","event_type":1,"trigger_type":4,"trigger_metadata":{"presets":[1,3]},"actions":[],"enabled":false},
	  {"id":"1030554520465440818","creator_id":"1008776202191634432","name":"Block Mention Spam","event_type":1,"trigger_type":5,"trigger_metadata":{"mention_total_limit":20},"actions":[{"type":1}],"enabled":true}]`)
	f.json("POST /guilds/1/auto-moderation/rules", 200, resp)
	f.json("PATCH /guilds/1/auto-moderation/rules/90", 200, resp)
	f.json("DELETE /guilds/1/auto-moderation/rules/90", 204, "")
	s := f.connect(t)
	rules, err := s.ListAutoModRules(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	want := discord.AutoModRule{ID: "90", Name: "bad words", Trigger: "keyword", Keywords: []string{"foo*"}, Regex: []string{},
		AllowList: []string{"food"}, Presets: []string{}, Enabled: true, ExemptRoles: []string{"3"}, ExemptChannels: []string{},
		Actions: []discord.AutoModAction{{Type: "block", CustomMessage: "no"}, {Type: "alert", ChannelID: "7"}, {Type: "timeout", DurationSeconds: 60}}}
	if len(rules) != 2 || !reflect.DeepEqual(rules[0], want) || !reflect.DeepEqual(rules[1].Presets, []string{"profanity", "slurs"}) || rules[1].Trigger != "keywordPreset" {
		t.Fatalf("rules = %+v", rules)
	}
	in := discord.AutoModRule{Name: "bad words", Trigger: "keyword", Keywords: []string{"foo*"}, Enabled: true,
		Actions: []discord.AutoModAction{{Type: "block", CustomMessage: "no"}, {Type: "alert", ChannelID: "7"}, {Type: "timeout", DurationSeconds: 60}}}
	if _, err := s.CreateAutoModRule(ctx, "1", in, "cicord"); err != nil {
		t.Fatal(err)
	}
	body := f.last().Body.(map[string]any)
	if body["trigger_type"] != 1.0 || body["event_type"] != 1.0 || !reflect.DeepEqual(body["exempt_roles"], []any{}) {
		t.Errorf("create body = %v", body)
	}
	if !reflect.DeepEqual(body["actions"], []any{
		map[string]any{"type": 1.0, "metadata": map[string]any{"custom_message": "no"}},
		map[string]any{"type": 2.0, "metadata": map[string]any{"channel_id": "7"}},
		map[string]any{"type": 3.0, "metadata": map[string]any{"duration_seconds": 60.0}},
	}) {
		t.Errorf("actions = %v", body["actions"])
	}
	if _, err := s.UpdateAutoModRule(ctx, "1", "90", in, ""); err != nil {
		t.Fatal(err)
	}
	if b := f.last().Body.(map[string]any); b["trigger_type"] != nil {
		t.Errorf("PATCH must not send trigger_type: %v", b)
	}
	// member profile rules run on member updates
	mp, _ := autoModRuleBody(discord.AutoModRule{Trigger: "memberProfile", Actions: []discord.AutoModAction{{Type: "blockInteraction"}}}, true)
	if mp.EventType != 2 || mp.TriggerType != 6 || mp.Actions[0].Type != 4 {
		t.Errorf("member profile body = %+v", mp)
	}
	if _, err := s.CreateAutoModRule(ctx, "1", discord.AutoModRule{Trigger: "harmfulLink"}, ""); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("bad trigger err = %v", err)
	}
	if err := s.DeleteAutoModRule(ctx, "1", "90", ""); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardingREST(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /guilds/1/onboarding", 200, `{"guild_id":"1","enabled":true,"mode":1,"default_channel_ids":["5"],
	  "prompts":[{"id":"80","type":1,"title":"Games?","single_select":true,"required":false,"in_onboarding":true,
	    "options":[{"id":"81","title":"Chess","description":null,"emoji":{"id":null,"name":"♟️"},"role_ids":["3"],"channel_ids":[]}]}]}`)
	f.json("PUT /guilds/1/onboarding", 200, `{"guild_id":"1","enabled":false,"mode":0,"default_channel_ids":[],"prompts":[]}`)
	s := f.connect(t)
	o, err := s.GetOnboarding(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	want := discord.Onboarding{Enabled: true, Mode: "advanced", DefaultChannelIDs: []string{"5"}, Prompts: []discord.OnboardingPrompt{{
		ID: "80", Type: "dropdown", Title: "Games?", SingleSelect: true, InOnboarding: true,
		Options: []discord.OnboardingOption{{ID: "81", Title: "Chess", Emoji: "♟️", RoleIDs: []string{"3"}, ChannelIDs: []string{}}},
	}}}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("onboarding = %+v", o)
	}
	o.Prompts = append(o.Prompts, discord.OnboardingPrompt{Title: "New", Options: []discord.OnboardingOption{{Title: "A", Emoji: "a:dance:123456789012345678"}}})
	if _, err := s.UpdateOnboarding(ctx, "1", o, "cicord"); err != nil {
		t.Fatal(err)
	}
	req := f.last()
	if req.Method != "PUT" {
		t.Fatalf("method = %s", req.Method)
	}
	prompts := req.Body.(map[string]any)["prompts"].([]any)
	p0 := prompts[0].(map[string]any)
	opt0 := p0["options"].([]any)[0].(map[string]any)
	if p0["id"] != "80" || opt0["id"] != "81" || opt0["emoji_name"] != "♟️" || opt0["emoji"] != nil {
		t.Errorf("existing prompt = %v", p0)
	}
	p1 := prompts[1].(map[string]any)
	opt1 := p1["options"].([]any)[0].(map[string]any)
	if !isSnowflake(p1["id"].(string)) || !isSnowflake(opt1["id"].(string)) || p1["id"] == opt1["id"] || p1["type"] != 0.0 {
		t.Errorf("new prompt ids = %v", p1)
	}
	if opt1["emoji_id"] != "123456789012345678" || opt1["emoji_name"] != "dance" || opt1["emoji_animated"] != true {
		t.Errorf("new option emoji = %v", opt1)
	}
	if req.Body.(map[string]any)["mode"] != 1.0 {
		t.Errorf("mode = %v", req.Body)
	}
}

func TestWelcomeScreenREST(t *testing.T) {
	f := newFakeDiscord(t)
	f.json("GET /guilds/1/welcome-screen", 200, `{"description":"Hi","welcome_channels":[{"channel_id":"5","description":"Rules","emoji_id":null,"emoji_name":"📜"}]}`)
	f.json("GET /guilds/1", 200, `{"id":"1","name":"S","features":["WELCOME_SCREEN_ENABLED"]}`)
	f.json("PATCH /guilds/1/welcome-screen", 200, `{"description":null,"welcome_channels":[]}`)
	s := f.connect(t)
	ws, err := s.GetWelcomeScreen(ctx, "1")
	want := discord.WelcomeScreen{Enabled: true, Description: "Hi", Channels: []discord.WelcomeChannel{{ChannelID: "5", Description: "Rules", Emoji: "📜"}}}
	if err != nil || !reflect.DeepEqual(ws, want) {
		t.Fatalf("welcome = %+v, %v", ws, err)
	}
	out, err := s.UpdateWelcomeScreen(ctx, "1", discord.WelcomeScreen{Enabled: false, Channels: []discord.WelcomeChannel{{ChannelID: "5", Description: "R"}}}, "")
	if err != nil || out.Enabled {
		t.Fatal(out, err)
	}
	if b := f.last().Body; !reflect.DeepEqual(b, map[string]any{"enabled": false, "description": nil,
		"welcome_channels": []any{map[string]any{"channel_id": "5", "description": "R", "emoji_id": nil, "emoji_name": nil}}}) {
		t.Errorf("body = %v", b)
	}
}

func TestEmojiREST(t *testing.T) {
	f := newFakeDiscord(t)
	e := `{"id":"95","name":"party","animated":false,"roles":["3"],"managed":false}`
	f.json("GET /guilds/1/emojis", 200, "["+e+"]")
	f.json("POST /guilds/1/emojis", 201, e)
	f.json("PATCH /guilds/1/emojis/95", 200, e)
	f.json("DELETE /guilds/1/emojis/95", 204, "")
	s := f.connect(t)
	list, err := s.ListEmojis(ctx, "1")
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], discord.Emoji{ID: "95", Name: "party", RoleIDs: []string{"3"}}) {
		t.Fatalf("emojis = %+v, %v", list, err)
	}
	if _, err := s.CreateEmoji(ctx, "1", discord.EmojiParams{Name: "party", Image: []byte("GIF89a...")}, ""); err != nil {
		t.Fatal(err)
	}
	b := f.last().Body.(map[string]any)
	if !strings.HasPrefix(b["image"].(string), "data:image/gif;base64,") || !reflect.DeepEqual(b["roles"], []any{}) {
		t.Errorf("create = %v", b)
	}
	if _, err := s.UpdateEmoji(ctx, "1", "95", discord.EmojiParams{Name: "p2", Image: []byte("x")}, ""); err != nil {
		t.Fatal(err)
	}
	if b := f.last().Body; !reflect.DeepEqual(b, map[string]any{"name": "p2"}) {
		t.Errorf("update = %v", b)
	}
	if err := s.DeleteEmoji(ctx, "1", "95", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEmoji(ctx, "1", discord.EmojiParams{Name: "x"}, ""); !errors.Is(err, discord.ErrBadRequest) {
		t.Errorf("no image err = %v", err)
	}
}

func TestStickerREST(t *testing.T) {
	f := newFakeDiscord(t)
	st := `{"id":"96","name":"wave","description":"hi","tags":"wave","format_type":1}`
	f.json("GET /guilds/1/stickers", 200, "["+st+"]")
	f.json("POST /guilds/1/stickers", 201, st)
	f.json("PATCH /guilds/1/stickers/96", 200, st)
	f.json("DELETE /guilds/1/stickers/96", 204, "")
	s := f.connect(t)
	list, err := s.ListStickers(ctx, "1")
	if err != nil || len(list) != 1 || list[0] != (discord.Sticker{ID: "96", Name: "wave", Description: "hi", Tags: "wave", Format: "png"}) {
		t.Fatalf("stickers = %+v, %v", list, err)
	}
	png := []byte("\x89PNG\r\n\x1a\nDATA")
	if _, err := s.CreateSticker(ctx, "1", discord.StickerParams{Name: "wave", Description: "hi", Tags: "wave", File: png}, "cicord"); err != nil {
		t.Fatal(err)
	}
	req := f.last()
	mt, params, err := mime.ParseMediaType(req.ContentType)
	if err != nil || mt != "multipart/form-data" || req.Reason != "cicord" {
		t.Fatalf("content type = %q (%v), reason %q", req.ContentType, err, req.Reason)
	}
	mr := multipart.NewReader(strings.NewReader(string(req.Raw)), params["boundary"])
	fields := map[string]string{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		v, _ := io.ReadAll(p)
		fields[p.FormName()] = string(v)
		if p.FormName() == "file" && (p.FileName() != "sticker.png" || p.Header.Get("Content-Type") != "image/png") {
			t.Errorf("file part = %v %v", p.FileName(), p.Header)
		}
	}
	if !reflect.DeepEqual(fields, map[string]string{"name": "wave", "description": "hi", "tags": "wave", "file": string(png)}) {
		t.Errorf("fields = %v", fields)
	}
	if _, err := s.UpdateSticker(ctx, "1", "96", discord.StickerParams{Name: "w2", Tags: "wave"}, ""); err != nil {
		t.Fatal(err)
	}
	if b := f.last().Body; !reflect.DeepEqual(b, map[string]any{"name": "w2", "description": nil, "tags": "wave"}) {
		t.Errorf("update = %v", b)
	}
	if err := s.DeleteSticker(ctx, "1", "96", ""); err != nil {
		t.Fatal(err)
	}
}

func TestSoundboardREST(t *testing.T) {
	f := newFakeDiscord(t)
	snd := `{"sound_id":"97","name":"airhorn","volume":0.5,"emoji_id":null,"emoji_name":"📯","available":true}`
	f.json("GET /guilds/1/soundboard-sounds", 200, `{"items":[`+snd+`]}`)
	f.json("POST /guilds/1/soundboard-sounds", 201, snd)
	f.json("PATCH /guilds/1/soundboard-sounds/97", 200, snd)
	f.json("DELETE /guilds/1/soundboard-sounds/97", 204, "")
	s := f.connect(t)
	list, err := s.ListSoundboardSounds(ctx, "1")
	if err != nil || len(list) != 1 || list[0] != (discord.SoundboardSound{ID: "97", Name: "airhorn", Volume: 0.5, Emoji: "📯"}) {
		t.Fatalf("sounds = %+v, %v", list, err)
	}
	if _, err := s.CreateSoundboardSound(ctx, "1", discord.SoundParams{Name: "airhorn", Sound: []byte("OggS..."), Volume: 0.5, Emoji: "📯"}, ""); err != nil {
		t.Fatal(err)
	}
	b := f.last().Body.(map[string]any)
	if !strings.HasPrefix(b["sound"].(string), "data:audio/ogg;base64,") || b["emoji_name"] != "📯" || b["emoji_id"] != nil || b["volume"] != 0.5 {
		t.Errorf("create = %v", b)
	}
	if _, err := s.UpdateSoundboardSound(ctx, "1", "97", discord.SoundParams{Name: "horn", Volume: 1, Sound: []byte("x")}, ""); err != nil {
		t.Fatal(err)
	}
	if b := f.last().Body; !reflect.DeepEqual(b, map[string]any{"name": "horn", "volume": 1.0, "emoji_id": nil, "emoji_name": nil}) {
		t.Errorf("update = %v", b)
	}
	if err := s.DeleteSoundboardSound(ctx, "1", "97", ""); err != nil {
		t.Fatal(err)
	}
}

func TestGuildUpdateKeepsParityFields(t *testing.T) {
	g := `{"id":"1","name":"S","features":["COMMUNITY"],"premium_tier":3,"premium_subscription_count":15,"banner":"b"}`
	var w wireGuild
	if err := json.Unmarshal([]byte(g), &w); err != nil {
		t.Fatal(err)
	}
	s := serverFromWire(w)
	if s.PremiumTier != 3 || s.PremiumSubscriptionCount != 15 || s.BannerURL == "" || !s.HasFeature(discord.FeatureCommunity) {
		t.Errorf("server = %+v", s)
	}
}

func TestNewSnowflakeIsMonotonic(t *testing.T) {
	a, b := newSnowflake(), newSnowflake()
	if a == b || !isSnowflake(a) || !idLess(a, b) {
		t.Errorf("ids %s %s", a, b)
	}
}

func idLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
