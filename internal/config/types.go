// Package config loads and validates a CODI repository (codi.yaml, servers/, bots/,
// plugins) into typed structures. See spec/README.md for the semantics.
package config

const APIVersion = "codi.dev/v1alpha1"

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
	// Path is the committed build output of a custom portal (relative to codi.yaml),
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
}

type ServerProfile struct {
	Name                 string `yaml:"name,omitempty" json:"name,omitempty"`
	Description          string `yaml:"description,omitempty" json:"description,omitempty"`
	Icon                 string `yaml:"icon,omitempty" json:"icon,omitempty"`
	VerificationLevel    string `yaml:"verificationLevel,omitempty" json:"verificationLevel,omitempty"`
	DefaultNotifications string `yaml:"defaultNotifications,omitempty" json:"defaultNotifications,omitempty"`
	SystemChannel        string `yaml:"systemChannel,omitempty" json:"systemChannel,omitempty"`
	RulesChannel         string `yaml:"rulesChannel,omitempty" json:"rulesChannel,omitempty"`
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
	Name        string   `yaml:"name" json:"name"`
	Color       string   `yaml:"color,omitempty" json:"color,omitempty"`
	Hoist       bool     `yaml:"hoist,omitempty" json:"hoist,omitempty"`
	Mentionable bool     `yaml:"mentionable,omitempty" json:"mentionable,omitempty"`
	Icon        string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Permissions []string `yaml:"permissions,omitempty" json:"permissions,omitempty"`
}

// OverwriteSpec is keyed by target: "@everyone", a role key, or "member:<id>".
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

type BotSpec struct {
	ApplicationID string           `yaml:"applicationId" json:"applicationId"`
	Token         TokenRef         `yaml:"token" json:"token"`
	Profile       *BotProfile      `yaml:"profile,omitempty" json:"profile,omitempty"`
	Presence      *Presence        `yaml:"presence,omitempty" json:"presence,omitempty"`
	Servers       []string         `yaml:"servers" json:"servers"`
	Intents       []string         `yaml:"intents,omitempty" json:"intents,omitempty"`
	Plugins       []PluginInstance `yaml:"plugins,omitempty" json:"plugins,omitempty"`
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

type PluginSpec struct {
	Engine       string            `yaml:"engine" json:"engine"`
	Entry        string            `yaml:"entry" json:"entry"`
	Capabilities []string          `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Network      *Network          `yaml:"network,omitempty" json:"network,omitempty"`
	Intents      []string          `yaml:"intents,omitempty" json:"intents,omitempty"`
	Config       *PluginConfigSpec `yaml:"config,omitempty" json:"config,omitempty"`
	Client       *PluginClient     `yaml:"client,omitempty" json:"client,omitempty"`
}

type Network struct {
	Allow []string `yaml:"allow" json:"allow"`
}

type PluginConfigSpec struct {
	Schema string `yaml:"schema" json:"schema"`
}

type PluginClient struct {
	Package string `yaml:"package" json:"package"`
}
