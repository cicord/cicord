package reconcile

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/state"
)

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const parityProject = "apiVersion: codi.dev/v1alpha1\nkind: Project\nmetadata:\n  name: parity\nspec:\n  engine: \">=0.0.0\"\n"

// parityRepo writes a repository with one server (the given spec body, indented under
// spec:) and the asset files it uses; it returns the loaded server.
func parityRepo(t *testing.T, spec string) (*config.Server, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel string, data []byte) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("codi.yaml", []byte(parityProject))
	write("servers/assets/banner.png", pngBytes(t, 96, 54, color.RGBA{200, 10, 10, 255}))
	write("servers/assets/banner2.png", pngBytes(t, 96, 54, color.RGBA{10, 200, 10, 255}))
	write("servers/assets/role.png", pngBytes(t, 64, 64, color.RGBA{10, 10, 200, 255}))
	write("servers/assets/emoji.png", pngBytes(t, 32, 32, color.RGBA{1, 2, 3, 255}))
	write("servers/assets/emoji2.png", pngBytes(t, 32, 32, color.RGBA{4, 5, 6, 255}))
	write("servers/assets/sticker.png", pngBytes(t, 160, 160, color.RGBA{7, 8, 9, 255}))
	write("servers/assets/sound.ogg", append([]byte("OggS\x00\x02"), make([]byte, 64)...))
	write("servers/parity.yaml", []byte("apiVersion: codi.dev/v1alpha1\nkind: Server\nmetadata:\n  name: parity\nspec:\n  discordId: \"112233445566778899\"\n"+spec))
	repo, diags := config.Load(dir, config.LoadOptions{})
	if repo == nil || diags.HasErrors() {
		t.Fatalf("load: %v", diags.Errors())
	}
	return repo.Servers["parity"], repo.Root
}

// a community server with every parity block.
const fullSpec = `
  profile:
    name: Parity
    verificationLevel: low
    explicitContentFilter: allMembers
    preferredLocale: fr
    premiumProgressBar: true
    banner: ./assets/banner.png
    systemChannel: general
    systemChannelFlags:
      welcomeMessages: true
      setupTips: false
      boostMessages: false
  community:
    enabled: true
    rulesChannel: rules
    updatesChannel: mod-news
    safetyAlertsChannel: mod-news
  afk:
    channel: lounge
    timeout: 900
  widget:
    enabled: true
    channel: general
  roles:
    mod:
      name: Mod
      colors: {primary: "#112233", secondary: "#445566"}
      emoji: "🛡️"
    vip:
      name: VIP
      color: "#ffaa00"
      icon: ./assets/role.png
    gamer:
      name: Gamer
  channels:
    rules: {name: rules, type: text}
    mod-news: {name: mod-news, type: text}
    general: {name: general, type: text, threadArchiveAfter: 3d, threadSlowmode: 10}
    news: {name: news, type: announcement}
    stage: {name: Stage, type: stage}
    lounge: {name: Lounge, type: voice, voiceRegion: rotterdam, videoQuality: full}
    c1: {name: c1, type: text}
    c2: {name: c2, type: text}
    c3: {name: c3, type: text}
    help:
      name: help
      type: forum
      guidelines: Be nice
      layout: gallery
      sortOrder: creationDate
      defaultReaction: "👍"
      tags:
        a-question: {name: Question, emoji: "❓"}
        b-solved: {name: Solved, moderated: true}
  automod:
    bad-words:
      name: Bad words
      trigger: keyword
      keywords: [darn, "heck*"]
      allowList: [heckin]
      actions:
        block: {message: Language!}
        alert: mod-news
        timeout: 10m
      exemptRoles: [mod]
      exemptChannels: [mod-news]
    mentions:
      name: Mention spam
      trigger: mentionSpam
      mentionLimit: 5
      actions: {block: {}}
    presets:
      name: Presets
      trigger: keywordPreset
      presets: [profanity, slurs]
      actions: {block: {}}
  welcomeScreen:
    description: Welcome to Parity
    channels:
      - {channel: rules, description: Read the rules, emoji: "📜"}
      - {channel: general, description: Say hi}
  onboarding:
    enabled: true
    defaultChannels: [rules, general, news, c1, c2, c3, mod-news]
    prompts:
      - key: games
        title: What do you play?
        options:
          - {title: Games, description: All games, emoji: "🎮", roles: [gamer], channels: [help]}
  emojis:
    party: {name: party, image: ./assets/emoji.png, roles: [vip]}
  stickers:
    wave: {name: Wave, description: Waving hand, emoji: wave, file: ./assets/sticker.png}
  soundboard:
    horn: {name: Horn, sound: ./assets/sound.ogg, volume: 0.5, emoji: "📯"}
`

func TestParityFullSyncIdempotent(t *testing.T) {
	desired, root := parityRepo(t, fullSpec)
	e := newEnv(t, desired, root)
	if err := e.w.SetBoosts(e.srv.ID, 7); err != nil {
		t.Fatal(err)
	}
	r := e.reconciler(e.session)
	ctx := context.Background()
	p, res, err := r.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, p)
	}
	if len(p.Blocked) != 0 {
		t.Fatalf("blocked:\n%s", p)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed: %+v", res.Failed)
	}
	e.mustEmptyPlan(r)

	srv, _ := e.w.Server(e.srv.ID)
	if !srv.HasFeature(discord.FeatureCommunity) || srv.BannerURL == "" || srv.PreferredLocale != "fr" || !srv.PremiumProgressBar ||
		srv.ExplicitContentFilter != "allMembers" || srv.AFKTimeout != 900 || srv.AFKChannelID != e.channel("Lounge").ID ||
		srv.PublicUpdatesChannelID != e.channel("mod-news").ID || srv.RulesChannelID != e.channel("rules").ID {
		t.Errorf("server not configured: %+v", srv)
	}
	if srv.SystemChannelFlags&discord.SuppressGuildReminderNotifications == 0 || srv.SystemChannelFlags&discord.SuppressJoinNotifications != 0 ||
		srv.SystemChannelFlags&discord.SuppressPremiumSubscriptions == 0 {
		t.Errorf("system channel flags = %b", srv.SystemChannelFlags)
	}
	if w := e.w.Widget(e.srv.ID); !w.Enabled || w.ChannelID != e.channel("general").ID {
		t.Errorf("widget = %+v", w)
	}
	if mod := e.role("Mod"); mod.UnicodeEmoji != "🛡️" || mod.Colors.Secondary != 0x445566 || mod.Color != 0x112233 {
		t.Errorf("mod role = %+v", mod)
	}
	if vip := e.role("VIP"); vip.IconURL == "" {
		t.Errorf("vip role icon not set: %+v", vip)
	}
	help := e.channel("help")
	if len(help.Tags) != 2 || help.Tags[0].Name != "Question" || help.Topic != "Be nice" || help.DefaultLayout != "gallery" || help.DefaultSortOrder != "creationDate" {
		t.Errorf("forum = %+v", help)
	}
	if l := e.channel("Lounge"); l.RTCRegion != "rotterdam" || l.VideoQuality != "full" {
		t.Errorf("voice = %+v", l)
	}
	if g := e.channel("general"); g.DefaultAutoArchive != 4320 || g.DefaultThreadSlowmode != 10 {
		t.Errorf("general = %+v", g)
	}
	if rules := e.w.AutoModRules(e.srv.ID); len(rules) != 3 {
		t.Errorf("automod rules = %+v", rules)
	}
	if ws := e.w.WelcomeScreen(e.srv.ID); !ws.Enabled || len(ws.Channels) != 2 {
		t.Errorf("welcome screen = %+v", ws)
	}
	if ob := e.w.Onboarding(e.srv.ID); !ob.Enabled || len(ob.Prompts) != 1 || len(ob.DefaultChannelIDs) != 7 {
		t.Errorf("onboarding = %+v", ob)
	}
	if n := len(e.w.Emojis(e.srv.ID)); n != 1 {
		t.Errorf("emojis = %d", n)
	}
	if n := len(e.w.Stickers(e.srv.ID)); n != 1 {
		t.Errorf("stickers = %d", n)
	}
	if s := e.w.SoundboardSounds(e.srv.ID); len(s) != 1 || s[0].Volume != 0.5 {
		t.Errorf("sounds = %+v", s)
	}

	// drift: changes made by hand in Discord are detected and reverted
	admin := e.w.AdminClient(e.srv.ID)
	rule := e.w.AutoModRules(e.srv.ID)[0]
	rule.Enabled = false
	if _, err := admin.UpdateAutoModRule(ctx, e.srv.ID, rule.ID, rule, ""); err != nil {
		t.Fatal(err)
	}
	tags := append([]discord.ForumTag(nil), help.Tags...)
	tags[0].Name = "Q"
	if _, err := admin.UpdateChannel(ctx, help.ID, discord.ChannelParams{Tags: &tags}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.UpdateServerWidget(ctx, e.srv.ID, discord.ServerWidget{}, ""); err != nil {
		t.Fatal(err)
	}
	drift, err := r.Drift(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range drift {
		seen[d.Kind+"."+d.Field] = true
	}
	for _, want := range []string{"automod.enabled", "channel.tags", "widget.enabled", "widget.channel"} {
		if !seen[want] {
			t.Errorf("drift misses %s: %+v", want, drift)
		}
	}
	if p, _, err := r.Sync(ctx); err != nil {
		t.Fatalf("revert: %v\n%s", err, p)
	}
	e.mustEmptyPlan(r)
	if got := e.channel("help").Tags; got[0].ID != help.Tags[0].ID || got[0].Name != "Question" {
		t.Errorf("tag rename did not keep the tag: %+v → %+v", help.Tags, got)
	}

	// adoption: the state is lost, AutoMod rules and expressions are adopted by name
	for _, m := range mustList(t, e) {
		if err := e.store.Resources().Delete(ctx, m.Server, m.Kind, m.Key); err != nil {
			t.Fatal(err)
		}
	}
	p, err = r.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adopted := map[string]int{}
	for _, c := range p.Changes {
		if c.Action != ActionAdopt {
			t.Errorf("after state loss, unexpected %s", c.Summary)
		}
		adopted[c.Kind]++
	}
	if adopted[KindAutoMod] != 3 || adopted[KindEmoji] != 1 || adopted[KindSticker] != 1 || adopted[KindSound] != 1 {
		t.Errorf("adopted = %v\n%s", adopted, p)
	}
	if _, _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	e.mustEmptyPlan(r)
}

func mustList(t *testing.T, e *env) []state.Resource {
	t.Helper()
	l, err := e.store.Resources().List(context.Background(), e.desired.Metadata.Name)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]state.Resource, len(l))
	copy(out, l)
	return out
}

func blockedWith(p Plan, kind, prefix string) *Blocked {
	for i, b := range p.Blocked {
		if b.Kind == kind && strings.HasPrefix(b.Reason, prefix) {
			return &p.Blocked[i]
		}
	}
	return nil
}

func TestParityBoostGating(t *testing.T) {
	desired, root := parityRepo(t, `
  profile:
    banner: ./assets/banner.png
  roles:
    mod:
      name: Mod
      colors: {primary: "#112233", secondary: "#445566"}
    vip:
      name: VIP
      icon: ./assets/role.png
`)
	e := newEnv(t, desired, root)
	r := e.reconciler(e.session)
	ctx := context.Background()
	p, res, err := r.Sync(ctx)
	if err != nil || len(res.Failed) > 0 {
		t.Fatalf("sync at level 0 must not fail: %v %+v\n%s", err, res.Failed, p)
	}
	b := blockedWith(p, KindServer, "boost: banner requires boost level 2; server is level 0")
	if b == nil {
		t.Fatalf("banner not blocked with a reason:\n%s", p)
	}
	if h := ExplainBlocked(*b); h.Code != ErrCodeBoostRequired || !strings.Contains(h.Message, "Banner requires boost level 2") {
		t.Errorf("explained = %+v", h)
	}
	if blockedWith(p, KindRole, "boost: a gradient role color requires boost level 2") == nil ||
		blockedWith(p, KindRole, "boost: a role icon requires boost level 2") == nil {
		t.Errorf("role style not blocked:\n%s", p)
	}
	// the solid primary color is applied meanwhile
	if mod := e.role("Mod"); mod.Color != 0x112233 || mod.Colors.Secondary != 0 {
		t.Errorf("mod = %+v", mod)
	}
	// a blocked plan has no changes left but stays OutOfSync (drift)
	p2, _ := r.Plan(ctx)
	if !p2.Empty() || len(p2.Blocked) != 3 || len(DriftOf(p2)) == 0 {
		t.Fatalf("plan at level 0:\n%s", p2)
	}

	if err := e.w.SetBoosts(e.srv.ID, 7); err != nil {
		t.Fatal(err)
	}
	p, res, err = r.Sync(ctx)
	if err != nil || len(p.Blocked) != 0 || len(res.Failed) != 0 {
		t.Fatalf("sync at level 2: %v\n%s", err, p)
	}
	e.mustEmptyPlan(r)
	srv, _ := e.w.Server(e.srv.ID)
	if srv.BannerURL == "" || e.role("VIP").IconURL == "" || e.role("Mod").Colors.Secondary != 0x445566 {
		t.Errorf("not applied at level 2")
	}

	// a new banner file is uploaded again, then nothing
	p3 := filepath.Join(root, "servers", "parity.yaml")
	data, _ := os.ReadFile(p3)
	os.WriteFile(p3, bytes.Replace(data, []byte("banner.png"), []byte("banner2.png"), 1), 0o644)
	repoDesired, _ := parityRepoReload(t, root)
	repoDesired.Spec.DiscordID = e.srv.ID
	r.SetDesired(repoDesired, root)
	p, _, err = r.Sync(ctx)
	if err != nil || len(p.Changes) != 1 || p.Changes[0].Fields["banner"].To != "./assets/banner2.png" {
		t.Fatalf("new banner: %v\n%s", err, p)
	}
	e.mustEmptyPlan(r)
}

func parityRepoReload(t *testing.T, root string) (*config.Server, string) {
	t.Helper()
	repo, diags := config.Load(root, config.LoadOptions{})
	if repo == nil || diags.HasErrors() {
		t.Fatalf("load: %v", diags.Errors())
	}
	s := repo.Servers["parity"]
	return s, root
}

func TestParityCommunityGating(t *testing.T) {
	// announcement channel and rules channel without Community: blocked at plan time
	desired, root := parityRepo(t, `
  profile:
    rulesChannel: rules
  channels:
    rules: {name: rules, type: text}
    news: {name: news, type: announcement}
`)
	e := newEnv(t, desired, root)
	r := e.reconciler(e.session)
	ctx := context.Background()
	p, res, err := r.Sync(ctx)
	if err != nil || len(res.Failed) > 0 {
		t.Fatalf("sync: %v %+v\n%s", err, res.Failed, p)
	}
	b := blockedWith(p, KindChannel, "community: announcement channels require the Community feature")
	if b == nil || blockedWith(p, KindServer, "community: the rules") == nil {
		t.Fatalf("not blocked:\n%s", p)
	}
	if h := ExplainBlocked(*b); h.Code != ErrCodeCommunityRequired {
		t.Errorf("explained = %+v", h)
	}

	// enabling Community without its prerequisites: blocked with every missing one
	desired, root = parityRepo(t, `
  channels:
    rules: {name: rules, type: text}
`)
	desired.Spec.Community = &config.CommunitySpec{Enabled: true, RulesChannel: "rules"} // invalid for codi validate too
	e = newEnv(t, desired, root)
	r = e.reconciler(e.session)
	p, err = r.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b = blockedWith(p, KindServer, "community: enabling Community requires")
	if b == nil || !strings.Contains(b.Reason, "community.updatesChannel") || !strings.Contains(b.Reason, "verificationLevel") ||
		!strings.Contains(b.Reason, "explicitContentFilter") {
		t.Fatalf("prerequisites not reported:\n%s", p)
	}

	// disabling Community
	desired, root = parityRepo(t, `
  profile: {verificationLevel: low, explicitContentFilter: allMembers}
  community: {enabled: true, rulesChannel: rules, updatesChannel: rules}
  channels:
    rules: {name: rules, type: text}
`)
	e = newEnv(t, desired, root)
	r = e.reconciler(e.session)
	if p, _, err := r.Sync(ctx); err != nil {
		t.Fatalf("enable: %v\n%s", err, p)
	}
	e.mustEmptyPlan(r)
	off := *e.desired
	off.Spec.Community = &config.CommunitySpec{Enabled: false}
	r.SetDesired(&off, root)
	p, _, err = r.Sync(ctx)
	if err != nil {
		t.Fatalf("disable: %v\n%s", err, p)
	}
	if srv, _ := e.w.Server(e.srv.ID); srv.HasFeature(discord.FeatureCommunity) {
		t.Error("community still enabled")
	}
	e.mustEmptyPlan(r)
}

func TestParityLimitsAndPrune(t *testing.T) {
	var spec strings.Builder
	spec.WriteString("  syncPolicy: {prune: true, deletionProtection: false}\n  stickers:\n")
	for _, k := range []string{"s1", "s2", "s3", "s4", "s5", "s6"} {
		spec.WriteString("    " + k + ": {name: Sticker " + k + ", emoji: wave, file: ./assets/sticker.png}\n")
	}
	spec.WriteString("  automod:\n    spam: {name: Spam, trigger: spam, actions: {block: {}}}\n")
	desired, root := parityRepo(t, spec.String())
	e := newEnv(t, desired, root)
	r := e.reconciler(e.session)
	ctx := context.Background()
	p, res, err := r.Sync(ctx)
	if err != nil || len(res.Failed) > 0 {
		t.Fatalf("sync: %v\n%s", err, p)
	}
	b := blockedWith(p, KindSticker, "limit: maximum number of stickers reached (5 at boost level 0)")
	if b == nil || b.Key != "s6" {
		t.Fatalf("6th sticker not blocked:\n%s", p)
	}
	if h := ExplainBlocked(*b); h.Code != ErrCodeLimitReached {
		t.Errorf("explained = %+v", h)
	}
	if n := len(e.w.Stickers(e.srv.ID)); n != 5 {
		t.Errorf("stickers = %d", n)
	}

	// removing keys with prune deletes the rule and a sticker
	cut := *e.desired
	cut.Spec.AutoMod = nil
	cut.Spec.Stickers = map[string]config.StickerSpec{}
	for k, v := range e.desired.Spec.Stickers {
		if k != "s1" {
			cut.Spec.Stickers[k] = v
		}
	}
	r.SetDesired(&cut, root)
	p, _, err = r.Sync(ctx)
	if err != nil {
		t.Fatalf("prune: %v\n%s", err, p)
	}
	deleted := map[string]bool{}
	for _, c := range p.Changes {
		if c.Action == ActionDelete {
			deleted[c.Kind+"/"+c.Key] = true
		}
	}
	if !deleted["automod/spam"] || !deleted["sticker/s1"] {
		t.Fatalf("deletes = %v\n%s", deleted, p)
	}
	if n := len(e.w.AutoModRules(e.srv.ID)); n != 0 {
		t.Errorf("automod rules left: %d", n)
	}
	// s6 now fits
	p, _, err = r.Sync(ctx)
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("s6: %v\n%s", err, p)
	}
	e.mustEmptyPlan(r)
}

func TestHumanizeParityErrors(t *testing.T) {
	cases := map[string]string{
		"reconcile parity: 1 change(s) failed: create emoji party: discord: bad request: maximum number of emojis reached (50)":                             ErrCodeLimitReached,
		"reconcile parity: 1 change(s) failed: update server parity: discord: bad request: banner requires boost level 2; server is level 0":                ErrCodeBoostRequired,
		"reconcile parity: 1 change(s) failed: update welcomeScreen welcomeScreen: discord: bad request: the welcome screen requires the Community feature": ErrCodeCommunityRequired,
		"reconcile parity: 1 change(s) failed: create automod spam: discord: missing permissions: need ManageServer":                                        ErrCodeMissingPermissions,
	}
	for raw, code := range cases {
		h := Humanize(raw)
		if h.Code != code {
			t.Errorf("%s → %s (%s), want %s", raw, h.Code, h.Message, code)
		}
		if h.Resource == nil {
			t.Errorf("%s: resource not parsed", raw)
		}
	}
	if h := Humanize(strings.Split(strings.Join([]string{"reconcile parity: 1 change(s) failed: create automod spam: discord: missing permissions: need ManageServer"}, ""), "\n")[0]); !strings.Contains(h.Message, "Manage Server") {
		t.Errorf("automod permission message = %q", h.Message)
	}
	for _, tc := range []struct{ reason, code string }{
		{"recreate: Discord cannot replace the file of an existing emoji: give it a new key", ErrCodeRecreateRequired},
		{"type change: the trigger of an AutoMod rule cannot change (spam → keyword): give the rule a new key", ErrCodeTypeChange},
	} {
		if h := ExplainBlocked(Blocked{Action: ActionUpdate, Kind: KindAutoMod, Key: "x", Reason: tc.reason}); h.Code != tc.code {
			t.Errorf("%s → %s", tc.reason, h.Code)
		}
	}
}
