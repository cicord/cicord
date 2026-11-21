// Package discord is CODI's boundary with Discord.
//
// Everything outside this package talks to Discord through the Client / Session /
// Connector interfaces defined here, using CODI vocabulary ("server", never "guild").
// Two implementations exist:
//   - internal/discord/disgoadapter: the real Discord API (via disgo);
//   - internal/sim: an in-memory simulator used by tests and `codi dev --sim`.
//
// IDs are Discord snowflakes encoded as decimal strings.
package discord

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors. Implementations wrap them so callers can use errors.Is.
var (
	ErrNotFound     = errors.New("discord: not found")
	ErrForbidden    = errors.New("discord: missing permissions")
	ErrInvalidToken = errors.New("discord: invalid token")
	ErrBadRequest   = errors.New("discord: bad request")
)

// ───────────────────────────────────────────────────────────── servers

// Server is a Discord server (a "guild" in the Discord API).
type Server struct {
	ID          string
	Name        string
	Description string
	OwnerID     string
	IconURL     string
	// VerificationLevel: none | low | medium | high | highest
	VerificationLevel string
	// DefaultNotifications: allMessages | onlyMentions
	DefaultNotifications string
	SystemChannelID      string
	RulesChannelID       string
}

// ServerParams is a partial update; nil fields are left unchanged.
// An empty string for a channel ID clears it.
type ServerParams struct {
	Name                 *string
	Description          *string
	Icon                 *[]byte // raw image bytes (png/jpg/gif)
	VerificationLevel    *string
	DefaultNotifications *string
	SystemChannelID      *string
	RulesChannelID       *string
}

// ───────────────────────────────────────────────────────────── roles

type Role struct {
	ID          string
	Name        string
	Color       int // 0xRRGGBB, 0 = no color
	Hoist       bool
	Mentionable bool
	Permissions Permissions
	// Position: higher is higher in the hierarchy. @everyone is always 0.
	Position int
	// Managed roles belong to an integration/bot and cannot be edited or assigned.
	Managed bool
}

// IsEveryone reports whether the role is the @everyone role of serverID.
func (r Role) IsEveryone(serverID string) bool { return r.ID == serverID }

type RoleParams struct {
	Name        *string
	Color       *int
	Hoist       *bool
	Mentionable *bool
	Permissions *Permissions
}

type RolePosition struct {
	ID       string
	Position int
}

// ───────────────────────────────────────────────────────────── channels

type OverwriteType int

const (
	OverwriteRole   OverwriteType = 0
	OverwriteMember OverwriteType = 1
)

type Overwrite struct {
	ID    string // role ID (the server ID for @everyone) or user ID
	Type  OverwriteType
	Allow Permissions
	Deny  Permissions
}

type Channel struct {
	ID       string
	ServerID string // empty for DMs
	Name     string
	Type     ChannelType
	ParentID string // category ID, empty if none
	Position int
	Topic    string
	NSFW     bool
	// RateLimitPerUser is the slowmode in seconds.
	RateLimitPerUser int
	Bitrate          int
	UserLimit        int
	Overwrites       []Overwrite
}

// ChannelParams is used for create (Type and Name required) and partial update
// (nil fields unchanged; Type ignored). Overwrites, when non-nil, replaces all overwrites.
// ParentID "" removes the channel from its category.
type ChannelParams struct {
	Name             *string
	Type             ChannelType
	ParentID         *string
	Position         *int
	Topic            *string
	NSFW             *bool
	RateLimitPerUser *int
	Bitrate          *int
	UserLimit        *int
	Overwrites       *[]Overwrite
	// LockPermissions syncs overwrites with the parent category (update only).
	LockPermissions *bool
}

type ChannelPosition struct {
	ID       string
	Position int
	ParentID *string // nil = unchanged, "" = no category
}

type ThreadParams struct {
	Name      string
	Private   bool
	MessageID string // start the thread from this message (public threads only)
}

// ───────────────────────────────────────────────────────────── users & members

type User struct {
	ID         string
	Username   string
	GlobalName string // display name, may be empty
	AvatarURL  string
	Bot        bool
}

// DisplayName returns GlobalName if set, Username otherwise.
func (u User) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

type Member struct {
	ServerID string
	User     User
	Nick     string
	RoleIDs  []string
	JoinedAt time.Time
	// TimeoutUntil is set while the member is timed out.
	TimeoutUntil *time.Time
}

// DisplayName returns the nickname, else the user's display name.
func (m Member) DisplayName() string {
	if m.Nick != "" {
		return m.Nick
	}
	return m.User.DisplayName()
}

func (m Member) HasRole(roleID string) bool {
	for _, id := range m.RoleIDs {
		if id == roleID {
			return true
		}
	}
	return false
}

type MemberParams struct {
	Nick *string // "" resets the nickname
	// TimeoutUntil: non-nil sets a timeout; a pointer to the zero time removes it.
	TimeoutUntil *time.Time
}

type SelfParams struct {
	Username *string
	Avatar   *[]byte
	Banner   *[]byte
}

type Presence struct {
	Status       string // online | idle | dnd | invisible
	ActivityType string // playing | streaming | listening | watching | competing | custom | "" (none)
	ActivityName string
	ActivityURL  string
}

// ───────────────────────────────────────────────────────────── messages

const (
	MessageFlagEphemeral MessageFlags = 1 << 6
)

type MessageFlags int

type EmbedField struct {
	Name   string
	Value  string
	Inline bool
}

type EmbedAuthor struct {
	Name    string
	URL     string
	IconURL string
}

type EmbedFooter struct {
	Text    string
	IconURL string
}

type Embed struct {
	Title       string
	Description string
	URL         string
	Color       int
	Timestamp   *time.Time
	Fields      []EmbedField
	Author      *EmbedAuthor
	Footer      *EmbedFooter
	ImageURL    string
	ThumbURL    string
}

type ComponentType int

const (
	ComponentActionRow    ComponentType = 1
	ComponentButton       ComponentType = 2
	ComponentStringSelect ComponentType = 3
	ComponentTextInput    ComponentType = 4
)

type ButtonStyle int

const (
	ButtonPrimary   ButtonStyle = 1
	ButtonSecondary ButtonStyle = 2
	ButtonSuccess   ButtonStyle = 3
	ButtonDanger    ButtonStyle = 4
	ButtonLink      ButtonStyle = 5
)

type SelectOption struct {
	Label       string
	Value       string
	Description string
	Emoji       string
	Default     bool
}

// Component is a message or modal component. Which fields apply depends on Type.
type Component struct {
	Type ComponentType
	// Action row
	Components []Component
	// Button / select / text input
	CustomID string
	Label    string
	Disabled bool
	// Button
	Style ButtonStyle
	Emoji string
	URL   string
	// Select
	Placeholder string
	MinValues   *int
	MaxValues   *int
	Options     []SelectOption
	// Text input (modals). TextStyle: 1 short, 2 paragraph.
	TextStyle int
	Required  bool
	Value     string
	MinLength *int
	MaxLength *int
}

type AllowedMentions struct {
	Users    bool
	Roles    bool
	Everyone bool
}

// MessageCreate is used to send or edit (full replace of the given fields) a message.
type MessageCreate struct {
	Content         string
	Embeds          []Embed
	Components      []Component
	AllowedMentions *AllowedMentions // nil = Discord default
	Flags           MessageFlags
	ReplyTo         string // message ID to reply to
}

type Attachment struct {
	ID          string
	Filename    string
	URL         string
	ContentType string
	Size        int
}

type Message struct {
	ID          string
	ChannelID   string
	ServerID    string // empty in DMs
	Author      User
	Member      *Member // set for server messages when available
	Content     string
	Embeds      []Embed
	Components  []Component
	Attachments []Attachment
	Flags       MessageFlags
	CreatedAt   time.Time
	EditedAt    *time.Time
}

type ListMessagesParams struct {
	Limit  int // 1..100, default 50
	Before string
	After  string
}

// ───────────────────────────────────────────────────────────── application commands

type CommandOptionType int

const (
	OptionString      CommandOptionType = 3
	OptionInteger     CommandOptionType = 4
	OptionBoolean     CommandOptionType = 5
	OptionUser        CommandOptionType = 6
	OptionChannel     CommandOptionType = 7
	OptionRole        CommandOptionType = 8
	OptionMentionable CommandOptionType = 9
	OptionNumber      CommandOptionType = 10
	OptionAttachment  CommandOptionType = 11
)

type CommandChoice struct {
	Name  string
	Value any // string or float64
}

type CommandOption struct {
	Type        CommandOptionType
	Name        string
	Description string
	Required    bool
	Choices     []CommandChoice
	MinValue    *float64
	MaxValue    *float64
}

// Command is a chat-input (slash) command.
type Command struct {
	Name        string
	Description string
	Options     []CommandOption
	// DefaultMemberPermissions: nil = everyone can use it.
	DefaultMemberPermissions *Permissions
}

// ───────────────────────────────────────────────────────────── interactions

type InteractionType int

const (
	InteractionCommand      InteractionType = 2
	InteractionComponent    InteractionType = 3
	InteractionAutocomplete InteractionType = 4
	InteractionModalSubmit  InteractionType = 5
)

// InteractionOption is a received command option. Value is string for string/user/
// channel/role/mentionable/attachment options (IDs), float64 for integer/number, bool for boolean.
type InteractionOption struct {
	Name  string
	Type  CommandOptionType
	Value any
}

type Resolved struct {
	Users       map[string]User
	Members     map[string]Member
	Roles       map[string]Role
	Channels    map[string]Channel
	Attachments map[string]Attachment
}

type Interaction struct {
	ID            string
	Token         string
	ApplicationID string
	Type          InteractionType
	ServerID      string // empty in DMs
	ChannelID     string
	User          User
	Member        *Member // nil in DMs
	// Command
	CommandName string
	Options     []InteractionOption
	Resolved    Resolved
	// Component
	CustomID      string
	ComponentType ComponentType
	Values        []string
	Message       *Message
	// Modal submit: text input custom ID → value
	ModalFields map[string]string
}

type InteractionResponseType int

const (
	ResponseMessage         InteractionResponseType = 4
	ResponseDeferredMessage InteractionResponseType = 5
	ResponseDeferredUpdate  InteractionResponseType = 6
	ResponseUpdateMessage   InteractionResponseType = 7
	ResponseModal           InteractionResponseType = 9
)

type Modal struct {
	CustomID   string
	Title      string
	Components []Component // action rows of text inputs
}

type InteractionResponse struct {
	Type InteractionResponseType
	// Data for Message / UpdateMessage; for DeferredMessage only Flags is used.
	Data  *MessageCreate
	Modal *Modal
}

// ───────────────────────────────────────────────────────────── client

// Client is the REST surface CODI uses. All methods are safe for concurrent use.
// Implementations handle rate limits internally (blocking until allowed).
type Client interface {
	GetUser(ctx context.Context, userID string) (User, error)
	UpdateSelf(ctx context.Context, p SelfParams) (User, error)

	GetServer(ctx context.Context, serverID string) (Server, error)
	UpdateServer(ctx context.Context, serverID string, p ServerParams) (Server, error)

	ListRoles(ctx context.Context, serverID string) ([]Role, error)
	CreateRole(ctx context.Context, serverID string, p RoleParams, reason string) (Role, error)
	UpdateRole(ctx context.Context, serverID, roleID string, p RoleParams, reason string) (Role, error)
	DeleteRole(ctx context.Context, serverID, roleID, reason string) error
	SetRolePositions(ctx context.Context, serverID string, positions []RolePosition) error

	// ListChannels returns all channels of a server, categories included (Type == ChannelCategory).
	ListChannels(ctx context.Context, serverID string) ([]Channel, error)
	GetChannel(ctx context.Context, channelID string) (Channel, error)
	CreateChannel(ctx context.Context, serverID string, p ChannelParams, reason string) (Channel, error)
	UpdateChannel(ctx context.Context, channelID string, p ChannelParams, reason string) (Channel, error)
	DeleteChannel(ctx context.Context, channelID, reason string) error
	SetChannelPositions(ctx context.Context, serverID string, positions []ChannelPosition) error
	CreateThread(ctx context.Context, channelID string, p ThreadParams) (Channel, error)

	GetMember(ctx context.Context, serverID, userID string) (Member, error)
	UpdateMember(ctx context.Context, serverID, userID string, p MemberParams, reason string) (Member, error)
	AddMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error
	RemoveMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error
	KickMember(ctx context.Context, serverID, userID, reason string) error
	BanMember(ctx context.Context, serverID, userID string, deleteMessageSeconds int, reason string) error

	// CreateDM opens (or returns) the DM channel with a user.
	CreateDM(ctx context.Context, userID string) (Channel, error)

	SendMessage(ctx context.Context, channelID string, m MessageCreate) (Message, error)
	EditMessage(ctx context.Context, channelID, messageID string, m MessageCreate) (Message, error)
	DeleteMessage(ctx context.Context, channelID, messageID, reason string) error
	ListMessages(ctx context.Context, channelID string, p ListMessagesParams) ([]Message, error)
	AddReaction(ctx context.Context, channelID, messageID, emoji string) error
	RemoveOwnReaction(ctx context.Context, channelID, messageID, emoji string) error

	// SetServerCommands overwrites all commands of the application in a server.
	SetServerCommands(ctx context.Context, applicationID, serverID string, cmds []Command) error

	RespondInteraction(ctx context.Context, interactionID, token string, r InteractionResponse) error
	EditOriginalResponse(ctx context.Context, applicationID, token string, m MessageCreate) (Message, error)
	CreateFollowup(ctx context.Context, applicationID, token string, m MessageCreate) (Message, error)
}

// Session is one bot identity: REST client + gateway connection.
type Session interface {
	Client
	// Self is the bot user. Valid after Connect.
	Self() User
	// ApplicationID is the bot's application ID. Valid after Connect.
	ApplicationID() string
	// Open connects to the gateway. handler is called sequentially, from a single
	// goroutine, for every event; it must not block for long.
	Open(ctx context.Context, intents Intents, handler func(Event)) error
	SetPresence(ctx context.Context, p Presence) error
	Close(ctx context.Context) error
}

// Connector creates sessions from bot tokens.
type Connector interface {
	// Connect validates the token and returns a session (gateway not opened yet).
	Connect(ctx context.Context, token string) (Session, error)
}
