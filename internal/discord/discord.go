// Package discord is Cicord's boundary with Discord.
//
// Everything outside this package talks to Discord through the Client / Session /
// Connector interfaces defined here, using Cicord vocabulary ("server", never "guild").
// Two implementations exist:
//   - internal/discord/disgoadapter: the real Discord API (via disgo);
//   - internal/sim: an in-memory simulator used by tests and `cicord dev --sim`.
//
// IDs are Discord snowflakes encoded as decimal strings.
package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	// BannerURL / SplashURL are the CDN URLs of the banner and invite splash ("" = none).
	BannerURL string
	SplashURL string
	// VerificationLevel: none | low | medium | high | highest
	VerificationLevel string
	// DefaultNotifications: allMessages | onlyMentions
	DefaultNotifications string
	// ExplicitContentFilter: disabled | membersWithoutRoles | allMembers
	ExplicitContentFilter string
	SystemChannelID       string
	// SystemChannelFlags are Discord's SUPPRESS_* bits for the system channel.
	SystemChannelFlags     SystemChannelFlags
	RulesChannelID         string
	PublicUpdatesChannelID string
	SafetyAlertsChannelID  string
	// AFKChannelID is the inactive voice channel ("" = none); AFKTimeout is in seconds
	// (60, 300, 900, 1800 or 3600).
	AFKChannelID string
	AFKTimeout   int
	// PreferredLocale is the community language (e.g. "en-US").
	PreferredLocale    string
	PremiumProgressBar bool
	// Features are Discord's server feature flags (COMMUNITY, BANNER, ROLE_ICONS…).
	Features []string
	// PremiumTier is the boost level (0-3); PremiumSubscriptionCount the number of boosts.
	PremiumTier              int
	PremiumSubscriptionCount int
}

// HasFeature reports whether the server has a feature flag.
func (s Server) HasFeature(f string) bool {
	for _, x := range s.Features {
		if x == f {
			return true
		}
	}
	return false
}

// SystemChannelFlags is the system_channel_flags bitset. Every bit SUPPRESSES a kind of
// system message: a cleared bit means the message is sent.
type SystemChannelFlags int

const (
	SuppressJoinNotifications                         SystemChannelFlags = 1 << 0
	SuppressPremiumSubscriptions                      SystemChannelFlags = 1 << 1
	SuppressGuildReminderNotifications                SystemChannelFlags = 1 << 2
	SuppressJoinNotificationReplies                   SystemChannelFlags = 1 << 3
	SuppressRoleSubscriptionPurchaseNotifications     SystemChannelFlags = 1 << 4
	SuppressRoleSubscriptionPurchaseNotificationReply SystemChannelFlags = 1 << 5
)

// ServerParams is a partial update; nil fields are left unchanged.
// An empty string for a channel ID clears it; empty image bytes remove the image.
type ServerParams struct {
	Name                   *string
	Description            *string
	Icon                   *[]byte // raw image bytes (png/jpg/gif/webp)
	Banner                 *[]byte
	Splash                 *[]byte
	VerificationLevel      *string
	DefaultNotifications   *string
	ExplicitContentFilter  *string
	SystemChannelID        *string
	SystemChannelFlags     *SystemChannelFlags
	RulesChannelID         *string
	PublicUpdatesChannelID *string
	SafetyAlertsChannelID  *string
	AFKChannelID           *string
	AFKTimeout             *int
	PreferredLocale        *string
	PremiumProgressBar     *bool
	// Features replaces the MUTABLE feature flags (MutableFeatures); the others are
	// kept by Discord. Callers send the live mutable set ± the flag they change.
	Features *[]string
}

// ServerWidget is the server widget setting (Server Settings → Widget).
type ServerWidget struct {
	Enabled   bool
	ChannelID string // invite channel, "" = none
}

// ───────────────────────────────────────────────────────────── roles

type Role struct {
	ID          string
	Name        string
	Color       int // 0xRRGGBB, 0 = no color (always Colors.Primary)
	Hoist       bool
	Mentionable bool
	Permissions Permissions
	// Position: higher is higher in the hierarchy. @everyone is always 0.
	Position int
	// Managed roles belong to an integration/bot and cannot be edited or assigned.
	Managed bool
	// IconURL is the role icon image ("" = none); UnicodeEmoji the emoji icon.
	IconURL      string
	UnicodeEmoji string
	// Colors is the role style: a gradient when Secondary is set (ENHANCED_ROLE_COLORS).
	Colors RoleColors
}

// RoleColors is a role style. Secondary/Tertiary 0 = none (solid color).
type RoleColors struct {
	Primary   int
	Secondary int
	Tertiary  int
}

// IsEveryone reports whether the role is the @everyone role of serverID.
func (r Role) IsEveryone(serverID string) bool { return r.ID == serverID }

// RoleParams is a partial update; nil fields are unchanged. Colors, when set, also
// sets Color (= Colors.Primary). Empty Icon bytes / "" UnicodeEmoji remove them.
type RoleParams struct {
	Name         *string
	Color        *int
	Colors       *RoleColors
	Hoist        *bool
	Mentionable  *bool
	Permissions  *Permissions
	Icon         *[]byte
	UnicodeEmoji *string
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
	// Voice / stage: RTCRegion "" = automatic; VideoQuality "" (unset) | auto | full.
	RTCRegion    string
	VideoQuality string
	// Thread defaults (text, announcement, forum, media): auto archive in minutes
	// (60, 1440, 4320, 10080; 0 = Discord default) and thread slowmode in seconds.
	DefaultAutoArchive    int
	DefaultThreadSlowmode int
	// Forum / media.
	Tags []ForumTag
	// DefaultReaction is a unicode emoji or "name:id" ("" = none).
	DefaultReaction string
	// DefaultSortOrder: "" (unset) | latestActivity | creationDate.
	DefaultSortOrder string
	// DefaultLayout (forum only): "" (unset) | list | gallery.
	DefaultLayout string
}

// ForumTag is a tag of a forum/media channel. Emoji is unicode or "name:id".
type ForumTag struct {
	ID        string
	Name      string
	Moderated bool
	Emoji     string
}

// ChannelParams is used for create (Type and Name required) and partial update
// (nil fields unchanged; Type ignored). Overwrites, when non-nil, replaces all overwrites.
// ParentID "" removes the channel from its category. Tags, when non-nil, replaces the
// tags: entries with an ID keep that tag (renamed in place), entries without one are
// created.
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

	RTCRegion             *string // "" = automatic
	VideoQuality          *string // auto | full
	DefaultAutoArchive    *int
	DefaultThreadSlowmode *int
	Tags                  *[]ForumTag
	DefaultReaction       *string // "" = none
	DefaultSortOrder      *string // latestActivity | creationDate
	DefaultLayout         *string // list | gallery
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

// ───────────────────────────────────────────────────────────── auto moderation

// AutoMod trigger types (Cicord names of Discord's trigger_type).
const (
	AutoModKeyword       = "keyword"       // 1 KEYWORD
	AutoModSpam          = "spam"          // 3 SPAM
	AutoModKeywordPreset = "keywordPreset" // 4 KEYWORD_PRESET
	AutoModMentionSpam   = "mentionSpam"   // 5 MENTION_SPAM
	AutoModMemberProfile = "memberProfile" // 6 MEMBER_PROFILE
)

// AutoMod action types.
const (
	AutoModActionBlock            = "block"            // 1 BLOCK_MESSAGE
	AutoModActionAlert            = "alert"            // 2 SEND_ALERT_MESSAGE
	AutoModActionTimeout          = "timeout"          // 3 TIMEOUT
	AutoModActionBlockInteraction = "blockInteraction" // 4 BLOCK_MEMBER_INTERACTION
)

// AutoModRule is an auto moderation rule. EventType is derived from Trigger
// (memberProfile → memberUpdate, otherwise messageSend).
type AutoModRule struct {
	ID      string
	Name    string
	Trigger string
	// Trigger metadata (which fields apply depends on Trigger).
	Keywords              []string
	Regex                 []string
	Presets               []string // profanity | sexualContent | slurs
	AllowList             []string
	MentionLimit          int
	MentionRaidProtection bool

	Actions        []AutoModAction
	Enabled        bool
	ExemptRoles    []string
	ExemptChannels []string
}

// AutoModAction is one action of a rule. CustomMessage applies to block, ChannelID to
// alert, DurationSeconds to timeout.
type AutoModAction struct {
	Type            string
	CustomMessage   string
	ChannelID       string
	DurationSeconds int
}

// ───────────────────────────────────────────────────────────── onboarding & welcome screen

// Onboarding is Discord's native onboarding (community servers). Mode: default | advanced.
type Onboarding struct {
	Enabled           bool
	Mode              string
	DefaultChannelIDs []string
	Prompts           []OnboardingPrompt
}

// OnboardingPrompt is a question. Type: multipleChoice | dropdown. ID "" = new.
type OnboardingPrompt struct {
	ID           string
	Type         string
	Title        string
	SingleSelect bool
	Required     bool
	InOnboarding bool
	Options      []OnboardingOption
}

// OnboardingOption is an answer. Emoji is unicode or "name:id". ID "" = new.
type OnboardingOption struct {
	ID          string
	Title       string
	Description string
	Emoji       string
	RoleIDs     []string
	ChannelIDs  []string
}

// WelcomeScreen is shown to new members of community servers.
type WelcomeScreen struct {
	Enabled     bool
	Description string
	Channels    []WelcomeChannel
}

type WelcomeChannel struct {
	ChannelID   string
	Description string
	Emoji       string // unicode or "name:id"
}

// ───────────────────────────────────────────────────────────── expressions

type Emoji struct {
	ID       string
	Name     string
	Animated bool
	RoleIDs  []string
	// Managed emojis belong to an integration (e.g. Twitch) and cannot be edited.
	Managed bool
}

// EmojiParams creates (Name, Image required) or updates (Name, RoleIDs; Image ignored)
// an emoji. RoleIDs nil = unchanged on update.
type EmojiParams struct {
	Name    string
	Image   []byte
	RoleIDs *[]string
}

type Sticker struct {
	ID          string
	Name        string
	Description string
	// Tags is the autocomplete/suggestion emoji name (e.g. "smile").
	Tags string
	// Format: png | apng | lottie | gif
	Format string
}

// StickerParams creates (File required) or updates (File ignored) a sticker.
type StickerParams struct {
	Name        string
	Description string
	Tags        string
	File        []byte
}

type SoundboardSound struct {
	ID     string
	Name   string
	Volume float64 // 0..1
	Emoji  string  // unicode or "name:id", "" = none
}

// SoundParams creates (Sound required: mp3/ogg) or updates (Sound ignored) a sound.
type SoundParams struct {
	Name   string
	Sound  []byte
	Volume float64
	Emoji  string
}

// ───────────────────────────────────────────────────────────── users & members

type User struct {
	PublicFlags uint64
	ID          string
	Username    string
	GlobalName  string // display name, may be empty
	AvatarURL   string
	Bot         bool
}

// DisplayName returns GlobalName if set, Username otherwise.
func (u User) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

// Ban identifies a user banned from a server, including the recorded reason.
type Ban struct {
	User   User
	Reason string
}

type Member struct {
	BannerURL, Bio string
	Pending        *bool
	PremiumSince   *time.Time
	AvatarURL      string
	ServerID       string
	User           User
	Nick           string
	RoleIDs        []string
	JoinedAt       time.Time
	// TimeoutUntil is set while the member is timed out.
	TimeoutUntil *time.Time
	// Flags are Discord's guild member flags (MemberFlagCompletedOnboarding…).
	Flags MemberFlags
}

// MemberFlags is the guild member flags bitset.
type MemberFlags int

const (
	MemberFlagDidRejoin           MemberFlags = 1 << 0
	MemberFlagCompletedOnboarding MemberFlags = 1 << 1
	MemberFlagStartedOnboarding   MemberFlags = 1 << 3
)

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
	RoleIDs []string // nil leaves roles alone; an empty slice clears them
	Nick    *string  // "" resets the nickname
	// TimeoutUntil: non-nil sets a timeout; a pointer to the zero time removes it.
	TimeoutUntil *time.Time
	// VoiceChannelID moves a member connected to voice to that voice channel; a pointer
	// to "" disconnects them (Discord's channel_id: null). It needs MoveMembers, and
	// Connect in the destination channel.
	VoiceChannelID *string
}

type GuildProfile struct {
	Nickname, Bio  *string
	Avatar, Banner *[]byte
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

// AllowedMentions restricts who a message notifies (Discord's allowed_mentions). Users,
// Roles and Everyone allow every mention of that type found in the content ("parse");
// UserIDs and RoleIDs allow only the listed users or roles. A type cannot be both parsed
// and listed (Validate). The zero value notifies no one.
type AllowedMentions struct {
	Users    bool
	Roles    bool
	Everyone bool
	// UserIDs / RoleIDs: at most MaxAllowedMentionIDs snowflakes each.
	UserIDs []string
	RoleIDs []string
	// RepliedUser notifies the author of the message replied to (MessageCreate.ReplyTo).
	RepliedUser bool
}

// MaxAllowedMentionIDs is Discord's limit of ids per allowed_mentions list.
const MaxAllowedMentionIDs = 100

// Validate applies Discord's rules: snowflake ids, at most MaxAllowedMentionIDs per list,
// and a type is never both parsed and listed. Errors wrap ErrBadRequest.
func (a AllowedMentions) Validate() error {
	check := func(kind string, parse bool, ids []string) error {
		if parse && len(ids) > 0 {
			return fmt.Errorf("%w: allowed mentions: %s cannot be both allowed (true) and listed", ErrBadRequest, kind)
		}
		if len(ids) > MaxAllowedMentionIDs {
			return fmt.Errorf("%w: allowed mentions: at most %d %s ids", ErrBadRequest, MaxAllowedMentionIDs, kind)
		}
		for _, id := range ids {
			if !IsSnowflake(id) {
				return fmt.Errorf("%w: allowed mentions: %s id %q is not a snowflake", ErrBadRequest, kind, id)
			}
		}
		return nil
	}
	if err := check("users", a.Users, a.UserIDs); err != nil {
		return err
	}
	return check("roles", a.Roles, a.RoleIDs)
}

// IsSnowflake reports whether s looks like a Discord id (1–20 decimal digits).
func IsSnowflake(s string) bool {
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

// MessageCreate is used to send or edit (full replace of the given fields) a message.
type MessageCreate struct {
	Nonce           string
	EnforceNonce    bool
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

type ReactionSummary struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Me    bool   `json:"me"`
}
type MessageReference struct {
	MessageID string `json:"messageId"`
	ChannelID string `json:"channelId,omitempty"`
	ServerID  string `json:"serverId,omitempty"`
}
type WebhookIdentity struct {
	Username  string `json:"username"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}
type MessagingClient interface {
	SetMessagePin(context.Context, string, string, bool) error
	ChannelPins(context.Context, string) ([]Message, error)
	Crosspost(context.Context, string, string) error
	ReactionUsers(context.Context, string, string, string, int, string) ([]User, error)
	RemoveUserReaction(context.Context, string, string, string, string) error
	Typing(context.Context, string) error
	SendAs(context.Context, string, WebhookIdentity, MessageCreate) (Message, error)
}
type Message struct {
	Reactions   []ReactionSummary
	Reference   *MessageReference
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
	// WebhookID is set for messages sent by a webhook.
	WebhookID string
	// ReplyToID is the message this one replies to ("" when not a reply).
	ReplyToID string
	// StickerIDs are the stickers sent with the message.
	StickerIDs []string
	// Poll is true when the message carries a Discord poll (emptied, like Content,
	// without the MessageContent intent).
	Poll bool
	// System is true for system messages (member joined, boost, pin…), which are not
	// written by their author.
	System bool
	// Interaction is set on a bot's reply to an application command: which command, used
	// by whom (received messages only; Discord sends it for every bot, so the analytics
	// see the commands of bots other than Cicord's).
	Interaction *MessageInteraction
}

// MessageInteraction is the command a message answers.
type MessageInteraction struct {
	Type InteractionType
	// Name is the command's name with its subcommand group and subcommand
	// ("ticket close"); "" when Discord did not send it.
	Name   string
	UserID string
}

type ListMessagesParams struct {
	Limit  int // 1..100, default 50
	Before string
	After  string
}

// ───────────────────────────────────────────────────────────── application commands

type CommandOptionType int

const (
	// OptionSubcommand and OptionSubcommandGroup structure a command: a command holds either
	// plain options or subcommands/groups (at most 25), a group holds only subcommands, and a
	// subcommand holds only plain options.
	OptionSubcommand      CommandOptionType = 1
	OptionSubcommandGroup CommandOptionType = 2
	OptionString          CommandOptionType = 3
	OptionInteger         CommandOptionType = 4
	OptionBoolean         CommandOptionType = 5
	OptionUser            CommandOptionType = 6
	OptionChannel         CommandOptionType = 7
	OptionRole            CommandOptionType = 8
	OptionMentionable     CommandOptionType = 9
	OptionNumber          CommandOptionType = 10
	OptionAttachment      CommandOptionType = 11
)

type CommandChoice struct {
	Name  string
	Value any // string or float64
}

// CommandLabels contains translations only. Canonical names remain stable IDs.
type CommandLabels struct {
	Names        map[string]string `json:"name_localizations,omitempty"`
	Descriptions map[string]string `json:"description_localizations,omitempty"`
}
type CommandOption struct {
	CommandLabels
	Type        CommandOptionType
	Name        string
	Description string
	Required    bool
	Choices     []CommandChoice
	MinValue    *float64
	MaxValue    *float64
	// MinLength / MaxLength bound string options (0..6000; MaxLength >= 1).
	MinLength *int
	MaxLength *int
	// Autocomplete: Discord sends InteractionAutocomplete while the user types this option
	// (string, integer and number options without Choices).
	Autocomplete bool
	// Options of a subcommand (plain options) or of a group (subcommands).
	Options []CommandOption
}

// IsSubcommand reports whether the option is a subcommand or a subcommand group.
func (o CommandOption) IsSubcommand() bool {
	return o.Type == OptionSubcommand || o.Type == OptionSubcommandGroup
}

// Command is a chat-input (slash) command.
type CommandType int

const (
	ChatInput   CommandType = 1
	UserMenu    CommandType = 2
	MessageMenu CommandType = 3
)

func (t CommandType) Key(name string) string {
	switch t {
	case UserMenu:
		return "user:" + name
	case MessageMenu:
		return "message:" + name
	}
	return name
}

type Command struct {
	CommandLabels
	Type        CommandType
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
// channel/role/mentionable/attachment options (IDs), float64 for integer/number, bool for
// boolean. Subcommands and groups have no value: their options are in Options. In an
// autocomplete interaction, the option being typed has Focused set and its Value is the raw
// text typed so far (a string, whatever the option type).
type InteractionOption struct {
	Name    string
	Type    CommandOptionType
	Value   any
	Focused bool
	Options []InteractionOption
}

// CommandPath returns the invoked command path ("ticket", "ticket close", "ticket admin
// purge": the command, then the group and the subcommand when present) and the options of
// the invoked leaf.
func (it Interaction) CommandPath() (path []string, options []InteractionOption) {
	path = []string{it.CommandName}
	options = it.Options
	for len(options) == 1 && (options[0].Type == OptionSubcommandGroup || options[0].Type == OptionSubcommand) {
		path = append(path, options[0].Name)
		options = options[0].Options
	}
	return path, options
}

// FocusedOption returns the option being typed in an autocomplete interaction.
func (it Interaction) FocusedOption() (InteractionOption, bool) {
	_, opts := it.CommandPath()
	for _, o := range opts {
		if o.Focused {
			return o, true
		}
	}
	return InteractionOption{}, false
}

type Resolved struct {
	Users       map[string]User
	Members     map[string]Member
	Roles       map[string]Role
	Channels    map[string]Channel
	Attachments map[string]Attachment
}

type Interaction struct {
	CommandType   CommandType
	TargetMessage *Message
	TargetMember  *Member
	Locale        string
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
	// ResponseAutocomplete answers an InteractionAutocomplete with Choices (at most 25). It
	// is the only valid response to an autocomplete interaction, which cannot be deferred.
	ResponseAutocomplete InteractionResponseType = 8
	ResponseModal        InteractionResponseType = 9
)

// MaxAutocompleteChoices is the most choices an autocomplete response may hold.
const MaxAutocompleteChoices = 25

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
	// Choices for Autocomplete (an empty list shows "no options").
	Choices []CommandChoice
}

// ───────────────────────────────────────────────────────────── client

// Client is the REST surface Cicord uses. All methods are safe for concurrent use.
// Implementations handle rate limits internally (blocking until allowed).
type IncidentActions struct {
	InvitesDisabledUntil *time.Time `json:"invitesDisabledUntil,omitempty"`
	DMsDisabledUntil     *time.Time `json:"dmsDisabledUntil,omitempty"`
}
type IncidentClient interface {
	SetIncident(context.Context, string, IncidentActions) error
}

type Client interface {
	ListAuditLog(ctx context.Context, serverID string, query AuditQuery) ([]AuditLogEntryEvent, error)
	GetUser(ctx context.Context, userID string) (User, error)
	UpdateSelf(ctx context.Context, p SelfParams) (User, error)

	GetServer(ctx context.Context, serverID string) (Server, error)
	UpdateServer(ctx context.Context, serverID string, p ServerParams) (Server, error)
	GetServerWidget(ctx context.Context, serverID string) (ServerWidget, error)
	UpdateServerWidget(ctx context.Context, serverID string, w ServerWidget, reason string) (ServerWidget, error)

	ListAutoModRules(ctx context.Context, serverID string) ([]AutoModRule, error)
	CreateAutoModRule(ctx context.Context, serverID string, r AutoModRule, reason string) (AutoModRule, error)
	// UpdateAutoModRule replaces every field but the trigger type (immutable on Discord).
	UpdateAutoModRule(ctx context.Context, serverID, ruleID string, r AutoModRule, reason string) (AutoModRule, error)
	DeleteAutoModRule(ctx context.Context, serverID, ruleID, reason string) error

	GetOnboarding(ctx context.Context, serverID string) (Onboarding, error)
	// UpdateOnboarding replaces the whole onboarding (prompts and options without ID are created).
	UpdateOnboarding(ctx context.Context, serverID string, o Onboarding, reason string) (Onboarding, error)
	GetWelcomeScreen(ctx context.Context, serverID string) (WelcomeScreen, error)
	UpdateWelcomeScreen(ctx context.Context, serverID string, ws WelcomeScreen, reason string) (WelcomeScreen, error)

	ListEmojis(ctx context.Context, serverID string) ([]Emoji, error)
	CreateEmoji(ctx context.Context, serverID string, p EmojiParams, reason string) (Emoji, error)
	UpdateEmoji(ctx context.Context, serverID, emojiID string, p EmojiParams, reason string) (Emoji, error)
	DeleteEmoji(ctx context.Context, serverID, emojiID, reason string) error
	ListStickers(ctx context.Context, serverID string) ([]Sticker, error)
	CreateSticker(ctx context.Context, serverID string, p StickerParams, reason string) (Sticker, error)
	UpdateSticker(ctx context.Context, serverID, stickerID string, p StickerParams, reason string) (Sticker, error)
	DeleteSticker(ctx context.Context, serverID, stickerID, reason string) error
	ListSoundboardSounds(ctx context.Context, serverID string) ([]SoundboardSound, error)
	CreateSoundboardSound(ctx context.Context, serverID string, p SoundParams, reason string) (SoundboardSound, error)
	UpdateSoundboardSound(ctx context.Context, serverID, soundID string, p SoundParams, reason string) (SoundboardSound, error)
	DeleteSoundboardSound(ctx context.Context, serverID, soundID, reason string) error

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
	UnbanMember(ctx context.Context, serverID, userID, reason string) error
	ListBans(ctx context.Context, serverID, after string, limit int) ([]Ban, error)

	// CreateDM opens (or returns) the DM channel with a user.
	CreateDM(ctx context.Context, userID string) (Channel, error)

	SendMessage(ctx context.Context, channelID string, m MessageCreate) (Message, error)
	EditMessage(ctx context.Context, channelID, messageID string, m MessageCreate) (Message, error)
	DeleteMessage(ctx context.Context, channelID, messageID, reason string) error
	BulkDeleteMessages(ctx context.Context, channelID string, ids []string, reason string) error
	ListMessages(ctx context.Context, channelID string, p ListMessagesParams) ([]Message, error)
	// GetMessage returns one message of a channel (ErrNotFound when it does not exist).
	GetMessage(ctx context.Context, channelID, messageID string) (Message, error)
	AddReaction(ctx context.Context, channelID, messageID, emoji string) error
	RemoveOwnReaction(ctx context.Context, channelID, messageID, emoji string) error

	// SetServerCommands overwrites all commands of the application in a server.
	SetServerCommands(ctx context.Context, applicationID, serverID string, cmds []Command) error

	RespondInteraction(ctx context.Context, interactionID, token string, r InteractionResponse) error
	EditOriginalResponse(ctx context.Context, applicationID, token string, m MessageCreate) (Message, error)
	CreateFollowup(ctx context.Context, applicationID, token string, m MessageCreate) (Message, error)
}

// MemberLister is implemented by clients able to list every member of a server. Discord
// has no "members of a role" endpoint: callers list the members and filter them. The
// Discord API pages GET /guilds/{id}/members by 1000 members per request (MaxListedMembers
// at most) and answers ErrForbidden when the application does not have the privileged
// ServerMembers intent enabled in the developer portal.
type MemberLister interface {
	// ListMembers returns the server's members in ascending user ID order.
	ListMembers(ctx context.Context, serverID string) ([]Member, error)
}

// MaxListedMembers bounds ListMembers: a larger server is an error, not a partial list.
const MaxListedMembers = 100_000

// SnowflakeTime decodes the creation timestamp of a Discord snowflake.
func SnowflakeTime(id string) time.Time {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(n>>22) + 1420070400000).UTC()
}

// SnowflakeLess compares two snowflakes (decimal IDs without leading zeros) numerically.
func SnowflakeLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// Session is one bot identity: REST client + gateway connection.
type Session interface {
	Client
	UpdateGuildProfile(ctx context.Context, serverID string, p GuildProfile) error
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

// GatewayStatusReader is implemented by sessions that know whether their gateway
// connection is up (READY, as opposed to connecting, resuming or closed). The runtime
// reports it as a metric; sessions without it count as connected while they are open.
type GatewayStatusReader interface {
	GatewayConnected() bool
}

// Connector creates sessions from bot tokens.
type Connector interface {
	// Connect validates the token and returns a session (gateway not opened yet).
	Connect(ctx context.Context, token string) (Session, error)
}
