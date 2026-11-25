// Package analytics is the server activity analytics of the runtime: the catalog of
// metrics (this file), the API types the admin dashboard reads (api.go), the collector
// that turns Discord gateway events and periodic samples into hourly aggregates, and
// the query/summary logic behind GET /api/v1/servers/{server}/analytics.
//
// Privacy rules, enforced everywhere in this package:
//   - only aggregates are stored: counts per hour, per server, with a bounded
//     dimension (a channel ID, an emoji, a command name, an invite code, a fixed label);
//   - never message content, never a member ID or name as a dimension;
//   - distinct-member counts use a daily HMAC token, deleted after the daily rollup;
//   - detailed presence (statuses, activities, platforms) needs the privileged
//     ServerPresences intent and spec.analytics.presence: true; without it only
//     Discord's approximate online count is sampled.
package analytics

import "github.com/cicord/cicord/internal/state"

// Metric describes one analytics series.
type Metric struct {
	Name string
	Kind state.AnalyticsKind
	// Dim names what the dimension holds ("" when the metric has none).
	Dim string
	// Unit of the values: "count", "members", "seconds", "level".
	Unit string
	// Source says where the data comes from (for the docs and the dashboard's help).
	Source string
	// Presence is true for metrics that need spec.analytics.presence and the privileged
	// ServerPresences intent.
	Presence bool
}

// Metric names. Counters are summed per hour; gauges are sampled (every
// SampleInterval) and keep the hour's last or max value; daily distinct counts are
// written once per day on the day's first bucket (00:00 UTC).
const (
	// Members
	MembersTotal        = "members.total"         // gauge last: human members (approximate member count minus the bots)
	MembersBots         = "members.bots"          // gauge last: bot members (accounts Discord flags as bots)
	MembersOnline       = "members.online"        // gauge last: human members online, idle or dnd
	MembersJoined       = "members.joined"        // counter
	MembersLeft         = "members.left"          // counter
	MembersJoinedByAge  = "members.joined.age"    // counter, dim: account age bucket (<1d, <7d, <30d, <1y, >=1y)
	MembersJoinedBots   = "members.joined.bots"   // counter: bots added
	MembersRoleAdded    = "members.roles.added"   // counter, dim: role ID
	MembersRoleRemoved  = "members.roles.removed" // counter, dim: role ID
	MembersBoosting     = "members.boosting"      // gauge last: premium subscription count (boosts)
	ServerBoostTier     = "server.boost.tier"     // gauge last: premium tier 0-3
	MembersActive       = "members.active"        // daily distinct members who sent a message, reacted, spoke or used a command
	MembersActiveChat   = "members.active.chat"   // daily distinct message authors
	MembersActiveVoice  = "members.active.voice"  // daily distinct members in voice
	MembersInvitedBy    = "invites.uses"          // counter, dim: invite code (vanity URL included)
	InvitesCreated      = "invites.created"       // counter
	OnboardingCompleted = "onboarding.completed"  // counter: members who completed Discord onboarding (member flags)

	// Messages
	MessagesSent        = "messages.sent"        // counter, dim: channel ID (humans only)
	MessagesSentBots    = "messages.sent.bots"   // counter, dim: channel ID (bots and webhooks)
	MessagesEdited      = "messages.edited"      // counter, dim: channel ID
	MessagesDeleted     = "messages.deleted"     // counter, dim: channel ID
	MessagesAttachments = "messages.attachments" // counter, dim: channel ID (attachment count)
	MessagesReplies     = "messages.replies"     // counter, dim: channel ID (messages that reply to another)
	MessagesStickers    = "messages.stickers"    // counter (stickers sent)
	ReactionsAdded      = "reactions.added"      // counter, dim: emoji (unicode or custom emoji name:id)
	ThreadsCreated      = "threads.created"      // counter, dim: parent channel ID
	PollsCreated        = "polls.created"        // counter, dim: channel ID (Discord native polls)

	// Voice
	VoiceSeconds   = "voice.seconds"     // counter, dim: channel ID (member-seconds spent in voice)
	VoiceSessions  = "voice.sessions"    // counter, dim: channel ID (joins of a voice channel)
	VoicePeak      = "voice.peak"        // gauge max: members in voice at the same time
	VoiceStreaming = "voice.streaming"   // counter, dim: channel ID (member-seconds streaming or with camera)
	StageSeconds   = "stage.seconds"     // counter, dim: channel ID (member-seconds in stage channels)
	EventsInterest = "events.interested" // gauge last, dim: scheduled event ID (interested users)

	// Moderation and safety
	ModerationActions = "moderation.actions" // counter, dim: action (ban, unban, kick, timeout, untimeout, prune, message_delete, role_update…) from the audit log
	AutoModTriggers   = "automod.triggers"   // counter, dim: AutoMod rule ID
	AutoModBlocked    = "automod.blocked"    // counter: messages blocked by AutoMod

	// Bot and commands
	CommandsUsed   = "commands.used"   // counter, dim: command name (slash, user, message commands)
	ComponentsUsed = "components.used" // counter: button and select menu interactions

	// Presence (spec.analytics.presence: true and the ServerPresences intent)
	PresenceStatus   = "presence.status"   // gauge last, dim: online, idle, dnd
	PresencePlatform = "presence.platform" // gauge last, dim: desktop, mobile, web
	PresenceActivity = "presence.activity" // gauge last, dim: "<type>:<name>" (playing:Minecraft, listening:Spotify…), top activities only
)

// Catalog lists every metric, in dashboard order.
var Catalog = []Metric{
	{Name: MembersTotal, Kind: state.AnalyticsGaugeLast, Unit: "members", Source: "human members: Discord's approximate member count minus the bot members, sampled"},
	{Name: MembersBots, Kind: state.AnalyticsGaugeLast, Unit: "members", Source: "bot members (accounts Discord flags as bots): the member list, read daily and kept current by join and leave events"},
	{Name: MembersOnline, Kind: state.AnalyticsGaugeLast, Unit: "members", Source: "human members online, idle or do not disturb: counted from presences when detailed presence is collected, otherwise Discord's approximate online count minus the bot members, sampled"},
	{Name: MembersJoined, Kind: state.AnalyticsCounter, Unit: "count", Source: "member join events"},
	{Name: MembersLeft, Kind: state.AnalyticsCounter, Unit: "count", Source: "member leave events (leaves, kicks and bans)"},
	{Name: MembersJoinedByAge, Kind: state.AnalyticsCounter, Dim: "account age", Unit: "count", Source: "member join events: age of the Discord account"},
	{Name: MembersJoinedBots, Kind: state.AnalyticsCounter, Unit: "count", Source: "bot accounts added"},
	{Name: MembersRoleAdded, Kind: state.AnalyticsCounter, Dim: "role", Unit: "count", Source: "member update events"},
	{Name: MembersRoleRemoved, Kind: state.AnalyticsCounter, Dim: "role", Unit: "count", Source: "member update events"},
	{Name: MembersBoosting, Kind: state.AnalyticsGaugeLast, Unit: "count", Source: "server boosts, sampled"},
	{Name: ServerBoostTier, Kind: state.AnalyticsGaugeLast, Unit: "level", Source: "server boost level, sampled"},
	{Name: MembersActive, Kind: state.AnalyticsGaugeLast, Unit: "members", Source: "distinct members active in the day (message, reaction, voice or command)"},
	{Name: MembersActiveChat, Kind: state.AnalyticsGaugeLast, Unit: "members", Source: "distinct message authors in the day"},
	{Name: MembersActiveVoice, Kind: state.AnalyticsGaugeLast, Unit: "members", Source: "distinct members in voice in the day"},
	{Name: MembersInvitedBy, Kind: state.AnalyticsCounter, Dim: "invite", Unit: "count", Source: "invite use counts compared before and after each join"},
	{Name: InvitesCreated, Kind: state.AnalyticsCounter, Unit: "count", Source: "invite create events"},
	{Name: OnboardingCompleted, Kind: state.AnalyticsCounter, Unit: "count", Source: "member updates: Discord onboarding completed"},

	{Name: MessagesSent, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "message events (members, not bots)"},
	{Name: MessagesSentBots, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "message events from bots and webhooks"},
	{Name: MessagesEdited, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "message update events"},
	{Name: MessagesDeleted, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "message delete events"},
	{Name: MessagesAttachments, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "attachments of new messages"},
	{Name: MessagesReplies, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "new messages that reply to another"},
	{Name: MessagesStickers, Kind: state.AnalyticsCounter, Unit: "count", Source: "stickers of new messages"},
	{Name: ReactionsAdded, Kind: state.AnalyticsCounter, Dim: "emoji", Unit: "count", Source: "reaction add events"},
	{Name: ThreadsCreated, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "thread create events (dimension: parent channel)"},
	{Name: PollsCreated, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "new messages carrying a Discord poll"},

	{Name: VoiceSeconds, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "seconds", Source: "voice state events: time members spend in each voice channel"},
	{Name: VoiceSessions, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "count", Source: "voice state events: joins of a voice channel"},
	{Name: VoicePeak, Kind: state.AnalyticsGaugeMax, Unit: "members", Source: "voice state events: most members in voice at once"},
	{Name: VoiceStreaming, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "seconds", Source: "voice state events: time streaming or with the camera on"},
	{Name: StageSeconds, Kind: state.AnalyticsCounter, Dim: "channel", Unit: "seconds", Source: "voice state events in stage channels"},
	{Name: EventsInterest, Kind: state.AnalyticsGaugeLast, Dim: "event", Unit: "members", Source: "scheduled events: interested members, sampled"},

	{Name: ModerationActions, Kind: state.AnalyticsCounter, Dim: "action", Unit: "count", Source: "audit log entry events"},
	{Name: AutoModTriggers, Kind: state.AnalyticsCounter, Dim: "rule", Unit: "count", Source: "AutoMod action execution events"},
	{Name: AutoModBlocked, Kind: state.AnalyticsCounter, Unit: "count", Source: "AutoMod action execution events that blocked a message"},

	{Name: CommandsUsed, Kind: state.AnalyticsCounter, Dim: "command", Unit: "count", Source: "application command interactions handled by the runtime"},
	{Name: ComponentsUsed, Kind: state.AnalyticsCounter, Unit: "count", Source: "button and select menu interactions"},

	{Name: PresenceStatus, Kind: state.AnalyticsGaugeLast, Dim: "status", Unit: "members", Source: "presence events (privileged intent)", Presence: true},
	{Name: PresencePlatform, Kind: state.AnalyticsGaugeLast, Dim: "platform", Unit: "members", Source: "presence events (privileged intent)", Presence: true},
	{Name: PresenceActivity, Kind: state.AnalyticsGaugeLast, Dim: "activity", Unit: "members", Source: "presence events: what members are playing, listening to or watching (privileged intent)", Presence: true},
}

// Lookup returns the catalog entry of a metric.
func Lookup(name string) (Metric, bool) {
	for _, m := range Catalog {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}

// Account age buckets of MembersJoinedByAge, youngest first.
var AccountAgeBuckets = []string{"<1d", "<7d", "<30d", "<1y", ">=1y"}

// Daily active sets (state.Analytics MarkActive) and the metric each one rolls up into.
const (
	ActiveSetAll   = "all"
	ActiveSetChat  = "chat"
	ActiveSetVoice = "voice"
)

// ActiveSetMetrics maps each active set to its daily distinct-count metric.
var ActiveSetMetrics = map[string]string{
	ActiveSetAll:   MembersActive,
	ActiveSetChat:  MembersActiveChat,
	ActiveSetVoice: MembersActiveVoice,
}
