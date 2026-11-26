package disgoadapter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cicord/cicord/internal/discord"
)

const cdnBase = "https://cdn.discordapp.com"

// ───────────────────────────────────────────────────────────── enums

// Verification levels (Discord ints ↔ Cicord names).
var verificationLevels = []string{"none", "low", "medium", "high", "highest"}

// Default message notification levels.
var notificationLevels = []string{"allMessages", "onlyMentions"}

// Explicit content filter levels (0 disabled, 1 members without roles, 2 all members).
var explicitContentFilters = []string{"disabled", "membersWithoutRoles", "allMembers"}

// Optional enums: index 0 is "unset" (Discord 0 / null).
var (
	videoQualityModes = []string{"", "auto", "full"}
	forumLayouts      = []string{"", "list", "gallery"}
	// sortOrders is shifted by one: Discord null → "", 0 → latestActivity, 1 → creationDate.
	sortOrders = []string{"", "latestActivity", "creationDate"}
)

// optEnumName maps an optional enum; out-of-range values (newer Discord values) → "".
func optEnumName(table []string, v int) string {
	if v > 0 && v < len(table) {
		return table[v]
	}
	return ""
}

func optEnumValue(table []string, name, what string) (int, error) {
	if name == "" {
		return 0, nil
	}
	return enumValue(table, name, what)
}

func verificationLevelName(v int) string { return enumName(verificationLevels, v) }
func notificationLevelName(v int) string { return enumName(notificationLevels, v) }

func verificationLevelValue(name string) (int, error) {
	return enumValue(verificationLevels, name, "verification level")
}

func notificationLevelValue(name string) (int, error) {
	return enumValue(notificationLevels, name, "default notifications")
}

func enumName(table []string, v int) string {
	if v >= 0 && v < len(table) {
		return table[v]
	}
	return fmt.Sprintf("unknown(%d)", v)
}

func enumValue(table []string, name, what string) (int, error) {
	for i, n := range table {
		if n == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown %s %q", discord.ErrBadRequest, what, name)
}

// ───────────────────────────────────────────────────────────── primitives

func permString(p discord.Permissions) string { return strconv.FormatUint(uint64(p), 10) }

func parsePerm(s string) discord.Permissions {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return discord.Permissions(v)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isSnowflake(s string) bool {
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

// imageDataURI encodes raw image bytes as the data URI Discord expects for icons,
// avatars and banners. An empty slice yields "" (callers send null to remove).
func imageDataURI(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	ct := http.DetectContentType(b)
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		ct = "image/png"
	case bytes.HasPrefix(b, []byte("GIF8")):
		ct = "image/gif"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		ct = "image/jpeg"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		ct = "image/webp"
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func cdnImage(kind, id string, hash *string) string {
	if hash == nil || *hash == "" || id == "" {
		return ""
	}
	ext := "png"
	if strings.HasPrefix(*hash, "a_") {
		ext = "gif"
	}
	return fmt.Sprintf("%s/%s/%s/%s.%s", cdnBase, kind, id, *hash, ext)
}

// ───────────────────────────────────────────────────────────── emoji

// emojiToWire parses a Cicord emoji string: a unicode emoji, "name:id", "a:name:id",
// or the mention form "<:name:id>" / "<a:name:id>".
func emojiToWire(s string) *wireEmoji {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "<"), ">")
	}
	if i := strings.LastIndexByte(s, ':'); i > 0 && isSnowflake(s[i+1:]) {
		name := s[:i]
		animated := false
		if strings.HasPrefix(name, "a:") {
			animated = true
			name = name[2:]
		}
		name = strings.TrimPrefix(name, ":")
		return &wireEmoji{ID: s[i+1:], Name: name, Animated: animated}
	}
	return &wireEmoji{Name: s}
}

// emojiFromWire returns the Cicord emoji string: unicode, or "name:id" for custom emojis.
func emojiFromWire(e *wireEmoji) string {
	if e == nil {
		return ""
	}
	if e.ID != "" {
		return e.Name + ":" + e.ID
	}
	return e.Name
}

// reactionEmojiPath returns the URL path segment for a reaction emoji
// (unicode, or "name:id" for custom ones), percent-encoded.
func reactionEmojiPath(s string) (string, error) {
	e := emojiToWire(s)
	if e == nil {
		return "", fmt.Errorf("%w: empty emoji", discord.ErrBadRequest)
	}
	v := e.Name
	if e.ID != "" {
		v = e.Name + ":" + e.ID
	}
	return pathEscape(v), nil
}

// ───────────────────────────────────────────────────────────── users, members, roles

func userFromWire(u wireUser) discord.User {
	return discord.User{
		ID: u.ID, PublicFlags: u.PublicFlags,
		Username:   u.Username,
		GlobalName: deref(u.GlobalName),
		AvatarURL:  cdnImage("avatars", u.ID, u.Avatar),
		Bot:        u.Bot,
	}
}

// memberFromWire maps a member. user overrides m.User when m has none
// (message and resolved members are sent without their user).
func memberFromWire(serverID string, m wireMember, user *wireUser) discord.Member {
	out := discord.Member{
		ServerID: serverID,
		Nick:     deref(m.Nick),
		RoleIDs:  append([]string{}, m.Roles...),
	}
	if out.ServerID == "" {
		out.ServerID = m.GuildID
	}
	switch {
	case m.User != nil:
		out.User = userFromWire(*m.User)
	case user != nil:
		out.User = userFromWire(*user)
	}
	if m.JoinedAt != nil {
		out.JoinedAt = *m.JoinedAt
	}
	if m.CommunicationDisabledUntil != nil && !m.CommunicationDisabledUntil.IsZero() {
		t := *m.CommunicationDisabledUntil
		out.TimeoutUntil = &t
	}
	out.Pending, out.PremiumSince = m.Pending, m.PremiumSince
	if out.ServerID != "" && out.User.ID != "" {
		out.AvatarURL = cdnImage("guilds/"+out.ServerID+"/users/"+out.User.ID, "avatars", m.Avatar)
	}
	out.Flags = discord.MemberFlags(m.Flags)
	return out
}

func roleFromWire(r wireRole) discord.Role {
	out := discord.Role{
		ID:           r.ID,
		Name:         r.Name,
		Color:        r.Color,
		Hoist:        r.Hoist,
		Mentionable:  r.Mentionable,
		Permissions:  parsePerm(r.Permissions),
		Position:     r.Position,
		Managed:      r.Managed,
		IconURL:      cdnImage("role-icons", r.ID, r.Icon),
		UnicodeEmoji: deref(r.UnicodeEmoji),
		Colors:       discord.RoleColors{Primary: r.Color},
	}
	if r.Colors != nil {
		out.Colors = discord.RoleColors{Primary: r.Colors.PrimaryColor, Secondary: derefInt(r.Colors.SecondaryColor), Tertiary: derefInt(r.Colors.TertiaryColor)}
	}
	return out
}

func roleParamsBody(p discord.RoleParams) map[string]any {
	body := map[string]any{}
	if p.Name != nil {
		body["name"] = *p.Name
	}
	if p.Colors != nil {
		// colors supersedes color (Discord keeps color = colors.primary_color).
		body["colors"] = map[string]any{
			"primary_color":   p.Colors.Primary,
			"secondary_color": nullIfZero(p.Colors.Secondary),
			"tertiary_color":  nullIfZero(p.Colors.Tertiary),
		}
	} else if p.Color != nil {
		body["color"] = *p.Color
	}
	if p.Icon != nil {
		body["icon"] = nullIfEmpty(imageDataURI(*p.Icon))
	}
	if p.UnicodeEmoji != nil {
		body["unicode_emoji"] = nullIfEmpty(*p.UnicodeEmoji)
	}
	if p.Hoist != nil {
		body["hoist"] = *p.Hoist
	}
	if p.Mentionable != nil {
		body["mentionable"] = *p.Mentionable
	}
	if p.Permissions != nil {
		body["permissions"] = permString(*p.Permissions)
	}
	return body
}

// ───────────────────────────────────────────────────────────── servers

func serverFromWire(g wireGuild) discord.Server {
	return discord.Server{
		ID:                   g.ID,
		Name:                 g.Name,
		Description:          deref(g.Description),
		OwnerID:              g.OwnerID,
		IconURL:              cdnImage("icons", g.ID, g.Icon),
		VerificationLevel:    verificationLevelName(g.VerificationLevel),
		DefaultNotifications: notificationLevelName(g.DefaultMessageNotifications),
		SystemChannelID:      deref(g.SystemChannelID),
		RulesChannelID:       deref(g.RulesChannelID),

		BannerURL:                cdnImage("banners", g.ID, g.Banner),
		SplashURL:                cdnImage("splashes", g.ID, g.Splash),
		ExplicitContentFilter:    enumName(explicitContentFilters, g.ExplicitContentFilter),
		SystemChannelFlags:       discord.SystemChannelFlags(g.SystemChannelFlags),
		PublicUpdatesChannelID:   deref(g.PublicUpdatesChannelID),
		SafetyAlertsChannelID:    deref(g.SafetyAlertsChannelID),
		AFKChannelID:             deref(g.AFKChannelID),
		AFKTimeout:               g.AFKTimeout,
		PreferredLocale:          g.PreferredLocale,
		PremiumProgressBar:       g.PremiumProgressBarEnabled,
		Features:                 append([]string{}, g.Features...),
		PremiumTier:              g.PremiumTier,
		PremiumSubscriptionCount: derefInt(g.PremiumSubscriptionCount),
	}
}

// serverParamsBody builds the Modify Guild body. "" channel IDs, an empty
// description and empty icon bytes are sent as JSON null (clear).
func serverParamsBody(p discord.ServerParams) (map[string]any, error) {
	body := map[string]any{}
	if p.Name != nil {
		body["name"] = *p.Name
	}
	if p.Description != nil {
		body["description"] = nullIfEmpty(*p.Description)
	}
	if p.Icon != nil {
		body["icon"] = nullIfEmpty(imageDataURI(*p.Icon))
	}
	if p.VerificationLevel != nil {
		v, err := verificationLevelValue(*p.VerificationLevel)
		if err != nil {
			return nil, err
		}
		body["verification_level"] = v
	}
	if p.DefaultNotifications != nil {
		v, err := notificationLevelValue(*p.DefaultNotifications)
		if err != nil {
			return nil, err
		}
		body["default_message_notifications"] = v
	}
	if p.SystemChannelID != nil {
		body["system_channel_id"] = nullIfEmpty(*p.SystemChannelID)
	}
	if p.RulesChannelID != nil {
		body["rules_channel_id"] = nullIfEmpty(*p.RulesChannelID)
	}
	if p.Banner != nil {
		body["banner"] = nullIfEmpty(imageDataURI(*p.Banner))
	}
	if p.Splash != nil {
		body["splash"] = nullIfEmpty(imageDataURI(*p.Splash))
	}
	if p.ExplicitContentFilter != nil {
		v, err := enumValue(explicitContentFilters, *p.ExplicitContentFilter, "explicit content filter")
		if err != nil {
			return nil, err
		}
		body["explicit_content_filter"] = v
	}
	if p.SystemChannelFlags != nil {
		body["system_channel_flags"] = int(*p.SystemChannelFlags)
	}
	if p.PublicUpdatesChannelID != nil {
		body["public_updates_channel_id"] = nullIfEmpty(*p.PublicUpdatesChannelID)
	}
	if p.SafetyAlertsChannelID != nil {
		body["safety_alerts_channel_id"] = nullIfEmpty(*p.SafetyAlertsChannelID)
	}
	if p.AFKChannelID != nil {
		body["afk_channel_id"] = nullIfEmpty(*p.AFKChannelID)
	}
	if p.AFKTimeout != nil {
		body["afk_timeout"] = *p.AFKTimeout
	}
	if p.PreferredLocale != nil {
		body["preferred_locale"] = *p.PreferredLocale
	}
	if p.PremiumProgressBar != nil {
		body["premium_progress_bar_enabled"] = *p.PremiumProgressBar
	}
	if p.Features != nil {
		f := append([]string{}, (*p.Features)...)
		body["features"] = f
	}
	return body, nil
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func nullIfZero(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// splitEmoji turns a Cicord emoji string (unicode, "name:id", "<:name:id>" or a bare
// custom emoji ID) into Discord's emoji_id / emoji_name pair.
func splitEmoji(s string) (id, name *string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) >= 15 && isSnowflake(s) {
		return &s, nil
	}
	e := emojiToWire(s)
	if e.ID != "" {
		eid := e.ID
		if e.Name == "" {
			return &eid, nil
		}
		n := e.Name
		return &eid, &n
	}
	n := e.Name
	return nil, &n
}

// joinEmoji is the inverse of splitEmoji: "name:id" for custom emojis (the bare ID
// when Discord sends no name), the unicode character otherwise.
func joinEmoji(id, name *string) string {
	if i := deref(id); i != "" && i != "0" {
		if n := deref(name); n != "" {
			return n + ":" + i
		}
		return i
	}
	return deref(name)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ───────────────────────────────────────────────────────────── channels

func overwriteFromWire(o wireOverwrite) discord.Overwrite {
	return discord.Overwrite{
		ID:    o.ID,
		Type:  discord.OverwriteType(o.Type),
		Allow: parsePerm(o.Allow),
		Deny:  parsePerm(o.Deny),
	}
}

func overwriteToWire(o discord.Overwrite) wireOverwrite {
	return wireOverwrite{
		ID:    o.ID,
		Type:  int(o.Type),
		Allow: permString(o.Allow),
		Deny:  permString(o.Deny),
	}
}

func overwritesToWire(in []discord.Overwrite) []wireOverwrite {
	out := make([]wireOverwrite, 0, len(in))
	for _, o := range in {
		out = append(out, overwriteToWire(o))
	}
	return out
}

func channelFromWire(c wireChannel) discord.Channel {
	out := discord.Channel{
		ID:               c.ID,
		ServerID:         c.GuildID,
		Name:             c.Name,
		Type:             discord.ChannelType(c.Type),
		ParentID:         deref(c.ParentID),
		Position:         c.Position,
		Topic:            deref(c.Topic),
		NSFW:             c.NSFW,
		RateLimitPerUser: c.RateLimitPerUser,
		Bitrate:          c.Bitrate,
		UserLimit:        c.UserLimit,

		RTCRegion:             deref(c.RTCRegion),
		VideoQuality:          optEnumName(videoQualityModes, c.VideoQualityMode),
		DefaultAutoArchive:    c.DefaultAutoArchiveDuration,
		DefaultThreadSlowmode: c.DefaultThreadRateLimitPerUser,
		DefaultLayout:         optEnumName(forumLayouts, c.DefaultForumLayout),
	}
	if c.DefaultSortOrder != nil {
		out.DefaultSortOrder = optEnumName(sortOrders, *c.DefaultSortOrder+1)
	}
	if c.DefaultReactionEmoji != nil {
		out.DefaultReaction = joinEmoji(c.DefaultReactionEmoji.EmojiID, c.DefaultReactionEmoji.EmojiName)
	}
	for _, t := range c.AvailableTags {
		out.Tags = append(out.Tags, discord.ForumTag{ID: t.ID, Name: t.Name, Moderated: t.Moderated, Emoji: joinEmoji(t.EmojiID, t.EmojiName)})
	}
	// Keep an explicit empty policy distinct from a partial channel with no ACL data.
	if c.PermissionOverwrites != nil {
		out.Overwrites = make([]discord.Overwrite, 0, len(c.PermissionOverwrites))
		for _, o := range c.PermissionOverwrites {
			out.Overwrites = append(out.Overwrites, overwriteFromWire(o))
		}
	}
	return out
}

// channelCreateBody builds the Create Guild Channel body. ParentID "" means no category.
func channelCreateBody(p discord.ChannelParams) (map[string]any, error) {
	if p.Name == nil || *p.Name == "" {
		return nil, fmt.Errorf("%w: channel name is required", discord.ErrBadRequest)
	}
	body, err := channelCommonBody(p)
	if err != nil {
		return nil, err
	}
	body["type"] = int(p.Type)
	if p.ParentID != nil {
		if *p.ParentID == "" {
			delete(body, "parent_id")
		}
	}
	return body, nil
}

// channelUpdateBody builds the Modify Channel body. ParentID "" is sent as null.
// LockPermissions is resolved by the caller (it needs the parent's overwrites).
func channelUpdateBody(p discord.ChannelParams) (map[string]any, error) {
	return channelCommonBody(p)
}

func channelCommonBody(p discord.ChannelParams) (map[string]any, error) {
	body := map[string]any{}
	if p.Name != nil {
		body["name"] = *p.Name
	}
	if p.ParentID != nil {
		body["parent_id"] = nullIfEmpty(*p.ParentID)
	}
	if p.Position != nil {
		body["position"] = *p.Position
	}
	if p.Topic != nil {
		body["topic"] = *p.Topic
	}
	if p.NSFW != nil {
		body["nsfw"] = *p.NSFW
	}
	if p.RateLimitPerUser != nil {
		body["rate_limit_per_user"] = *p.RateLimitPerUser
	}
	if p.Bitrate != nil {
		body["bitrate"] = *p.Bitrate
	}
	if p.UserLimit != nil {
		body["user_limit"] = *p.UserLimit
	}
	if p.Overwrites != nil {
		body["permission_overwrites"] = overwritesToWire(*p.Overwrites)
	}
	if p.RTCRegion != nil {
		body["rtc_region"] = nullIfEmpty(*p.RTCRegion)
	}
	if p.VideoQuality != nil {
		v, err := optEnumValue(videoQualityModes, *p.VideoQuality, "video quality")
		if err != nil {
			return nil, err
		}
		body["video_quality_mode"] = v
	}
	if p.DefaultAutoArchive != nil {
		body["default_auto_archive_duration"] = *p.DefaultAutoArchive
	}
	if p.DefaultThreadSlowmode != nil {
		body["default_thread_rate_limit_per_user"] = *p.DefaultThreadSlowmode
	}
	if p.Tags != nil {
		tags := make([]wireForumTag, 0, len(*p.Tags))
		for _, t := range *p.Tags {
			id, name := splitEmoji(t.Emoji)
			tags = append(tags, wireForumTag{ID: t.ID, Name: t.Name, Moderated: t.Moderated, EmojiID: id, EmojiName: name})
		}
		body["available_tags"] = tags
	}
	if p.DefaultReaction != nil {
		if *p.DefaultReaction == "" {
			body["default_reaction_emoji"] = nil
		} else {
			id, name := splitEmoji(*p.DefaultReaction)
			body["default_reaction_emoji"] = wireDefaultReaction{EmojiID: id, EmojiName: name}
		}
	}
	if p.DefaultSortOrder != nil {
		v, err := optEnumValue(sortOrders, *p.DefaultSortOrder, "sort order")
		if err != nil {
			return nil, err
		}
		if v == 0 {
			body["default_sort_order"] = nil
		} else {
			body["default_sort_order"] = v - 1
		}
	}
	if p.DefaultLayout != nil {
		v, err := optEnumValue(forumLayouts, *p.DefaultLayout, "forum layout")
		if err != nil {
			return nil, err
		}
		body["default_forum_layout"] = v
	}
	return body, nil
}

func channelPositionsBody(positions []discord.ChannelPosition) []map[string]any {
	out := make([]map[string]any, 0, len(positions))
	for _, p := range positions {
		e := map[string]any{"id": p.ID, "position": p.Position}
		if p.ParentID != nil {
			e["parent_id"] = nullIfEmpty(*p.ParentID)
		}
		out = append(out, e)
	}
	return out
}

// ───────────────────────────────────────────────────────────── embeds

func embedToWire(e discord.Embed) wireEmbed {
	w := wireEmbed{
		Type:        "rich",
		Title:       e.Title,
		Description: e.Description,
		URL:         e.URL,
		Color:       e.Color,
		Timestamp:   e.Timestamp,
	}
	for _, f := range e.Fields {
		w.Fields = append(w.Fields, wireEmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
	}
	if e.Author != nil {
		w.Author = &wireEmbedAuthor{Name: e.Author.Name, URL: e.Author.URL, IconURL: e.Author.IconURL}
	}
	if e.Footer != nil {
		w.Footer = &wireEmbedFooter{Text: e.Footer.Text, IconURL: e.Footer.IconURL}
	}
	if e.ImageURL != "" {
		w.Image = &wireEmbedMedia{URL: e.ImageURL}
	}
	if e.ThumbURL != "" {
		w.Thumbnail = &wireEmbedMedia{URL: e.ThumbURL}
	}
	return w
}

func embedFromWire(w wireEmbed) discord.Embed {
	e := discord.Embed{
		Title:       w.Title,
		Description: w.Description,
		URL:         w.URL,
		Color:       w.Color,
		Timestamp:   w.Timestamp,
	}
	for _, f := range w.Fields {
		e.Fields = append(e.Fields, discord.EmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
	}
	if w.Author != nil {
		e.Author = &discord.EmbedAuthor{Name: w.Author.Name, URL: w.Author.URL, IconURL: w.Author.IconURL}
	}
	if w.Footer != nil {
		e.Footer = &discord.EmbedFooter{Text: w.Footer.Text, IconURL: w.Footer.IconURL}
	}
	if w.Image != nil {
		e.ImageURL = w.Image.URL
	}
	if w.Thumbnail != nil {
		e.ThumbURL = w.Thumbnail.URL
	}
	return e
}

func embedsToWire(in []discord.Embed) []wireEmbed {
	if in == nil {
		return nil
	}
	out := make([]wireEmbed, 0, len(in))
	for _, e := range in {
		out = append(out, embedToWire(e))
	}
	return out
}

// ───────────────────────────────────────────────────────────── components

func componentToWire(c discord.Component) wireComponent {
	w := wireComponent{ComponentPresentation: c.ComponentPresentation, Required: c.ModalRequired,
		Type:        int(c.Type),
		CustomID:    c.CustomID,
		Label:       c.Label,
		Disabled:    c.Disabled,
		Placeholder: c.Placeholder,
		MinValues:   c.MinValues,
		MaxValues:   c.MaxValues,
	}
	for _, child := range c.Components {
		w.Components = append(w.Components, componentToWire(child))
	}
	if c.Accessory != nil {
		a := componentToWire(*c.Accessory)
		w.Accessory = &a
	}
	if c.Type == discord.ComponentLabel && len(w.Components) == 1 {
		w.Component = &w.Components[0]
		w.Components = nil
	}
	switch c.Type {
	case discord.ComponentButton:
		w.Style = int(c.Style)
		w.Emoji = emojiToWire(c.Emoji)
		if c.Style == discord.ButtonLink {
			// Link buttons carry a URL and must not have a custom_id.
			w.URL = c.URL
			w.CustomID = ""
		}
	case discord.ComponentStringSelect:
		for _, o := range c.Options {
			w.Options = append(w.Options, wireSelectOption{
				Label:       o.Label,
				Value:       o.Value,
				Description: o.Description,
				Emoji:       emojiToWire(o.Emoji),
				Default:     o.Default,
			})
		}
	case discord.ComponentTextInput:
		style := c.TextStyle
		if style == 0 {
			style = 1
		}
		w.Style = style
		req := c.Required
		w.Required = &req // Discord defaults to required=true; always send it.
		w.Value = c.Value
		w.MinLength = c.MinLength
		w.MaxLength = c.MaxLength
	}
	return w
}

func componentsToWire(in []discord.Component) []wireComponent {
	if in == nil {
		return nil
	}
	out := make([]wireComponent, 0, len(in))
	for _, c := range in {
		out = append(out, componentToWire(c))
	}
	return out
}

func componentFromWire(w wireComponent) discord.Component {
	c := discord.Component{ComponentPresentation: w.ComponentPresentation,
		Type:        discord.ComponentType(w.Type),
		CustomID:    w.CustomID,
		Label:       w.Label,
		Disabled:    w.Disabled,
		Emoji:       emojiFromWire(w.Emoji),
		URL:         w.URL,
		Placeholder: w.Placeholder,
		MinValues:   w.MinValues,
		MaxValues:   w.MaxValues,
		Value:       w.Value,
		MinLength:   w.MinLength,
		MaxLength:   w.MaxLength,
	}
	if w.Accessory != nil {
		a := componentFromWire(*w.Accessory)
		c.Accessory = &a
	}
	if w.Type != int(discord.ComponentTextInput) && w.Required != nil {
		c.Required = *w.Required
		c.ModalRequired = w.Required
	}
	if w.Type == int(discord.ComponentTextInput) {
		c.TextStyle = w.Style
		c.Required = w.Required == nil || *w.Required
	} else {
		c.Style = discord.ButtonStyle(w.Style)
	}
	for _, o := range w.Options {
		c.Options = append(c.Options, discord.SelectOption{
			Label:       o.Label,
			Value:       o.Value,
			Description: o.Description,
			Emoji:       emojiFromWire(o.Emoji),
			Default:     o.Default,
		})
	}
	for _, child := range w.Components {
		c.Components = append(c.Components, componentFromWire(child))
	}
	if w.Component != nil {
		c.Components = append(c.Components, componentFromWire(*w.Component))
	}
	return c
}

// modalFields collects text input values (custom ID → value) from a modal submit,
// walking action rows and label wrappers recursively. Select menus inside modals
// report their values joined with ",".
func modalFields(comps []wireComponent) map[string]string {
	out := map[string]string{}
	var walk func([]wireComponent)
	walk = func(cs []wireComponent) {
		for _, c := range cs {
			if c.CustomID != "" {
				if c.Values != nil {
					out[c.CustomID] = strings.Join(c.Values, ",")
				} else {
					out[c.CustomID] = c.Value
				}
			}
			walk(c.Components)
			if c.Component != nil {
				walk([]wireComponent{*c.Component})
			}
		}
	}
	walk(comps)
	return out
}

// ───────────────────────────────────────────────────────────── messages

func allowedMentionsToWire(a *discord.AllowedMentions) *wireAllowedMentions {
	if a == nil {
		return nil
	}
	w := &wireAllowedMentions{Parse: []string{}}
	if a.Users {
		w.Parse = append(w.Parse, "users")
	}
	if a.Roles {
		w.Parse = append(w.Parse, "roles")
	}
	if a.Everyone {
		w.Parse = append(w.Parse, "everyone")
	}
	// Discord rejects a type that is both in parse and listed: Validate refuses that
	// earlier, and a listed type is never parsed here.
	if !a.Users {
		w.Users = a.UserIDs
	}
	if !a.Roles {
		w.Roles = a.RoleIDs
	}
	w.RepliedUser = a.RepliedUser
	return w
}

func messageSendToWire(m discord.MessageCreate) wireMessageSend {
	w := wireMessageSend{
		Attachments:     uploadDescriptions(m.Files),
		Poll:            pollToWire(m.Poll),
		Content:         m.Content,
		Embeds:          embedsToWire(m.Embeds),
		Components:      componentsToWire(m.Components),
		AllowedMentions: allowedMentionsToWire(m.AllowedMentions),
		Flags:           int(m.Flags),
	}
	if m.ReplyTo != "" {
		w.MessageReference = &wireMessageReference{MessageID: m.ReplyTo, FailIfNotExists: false}
	}
	return w
}

// messageEditToWire builds an edit body: content, embeds and components are always
// sent, so the message ends up exactly as described (nil slices clear them).
// The ephemeral flag cannot be changed on edit and is dropped.
func messageEditToWire(m discord.MessageCreate) wireMessageEdit {
	w := wireMessageEdit{
		Content:         m.Content,
		Embeds:          embedsToWire(m.Embeds),
		Components:      componentsToWire(m.Components),
		AllowedMentions: allowedMentionsToWire(m.AllowedMentions),
	}
	if m.Files != nil {
		a := uploadDescriptions(m.Files)
		w.Attachments = &a
	}
	if w.Embeds == nil {
		w.Embeds = []wireEmbed{}
	}
	if w.Components == nil {
		w.Components = []wireComponent{}
	}
	if m.Flags&discord.MessageFlagComponentsV2 != 0 {
		w.Content = nil
	}
	if f := int(m.Flags &^ discord.MessageFlagEphemeral); f != 0 {
		w.Flags = &f
	}
	return w
}

func messageFromWire(w wireMessage) discord.Message {
	m := discord.Message{
		ID:        w.ID,
		ChannelID: w.ChannelID,
		ServerID:  w.GuildID,
		Author:    userFromWire(w.Author),
		Content:   w.Content,
		Flags:     discord.MessageFlags(w.Flags),
		CreatedAt: w.Timestamp,
		EditedAt:  w.EditedTimestamp,
		WebhookID: w.WebhookID,
		Poll:      w.Poll != nil, PollResults: pollFromWire(w.Poll),
		System: isSystemMessage(w.Type),
	}
	if r := w.MessageReference; r != nil && r.Type == 0 && w.Type == messageTypeReply {
		m.ReplyToID = r.MessageID
		m.Reference = &discord.MessageReference{MessageID: r.MessageID, ChannelID: r.ChannelID, ServerID: r.GuildID}
	}
	for _, r := range w.Reactions {
		m.Reactions = append(m.Reactions, discord.ReactionSummary{Emoji: emojiFromWire(&r.Emoji), Count: r.Count, Me: r.Me})
	}
	for _, st := range w.StickerItems {
		m.StickerIDs = append(m.StickerIDs, st.ID)
	}
	switch {
	case w.Interaction != nil:
		m.Interaction = &discord.MessageInteraction{Type: discord.InteractionType(w.Interaction.Type), Name: w.Interaction.Name, UserID: w.Interaction.User.ID}
	case w.InteractionMetadata != nil:
		m.Interaction = &discord.MessageInteraction{Type: discord.InteractionType(w.InteractionMetadata.Type), UserID: w.InteractionMetadata.User.ID}
	}
	if w.Member != nil && w.GuildID != "" {
		mem := memberFromWire(w.GuildID, *w.Member, &w.Author)
		m.Member = &mem
	}
	for _, e := range w.Embeds {
		m.Embeds = append(m.Embeds, embedFromWire(e))
	}
	for _, c := range w.Components {
		m.Components = append(m.Components, componentFromWire(c))
	}
	for _, a := range w.Attachments {
		m.Attachments = append(m.Attachments, attachmentFromWire(a))
	}
	return m
}

// Message types written by their author: DEFAULT, REPLY, CHAT_INPUT_COMMAND and
// CONTEXT_MENU_COMMAND. Every other type is a system message.
const messageTypeReply = 19

func isSystemMessage(t int) bool {
	switch t {
	case 0, messageTypeReply, 20, 23:
		return false
	}
	return true
}

func attachmentFromWire(a wireAttachment) discord.Attachment {
	return discord.Attachment{Description: a.Description, ID: a.ID, Filename: a.Filename, URL: a.URL, ContentType: a.ContentType, Size: a.Size}
}

// ───────────────────────────────────────────────────────────── commands

func commandOptionToWire(o discord.CommandOption) wireCommandOption {
	wo := wireCommandOption{CommandLabels: o.CommandLabels,
		Type:         int(o.Type),
		Name:         o.Name,
		Description:  o.Description,
		Required:     o.Required && !o.IsSubcommand(),
		MinValue:     o.MinValue,
		MaxValue:     o.MaxValue,
		MinLength:    o.MinLength,
		MaxLength:    o.MaxLength,
		Autocomplete: o.Autocomplete,
	}
	for _, ch := range o.Choices {
		wo.Choices = append(wo.Choices, wireCommandChoice{Name: ch.Name, NameLocalizations: ch.Names, Value: ch.Value})
	}
	for _, sub := range o.Options {
		wo.Options = append(wo.Options, commandOptionToWire(sub))
	}
	return wo
}

func commandToWire(c discord.Command) wireCommand {
	w := wireCommand{CommandLabels: c.CommandLabels, Type: int(c.Type), Name: c.Name, Description: c.Description}
	if w.Type == 0 {
		w.Type = 1
	}
	for _, o := range c.Options {
		w.Options = append(w.Options, commandOptionToWire(o))
	}
	if c.DefaultMemberPermissions != nil {
		s := permString(*c.DefaultMemberPermissions)
		w.DefaultMemberPermissions = &s
	}
	return w
}

func commandsToWire(cmds []discord.Command) []wireCommand {
	out := make([]wireCommand, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, commandToWire(c))
	}
	return out
}

// ───────────────────────────────────────────────────────────── interaction responses

func interactionResponseToWire(r discord.InteractionResponse) (wireInteractionResponse, error) {
	w := wireInteractionResponse{Type: int(r.Type)}
	switch r.Type {
	case discord.ResponseMessage:
		if r.Data == nil {
			return w, fmt.Errorf("%w: message response without data", discord.ErrBadRequest)
		}
		data := messageSendToWire(*r.Data)
		data.MessageReference = nil // not part of interaction callback data
		w.Data = data
	case discord.ResponseUpdateMessage:
		if r.Data == nil {
			return w, fmt.Errorf("%w: update response without data", discord.ErrBadRequest)
		}
		w.Data = messageEditToWire(*r.Data)
	case discord.ResponseDeferredMessage:
		if r.Data != nil && r.Data.Flags != 0 {
			w.Data = map[string]any{"flags": int(r.Data.Flags)}
		}
	case discord.ResponseDeferredUpdate:
	case discord.ResponseAutocomplete:
		if len(r.Choices) > discord.MaxAutocompleteChoices {
			return w, fmt.Errorf("%w: %d autocomplete choices (at most %d)", discord.ErrBadRequest, len(r.Choices), discord.MaxAutocompleteChoices)
		}
		choices := make([]wireCommandChoice, 0, len(r.Choices))
		for _, ch := range r.Choices {
			choices = append(choices, wireCommandChoice{Name: ch.Name, Value: ch.Value})
		}
		w.Data = map[string]any{"choices": choices}
	case discord.ResponseModal:
		if r.Modal == nil {
			return w, fmt.Errorf("%w: modal response without modal", discord.ErrBadRequest)
		}
		w.Data = wireModal{
			CustomID:   r.Modal.CustomID,
			Title:      r.Modal.Title,
			Components: componentsToWire(r.Modal.Components),
		}
	default:
		return w, fmt.Errorf("%w: unsupported interaction response type %d", discord.ErrBadRequest, r.Type)
	}
	return w, nil
}

// ───────────────────────────────────────────────────────────── received interactions

// optionValue converts a raw option value to the contract's Go type: float64 for
// integer/number, bool for boolean, string otherwise (IDs for user/channel/role/...).
func optionValue(t int, raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch discord.CommandOptionType(t) {
	case discord.OptionInteger, discord.OptionNumber:
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			return f
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		}
		return nil
	case discord.OptionBoolean:
		var b bool
		_ = json.Unmarshal(raw, &b)
		return b
	default:
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return strings.Trim(string(raw), `"`)
	}
}

// interactionOptionsFromWire keeps the order Discord sent. Subcommands and groups (types
// 1/2) have no value and nest their options. The focused option of an autocomplete keeps
// the raw text typed so far (a string, even for integer/number options: "1." or "-" are
// not numbers yet).
func interactionOptionsFromWire(in []wireInteractionOption) []discord.InteractionOption {
	var out []discord.InteractionOption
	for _, o := range in {
		io := discord.InteractionOption{
			Name:    o.Name,
			Type:    discord.CommandOptionType(o.Type),
			Focused: o.Focused,
			Options: interactionOptionsFromWire(o.Options),
		}
		switch {
		case o.Type == int(discord.OptionSubcommand) || o.Type == int(discord.OptionSubcommandGroup):
		case o.Focused:
			io.Value = optionValue(int(discord.OptionString), o.Value)
			if io.Value == nil {
				io.Value = ""
			}
		default:
			io.Value = optionValue(o.Type, o.Value)
		}
		out = append(out, io)
	}
	return out
}

func resolvedFromWire(serverID string, r *wireResolved) discord.Resolved {
	var out discord.Resolved
	if r == nil {
		return out
	}
	if len(r.Users) > 0 {
		out.Users = make(map[string]discord.User, len(r.Users))
		for id, u := range r.Users {
			if u.ID == "" {
				u.ID = id
			}
			out.Users[id] = userFromWire(u)
		}
	}
	if len(r.Members) > 0 {
		out.Members = make(map[string]discord.Member, len(r.Members))
		for id, m := range r.Members {
			var user *wireUser
			if u, ok := r.Users[id]; ok {
				if u.ID == "" {
					u.ID = id
				}
				user = &u
			} else {
				user = &wireUser{ID: id}
			}
			out.Members[id] = memberFromWire(serverID, m, user)
		}
	}
	if len(r.Roles) > 0 {
		out.Roles = make(map[string]discord.Role, len(r.Roles))
		for id, ro := range r.Roles {
			if ro.ID == "" {
				ro.ID = id
			}
			out.Roles[id] = roleFromWire(ro)
		}
	}
	if len(r.Channels) > 0 {
		out.Channels = make(map[string]discord.Channel, len(r.Channels))
		for id, c := range r.Channels {
			if c.ID == "" {
				c.ID = id
			}
			if c.GuildID == "" {
				c.GuildID = serverID
			}
			out.Channels[id] = channelFromWire(c)
		}
	}
	if len(r.Attachments) > 0 {
		out.Attachments = make(map[string]discord.Attachment, len(r.Attachments))
		for id, a := range r.Attachments {
			if a.ID == "" {
				a.ID = id
			}
			out.Attachments[id] = attachmentFromWire(a)
		}
	}
	return out
}

func interactionFromWire(w wireInteraction) discord.Interaction {
	in := discord.Interaction{
		ID: w.ID, Locale: w.Locale,
		Token:         w.Token,
		ApplicationID: w.ApplicationID,
		Type:          discord.InteractionType(w.Type),
		ServerID:      w.GuildID,
		ChannelID:     w.ChannelID,
	}
	if in.ChannelID == "" && w.Channel != nil {
		in.ChannelID = w.Channel.ID
	}
	switch {
	case w.Member != nil:
		m := memberFromWire(w.GuildID, *w.Member, w.User)
		in.Member = &m
		in.User = m.User
	case w.User != nil:
		in.User = userFromWire(*w.User)
	}
	if w.Message != nil {
		msg := messageFromWire(*w.Message)
		in.Message = &msg
	}
	if d := w.Data; d != nil {
		switch in.Type {
		case discord.InteractionCommand, discord.InteractionAutocomplete:
			in.CommandName = d.Name
			in.Options = interactionOptionsFromWire(d.Options)
			in.Resolved = resolvedFromWire(w.GuildID, d.Resolved)
			in.CommandType = discord.CommandType(d.Type)
			if d.Resolved != nil && d.TargetID != "" {
				if in.CommandType == discord.UserMenu {
					if member, ok := in.Resolved.Members[d.TargetID]; ok {
						in.TargetMember = &member
					}
				}
				if in.CommandType == discord.MessageMenu {
					if msg, ok := d.Resolved.Messages[d.TargetID]; ok && (msg.ChannelID == "" || msg.ChannelID == in.ChannelID) && (msg.GuildID == "" || msg.GuildID == in.ServerID) {
						msg.ChannelID = in.ChannelID
						msg.GuildID = in.ServerID
						target := messageFromWire(msg)
						in.TargetMessage = &target
					}
				}
			}
		case discord.InteractionComponent:
			in.CustomID = d.CustomID
			in.ComponentType = discord.ComponentType(d.ComponentType)
			in.Values = append([]string{}, d.Values...)
			in.Resolved = resolvedFromWire(w.GuildID, d.Resolved)
		case discord.InteractionModalSubmit:
			in.CustomID = d.CustomID
			in.ModalFields = modalFields(d.Components)
			in.ModalSelections = modalSelections(d.Components)
		}
	}
	return in
}

func modalSelections(comps []wireComponent) map[string][]string {
	out := map[string][]string{}
	var walk func([]wireComponent)
	walk = func(cs []wireComponent) {
		for _, c := range cs {
			if c.CustomID != "" && c.Values != nil {
				out[c.CustomID] = append([]string{}, c.Values...)
			}
			walk(c.Components)
			if c.Component != nil {
				walk([]wireComponent{*c.Component})
			}
		}
	}
	walk(comps)
	return out
}
