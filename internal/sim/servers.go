package sim

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
)

// ServerOption configures CreateServer.
type ServerOption func(*serverConfig)

type serverConfig struct {
	general bool
}

// WithGeneralChannel creates an uncategorized "general" text channel and makes it the
// system channel, like a new Discord server.
func WithGeneralChannel() ServerOption {
	return func(c *serverConfig) { c.general = true }
}

// CreateServer creates a server owned by ownerID (who becomes its first member). The
// @everyone role has ID == server ID, position 0 and DefaultEveryonePermissions.
// It panics if ownerID is not a known user.
func (w *World) CreateServer(name, ownerID string, opts ...ServerOption) discord.Server {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.users[ownerID] == nil {
		panic("sim: CreateServer: unknown owner " + ownerID)
	}
	return w.createServer(w.newID(), name, ownerID, opts)
}

// CreateServerWithID is CreateServer with a fixed server ID (and @everyone role ID),
// e.g. to seed a world from repository YAML. It fails with ErrBadRequest if id is not a
// valid snowflake or is already used by any entity, and ErrNotFound if the owner is
// unknown. Generated IDs never collide with injected ones.
func (w *World) CreateServerWithID(id, name, ownerID string, opts ...ServerOption) (discord.Server, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.users[ownerID] == nil {
		return discord.Server{}, notFound("unknown owner %s", ownerID)
	}
	if err := w.reserveID(id); err != nil {
		return discord.Server{}, err
	}
	return w.createServer(id, name, ownerID, opts), nil
}

// createServer builds a server. Caller holds w.mu and has validated owner and id.
func (w *World) createServer(id, name, ownerID string, opts []ServerOption) discord.Server {
	var cfg serverConfig
	for _, o := range opts {
		o(&cfg)
	}
	owner := w.users[ownerID]
	s := &serverState{
		info: discord.Server{
			ID: id, Name: name, OwnerID: ownerID,
			VerificationLevel: "none", DefaultNotifications: "allMessages",
			ExplicitContentFilter: "disabled", AFKTimeout: 300, PreferredLocale: "en-US",
			Features: []string{},
		},
		roles:      map[string]*discord.Role{id: {ID: id, Name: "@everyone", Permissions: DefaultEveryonePermissions}},
		managedBy:  map[string]string{},
		members:    map[string]*discord.Member{},
		bans:       map[string]discord.Ban{},
		automod:    map[string]*discord.AutoModRule{},
		emojis:     map[string]*discord.Emoji{},
		stickers:   map[string]*discord.Sticker{},
		sounds:     map[string]*discord.SoundboardSound{},
		onboarding: discord.Onboarding{Mode: "default", DefaultChannelIDs: []string{}, Prompts: []discord.OnboardingPrompt{}},
		welcome:    discord.WelcomeScreen{Channels: []discord.WelcomeChannel{}},
	}
	w.servers[id] = s
	s.members[ownerID] = &discord.Member{ServerID: id, User: owner.user, RoleIDs: []string{}, JoinedAt: w.now()}
	s.memberOrder = append(s.memberOrder, ownerID)
	if cfg.general {
		c := &channelState{ch: discord.Channel{ID: w.newID(), ServerID: id, Name: "general", Type: discord.ChannelText}}
		w.channels[c.ch.ID] = c
		s.channelIDs = append(s.channelIDs, c.ch.ID)
		s.info.SystemChannelID = c.ch.ID
	}
	w.changed()
	return cloneServer(s.info)
}

func (w *World) server(serverID string) (*serverState, error) {
	s := w.servers[serverID]
	if s == nil {
		return nil, notFound("unknown server %s", serverID)
	}
	return s, nil
}

// ───────────────────────────────────────────────────────────── server

func (w *World) getServer(a actor, serverID string) (discord.Server, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return discord.Server{}, err
	}
	if s.members[a.userID] == nil {
		return discord.Server{}, forbidden("missing access to server %s", serverID)
	}
	return cloneServer(s.info), nil
}

var (
	verificationLevels   = map[string]bool{"none": true, "low": true, "medium": true, "high": true, "highest": true}
	defaultNotifications = map[string]bool{"allMessages": true, "onlyMentions": true}
)

func (w *World) updateServer(a actor, serverID string, p discord.ServerParams) (discord.Server, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err == nil {
		err = w.requireServer(a, s, permManageServer)
	}
	if err == nil {
		err = w.applyServerParams(s, p)
	}
	if err != nil {
		return discord.Server{}, w.record(a, "UpdateServer", serverID, serverID, "", err)
	}
	w.recordOK(a, "UpdateServer", serverID, serverID, "")
	w.emit(discord.ServerUpdateEvent{Server: cloneServer(s.info)})
	return cloneServer(s.info), nil
}

func (w *World) applyServerParams(s *serverState, p discord.ServerParams) error {
	info := cloneServer(s.info)
	if p.Name != nil {
		if n := utf8.RuneCountInString(*p.Name); n < 2 || n > 100 {
			return badRequest("server name must be 2-100 characters")
		}
		info.Name = *p.Name
	}
	if p.Description != nil {
		if utf8.RuneCountInString(*p.Description) > 120 {
			return badRequest("server description must be at most 120 characters")
		}
		info.Description = *p.Description
	}
	// Feature flags first: the boost and community checks below use the new set.
	wasCommunity := info.HasFeature(discord.FeatureCommunity)
	if p.Features != nil {
		feats, err := mergeFeatures(info.Features, *p.Features)
		if err != nil {
			return err
		}
		info.Features = feats
	}
	community := info.HasFeature(discord.FeatureCommunity)
	if p.Icon != nil {
		if len(*p.Icon) == 0 {
			info.IconURL = ""
		} else {
			if isGIF(*p.Icon) {
				if r := info.BoostReason("an animated icon", discord.FeatureAnimatedIcon); r != "" {
					return badRequest("%s (ANIMATED_ICON feature)", r)
				}
			}
			info.IconURL = w.cdnPut(cdnURL("icons", s.info.ID, *p.Icon), *p.Icon)
		}
	}
	if p.Banner != nil {
		if len(*p.Banner) == 0 {
			info.BannerURL = ""
		} else {
			if r := info.BoostReason("banner", discord.FeatureBanner); r != "" {
				return badRequest("%s (BANNER feature)", r)
			}
			if isGIF(*p.Banner) {
				if r := info.BoostReason("an animated banner", discord.FeatureAnimatedBanner); r != "" {
					return badRequest("%s (ANIMATED_BANNER feature)", r)
				}
			}
			info.BannerURL = w.cdnPut(cdnURL("banners", s.info.ID, *p.Banner), *p.Banner)
		}
	}
	if p.Splash != nil {
		if len(*p.Splash) == 0 {
			info.SplashURL = ""
		} else {
			if r := info.BoostReason("invite splash", discord.FeatureInviteSplash); r != "" {
				return badRequest("%s (INVITE_SPLASH feature)", r)
			}
			info.SplashURL = w.cdnPut(cdnURL("splashes", s.info.ID, *p.Splash), *p.Splash)
		}
	}
	if p.VerificationLevel != nil {
		if !verificationLevels[*p.VerificationLevel] {
			return badRequest("invalid verification level %q", *p.VerificationLevel)
		}
		info.VerificationLevel = *p.VerificationLevel
	}
	if p.DefaultNotifications != nil {
		if !defaultNotifications[*p.DefaultNotifications] {
			return badRequest("invalid default notifications %q", *p.DefaultNotifications)
		}
		info.DefaultNotifications = *p.DefaultNotifications
	}
	if p.ExplicitContentFilter != nil {
		if !explicitFilters[*p.ExplicitContentFilter] {
			return badRequest("invalid explicit content filter %q", *p.ExplicitContentFilter)
		}
		info.ExplicitContentFilter = *p.ExplicitContentFilter
	}
	if p.PreferredLocale != nil {
		if !discordLocales[*p.PreferredLocale] {
			return badRequest("invalid preferred locale %q", *p.PreferredLocale)
		}
		info.PreferredLocale = *p.PreferredLocale
	}
	if p.PremiumProgressBar != nil {
		info.PremiumProgressBar = *p.PremiumProgressBar
	}
	if p.SystemChannelFlags != nil {
		if *p.SystemChannelFlags < 0 || *p.SystemChannelFlags >= 1<<6 {
			return badRequest("invalid system channel flags %d", *p.SystemChannelFlags)
		}
		info.SystemChannelFlags = *p.SystemChannelFlags
	}
	if p.AFKTimeout != nil {
		if !discord.ValidAFKTimeout(*p.AFKTimeout) {
			return badRequest("afk timeout must be one of %s seconds", discord.AFKTimeoutsDescription)
		}
		info.AFKTimeout = *p.AFKTimeout
	}
	checkChan := func(id, what string, types ...discord.ChannelType) error {
		if id == "" {
			return nil
		}
		c := w.channels[id]
		if c == nil || c.ch.ServerID != s.info.ID || !slices.Contains(types, c.ch.Type) {
			kind := "text"
			if types[0] == discord.ChannelVoice {
				kind = "voice"
			}
			return badRequest("%s must be a %s channel of the server", what, kind)
		}
		return nil
	}
	if p.AFKChannelID != nil {
		if err := checkChan(*p.AFKChannelID, "afk channel", discord.ChannelVoice); err != nil {
			return err
		}
		info.AFKChannelID = *p.AFKChannelID
	}
	if p.SystemChannelID != nil {
		if err := checkChan(*p.SystemChannelID, "system channel", discord.ChannelText); err != nil {
			return err
		}
		info.SystemChannelID = *p.SystemChannelID
	}
	communityRef := func(id *string, what string, dst *string) error {
		if id == nil {
			return nil
		}
		if *id != "" && !community {
			return badRequest("the %s requires the Community feature", what)
		}
		if err := checkChan(*id, what, discord.ChannelText); err != nil {
			return err
		}
		*dst = *id
		return nil
	}
	if err := communityRef(p.RulesChannelID, "rules channel", &info.RulesChannelID); err != nil {
		return err
	}
	if err := communityRef(p.PublicUpdatesChannelID, "community updates channel", &info.PublicUpdatesChannelID); err != nil {
		return err
	}
	if err := communityRef(p.SafetyAlertsChannelID, "safety alerts channel", &info.SafetyAlertsChannelID); err != nil {
		return err
	}
	touched := !wasCommunity || p.RulesChannelID != nil || p.PublicUpdatesChannelID != nil ||
		p.VerificationLevel != nil || p.ExplicitContentFilter != nil
	if community && touched {
		// Discord's Community requirements: checked when enabling, and on every change of
		// the related settings while enabled.
		switch {
		case info.RulesChannelID == "":
			return badRequest("Community servers require a rules channel (rules_channel_id)")
		case info.PublicUpdatesChannelID == "":
			return badRequest("Community servers require a community updates channel (public_updates_channel_id)")
		case info.VerificationLevel == "none":
			return badRequest("Community servers require a verification level of at least low")
		case info.ExplicitContentFilter != "allMembers":
			return badRequest("Community servers require the explicit content filter to scan media from all members (allMembers)")
		}
	}
	if community && !wasCommunity {
		info.Features = addFeature(info.Features, discord.FeatureNews)
	} else if !community && wasCommunity {
		info.Features = removeFeature(info.Features, discord.FeatureNews, discord.FeatureWelcomeScreenEnabled, discord.FeatureGuildOnboarding)
		s.welcome.Enabled = false
		s.onboarding.Enabled = false
	}
	s.info = info
	return nil
}

var (
	explicitFilters = map[string]bool{"disabled": true, "membersWithoutRoles": true, "allMembers": true}
	// discordLocales are Discord's locales (Reference → Locales).
	discordLocales = map[string]bool{
		"id": true, "da": true, "de": true, "en-GB": true, "en-US": true, "es-ES": true, "es-419": true,
		"fr": true, "hr": true, "it": true, "lt": true, "hu": true, "nl": true, "no": true, "pl": true,
		"pt-BR": true, "ro": true, "fi": true, "sv-SE": true, "vi": true, "tr": true, "cs": true, "el": true,
		"bg": true, "ru": true, "uk": true, "hi": true, "th": true, "zh-CN": true, "ja": true, "zh-TW": true, "ko": true,
	}
)

// mergeFeatures applies a Modify Guild `features` list: only mutable flags can be added
// or removed, the other live flags are kept.
func mergeFeatures(live, req []string) ([]string, error) {
	want := map[string]bool{}
	for _, f := range req {
		if !discord.IsMutableFeature(f) && !slices.Contains(live, f) {
			return nil, badRequest("feature %s cannot be set (mutable features: %s)", f, strings.Join(discord.MutableFeatures, ", "))
		}
		want[f] = true
	}
	if want[discord.FeatureDiscoverable] && !want[discord.FeatureCommunity] {
		return nil, badRequest("DISCOVERABLE requires the Community feature")
	}
	out := []string{}
	for _, f := range live {
		if !discord.IsMutableFeature(f) || want[f] {
			out = append(out, f)
		}
	}
	for _, f := range discord.MutableFeatures {
		if want[f] && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out, nil
}

func addFeature(fs []string, f string) []string {
	if slices.Contains(fs, f) {
		return fs
	}
	return append(fs, f)
}

func removeFeature(fs []string, rm ...string) []string {
	out := fs[:0:0]
	for _, f := range fs {
		if !slices.Contains(rm, f) {
			out = append(out, f)
		}
	}
	return out
}

func cloneServer(s discord.Server) discord.Server {
	s.Features = append([]string{}, s.Features...)
	return s
}

func isGIF(b []byte) bool { return len(b) >= 4 && string(b[:4]) == "GIF8" }

// cdnURL mimics a Discord CDN image URL ("a_" hash prefix for animated images).
func cdnURL(kind, id string, b []byte) string {
	if isGIF(b) {
		return "https://cdn.discordapp.com/" + kind + "/" + id + "/a_" + imageHash(b) + ".gif"
	}
	return "https://cdn.discordapp.com/" + kind + "/" + id + "/" + imageHash(b) + ".png"
}

func imageHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

// ───────────────────────────────────────────────────────────── roles

func (w *World) listRoles(a actor, serverID string) ([]discord.Role, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if s.members[a.userID] == nil {
		return nil, forbidden("missing access to server %s", serverID)
	}
	return w.sortedRoles(s), nil
}

// sortedRoles returns the roles by ascending position (@everyone first).
func (w *World) sortedRoles(s *serverState) []discord.Role {
	out := make([]discord.Role, 0, len(s.roles))
	for _, r := range s.roles {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return snowLess(out[i].ID, out[j].ID)
	})
	return out
}

// Holographic role style: the only accepted tertiary color, with these exact values.
const (
	holoPrimary   = 11127295
	holoSecondary = 16759788
	holoTertiary  = 16761760
)

func validateRoleParams(info discord.Server, p discord.RoleParams) error {
	if p.Name != nil {
		if n := utf8.RuneCountInString(*p.Name); n < 1 || n > 100 {
			return badRequest("role name must be 1-100 characters")
		}
	}
	inRange := func(c int) bool { return c >= 0 && c <= 0xFFFFFF }
	if p.Color != nil && !inRange(*p.Color) {
		return badRequest("role color out of range")
	}
	if c := p.Colors; c != nil {
		if !inRange(c.Primary) || !inRange(c.Secondary) || !inRange(c.Tertiary) {
			return badRequest("role color out of range")
		}
		if c.Secondary != 0 || c.Tertiary != 0 {
			if r := info.BoostReason("a gradient role color", discord.FeatureEnhancedRoleColors); r != "" {
				return badRequest("%s (ENHANCED_ROLE_COLORS feature)", r)
			}
		}
		if c.Tertiary != 0 && (c.Primary != holoPrimary || c.Secondary != holoSecondary || c.Tertiary != holoTertiary) {
			return badRequest("tertiary_color is only valid for the holographic style (primary %d, secondary %d, tertiary %d)", holoPrimary, holoSecondary, holoTertiary)
		}
	}
	if (p.Icon != nil && len(*p.Icon) > 0) || (p.UnicodeEmoji != nil && *p.UnicodeEmoji != "") {
		if r := info.BoostReason("a role icon", discord.FeatureRoleIcons); r != "" {
			return badRequest("%s (ROLE_ICONS feature)", r)
		}
	}
	if p.Icon != nil && len(*p.Icon) > discord.MaxEmojiImageBytes {
		return badRequest("role icon must be at most 256 KiB")
	}
	return nil
}

func applyRoleParams(r *discord.Role, p discord.RoleParams) {
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.Color != nil {
		r.Color = *p.Color
		r.Colors = discord.RoleColors{Primary: *p.Color}
	}
	if p.Colors != nil {
		r.Colors = *p.Colors
		r.Color = p.Colors.Primary
	}
	if p.Hoist != nil {
		r.Hoist = *p.Hoist
	}
	if p.Mentionable != nil {
		r.Mentionable = *p.Mentionable
	}
	if p.Permissions != nil {
		r.Permissions = *p.Permissions
	}
	// An image icon and an emoji icon are exclusive (the Discord client shows one).
	if p.Icon != nil {
		if len(*p.Icon) == 0 {
			r.IconURL = ""
		} else {
			r.IconURL = cdnURL("role-icons", r.ID, *p.Icon)
			r.UnicodeEmoji = ""
		}
	}
	if p.UnicodeEmoji != nil {
		r.UnicodeEmoji = *p.UnicodeEmoji
		if *p.UnicodeEmoji != "" {
			r.IconURL = ""
		}
	}
}

// checkGrant enforces "you cannot grant permissions you do not have".
func (w *World) checkGrant(a actor, s *serverState, added discord.Permissions) error {
	have := w.basePerms(s, a.userID)
	if have == allPermissions || added&^have == 0 {
		return nil
	}
	return forbidden("cannot grant permissions you do not have: %s", strings.Join((added&^have).Names(), ", "))
}

func (w *World) createRole(a actor, serverID string, p discord.RoleParams, reason string) (discord.Role, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Role, error) {
		return discord.Role{}, w.record(a, "CreateRole", serverID, "", reason, err)
	}
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permManageRoles); err != nil {
		return fail(err)
	}
	if err := validateRoleParams(s.info, p); err != nil {
		return fail(err)
	}
	r := &discord.Role{Name: "new role", Permissions: s.roles[s.info.ID].Permissions}
	applyRoleParams(r, p)
	if p.Permissions != nil {
		if err := w.checkGrant(a, s, *p.Permissions); err != nil {
			return fail(err)
		}
	}
	r.ID = w.newID()
	if r.IconURL != "" {
		r.IconURL = w.cdnPut(cdnURL("role-icons", r.ID, *p.Icon), *p.Icon)
	}
	snap := snapshotRoles(s)
	s.roles[r.ID] = r
	// New roles are created at position 1, just above @everyone.
	w.layoutRoles(s, map[string]int{r.ID: 1}, []string{r.ID})
	w.recordOK(a, "CreateRole", serverID, r.ID, reason)
	w.emit(discord.RoleCreateEvent{ServerID: serverID, Role: *r})
	w.emitRoleChanges(s, snap, r.ID)
	return *r, nil
}

func (w *World) updateRole(a actor, serverID, roleID string, p discord.RoleParams, reason string) (discord.Role, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Role, error) {
		return discord.Role{}, w.record(a, "UpdateRole", serverID, roleID, reason, err)
	}
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permManageRoles); err != nil {
		return fail(err)
	}
	r := s.roles[roleID]
	if r == nil {
		return fail(notFound("unknown role %s", roleID))
	}
	if err := w.requireAbove(a, s, r.Position, "role "+r.Name); err != nil {
		return fail(err)
	}
	if err := validateRoleParams(s.info, p); err != nil {
		return fail(err)
	}
	if roleID == serverID && p.Name != nil && *p.Name != r.Name {
		return fail(badRequest("cannot rename @everyone"))
	}
	if p.Permissions != nil {
		if err := w.checkGrant(a, s, *p.Permissions&^r.Permissions); err != nil {
			return fail(err)
		}
	}
	before := *r
	applyRoleParams(r, p)
	if p.Icon != nil && len(*p.Icon) > 0 {
		w.cdnPut(r.IconURL, *p.Icon)
	}
	w.recordOK(a, "UpdateRole", serverID, roleID, reason)
	var changes []discord.AuditLogChange
	if before.Permissions != r.Permissions {
		changes = append(changes, discord.AuditLogChange{Key: "permissions", Old: auditValue(fmt.Sprint(uint64(before.Permissions))), New: auditValue(fmt.Sprint(uint64(r.Permissions)))})
	}
	if before.Name != r.Name {
		changes = append(changes, discord.AuditLogChange{Key: "name", Old: auditValue(before.Name), New: auditValue(r.Name)})
	}
	w.auditWithReason(serverID, a.userID, 31, roleID, a.auditReason(reason), changes...)
	w.emit(discord.RoleUpdateEvent{ServerID: serverID, Role: *r})
	return *r, nil
}

func (w *World) deleteRole(a actor, serverID, roleID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) error { return w.record(a, "DeleteRole", serverID, roleID, reason, err) }
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permManageRoles); err != nil {
		return fail(err)
	}
	r := s.roles[roleID]
	if r == nil {
		return fail(notFound("unknown role %s", roleID))
	}
	if roleID == serverID {
		return fail(badRequest("cannot delete @everyone"))
	}
	if r.Managed {
		return fail(forbidden("cannot delete managed role %s", r.Name))
	}
	if err := w.requireAbove(a, s, r.Position, "role "+r.Name); err != nil {
		return fail(err)
	}
	w.recordOK(a, "DeleteRole", serverID, roleID, reason)
	w.removeRole(s, roleID)
	return nil
}

// removeRole deletes a role, strips it from members and overwrites and emits events.
func (w *World) removeRole(s *serverState, roleID string) {
	snap := snapshotRoles(s)
	delete(s.roles, roleID)
	delete(s.managedBy, roleID)
	for _, m := range s.members {
		m.RoleIDs = removeString(m.RoleIDs, roleID)
	}
	w.layoutRoles(s, nil, nil)
	w.forgetRole(s, roleID)
	w.emit(discord.RoleDeleteEvent{ServerID: s.info.ID, RoleID: roleID})
	w.emitRoleChanges(s, snap, "")
	for _, id := range s.channelIDs {
		c := w.channels[id]
		n := len(c.ch.Overwrites)
		kept := c.ch.Overwrites[:0:0]
		for _, ow := range c.ch.Overwrites {
			if ow.Type != discord.OverwriteRole || ow.ID != roleID {
				kept = append(kept, ow)
			}
		}
		if len(kept) != n {
			c.ch.Overwrites = kept
			w.emit(discord.ChannelUpdateEvent{Channel: c.ch})
		}
	}
}

func (w *World) setRolePositions(a actor, serverID string, positions []discord.RolePosition) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) error { return w.record(a, "SetRolePositions", serverID, "", "", err) }
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permManageRoles); err != nil {
		return fail(err)
	}
	top := w.highest(s, a.userID)
	requested := map[string]int{}
	var order []string
	for _, rp := range positions {
		r := s.roles[rp.ID]
		if r == nil {
			return fail(notFound("unknown role %s", rp.ID))
		}
		if rp.ID == serverID {
			return fail(badRequest("cannot move @everyone"))
		}
		if _, dup := requested[rp.ID]; dup {
			return fail(badRequest("duplicate role %s", rp.ID))
		}
		if r.Position >= top || rp.Position >= top {
			return fail(forbidden("cannot move role %s at or above your highest role", r.Name))
		}
		requested[rp.ID] = rp.Position
		order = append(order, rp.ID)
	}
	snap := snapshotRoles(s)
	w.layoutRoles(s, requested, order)
	w.recordOK(a, "SetRolePositions", serverID, "", "")
	w.emitRoleChanges(s, snap, "")
	return nil
}

// layoutRoles normalizes role positions to 1..n (@everyone stays 0). requested roles
// land exactly on their requested position (clamped); the others keep their order.
func (w *World) layoutRoles(s *serverState, requested map[string]int, order []string) {
	var ids []string
	for id := range s.roles {
		if id != s.info.ID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.roles[ids[i]], s.roles[ids[j]]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return snowLess(a.ID, b.ID)
	})
	req := make(map[string]int, len(requested))
	for id, p := range requested {
		req[id] = p - 1 // slot index
	}
	for i, id := range placeExact(ids, req, order) {
		s.roles[id].Position = i + 1
	}
	s.roles[s.info.ID].Position = 0
}

// placeExact puts requested IDs on their slot (clamped, nearest free slot on conflict)
// and fills remaining slots with the other IDs in their current order.
func placeExact(ids []string, requested map[string]int, order []string) []string {
	n := len(ids)
	slots := make([]string, n)
	used := make([]bool, n)
	inGroup := make(map[string]bool, n)
	for _, id := range ids {
		inGroup[id] = true
	}
	var moved []string
	for _, id := range order {
		if _, ok := requested[id]; ok && inGroup[id] {
			moved = append(moved, id)
		}
	}
	sort.SliceStable(moved, func(i, j int) bool { return requested[moved[i]] < requested[moved[j]] })
	isMoved := map[string]bool{}
	for _, id := range moved {
		p := min(max(requested[id], 0), n-1)
		slot := -1
		for d := 0; d < n && slot < 0; d++ {
			if p+d < n && !used[p+d] {
				slot = p + d
			} else if p-d >= 0 && !used[p-d] {
				slot = p - d
			}
		}
		slots[slot], used[slot] = id, true
		isMoved[id] = true
	}
	next := 0
	for _, id := range ids {
		if isMoved[id] {
			continue
		}
		for used[next] {
			next++
		}
		slots[next], used[next] = id, true
	}
	return slots
}

func snapshotRoles(s *serverState) map[string]int {
	m := make(map[string]int, len(s.roles))
	for id, r := range s.roles {
		m[id] = r.Position
	}
	return m
}

// emitRoleChanges emits RoleUpdate for roles whose position changed since snap.
func (w *World) emitRoleChanges(s *serverState, snap map[string]int, skip string) {
	for _, r := range w.sortedRoles(s) {
		if old, ok := snap[r.ID]; ok && old != r.Position && r.ID != skip {
			w.emit(discord.RoleUpdateEvent{ServerID: s.info.ID, Role: r})
		}
	}
}

func removeString(in []string, v string) []string {
	out := in[:0:0]
	for _, s := range in {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}

// ───────────────────────────────────────────────────────────── channels

var creatableTypes = map[discord.ChannelType]bool{
	discord.ChannelText: true, discord.ChannelVoice: true, discord.ChannelCategory: true,
	discord.ChannelAnnouncement: true, discord.ChannelStage: true, discord.ChannelForum: true,
	discord.ChannelMedia: true,
}

func textLike(t discord.ChannelType) bool {
	switch t {
	case discord.ChannelText, discord.ChannelAnnouncement, discord.ChannelForum, discord.ChannelMedia:
		return true
	}
	return false
}

func voiceLike(t discord.ChannelType) bool {
	return t == discord.ChannelVoice || t == discord.ChannelStage
}

// normalizeChannelName mimics Discord for text-like channels: lowercase, whitespace
// runs replaced by a single "-".
func normalizeChannelName(t discord.ChannelType, name string) string {
	if !textLike(t) {
		return name
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsSpace(r) {
			if !dash {
				b.WriteRune('-')
				dash = true
			}
			continue
		}
		dash = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func isThreadParentable(t discord.ChannelType) bool {
	return t == discord.ChannelText || t == discord.ChannelAnnouncement || t == discord.ChannelForum || t == discord.ChannelMedia
}

func groupKey(c discord.Channel) string {
	switch {
	case c.Type == discord.ChannelCategory:
		return "category"
	case voiceLike(c.Type):
		return c.ParentID + "/voice"
	default:
		return c.ParentID + "/text"
	}
}

func (w *World) listChannels(a actor, serverID string) ([]discord.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if s.members[a.userID] == nil {
		return nil, forbidden("missing access to server %s", serverID)
	}
	all := w.serverChannels(s, false)
	if !w.hideUnviewable || !a.isBot() {
		return all, nil
	}
	out := all[:0]
	for _, c := range all {
		if w.channelPerms(w.channels[c.ID], a.userID).Has(permView) {
			out = append(out, c)
		}
	}
	return out, nil
}

// serverChannels returns the channels in Discord client order: uncategorized channels
// first, then each category followed by its children (text before voice).
func (w *World) serverChannels(s *serverState, threads bool) []discord.Channel {
	var top, cats []discord.Channel
	children := map[string][]discord.Channel{}
	var ths []discord.Channel
	for _, id := range s.channelIDs {
		c := cloneChannel(w.channels[id].ch)
		switch {
		case isThread(c.Type):
			ths = append(ths, c)
		case c.Type == discord.ChannelCategory:
			cats = append(cats, c)
		case c.ParentID == "":
			top = append(top, c)
		default:
			children[c.ParentID] = append(children[c.ParentID], c)
		}
	}
	less := func(list []discord.Channel) func(i, j int) bool {
		return func(i, j int) bool {
			a, b := list[i], list[j]
			if va, vb := voiceLike(a.Type), voiceLike(b.Type); va != vb {
				return !va
			}
			if a.Position != b.Position {
				return a.Position < b.Position
			}
			return snowLess(a.ID, b.ID)
		}
	}
	sort.Slice(top, less(top))
	sort.Slice(cats, less(cats))
	out := append([]discord.Channel{}, top...)
	for _, cat := range cats {
		out = append(out, cat)
		ch := children[cat.ID]
		sort.Slice(ch, less(ch))
		out = append(out, ch...)
	}
	if threads {
		out = append(out, ths...)
	}
	return out
}

func (w *World) channel(channelID string) (*channelState, error) {
	c := w.channels[channelID]
	if c == nil {
		return nil, notFound("unknown channel %s", channelID)
	}
	return c, nil
}

func (w *World) getChannel(a actor, channelID string) (discord.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, err := w.channel(channelID)
	if err != nil {
		return discord.Channel{}, err
	}
	if err := w.requireChannel(a, c, 0); err != nil {
		return discord.Channel{}, err
	}
	return cloneChannel(c.ch), nil
}

// checkOverwrites validates overwrites and the actor's right to change them from old to
// new: ManageRoles is required, only permissions the actor has can be allowed or denied,
// and only administrators can put ManageRoles in an overwrite.
func (w *World) checkOverwrites(a actor, s *serverState, old, ows []discord.Overwrite) error {
	seen := map[string]bool{}
	for _, ow := range ows {
		if seen[ow.ID] {
			return badRequest("duplicate overwrite %s", ow.ID)
		}
		seen[ow.ID] = true
		switch ow.Type {
		case discord.OverwriteRole:
			if s.roles[ow.ID] == nil {
				return badRequest("overwrite for unknown role %s", ow.ID)
			}
		case discord.OverwriteMember:
			if w.users[ow.ID] == nil {
				return badRequest("overwrite for unknown user %s", ow.ID)
			}
		default:
			return badRequest("invalid overwrite type %d", ow.Type)
		}
	}
	oldBy := map[string]discord.Overwrite{}
	for _, ow := range old {
		oldBy[ow.ID] = ow
	}
	var changed discord.Permissions
	for _, ow := range ows {
		o := oldBy[ow.ID]
		changed |= (o.Allow ^ ow.Allow) | (o.Deny ^ ow.Deny)
		if _, ok := oldBy[ow.ID]; ok && o.Type != ow.Type {
			changed |= ow.Allow | ow.Deny
		}
		delete(oldBy, ow.ID)
	}
	removed := len(oldBy) > 0
	for _, o := range oldBy {
		changed |= o.Allow | o.Deny
	}
	if changed == 0 && !removed && len(ows) == len(old) && sameOverwriteIDs(old, ows) {
		return nil
	}
	have := w.basePerms(s, a.userID)
	if !have.Has(permManageRoles) {
		return missingErr(have, permManageRoles)
	}
	if have == allPermissions {
		return nil
	}
	if changed&permManageRoles != 0 {
		return forbidden("only administrators can set ManageRoles in channel overwrites")
	}
	if changed&^have != 0 {
		return forbidden("cannot allow or deny permissions you do not have: %s", strings.Join((changed&^have).Names(), ", "))
	}
	return nil
}

func sameOverwriteIDs(a, b []discord.Overwrite) bool {
	ids := map[string]bool{}
	for _, o := range a {
		ids[o.ID] = true
	}
	for _, o := range b {
		if !ids[o.ID] {
			return false
		}
	}
	return true
}

func (w *World) validateParent(s *serverState, t discord.ChannelType, parentID string) error {
	if parentID == "" {
		return nil
	}
	if t == discord.ChannelCategory {
		return badRequest("categories cannot have a parent")
	}
	p := w.channels[parentID]
	if p == nil || p.ch.ServerID != s.info.ID || p.ch.Type != discord.ChannelCategory {
		return badRequest("parent %s is not a category of the server", parentID)
	}
	return nil
}

func forumLike(t discord.ChannelType) bool {
	return t == discord.ChannelForum || t == discord.ChannelMedia
}

// voiceRegions are Discord's voice region IDs (GET /voice/regions).
var voiceRegions = map[string]bool{
	"brazil": true, "hongkong": true, "india": true, "japan": true, "rotterdam": true, "russia": true,
	"singapore": true, "southafrica": true, "south-korea": true, "sydney": true, "us-central": true,
	"us-east": true, "us-south": true, "us-west": true, "dubai": true, "atlanta": true, "newark": true,
	"santa-clara": true, "seattle": true, "stockholm": true, "frankfurt": true, "madrid": true,
	"milan": true, "bucharest": true, "finland": true, "tel-aviv": true, "buenos-aires": true,
	"santiago": true, "st-pete": true, "montreal": true,
}

// applyChannelFields applies the type-specific fields of p to ch, validating them.
// Fields that do not apply to the channel type are ignored, like Discord.
func (w *World) applyChannelFields(ch *discord.Channel, p discord.ChannelParams) error {
	if p.Name != nil {
		name := normalizeChannelName(ch.Type, *p.Name)
		if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
			return badRequest("channel name must be 1-100 characters")
		}
		ch.Name = name
	}
	if p.Topic != nil && textLike(ch.Type) {
		maxTopic := 1024
		if forumLike(ch.Type) {
			maxTopic = 4096
		}
		if utf8.RuneCountInString(*p.Topic) > maxTopic {
			return badRequest("topic must be at most %d characters", maxTopic)
		}
		ch.Topic = *p.Topic
	}
	if p.NSFW != nil && ch.Type != discord.ChannelCategory {
		ch.NSFW = *p.NSFW
	}
	if p.RateLimitPerUser != nil && ch.Type != discord.ChannelCategory {
		if *p.RateLimitPerUser < 0 || *p.RateLimitPerUser > 21600 {
			return badRequest("rate limit per user must be 0-21600")
		}
		ch.RateLimitPerUser = *p.RateLimitPerUser
	}
	if p.Bitrate != nil && voiceLike(ch.Type) {
		if *p.Bitrate < 8000 || *p.Bitrate > 384000 {
			return badRequest("bitrate must be 8000-384000")
		}
		ch.Bitrate = *p.Bitrate
	}
	if p.UserLimit != nil && voiceLike(ch.Type) {
		maxLimit := 99
		if ch.Type == discord.ChannelStage {
			maxLimit = 10000
		}
		if *p.UserLimit < 0 || *p.UserLimit > maxLimit {
			return badRequest("user limit must be 0-%d", maxLimit)
		}
		ch.UserLimit = *p.UserLimit
	}
	if p.RTCRegion != nil && voiceLike(ch.Type) {
		if *p.RTCRegion != "" && !voiceRegions[*p.RTCRegion] {
			return badRequest("unknown voice region %q", *p.RTCRegion)
		}
		ch.RTCRegion = *p.RTCRegion
	}
	if p.VideoQuality != nil && voiceLike(ch.Type) {
		if *p.VideoQuality != "auto" && *p.VideoQuality != "full" {
			return badRequest("video quality mode must be auto or full")
		}
		ch.VideoQuality = *p.VideoQuality
	}
	if p.DefaultAutoArchive != nil && isThreadParentable(ch.Type) {
		if !discord.ValidAutoArchive(*p.DefaultAutoArchive) {
			return badRequest("default auto archive duration must be one of %s minutes", discord.ThreadArchiveDescription)
		}
		ch.DefaultAutoArchive = *p.DefaultAutoArchive
	}
	if p.DefaultThreadSlowmode != nil && isThreadParentable(ch.Type) {
		if *p.DefaultThreadSlowmode < 0 || *p.DefaultThreadSlowmode > 21600 {
			return badRequest("default thread rate limit per user must be 0-21600")
		}
		ch.DefaultThreadSlowmode = *p.DefaultThreadSlowmode
	}
	if p.Tags != nil && forumLike(ch.Type) {
		tags, err := w.forumTags(ch.Tags, *p.Tags)
		if err != nil {
			return err
		}
		ch.Tags = tags
	}
	if p.DefaultReaction != nil && forumLike(ch.Type) {
		ch.DefaultReaction = *p.DefaultReaction
	}
	if p.DefaultSortOrder != nil && forumLike(ch.Type) {
		if *p.DefaultSortOrder != "latestActivity" && *p.DefaultSortOrder != "creationDate" {
			return badRequest("default sort order must be latestActivity or creationDate")
		}
		ch.DefaultSortOrder = *p.DefaultSortOrder
	}
	if p.DefaultLayout != nil && ch.Type == discord.ChannelForum {
		if *p.DefaultLayout != "list" && *p.DefaultLayout != "gallery" {
			return badRequest("default forum layout must be list or gallery")
		}
		ch.DefaultLayout = *p.DefaultLayout
	}
	return nil
}

// forumTags applies a full tag list: entries with an ID keep that tag, the others are
// created with a new ID.
func (w *World) forumTags(live, want []discord.ForumTag) ([]discord.ForumTag, error) {
	if len(want) > discord.ForumMaxTags {
		return nil, badRequest("a channel can have at most %d tags", discord.ForumMaxTags)
	}
	known := map[string]bool{}
	for _, t := range live {
		known[t.ID] = true
	}
	out := make([]discord.ForumTag, 0, len(want))
	seen := map[string]bool{}
	for _, t := range want {
		if n := utf8.RuneCountInString(t.Name); n < 1 || n > discord.ForumMaxTagName {
			return nil, badRequest("tag name must be 1-%d characters", discord.ForumMaxTagName)
		}
		if t.ID != "" {
			if !known[t.ID] || seen[t.ID] {
				return nil, badRequest("unknown tag %s", t.ID)
			}
		} else {
			t.ID = w.newID()
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out, nil
}

func (w *World) createChannel(a actor, serverID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Channel, error) {
		return discord.Channel{}, w.record(a, "CreateChannel", serverID, "", reason, err)
	}
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permManageChannels); err != nil {
		return fail(err)
	}
	if !creatableTypes[p.Type] {
		return fail(badRequest("cannot create a channel of type %s", p.Type))
	}
	if p.Name == nil {
		return fail(badRequest("channel name is required"))
	}
	if (p.Type == discord.ChannelAnnouncement || p.Type == discord.ChannelStage) && !s.info.HasFeature(discord.FeatureCommunity) {
		return fail(badRequest("%s channels require the Community feature", p.Type))
	}
	ch := discord.Channel{ServerID: serverID, Type: p.Type}
	if voiceLike(p.Type) {
		ch.Bitrate = 64000
	}
	if err := w.applyChannelFields(&ch, p); err != nil {
		return fail(err)
	}
	if p.ParentID != nil {
		if err := w.validateParent(s, p.Type, *p.ParentID); err != nil {
			return fail(err)
		}
		ch.ParentID = *p.ParentID
	}
	switch {
	case p.Overwrites != nil:
		if err := w.checkOverwrites(a, s, nil, *p.Overwrites); err != nil {
			return fail(err)
		}
		ch.Overwrites = cloneOverwrites(*p.Overwrites)
	case ch.ParentID != "":
		// Like Discord, a channel created in a category is synced with it.
		ch.Overwrites = cloneOverwrites(w.channels[ch.ParentID].ch.Overwrites)
	}
	ch.ID = w.newID()
	ch.Position = 1 << 30 // end of its group unless a position is requested
	c := &channelState{ch: ch}
	snap := w.snapshotChannels(s)
	w.channels[ch.ID] = c
	s.channelIDs = append(s.channelIDs, ch.ID)
	var req map[string]int
	if p.Position != nil {
		req = map[string]int{ch.ID: *p.Position}
	}
	w.layoutChannels(s, req, []string{ch.ID})
	w.recordOK(a, "CreateChannel", serverID, ch.ID, reason)
	w.emit(discord.ChannelCreateEvent{Channel: c.ch})
	w.emitChannelChanges(s, snap, ch.ID)
	return cloneChannel(c.ch), nil
}

func (w *World) updateChannel(a actor, channelID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	serverID := ""
	fail := func(err error) (discord.Channel, error) {
		return discord.Channel{}, w.record(a, "UpdateChannel", serverID, channelID, reason, err)
	}
	c, err := w.channel(channelID)
	if err != nil {
		return fail(err)
	}
	serverID = c.ch.ServerID
	if c.ch.Type == discord.ChannelDM {
		return fail(badRequest("cannot modify a DM channel"))
	}
	s := w.servers[serverID]
	need := permManageChannels
	if isThread(c.ch.Type) {
		need = discord.MustPermissions("ManageThreads")
	}
	if err := w.requireChannel(a, c, need); err != nil {
		return fail(err)
	}
	ch := cloneChannel(c.ch)
	if err := w.applyChannelFields(&ch, p); err != nil {
		return fail(err)
	}
	if p.ParentID != nil && !isThread(ch.Type) {
		if err := w.validateParent(s, ch.Type, *p.ParentID); err != nil {
			return fail(err)
		}
		ch.ParentID = *p.ParentID
	}
	newOws := ch.Overwrites
	if p.Overwrites != nil {
		newOws = cloneOverwrites(*p.Overwrites)
	}
	if p.LockPermissions != nil && *p.LockPermissions && ch.ParentID != "" {
		newOws = cloneOverwrites(w.channels[ch.ParentID].ch.Overwrites)
	}
	if !isThread(ch.Type) {
		if err := w.checkOverwrites(a, s, c.ch.Overwrites, newOws); err != nil {
			return fail(err)
		}
		ch.Overwrites = newOws
	}
	snap := w.snapshotChannels(s)
	moved := ch.ParentID != c.ch.ParentID
	previousChannel := c.ch
	c.ch = ch
	var req map[string]int
	if p.Position != nil && !isThread(ch.Type) {
		req = map[string]int{ch.ID: *p.Position}
	} else if moved {
		c.ch.Position = 1 << 30
	}
	if !isThread(ch.Type) {
		w.layoutChannels(s, req, []string{ch.ID})
	}
	w.recordOK(a, "UpdateChannel", serverID, channelID, reason)
	var changes []discord.AuditLogChange
	if previousChannel.Name != ch.Name {
		changes = append(changes, discord.AuditLogChange{Key: "name", Old: auditValue(previousChannel.Name), New: auditValue(ch.Name)})
	}
	if p.Overwrites != nil {
		changes = append(changes, discord.AuditLogChange{Key: "permission_overwrites", Old: auditValue(previousChannel.Overwrites), New: auditValue(ch.Overwrites)})
	}
	w.auditWithReason(serverID, a.userID, 11, channelID, a.auditReason(reason), changes...)
	w.emit(discord.ChannelUpdateEvent{Channel: c.ch})
	w.emitChannelChanges(s, snap, ch.ID)
	return cloneChannel(c.ch), nil
}

func (w *World) deleteChannel(a actor, channelID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	serverID := ""
	fail := func(err error) error { return w.record(a, "DeleteChannel", serverID, channelID, reason, err) }
	c, err := w.channel(channelID)
	if err != nil {
		return fail(err)
	}
	serverID = c.ch.ServerID
	if c.ch.Type == discord.ChannelDM {
		return fail(badRequest("cannot delete a DM channel"))
	}
	need := permManageChannels
	if isThread(c.ch.Type) {
		need = discord.MustPermissions("ManageThreads")
	}
	if err := w.requireChannel(a, c, need); err != nil {
		return fail(err)
	}
	w.recordOK(a, "DeleteChannel", serverID, channelID, reason)
	w.removeChannel(w.servers[serverID], c)
	return nil
}

// removeChannel deletes a channel, its threads and messages, uncategorizes children
// and clears server references, emitting the corresponding events.
func (w *World) removeChannel(s *serverState, c *channelState) {
	snap := w.snapshotChannels(s)
	var threads []*channelState
	for _, id := range s.channelIDs {
		o := w.channels[id]
		if o.ch.ParentID == c.ch.ID {
			if isThread(o.ch.Type) {
				threads = append(threads, o)
			} else {
				o.ch.ParentID = ""
				o.ch.Position = 1 << 30
			}
		}
	}
	drop := func(x *channelState) {
		for _, mid := range x.msgIDs {
			delete(w.messages, mid)
		}
		delete(w.channels, x.ch.ID)
		s.channelIDs = removeString(s.channelIDs, x.ch.ID)
	}
	for _, t := range threads {
		drop(t)
	}
	drop(c)
	if voiceLike(c.ch.Type) {
		w.disconnectVoice(s, c.ch.ID) // Discord disconnects the members first
	}
	w.layoutChannels(s, nil, nil)
	w.emit(discord.ChannelDeleteEvent{Channel: c.ch})
	for _, t := range threads {
		w.emit(discord.ChannelDeleteEvent{Channel: t.ch})
	}
	w.emitChannelChanges(s, snap, "")
	cleared := false
	for _, ref := range []*string{&s.info.SystemChannelID, &s.info.RulesChannelID, &s.info.PublicUpdatesChannelID,
		&s.info.SafetyAlertsChannelID, &s.info.AFKChannelID} {
		if *ref == c.ch.ID {
			*ref = ""
			cleared = true
		}
	}
	if s.widget.ChannelID == c.ch.ID {
		s.widget.ChannelID = ""
	}
	w.forgetChannel(s, c.ch.ID)
	if cleared {
		w.emit(discord.ServerUpdateEvent{Server: cloneServer(s.info)})
	}
}

func (w *World) setChannelPositions(a actor, serverID string, positions []discord.ChannelPosition) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) error { return w.record(a, "SetChannelPositions", serverID, "", "", err) }
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permManageChannels); err != nil {
		return fail(err)
	}
	seen := map[string]bool{}
	for _, cp := range positions {
		c := w.channels[cp.ID]
		if c == nil || c.ch.ServerID != serverID || isThread(c.ch.Type) {
			return fail(notFound("unknown channel %s", cp.ID))
		}
		if seen[cp.ID] {
			return fail(badRequest("duplicate channel %s", cp.ID))
		}
		seen[cp.ID] = true
		if cp.ParentID != nil {
			if err := w.validateParent(s, c.ch.Type, *cp.ParentID); err != nil {
				return fail(err)
			}
		}
	}
	snap := w.snapshotChannels(s)
	requested := map[string]int{}
	var order []string
	for _, cp := range positions {
		c := w.channels[cp.ID]
		if cp.ParentID != nil && *cp.ParentID != c.ch.ParentID {
			c.ch.ParentID = *cp.ParentID
			c.ch.Position = 1 << 30
		}
		requested[cp.ID] = cp.Position
		order = append(order, cp.ID)
	}
	w.layoutChannels(s, requested, order)
	w.recordOK(a, "SetChannelPositions", serverID, "", "")
	w.emitChannelChanges(s, snap, "")
	return nil
}

// layoutChannels normalizes positions to 0..k-1 within each sibling group (categories;
// text-like / voice-like channels of the same parent).
func (w *World) layoutChannels(s *serverState, requested map[string]int, order []string) {
	groups := map[string][]string{}
	for _, id := range s.channelIDs {
		c := w.channels[id]
		if isThread(c.ch.Type) {
			continue
		}
		k := groupKey(c.ch)
		groups[k] = append(groups[k], id)
	}
	for _, ids := range groups {
		sort.SliceStable(ids, func(i, j int) bool {
			a, b := w.channels[ids[i]].ch, w.channels[ids[j]].ch
			if a.Position != b.Position {
				return a.Position < b.Position
			}
			return snowLess(a.ID, b.ID)
		})
		for i, id := range placeExact(ids, requested, order) {
			w.channels[id].ch.Position = i
		}
	}
}

type chanSnap struct {
	pos    int
	parent string
}

func (w *World) snapshotChannels(s *serverState) map[string]chanSnap {
	m := make(map[string]chanSnap, len(s.channelIDs))
	for _, id := range s.channelIDs {
		c := w.channels[id].ch
		m[id] = chanSnap{c.Position, c.ParentID}
	}
	return m
}

// emitChannelChanges emits ChannelUpdate for channels whose position or parent changed.
func (w *World) emitChannelChanges(s *serverState, snap map[string]chanSnap, skip string) {
	for _, id := range s.channelIDs {
		c := w.channels[id].ch
		if old, ok := snap[id]; ok && id != skip && (old.pos != c.Position || old.parent != c.ParentID) {
			w.emit(discord.ChannelUpdateEvent{Channel: c})
		}
	}
}

func (w *World) createThread(a actor, channelID string, p discord.ThreadParams) (discord.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	serverID := ""
	fail := func(err error) (discord.Channel, error) {
		return discord.Channel{}, w.record(a, "CreateThread", serverID, channelID, "", err)
	}
	parent, err := w.channel(channelID)
	if err != nil {
		return fail(err)
	}
	serverID = parent.ch.ServerID
	if !isThreadParentable(parent.ch.Type) {
		return fail(badRequest("cannot create a thread in a %s channel", parent.ch.Type))
	}
	need := permPublicThreads
	typ := discord.ChannelPublicThread
	switch {
	case parent.ch.Type == discord.ChannelAnnouncement:
		if p.Private {
			return fail(badRequest("announcement channels cannot have private threads"))
		}
		typ = discord.ChannelAnnouncementThread
	case p.Private:
		if p.MessageID != "" {
			return fail(badRequest("private threads cannot start from a message"))
		}
		need, typ = permPrivateThreads, discord.ChannelPrivateThread
	}
	if err := w.requireChannel(a, parent, need); err != nil {
		return fail(err)
	}
	if n := utf8.RuneCountInString(p.Name); n < 1 || n > 100 {
		return fail(badRequest("thread name must be 1-100 characters"))
	}
	if p.MessageID != "" {
		m := w.messages[p.MessageID]
		if m == nil || m.ephemeral || m.msg.ChannelID != channelID {
			return fail(notFound("unknown message %s", p.MessageID))
		}
	}
	ch := discord.Channel{ID: w.newID(), ServerID: serverID, Name: p.Name, Type: typ, ParentID: channelID}
	if p.MessageID != "" {
		ch.ID = p.MessageID // like Discord, a thread started from a message shares its ID
		if w.channels[ch.ID] != nil {
			return fail(badRequest("a thread has already been created for this message"))
		}
	}
	s := w.servers[serverID]
	w.channels[ch.ID] = &channelState{ch: ch}
	s.channelIDs = append(s.channelIDs, ch.ID)
	w.recordOK(a, "CreateThread", serverID, ch.ID, "")
	w.emit(discord.ChannelCreateEvent{Channel: ch, NewThread: true})
	return ch, nil
}
