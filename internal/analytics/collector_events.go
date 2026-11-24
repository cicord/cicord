package analytics

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// event maps a gateway event of the collecting bot to metrics. Caller holds c.mu.
func (c *Collector) event(s *collected, ev discord.Event, now time.Time) {
	switch e := ev.(type) {
	case discord.MemberJoinEvent:
		if e.Member.User.Bot {
			c.count(s, MembersJoinedBots, "", now, 1)
			return
		}
		c.count(s, MembersJoined, "", now, 1)
		c.count(s, MembersJoinedByAge, AccountAgeBucket(e.Member.User.ID, now), now, 1)
		c.scheduleInvites(s)

	case discord.MemberLeaveEvent:
		if !e.User.Bot {
			c.count(s, MembersLeft, "", now, 1)
		}
		if vs := s.voice[e.User.ID]; vs != nil {
			c.credit(s, vs, now)
			delete(s.voice, e.User.ID)
		}
		delete(s.presence, e.User.ID)

	case discord.MemberUpdateEvent:
		// Without the previous state a change cannot be told from a refresh.
		if e.Previous == nil || e.Member.User.Bot {
			return
		}
		for _, id := range e.Member.RoleIDs {
			if !slices.Contains(e.Previous.RoleIDs, id) {
				c.count(s, MembersRoleAdded, id, now, 1)
			}
		}
		for _, id := range e.Previous.RoleIDs {
			if !slices.Contains(e.Member.RoleIDs, id) {
				c.count(s, MembersRoleRemoved, id, now, 1)
			}
		}
		done := discord.MemberFlagCompletedOnboarding
		if e.Member.Flags&done != 0 && e.Previous.Flags&done == 0 {
			c.count(s, OnboardingCompleted, "", now, 1)
		}

	case discord.MessageCreateEvent:
		m := e.Message
		if m.System {
			return // join, boost and pin notices are not written by their author
		}
		if m.Author.Bot || m.WebhookID != "" {
			c.count(s, MessagesSentBots, m.ChannelID, now, 1)
			return
		}
		c.count(s, MessagesSent, m.ChannelID, now, 1)
		if m.ReplyToID != "" {
			c.count(s, MessagesReplies, m.ChannelID, now, 1)
		}
		c.count(s, MessagesStickers, "", now, float64(len(m.StickerIDs)))
		// Without MessageContent Discord empties attachments and polls, except in the
		// messages that mention the bot: counting those would be biased.
		if s.cfg.Intents.Has(intentMessageContent) {
			c.count(s, MessagesAttachments, m.ChannelID, now, float64(len(m.Attachments)))
			if m.Poll {
				c.count(s, PollsCreated, m.ChannelID, now, 1)
			}
		}
		c.markActive(s, m.Author.ID, now, ActiveSetChat, ActiveSetAll)

	case discord.MessageUpdateEvent:
		m := e.Message
		// Embed unfurls also update messages: only real edits carry edited_timestamp.
		if m.EditedAt == nil || m.Author.Bot || m.WebhookID != "" || m.System {
			return
		}
		c.count(s, MessagesEdited, m.ChannelID, now, 1)

	case discord.MessageDeleteEvent:
		c.count(s, MessagesDeleted, e.ChannelID, now, 1)

	case discord.ReactionAddEvent:
		if e.Member != nil && e.Member.User.Bot || c.botUsers[e.UserID] || e.Emoji == "" {
			return
		}
		c.count(s, ReactionsAdded, e.Emoji, now, 1)
		c.markActive(s, e.UserID, now, ActiveSetAll)

	case discord.ChannelCreateEvent:
		if e.NewThread {
			c.count(s, ThreadsCreated, e.Channel.ParentID, now, 1)
		}

	case discord.VoiceStateUpdateEvent:
		c.voiceUpdate(s, e.State, now)

	case discord.ServerAvailableEvent:
		c.applySnapshot(s, e, now)

	case discord.PresenceUpdateEvent:
		if !s.cfg.presenceCollected() {
			return
		}
		if p, ok := presenceFrom(e.Presence); ok {
			s.presence[e.Presence.UserID] = p
		} else {
			delete(s.presence, e.Presence.UserID)
		}

	case discord.AuditLogEntryEvent:
		if c.botUsers[e.UserID] {
			return // Cicord's own changes (reconciliation, plugins) are not moderation
		}
		c.count(s, ModerationActions, ModerationAction(e), now, 1)

	case discord.AutoModActionEvent:
		if e.Action == discord.AutoModActionBlock {
			c.count(s, AutoModBlocked, "", now, 1)
		}
		// One event per action of the rule: count the trigger once.
		key := e.RuleID + "|" + e.UserID + "|" + e.ChannelID
		if last, ok := s.autoMod[key]; !ok || now.Sub(last) > autoModWindow {
			c.count(s, AutoModTriggers, e.RuleID, now, 1)
		}
		s.autoMod[key] = now
		if len(s.autoMod) > 256 {
			for k, t := range s.autoMod {
				if now.Sub(t) > autoModWindow {
					delete(s.autoMod, k)
				}
			}
		}

	case discord.InviteCreateEvent:
		c.count(s, InvitesCreated, "", now, 1)
		if s.invites.known {
			s.invites.uses[e.Code] = discord.InviteUses{Code: e.Code, Uses: e.Uses, MaxUses: e.MaxUses}
		}

	case discord.InviteDeleteEvent:
		s.invites.noteDeleted(e.Code, now)
	}
}

// interaction counts commands and components (any bot of the server). Caller holds c.mu.
func (c *Collector) interaction(s *collected, it discord.Interaction, now time.Time) {
	switch it.Type {
	case discord.InteractionCommand:
		path, _ := it.CommandPath()
		c.count(s, CommandsUsed, strings.Join(path, " "), now, 1)
		c.markActive(s, interactionUser(it), now, ActiveSetAll)
	case discord.InteractionComponent:
		c.count(s, ComponentsUsed, "", now, 1)
	}
}

func interactionUser(it discord.Interaction) string {
	if it.User.ID != "" {
		return it.User.ID
	}
	if it.Member != nil {
		return it.Member.User.ID
	}
	return ""
}

// ───────────────────────────────────────────────────────────── account age

// discordEpochMs is the Discord snowflake epoch (2015-01-01T00:00:00Z).
const discordEpochMs = 1420070400000

// SnowflakeTime returns the creation time encoded in a Discord ID (zero when invalid).
func SnowflakeTime(id string) time.Time {
	v, err := strconv.ParseUint(id, 10, 64)
	if err != nil || v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(v>>22) + discordEpochMs).UTC()
}

// AccountAgeBucket returns the AccountAgeBuckets label of an account created at the
// time encoded in its user ID.
func AccountAgeBucket(userID string, now time.Time) string {
	created := SnowflakeTime(userID)
	if created.IsZero() {
		return AccountAgeBuckets[len(AccountAgeBuckets)-1]
	}
	switch age := now.Sub(created); {
	case age < 24*time.Hour:
		return AccountAgeBuckets[0]
	case age < 7*24*time.Hour:
		return AccountAgeBuckets[1]
	case age < 30*24*time.Hour:
		return AccountAgeBuckets[2]
	case age < 365*24*time.Hour:
		return AccountAgeBuckets[3]
	}
	return AccountAgeBuckets[4]
}

// ───────────────────────────────────────────────────────────── moderation actions

// auditActions names the audit log action types (dimension of ModerationActions).
var auditActions = map[int]string{
	1: "server_update", 10: "channel_create", 11: "channel_update", 12: "channel_delete",
	13: "channel_permission_create", 14: "channel_permission_update", 15: "channel_permission_delete",
	20: "kick", 21: "prune", 22: "ban", 23: "unban", 24: "member_update", 25: "member_role_update",
	26: "member_move", 27: "member_disconnect", 28: "bot_add",
	30: "role_create", 31: "role_update", 32: "role_delete",
	40: "invite_create", 41: "invite_update", 42: "invite_delete",
	50: "webhook_create", 51: "webhook_update", 52: "webhook_delete",
	60: "emoji_create", 61: "emoji_update", 62: "emoji_delete",
	72: "message_delete", 73: "message_bulk_delete", 74: "message_pin", 75: "message_unpin",
	80: "integration_create", 81: "integration_update", 82: "integration_delete",
	83: "stage_create", 84: "stage_update", 85: "stage_delete",
	90: "sticker_create", 91: "sticker_update", 92: "sticker_delete",
	100: "event_create", 101: "event_update", 102: "event_delete",
	110: "thread_create", 111: "thread_update", 112: "thread_delete",
	121: "command_permission_update",
	130: "sound_create", 131: "sound_update", 132: "sound_delete",
	140: "automod_rule_create", 141: "automod_rule_update", 142: "automod_rule_delete",
	143: "automod_block", 144: "automod_flag", 145: "automod_timeout", 146: "automod_quarantine",
	163: "onboarding_prompt_create", 164: "onboarding_prompt_update", 165: "onboarding_prompt_delete",
	166: "onboarding_create", 167: "onboarding_update",
	190: "home_settings_create", 191: "home_settings_update",
}

// ModerationAction returns the ModerationActions dimension of an audit log entry: a
// member update that sets or clears communication_disabled_until is a timeout or an
// untimeout; unknown action types are "other".
func ModerationAction(e discord.AuditLogEntryEvent) string {
	if e.ActionType == discord.AuditMemberUpdate {
		if ch, ok := e.Change("communication_disabled_until"); ok {
			if v := strings.TrimSpace(string(ch.New)); v != "" && v != "null" {
				return "timeout"
			}
			return "untimeout"
		}
	}
	if a, ok := auditActions[e.ActionType]; ok {
		return a
	}
	return OtherDim
}
