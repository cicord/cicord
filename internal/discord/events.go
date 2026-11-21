package discord

// Event is a gateway event delivered to Session.Open's handler.
type Event interface{ isEvent() }

type ReadyEvent struct {
	User      User
	ServerIDs []string
}

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

type ChannelCreateEvent struct{ Channel Channel }
type ChannelUpdateEvent struct{ Channel Channel }
type ChannelDeleteEvent struct{ Channel Channel }

type RoleCreateEvent struct {
	ServerID string
	Role     Role
}
type RoleUpdateEvent struct {
	ServerID string
	Role     Role
}
type RoleDeleteEvent struct {
	ServerID string
	RoleID   string
}

type ServerUpdateEvent struct{ Server Server }

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

// ServerIDOf returns the server an event belongs to ("" for DMs / global events).
func ServerIDOf(e Event) string {
	switch ev := e.(type) {
	case MemberJoinEvent:
		return ev.Member.ServerID
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
	}
	return ""
}
