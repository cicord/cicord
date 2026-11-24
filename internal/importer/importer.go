// Package importer turns the live state of a Discord server (reconcile.Snapshot) into a
// Server YAML document (servers/<key>.yaml) plus the resource map entries that tie the
// generated keys to the existing Discord IDs (cicord import, ADR-0003).
//
// Keys are derived from display names: accents are stripped, everything else that is
// not [a-z0-9] becomes a dash, a leading digit gets a prefix, and duplicates get a
// numeric suffix. Roles and categories+channels are two key namespaces (categories and
// channels share one, as required by validation).
//
// What is imported: the profile (name, description, verification level, default
// notifications, system/rules channels), @everyone permissions, every role that is not
// managed by an integration (name, color, hoist, mentionable, permissions) with the
// hierarchy, categories and channels (name, type, category, position, topic, nsfw,
// slowmode, bitrate, user limit, permission overwrites; a channel whose overwrites equal
// its category's is left synced), the parity blocks (AutoMod, onboarding, widget,
// welcome screen) and, with a Downloader, the files: the server icon, banner and invite
// splash, role icons, emojis, stickers and soundboard sounds are downloaded from the
// Discord CDN into servers/assets/<key>/ (Result.Files) and referenced from the YAML;
// Result.RecordFiles records them for the reconciler so that the first plan is empty.
// Not imported: threads, channel types Cicord does not manage, managed emojis, files over
// Discord's upload limits, and overwrites targeting managed roles (they cannot be
// referenced by key: a warning is returned for each).
package importer

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"

	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/reconcile"
	"github.com/npjonath/cicord/internal/state"
)

// Options configures Generate.
type Options struct {
	// ServerKey is the key of the generated server (metadata.name); default: derived
	// from the server name.
	ServerKey string
	// Snapshot is the live state (reconcile.Fetch).
	Snapshot reconcile.Snapshot
	// Download fetches the files (server and role images, expressions) from the
	// Discord CDN. nil: they are not imported (a warning says what to export by hand).
	Download Downloader
}

// Result is a generated server.
type Result struct {
	Key string
	// YAML is the servers/<Key>.yaml document.
	YAML []byte
	// Server is the decoded desired state (same content as YAML).
	Server config.ServerSpec
	// Resources are the resource map entries (owner git) to record.
	Resources []state.Resource
	// Files are the downloaded files the YAML references (to write into the
	// repository, then record with RecordFiles).
	Files []File
	// Warnings describe what could not be imported.
	Warnings []string
}

// defaultBitrate is Discord's default voice bitrate: not written to keep files short.
const defaultBitrate = 64000

// Generate builds the Server YAML of a live snapshot (ctx bounds the downloads).
func Generate(ctx context.Context, o Options) (*Result, error) {
	snap := o.Snapshot
	srv := snap.Server
	if srv.ID == "" {
		return nil, fmt.Errorf("importer: empty snapshot")
	}
	key := o.ServerKey
	if key == "" {
		key = Slug(srv.Name, "server")
	}
	if !KeyPattern(key) {
		return nil, fmt.Errorf("importer: %q is not a valid server key", key)
	}
	g := &gen{res: &Result{Key: key}, serverID: srv.ID, roleKeys: map[string]string{}, chanKeys: map[string]string{},
		roleNS: newNamespace(), chanNS: newNamespace(), snap: snap, ctx: ctx, dl: o.Download}
	g.importRoles()
	g.importChannels()
	g.importProfile()
	g.importParity()
	g.importExpressions()
	sp := g.spec()
	g.res.Server = sp
	doc, err := g.document(sp)
	if err != nil {
		return nil, err
	}
	g.res.YAML = doc
	return g.res, nil
}

type gen struct {
	res      *Result
	serverID string
	snap     reconcile.Snapshot
	ctx      context.Context
	dl       Downloader

	roleNS, chanNS *namespace
	roleKeys       map[string]string // role ID → key
	chanKeys       map[string]string // channel/category ID → key

	everyone   []string
	roleOrder  []string // keys, top first
	roles      map[string]config.RoleSpec
	catOrder   []string
	categories map[string]config.Category
	chanOrder  []string
	channels   map[string]config.Channel
	profileV   *config.ServerProfile

	afk           *config.AFKSpec
	widget        *config.WidgetSpec
	community     *config.CommunitySpec
	automodOrder  []string
	automod       map[string]config.AutoModSpec
	onboarding    *config.OnboardingSpec
	welcomeScreen *config.WelcomeScreenSpec

	emojiOrder, stickerOrder, soundOrder []string
	emojis                               map[string]config.EmojiSpec
	stickers                             map[string]config.StickerSpec
	sounds                               map[string]config.SoundboardSpec
}

func (g *gen) warn(format string, args ...any) {
	g.res.Warnings = append(g.res.Warnings, fmt.Sprintf(format, args...))
}

func (g *gen) record(kind state.ResourceKind, key, id string) {
	g.res.Resources = append(g.res.Resources, state.Resource{Server: g.res.Key, Kind: kind, Key: key, ID: id, Owner: state.OwnerGit})
}

func (g *gen) importRoles() {
	roles := append([]discord.Role(nil), g.snap.Roles...)
	sort.SliceStable(roles, func(i, j int) bool {
		if roles[i].Position != roles[j].Position {
			return roles[i].Position > roles[j].Position
		}
		return idLess(roles[i].ID, roles[j].ID)
	})
	g.roles = map[string]config.RoleSpec{}
	g.everyone = []string{}
	for _, r := range roles {
		if r.IsEveryone(g.serverID) {
			g.everyone = r.Permissions.Names()
			continue
		}
		if r.Managed {
			g.warn("role %q (%s) is managed by an integration or bot: not imported", r.Name, r.ID)
			continue
		}
		k := g.roleNS.take(Slug(r.Name, "role"))
		g.roleKeys[r.ID] = k
		g.roleOrder = append(g.roleOrder, k)
		spec := config.RoleSpec{Name: r.Name, Hoist: r.Hoist, Mentionable: r.Mentionable, Permissions: r.Permissions.Names()}
		if r.Color != 0 {
			spec.Color = fmt.Sprintf("#%06x", r.Color)
		}
		if c := r.Colors; c.Secondary != 0 || c.Tertiary != 0 {
			rc := &config.RoleColors{Primary: fmt.Sprintf("#%06x", c.Primary), Secondary: fmt.Sprintf("#%06x", c.Secondary)}
			if c.Tertiary != 0 {
				rc.Tertiary = fmt.Sprintf("#%06x", c.Tertiary)
			}
			spec.Colors = rc
		}
		if r.UnicodeEmoji != "" {
			spec.Emoji = r.UnicodeEmoji
		} else if r.IconURL != "" {
			spec.Icon = g.download("role:"+k, r.IconURL, "roles/"+k, maxRoleIcon, sniffImage,
				fmt.Sprintf("the icon of role %q", r.Name), "roles."+k+".icon")
		}
		g.roles[k] = spec
		g.record(state.KindRole, k, r.ID)
	}
}

func managedType(t discord.ChannelType) bool {
	switch t {
	case discord.ChannelText, discord.ChannelVoice, discord.ChannelAnnouncement, discord.ChannelStage,
		discord.ChannelForum, discord.ChannelMedia:
		return true
	}
	return false
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

func (g *gen) importChannels() {
	var cats, chans []discord.Channel
	for _, c := range g.snap.Channels {
		switch {
		case c.Type == discord.ChannelCategory:
			cats = append(cats, c)
		case managedType(c.Type):
			chans = append(chans, c)
		case c.Type == discord.ChannelAnnouncementThread || c.Type == discord.ChannelPublicThread || c.Type == discord.ChannelPrivateThread:
			// threads are never managed
		default:
			g.warn("channel %q (%s) has a type Cicord does not manage (%s): not imported", c.Name, c.ID, c.Type)
		}
	}
	byPos := func(s []discord.Channel) {
		sort.SliceStable(s, func(i, j int) bool {
			if s[i].Position != s[j].Position {
				return s[i].Position < s[j].Position
			}
			return idLess(s[i].ID, s[j].ID)
		})
	}
	byPos(cats)
	g.categories = map[string]config.Category{}
	catOws := map[string]map[string]config.OverwriteSpec{} // category ID → converted overwrites
	for i, c := range cats {
		k := g.chanNS.take(Slug(c.Name, "category"))
		g.chanKeys[c.ID] = k
		g.catOrder = append(g.catOrder, k)
		pos := i
		ows := g.overwrites(c.Overwrites, "category "+strconv.Quote(c.Name))
		catOws[c.ID] = ows
		g.categories[k] = config.Category{Name: c.Name, Position: &pos, Overwrites: ows}
		g.record(state.KindCategory, k, c.ID)
	}

	// channels: grouped by parent (no category first, then category order), text-like
	// before voice-like inside a parent, live order inside each group
	parentRank := map[string]int{"": -1}
	for i, c := range cats {
		parentRank[c.ID] = i
	}
	known := func(parent string) string {
		if _, ok := parentRank[parent]; ok {
			return parent
		}
		return ""
	}
	sort.SliceStable(chans, func(i, j int) bool {
		a, b := chans[i], chans[j]
		pa, pb := parentRank[known(a.ParentID)], parentRank[known(b.ParentID)]
		if pa != pb {
			return pa < pb
		}
		if voiceLike(a.Type) != voiceLike(b.Type) {
			return !voiceLike(a.Type)
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return idLess(a.ID, b.ID)
	})
	g.channels = map[string]config.Channel{}
	next := map[string]int{}
	for _, c := range chans {
		parent := known(c.ParentID)
		if parent == "" && c.ParentID != "" {
			g.warn("channel %q (%s) belongs to an unknown category %s: imported without category", c.Name, c.ID, c.ParentID)
		}
		k := g.chanNS.take(Slug(c.Name, "channel"))
		g.chanKeys[c.ID] = k
		g.chanOrder = append(g.chanOrder, k)
		pos := next[parent]
		next[parent]++
		ch := config.Channel{Name: c.Name, Type: c.Type.String(), Position: &pos, NSFW: c.NSFW}
		if parent != "" {
			ch.Category = g.chanKeys[parent]
		}
		if textLike(c.Type) {
			ch.Topic = c.Topic
		}
		if c.Type != discord.ChannelAnnouncement && c.RateLimitPerUser > 0 {
			ch.Slowmode = c.RateLimitPerUser
		}
		if voiceLike(c.Type) {
			if c.Bitrate > 0 && c.Bitrate != defaultBitrate {
				ch.Bitrate = c.Bitrate
			}
			ch.UserLimit = c.UserLimit
		}
		importChannelExtras(c, &ch)
		ows := g.overwrites(c.Overwrites, "channel "+strconv.Quote(c.Name))
		if parent != "" {
			parentOws := catOws[parent]
			switch {
			case sameOverwrites(ows, parentOws):
				// synced with its category: leave the default (syncWithCategory: true)
			case len(ows) == 0:
				f := false
				ch.SyncWithCategory = &f
			default:
				ch.Overwrites = ows
			}
		} else {
			ch.Overwrites = ows
		}
		g.channels[k] = ch
		g.record(state.KindChannel, k, c.ID)
	}
}

func (g *gen) overwrites(ows []discord.Overwrite, where string) map[string]config.OverwriteSpec {
	out := map[string]config.OverwriteSpec{}
	for _, o := range ows {
		if o.Allow == 0 && o.Deny == 0 {
			continue
		}
		var target string
		switch {
		case o.Type == discord.OverwriteMember:
			target = "member:" + o.ID
		case o.ID == g.serverID:
			target = "@everyone"
		default:
			k, ok := g.roleKeys[o.ID]
			if !ok {
				name := o.ID
				for _, r := range g.snap.Roles {
					if r.ID == o.ID {
						name = strconv.Quote(r.Name)
					}
				}
				g.warn("%s: overwrite for role %s cannot be imported (managed or unknown role); applying this file removes it", where, name)
				continue
			}
			target = k
		}
		out[target] = config.OverwriteSpec{Allow: o.Allow.Names(), Deny: o.Deny.Names()}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sameOverwrites(a, b map[string]config.OverwriteSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !sameSet(av.Allow, bv.Allow) || !sameSet(av.Deny, bv.Deny) {
			return false
		}
	}
	return true
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	for _, s := range b {
		if !m[s] {
			return false
		}
	}
	return true
}

func (g *gen) importProfile() {
	s := g.snap.Server
	p := &config.ServerProfile{VerificationLevel: s.VerificationLevel, DefaultNotifications: s.DefaultNotifications}
	if n := []rune(s.Name); len(n) >= 2 && len(n) <= 100 {
		p.Name = s.Name
	}
	if d := s.Description; d != "" {
		if len([]rune(d)) <= 120 {
			p.Description = d
		} else {
			g.warn("server description is longer than 120 characters: not imported")
		}
	}
	if s.SystemChannelID != "" {
		if k, ok := g.chanKeys[s.SystemChannelID]; ok && g.channels[k].Name != "" {
			p.SystemChannel = k
		}
	}
	chanKey := func(id string) string {
		if k, ok := g.chanKeys[id]; ok && g.channels[k].Name != "" {
			return k
		}
		return ""
	}
	p.ExplicitContentFilter = s.ExplicitContentFilter
	p.PreferredLocale = s.PreferredLocale
	if s.PremiumProgressBar {
		t := true
		p.PremiumProgressBar = &t
	}
	if s.SystemChannelID != "" {
		flags := reconcile.SystemFlagsFromLive(s.SystemChannelFlags)
		p.SystemChannelFlags = &flags
	}
	if s.HasFeature(discord.FeatureCommunity) {
		g.community = &config.CommunitySpec{Enabled: true, RulesChannel: chanKey(s.RulesChannelID),
			UpdatesChannel: chanKey(s.PublicUpdatesChannelID), SafetyAlertsChannel: chanKey(s.SafetyAlertsChannelID)}
	} else if k := chanKey(s.RulesChannelID); k != "" {
		p.RulesChannel = k
	}
	if s.AFKChannelID != "" || s.AFKTimeout != 0 {
		a := &config.AFKSpec{Channel: chanKey(s.AFKChannelID)}
		if discord.ValidAFKTimeout(s.AFKTimeout) {
			a.Timeout = s.AFKTimeout
		}
		if a.Channel != "" || a.Timeout != 0 {
			g.afk = a
		}
	}
	p.Icon = g.download("icon", s.IconURL, "icon", maxServerImage, sniffImage, "the server icon", "profile.icon")
	p.Banner = g.download("banner", s.BannerURL, "banner", maxServerImage, sniffImage, "the server banner", "profile.banner")
	p.Splash = g.download("splash", s.SplashURL, "splash", maxServerImage, sniffImage, "the invite splash", "profile.splash")
	g.profileV = p
}

func (g *gen) spec() config.ServerSpec {
	f, t := false, true
	sp := config.ServerSpec{
		DiscordID: g.serverID, Profile: g.profileV,
		SyncPolicy: config.SyncPolicy{SelfHealP: &f, PruneP: &f, DeletionProtectionP: &t},
		Everyone:   &config.EveryoneSpec{Permissions: g.everyone},
		Roles:      g.roles, Hierarchy: g.roleOrder, Categories: g.categories, Channels: g.channels,
		AFK: g.afk, Widget: g.widget, Community: g.community, Onboarding: g.onboarding, WelcomeScreen: g.welcomeScreen,
	}
	if len(g.automod) > 0 {
		sp.AutoMod = g.automod
	}
	sp.Emojis, sp.Stickers, sp.Soundboard = g.emojis, g.stickers, g.sounds
	return sp
}

// ───────────────────────────────────────────────────────────── YAML output

type node = yaml.Node

func str(s string) *node { return &node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
func quoted(s string) *node {
	return &node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.DoubleQuotedStyle}
}
func num(i int) *node { return &node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(i)} }
func boolean(b bool) *node {
	return &node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

func flowList(items []string) *node {
	n := &node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, s := range items {
		n.Content = append(n.Content, str(s))
	}
	return n
}

type mapping struct{ n *node }

func newMap() *mapping { return &mapping{n: &node{Kind: yaml.MappingNode, Tag: "!!map"}} }

func (m *mapping) add(k string, v *node) *mapping {
	m.n.Content = append(m.n.Content, str(k), v)
	return m
}

func overwritesNode(ows map[string]config.OverwriteSpec) *node {
	targets := make([]string, 0, len(ows))
	for t := range ows {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		// @everyone first, then roles, then members
		rank := func(s string) int {
			switch {
			case s == "@everyone":
				return 0
			case strings.HasPrefix(s, "member:"):
				return 2
			}
			return 1
		}
		if rank(targets[i]) != rank(targets[j]) {
			return rank(targets[i]) < rank(targets[j])
		}
		return targets[i] < targets[j]
	})
	m := newMap()
	for _, t := range targets {
		o := ows[t]
		e := newMap()
		if len(o.Allow) > 0 {
			e.add("allow", flowList(o.Allow))
		}
		if len(o.Deny) > 0 {
			e.add("deny", flowList(o.Deny))
		}
		kn := str(t)
		if t == "@everyone" {
			kn.Style = yaml.DoubleQuotedStyle
		}
		m.n.Content = append(m.n.Content, kn, e.n)
	}
	return m.n
}

func (g *gen) document(sp config.ServerSpec) ([]byte, error) {
	root := newMap()
	root.add("apiVersion", str(config.APIVersion)).add("kind", str("Server"))
	root.add("metadata", newMap().add("name", str(g.res.Key)).n)
	spec := newMap()
	spec.add("discordId", quoted(sp.DiscordID))

	if p := sp.Profile; p != nil {
		pm := newMap()
		if p.Name != "" {
			pm.add("name", str(p.Name))
		}
		if p.Description != "" {
			pm.add("description", str(p.Description))
		}
		for _, f := range [][2]string{{"icon", p.Icon}, {"banner", p.Banner}, {"splash", p.Splash}} {
			if f[1] != "" {
				pm.add(f[0], str(f[1]))
			}
		}
		if p.VerificationLevel != "" {
			pm.add("verificationLevel", str(p.VerificationLevel))
		}
		if p.DefaultNotifications != "" {
			pm.add("defaultNotifications", str(p.DefaultNotifications))
		}
		if p.ExplicitContentFilter != "" {
			pm.add("explicitContentFilter", str(p.ExplicitContentFilter))
		}
		if p.PreferredLocale != "" {
			pm.add("preferredLocale", str(p.PreferredLocale))
		}
		if p.PremiumProgressBar != nil {
			pm.add("premiumProgressBar", boolean(*p.PremiumProgressBar))
		}
		if p.SystemChannel != "" {
			pm.add("systemChannel", str(p.SystemChannel))
		}
		if p.SystemChannelFlags != nil {
			pm.add("systemChannelFlags", encode(p.SystemChannelFlags))
		}
		if p.RulesChannel != "" {
			pm.add("rulesChannel", str(p.RulesChannel))
		}
		if len(pm.n.Content) > 0 {
			spec.add("profile", pm.n)
		}
	}
	if sp.Community != nil {
		spec.add("community", encode(sp.Community))
	}
	if sp.AFK != nil {
		spec.add("afk", encode(sp.AFK))
	}
	if sp.Widget != nil {
		spec.add("widget", encode(sp.Widget))
	}

	pol := newMap().add("selfHeal", boolean(false)).add("prune", boolean(false)).add("deletionProtection", boolean(true))
	spec.add("syncPolicy", pol.n)
	spec.n.Content[len(spec.n.Content)-2].HeadComment = "Safe defaults: drift is only reported, nothing is ever deleted."

	spec.add("everyone", newMap().add("permissions", flowList(sp.Everyone.Permissions)).n)

	if len(g.roleOrder) > 0 {
		rm := newMap()
		for _, k := range g.roleOrder {
			r := sp.Roles[k]
			e := newMap().add("name", str(r.Name))
			if r.Color != "" {
				e.add("color", quoted(r.Color))
			}
			if r.Colors != nil {
				e.add("colors", encode(r.Colors))
			}
			if r.Emoji != "" {
				e.add("emoji", quoted(r.Emoji))
			}
			if r.Icon != "" {
				e.add("icon", str(r.Icon))
			}
			if r.Hoist {
				e.add("hoist", boolean(true))
			}
			if r.Mentionable {
				e.add("mentionable", boolean(true))
			}
			if len(r.Permissions) > 0 {
				e.add("permissions", flowList(r.Permissions))
			}
			rm.add(k, e.n)
		}
		spec.add("roles", rm.n)
		spec.add("hierarchy", flowList(g.roleOrder))
	}

	if len(g.catOrder) > 0 {
		cm := newMap()
		for _, k := range g.catOrder {
			c := sp.Categories[k]
			e := newMap().add("name", str(c.Name)).add("position", num(*c.Position))
			if len(c.Overwrites) > 0 {
				e.add("overwrites", overwritesNode(c.Overwrites))
			}
			cm.add(k, e.n)
		}
		spec.add("categories", cm.n)
	}

	if len(g.chanOrder) > 0 {
		chm := newMap()
		for _, k := range g.chanOrder {
			c := sp.Channels[k]
			e := newMap().add("name", str(c.Name)).add("type", str(c.Type))
			if c.Category != "" {
				e.add("category", str(c.Category))
			}
			e.add("position", num(*c.Position))
			if c.Topic != "" {
				e.add("topic", str(c.Topic))
			}
			if c.NSFW {
				e.add("nsfw", boolean(true))
			}
			if c.Slowmode > 0 {
				e.add("slowmode", num(c.Slowmode))
			}
			if c.Bitrate > 0 {
				e.add("bitrate", num(c.Bitrate))
			}
			if c.UserLimit > 0 {
				e.add("userLimit", num(c.UserLimit))
			}
			addChannelExtras(e, c)
			if c.SyncWithCategory != nil {
				e.add("syncWithCategory", boolean(*c.SyncWithCategory))
			}
			if len(c.Overwrites) > 0 {
				e.add("overwrites", overwritesNode(c.Overwrites))
			}
			chm.add(k, e.n)
		}
		spec.add("channels", chm.n)
	}
	if len(g.automodOrder) > 0 {
		am := newMap()
		for _, k := range g.automodOrder {
			am.add(k, encode(sp.AutoMod[k]))
		}
		spec.add("automod", am.n)
	}
	if sp.Onboarding != nil {
		spec.add("onboarding", encode(sp.Onboarding))
	}
	if sp.WelcomeScreen != nil {
		spec.add("welcomeScreen", encode(sp.WelcomeScreen))
	}
	if len(g.emojiOrder) > 0 {
		m := newMap()
		for _, k := range g.emojiOrder {
			m.add(k, encode(sp.Emojis[k]))
		}
		spec.add("emojis", m.n)
	}
	if len(g.stickerOrder) > 0 {
		m := newMap()
		for _, k := range g.stickerOrder {
			m.add(k, encode(sp.Stickers[k]))
		}
		spec.add("stickers", m.n)
	}
	if len(g.soundOrder) > 0 {
		m := newMap()
		for _, k := range g.soundOrder {
			m.add(k, encode(sp.Soundboard[k]))
		}
		spec.add("soundboard", m.n)
	}
	root.add("spec", spec.n)

	doc := &node{Kind: yaml.DocumentNode, Content: []*node{root.n}}
	doc.HeadComment = fmt.Sprintf("Imported from the Discord server %q (%s) by `cicord import`.\nKeys (role, category and channel names below spec) are stable identifiers: rename them\nwith `cicord mv`, change display names freely.", g.snap.Server.Name, g.serverID)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return unescapeAstral([]byte(b.String())), nil
}

var astralEscape = regexp.MustCompile(`\\\\|\\U[0-9A-Fa-f]{8}`)

// unescapeAstral turns the \UXXXXXXXX escapes that yaml.v3 emits for characters outside
// the Basic Multilingual Plane (emoji, common in Discord names) back into UTF-8. The
// rewrite is only kept when re-parsing proves that no value changed (same approach as
// internal/writeback).
func unescapeAstral(b []byte) []byte {
	out := astralEscape.ReplaceAllFunc(b, func(m []byte) []byte {
		if len(m) == 2 { // escaped backslash
			return m
		}
		v, err := strconv.ParseUint(string(m[2:]), 16, 32)
		if err != nil || v > unicode.MaxRune {
			return m
		}
		r := rune(v)
		if !utf8.ValidRune(r) || !unicode.IsPrint(r) {
			return m
		}
		return []byte(string(r))
	})
	if bytes.Equal(out, b) {
		return b
	}
	var before, after any
	if yaml.Unmarshal(b, &before) != nil || yaml.Unmarshal(out, &after) != nil || !reflect.DeepEqual(before, after) {
		return b
	}
	return out
}

// ───────────────────────────────────────────────────────────── keys

const maxKeyLen = 63

// KeyPattern reports whether s is a valid resource key (^[a-z][a-z0-9-]{0,62}$).
func KeyPattern(s string) bool {
	if len(s) == 0 || len(s) > maxKeyLen || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

var stripMarks = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Slug derives a key from a display name: "Général Chat" → "general-chat",
// "2024 events" → "<prefix>-2024-events", "🎮" → prefix.
func Slug(name, prefix string) string {
	s, _, err := transform.String(stripMarks, name)
	if err != nil {
		s = name
	}
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == 'ß':
			b.WriteString("ss")
			dash = false
		case r == 'æ':
			b.WriteString("ae")
			dash = false
		case r == 'œ':
			b.WriteString("oe")
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return prefix
	}
	if out[0] < 'a' || out[0] > 'z' {
		out = prefix + "-" + out
	}
	if len(out) > maxKeyLen {
		out = strings.TrimRight(out[:maxKeyLen], "-")
	}
	return out
}

// namespace hands out unique keys.
type namespace struct{ used map[string]bool }

func newNamespace() *namespace { return &namespace{used: map[string]bool{}} }

func (n *namespace) take(base string) string {
	if !n.used[base] && base != "everyone" {
		n.used[base] = true
		return base
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		b := base
		if len(b)+len(suffix) > maxKeyLen {
			b = strings.TrimRight(b[:maxKeyLen-len(suffix)], "-")
		}
		k := b + suffix
		if !n.used[k] {
			n.used[k] = true
			return k
		}
	}
}

// idLess orders snowflakes numerically.
func idLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
