package discord

import (
	"context"
	"encoding/json"
	"time"
)

// Events and REST reads used by the server activity analytics (internal/analytics).
// Selected events are also available through the permission-gated plugin SDK.

// ───────────────────────────────────────────────────────────── voice

// VoiceState is a member's voice connection. ChannelID is "" when the member left voice.
type VoiceState struct {
	ServerID  string
	ChannelID string
	UserID    string
	Bot       bool // the member is a bot (when the payload carries the member)
	// SelfStream: the member is streaming (Go Live); SelfVideo: the camera is on.
	SelfStream bool
	SelfVideo  bool
	SelfMute   bool
	SelfDeaf   bool
	// Member is the member, when the payload carries it (VOICE_STATE_UPDATE does; the
	// voice states of GUILD_CREATE get it from the members array).
	Member *Member
}

// VoiceStateUpdateEvent needs the ServerVoiceStates intent. Previous is not sent by
// Discord: the runtime fills it from its voice state cache (the member's state before
// this update; nil when they were not in voice, or when the cache does not know).
type VoiceStateUpdateEvent struct {
	State    VoiceState
	Previous *VoiceState
}

// ───────────────────────────────────────────────────────────── presence

// Activity is one activity of a member presence. Type: playing | streaming | listening
// | watching | custom | competing.
type Activity struct {
	Type string
	Name string
}

// MemberPresence is a member's presence: Status online | idle | dnd | offline, and the
// status on each client platform ("" when not connected on it).
type MemberPresence struct {
	UserID     string
	Status     string
	Desktop    string
	Mobile     string
	Web        string
	Activities []Activity
}

// PresenceUpdateEvent needs the privileged ServerPresences intent.
type PresenceUpdateEvent struct {
	ServerID string
	Presence MemberPresence
}

// ServerAvailableEvent is delivered every time a server becomes available to the bot
// (each GUILD_CREATE: after READY, after an outage, on a join). It carries the voice
// states of the server (ServerVoiceStates intent) and the presences of its members
// (ServerPresences intent), so that consumers can rebuild their state after a
// reconnect or a restart.
type ServerAvailableEvent struct {
	ServerID    string
	VoiceStates []VoiceState
	Presences   []MemberPresence
}

// ───────────────────────────────────────────────────────────── moderation

// AuditLogChange is one changed field of an audit log entry (raw JSON values).
type AuditLogChange struct {
	Key string
	Old json.RawMessage
	New json.RawMessage
}

// Audit log action types Cicord tells apart (Discord's audit log events).
const (
	AuditMemberKick       = 20
	AuditMemberPrune      = 21
	AuditMemberBanAdd     = 22
	AuditMemberBanRemove  = 23
	AuditMemberUpdate     = 24
	AuditMemberRoleUpdate = 25
	AuditMessageDelete    = 72
)

// AuditLogEntryEvent is a new audit log entry. It needs the ServerModeration intent
// and the ViewAuditLog permission.
type AuditLogEntryEvent struct {
	Attribution Attribution
	Actor       *User // populated when supplied by REST or the simulator
	Reason      string
	Options     map[string]json.RawMessage
	ServerID    string
	ID          string
	ActionType  int
	UserID      string // who performed the action ("" when unknown)
	TargetID    string
	Changes     []AuditLogChange
}

// Change returns the change of a key, if any.
func (e AuditLogEntryEvent) Change(key string) (AuditLogChange, bool) {
	for _, c := range e.Changes {
		if c.Key == key {
			return c, true
		}
	}
	return AuditLogChange{}, false
}

// AutoModActionEvent is one action executed by an AutoMod rule (a rule with several
// actions produces one event per action). It needs the AutoModerationExecution intent
// and the ManageServer permission. The matched content is never decoded.
type AutoModActionEvent struct {
	Matched   string // only exposed to plugins with MessageContent
	ServerID  string
	RuleID    string
	Trigger   string // AutoModKeyword, AutoModSpam…
	Action    string // AutoModActionBlock, AutoModActionAlert…
	UserID    string
	ChannelID string
	MessageID string
}

// ───────────────────────────────────────────────────────────── invites

// InviteCreateEvent and InviteDeleteEvent need the ServerInvites intent and the
// ManageChannels permission on the channel.
type InviteCreateEvent struct {
	Inviter   *User
	ExpiresAt *time.Time
	ServerID  string
	ChannelID string
	Code      string
	MaxUses   int // 0 = unlimited
	Uses      int
}

type InviteDeleteEvent struct {
	ServerID  string
	ChannelID string
	Code      string
}

func (VoiceStateUpdateEvent) isEvent() {}
func (PresenceUpdateEvent) isEvent()   {}
func (ServerAvailableEvent) isEvent()  {}
func (AuditLogEntryEvent) isEvent()    {}
func (AutoModActionEvent) isEvent()    {}
func (InviteCreateEvent) isEvent()     {}
func (InviteDeleteEvent) isEvent()     {}

// ───────────────────────────────────────────────────────────── REST reads

// ServerCounts are the approximate counts of a server (GET /guilds/{id}?with_counts=true).
type ServerCounts struct {
	ApproximateMembers int
	// ApproximateOnline counts members online, idle or do not disturb.
	ApproximateOnline int
	Boosts            int
	BoostTier         int
}

// ScheduledEventInterest is a scheduled event with the number of interested members.
// Status: scheduled | active | completed | canceled.
type ScheduledEventInterest struct {
	ID         string
	Name       string
	Status     string
	Interested int
}

// InviteUses is an invite with its use count. The vanity URL has Vanity set.
type InviteUses struct {
	ChannelID string
	Inviter   *User
	ExpiresAt *time.Time
	Code      string
	Uses      int
	MaxUses   int // 0 = unlimited
	Vanity    bool
}

// AnalyticsReader is implemented by clients able to read the counts sampled by the
// analytics collector. Every call is one REST request per server (rate limited).
type AnalyticsReader interface {
	GetServerCounts(ctx context.Context, serverID string) (ServerCounts, error)
	// ListScheduledEventInterest returns the scheduled events with their interested counts.
	ListScheduledEventInterest(ctx context.Context, serverID string) ([]ScheduledEventInterest, error)
	// ListInviteUses returns the server's invites and, when the server has one, its vanity
	// URL. It needs the ManageServer permission (ErrForbidden otherwise).
	ListInviteUses(ctx context.Context, serverID string) ([]InviteUses, error)
	// ListServerBots returns the user IDs of the server's bot members (accounts Discord
	// flags as bots). It lists every member, 1000 per request, and needs the privileged
	// ServerMembers intent.
	ListServerBots(ctx context.Context, serverID string) ([]string, error)
}
