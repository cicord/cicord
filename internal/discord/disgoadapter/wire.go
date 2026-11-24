package disgoadapter

// Wire types mirror the Discord API v10 JSON objects (only the fields Cicord uses).
//
// The adapter decodes every REST response and every gateway dispatch payload into
// these flat structs instead of disgo's typed models: it keeps one mapping layer
// (wire ↔ Cicord) shared by REST and gateway, preserves option order in interactions,
// and lets request bodies express JSON null where the contract needs it (clearing a
// system channel, removing a category, lifting a timeout). disgo is still used for
// the transport: rest.Client (endpoints, auth, rate limiting) and gateway.Gateway
// (identify, heartbeat, resume, reconnect).

import (
	"encoding/json"
	"time"
)

type wireUser struct {
	ID         string  `json:"id"`
	Username   string  `json:"username"`
	GlobalName *string `json:"global_name"`
	Avatar     *string `json:"avatar"`
	Bot        bool    `json:"bot"`
}

type wireApplication struct {
	ID    string `json:"id"`
	Flags int    `json:"flags"`
}

type wireMember struct {
	User                       *wireUser  `json:"user,omitempty"`
	Nick                       *string    `json:"nick"`
	Roles                      []string   `json:"roles"`
	JoinedAt                   *time.Time `json:"joined_at"`
	CommunicationDisabledUntil *time.Time `json:"communication_disabled_until"`
	// GuildID is only present in some gateway payloads (GUILD_MEMBER_ADD / UPDATE).
	GuildID string `json:"guild_id,omitempty"`
}

type wireRole struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       int    `json:"color"`
	Hoist       bool   `json:"hoist"`
	Mentionable bool   `json:"mentionable"`
	Permissions string `json:"permissions"`
	Position    int    `json:"position"`
	Managed     bool   `json:"managed"`
	// Role icon (ROLE_ICONS) and style (ENHANCED_ROLE_COLORS).
	Icon         *string         `json:"icon"`
	UnicodeEmoji *string         `json:"unicode_emoji"`
	Colors       *wireRoleColors `json:"colors,omitempty"`
}

type wireRoleColors struct {
	PrimaryColor   int  `json:"primary_color"`
	SecondaryColor *int `json:"secondary_color"`
	TertiaryColor  *int `json:"tertiary_color"`
}

type wireOverwrite struct {
	ID    string `json:"id"`
	Type  int    `json:"type"`
	Allow string `json:"allow"`
	Deny  string `json:"deny"`
}

type wireChannel struct {
	ID                   string          `json:"id"`
	GuildID              string          `json:"guild_id,omitempty"`
	Name                 string          `json:"name"`
	Type                 int             `json:"type"`
	ParentID             *string         `json:"parent_id"`
	Position             int             `json:"position"`
	Topic                *string         `json:"topic"`
	NSFW                 bool            `json:"nsfw"`
	RateLimitPerUser     int             `json:"rate_limit_per_user"`
	Bitrate              int             `json:"bitrate"`
	UserLimit            int             `json:"user_limit"`
	PermissionOverwrites []wireOverwrite `json:"permission_overwrites"`

	RTCRegion                     *string              `json:"rtc_region"`
	VideoQualityMode              int                  `json:"video_quality_mode"`
	DefaultAutoArchiveDuration    int                  `json:"default_auto_archive_duration"`
	DefaultThreadRateLimitPerUser int                  `json:"default_thread_rate_limit_per_user"`
	AvailableTags                 []wireForumTag       `json:"available_tags"`
	DefaultReactionEmoji          *wireDefaultReaction `json:"default_reaction_emoji"`
	DefaultSortOrder              *int                 `json:"default_sort_order"`
	DefaultForumLayout            int                  `json:"default_forum_layout"`
}

type wireForumTag struct {
	ID        string  `json:"id,omitempty"`
	Name      string  `json:"name"`
	Moderated bool    `json:"moderated"`
	EmojiID   *string `json:"emoji_id"`
	EmojiName *string `json:"emoji_name"`
}

type wireDefaultReaction struct {
	EmojiID   *string `json:"emoji_id"`
	EmojiName *string `json:"emoji_name"`
}

type wireGuild struct {
	ID                          string  `json:"id"`
	Name                        string  `json:"name"`
	Description                 *string `json:"description"`
	OwnerID                     string  `json:"owner_id"`
	Icon                        *string `json:"icon"`
	VerificationLevel           int     `json:"verification_level"`
	DefaultMessageNotifications int     `json:"default_message_notifications"`
	SystemChannelID             *string `json:"system_channel_id"`
	RulesChannelID              *string `json:"rules_channel_id"`

	Banner                    *string  `json:"banner"`
	Splash                    *string  `json:"splash"`
	ExplicitContentFilter     int      `json:"explicit_content_filter"`
	SystemChannelFlags        int      `json:"system_channel_flags"`
	PublicUpdatesChannelID    *string  `json:"public_updates_channel_id"`
	SafetyAlertsChannelID     *string  `json:"safety_alerts_channel_id"`
	AFKChannelID              *string  `json:"afk_channel_id"`
	AFKTimeout                int      `json:"afk_timeout"`
	PreferredLocale           string   `json:"preferred_locale"`
	PremiumProgressBarEnabled bool     `json:"premium_progress_bar_enabled"`
	Features                  []string `json:"features"`
	PremiumTier               int      `json:"premium_tier"`
	PremiumSubscriptionCount  *int     `json:"premium_subscription_count"`
}

type wireWidget struct {
	Enabled   bool    `json:"enabled"`
	ChannelID *string `json:"channel_id"`
}

// ───────────────────────────────────────────────────────────── auto moderation

type wireAutoModMetadata struct {
	KeywordFilter                []string `json:"keyword_filter,omitempty"`
	RegexPatterns                []string `json:"regex_patterns,omitempty"`
	Presets                      []int    `json:"presets,omitempty"`
	AllowList                    []string `json:"allow_list,omitempty"`
	MentionTotalLimit            int      `json:"mention_total_limit,omitempty"`
	MentionRaidProtectionEnabled bool     `json:"mention_raid_protection_enabled,omitempty"`
}

type wireAutoModActionMetadata struct {
	ChannelID       string `json:"channel_id,omitempty"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
	CustomMessage   string `json:"custom_message,omitempty"`
}

type wireAutoModAction struct {
	Type     int                        `json:"type"`
	Metadata *wireAutoModActionMetadata `json:"metadata,omitempty"`
}

type wireAutoModRule struct {
	ID              string              `json:"id,omitempty"`
	GuildID         string              `json:"guild_id,omitempty"`
	CreatorID       string              `json:"creator_id,omitempty"`
	Name            string              `json:"name"`
	EventType       int                 `json:"event_type"`
	TriggerType     int                 `json:"trigger_type,omitempty"`
	TriggerMetadata wireAutoModMetadata `json:"trigger_metadata"`
	Actions         []wireAutoModAction `json:"actions"`
	Enabled         bool                `json:"enabled"`
	ExemptRoles     []string            `json:"exempt_roles"`
	ExemptChannels  []string            `json:"exempt_channels"`
}

// ───────────────────────────────────────────────────────────── onboarding & welcome screen

// wireOnboardingOption is read with an "emoji" object and written with the flat
// emoji_id / emoji_name / emoji_animated fields (Discord's documented write form).
type wireOnboardingOption struct {
	ID            string     `json:"id"`
	ChannelIDs    []string   `json:"channel_ids"`
	RoleIDs       []string   `json:"role_ids"`
	Emoji         *wireEmoji `json:"emoji,omitempty"`
	EmojiID       *string    `json:"emoji_id,omitempty"`
	EmojiName     *string    `json:"emoji_name,omitempty"`
	EmojiAnimated *bool      `json:"emoji_animated,omitempty"`
	Title         string     `json:"title"`
	Description   *string    `json:"description"`
}

type wireOnboardingPrompt struct {
	ID           string                 `json:"id"`
	Type         int                    `json:"type"`
	Options      []wireOnboardingOption `json:"options"`
	Title        string                 `json:"title"`
	SingleSelect bool                   `json:"single_select"`
	Required     bool                   `json:"required"`
	InOnboarding bool                   `json:"in_onboarding"`
}

type wireOnboarding struct {
	GuildID           string                 `json:"guild_id,omitempty"`
	Prompts           []wireOnboardingPrompt `json:"prompts"`
	DefaultChannelIDs []string               `json:"default_channel_ids"`
	Enabled           bool                   `json:"enabled"`
	Mode              int                    `json:"mode"`
}

type wireWelcomeChannel struct {
	ChannelID   string  `json:"channel_id"`
	Description string  `json:"description"`
	EmojiID     *string `json:"emoji_id"`
	EmojiName   *string `json:"emoji_name"`
}

type wireWelcomeScreen struct {
	Enabled         *bool                `json:"enabled,omitempty"`
	Description     *string              `json:"description"`
	WelcomeChannels []wireWelcomeChannel `json:"welcome_channels"`
}

// ───────────────────────────────────────────────────────────── expressions

type wireGuildEmoji struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Animated bool     `json:"animated"`
	Roles    []string `json:"roles"`
	Managed  bool     `json:"managed"`
}

type wireSticker struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Tags        string  `json:"tags"`
	FormatType  int     `json:"format_type"`
}

type wireSound struct {
	SoundID   string  `json:"sound_id"`
	Name      string  `json:"name"`
	Volume    float64 `json:"volume"`
	EmojiID   *string `json:"emoji_id"`
	EmojiName *string `json:"emoji_name"`
}

type wireSoundList struct {
	Items []wireSound `json:"items"`
}

// Gateway payloads of the expression events.
type wireEmojisUpdate struct {
	GuildID string           `json:"guild_id"`
	Emojis  []wireGuildEmoji `json:"emojis"`
}

type wireStickersUpdate struct {
	GuildID  string        `json:"guild_id"`
	Stickers []wireSticker `json:"stickers"`
}

type wireSoundEvent struct {
	wireSound
	GuildID string `json:"guild_id"`
}

type wireSoundsUpdate struct {
	GuildID string      `json:"guild_id"`
	Sounds  []wireSound `json:"soundboard_sounds"`
}

type wireEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type wireEmbedAuthor struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

type wireEmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

type wireEmbedMedia struct {
	URL string `json:"url"`
}

type wireEmbed struct {
	Type        string           `json:"type,omitempty"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	URL         string           `json:"url,omitempty"`
	Color       int              `json:"color,omitempty"`
	Timestamp   *time.Time       `json:"timestamp,omitempty"`
	Fields      []wireEmbedField `json:"fields,omitempty"`
	Author      *wireEmbedAuthor `json:"author,omitempty"`
	Footer      *wireEmbedFooter `json:"footer,omitempty"`
	Image       *wireEmbedMedia  `json:"image,omitempty"`
	Thumbnail   *wireEmbedMedia  `json:"thumbnail,omitempty"`
}

type wireEmoji struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Animated bool   `json:"animated,omitempty"`
}

type wireSelectOption struct {
	Label       string     `json:"label"`
	Value       string     `json:"value"`
	Description string     `json:"description,omitempty"`
	Emoji       *wireEmoji `json:"emoji,omitempty"`
	Default     bool       `json:"default,omitempty"`
}

// wireComponent is the union of all component shapes Cicord uses. Fields are
// omitted when empty so a button does not carry select fields, etc.
type wireComponent struct {
	Type        int                `json:"type"`
	Components  []wireComponent    `json:"components,omitempty"`
	CustomID    string             `json:"custom_id,omitempty"`
	Label       string             `json:"label,omitempty"`
	Disabled    bool               `json:"disabled,omitempty"`
	Style       int                `json:"style,omitempty"`
	Emoji       *wireEmoji         `json:"emoji,omitempty"`
	URL         string             `json:"url,omitempty"`
	Placeholder string             `json:"placeholder,omitempty"`
	MinValues   *int               `json:"min_values,omitempty"`
	MaxValues   *int               `json:"max_values,omitempty"`
	Options     []wireSelectOption `json:"options,omitempty"`
	Required    *bool              `json:"required,omitempty"`
	Value       string             `json:"value,omitempty"`
	MinLength   *int               `json:"min_length,omitempty"`
	MaxLength   *int               `json:"max_length,omitempty"`
	// Received only: modal submits may wrap inputs in label components (type 18),
	// whose child is in "component"; select menus in modals report "values".
	Component *wireComponent `json:"component,omitempty"`
	Values    []string       `json:"values,omitempty"`
}

type wireAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

type wireMessage struct {
	ID              string           `json:"id"`
	ChannelID       string           `json:"channel_id"`
	GuildID         string           `json:"guild_id,omitempty"`
	Author          wireUser         `json:"author"`
	Member          *wireMember      `json:"member,omitempty"`
	Content         string           `json:"content"`
	Embeds          []wireEmbed      `json:"embeds"`
	Components      []wireComponent  `json:"components"`
	Attachments     []wireAttachment `json:"attachments"`
	Flags           int              `json:"flags"`
	Timestamp       time.Time        `json:"timestamp"`
	EditedTimestamp *time.Time       `json:"edited_timestamp"`
}

type wireAllowedMentions struct {
	// Parse must always be emitted: an empty list disables all mentions, while an
	// absent allowed_mentions object means "Discord default".
	Parse []string `json:"parse"`
}

type wireMessageReference struct {
	MessageID       string `json:"message_id"`
	FailIfNotExists bool   `json:"fail_if_not_exists"`
}

// wireMessageSend is the body of create-message style requests (channel messages,
// interaction responses, followups). For edits, Content/Embeds/Components are sent
// even when empty (full replace), see messageEditBody.
type wireMessageSend struct {
	Content          string                `json:"content,omitempty"`
	Embeds           []wireEmbed           `json:"embeds,omitempty"`
	Components       []wireComponent       `json:"components,omitempty"`
	AllowedMentions  *wireAllowedMentions  `json:"allowed_mentions,omitempty"`
	Flags            int                   `json:"flags,omitempty"`
	MessageReference *wireMessageReference `json:"message_reference,omitempty"`
}

type wireMessageEdit struct {
	Content         string               `json:"content"`
	Embeds          []wireEmbed          `json:"embeds"`
	Components      []wireComponent      `json:"components"`
	AllowedMentions *wireAllowedMentions `json:"allowed_mentions,omitempty"`
	Flags           *int                 `json:"flags,omitempty"`
}

type wireCommandChoice struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type wireCommandOption struct {
	Type        int                 `json:"type"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Required    bool                `json:"required,omitempty"`
	Choices     []wireCommandChoice `json:"choices,omitempty"`
	MinValue    *float64            `json:"min_value,omitempty"`
	MaxValue    *float64            `json:"max_value,omitempty"`
}

type wireCommand struct {
	Type                     int                 `json:"type"`
	Name                     string              `json:"name"`
	Description              string              `json:"description"`
	Options                  []wireCommandOption `json:"options,omitempty"`
	DefaultMemberPermissions *string             `json:"default_member_permissions,omitempty"`
}

type wireModal struct {
	CustomID   string          `json:"custom_id"`
	Title      string          `json:"title"`
	Components []wireComponent `json:"components"`
}

type wireInteractionResponse struct {
	Type int `json:"type"`
	Data any `json:"data,omitempty"`
}

// ───────────────────────────────────────────────────────────── received interactions

type wireInteractionOption struct {
	Name    string                  `json:"name"`
	Type    int                     `json:"type"`
	Value   json.RawMessage         `json:"value,omitempty"`
	Options []wireInteractionOption `json:"options,omitempty"`
}

type wireResolved struct {
	Users       map[string]wireUser       `json:"users"`
	Members     map[string]wireMember     `json:"members"`
	Roles       map[string]wireRole       `json:"roles"`
	Channels    map[string]wireChannel    `json:"channels"`
	Attachments map[string]wireAttachment `json:"attachments"`
}

type wireInteractionData struct {
	// Application command
	ID       string                  `json:"id"`
	Name     string                  `json:"name"`
	Options  []wireInteractionOption `json:"options"`
	Resolved *wireResolved           `json:"resolved"`
	// Component / modal submit
	CustomID      string          `json:"custom_id"`
	ComponentType int             `json:"component_type"`
	Values        []string        `json:"values"`
	Components    []wireComponent `json:"components"`
}

type wireInteraction struct {
	ID            string               `json:"id"`
	ApplicationID string               `json:"application_id"`
	Type          int                  `json:"type"`
	Token         string               `json:"token"`
	GuildID       string               `json:"guild_id,omitempty"`
	ChannelID     string               `json:"channel_id,omitempty"`
	Channel       *wireChannel         `json:"channel,omitempty"`
	Member        *wireMember          `json:"member,omitempty"`
	User          *wireUser            `json:"user,omitempty"`
	Data          *wireInteractionData `json:"data,omitempty"`
	Message       *wireMessage         `json:"message,omitempty"`
}

// ───────────────────────────────────────────────────────────── gateway payloads

type wireReady struct {
	User   wireUser `json:"user"`
	Guilds []struct {
		ID string `json:"id"`
	} `json:"guilds"`
	Application wireApplication `json:"application"`
}

type wireGuildCreate struct {
	ID          string       `json:"id"`
	Unavailable bool         `json:"unavailable"`
	Members     []wireMember `json:"members"`
}

type wireGuildDelete struct {
	ID          string `json:"id"`
	Unavailable bool   `json:"unavailable"`
}

type wireMembersChunk struct {
	GuildID string       `json:"guild_id"`
	Members []wireMember `json:"members"`
}

type wireMemberRemove struct {
	GuildID string   `json:"guild_id"`
	User    wireUser `json:"user"`
}

type wireMessageDelete struct {
	ID        string   `json:"id"`
	IDs       []string `json:"ids"`
	ChannelID string   `json:"channel_id"`
	GuildID   string   `json:"guild_id"`
}

type wireReaction struct {
	UserID    string      `json:"user_id"`
	ChannelID string      `json:"channel_id"`
	MessageID string      `json:"message_id"`
	GuildID   string      `json:"guild_id"`
	Member    *wireMember `json:"member"`
	Emoji     wireEmoji   `json:"emoji"`
}

type wireRoleEvent struct {
	GuildID string   `json:"guild_id"`
	Role    wireRole `json:"role"`
	RoleID  string   `json:"role_id"`
}
