package reconcile

// Discord parity (docs/discord-parity.md): boost/community gating, server engagement and
// safety settings, role styles, channel extras, the widget, welcome screen, native
// onboarding, AutoMod rules and expressions (emojis, stickers, soundboard sounds).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/state"
)

// allKinds are the resource map kinds the reconciler manages.
var allKinds = []state.ResourceKind{state.KindRole, state.KindCategory, state.KindChannel,
	state.KindAutoMod, state.KindEmoji, state.KindSticker, state.KindSound}

// Blocked reason prefixes (ExplainBlocked).
const (
	prefixBoost     = "boost: "
	prefixCommunity = "community: "
	prefixLimit     = "limit: "
	prefixRecreate  = "recreate: "
	// prefixUndeletable: Discord refuses the deletion whatever the permissions.
	prefixUndeletable = "undeletable: "
)

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func isGIF(b []byte) bool { return bytes.HasPrefix(b, []byte("GIF8")) }

// ───────────────────────────────────────────────────────────── community

func needsCommunity(t discord.ChannelType) bool {
	return t == discord.ChannelAnnouncement || t == discord.ChannelStage
}

// rulesChannelKey is community.rulesChannel, else the deprecated profile.rulesChannel.
func (p *planner) rulesChannelKey() string {
	if c := p.sp.Community; c != nil && c.RulesChannel != "" {
		return c.RulesChannel
	}
	if p.sp.Profile != nil {
		return p.sp.Profile.RulesChannel
	}
	return ""
}

// planCommunityState decides whether Community is on after the plan. Enabling it is
// blocked (with every missing prerequisite) unless the server will have rules and
// updates channels, a verification level of at least low and the explicit content
// filter on all members.
func (p *planner) planCommunityState() {
	live := p.in.Live.Server
	p.communityLive = live.HasFeature(discord.FeatureCommunity)
	p.communityAfter = p.communityLive
	c := p.sp.Community
	if c == nil || c.Enabled == p.communityLive {
		return
	}
	if !c.Enabled {
		p.communityAfter, p.communityMoves = false, true
		return
	}
	pr := p.sp.Profile
	if pr == nil {
		pr = &config.ServerProfile{}
	}
	var missing []string
	chanOK := func(key string) bool {
		h := p.how[state.KindChannel][key]
		return key != "" && h != "" && h != "skip"
	}
	if !chanOK(p.rulesChannelKey()) {
		missing = append(missing, "a rules channel (community.rulesChannel)")
	}
	if !chanOK(c.UpdatesChannel) {
		missing = append(missing, "a community updates channel (community.updatesChannel)")
	}
	ver := orDefault(pr.VerificationLevel, live.VerificationLevel)
	if ver == "" || ver == "none" {
		missing = append(missing, "profile.verificationLevel low or higher (is "+orDefault(ver, "none")+")")
	}
	if f := orDefault(pr.ExplicitContentFilter, live.ExplicitContentFilter); f != "allMembers" {
		missing = append(missing, "profile.explicitContentFilter: allMembers (is "+orDefault(f, "disabled")+")")
	}
	if len(missing) > 0 {
		p.blockFields(ActionUpdate, KindServer, p.serverKey, p.serverID,
			prefixCommunity+"enabling Community requires "+strings.Join(missing, ", "),
			map[string]FieldChange{"community": {From: false, To: true}})
		return
	}
	p.communityAfter, p.communityMoves = true, true
}

// ───────────────────────────────────────────────────────────── server

// serverImage plans a boost-gated server image (banner, splash).
func (p *planner) serverImage(fields map[string]FieldChange, name, path, liveURL, feature string, dst *[]byte, dstHash *string) {
	if path == "" {
		return
	}
	data := p.in.Files[name]
	if data == nil {
		return
	}
	h := hashOf(data)
	st := p.in.FileState[name]
	if liveURL != "" && st.Hash == h && st.URL == liveURL {
		return
	}
	fc := FieldChange{From: nilIfEmpty(liveURL), To: path}
	what := name
	if name == "splash" {
		what = "invite splash"
	}
	if name == "banner" && isGIF(data) {
		what, feature = "animated banner", discord.FeatureAnimatedBanner
	}
	if r := p.in.Live.Server.BoostReason(what, feature); r != "" {
		p.blockFields(ActionUpdate, KindServer, p.serverKey, p.serverID, prefixBoost+r, map[string]FieldChange{name: fc})
		return
	}
	fields[name] = fc
	*dst, *dstHash = data, h
}

var systemFlagBits = []struct {
	name string
	bit  discord.SystemChannelFlags
	get  func(config.SystemChannelFlags) *bool
}{
	{"welcomeMessages", discord.SuppressJoinNotifications, func(f config.SystemChannelFlags) *bool { return f.WelcomeMessages }},
	{"boostMessages", discord.SuppressPremiumSubscriptions, func(f config.SystemChannelFlags) *bool { return f.BoostMessages }},
	{"setupTips", discord.SuppressGuildReminderNotifications, func(f config.SystemChannelFlags) *bool { return f.SetupTips }},
	{"welcomeStickerReplies", discord.SuppressJoinNotificationReplies, func(f config.SystemChannelFlags) *bool { return f.WelcomeStickerReplies }},
	{"roleSubscriptionMessages", discord.SuppressRoleSubscriptionPurchaseNotifications, func(f config.SystemChannelFlags) *bool { return f.RoleSubscriptionMessages }},
	{"roleSubscriptionStickerReplies", discord.SuppressRoleSubscriptionPurchaseNotificationReply, func(f config.SystemChannelFlags) *bool { return f.RoleSubscriptionStickerReplies }},
}

// applySystemFlags applies the declared toggles (true = shown = bit cleared) on the live
// bits; undeclared toggles keep their live value.
func applySystemFlags(live discord.SystemChannelFlags, f config.SystemChannelFlags) discord.SystemChannelFlags {
	out := live
	for _, b := range systemFlagBits {
		if v := b.get(f); v != nil {
			if *v {
				out &^= b.bit
			} else {
				out |= b.bit
			}
		}
	}
	return out
}

// SystemFlagsDisplay lists the system messages that are shown.
func systemFlagsDisplay(f discord.SystemChannelFlags) map[string]bool {
	m := map[string]bool{}
	for _, b := range systemFlagBits {
		m[b.name] = f&b.bit == 0
	}
	return m
}

// SystemFlagsFromLive converts live bits to the YAML toggles (cicord import).
func SystemFlagsFromLive(f discord.SystemChannelFlags) config.SystemChannelFlags {
	shown := func(bit discord.SystemChannelFlags) *bool { v := f&bit == 0; return &v }
	return config.SystemChannelFlags{
		WelcomeMessages:                shown(discord.SuppressJoinNotifications),
		BoostMessages:                  shown(discord.SuppressPremiumSubscriptions),
		SetupTips:                      shown(discord.SuppressGuildReminderNotifications),
		WelcomeStickerReplies:          shown(discord.SuppressJoinNotificationReplies),
		RoleSubscriptionMessages:       shown(discord.SuppressRoleSubscriptionPurchaseNotifications),
		RoleSubscriptionStickerReplies: shown(discord.SuppressRoleSubscriptionPurchaseNotificationReply),
	}
}

// ───────────────────────────────────────────────────────────── roles

func colorsDisplay(c discord.RoleColors) map[string]string {
	m := map[string]string{"primary": colorString(c.Primary)}
	if c.Secondary != 0 {
		m["secondary"] = colorString(c.Secondary)
	}
	if c.Tertiary != 0 {
		m["tertiary"] = colorString(c.Tertiary)
	}
	return m
}

// roleStyle reads the style fields of a role spec into w.
func (p *planner) roleStyle(key string, spec config.RoleSpec, w *roleWant) error {
	if c := spec.Colors; c != nil {
		var rc discord.RoleColors
		var err error
		if rc.Primary, err = parseColor(c.Primary); err != nil {
			return fmt.Errorf("roles.%s.colors.primary: %w", key, err)
		}
		if rc.Secondary, err = parseColor(c.Secondary); err != nil {
			return fmt.Errorf("roles.%s.colors.secondary: %w", key, err)
		}
		if rc.Tertiary, err = parseColor(c.Tertiary); err != nil {
			return fmt.Errorf("roles.%s.colors.tertiary: %w", key, err)
		}
		w.colors = &rc
		w.color = rc.Primary
	}
	if spec.Icon != "" {
		w.manageIcon, w.iconFile = true, spec.Icon
		if data := p.in.Files["role:"+key]; data != nil {
			w.icon, w.iconHash, w.iconState = data, hashOf(data), p.in.FileState["role:"+key]
		}
	}
	if spec.Emoji != "" {
		w.manageIcon, w.emoji = true, spec.Emoji
	}
	return nil
}

// roleExtras adds the style differences (gradient colors, icon / emoji) to fields and rp,
// blocking the boost-gated ones the server cannot use.
func (p *planner) roleExtras(key string, w roleWant, live discord.Role, fields map[string]FieldChange, rp *discord.RoleParams, o *op, create bool) {
	srv := p.in.Live.Server
	liveC := live.Colors
	if liveC.Primary == 0 {
		liveC.Primary = live.Color
	}
	id := live.ID
	if w.colors != nil && (create && (w.colors.Secondary != 0 || w.colors.Tertiary != 0) || !create && *w.colors != liveC) {
		gradient := w.colors.Secondary != 0 || w.colors.Tertiary != 0
		fc := FieldChange{To: colorsDisplay(*w.colors)}
		if !create {
			fc.From = colorsDisplay(liveC)
		}
		if gradient && !srv.HasBoostFeature(discord.FeatureEnhancedRoleColors) {
			p.blockFields(ActionUpdate, KindRole, key, id, prefixBoost+srv.BoostReason("a gradient role color", discord.FeatureEnhancedRoleColors),
				map[string]FieldChange{"colors": fc})
			// keep the primary color in sync meanwhile
			if !create && live.Color != w.colors.Primary {
				fields["color"] = FieldChange{From: colorString(live.Color), To: colorString(w.colors.Primary)}
				c := w.colors.Primary
				rp.Color = &c
			}
		} else {
			fields["colors"] = fc
			c := *w.colors
			rp.Colors = &c
		}
	}
	if w.colors == nil && !create && (live.Colors.Secondary != 0 || live.Colors.Tertiary != 0) {
		fields["colors"] = FieldChange{From: colorsDisplay(live.Colors), To: nil}
		rp.Colors = &discord.RoleColors{Primary: w.color}
	}
	if !w.manageIcon {
		return
	}
	ifields := map[string]FieldChange{}
	var icon *[]byte
	var emoji *string
	switch {
	case w.emoji != "":
		if live.UnicodeEmoji != w.emoji || live.IconURL != "" {
			ifields["emoji"] = FieldChange{From: nilIfEmpty(live.UnicodeEmoji), To: w.emoji}
			e := w.emoji
			emoji = &e
			if live.IconURL != "" {
				empty := []byte{}
				icon = &empty
			}
		}
	case w.icon != nil:
		if live.IconURL == "" || w.iconState.Hash != w.iconHash || w.iconState.URL != live.IconURL || live.UnicodeEmoji != "" {
			ifields["icon"] = FieldChange{From: nilIfEmpty(live.IconURL), To: w.iconFile}
			data := w.icon
			icon = &data
			if live.UnicodeEmoji != "" {
				e := ""
				emoji = &e
			}
		}
	}
	if len(ifields) == 0 {
		return
	}
	if r := srv.BoostReason("a role icon", discord.FeatureRoleIcons); r != "" {
		p.blockFields(ActionUpdate, KindRole, key, id, prefixBoost+r, ifields)
		return
	}
	for k, v := range ifields {
		fields[k] = v
	}
	rp.Icon, rp.UnicodeEmoji = icon, emoji
	if icon != nil && len(*icon) > 0 {
		o.roleIcon, o.roleIconHash = *icon, w.iconHash
	}
}

// ───────────────────────────────────────────────────────────── channels

var archiveMinutes = map[string]int{"1h": 60, "24h": 1440, "3d": 4320, "1w": 10080}

// ArchiveName converts minutes to the YAML value ("" when not one of Discord's).
func ArchiveName(minutes int) string {
	for k, v := range archiveMinutes {
		if v == minutes {
			return k
		}
	}
	return ""
}

func isThreadParent(t discord.ChannelType) bool {
	switch t {
	case discord.ChannelText, discord.ChannelAnnouncement, discord.ChannelForum, discord.ChannelMedia:
		return true
	}
	return false
}

func isForumLike(t discord.ChannelType) bool {
	return t == discord.ChannelForum || t == discord.ChannelMedia
}

// channelExtras reads the parity fields of a channel spec (managed only when set and
// when they apply to the channel type).
func channelExtras(key string, c config.Channel, cs *chanSpec) error {
	t := cs.typ
	if isVoice(t) {
		if c.VoiceRegion != "" {
			r := c.VoiceRegion
			if r == "automatic" {
				r = ""
			}
			cs.rtcRegion = &r
		}
		if c.VideoQuality != "" {
			v := c.VideoQuality
			cs.videoQuality = &v
		}
	}
	if isThreadParent(t) {
		if c.ThreadArchiveAfter != "" {
			m, ok := archiveMinutes[c.ThreadArchiveAfter]
			if !ok {
				return fmt.Errorf("channels.%s.threadArchiveAfter: invalid value %q (1h, 24h, 3d or 1w)", key, c.ThreadArchiveAfter)
			}
			cs.autoArchive = &m
		}
		if c.ThreadSlowmode != nil {
			v := *c.ThreadSlowmode
			cs.threadSlowmode = &v
		}
	}
	if isForumLike(t) {
		if c.Tags != nil {
			tags := make([]discord.ForumTag, 0, len(c.Tags))
			for _, k := range sortedKeys(c.Tags) {
				tg := c.Tags[k]
				tags = append(tags, discord.ForumTag{Name: tg.Name, Emoji: tg.Emoji, Moderated: tg.Moderated})
			}
			cs.tags = &tags
		}
		if c.DefaultReaction != "" {
			v := c.DefaultReaction
			cs.defaultReaction = &v
		}
		if c.SortOrder != "" {
			v := c.SortOrder
			cs.sortOrder = &v
		}
		if t == discord.ChannelForum && c.Layout != "" {
			v := c.Layout
			cs.layout = &v
		}
	}
	return nil
}

func createExtrasFields(cs *chanSpec, fields map[string]FieldChange) {
	if cs.rtcRegion != nil {
		fields["voiceRegion"] = FieldChange{To: regionDisplay(*cs.rtcRegion)}
	}
	if cs.videoQuality != nil {
		fields["videoQuality"] = FieldChange{To: *cs.videoQuality}
	}
	if cs.autoArchive != nil {
		fields["threadArchiveAfter"] = FieldChange{To: ArchiveName(*cs.autoArchive)}
	}
	if cs.threadSlowmode != nil && *cs.threadSlowmode != 0 {
		fields["threadSlowmode"] = FieldChange{To: *cs.threadSlowmode}
	}
	if cs.tags != nil && len(*cs.tags) > 0 {
		fields["tags"] = FieldChange{To: tagsDisplay(*cs.tags)}
	}
	if cs.defaultReaction != nil {
		fields["defaultReaction"] = FieldChange{To: *cs.defaultReaction}
	}
	if cs.sortOrder != nil {
		fields["sortOrder"] = FieldChange{To: *cs.sortOrder}
	}
	if cs.layout != nil {
		fields["layout"] = FieldChange{To: *cs.layout}
	}
}

func regionDisplay(r string) string {
	if r == "" {
		return "automatic"
	}
	return r
}

func tagsDisplay(tags []discord.ForumTag) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		s := t.Name
		if t.Emoji != "" {
			s = t.Emoji + " " + s
		}
		if t.Moderated {
			s += " (moderated)"
		}
		out = append(out, s)
	}
	return out
}

// normEmoji compares emojis: a custom emoji ("name:id", "<:name:id>", "<a:name:id>")
// compares by its ID (Discord returns custom forum emojis by ID only).
func normEmoji(s string) string {
	s = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(s), "<"), ">")
	if i := strings.LastIndexByte(s, ':'); i >= 0 && isID(s[i+1:]) {
		return s[i+1:]
	}
	return s
}

func isID(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// sameTags compares tags as a set matched by name (YAML maps have no order, so the
// display order of the tags is left as it is in Discord).
func sameTags(want, live []discord.ForumTag) bool {
	if len(want) != len(live) {
		return false
	}
	byName := map[string]discord.ForumTag{}
	for _, l := range live {
		byName[l.Name] = l
	}
	for _, w := range want {
		l, ok := byName[w.Name]
		if !ok || w.Moderated != l.Moderated || normEmoji(w.Emoji) != normEmoji(l.Emoji) {
			return false
		}
	}
	return true
}

// assignTagIDs keeps live tag IDs: by name first, then the remaining live tags in order
// (a renamed tag stays the same tag, so posts keep it).
func assignTagIDs(want, live []discord.ForumTag) []discord.ForumTag {
	out := append([]discord.ForumTag(nil), want...)
	used := map[string]bool{}
	for i := range out {
		for _, l := range live {
			if !used[l.ID] && l.Name == out[i].Name {
				out[i].ID, used[l.ID] = l.ID, true
				break
			}
		}
	}
	j := 0
	for i := range out {
		if out[i].ID != "" {
			continue
		}
		for j < len(live) && used[live[j].ID] {
			j++
		}
		if j < len(live) {
			out[i].ID, used[live[j].ID] = live[j].ID, true
		}
	}
	return out
}

func diffChannelExtras(cs *chanSpec, live discord.Channel, fields map[string]FieldChange, send map[string]bool) {
	if v := cs.rtcRegion; v != nil && *v != live.RTCRegion {
		fields["voiceRegion"] = FieldChange{From: regionDisplay(live.RTCRegion), To: regionDisplay(*v)}
		send["rtcRegion"] = true
	}
	if v := cs.videoQuality; v != nil && *v != orDefault(live.VideoQuality, "auto") {
		fields["videoQuality"] = FieldChange{From: orDefault(live.VideoQuality, "auto"), To: *v}
		send["videoQuality"] = true
	}
	if v := cs.autoArchive; v != nil && *v != live.DefaultAutoArchive {
		fields["threadArchiveAfter"] = FieldChange{From: nilIfEmpty(ArchiveName(live.DefaultAutoArchive)), To: ArchiveName(*v)}
		send["autoArchive"] = true
	}
	if v := cs.threadSlowmode; v != nil && *v != live.DefaultThreadSlowmode {
		fields["threadSlowmode"] = FieldChange{From: live.DefaultThreadSlowmode, To: *v}
		send["threadSlowmode"] = true
	}
	if v := cs.tags; v != nil && !sameTags(*v, live.Tags) {
		fields["tags"] = FieldChange{From: tagsDisplay(live.Tags), To: tagsDisplay(*v)}
		send["tags"] = true
		ids := assignTagIDs(*v, live.Tags)
		cs.tags = &ids
	}
	if v := cs.defaultReaction; v != nil && normEmoji(*v) != normEmoji(live.DefaultReaction) {
		fields["defaultReaction"] = FieldChange{From: nilIfEmpty(live.DefaultReaction), To: *v}
		send["defaultReaction"] = true
	}
	if v := cs.sortOrder; v != nil && *v != live.DefaultSortOrder {
		fields["sortOrder"] = FieldChange{From: nilIfEmpty(live.DefaultSortOrder), To: *v}
		send["sortOrder"] = true
	}
	if v := cs.layout; v != nil && *v != live.DefaultLayout {
		fields["layout"] = FieldChange{From: nilIfEmpty(live.DefaultLayout), To: *v}
		send["layout"] = true
	}
}

// ───────────────────────────────────────────────────────────── keyed parity kinds

// fetched reports whether the live state of a kind is known.
func (p *planner) fetched(rk state.ResourceKind) bool {
	switch rk {
	case state.KindAutoMod:
		return p.in.Live.HasAutoMod
	case state.KindEmoji:
		return p.in.Live.HasEmojis
	case state.KindSticker:
		return p.in.Live.HasStickers
	case state.KindSound:
		return p.in.Live.HasSounds
	}
	return true
}

func (p *planner) parityAlive(rk state.ResourceKind, id string) (bool, string) {
	switch rk {
	case state.KindAutoMod:
		for _, r := range p.in.Live.AutoMod {
			if r.ID == id {
				return true, "AutoMod rule " + quote(r.Name)
			}
		}
	case state.KindEmoji:
		for _, e := range p.in.Live.Emojis {
			if e.ID == id {
				return true, "emoji :" + e.Name + ":"
			}
		}
	case state.KindSticker:
		for _, s := range p.in.Live.Stickers {
			if s.ID == id {
				return true, "sticker " + quote(s.Name)
			}
		}
	case state.KindSound:
		for _, s := range p.in.Live.Sounds {
			if s.ID == id {
				return true, "sound " + quote(s.Name)
			}
		}
	}
	return false, ""
}

// resolveParity resolves AutoMod rules and expressions like roles: mapped, adopted by
// name (AutoMod: name and trigger), or created.
func (p *planner) resolveParity() {
	live := p.in.Live
	if live.HasAutoMod {
		for _, key := range sortedKeys(p.sp.AutoMod) {
			spec := p.sp.AutoMod[key]
			p.resolve(state.KindAutoMod, key, func(id string) bool {
				return slices.ContainsFunc(live.AutoMod, func(r discord.AutoModRule) bool { return r.ID == id })
			}, func() string {
				var cands []string
				for _, r := range live.AutoMod {
					if r.Name == spec.Name && r.Trigger == spec.Trigger {
						cands = append(cands, r.ID)
					}
				}
				if id := p.pick(cands); id != "" {
					return id
				}
				// Triggers allowed once per server (mentionSpam, spam…): the existing rule
				// is the one the key describes, whatever its name — Discord creates a
				// "Block Mention Spam" rule itself when Community is enabled.
				if discord.AutoModRuleLimit(spec.Trigger) != 1 {
					return ""
				}
				cands = cands[:0]
				for _, r := range live.AutoMod {
					if r.Trigger == spec.Trigger {
						cands = append(cands, r.ID)
					}
				}
				return p.pick(cands)
			})
		}
	}
	type named struct{ id, name string }
	resolveNamed := func(rk state.ResourceKind, keys []string, nameOf func(string) string, items []named) {
		for _, key := range keys {
			want := nameOf(key)
			p.resolve(rk, key, func(id string) bool {
				return slices.ContainsFunc(items, func(n named) bool { return n.id == id })
			}, func() string {
				var cands []string
				for _, n := range items {
					if n.name == want {
						cands = append(cands, n.id)
					}
				}
				return p.pick(cands)
			})
		}
	}
	if live.HasEmojis {
		var items []named
		for _, e := range live.Emojis {
			if !e.Managed {
				items = append(items, named{e.ID, e.Name})
			}
		}
		resolveNamed(state.KindEmoji, sortedKeys(p.sp.Emojis), func(k string) string { return p.sp.Emojis[k].Name }, items)
	}
	if live.HasStickers {
		var items []named
		for _, s := range live.Stickers {
			items = append(items, named{s.ID, s.Name})
		}
		resolveNamed(state.KindSticker, sortedKeys(p.sp.Stickers), func(k string) string { return p.sp.Stickers[k].Name }, items)
	}
	if live.HasSounds {
		var items []named
		for _, s := range live.Sounds {
			items = append(items, named{s.ID, s.Name})
		}
		resolveNamed(state.KindSound, sortedKeys(p.sp.Soundboard), func(k string) string { return p.sp.Soundboard[k].Name }, items)
	}
}

// keyIDs resolves keys to plan-time IDs; ok is false when one is created by the plan
// (or unknown), so the comparison is always a difference.
func (p *planner) keyIDs(rk state.ResourceKind, keys []string) ([]string, bool) {
	out := make([]string, 0, len(keys))
	ok := true
	for _, k := range keys {
		id, found := p.ids[rk][k]
		if !found {
			ok = false
			continue
		}
		out = append(out, id)
	}
	return out, ok
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// ───────────────────────────────────────────────────────────── widget

type widgetWant struct {
	enabled    bool
	channelKey string
}

func (p *planner) planWidget() {
	w := p.sp.Widget
	if w == nil || p.in.Live.Widget == nil {
		return
	}
	live := *p.in.Live.Widget
	fields := map[string]FieldChange{}
	if w.Enabled != live.Enabled {
		fields["enabled"] = FieldChange{From: live.Enabled, To: w.Enabled}
	}
	if w.Channel != "" {
		if id, ok := p.ids[state.KindChannel][w.Channel]; !ok || id != live.ChannelID {
			fields["channel"] = FieldChange{From: p.channelRefName(live.ChannelID), To: w.Channel}
		}
	} else if live.ChannelID != "" {
		fields["channel"] = FieldChange{From: p.channelRefName(live.ChannelID), To: nil}
	}
	if len(fields) == 0 {
		return
	}
	p.add(Change{Action: ActionUpdate, Kind: KindWidget, Key: KindWidget, ID: p.serverID,
		Summary: "update server widget: " + fieldList(fields), Fields: fields,
		op: &op{phase: phaseWidget, widget: &widgetWant{enabled: w.Enabled, channelKey: w.Channel}}})
}

// ───────────────────────────────────────────────────────────── welcome screen

type welcomeWant struct {
	enabled     bool
	description string
	channels    []config.WelcomeChannel
}

func (p *planner) planWelcomeScreen() {
	w := p.sp.WelcomeScreen
	if w == nil || p.in.Live.WelcomeScreen == nil {
		return
	}
	live := *p.in.Live.WelcomeScreen
	fields := map[string]FieldChange{}
	if w.IsEnabled() != live.Enabled {
		fields["enabled"] = FieldChange{From: live.Enabled, To: w.IsEnabled()}
	}
	if w.Description != live.Description {
		fields["description"] = FieldChange{From: live.Description, To: w.Description}
	}
	same := len(w.Channels) == len(live.Channels)
	for i := 0; same && i < len(w.Channels); i++ {
		id, ok := p.ids[state.KindChannel][w.Channels[i].Channel]
		l := live.Channels[i]
		same = ok && id == l.ChannelID && w.Channels[i].Description == l.Description && normEmoji(w.Channels[i].Emoji) == normEmoji(l.Emoji)
	}
	if !same {
		from := []string{}
		for _, c := range live.Channels {
			from = append(from, fmt.Sprint(p.channelRefName(c.ChannelID)))
		}
		to := []string{}
		for _, c := range w.Channels {
			to = append(to, c.Channel)
		}
		fields["channels"] = FieldChange{From: from, To: to}
	}
	if len(fields) == 0 {
		return
	}
	if !p.communityAfter && (w.IsEnabled() || live.Enabled) {
		p.blockFields(ActionUpdate, KindWelcomeScreen, KindWelcomeScreen, p.serverID,
			prefixCommunity+"the welcome screen requires the Community feature (set community.enabled: true)", fields)
		return
	}
	p.add(Change{Action: ActionUpdate, Kind: KindWelcomeScreen, Key: KindWelcomeScreen, ID: p.serverID,
		Summary: "update welcome screen: " + fieldList(fields), Fields: fields,
		op: &op{phase: phaseWelcome, welcome: &welcomeWant{enabled: w.IsEnabled(), description: w.Description, channels: w.Channels}}})
}

// ───────────────────────────────────────────────────────────── onboarding

type onboardingWant struct {
	spec config.OnboardingSpec
}

func promptType(t string) string { return orDefault(t, "multipleChoice") }

func (p *planner) sameOnboarding(w config.OnboardingSpec, live discord.Onboarding) map[string]FieldChange {
	fields := map[string]FieldChange{}
	if w.Enabled != live.Enabled {
		fields["enabled"] = FieldChange{From: live.Enabled, To: w.Enabled}
	}
	if m := orDefault(w.Mode, "default"); m != orDefault(live.Mode, "default") {
		fields["mode"] = FieldChange{From: orDefault(live.Mode, "default"), To: m}
	}
	if ids, ok := p.keyIDs(state.KindChannel, w.DefaultChannels); !ok || !sameSet(ids, live.DefaultChannelIDs) {
		from := []string{}
		for _, id := range live.DefaultChannelIDs {
			from = append(from, fmt.Sprint(p.channelRefName(id)))
		}
		fields["defaultChannels"] = FieldChange{From: from, To: append([]string{}, w.DefaultChannels...)}
	}
	same := len(w.Prompts) == len(live.Prompts)
	for i := 0; same && i < len(w.Prompts); i++ {
		wp, lp := w.Prompts[i], live.Prompts[i]
		same = wp.Title == lp.Title && promptType(wp.Type) == promptType(lp.Type) && wp.SingleSelect == lp.SingleSelect &&
			wp.Required == lp.Required && wp.IsInOnboarding() == lp.InOnboarding && len(wp.Options) == len(lp.Options)
		for j := 0; same && j < len(wp.Options); j++ {
			wo, lo := wp.Options[j], lp.Options[j]
			roles, okR := p.keyIDs(state.KindRole, wo.Roles)
			chans, okC := p.keyIDs(state.KindChannel, wo.Channels)
			same = wo.Title == lo.Title && wo.Description == lo.Description && normEmoji(wo.Emoji) == normEmoji(lo.Emoji) &&
				okR && okC && sameSet(roles, lo.RoleIDs) && sameSet(chans, lo.ChannelIDs)
		}
	}
	if !same {
		from, to := []string{}, []string{}
		for _, lp := range live.Prompts {
			from = append(from, lp.Title)
		}
		for _, wp := range w.Prompts {
			to = append(to, wp.Title)
		}
		fields["prompts"] = FieldChange{From: from, To: to}
	}
	return fields
}

func (p *planner) planOnboarding() {
	w := p.sp.Onboarding
	if w == nil || p.in.Live.Onboarding == nil {
		return
	}
	fields := p.sameOnboarding(*w, *p.in.Live.Onboarding)
	if len(fields) == 0 {
		return
	}
	if w.Enabled && !p.communityAfter {
		p.blockFields(ActionUpdate, KindOnboarding, KindOnboarding, p.serverID,
			prefixCommunity+"Discord onboarding requires the Community feature (set community.enabled: true)", fields)
		return
	}
	p.add(Change{Action: ActionUpdate, Kind: KindOnboarding, Key: KindOnboarding, ID: p.serverID,
		Summary: "update onboarding: " + fieldList(fields), Fields: fields,
		op: &op{phase: phaseOnboarding, onboarding: &onboardingWant{spec: *w}}})
}

// ───────────────────────────────────────────────────────────── AutoMod

type automodWant struct {
	rule           discord.AutoModRule // IDs empty; resolved at apply time
	alertKey       string
	exemptRoles    []string
	exemptChannels []string
}

// ParseDuration parses an AutoMod timeout ("60s", "10m", "1h", "1d", "1w").
func ParseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	if unit == 0 {
		return 0, fmt.Errorf("invalid duration %q (unit s, m, h, d or w)", s)
	}
	return time.Duration(n) * unit, nil
}

// DurationName formats seconds as the largest exact unit ("1h", "10m", "90s").
func DurationName(sec int) string {
	for _, u := range []struct {
		s string
		n int
	}{{"w", 604800}, {"d", 86400}, {"h", 3600}, {"m", 60}} {
		if sec >= u.n && sec%u.n == 0 {
			return strconv.Itoa(sec/u.n) + u.s
		}
	}
	return strconv.Itoa(sec) + "s"
}

func automodFromSpec(key string, s config.AutoModSpec) (automodWant, error) {
	r := discord.AutoModRule{Name: s.Name, Trigger: s.Trigger, Keywords: s.Keywords, Regex: s.Regex, AllowList: s.AllowList,
		Presets: s.Presets, MentionLimit: s.MentionLimit, MentionRaidProtection: s.MentionRaidProtection, Enabled: s.IsEnabled()}
	w := automodWant{exemptRoles: s.ExemptRoles, exemptChannels: s.ExemptChannels, alertKey: s.Actions.Alert}
	if b := s.Actions.Block; b != nil {
		t := discord.AutoModActionBlock
		if s.Trigger == discord.AutoModMemberProfile {
			t = discord.AutoModActionBlockInteraction
		}
		r.Actions = append(r.Actions, discord.AutoModAction{Type: t, CustomMessage: b.Message})
	}
	if s.Actions.Alert != "" {
		r.Actions = append(r.Actions, discord.AutoModAction{Type: discord.AutoModActionAlert})
	}
	if s.Actions.Timeout != "" {
		d, err := ParseDuration(s.Actions.Timeout)
		if err != nil {
			return w, fmt.Errorf("automod.%s.actions.timeout: %w", key, err)
		}
		r.Actions = append(r.Actions, discord.AutoModAction{Type: discord.AutoModActionTimeout, DurationSeconds: int(d / time.Second)})
	}
	w.rule = r
	return w, nil
}

func actionsDisplay(as []discord.AutoModAction, chanName func(string) any) map[string]any {
	m := map[string]any{}
	for _, a := range as {
		switch a.Type {
		case discord.AutoModActionBlock, discord.AutoModActionBlockInteraction:
			m["block"] = map[string]any{"message": a.CustomMessage}
		case discord.AutoModActionAlert:
			m["alert"] = chanName(a.ChannelID)
		case discord.AutoModActionTimeout:
			m["timeout"] = DurationName(a.DurationSeconds)
		}
	}
	return m
}

// automodDiff compares a desired rule with a live one.
func (p *planner) automodDiff(w automodWant, live discord.AutoModRule) map[string]FieldChange {
	f := map[string]FieldChange{}
	r := w.rule
	if r.Name != live.Name {
		f["name"] = FieldChange{From: live.Name, To: r.Name}
	}
	if r.Enabled != live.Enabled {
		f["enabled"] = FieldChange{From: live.Enabled, To: r.Enabled}
	}
	list := func(name string, want, have []string) {
		if !sameSet(want, have) {
			f[name] = FieldChange{From: nonNil(have), To: nonNil(want)}
		}
	}
	list("keywords", r.Keywords, live.Keywords)
	list("regex", r.Regex, live.Regex)
	list("allowList", r.AllowList, live.AllowList)
	list("presets", r.Presets, live.Presets)
	if r.MentionLimit != live.MentionLimit {
		f["mentionLimit"] = FieldChange{From: live.MentionLimit, To: r.MentionLimit}
	}
	if r.MentionRaidProtection != live.MentionRaidProtection {
		f["mentionRaidProtection"] = FieldChange{From: live.MentionRaidProtection, To: r.MentionRaidProtection}
	}
	roles, okR := p.keyIDs(state.KindRole, w.exemptRoles)
	if !okR || !sameSet(roles, live.ExemptRoles) {
		f["exemptRoles"] = FieldChange{From: len(live.ExemptRoles), To: nonNil(w.exemptRoles)}
	}
	chans, okC := p.keyIDs(state.KindChannel, w.exemptChannels)
	if !okC || !sameSet(chans, live.ExemptChannels) {
		f["exemptChannels"] = FieldChange{From: len(live.ExemptChannels), To: nonNil(w.exemptChannels)}
	}
	// actions: compared by type
	alertID, okA := "", true
	if w.alertKey != "" {
		alertID, okA = p.ids[state.KindChannel][w.alertKey]
	}
	same := okA && len(r.Actions) == len(live.Actions)
	for _, a := range r.Actions {
		if !same {
			break
		}
		i := slices.IndexFunc(live.Actions, func(l discord.AutoModAction) bool { return l.Type == a.Type })
		if i < 0 {
			same = false
			break
		}
		l := live.Actions[i]
		switch a.Type {
		case discord.AutoModActionBlock, discord.AutoModActionBlockInteraction:
			same = a.CustomMessage == l.CustomMessage
		case discord.AutoModActionAlert:
			same = alertID == l.ChannelID
		case discord.AutoModActionTimeout:
			same = a.DurationSeconds == l.DurationSeconds
		}
	}
	if !same {
		want := actionsDisplay(r.Actions, func(string) any { return w.alertKey })
		f["actions"] = FieldChange{From: actionsDisplay(live.Actions, p.channelRefName), To: want}
	}
	return f
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (p *planner) planAutoMod() error {
	if !p.in.Live.HasAutoMod {
		return nil
	}
	byID := map[string]discord.AutoModRule{}
	count := map[string]int{} // live rules per trigger that this plan does not manage
	managed := map[string]bool{}
	for _, k := range sortedKeys(p.sp.AutoMod) {
		if id, ok := p.ids[state.KindAutoMod][k]; ok {
			managed[id] = true
		}
	}
	for _, r := range p.in.Live.AutoMod {
		byID[r.ID] = r
		if !managed[r.ID] {
			count[r.Trigger]++
		}
	}
	for _, key := range sortedKeys(p.sp.AutoMod) {
		spec := p.sp.AutoMod[key]
		w, err := automodFromSpec(key, spec)
		if err != nil {
			return err
		}
		switch p.how[state.KindAutoMod][key] {
		case "skip":
			continue
		case "create":
			limit := discord.AutoModRuleLimit(spec.Trigger)
			if count[spec.Trigger] >= limit {
				p.blockFields(ActionCreate, KindAutoMod, key, "", fmt.Sprintf("%sa server can have at most %d %s AutoMod rule(s); %d already exist in Discord",
					prefixLimit, limit, spec.Trigger, count[spec.Trigger]), map[string]FieldChange{"exists": {From: false, To: true}})
				continue
			}
			count[spec.Trigger]++
			p.add(Change{Action: ActionCreate, Kind: KindAutoMod, Key: key,
				Summary: fmt.Sprintf("create AutoMod rule %q (%s)", spec.Name, spec.Trigger),
				Fields: map[string]FieldChange{"name": {To: spec.Name}, "trigger": {To: spec.Trigger},
					"actions": {To: actionsDisplay(w.rule.Actions, func(string) any { return w.alertKey })}},
				op: &op{phase: phaseAutoMod, rk: state.KindAutoMod, automod: &w}})
		default:
			live := byID[p.ids[state.KindAutoMod][key]]
			if live.Trigger != spec.Trigger {
				p.blockFields(ActionUpdate, KindAutoMod, key, live.ID,
					fmt.Sprintf("type change: the trigger of an AutoMod rule cannot change (%s → %s): give the rule a new key", live.Trigger, spec.Trigger),
					map[string]FieldChange{"trigger": {From: live.Trigger, To: spec.Trigger}})
				continue
			}
			fields := p.automodDiff(w, live)
			adopt := p.how[state.KindAutoMod][key] == "adopt"
			if !adopt && len(fields) == 0 {
				continue
			}
			c := Change{Action: ActionUpdate, Kind: KindAutoMod, Key: key, ID: live.ID,
				op: &op{phase: phaseAutoMod, rk: state.KindAutoMod, automod: &w}}
			if len(fields) > 0 {
				c.Fields = fields
			}
			if adopt {
				c.Action = ActionAdopt
				c.Summary = fmt.Sprintf("adopt AutoMod rule %q (%s)", live.Name, live.ID)
				if len(fields) > 0 {
					c.Summary += ", update " + fieldList(fields)
				}
			} else {
				c.Summary = fmt.Sprintf("update AutoMod rule %q: %s", spec.Name, fieldList(fields))
			}
			p.add(c)
		}
	}
	return nil
}

// ───────────────────────────────────────────────────────────── expressions

type exprWant struct {
	file  string // logical file name ("emoji:<key>"…)
	data  []byte
	hash  string
	name  string
	roles []string // emoji role keys
	desc  string   // sticker
	tags  string   // sticker
	vol   float64  // sound
	emoji string   // sound
}

func (p *planner) planExpressions() error {
	srv := p.in.Live.Server
	if p.in.Live.HasEmojis {
		live := map[string]discord.Emoji{}
		static, animated := 0, 0
		for _, e := range p.in.Live.Emojis {
			live[e.ID] = e
			if e.Animated {
				animated++
			} else {
				static++
			}
		}
		for _, key := range sortedKeys(p.sp.Emojis) {
			spec := p.sp.Emojis[key]
			w := p.exprFile("emoji:" + key)
			w.name, w.roles = spec.Name, spec.Roles
			var cur *discord.Emoji
			if id, ok := p.ids[state.KindEmoji][key]; ok {
				e := live[id]
				cur = &e
			}
			fields := map[string]FieldChange{}
			if cur != nil {
				if cur.Name != spec.Name {
					fields["name"] = FieldChange{From: cur.Name, To: spec.Name}
				}
				if ids, ok := p.keyIDs(state.KindRole, spec.Roles); !ok || !sameSet(ids, cur.RoleIDs) {
					fields["roles"] = FieldChange{From: len(cur.RoleIDs), To: nonNil(spec.Roles)}
				}
			}
			n, limit := &static, discord.EmojiLimit(srv.PremiumTier)
			if isGIF(w.data) {
				n = &animated
			}
			p.planExpr(state.KindEmoji, KindEmoji, key, "emoji :"+spec.Name+":", spec.Image, w, cur != nil, fields, n, limit, "emojis")
		}
	}
	if p.in.Live.HasStickers {
		live := map[string]discord.Sticker{}
		for _, s := range p.in.Live.Stickers {
			live[s.ID] = s
		}
		n := len(p.in.Live.Stickers)
		for _, key := range sortedKeys(p.sp.Stickers) {
			spec := p.sp.Stickers[key]
			w := p.exprFile("sticker:" + key)
			w.name, w.desc, w.tags = spec.Name, spec.Description, spec.Emoji
			fields := map[string]FieldChange{}
			id, ok := p.ids[state.KindSticker][key]
			if ok {
				cur := live[id]
				if cur.Name != spec.Name {
					fields["name"] = FieldChange{From: cur.Name, To: spec.Name}
				}
				if cur.Description != spec.Description {
					fields["description"] = FieldChange{From: cur.Description, To: spec.Description}
				}
				if cur.Tags != spec.Emoji {
					fields["emoji"] = FieldChange{From: cur.Tags, To: spec.Emoji}
				}
			}
			p.planExpr(state.KindSticker, KindSticker, key, "sticker "+quote(spec.Name), spec.File, w, ok, fields, &n, discord.StickerLimit(srv.PremiumTier), "stickers")
		}
	}
	if p.in.Live.HasSounds {
		live := map[string]discord.SoundboardSound{}
		for _, s := range p.in.Live.Sounds {
			live[s.ID] = s
		}
		n := len(p.in.Live.Sounds)
		for _, key := range sortedKeys(p.sp.Soundboard) {
			spec := p.sp.Soundboard[key]
			w := p.exprFile("sound:" + key)
			w.name, w.emoji, w.vol = spec.Name, spec.Emoji, 1
			if spec.Volume != nil {
				w.vol = *spec.Volume
			}
			fields := map[string]FieldChange{}
			id, ok := p.ids[state.KindSound][key]
			if ok {
				cur := live[id]
				if cur.Name != spec.Name {
					fields["name"] = FieldChange{From: cur.Name, To: spec.Name}
				}
				if math.Abs(cur.Volume-w.vol) > 0.005 {
					fields["volume"] = FieldChange{From: cur.Volume, To: w.vol}
				}
				if normEmoji(cur.Emoji) != normEmoji(spec.Emoji) {
					fields["emoji"] = FieldChange{From: nilIfEmpty(cur.Emoji), To: nilIfEmpty(spec.Emoji)}
				}
			}
			p.planExpr(state.KindSound, KindSound, key, "sound "+quote(spec.Name), spec.Sound, w, ok, fields, &n, discord.SoundboardLimit(srv.PremiumTier), "soundboard sounds")
		}
	}
	return nil
}

func (p *planner) exprFile(name string) exprWant {
	w := exprWant{file: name, data: p.in.Files[name]}
	if w.data != nil {
		w.hash = hashOf(w.data)
	}
	return w
}

// planExpr plans one emoji / sticker / sound. Discord cannot replace the file of an
// expression: a changed file (hash recorded at the last upload) is blocked.
func (p *planner) planExpr(rk state.ResourceKind, kind, key, label, path string, w exprWant, exists bool, fields map[string]FieldChange, count *int, limit int, plural string) {
	how := p.how[rk][key]
	if how == "skip" {
		return
	}
	if !exists {
		if *count >= limit {
			p.blockFields(ActionCreate, kind, key, "", fmt.Sprintf("%smaximum number of %s reached (%d at boost level %d)",
				prefixLimit, plural, limit, p.in.Live.Server.PremiumTier), map[string]FieldChange{"exists": {From: false, To: true}})
			return
		}
		*count++
		if w.data == nil {
			return // file missing: reported by validation
		}
		p.add(Change{Action: ActionCreate, Kind: kind, Key: key, Summary: "create " + label,
			Fields: map[string]FieldChange{"name": {To: w.name}, "file": {To: path}},
			op:     &op{phase: phaseExpressions, rk: rk, expr: &w}})
		return
	}
	id := p.ids[rk][key]
	st := p.in.FileState[w.file]
	if w.hash != "" && st.Hash != "" && st.Hash != w.hash {
		p.blockFields(ActionUpdate, kind, key, id,
			prefixRecreate+"Discord cannot replace the file of an existing "+kind+": give it a new key (the old one is then removed or unmanaged)",
			map[string]FieldChange{"file": {From: "(previous file)", To: path}})
	}
	adopt := how == "adopt"
	if !adopt && len(fields) == 0 {
		return
	}
	c := Change{Action: ActionUpdate, Kind: kind, Key: key, ID: id, op: &op{phase: phaseExpressions, rk: rk, expr: &w}}
	if len(fields) > 0 {
		c.Fields = fields
	}
	if adopt {
		c.Action = ActionAdopt
		c.Summary = fmt.Sprintf("adopt %s (%s)", label, id)
		if len(fields) > 0 {
			c.Summary += ", update " + fieldList(fields)
		}
	} else {
		c.Summary = "update " + label + ": " + fieldList(fields)
	}
	p.add(c)
}

// ───────────────────────────────────────────────────────────── files

// parityFiles lists the repository files a server needs besides the icon:
// logical name → path relative to the server YAML.
func parityFiles(sp *config.ServerSpec) map[string]string {
	out := map[string]string{}
	if pr := sp.Profile; pr != nil {
		if pr.Banner != "" {
			out["banner"] = pr.Banner
		}
		if pr.Splash != "" {
			out["splash"] = pr.Splash
		}
	}
	for k, r := range sp.Roles {
		if r.Icon != "" {
			out["role:"+k] = r.Icon
		}
	}
	for k, e := range sp.Emojis {
		out["emoji:"+k] = e.Image
	}
	for k, s := range sp.Stickers {
		out["sticker:"+k] = s.File
	}
	for k, s := range sp.Soundboard {
		out["sound:"+k] = s.Sound
	}
	return out
}

// FetchOptionsFor selects the optional live parts a server needs: declared blocks, or
// git mappings of that kind (removed keys must be pruned or unmanaged).
func FetchOptionsFor(sp *config.ServerSpec, mapped []state.Resource) FetchOptions {
	has := map[state.ResourceKind]bool{}
	for _, m := range mapped {
		if m.Owner == state.OwnerGit {
			has[m.Kind] = true
		}
	}
	return FetchOptions{
		Widget:        sp.Widget != nil,
		Onboarding:    sp.Onboarding != nil,
		WelcomeScreen: sp.WelcomeScreen != nil,
		AutoMod:       len(sp.AutoMod) > 0 || has[state.KindAutoMod],
		Emojis:        len(sp.Emojis) > 0 || has[state.KindEmoji],
		Stickers:      len(sp.Stickers) > 0 || has[state.KindSticker],
		Sounds:        len(sp.Soundboard) > 0 || has[state.KindSound],
	}
}
