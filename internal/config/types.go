// Package config loads and validates a Cicord repository (cicord.yaml, servers/, bots/,
// plugins) into typed structures. See https://cicord.dev/docs/configuration/server/ for the semantics.
package config

import (
	"strconv"
	"strings"
)

const APIVersion = "cicord.dev/v1alpha1"

type Metadata struct {
	Name        string            `yaml:"name" json:"name"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// ───────────────────────────────────────────────────────────── Project

type Project struct {
	APIVersion string      `yaml:"apiVersion" json:"apiVersion"`
	Kind       string      `yaml:"kind" json:"kind"`
	Metadata   Metadata    `yaml:"metadata" json:"metadata"`
	Spec       ProjectSpec `yaml:"spec" json:"spec"`
}

type ProjectSpec struct {
	Engine     string         `yaml:"engine" json:"engine"`
	Source     *SourceSpec    `yaml:"source,omitempty" json:"source,omitempty"`
	Runtime    *RuntimeSpec   `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Registries []Registry     `yaml:"registries,omitempty" json:"registries,omitempty"`
	WriteBack  *WriteBack     `yaml:"writeBack,omitempty" json:"writeBack,omitempty"`
	Portal     *ProjectPortal `yaml:"portal,omitempty" json:"portal,omitempty"`
}

type SourceSpec struct {
	Provider     string `yaml:"provider" json:"provider"` // github | git | embedded
	Repo         string `yaml:"repo,omitempty" json:"repo,omitempty"`
	Branch       string `yaml:"branch,omitempty" json:"branch,omitempty"`
	Path         string `yaml:"path,omitempty" json:"path,omitempty"`
	PollInterval string `yaml:"pollInterval,omitempty" json:"pollInterval,omitempty"`
}

type RuntimeSpec struct {
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
}

type Registry struct {
	Name string `yaml:"name" json:"name"`
	URL  string `yaml:"url" json:"url"`
	// SecretRef names the secret holding an access token for a private registry (and the
	// private plugin repositories on the same host). Never the token itself.
	SecretRef string `yaml:"secretRef,omitempty" json:"secretRef,omitempty"`
}

// RegistryTokenRef is the only secretRef a registry may have: CICORD_REGISTRY_<NAME>_TOKEN,
// derived from its own name (names have no "_", so the mapping is one-to-one). A cicord.yaml
// edit can thus never make the runtime send another secret — a bot token, or another
// registry's token — to a registry host.
func RegistryTokenRef(name string) string {
	return "CICORD_REGISTRY_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_TOKEN"
}

// TokenRef is the secret of a private registry ("" when it has none or an invalid one).
func (r Registry) TokenRef() string {
	if r.SecretRef != "" && r.SecretRef == RegistryTokenRef(r.Name) {
		return r.SecretRef
	}
	return ""
}

// OfficialRegistryName is the source name of the official Cicord plugins. It is reserved:
// no registry of cicord.yaml may use it.
const OfficialRegistryName = "cicord"

// NormalizeRegistryURL returns a comparable form of a git URL: lower-case scheme and
// host, no trailing slash or ".git".
func NormalizeRegistryURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	s = strings.TrimSuffix(s, "/")
	if scheme, rest, ok := strings.Cut(s, "://"); ok {
		host, p, _ := strings.Cut(rest, "/")
		s = strings.ToLower(scheme) + "://" + strings.ToLower(host)
		if p != "" {
			s += "/" + p
		}
	}
	return s
}

type WriteBack struct {
	Enabled     *bool             `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Branch      string            `yaml:"branch,omitempty" json:"branch,omitempty"`
	DefaultMode string            `yaml:"defaultMode,omitempty" json:"defaultMode,omitempty"`
	Policies    []WriteBackPolicy `yaml:"policies,omitempty" json:"policies,omitempty"`
}

type WriteBackPolicy struct {
	Paths []string `yaml:"paths" json:"paths"`
	Mode  string   `yaml:"mode" json:"mode"` // commit | pullRequest
}

type ProjectPortal struct {
	// Path is the committed build output of a custom portal (relative to cicord.yaml),
	// served at / instead of the default portal.
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
	URL  string `yaml:"url,omitempty" json:"url,omitempty"`
	// AllowedOrigins are third-party sites embedding the SDK in token mode.
	AllowedOrigins []string `yaml:"allowedOrigins,omitempty" json:"allowedOrigins,omitempty"`
}

// ───────────────────────────────────────────────────────────── Server

type Server struct {
	APIVersion string     `yaml:"apiVersion" json:"apiVersion"`
	Kind       string     `yaml:"kind" json:"kind"`
	Metadata   Metadata   `yaml:"metadata" json:"metadata"`
	Spec       ServerSpec `yaml:"spec" json:"spec"`

	// File is the path of the YAML file, relative to the repo root.
	File string `yaml:"-" json:"-"`
}

type ServerSpec struct {
	DiscordID  string              `yaml:"discordId" json:"discordId"`
	Profile    *ServerProfile      `yaml:"profile,omitempty" json:"profile,omitempty"`
	SyncPolicy SyncPolicy          `yaml:"syncPolicy,omitempty" json:"syncPolicy,omitempty"`
	Everyone   *EveryoneSpec       `yaml:"everyone,omitempty" json:"everyone,omitempty"`
	Roles      map[string]RoleSpec `yaml:"roles,omitempty" json:"roles,omitempty"`
	Hierarchy  []string            `yaml:"hierarchy,omitempty" json:"hierarchy,omitempty"`
	Categories map[string]Category `yaml:"categories,omitempty" json:"categories,omitempty"`
	Channels   map[string]Channel  `yaml:"channels,omitempty" json:"channels,omitempty"`
	Access     *Access             `yaml:"access,omitempty" json:"access,omitempty"`
	Portal     *ServerPortal       `yaml:"portal,omitempty" json:"portal,omitempty"`
	Analytics  *AnalyticsSpec      `yaml:"analytics,omitempty" json:"analytics,omitempty"`

	// Discord parity (https://cicord.dev/docs/reference/discord-parity/). Every block is managed only when present.
	AFK           *AFKSpec                  `yaml:"afk,omitempty" json:"afk,omitempty"`
	Widget        *WidgetSpec               `yaml:"widget,omitempty" json:"widget,omitempty"`
	Community     *CommunitySpec            `yaml:"community,omitempty" json:"community,omitempty"`
	AutoMod       map[string]AutoModSpec    `yaml:"automod,omitempty" json:"automod,omitempty"`
	Onboarding    *OnboardingSpec           `yaml:"onboarding,omitempty" json:"onboarding,omitempty"`
	WelcomeScreen *WelcomeScreenSpec        `yaml:"welcomeScreen,omitempty" json:"welcomeScreen,omitempty"`
	Emojis        map[string]EmojiSpec      `yaml:"emojis,omitempty" json:"emojis,omitempty"`
	Stickers      map[string]StickerSpec    `yaml:"stickers,omitempty" json:"stickers,omitempty"`
	Soundboard    map[string]SoundboardSpec `yaml:"soundboard,omitempty" json:"soundboard,omitempty"`
}

// AnalyticsSpec is spec.analytics: activity analytics of the server (internal/analytics).
// Analytics are collected when the block is absent.
type AnalyticsSpec struct {
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Retention in days, "<n>d" (default 400d).
	Retention string `yaml:"retention,omitempty" json:"retention,omitempty"`
	// Presence also aggregates member presences (privileged ServerPresences intent).
	Presence bool `yaml:"presence,omitempty" json:"presence,omitempty"`
	// Portal shows server-wide aggregates in the community portal: "off" (default),
	// "members" (signed-in members of the server) or "public" (anyone).
	Portal string `yaml:"portal,omitempty" json:"portal,omitempty"`
}

// AnalyticsPortal returns who may read the portal's analytics of the server: "off",
// "members" or "public". Off whenever analytics are disabled.
func (s *ServerSpec) AnalyticsPortal() string {
	enabled, _, _ := s.AnalyticsSettings()
	if !enabled || s.Analytics == nil {
		return "off"
	}
	switch s.Analytics.Portal {
	case "members", "public":
		return s.Analytics.Portal
	}
	return "off"
}

// DefaultAnalyticsRetentionDays is the retention when spec.analytics.retention is unset.
const DefaultAnalyticsRetentionDays = 400

// AnalyticsSettings returns whether analytics are collected for the server, how many
// days points are kept and whether presences are aggregated.
func (s *ServerSpec) AnalyticsSettings() (enabled bool, retentionDays int, presence bool) {
	enabled, retentionDays = true, DefaultAnalyticsRetentionDays
	if s == nil || s.Analytics == nil {
		return enabled, retentionDays, false
	}
	a := s.Analytics
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	if n, ok := strings.CutSuffix(a.Retention, "d"); ok {
		if d, err := strconv.Atoi(n); err == nil && d > 0 {
			retentionDays = d
		}
	}
	return enabled, retentionDays, a.Presence
}

type ServerProfile struct {
	Name        string `yaml:"name,omitempty" json:"name,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Icon        string `yaml:"icon,omitempty" json:"icon,omitempty"`
	// Banner (boost level 2) and Splash (invite background, boost level 1) are image
	// files relative to the server YAML.
	Banner               string `yaml:"banner,omitempty" json:"banner,omitempty"`
	Splash               string `yaml:"splash,omitempty" json:"splash,omitempty"`
	VerificationLevel    string `yaml:"verificationLevel,omitempty" json:"verificationLevel,omitempty"`
	DefaultNotifications string `yaml:"defaultNotifications,omitempty" json:"defaultNotifications,omitempty"`
	// ExplicitContentFilter: disabled | membersWithoutRoles | allMembers.
	ExplicitContentFilter string              `yaml:"explicitContentFilter,omitempty" json:"explicitContentFilter,omitempty"`
	PreferredLocale       string              `yaml:"preferredLocale,omitempty" json:"preferredLocale,omitempty"`
	PremiumProgressBar    *bool               `yaml:"premiumProgressBar,omitempty" json:"premiumProgressBar,omitempty"`
	SystemChannel         string              `yaml:"systemChannel,omitempty" json:"systemChannel,omitempty"`
	SystemChannelFlags    *SystemChannelFlags `yaml:"systemChannelFlags,omitempty" json:"systemChannelFlags,omitempty"`
	// RulesChannel is deprecated: use community.rulesChannel (kept as an alias).
	RulesChannel string `yaml:"rulesChannel,omitempty" json:"rulesChannel,omitempty"`
}

// SystemChannelFlags are the system message toggles, as "shown" booleans (unset = not
// managed). Discord stores the opposite (SUPPRESS_* bits).
type SystemChannelFlags struct {
	WelcomeMessages                *bool `yaml:"welcomeMessages,omitempty" json:"welcomeMessages,omitempty"`
	WelcomeStickerReplies          *bool `yaml:"welcomeStickerReplies,omitempty" json:"welcomeStickerReplies,omitempty"`
	BoostMessages                  *bool `yaml:"boostMessages,omitempty" json:"boostMessages,omitempty"`
	SetupTips                      *bool `yaml:"setupTips,omitempty" json:"setupTips,omitempty"`
	RoleSubscriptionMessages       *bool `yaml:"roleSubscriptionMessages,omitempty" json:"roleSubscriptionMessages,omitempty"`
	RoleSubscriptionStickerReplies *bool `yaml:"roleSubscriptionStickerReplies,omitempty" json:"roleSubscriptionStickerReplies,omitempty"`
}

// AFKSpec is the inactive voice channel and timeout (each managed only when set).
type AFKSpec struct {
	Channel string `yaml:"channel,omitempty" json:"channel,omitempty"`
	// Timeout in seconds: 60, 300, 900, 1800 or 3600 (0 = not managed).
	Timeout int `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type WidgetSpec struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Channel string `yaml:"channel,omitempty" json:"channel,omitempty"`
}

// CommunitySpec turns the COMMUNITY feature on or off with its channels.
type CommunitySpec struct {
	Enabled             bool   `yaml:"enabled" json:"enabled"`
	RulesChannel        string `yaml:"rulesChannel,omitempty" json:"rulesChannel,omitempty"`
	UpdatesChannel      string `yaml:"updatesChannel,omitempty" json:"updatesChannel,omitempty"`
	SafetyAlertsChannel string `yaml:"safetyAlertsChannel,omitempty" json:"safetyAlertsChannel,omitempty"`
}

// AutoModSpec is one AutoMod rule (spec.automod.<key>).
type AutoModSpec struct {
	Name string `yaml:"name" json:"name"`
	// Trigger: keyword | keywordPreset | mentionSpam | spam | memberProfile.
	Trigger      string   `yaml:"trigger" json:"trigger"`
	Enabled      *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"` // default true
	Keywords     []string `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	Regex        []string `yaml:"regex,omitempty" json:"regex,omitempty"`
	AllowList    []string `yaml:"allowList,omitempty" json:"allowList,omitempty"`
	Presets      []string `yaml:"presets,omitempty" json:"presets,omitempty"` // profanity | sexualContent | slurs
	MentionLimit int      `yaml:"mentionLimit,omitempty" json:"mentionLimit,omitempty"`
	// MentionRaidProtection (mentionSpam only).
	MentionRaidProtection bool           `yaml:"mentionRaidProtection,omitempty" json:"mentionRaidProtection,omitempty"`
	Actions               AutoModActions `yaml:"actions" json:"actions"`
	// ExemptRoles / ExemptChannels are role and channel keys.
	ExemptRoles    []string `yaml:"exemptRoles,omitempty" json:"exemptRoles,omitempty"`
	ExemptChannels []string `yaml:"exemptChannels,omitempty" json:"exemptChannels,omitempty"`
}

// IsEnabled applies the default (true).
func (a AutoModSpec) IsEnabled() bool { return a.Enabled == nil || *a.Enabled }

type AutoModActions struct {
	// Block blocks the message (memberProfile: blocks the member's interactions).
	Block *AutoModBlock `yaml:"block,omitempty" json:"block,omitempty"`
	// Alert is a channel key that receives an alert.
	Alert string `yaml:"alert,omitempty" json:"alert,omitempty"`
	// Timeout is a duration (e.g. "60s", "10m", "1h", "1w"; at most 4 weeks).
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type AutoModBlock struct {
	// Message is shown to the member (at most 150 characters).
	Message string `yaml:"message,omitempty" json:"message,omitempty"`
}

// OnboardingSpec is Discord's native onboarding (community servers).
type OnboardingSpec struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Mode: default (only default channels count) | advanced.
	Mode            string             `yaml:"mode,omitempty" json:"mode,omitempty"`
	DefaultChannels []string           `yaml:"defaultChannels,omitempty" json:"defaultChannels,omitempty"`
	Prompts         []OnboardingPrompt `yaml:"prompts,omitempty" json:"prompts,omitempty"`
}

type OnboardingPrompt struct {
	Key   string `yaml:"key" json:"key"`
	Title string `yaml:"title" json:"title"`
	// Type: multipleChoice (default) | dropdown.
	Type         string             `yaml:"type,omitempty" json:"type,omitempty"`
	SingleSelect bool               `yaml:"singleSelect,omitempty" json:"singleSelect,omitempty"`
	Required     bool               `yaml:"required,omitempty" json:"required,omitempty"`
	InOnboarding *bool              `yaml:"inOnboarding,omitempty" json:"inOnboarding,omitempty"` // default true
	Options      []OnboardingOption `yaml:"options" json:"options"`
}

// IsInOnboarding applies the default (true).
func (p OnboardingPrompt) IsInOnboarding() bool { return p.InOnboarding == nil || *p.InOnboarding }

type OnboardingOption struct {
	Title       string   `yaml:"title" json:"title"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Emoji       string   `yaml:"emoji,omitempty" json:"emoji,omitempty"`
	Roles       []string `yaml:"roles,omitempty" json:"roles,omitempty"`
	Channels    []string `yaml:"channels,omitempty" json:"channels,omitempty"`
}

type WelcomeScreenSpec struct {
	Enabled     *bool            `yaml:"enabled,omitempty" json:"enabled,omitempty"` // default true
	Description string           `yaml:"description,omitempty" json:"description,omitempty"`
	Channels    []WelcomeChannel `yaml:"channels,omitempty" json:"channels,omitempty"`
}

// IsEnabled applies the default (true).
func (w WelcomeScreenSpec) IsEnabled() bool { return w.Enabled == nil || *w.Enabled }

type WelcomeChannel struct {
	Channel     string `yaml:"channel" json:"channel"`
	Description string `yaml:"description" json:"description"`
	Emoji       string `yaml:"emoji,omitempty" json:"emoji,omitempty"`
}

// EmojiSpec is a custom emoji from a repository image (png/jpg/gif/webp, ≤ 256 KiB).
type EmojiSpec struct {
	Name  string `yaml:"name" json:"name"`
	Image string `yaml:"image" json:"image"`
	// Roles restricts the emoji to role keys (empty = everyone).
	Roles []string `yaml:"roles,omitempty" json:"roles,omitempty"`
}

// StickerSpec is a custom sticker from a repository file (png/apng/gif/lottie json, ≤ 512 KiB).
type StickerSpec struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Emoji is the related emoji name used for suggestions (e.g. "smile").
	Emoji string `yaml:"emoji" json:"emoji"`
	File  string `yaml:"file" json:"file"`
}

// SoundboardSpec is a soundboard sound from a repository file (mp3/ogg, ≤ 512 KiB).
type SoundboardSpec struct {
	Name   string   `yaml:"name" json:"name"`
	Sound  string   `yaml:"sound" json:"sound"`
	Volume *float64 `yaml:"volume,omitempty" json:"volume,omitempty"` // 0..1, default 1
	Emoji  string   `yaml:"emoji,omitempty" json:"emoji,omitempty"`
}

// SyncPolicy uses pointers so that unset fields get the documented defaults.
type SyncPolicy struct {
	SelfHealP           *bool `yaml:"selfHeal,omitempty" json:"selfHeal,omitempty"`
	PruneP              *bool `yaml:"prune,omitempty" json:"prune,omitempty"`
	DeletionProtectionP *bool `yaml:"deletionProtection,omitempty" json:"deletionProtection,omitempty"`
}

func (p SyncPolicy) SelfHeal() bool { return p.SelfHealP != nil && *p.SelfHealP }
func (p SyncPolicy) Prune() bool    { return p.PruneP != nil && *p.PruneP }
func (p SyncPolicy) DeletionProtection() bool {
	return p.DeletionProtectionP == nil || *p.DeletionProtectionP
}

type EveryoneSpec struct {
	Permissions []string `yaml:"permissions,omitempty" json:"permissions,omitempty"`
}

type RoleSpec struct {
	Name  string `yaml:"name" json:"name"`
	Color string `yaml:"color,omitempty" json:"color,omitempty"`
	// Colors is a gradient role style (boost level 2); color is its solid shorthand.
	Colors      *RoleColors `yaml:"colors,omitempty" json:"colors,omitempty"`
	Hoist       bool        `yaml:"hoist,omitempty" json:"hoist,omitempty"`
	Mentionable bool        `yaml:"mentionable,omitempty" json:"mentionable,omitempty"`
	// Icon is an image file (boost level 2); Emoji a unicode emoji icon. One or the other.
	Icon        string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Emoji       string   `yaml:"emoji,omitempty" json:"emoji,omitempty"`
	Permissions []string `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// Members are Discord user IDs who must hold the role (additive: unlisted holders
	// keep it, and removing an ID never removes the role).
	Members []string `yaml:"members,omitempty" json:"members,omitempty"`
}

type RoleColors struct {
	Primary   string `yaml:"primary" json:"primary"`
	Secondary string `yaml:"secondary,omitempty" json:"secondary,omitempty"`
	Tertiary  string `yaml:"tertiary,omitempty" json:"tertiary,omitempty"`
}

// OverwriteSpec is keyed by target: "@everyone", "@bot" (the managing bot's own
// managed role), a role key, or "member:<id>".
type OverwriteSpec struct {
	Allow []string `yaml:"allow,omitempty" json:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty" json:"deny,omitempty"`
}

type Category struct {
	Name       string                   `yaml:"name" json:"name"`
	Position   *int                     `yaml:"position,omitempty" json:"position,omitempty"`
	Overwrites map[string]OverwriteSpec `yaml:"overwrites,omitempty" json:"overwrites,omitempty"`
}

type Channel struct {
	Name             string                   `yaml:"name" json:"name"`
	Type             string                   `yaml:"type" json:"type"`
	Category         string                   `yaml:"category,omitempty" json:"category,omitempty"`
	Position         *int                     `yaml:"position,omitempty" json:"position,omitempty"`
	Topic            string                   `yaml:"topic,omitempty" json:"topic,omitempty"`
	NSFW             bool                     `yaml:"nsfw,omitempty" json:"nsfw,omitempty"`
	Slowmode         int                      `yaml:"slowmode,omitempty" json:"slowmode,omitempty"`
	Bitrate          int                      `yaml:"bitrate,omitempty" json:"bitrate,omitempty"`
	UserLimit        int                      `yaml:"userLimit,omitempty" json:"userLimit,omitempty"`
	SyncWithCategory *bool                    `yaml:"syncWithCategory,omitempty" json:"syncWithCategory,omitempty"`
	Overwrites       map[string]OverwriteSpec `yaml:"overwrites,omitempty" json:"overwrites,omitempty"`

	// Voice / stage: VoiceRegion ("automatic" or a region id such as "rotterdam"),
	// VideoQuality (auto | full). Managed only when set.
	VoiceRegion  string `yaml:"voiceRegion,omitempty" json:"voiceRegion,omitempty"`
	VideoQuality string `yaml:"videoQuality,omitempty" json:"videoQuality,omitempty"`
	// Thread defaults: ThreadArchiveAfter is 1h | 24h | 3d | 1w; ThreadSlowmode seconds.
	ThreadArchiveAfter string `yaml:"threadArchiveAfter,omitempty" json:"threadArchiveAfter,omitempty"`
	ThreadSlowmode     *int   `yaml:"threadSlowmode,omitempty" json:"threadSlowmode,omitempty"`
	// Forum / media channels. Guidelines is the forum's "post guidelines" (= topic).
	Tags            map[string]ForumTag `yaml:"tags,omitempty" json:"tags,omitempty"`
	DefaultReaction string              `yaml:"defaultReaction,omitempty" json:"defaultReaction,omitempty"`
	SortOrder       string              `yaml:"sortOrder,omitempty" json:"sortOrder,omitempty"` // latestActivity | creationDate
	Layout          string              `yaml:"layout,omitempty" json:"layout,omitempty"`       // list | gallery (forum)
	Guidelines      string              `yaml:"guidelines,omitempty" json:"guidelines,omitempty"`
}

// ForumTag is a tag of a forum/media channel (channels.<k>.tags.<key>).
type ForumTag struct {
	Name      string `yaml:"name" json:"name"`
	Emoji     string `yaml:"emoji,omitempty" json:"emoji,omitempty"`
	Moderated bool   `yaml:"moderated,omitempty" json:"moderated,omitempty"`
}

// EffectiveTopic is the topic, or the forum guidelines (an alias).
func (c Channel) EffectiveTopic() string {
	if c.Topic != "" {
		return c.Topic
	}
	return c.Guidelines
}

// SyncsWithCategory applies the documented default: true when no overwrites are set.
func (c Channel) SyncsWithCategory() bool {
	if c.SyncWithCategory != nil {
		return *c.SyncWithCategory
	}
	return len(c.Overwrites) == 0
}

type Principals struct {
	Roles []string `yaml:"roles,omitempty" json:"roles,omitempty"`
	Users []string `yaml:"users,omitempty" json:"users,omitempty"`
}

type Access struct {
	Admins     *Principals `yaml:"admins,omitempty" json:"admins,omitempty"`
	Moderators *Principals `yaml:"moderators,omitempty" json:"moderators,omitempty"`
}

type ServerPortal struct {
	Theme       *Theme       `yaml:"theme,omitempty" json:"theme,omitempty"`
	Title       string       `yaml:"title,omitempty" json:"title,omitempty"`
	Tagline     string       `yaml:"tagline,omitempty" json:"tagline,omitempty"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty"`
	Banner      string       `yaml:"banner,omitempty" json:"banner,omitempty"`
	Links       []PortalLink `yaml:"links,omitempty" json:"links,omitempty"`
	Pages       *PortalPages `yaml:"pages,omitempty" json:"pages,omitempty"`
	Locale      string       `yaml:"locale,omitempty" json:"locale,omitempty"`
}

type PortalLink struct {
	Label string `yaml:"label" json:"label"`
	URL   string `yaml:"url" json:"url"`
	Icon  string `yaml:"icon,omitempty" json:"icon,omitempty"`
}

// PortalPages toggles the pages of the default portal (unset = enabled).
type PortalPages struct {
	Onboarding  *bool `yaml:"onboarding,omitempty" json:"onboarding,omitempty"`
	Profile     *bool `yaml:"profile,omitempty" json:"profile,omitempty"`
	Leaderboard *bool `yaml:"leaderboard,omitempty" json:"leaderboard,omitempty"`
	Tickets     *bool `yaml:"tickets,omitempty" json:"tickets,omitempty"`
}

type Theme struct {
	Primary string `yaml:"primary,omitempty" json:"primary,omitempty"`
	Accent  string `yaml:"accent,omitempty" json:"accent,omitempty"`
	Logo    string `yaml:"logo,omitempty" json:"logo,omitempty"`
	Mode    string `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// ───────────────────────────────────────────────────────────── Bot

type Bot struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       BotSpec  `yaml:"spec" json:"spec"`

	File string `yaml:"-" json:"-"`
}

type DirectMessageRoute struct {
	Instance string `yaml:"instance" json:"instance"`
}

type BotSpec struct {
	DirectMessages *DirectMessageRoute `yaml:"directMessages,omitempty" json:"directMessages,omitempty"`
	ApplicationID  string              `yaml:"applicationId" json:"applicationId"`
	Token          TokenRef            `yaml:"token" json:"token"`
	Profile        *BotProfile         `yaml:"profile,omitempty" json:"profile,omitempty"`
	Presence       *Presence           `yaml:"presence,omitempty" json:"presence,omitempty"`
	Servers        []BotServer         `yaml:"servers" json:"servers"`
	Intents        []string            `yaml:"intents,omitempty" json:"intents,omitempty"`
	Plugins        []PluginInstance    `yaml:"plugins,omitempty" json:"plugins,omitempty"`
}

type TokenRef struct {
	SecretRef string `yaml:"secretRef" json:"secretRef"`
}

type BotProfile struct {
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Avatar   string `yaml:"avatar,omitempty" json:"avatar,omitempty"`
	Banner   string `yaml:"banner,omitempty" json:"banner,omitempty"`
}

type Presence struct {
	Status   string    `yaml:"status,omitempty" json:"status,omitempty"`
	Activity *Activity `yaml:"activity,omitempty" json:"activity,omitempty"`
}

type Activity struct {
	Type string `yaml:"type" json:"type"`
	Name string `yaml:"name" json:"name"`
	URL  string `yaml:"url,omitempty" json:"url,omitempty"`
}

type PluginInstance struct {
	Secrets   map[string]TokenRef       `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Network   *Network                  `yaml:"network,omitempty" json:"network,omitempty"`
	Files     []string                  `yaml:"files,omitempty" json:"files,omitempty"`
	Name      string                    `yaml:"name" json:"name"`
	Source    string                    `yaml:"source" json:"source"`
	Version   string                    `yaml:"version,omitempty" json:"version,omitempty"`
	Servers   []string                  `yaml:"servers,omitempty" json:"servers,omitempty"`
	Grants    []string                  `yaml:"grants,omitempty" json:"grants,omitempty"`
	Config    map[string]any            `yaml:"config,omitempty" json:"config,omitempty"`
	Overrides map[string]ServerOverride `yaml:"overrides,omitempty" json:"overrides,omitempty"`
}

type ServerOverride struct {
	Config map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
}

// IsLocal reports whether Source is a path (as opposed to a registry reference).
func (p PluginInstance) IsLocal() bool { return len(p.Source) > 0 && p.Source[0] == '.' }

// ───────────────────────────────────────────────────────────── Plugin manifest

type Plugin struct {
	APIVersion string         `yaml:"apiVersion" json:"apiVersion"`
	Kind       string         `yaml:"kind" json:"kind"`
	Metadata   PluginMetadata `yaml:"metadata" json:"metadata"`
	Spec       PluginSpec     `yaml:"spec" json:"spec"`
}

type PluginMetadata struct {
	Name        string   `yaml:"name" json:"name"`
	Version     string   `yaml:"version" json:"version"`
	DisplayName string   `yaml:"displayName,omitempty" json:"displayName,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Authors     []string `yaml:"authors,omitempty" json:"authors,omitempty"`
	License     string   `yaml:"license,omitempty" json:"license,omitempty"`
	Homepage    string   `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	Keywords    []string `yaml:"keywords,omitempty" json:"keywords,omitempty"`
}

type PluginSecret struct {
	Name        string   `yaml:"name,omitempty" json:"name,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Required    bool     `yaml:"required,omitempty" json:"required,omitempty"`
	Hosts       []string `yaml:"hosts,omitempty" json:"hosts,omitempty"`
}

type PluginSpec struct {
	Locales      string            `yaml:"locales,omitempty" json:"locales,omitempty"`
	Secrets      []PluginSecret    `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Engine       string            `yaml:"engine" json:"engine"`
	Entry        string            `yaml:"entry" json:"entry"`
	Capabilities []string          `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Network      *Network          `yaml:"network,omitempty" json:"network,omitempty"`
	Intents      []string          `yaml:"intents,omitempty" json:"intents,omitempty"`
	Config       *PluginConfigSpec `yaml:"config,omitempty" json:"config,omitempty"`
	Routes       *PluginRoutesSpec `yaml:"routes,omitempty" json:"routes,omitempty"`
	Client       *PluginClient     `yaml:"client,omitempty" json:"client,omitempty"`
}

// PluginRoutesSpec describes the portal routes of a plugin for the portals.
type PluginRoutesSpec struct {
	// Types is the route contract: a type-only TypeScript file exporting `interface Routes`,
	// copied into the portals by `cicord portal typegen`.
	Types string `yaml:"types,omitempty" json:"types,omitempty"`
}

type Network struct {
	Configurable bool     `yaml:"configurable,omitempty" json:"configurable,omitempty"`
	Allow        []string `yaml:"allow" json:"allow"`
}

type PluginConfigSpec struct {
	Schema string `yaml:"schema" json:"schema"`
}

type PluginClient struct {
	Package string `yaml:"package" json:"package"`
}
