package sim

// Discord parity: server widget, AutoMod, native onboarding, welcome screen, emojis,
// stickers, soundboard, boost level and feature flags. The rules mirror Discord's API
// documentation (limits in internal/discord/limits.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
)

var (
	permManageExpressions = discord.MustPermissions("ManageExpressions")
	permCreateExpressions = discord.MustPermissions("CreateExpressions")
)

// ───────────────────────────────────────────────────────────── boosts & features

// SetBoosts sets the number of boosts of a server: PremiumSubscriptionCount, the boost
// level (discord.BoostTierFor) and the feature flags that level unlocks. Flags added
// with SetFeatures are kept. It emits ServerUpdateEvent.
func (w *World) SetBoosts(serverID string, n int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	if n < 0 {
		return badRequest("boost count must be >= 0")
	}
	tier := discord.BoostTierFor(n)
	s.info.PremiumSubscriptionCount = n
	s.info.PremiumTier = tier
	feats := removeFeature(s.info.Features, discord.TierFeatures(3)...)
	for _, f := range discord.TierFeatures(tier) {
		feats = addFeature(feats, f)
	}
	for f := range s.pinned {
		feats = addFeature(feats, f)
	}
	s.info.Features = feats
	w.emit(discord.ServerUpdateEvent{Server: cloneServer(s.info)})
	w.changed()
	return nil
}

// SetFeatures adds and removes arbitrary feature flags (e.g. PARTNERED, or BANNER on a
// partnered server without boosts), bypassing Discord's rules. Added flags survive
// SetBoosts. It emits ServerUpdateEvent.
func (w *World) SetFeatures(serverID string, add, remove []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	if s.pinned == nil {
		s.pinned = map[string]bool{}
	}
	for _, f := range remove {
		s.info.Features = removeFeature(s.info.Features, f)
		delete(s.pinned, f)
	}
	for _, f := range add {
		s.info.Features = addFeature(s.info.Features, f)
		s.pinned[f] = true
	}
	w.emit(discord.ServerUpdateEvent{Server: cloneServer(s.info)})
	w.changed()
	return nil
}

func (w *World) setFeature(s *serverState, f string, on bool) {
	if on {
		s.info.Features = addFeature(s.info.Features, f)
	} else {
		s.info.Features = removeFeature(s.info.Features, f)
	}
}

// ───────────────────────────────────────────────────────────── widget

func (w *World) getWidget(a actor, serverID string) (discord.ServerWidget, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err != nil {
		return discord.ServerWidget{}, err
	}
	return s.widget, nil
}

func (w *World) updateWidget(a actor, serverID string, ws discord.ServerWidget, reason string) (discord.ServerWidget, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err == nil && ws.ChannelID != "" {
		if c := w.channels[ws.ChannelID]; c == nil || c.ch.ServerID != serverID || isThread(c.ch.Type) || c.ch.Type == discord.ChannelCategory {
			err = badRequest("widget channel must be a channel of the server")
		}
	}
	if err != nil {
		return discord.ServerWidget{}, w.record(a, "UpdateServerWidget", serverID, serverID, reason, err)
	}
	s.widget = ws
	w.recordOK(a, "UpdateServerWidget", serverID, serverID, reason)
	return ws, nil
}

// ───────────────────────────────────────────────────────────── AutoMod

func (w *World) listAutoMod(a actor, serverID string) ([]discord.AutoModRule, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err != nil {
		return nil, err
	}
	return w.sortedAutoMod(s), nil
}

func (w *World) sortedAutoMod(s *serverState) []discord.AutoModRule {
	out := make([]discord.AutoModRule, 0, len(s.automod))
	for _, r := range s.automod {
		out = append(out, cloneAutoMod(*r))
	}
	sort.Slice(out, func(i, j int) bool { return snowLess(out[i].ID, out[j].ID) })
	return out
}

var autoModTriggers = map[string]bool{
	discord.AutoModKeyword: true, discord.AutoModSpam: true, discord.AutoModKeywordPreset: true,
	discord.AutoModMentionSpam: true, discord.AutoModMemberProfile: true,
}

var autoModPresets = map[string]bool{"profanity": true, "sexualContent": true, "slurs": true}

// validateAutoMod checks a rule against Discord's limits. Caller holds w.mu.
func (w *World) validateAutoMod(a actor, s *serverState, r discord.AutoModRule) error {
	if n := utf8.RuneCountInString(r.Name); n < 1 || n > discord.AutoModMaxRuleNameLength {
		return badRequest("rule name must be 1-%d characters", discord.AutoModMaxRuleNameLength)
	}
	t := r.Trigger
	if !autoModTriggers[t] {
		return badRequest("invalid trigger type %q", t)
	}
	keywordish := t == discord.AutoModKeyword || t == discord.AutoModMemberProfile
	strs := func(field string, list []string, maxN, maxLen int) error {
		if len(list) > maxN {
			return badRequest("%s: at most %d entries", field, maxN)
		}
		for _, k := range list {
			if n := utf8.RuneCountInString(k); n < 1 || n > maxLen {
				return badRequest("%s: each entry must be 1-%d characters", field, maxLen)
			}
		}
		return nil
	}
	if len(r.Keywords) > 0 && !keywordish {
		return badRequest("keyword_filter is only valid for keyword and member profile rules")
	}
	if err := strs("keyword_filter", r.Keywords, discord.AutoModMaxKeywords, discord.AutoModMaxKeywordLength); err != nil {
		return err
	}
	if len(r.Regex) > 0 && !keywordish {
		return badRequest("regex_patterns is only valid for keyword and member profile rules")
	}
	if err := strs("regex_patterns", r.Regex, discord.AutoModMaxRegex, discord.AutoModMaxRegexLength); err != nil {
		return err
	}
	for _, re := range r.Regex {
		if _, err := regexp.Compile(re); err != nil {
			return badRequest("regex_patterns: invalid pattern %q", re)
		}
	}
	if len(r.Presets) > 0 && t != discord.AutoModKeywordPreset {
		return badRequest("presets is only valid for keyword preset rules")
	}
	for _, p := range r.Presets {
		if !autoModPresets[p] {
			return badRequest("presets: invalid preset %q", p)
		}
	}
	switch {
	case len(r.AllowList) > 0 && t != discord.AutoModKeyword && t != discord.AutoModKeywordPreset && t != discord.AutoModMemberProfile:
		return badRequest("allow_list is only valid for keyword, keyword preset and member profile rules")
	case t == discord.AutoModKeywordPreset:
		if err := strs("allow_list", r.AllowList, discord.AutoModMaxPresetAllowList, discord.AutoModMaxKeywordLength); err != nil {
			return err
		}
	default:
		if err := strs("allow_list", r.AllowList, discord.AutoModMaxAllowList, discord.AutoModMaxKeywordLength); err != nil {
			return err
		}
	}
	if t == discord.AutoModMentionSpam {
		if r.MentionLimit < 1 || r.MentionLimit > discord.AutoModMaxMentionLimit {
			return badRequest("mention_total_limit must be 1-%d", discord.AutoModMaxMentionLimit)
		}
	} else if r.MentionLimit != 0 || r.MentionRaidProtection {
		return badRequest("mention_total_limit and mention_raid_protection_enabled are only valid for mention spam rules")
	}
	if len(r.Actions) == 0 {
		return badRequest("a rule needs at least one action")
	}
	for _, act := range r.Actions {
		if !discord.AutoModAllowsAction(t, act.Type) {
			return badRequest("action %q is not allowed for %s rules", act.Type, t)
		}
		switch act.Type {
		case discord.AutoModActionBlock:
			if utf8.RuneCountInString(act.CustomMessage) > discord.AutoModMaxCustomMessage {
				return badRequest("custom_message must be at most %d characters", discord.AutoModMaxCustomMessage)
			}
		case discord.AutoModActionAlert:
			c := w.channels[act.ChannelID]
			if c == nil || c.ch.ServerID != s.info.ID || (c.ch.Type != discord.ChannelText && c.ch.Type != discord.ChannelAnnouncement) {
				return badRequest("alert channel must be a text channel of the server")
			}
		case discord.AutoModActionTimeout:
			if act.DurationSeconds < 1 || act.DurationSeconds > discord.AutoModMaxTimeoutSeconds {
				return badRequest("timeout duration_seconds must be 1-%d", discord.AutoModMaxTimeoutSeconds)
			}
			if p := w.basePerms(s, a.userID); !p.Has(permModerate) {
				return missingErr(p, permModerate)
			}
		}
	}
	if len(r.ExemptRoles) > discord.AutoModMaxExemptRoles {
		return badRequest("exempt_roles: at most %d roles", discord.AutoModMaxExemptRoles)
	}
	for _, id := range r.ExemptRoles {
		if s.roles[id] == nil {
			return badRequest("exempt_roles: unknown role %s", id)
		}
	}
	if len(r.ExemptChannels) > discord.AutoModMaxExemptChannels {
		return badRequest("exempt_channels: at most %d channels", discord.AutoModMaxExemptChannels)
	}
	for _, id := range r.ExemptChannels {
		if c := w.channels[id]; c == nil || c.ch.ServerID != s.info.ID {
			return badRequest("exempt_channels: unknown channel %s", id)
		}
	}
	return nil
}

func (w *World) createAutoMod(a actor, serverID string, r discord.AutoModRule, reason string) (discord.AutoModRule, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.AutoModRule, error) {
		return discord.AutoModRule{}, w.record(a, "CreateAutoModRule", serverID, "", reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err == nil {
		err = w.validateAutoMod(a, s, r)
	}
	if err != nil {
		return fail(err)
	}
	n := 0
	for _, x := range s.automod {
		if x.Trigger == r.Trigger {
			n++
		}
	}
	if limit := discord.AutoModRuleLimit(r.Trigger); n >= limit {
		return fail(badRequest("maximum number of %s rules reached (%d)", r.Trigger, limit))
	}
	r = cloneAutoMod(r)
	r.ID = w.newID()
	s.automod[r.ID] = &r
	w.recordOK(a, "CreateAutoModRule", serverID, r.ID, reason)
	w.emit(discord.AutoModRuleCreateEvent{ServerID: serverID, Rule: cloneAutoMod(r)})
	return cloneAutoMod(r), nil
}

func (w *World) updateAutoMod(a actor, serverID, ruleID string, r discord.AutoModRule, reason string) (discord.AutoModRule, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.AutoModRule, error) {
		return discord.AutoModRule{}, w.record(a, "UpdateAutoModRule", serverID, ruleID, reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err != nil {
		return fail(err)
	}
	cur := s.automod[ruleID]
	if cur == nil {
		return fail(notFound("unknown auto moderation rule %s", ruleID))
	}
	if r.Trigger != "" && r.Trigger != cur.Trigger {
		return fail(badRequest("the trigger type of a rule cannot be changed (%s → %s)", cur.Trigger, r.Trigger))
	}
	r.Trigger = cur.Trigger
	if err := w.validateAutoMod(a, s, r); err != nil {
		return fail(err)
	}
	r = cloneAutoMod(r)
	r.ID = ruleID
	s.automod[ruleID] = &r
	w.recordOK(a, "UpdateAutoModRule", serverID, ruleID, reason)
	w.emit(discord.AutoModRuleUpdateEvent{ServerID: serverID, Rule: cloneAutoMod(r)})
	return cloneAutoMod(r), nil
}

func (w *World) deleteAutoMod(a actor, serverID, ruleID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err == nil && s.automod[ruleID] == nil {
		err = notFound("unknown auto moderation rule %s", ruleID)
	}
	if err == nil && s.automod[ruleID].Trigger == discord.AutoModMentionSpam && s.info.HasFeature(discord.FeatureCommunity) {
		// Discord: 403 code 200006 "Mention Spam AutoMod rule cannot be deleted from community servers"
		err = forbidden("Mention Spam AutoMod rule cannot be deleted from community servers")
	}
	if err != nil {
		return w.record(a, "DeleteAutoModRule", serverID, ruleID, reason, err)
	}
	old := cloneAutoMod(*s.automod[ruleID])
	delete(s.automod, ruleID)
	w.recordOK(a, "DeleteAutoModRule", serverID, ruleID, reason)
	w.emit(discord.AutoModRuleDeleteEvent{ServerID: serverID, Rule: old})
	return nil
}

var permConnect = discord.MustPermissions("Connect")

// ───────────────────────────────────────────────────────────── onboarding

func (w *World) getOnboarding(a actor, serverID string) (discord.Onboarding, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return discord.Onboarding{}, err
	}
	if s.members[a.userID] == nil {
		return discord.Onboarding{}, forbidden("missing access to server %s", serverID)
	}
	return cloneOnboarding(s.onboarding), nil
}

// everyoneCan reports whether @everyone has perm in a channel (server permissions and
// the channel's @everyone overwrite). Caller holds w.mu.
func (w *World) everyoneCan(s *serverState, c *channelState, perm discord.Permissions) bool {
	p := s.roles[s.info.ID].Permissions
	if p&permAdmin != 0 {
		return true
	}
	for _, ow := range c.ch.Overwrites {
		if ow.Type == discord.OverwriteRole && ow.ID == s.info.ID {
			p = p&^ow.Deny | ow.Allow
		}
	}
	return p.Has(perm | permView)
}

func (w *World) updateOnboarding(a actor, serverID string, o discord.Onboarding, reason string) (discord.Onboarding, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Onboarding, error) {
		return discord.Onboarding{}, w.record(a, "UpdateOnboarding", serverID, serverID, reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer|permManageRoles)
	}
	if err != nil {
		return fail(err)
	}
	o = cloneOnboarding(o)
	if o.Mode == "" {
		o.Mode = "default"
	}
	if o.Mode != "default" && o.Mode != "advanced" {
		return fail(badRequest("onboarding mode must be default or advanced"))
	}
	chanOK := func(id string) bool {
		c := w.channels[id]
		return c != nil && c.ch.ServerID == serverID && !isThread(c.ch.Type)
	}
	for _, id := range o.DefaultChannelIDs {
		if !chanOK(id) {
			return fail(badRequest("default_channel_ids: unknown channel %s", id))
		}
		// Discord: DEFAULT_CHANNEL_REQUIRES_EVERYONE_ACCESS (view; voice and stage: connect too).
		need := discord.Permissions(0)
		if t := w.channels[id].ch.Type; t == discord.ChannelVoice || t == discord.ChannelStage {
			need = permConnect
		}
		if !w.everyoneCan(s, w.channels[id], need) {
			return fail(badRequest("default_channel_ids: Default channel requires @everyone access (%s)", id))
		}
	}
	counted := append([]string{}, o.DefaultChannelIDs...)
	for i := range o.Prompts {
		pr := &o.Prompts[i]
		if pr.Type == "" {
			pr.Type = "multipleChoice"
		}
		if pr.Type != "multipleChoice" && pr.Type != "dropdown" {
			return fail(badRequest("prompt type must be multipleChoice or dropdown"))
		}
		if n := utf8.RuneCountInString(pr.Title); n < 1 || n > 100 {
			return fail(badRequest("prompt title must be 1-100 characters"))
		}
		if len(pr.Options) < 1 || len(pr.Options) > 50 {
			return fail(badRequest("a prompt needs 1-50 options"))
		}
		if pr.ID == "" {
			pr.ID = w.newID()
		}
		for j := range pr.Options {
			op := &pr.Options[j]
			if n := utf8.RuneCountInString(op.Title); n < 1 || n > 50 {
				return fail(badRequest("option title must be 1-50 characters"))
			}
			if utf8.RuneCountInString(op.Description) > 100 {
				return fail(badRequest("option description must be at most 100 characters"))
			}
			if len(op.RoleIDs) == 0 && len(op.ChannelIDs) == 0 {
				return fail(badRequest("option %q must grant at least one role or channel", op.Title))
			}
			for _, id := range op.RoleIDs {
				if s.roles[id] == nil || id == serverID {
					return fail(badRequest("option %q: unknown role %s", op.Title, id))
				}
			}
			for _, id := range op.ChannelIDs {
				if !chanOK(id) {
					return fail(badRequest("option %q: unknown channel %s", op.Title, id))
				}
			}
			if op.ID == "" {
				op.ID = w.newID()
			}
			if o.Mode == "advanced" {
				counted = append(counted, op.ChannelIDs...)
			}
		}
	}
	if o.Enabled {
		if !s.info.HasFeature(discord.FeatureCommunity) {
			return fail(badRequest("onboarding requires the Community feature"))
		}
		slices.Sort(counted)
		counted = slices.Compact(counted)
		chattable := 0
		for _, id := range counted {
			if w.everyoneCan(s, w.channels[id], permSend) {
				chattable++
			}
		}
		if len(counted) < discord.OnboardingMinDefaultChans || chattable < discord.OnboardingMinChattable {
			return fail(badRequest("onboarding needs at least %d default channels, %d of which @everyone can send messages in (got %d, %d)",
				discord.OnboardingMinDefaultChans, discord.OnboardingMinChattable, len(counted), chattable))
		}
	}
	s.onboarding = o
	had := s.info.HasFeature(discord.FeatureGuildOnboarding)
	w.setFeature(s, discord.FeatureGuildOnboarding, o.Enabled)
	w.recordOK(a, "UpdateOnboarding", serverID, serverID, reason)
	if had != o.Enabled {
		w.emit(discord.ServerUpdateEvent{Server: cloneServer(s.info)})
	}
	w.changed()
	return cloneOnboarding(o), nil
}

// ───────────────────────────────────────────────────────────── welcome screen

func (w *World) getWelcome(a actor, serverID string) (discord.WelcomeScreen, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err != nil {
		return discord.WelcomeScreen{}, err
	}
	return cloneWelcome(s.welcome), nil
}

func (w *World) updateWelcome(a actor, serverID string, ws discord.WelcomeScreen, reason string) (discord.WelcomeScreen, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.WelcomeScreen, error) {
		return discord.WelcomeScreen{}, w.record(a, "UpdateWelcomeScreen", serverID, serverID, reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err != nil {
		return fail(err)
	}
	if ws.Enabled && !s.info.HasFeature(discord.FeatureCommunity) {
		return fail(badRequest("the welcome screen requires the Community feature"))
	}
	if utf8.RuneCountInString(ws.Description) > discord.WelcomeScreenMaxDesc {
		return fail(badRequest("welcome screen description must be at most %d characters", discord.WelcomeScreenMaxDesc))
	}
	if len(ws.Channels) > discord.WelcomeScreenMaxChannels {
		return fail(badRequest("the welcome screen can show at most %d channels", discord.WelcomeScreenMaxChannels))
	}
	for _, wc := range ws.Channels {
		c := w.channels[wc.ChannelID]
		if c == nil || c.ch.ServerID != serverID || isThread(c.ch.Type) || c.ch.Type == discord.ChannelCategory {
			return fail(badRequest("welcome channel %s is not a channel of the server", wc.ChannelID))
		}
		if n := utf8.RuneCountInString(wc.Description); n < 1 || n > 42 {
			return fail(badRequest("welcome channel description must be 1-42 characters"))
		}
	}
	s.welcome = cloneWelcome(ws)
	had := s.info.HasFeature(discord.FeatureWelcomeScreenEnabled)
	w.setFeature(s, discord.FeatureWelcomeScreenEnabled, ws.Enabled)
	w.recordOK(a, "UpdateWelcomeScreen", serverID, serverID, reason)
	if had != ws.Enabled {
		w.emit(discord.ServerUpdateEvent{Server: cloneServer(s.info)})
	}
	w.changed()
	return cloneWelcome(s.welcome), nil
}

// ───────────────────────────────────────────────────────────── expressions

var reEmojiName = regexp.MustCompile(`^[A-Za-z0-9_]{2,32}$`)

func imageFormat(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		if i := bytes.Index(b, []byte("IDAT")); bytes.Contains(b[:max(i, 0)], []byte("acTL")) {
			return "apng"
		}
		return "png"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "gif"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	}
	t := bytes.TrimSpace(b)
	if len(t) > 0 && t[0] == '{' && json.Valid(t) {
		return "lottie"
	}
	return ""
}

func soundFormat(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("OggS")):
		return "ogg"
	case bytes.HasPrefix(b, []byte("ID3")), len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0:
		return "mp3"
	}
	return ""
}

func (w *World) requireCreateExpr(a actor, s *serverState) error {
	if s.members[a.userID] == nil {
		return forbidden("missing access to server %s", s.info.ID)
	}
	p := w.basePerms(s, a.userID)
	if p.Has(permManageExpressions) || p.Has(permCreateExpressions) {
		return nil
	}
	return missingErr(p, permCreateExpressions)
}

func (w *World) checkRoleIDs(s *serverState, ids []string) error {
	for _, id := range ids {
		if s.roles[id] == nil || id == s.info.ID {
			return badRequest("unknown role %s", id)
		}
	}
	return nil
}

func (w *World) listEmojis(a actor, serverID string) ([]discord.Emoji, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if s.members[a.userID] == nil {
		return nil, forbidden("missing access to server %s", serverID)
	}
	return sortedEmojis(s), nil
}

func sortedEmojis(s *serverState) []discord.Emoji {
	out := make([]discord.Emoji, 0, len(s.emojis))
	for _, e := range s.emojis {
		out = append(out, cloneEmoji(*e))
	}
	sort.Slice(out, func(i, j int) bool { return snowLess(out[i].ID, out[j].ID) })
	return out
}

func (w *World) createEmoji(a actor, serverID string, p discord.EmojiParams, reason string) (discord.Emoji, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Emoji, error) {
		return discord.Emoji{}, w.record(a, "CreateEmoji", serverID, "", reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireCreateExpr(a, s)
	}
	if err != nil {
		return fail(err)
	}
	if !reEmojiName.MatchString(p.Name) {
		return fail(badRequest("emoji name must be 2-32 characters: letters, digits and underscores"))
	}
	if len(p.Image) == 0 {
		return fail(badRequest("emoji image is required"))
	}
	if len(p.Image) > discord.MaxEmojiImageBytes {
		return fail(badRequest("emoji image must be at most 256 KiB"))
	}
	f := imageFormat(p.Image)
	if f != "png" && f != "apng" && f != "jpeg" && f != "gif" && f != "webp" {
		return fail(badRequest("emoji image must be png, jpeg, gif or webp"))
	}
	animated := f == "gif"
	n := 0
	for _, e := range s.emojis {
		if e.Animated == animated && !e.Managed {
			n++
		}
	}
	if limit := discord.EmojiLimit(s.info.PremiumTier); n >= limit {
		kind := "emojis"
		if animated {
			kind = "animated emojis"
		}
		return fail(badRequest("maximum number of %s reached (%d)", kind, limit))
	}
	e := discord.Emoji{Name: p.Name, Animated: animated, RoleIDs: []string{}}
	if p.RoleIDs != nil {
		if err := w.checkRoleIDs(s, *p.RoleIDs); err != nil {
			return fail(err)
		}
		e.RoleIDs = append([]string{}, *p.RoleIDs...)
	}
	e.ID = w.newID()
	s.emojis[e.ID] = &e
	w.cdnPut(e.ImageURL(), p.Image)
	w.recordOK(a, "CreateEmoji", serverID, e.ID, reason)
	w.emit(discord.EmojisUpdateEvent{ServerID: serverID, Emojis: sortedEmojis(s)})
	return cloneEmoji(e), nil
}

func (w *World) updateEmoji(a actor, serverID, emojiID string, p discord.EmojiParams, reason string) (discord.Emoji, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Emoji, error) {
		return discord.Emoji{}, w.record(a, "UpdateEmoji", serverID, emojiID, reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageExpressions)
	}
	if err != nil {
		return fail(err)
	}
	e := s.emojis[emojiID]
	if e == nil {
		return fail(notFound("unknown emoji %s", emojiID))
	}
	if e.Managed {
		return fail(forbidden("cannot edit managed emoji %s", e.Name))
	}
	if p.Name != "" && !reEmojiName.MatchString(p.Name) {
		return fail(badRequest("emoji name must be 2-32 characters: letters, digits and underscores"))
	}
	if p.RoleIDs != nil {
		if err := w.checkRoleIDs(s, *p.RoleIDs); err != nil {
			return fail(err)
		}
	}
	if p.Name != "" {
		e.Name = p.Name
	}
	if p.RoleIDs != nil {
		e.RoleIDs = append([]string{}, *p.RoleIDs...)
	}
	w.recordOK(a, "UpdateEmoji", serverID, emojiID, reason)
	w.emit(discord.EmojisUpdateEvent{ServerID: serverID, Emojis: sortedEmojis(s)})
	return cloneEmoji(*e), nil
}

func (w *World) deleteEmoji(a actor, serverID, emojiID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageExpressions)
	}
	if err == nil && s.emojis[emojiID] == nil {
		err = notFound("unknown emoji %s", emojiID)
	}
	if err != nil {
		return w.record(a, "DeleteEmoji", serverID, emojiID, reason, err)
	}
	delete(s.emojis, emojiID)
	w.recordOK(a, "DeleteEmoji", serverID, emojiID, reason)
	w.emit(discord.EmojisUpdateEvent{ServerID: serverID, Emojis: sortedEmojis(s)})
	return nil
}

func (w *World) listStickers(a actor, serverID string) ([]discord.Sticker, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if s.members[a.userID] == nil {
		return nil, forbidden("missing access to server %s", serverID)
	}
	return sortedStickers(s), nil
}

func sortedStickers(s *serverState) []discord.Sticker {
	out := make([]discord.Sticker, 0, len(s.stickers))
	for _, st := range s.stickers {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return snowLess(out[i].ID, out[j].ID) })
	return out
}

func validateStickerText(p discord.StickerParams) error {
	if n := utf8.RuneCountInString(p.Name); n < 2 || n > 30 {
		return badRequest("sticker name must be 2-30 characters")
	}
	if n := utf8.RuneCountInString(p.Description); n != 0 && (n < 2 || n > 100) {
		return badRequest("sticker description must be empty or 2-100 characters")
	}
	if n := utf8.RuneCountInString(p.Tags); n < 1 || n > 200 {
		return badRequest("sticker tags must be 1-200 characters")
	}
	return nil
}

func (w *World) createSticker(a actor, serverID string, p discord.StickerParams, reason string) (discord.Sticker, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Sticker, error) {
		return discord.Sticker{}, w.record(a, "CreateSticker", serverID, "", reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireCreateExpr(a, s)
	}
	if err == nil {
		err = validateStickerText(p)
	}
	if err != nil {
		return fail(err)
	}
	if len(p.File) == 0 {
		return fail(badRequest("sticker file is required"))
	}
	if len(p.File) > discord.MaxStickerFileBytes {
		return fail(badRequest("sticker file must be at most 512 KiB"))
	}
	f := imageFormat(p.File)
	switch f {
	case "png", "apng", "gif":
	case "lottie":
		if !s.info.HasFeature("VERIFIED") && !s.info.HasFeature("PARTNERED") {
			return fail(badRequest("lottie stickers require a verified or partnered server"))
		}
	default:
		return fail(badRequest("sticker file must be png, apng, gif or lottie json"))
	}
	if limit := discord.StickerLimit(s.info.PremiumTier); len(s.stickers) >= limit {
		return fail(badRequest("maximum number of stickers reached (%d)", limit))
	}
	st := discord.Sticker{ID: w.newID(), Name: p.Name, Description: p.Description, Tags: p.Tags, Format: f}
	s.stickers[st.ID] = &st
	w.cdnPut(st.FileURL(), p.File)
	w.recordOK(a, "CreateSticker", serverID, st.ID, reason)
	w.emit(discord.StickersUpdateEvent{ServerID: serverID, Stickers: sortedStickers(s)})
	return st, nil
}

func (w *World) updateSticker(a actor, serverID, stickerID string, p discord.StickerParams, reason string) (discord.Sticker, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Sticker, error) {
		return discord.Sticker{}, w.record(a, "UpdateSticker", serverID, stickerID, reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageExpressions)
	}
	if err != nil {
		return fail(err)
	}
	st := s.stickers[stickerID]
	if st == nil {
		return fail(notFound("unknown sticker %s", stickerID))
	}
	if err := validateStickerText(p); err != nil {
		return fail(err)
	}
	st.Name, st.Description, st.Tags = p.Name, p.Description, p.Tags
	w.recordOK(a, "UpdateSticker", serverID, stickerID, reason)
	w.emit(discord.StickersUpdateEvent{ServerID: serverID, Stickers: sortedStickers(s)})
	return *st, nil
}

func (w *World) deleteSticker(a actor, serverID, stickerID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageExpressions)
	}
	if err == nil && s.stickers[stickerID] == nil {
		err = notFound("unknown sticker %s", stickerID)
	}
	if err != nil {
		return w.record(a, "DeleteSticker", serverID, stickerID, reason, err)
	}
	delete(s.stickers, stickerID)
	w.recordOK(a, "DeleteSticker", serverID, stickerID, reason)
	w.emit(discord.StickersUpdateEvent{ServerID: serverID, Stickers: sortedStickers(s)})
	return nil
}

func (w *World) listSounds(a actor, serverID string) ([]discord.SoundboardSound, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if s.members[a.userID] == nil {
		return nil, forbidden("missing access to server %s", serverID)
	}
	return sortedSounds(s), nil
}

func sortedSounds(s *serverState) []discord.SoundboardSound {
	out := make([]discord.SoundboardSound, 0, len(s.sounds))
	for _, x := range s.sounds {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool { return snowLess(out[i].ID, out[j].ID) })
	return out
}

func validateSoundText(p discord.SoundParams) error {
	if n := utf8.RuneCountInString(p.Name); n < 2 || n > 32 {
		return badRequest("sound name must be 2-32 characters")
	}
	if p.Volume < 0 || p.Volume > 1 {
		return badRequest("sound volume must be between 0 and 1")
	}
	return nil
}

func (w *World) createSound(a actor, serverID string, p discord.SoundParams, reason string) (discord.SoundboardSound, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.SoundboardSound, error) {
		return discord.SoundboardSound{}, w.record(a, "CreateSoundboardSound", serverID, "", reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireCreateExpr(a, s)
	}
	if err == nil {
		err = validateSoundText(p)
	}
	if err != nil {
		return fail(err)
	}
	if len(p.Sound) == 0 {
		return fail(badRequest("sound file is required"))
	}
	if len(p.Sound) > discord.MaxSoundFileBytes {
		return fail(badRequest("sound file must be at most 512 KiB"))
	}
	if soundFormat(p.Sound) == "" {
		return fail(badRequest("sound file must be mp3 or ogg"))
	}
	if limit := discord.SoundboardLimit(s.info.PremiumTier); len(s.sounds) >= limit {
		return fail(badRequest("maximum number of soundboard sounds reached (%d)", limit))
	}
	x := discord.SoundboardSound{ID: w.newID(), Name: p.Name, Volume: p.Volume, Emoji: p.Emoji}
	s.sounds[x.ID] = &x
	w.cdnPut(x.FileURL(), p.Sound)
	w.recordOK(a, "CreateSoundboardSound", serverID, x.ID, reason)
	w.emit(discord.SoundCreateEvent{ServerID: serverID, Sound: x})
	return x, nil
}

func (w *World) updateSound(a actor, serverID, soundID string, p discord.SoundParams, reason string) (discord.SoundboardSound, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.SoundboardSound, error) {
		return discord.SoundboardSound{}, w.record(a, "UpdateSoundboardSound", serverID, soundID, reason, err)
	}
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageExpressions)
	}
	if err != nil {
		return fail(err)
	}
	x := s.sounds[soundID]
	if x == nil {
		return fail(notFound("unknown soundboard sound %s", soundID))
	}
	if err := validateSoundText(p); err != nil {
		return fail(err)
	}
	x.Name, x.Volume, x.Emoji = p.Name, p.Volume, p.Emoji
	w.recordOK(a, "UpdateSoundboardSound", serverID, soundID, reason)
	w.emit(discord.SoundUpdateEvent{ServerID: serverID, Sound: *x})
	return *x, nil
}

func (w *World) deleteSound(a actor, serverID, soundID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageExpressions)
	}
	if err == nil && s.sounds[soundID] == nil {
		err = notFound("unknown soundboard sound %s", soundID)
	}
	if err != nil {
		return w.record(a, "DeleteSoundboardSound", serverID, soundID, reason, err)
	}
	delete(s.sounds, soundID)
	w.recordOK(a, "DeleteSoundboardSound", serverID, soundID, reason)
	w.emit(discord.SoundDeleteEvent{ServerID: serverID, SoundID: soundID})
	return nil
}

// ───────────────────────────────────────────────────────────── reference cleanup

// forgetChannel drops a deleted channel from AutoMod exemptions, onboarding and the
// welcome screen. Caller holds w.mu.
func (w *World) forgetChannel(s *serverState, id string) {
	for _, r := range s.automod {
		r.ExemptChannels = removeString(r.ExemptChannels, id)
	}
	s.onboarding.DefaultChannelIDs = removeString(s.onboarding.DefaultChannelIDs, id)
	for i := range s.onboarding.Prompts {
		for j := range s.onboarding.Prompts[i].Options {
			op := &s.onboarding.Prompts[i].Options[j]
			op.ChannelIDs = removeString(op.ChannelIDs, id)
		}
	}
	kept := s.welcome.Channels[:0:0]
	for _, wc := range s.welcome.Channels {
		if wc.ChannelID != id {
			kept = append(kept, wc)
		}
	}
	s.welcome.Channels = kept
}

// forgetRole drops a deleted role from AutoMod exemptions, emoji restrictions and
// onboarding options. Caller holds w.mu.
func (w *World) forgetRole(s *serverState, id string) {
	for _, r := range s.automod {
		r.ExemptRoles = removeString(r.ExemptRoles, id)
	}
	for _, e := range s.emojis {
		e.RoleIDs = removeString(e.RoleIDs, id)
	}
	for i := range s.onboarding.Prompts {
		for j := range s.onboarding.Prompts[i].Options {
			op := &s.onboarding.Prompts[i].Options[j]
			op.RoleIDs = removeString(op.RoleIDs, id)
		}
	}
}

// ───────────────────────────────────────────────────────────── clones

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}

func cloneAutoMod(r discord.AutoModRule) discord.AutoModRule {
	r.Keywords, r.Regex, r.Presets, r.AllowList = cloneStrings(r.Keywords), cloneStrings(r.Regex), cloneStrings(r.Presets), cloneStrings(r.AllowList)
	r.ExemptRoles, r.ExemptChannels = cloneStrings(r.ExemptRoles), cloneStrings(r.ExemptChannels)
	if r.Actions != nil {
		r.Actions = append([]discord.AutoModAction{}, r.Actions...)
	}
	return r
}

func cloneOnboarding(o discord.Onboarding) discord.Onboarding {
	o.DefaultChannelIDs = append([]string{}, o.DefaultChannelIDs...)
	ps := make([]discord.OnboardingPrompt, len(o.Prompts))
	for i, p := range o.Prompts {
		ops := make([]discord.OnboardingOption, len(p.Options))
		for j, op := range p.Options {
			op.RoleIDs, op.ChannelIDs = append([]string{}, op.RoleIDs...), append([]string{}, op.ChannelIDs...)
			ops[j] = op
		}
		p.Options = ops
		ps[i] = p
	}
	o.Prompts = ps
	return o
}

func cloneWelcome(ws discord.WelcomeScreen) discord.WelcomeScreen {
	ws.Channels = append([]discord.WelcomeChannel{}, ws.Channels...)
	return ws
}

func cloneEmoji(e discord.Emoji) discord.Emoji {
	e.RoleIDs = append([]string{}, e.RoleIDs...)
	return e
}

// ───────────────────────────────────────────────────────────── inspection

// inspect runs fn on the server under w.mu; an unknown server is a no-op.
func (w *World) inspect(serverID string, fn func(s *serverState)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s := w.servers[serverID]; s != nil {
		fn(s)
	}
}

// Widget returns the widget settings of a server.
func (w *World) Widget(serverID string) (out discord.ServerWidget) {
	w.inspect(serverID, func(s *serverState) { out = s.widget })
	return out
}

// AutoModRules returns the AutoMod rules of a server (ID order).
func (w *World) AutoModRules(serverID string) (out []discord.AutoModRule) {
	w.inspect(serverID, func(s *serverState) { out = w.sortedAutoMod(s) })
	return out
}

// Onboarding returns the native onboarding of a server.
func (w *World) Onboarding(serverID string) (out discord.Onboarding) {
	w.inspect(serverID, func(s *serverState) { out = cloneOnboarding(s.onboarding) })
	return out
}

// WelcomeScreen returns the welcome screen of a server.
func (w *World) WelcomeScreen(serverID string) (out discord.WelcomeScreen) {
	w.inspect(serverID, func(s *serverState) { out = cloneWelcome(s.welcome) })
	return out
}

// Emojis returns the custom emojis of a server (ID order).
func (w *World) Emojis(serverID string) (out []discord.Emoji) {
	w.inspect(serverID, func(s *serverState) { out = sortedEmojis(s) })
	return out
}

// Stickers returns the custom stickers of a server (ID order).
func (w *World) Stickers(serverID string) (out []discord.Sticker) {
	w.inspect(serverID, func(s *serverState) { out = sortedStickers(s) })
	return out
}

// SoundboardSounds returns the soundboard sounds of a server (ID order).
func (w *World) SoundboardSounds(serverID string) (out []discord.SoundboardSound) {
	w.inspect(serverID, func(s *serverState) { out = sortedSounds(s) })
	return out
}

// ───────────────────────────────────────────────────────────── client methods

func (c *Client) GetServerWidget(ctx context.Context, serverID string) (discord.ServerWidget, error) {
	if err := ctx.Err(); err != nil {
		return discord.ServerWidget{}, err
	}
	return c.w.getWidget(c.a, serverID)
}

func (c *Client) UpdateServerWidget(ctx context.Context, serverID string, ws discord.ServerWidget, reason string) (discord.ServerWidget, error) {
	if err := ctx.Err(); err != nil {
		return discord.ServerWidget{}, err
	}
	return c.w.updateWidget(c.a, serverID, ws, reason)
}

func (c *Client) ListAutoModRules(ctx context.Context, serverID string) ([]discord.AutoModRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listAutoMod(c.a, serverID)
}

func (c *Client) CreateAutoModRule(ctx context.Context, serverID string, r discord.AutoModRule, reason string) (discord.AutoModRule, error) {
	if err := ctx.Err(); err != nil {
		return discord.AutoModRule{}, err
	}
	return c.w.createAutoMod(c.a, serverID, r, reason)
}

func (c *Client) UpdateAutoModRule(ctx context.Context, serverID, ruleID string, r discord.AutoModRule, reason string) (discord.AutoModRule, error) {
	if err := ctx.Err(); err != nil {
		return discord.AutoModRule{}, err
	}
	return c.w.updateAutoMod(c.a, serverID, ruleID, r, reason)
}

func (c *Client) DeleteAutoModRule(ctx context.Context, serverID, ruleID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteAutoMod(c.a, serverID, ruleID, reason)
}

func (c *Client) GetOnboarding(ctx context.Context, serverID string) (discord.Onboarding, error) {
	if err := ctx.Err(); err != nil {
		return discord.Onboarding{}, err
	}
	return c.w.getOnboarding(c.a, serverID)
}

func (c *Client) UpdateOnboarding(ctx context.Context, serverID string, o discord.Onboarding, reason string) (discord.Onboarding, error) {
	if err := ctx.Err(); err != nil {
		return discord.Onboarding{}, err
	}
	return c.w.updateOnboarding(c.a, serverID, o, reason)
}

func (c *Client) GetWelcomeScreen(ctx context.Context, serverID string) (discord.WelcomeScreen, error) {
	if err := ctx.Err(); err != nil {
		return discord.WelcomeScreen{}, err
	}
	return c.w.getWelcome(c.a, serverID)
}

func (c *Client) UpdateWelcomeScreen(ctx context.Context, serverID string, ws discord.WelcomeScreen, reason string) (discord.WelcomeScreen, error) {
	if err := ctx.Err(); err != nil {
		return discord.WelcomeScreen{}, err
	}
	return c.w.updateWelcome(c.a, serverID, ws, reason)
}

func (c *Client) ListEmojis(ctx context.Context, serverID string) ([]discord.Emoji, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listEmojis(c.a, serverID)
}

func (c *Client) CreateEmoji(ctx context.Context, serverID string, p discord.EmojiParams, reason string) (discord.Emoji, error) {
	if err := ctx.Err(); err != nil {
		return discord.Emoji{}, err
	}
	return c.w.createEmoji(c.a, serverID, p, reason)
}

func (c *Client) UpdateEmoji(ctx context.Context, serverID, emojiID string, p discord.EmojiParams, reason string) (discord.Emoji, error) {
	if err := ctx.Err(); err != nil {
		return discord.Emoji{}, err
	}
	return c.w.updateEmoji(c.a, serverID, emojiID, p, reason)
}

func (c *Client) DeleteEmoji(ctx context.Context, serverID, emojiID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteEmoji(c.a, serverID, emojiID, reason)
}

func (c *Client) ListStickers(ctx context.Context, serverID string) ([]discord.Sticker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listStickers(c.a, serverID)
}

func (c *Client) CreateSticker(ctx context.Context, serverID string, p discord.StickerParams, reason string) (discord.Sticker, error) {
	if err := ctx.Err(); err != nil {
		return discord.Sticker{}, err
	}
	return c.w.createSticker(c.a, serverID, p, reason)
}

func (c *Client) UpdateSticker(ctx context.Context, serverID, stickerID string, p discord.StickerParams, reason string) (discord.Sticker, error) {
	if err := ctx.Err(); err != nil {
		return discord.Sticker{}, err
	}
	return c.w.updateSticker(c.a, serverID, stickerID, p, reason)
}

func (c *Client) DeleteSticker(ctx context.Context, serverID, stickerID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteSticker(c.a, serverID, stickerID, reason)
}

func (c *Client) ListSoundboardSounds(ctx context.Context, serverID string) ([]discord.SoundboardSound, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listSounds(c.a, serverID)
}

func (c *Client) CreateSoundboardSound(ctx context.Context, serverID string, p discord.SoundParams, reason string) (discord.SoundboardSound, error) {
	if err := ctx.Err(); err != nil {
		return discord.SoundboardSound{}, err
	}
	return c.w.createSound(c.a, serverID, p, reason)
}

func (c *Client) UpdateSoundboardSound(ctx context.Context, serverID, soundID string, p discord.SoundParams, reason string) (discord.SoundboardSound, error) {
	if err := ctx.Err(); err != nil {
		return discord.SoundboardSound{}, err
	}
	return c.w.updateSound(c.a, serverID, soundID, p, reason)
}

func (c *Client) DeleteSoundboardSound(ctx context.Context, serverID, soundID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteSound(c.a, serverID, soundID, reason)
}

// featureList parses a comma-separated feature list ("COMMUNITY, PARTNERED").
func featureList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, strings.ToUpper(f))
		}
	}
	return out
}

// ParseFeatureList parses the sim.cicord.dev/features annotation value.
func ParseFeatureList(s string) []string { return featureList(s) }
