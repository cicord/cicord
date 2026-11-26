package discord

// Event is a gateway event delivered to Session.Open's handler.
type Event interface{ isEvent() }

type ReadyEvent struct {
	User      User
	ServerIDs []string
}

// Ban gateway notifications identify the affected user. Discord supplies no reason here.
type BanAddEvent struct {
	ServerID string
	User     User
}
type BanRemoveEvent struct {
	ServerID string
	User     User
}

func (BanAddEvent) isEvent()    {}
func (BanRemoveEvent) isEvent() {}

type MemberJoinEvent struct{ Member Member }

type MemberLeaveEvent struct {
	ServerID string
	User     User
}

type MemberUpdateEvent struct {
	Member   Member
	Previous *Member // nil when not cached
}

type MessageCreateEvent struct{ Message Message }
type MessageUpdateEvent struct{ Message Message }

type MessageDeleteEvent struct {
	ID        string
	ChannelID string
	ServerID  string
}

type ReactionEvent struct {
	ServerID  string
	ChannelID string
	MessageID string
	UserID    string
	Emoji     string // unicode emoji or "name:id" for custom emojis
	Member    *Member
}

type ReactionAddEvent struct{ ReactionEvent }
type ReactionRemoveEvent struct{ ReactionEvent }

type InteractionCreateEvent struct{ Interaction Interaction }

// ChannelCreateEvent is also delivered for threads. NewThread is set when the thread was
// just created (THREAD_CREATE newly_created), not when the bot was added to an existing
// thread.
type ChannelCreateEvent struct {
	Channel   Channel
	NewThread bool
}
type ChannelUpdateEvent struct {
	Channel  Channel
	Previous *Channel
}
type WebhooksUpdateEvent struct {
	ServerID  string
	ChannelID string
}

func (WebhooksUpdateEvent) isEvent() {}

type ChannelDeleteEvent struct{ Channel Channel }

type RoleCreateEvent struct {
	ServerID string
	Role     Role
}
type RoleUpdateEvent struct {
	Previous *Role
	ServerID string
	Role     Role
}
type RoleDeleteEvent struct {
	Previous *Role
	ServerID string
	RoleID   string
}

type ServerUpdateEvent struct {
	Server   Server
	Previous *Server
}

// ServerJoinEvent is delivered when the bot joins a server (invite accepted) while
// connected. Servers that become available after READY (lazy loading) or after an
// outage are not joins.
type ServerJoinEvent struct{ Server Server }

// ServerLeaveEvent is delivered when the bot leaves a server: it was kicked or banned, it
// left, or the server was deleted (not when the server becomes unavailable).
type ServerLeaveEvent struct{ ServerID string }

// AutoMod rule events need the AutoModerationConfiguration intent.
type AutoModRuleCreateEvent struct {
	ServerID string
	Rule     AutoModRule
}
type AutoModRuleUpdateEvent struct {
	ServerID string
	Rule     AutoModRule
}
type AutoModRuleDeleteEvent struct {
	ServerID string
	Rule     AutoModRule
}

// Expression events need the ServerExpressions intent. Emojis and stickers are
// reported as the full new list of the server.
type EmojisUpdateEvent struct {
	ServerID string
	Emojis   []Emoji
}
type StickersUpdateEvent struct {
	ServerID string
	Stickers []Sticker
}
type SoundCreateEvent struct {
	ServerID string
	Sound    SoundboardSound
}
type SoundUpdateEvent struct {
	ServerID string
	Sound    SoundboardSound
}
type SoundDeleteEvent struct {
	ServerID string
	SoundID  string
}

func (ReadyEvent) isEvent()             {}
func (MemberJoinEvent) isEvent()        {}
func (MemberLeaveEvent) isEvent()       {}
func (MemberUpdateEvent) isEvent()      {}
func (MessageCreateEvent) isEvent()     {}
func (MessageUpdateEvent) isEvent()     {}
func (MessageDeleteEvent) isEvent()     {}
func (ReactionAddEvent) isEvent()       {}
func (ReactionRemoveEvent) isEvent()    {}
func (InteractionCreateEvent) isEvent() {}
func (ChannelCreateEvent) isEvent()     {}
func (ChannelUpdateEvent) isEvent()     {}
func (ChannelDeleteEvent) isEvent()     {}
func (RoleCreateEvent) isEvent()        {}
func (RoleUpdateEvent) isEvent()        {}
func (RoleDeleteEvent) isEvent()        {}
func (ServerUpdateEvent) isEvent()      {}
func (ServerJoinEvent) isEvent()        {}
func (ServerLeaveEvent) isEvent()       {}
func (AutoModRuleCreateEvent) isEvent() {}
func (AutoModRuleUpdateEvent) isEvent() {}
func (AutoModRuleDeleteEvent) isEvent() {}
func (EmojisUpdateEvent) isEvent()      {}
func (StickersUpdateEvent) isEvent()    {}
func (SoundCreateEvent) isEvent()       {}
func (SoundUpdateEvent) isEvent()       {}
func (SoundDeleteEvent) isEvent()       {}

// ServerIDOf returns the server an event belongs to ("" for DMs / global events).
func ServerIDOf(e Event) string {
	switch ev := e.(type) {
	case MemberJoinEvent:
		return ev.Member.ServerID
	case BanAddEvent:
		return ev.ServerID
	case BanRemoveEvent:
		return ev.ServerID
	case MemberLeaveEvent:
		return ev.ServerID
	case MemberUpdateEvent:
		return ev.Member.ServerID
	case MessageCreateEvent:
		return ev.Message.ServerID
	case MessageUpdateEvent:
		return ev.Message.ServerID
	case MessageDeleteEvent:
		return ev.ServerID
	case ReactionAddEvent:
		return ev.ServerID
	case ReactionRemoveEvent:
		return ev.ServerID
	case InteractionCreateEvent:
		return ev.Interaction.ServerID
	case ChannelCreateEvent:
		return ev.Channel.ServerID
	case ChannelUpdateEvent:
		return ev.Channel.ServerID
	case WebhooksUpdateEvent:
		return ev.ServerID
	case ChannelDeleteEvent:
		return ev.Channel.ServerID
	case RoleCreateEvent:
		return ev.ServerID
	case RoleUpdateEvent:
		return ev.ServerID
	case RoleDeleteEvent:
		return ev.ServerID
	case ServerUpdateEvent:
		return ev.Server.ID
	case ServerJoinEvent:
		return ev.Server.ID
	case ServerLeaveEvent:
		return ev.ServerID
	case AutoModRuleCreateEvent:
		return ev.ServerID
	case AutoModRuleUpdateEvent:
		return ev.ServerID
	case AutoModRuleDeleteEvent:
		return ev.ServerID
	case EmojisUpdateEvent:
		return ev.ServerID
	case StickersUpdateEvent:
		return ev.ServerID
	case SoundCreateEvent:
		return ev.ServerID
	case SoundUpdateEvent:
		return ev.ServerID
	case SoundDeleteEvent:
		return ev.ServerID
	case VoiceStateUpdateEvent:
		return ev.State.ServerID
	case PresenceUpdateEvent:
		return ev.ServerID
	case ServerAvailableEvent:
		return ev.ServerID
	case AuditLogEntryEvent:
		return ev.ServerID
	case AutoModActionEvent:
		return ev.ServerID
	case InviteCreateEvent:
		return ev.ServerID
	case InviteDeleteEvent:
		return ev.ServerID
	}
	return ""
}
