package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// RulesChannelKey is community.rulesChannel, else the deprecated profile.rulesChannel.
func (sp *ServerSpec) RulesChannelKey() string {
	if sp.Community != nil && sp.Community.RulesChannel != "" {
		return sp.Community.RulesChannel
	}
	if sp.Profile != nil {
		return sp.Profile.RulesChannel
	}
	return ""
}

// CommunityDeclared reports whether the YAML turns Community on.
func (sp *ServerSpec) CommunityDeclared() bool { return sp.Community != nil && sp.Community.Enabled }

var verificationRank = map[string]int{"none": 0, "low": 1, "medium": 2, "high": 3, "highest": 4}

// AutoModTimeout parses an AutoMod timeout duration ("60s", "10m", "1h", "1d", "1w").
func AutoModTimeout(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	if err != nil || n <= 0 || unit == 0 {
		return 0, fmt.Errorf("invalid duration %q (e.g. 60s, 10m, 1h, 1d, 1w)", s)
	}
	return time.Duration(n) * unit, nil
}

// checkServerParity validates the Discord parity blocks of a server (https://cicord.dev/docs/reference/discord-parity/).
func (l *loader) checkServerParity(s *Server, doc *document) {
	sp := &s.Spec
	errf := func(path, format string, args ...any) {
		l.sink.errorf(s.File, doc.lineOf(path), path, format, args...)
	}
	warnf := func(path, format string, args ...any) {
		l.sink.warnf(s.File, doc.lineOf(path), path, format, args...)
	}
	chanType := func(k string) (string, bool) {
		c, ok := sp.Channels[k]
		return c.Type, ok
	}
	chanRef := func(path, k string, types ...string) {
		if k == "" {
			return
		}
		t, ok := chanType(k)
		if !ok {
			errf(path, "unknown channel %q", k)
			return
		}
		if len(types) > 0 && !contains(types, t) {
			errf(path, "channel %q is a %s channel; expected %s", k, t, strings.Join(types, " or "))
		}
	}
	roleRef := func(path, k string) {
		if _, ok := sp.Roles[k]; !ok {
			errf(path, "unknown role %q", k)
		}
	}
	dir := filepath.Join(l.root, filepath.Dir(filepath.FromSlash(s.File)))
	fileRef := func(path, rel string, max int) {
		if rel == "" {
			return
		}
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		switch {
		case err != nil:
			errf(path, "file %s not found", rel)
		case max > 0 && st.Size() > int64(max):
			errf(path, "file %s is %d KiB; Discord accepts at most %d KiB", rel, st.Size()/1024, max/1024)
		}
	}
	community := sp.CommunityDeclared()
	needCommunity := func(path, what string) {
		if community {
			return
		}
		if sp.Community != nil { // explicitly disabled
			errf(path, "%s requires Community (community.enabled is false)", what)
			return
		}
		warnf(path, "%s requires Community: declare community.enabled: true (the plan blocks it while the server is not a Community server)", what)
	}

	// profile
	if p := sp.Profile; p != nil {
		fileRef("/spec/profile/icon", p.Icon, 10<<20)
		fileRef("/spec/profile/banner", p.Banner, 10<<20)
		fileRef("/spec/profile/splash", p.Splash, 10<<20)
		if p.RulesChannel != "" {
			if sp.Community != nil && sp.Community.RulesChannel != "" && sp.Community.RulesChannel != p.RulesChannel {
				errf("/spec/profile/rulesChannel", "profile.rulesChannel (%q) and community.rulesChannel (%q) differ: keep community.rulesChannel only", p.RulesChannel, sp.Community.RulesChannel)
			} else {
				warnf("/spec/profile/rulesChannel", "profile.rulesChannel is deprecated: move it to community.rulesChannel")
			}
			needCommunity("/spec/profile/rulesChannel", "the rules channel")
		}
		if p.SystemChannel != "" {
			chanRef("/spec/profile/systemChannel", p.SystemChannel, "text")
		}
	}
	// community
	if c := sp.Community; c != nil {
		chanRef("/spec/community/rulesChannel", c.RulesChannel, "text")
		chanRef("/spec/community/updatesChannel", c.UpdatesChannel, "text")
		chanRef("/spec/community/safetyAlertsChannel", c.SafetyAlertsChannel, "text")
		if c.Enabled {
			if sp.RulesChannelKey() == "" {
				errf("/spec/community", "Community requires a rules channel (community.rulesChannel)")
			}
			if c.UpdatesChannel == "" {
				errf("/spec/community", "Community requires a community updates channel (community.updatesChannel)")
			}
			var pr ServerProfile
			if sp.Profile != nil {
				pr = *sp.Profile
			}
			switch {
			case pr.VerificationLevel == "":
				warnf("/spec/community", "Community requires a verification level of at least low: set profile.verificationLevel")
			case verificationRank[pr.VerificationLevel] < 1:
				errf("/spec/profile/verificationLevel", "Community requires a verification level of at least low")
			}
			switch {
			case pr.ExplicitContentFilter == "":
				warnf("/spec/community", "Community requires the explicit content filter on all members: set profile.explicitContentFilter: allMembers")
			case pr.ExplicitContentFilter != "allMembers":
				errf("/spec/profile/explicitContentFilter", "Community requires explicitContentFilter: allMembers")
			}
		}
		if c.SafetyAlertsChannel != "" || c.UpdatesChannel != "" {
			needCommunity("/spec/community", "the community updates and safety alerts channels")
		}
	}
	// afk / widget
	if a := sp.AFK; a != nil {
		chanRef("/spec/afk/channel", a.Channel, "voice")
		if a.Timeout != 0 && !discord.ValidAFKTimeout(a.Timeout) {
			errf("/spec/afk/timeout", "AFK timeout must be %s seconds", discord.AFKTimeoutsDescription)
		}
	}
	if w := sp.Widget; w != nil {
		chanRef("/spec/widget/channel", w.Channel)
	}
	// roles
	for _, k := range sortedKeys(sp.Roles) {
		r := sp.Roles[k]
		base := jsonPointer("spec", "roles", k)
		if r.Icon != "" && r.Emoji != "" {
			errf(base, "a role icon is either an image (icon) or an emoji (emoji), not both")
		}
		fileRef(base+"/icon", r.Icon, 256<<10)
		if c := r.Colors; c != nil {
			if r.Color != "" && !strings.EqualFold(r.Color, c.Primary) {
				errf(base+"/color", "color (%s) is the solid shorthand of colors.primary (%s): remove one", r.Color, c.Primary)
			}
			if c.Tertiary != "" && c.Secondary == "" {
				errf(base+"/colors", "colors.tertiary needs colors.secondary")
			}
		}
	}
	// channels
	for _, k := range sortedKeys(sp.Channels) {
		c := sp.Channels[k]
		base := jsonPointer("spec", "channels", k)
		voice := c.Type == "voice" || c.Type == "stage"
		forum := c.Type == "forum" || c.Type == "media"
		threads := c.Type == "text" || c.Type == "announcement" || forum
		only := func(field string, set, ok bool, types string) {
			if set && !ok {
				errf(base+"/"+field, "%s only applies to %s channels", field, types)
			}
		}
		only("voiceRegion", c.VoiceRegion != "", voice, "voice and stage")
		only("videoQuality", c.VideoQuality != "", voice, "voice and stage")
		only("threadArchiveAfter", c.ThreadArchiveAfter != "", threads, "text, announcement, forum and media")
		only("threadSlowmode", c.ThreadSlowmode != nil, threads, "text, announcement, forum and media")
		only("tags", c.Tags != nil, forum, "forum and media")
		only("defaultReaction", c.DefaultReaction != "", forum, "forum and media")
		only("sortOrder", c.SortOrder != "", forum, "forum and media")
		only("layout", c.Layout != "", c.Type == "forum", "forum")
		only("guidelines", c.Guidelines != "", forum, "forum and media")
		if c.Guidelines != "" && c.Topic != "" {
			errf(base+"/guidelines", "guidelines is the forum topic: set topic or guidelines, not both")
		}
		if (c.Type == "announcement" || c.Type == "stage") && !community {
			needCommunity(base+"/type", c.Type+" channels")
		}
		names := map[string]bool{}
		for _, tk := range sortedKeys(c.Tags) {
			n := strings.ToLower(c.Tags[tk].Name)
			if names[n] {
				errf(base+"/tags/"+tk, "duplicate tag name %q", c.Tags[tk].Name)
			}
			names[n] = true
		}
	}
	// automod
	perTrigger := map[string]int{}
	for _, k := range sortedKeys(sp.AutoMod) {
		r := sp.AutoMod[k]
		base := jsonPointer("spec", "automod", k)
		perTrigger[r.Trigger]++
		if n, max := perTrigger[r.Trigger], discord.AutoModRuleLimit(r.Trigger); max > 0 && n == max+1 {
			errf(base, "a server can have at most %d %s AutoMod rule(s)", max, r.Trigger)
		}
		field := func(name string, set bool, triggers ...string) {
			if set && !contains(triggers, r.Trigger) {
				errf(base+"/"+name, "%s does not apply to %s rules", name, r.Trigger)
			}
		}
		field("keywords", len(r.Keywords) > 0, discord.AutoModKeyword, discord.AutoModMemberProfile)
		field("regex", len(r.Regex) > 0, discord.AutoModKeyword, discord.AutoModMemberProfile)
		field("allowList", len(r.AllowList) > 0, discord.AutoModKeyword, discord.AutoModKeywordPreset, discord.AutoModMemberProfile)
		field("presets", len(r.Presets) > 0, discord.AutoModKeywordPreset)
		field("mentionLimit", r.MentionLimit > 0, discord.AutoModMentionSpam)
		field("mentionRaidProtection", r.MentionRaidProtection, discord.AutoModMentionSpam)
		switch r.Trigger {
		case discord.AutoModKeyword, discord.AutoModMemberProfile:
			if len(r.Keywords) == 0 && len(r.Regex) == 0 {
				errf(base, "%s rules need keywords or regex", r.Trigger)
			}
		case discord.AutoModKeywordPreset:
			if len(r.Presets) == 0 {
				errf(base, "keywordPreset rules need presets")
			}
		case discord.AutoModMentionSpam:
			if r.MentionLimit == 0 && !r.MentionRaidProtection {
				errf(base, "mentionSpam rules need mentionLimit (or mentionRaidProtection)")
			}
		}
		if max := discord.AutoModMaxAllowList; r.Trigger != discord.AutoModKeywordPreset && len(r.AllowList) > max {
			errf(base+"/allowList", "at most %d allow list entries for %s rules", max, r.Trigger)
		}
		for i, re := range r.Regex {
			if _, err := regexp.Compile(re); err != nil {
				errf(fmt.Sprintf("%s/regex/%d", base, i), "invalid regular expression: %v", err)
			}
		}
		a := r.Actions
		if a.Block == nil && a.Alert == "" && a.Timeout == "" {
			errf(base+"/actions", "at least one action (block, alert or timeout) is required")
		}
		if a.Alert != "" {
			chanRef(base+"/actions/alert", a.Alert, "text", "announcement")
			if !discord.AutoModAllowsAction(r.Trigger, discord.AutoModActionAlert) {
				errf(base+"/actions/alert", "alert is not available for %s rules", r.Trigger)
			}
		}
		if a.Block != nil && r.Trigger != discord.AutoModMemberProfile && !discord.AutoModAllowsAction(r.Trigger, discord.AutoModActionBlock) {
			errf(base+"/actions/block", "block is not available for %s rules", r.Trigger)
		}
		if a.Timeout != "" {
			if !discord.AutoModAllowsAction(r.Trigger, discord.AutoModActionTimeout) {
				errf(base+"/actions/timeout", "timeout is only available for keyword and mentionSpam rules")
			} else if d, err := AutoModTimeout(a.Timeout); err != nil {
				errf(base+"/actions/timeout", "%v", err)
			} else if d > time.Duration(discord.AutoModMaxTimeoutSeconds)*time.Second {
				errf(base+"/actions/timeout", "timeout is at most 4 weeks")
			}
		}
		for i, rk := range r.ExemptRoles {
			roleRef(fmt.Sprintf("%s/exemptRoles/%d", base, i), rk)
		}
		for i, ck := range r.ExemptChannels {
			chanRef(fmt.Sprintf("%s/exemptChannels/%d", base, i), ck)
		}
	}
	// onboarding
	if o := sp.Onboarding; o != nil {
		if o.Enabled {
			needCommunity("/spec/onboarding", "Discord onboarding")
			if orDefaultStr(o.Mode, "default") == "default" && len(o.DefaultChannels) < discord.OnboardingMinDefaultChans {
				errf("/spec/onboarding/defaultChannels", "Discord onboarding needs at least %d default channels (%d of them where @everyone can send messages); %d declared",
					discord.OnboardingMinDefaultChans, discord.OnboardingMinChattable, len(o.DefaultChannels))
			}
		}
		for i, ck := range o.DefaultChannels {
			chanRef(fmt.Sprintf("/spec/onboarding/defaultChannels/%d", i), ck)
		}
		if o.Enabled {
			checkOnboardingAccess(sp, o, errf)
		}
		keys := map[string]bool{}
		for i, pr := range o.Prompts {
			base := fmt.Sprintf("/spec/onboarding/prompts/%d", i)
			if keys[pr.Key] {
				errf(base+"/key", "duplicate question key %q", pr.Key)
			}
			keys[pr.Key] = true
			for j, op := range pr.Options {
				ob := fmt.Sprintf("%s/options/%d", base, j)
				if len(op.Roles) == 0 && len(op.Channels) == 0 {
					errf(ob, "an answer must give at least one role or channel")
				}
				for x, rk := range op.Roles {
					roleRef(fmt.Sprintf("%s/roles/%d", ob, x), rk)
				}
				for x, ck := range op.Channels {
					chanRef(fmt.Sprintf("%s/channels/%d", ob, x), ck)
				}
			}
		}
	}
	if w := sp.WelcomeScreen; w != nil {
		if w.IsEnabled() {
			needCommunity("/spec/welcomeScreen", "the welcome screen")
		}
		for i, c := range w.Channels {
			chanRef(fmt.Sprintf("/spec/welcomeScreen/channels/%d/channel", i), c.Channel)
		}
	}
	// expressions
	names := map[string]string{}
	for _, k := range sortedKeys(sp.Emojis) {
		e := sp.Emojis[k]
		base := jsonPointer("spec", "emojis", k)
		fileRef(base+"/image", e.Image, discord.MaxEmojiImageBytes)
		if other, dup := names[e.Name]; dup {
			errf(base+"/name", "emoji name %q is also used by emojis.%s", e.Name, other)
		}
		names[e.Name] = k
		for i, rk := range e.Roles {
			roleRef(fmt.Sprintf("%s/roles/%d", base, i), rk)
		}
	}
	for _, k := range sortedKeys(sp.Stickers) {
		fileRef(jsonPointer("spec", "stickers", k, "file"), sp.Stickers[k].File, discord.MaxStickerFileBytes)
	}
	for _, k := range sortedKeys(sp.Soundboard) {
		fileRef(jsonPointer("spec", "soundboard", k, "sound"), sp.Soundboard[k].Sound, discord.MaxSoundFileBytes)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func orDefaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// checkOnboardingAccess applies Discord's onboarding rules that depend on @everyone's
// permissions, when the YAML declares them (spec.everyone): every default channel must be
// open to @everyone (voice and stage: Connect too), and at least
// discord.OnboardingMinChattable counted channels must let @everyone send messages.
func checkOnboardingAccess(sp *ServerSpec, o *OnboardingSpec, errf func(path, format string, args ...any)) {
	if sp.Everyone == nil {
		return // @everyone is not managed: its live permissions are unknown here
	}
	base, err := discord.ParsePermissions(sp.Everyone.Permissions)
	if err != nil {
		return // reported by the permission checks
	}
	admin := discord.MustPermissions("Administrator")
	view, send, connect := discord.MustPermissions("ViewChannel"), discord.MustPermissions("SendMessages"), discord.MustPermissions("Connect")
	everyone := func(c Channel) discord.Permissions {
		if base&admin != 0 {
			return ^discord.Permissions(0)
		}
		ows := c.Overwrites
		if c.SyncsWithCategory() {
			ows = sp.Categories[c.Category].Overwrites
		}
		p := base
		if ow, ok := ows["@everyone"]; ok {
			deny, _ := discord.ParsePermissions(ow.Deny)
			allow, _ := discord.ParsePermissions(ow.Allow)
			p = p&^deny | allow
		}
		return p
	}
	isVoice := func(c Channel) bool { return c.Type == "voice" || c.Type == "stage" }
	for i, ck := range o.DefaultChannels {
		c, ok := sp.Channels[ck]
		if !ok {
			continue
		}
		p := everyone(c)
		switch {
		case !p.Has(view):
			errf(fmt.Sprintf("/spec/onboarding/defaultChannels/%d", i), "@everyone cannot see channel %q: Discord only accepts default channels open to @everyone", ck)
		case isVoice(c) && !p.Has(connect):
			errf(fmt.Sprintf("/spec/onboarding/defaultChannels/%d", i), "@everyone cannot connect to voice channel %q: Discord only accepts default channels open to @everyone (add Connect to everyone.permissions or allow it on the channel)", ck)
		}
	}
	counted := append([]string{}, o.DefaultChannels...)
	if orDefaultStr(o.Mode, "default") == "advanced" {
		for _, pr := range o.Prompts {
			for _, op := range pr.Options {
				counted = append(counted, op.Channels...)
			}
		}
	}
	slices.Sort(counted)
	counted = slices.Compact(counted)
	chattable := 0
	for _, ck := range counted {
		if c, ok := sp.Channels[ck]; ok && everyone(c).Has(view|send) {
			chattable++
		}
	}
	if len(counted) >= discord.OnboardingMinDefaultChans && chattable < discord.OnboardingMinChattable {
		errf("/spec/onboarding/defaultChannels", "Discord onboarding needs %d default channels where @everyone can send messages; %d here (add SendMessages to everyone.permissions, or allow it on those channels)",
			discord.OnboardingMinChattable, chattable)
	}
}
