package importer

import (
	"sort"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/reconcile"
	"github.com/npjonath/codi/internal/state"
)

// encode turns a config value into a YAML node (struct field order, omitempty honored).
func encode(v any) *node {
	n := &node{}
	if err := n.Encode(v); err != nil {
		return str("")
	}
	return n
}

func importChannelExtras(c discord.Channel, ch *config.Channel) {
	if voiceLike(c.Type) {
		if c.RTCRegion != "" {
			ch.VoiceRegion = c.RTCRegion
		}
		if c.VideoQuality == "full" {
			ch.VideoQuality = "full"
		}
	}
	if textLike(c.Type) {
		if n := reconcile.ArchiveName(c.DefaultAutoArchive); n != "" {
			ch.ThreadArchiveAfter = n
		}
		if c.DefaultThreadSlowmode > 0 {
			v := c.DefaultThreadSlowmode
			ch.ThreadSlowmode = &v
		}
	}
	if c.Type == discord.ChannelForum || c.Type == discord.ChannelMedia {
		if len(c.Tags) > 0 {
			ns := newNamespace()
			ch.Tags = map[string]config.ForumTag{}
			for _, t := range c.Tags {
				ch.Tags[ns.take(Slug(t.Name, "tag"))] = config.ForumTag{Name: t.Name, Emoji: t.Emoji, Moderated: t.Moderated}
			}
		}
		ch.DefaultReaction = c.DefaultReaction
		ch.SortOrder = c.DefaultSortOrder
		if c.Type == discord.ChannelForum {
			ch.Layout = c.DefaultLayout
		}
	}
}

// addChannelExtras writes the parity fields of a channel (importChannelExtras).
func addChannelExtras(e *mapping, c config.Channel) {
	if c.VoiceRegion != "" {
		e.add("voiceRegion", str(c.VoiceRegion))
	}
	if c.VideoQuality != "" {
		e.add("videoQuality", str(c.VideoQuality))
	}
	if c.ThreadArchiveAfter != "" {
		e.add("threadArchiveAfter", str(c.ThreadArchiveAfter))
	}
	if c.ThreadSlowmode != nil {
		e.add("threadSlowmode", num(*c.ThreadSlowmode))
	}
	if len(c.Tags) > 0 {
		e.add("tags", encode(c.Tags))
	}
	if c.DefaultReaction != "" {
		e.add("defaultReaction", quoted(c.DefaultReaction))
	}
	if c.SortOrder != "" {
		e.add("sortOrder", str(c.SortOrder))
	}
	if c.Layout != "" {
		e.add("layout", str(c.Layout))
	}
}

// keysOf converts IDs to keys, warning about the ones without a key.
func (g *gen) keysOf(ids []string, keys map[string]string, what, where string) []string {
	var out []string
	for _, id := range ids {
		if k, ok := keys[id]; ok && (what != "channel" || g.channels[k].Name != "") {
			out = append(out, k)
			continue
		}
		g.warn("%s: %s %s cannot be referenced by key: not imported", where, what, id)
	}
	sort.Strings(out)
	return out
}

func (g *gen) importParity() {
	snap := g.snap
	chanKey := func(id string) string {
		if k, ok := g.chanKeys[id]; ok && g.channels[k].Name != "" {
			return k
		}
		return ""
	}
	if w := snap.Widget; w != nil && (w.Enabled || w.ChannelID != "") {
		g.widget = &config.WidgetSpec{Enabled: w.Enabled, Channel: chanKey(w.ChannelID)}
	}
	if snap.HasAutoMod && len(snap.AutoMod) > 0 {
		ns := newNamespace()
		g.automod = map[string]config.AutoModSpec{}
		rules := append([]discord.AutoModRule(nil), snap.AutoMod...)
		sort.SliceStable(rules, func(i, j int) bool { return idLess(rules[i].ID, rules[j].ID) })
		for _, r := range rules {
			k := ns.take(Slug(r.Name, "rule"))
			where := "AutoMod rule " + quoteName(r.Name)
			spec := config.AutoModSpec{Name: r.Name, Trigger: r.Trigger, Keywords: r.Keywords, Regex: r.Regex,
				AllowList: r.AllowList, Presets: r.Presets, MentionLimit: r.MentionLimit, MentionRaidProtection: r.MentionRaidProtection,
				ExemptRoles: g.keysOf(r.ExemptRoles, g.roleKeys, "role", where), ExemptChannels: g.keysOf(r.ExemptChannels, g.chanKeys, "channel", where)}
			if !r.Enabled {
				f := false
				spec.Enabled = &f
			}
			for _, a := range r.Actions {
				switch a.Type {
				case discord.AutoModActionBlock, discord.AutoModActionBlockInteraction:
					spec.Actions.Block = &config.AutoModBlock{Message: a.CustomMessage}
				case discord.AutoModActionAlert:
					if spec.Actions.Alert = chanKey(a.ChannelID); spec.Actions.Alert == "" {
						g.warn("%s: alert channel %s cannot be referenced by key: alert not imported", where, a.ChannelID)
					}
				case discord.AutoModActionTimeout:
					spec.Actions.Timeout = reconcile.DurationName(a.DurationSeconds)
				}
			}
			g.automod[k] = spec
			g.automodOrder = append(g.automodOrder, k)
			g.record(state.KindAutoMod, k, r.ID)
		}
	}
	if o := snap.Onboarding; o != nil && (o.Enabled || len(o.Prompts) > 0 || len(o.DefaultChannelIDs) > 0) {
		spec := &config.OnboardingSpec{Enabled: o.Enabled, DefaultChannels: g.keysOf(o.DefaultChannelIDs, g.chanKeys, "channel", "onboarding")}
		if o.Mode == "advanced" {
			spec.Mode = "advanced"
		}
		ns := newNamespace()
		for _, pr := range o.Prompts {
			cp := config.OnboardingPrompt{Key: ns.take(Slug(pr.Title, "question")), Title: pr.Title, SingleSelect: pr.SingleSelect, Required: pr.Required}
			if pr.Type == "dropdown" {
				cp.Type = "dropdown"
			}
			if !pr.InOnboarding {
				f := false
				cp.InOnboarding = &f
			}
			for _, op := range pr.Options {
				where := "onboarding question " + quoteName(pr.Title)
				// keep the live order of IDs: options compare as sets
				cp.Options = append(cp.Options, config.OnboardingOption{Title: op.Title, Description: op.Description, Emoji: op.Emoji,
					Roles: g.keysOf(op.RoleIDs, g.roleKeys, "role", where), Channels: g.keysOf(op.ChannelIDs, g.chanKeys, "channel", where)})
			}
			spec.Prompts = append(spec.Prompts, cp)
		}
		g.onboarding = spec
	}
	if ws := snap.WelcomeScreen; ws != nil && (ws.Enabled || len(ws.Channels) > 0 || ws.Description != "") {
		spec := &config.WelcomeScreenSpec{Description: ws.Description}
		if !ws.Enabled {
			f := false
			spec.Enabled = &f
		}
		for _, c := range ws.Channels {
			k := chanKey(c.ChannelID)
			if k == "" {
				g.warn("welcome screen: channel %s cannot be referenced by key: not imported", c.ChannelID)
				continue
			}
			spec.Channels = append(spec.Channels, config.WelcomeChannel{Channel: k, Description: c.Description, Emoji: c.Emoji})
		}
		g.welcomeScreen = spec
	}
	if n := len(snap.Emojis); n > 0 {
		g.warn("%d emoji(s) not imported: export their images to the repository and declare them under spec.emojis", n)
	}
	if n := len(snap.Stickers); n > 0 {
		g.warn("%d sticker(s) not imported: export their files to the repository and declare them under spec.stickers", n)
	}
	if n := len(snap.Sounds); n > 0 {
		g.warn("%d soundboard sound(s) not imported: export their files to the repository and declare them under spec.soundboard", n)
	}
}

func quoteName(s string) string { return "\"" + s + "\"" }
