package reconcile

import (
	"errors"
	"fmt"
	"strings"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/state"
)

// Change actions (spec/api.md §4, plus "unmanage").
const (
	ActionCreate   = "create"
	ActionUpdate   = "update"
	ActionDelete   = "delete"
	ActionAdopt    = "adopt"
	ActionReorder  = "reorder"
	ActionUnmanage = "unmanage"
)

// Change kinds.
const (
	KindServer   = "server"
	KindRole     = "role"
	KindCategory = "category"
	KindChannel  = "channel"
	KindEveryone = "everyone"
	// Discord parity (docs/discord-parity.md).
	KindWidget        = "widget"
	KindAutoMod       = "automod"
	KindOnboarding    = "onboarding"
	KindWelcomeScreen = "welcomeScreen"
	KindEmoji         = "emoji"
	KindSticker       = "sticker"
	KindSound         = "sound"
)

// Reorder change keys.
const (
	// KeyHierarchy is the key of the role reorder change.
	KeyHierarchy = "hierarchy"
	// KeyCategories is the key of the category reorder change.
	KeyCategories = "categories"
	// KeyNoCategory is the key of the reorder change of uncategorized channels;
	// channel reorders inside a category use the category key.
	KeyNoCategory = "(none)"
)

// FieldChange is the from → to of one field. Values are JSON friendly: strings,
// numbers, booleans, permission name lists, overwrite maps, key lists.
type FieldChange struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// Change is one planned operation.
type Change struct {
	Action  string                 `json:"action"`
	Kind    string                 `json:"kind"`
	Key     string                 `json:"key"`
	ID      string                 `json:"id,omitempty"`
	Summary string                 `json:"summary"`
	Fields  map[string]FieldChange `json:"fields,omitempty"`

	op *op // what Apply needs; nil on a decoded plan
}

// Blocked is an operation the reconciler refuses to perform.
type Blocked struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
	// Fields are the differences that could not be applied (when known).
	Fields map[string]FieldChange `json:"fields,omitempty"`
}

// Plan is the diff between the desired and the live state of one server.
// Changes are in apply order.
type Plan struct {
	Server  string    `json:"server"`
	Changes []Change  `json:"changes"`
	Blocked []Blocked `json:"blocked"`
}

// Empty reports whether the plan has no change to apply (blocked operations are not
// changes).
func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// String renders the plan as human readable lines.
func (p Plan) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "server %s: %d change(s), %d blocked\n", p.Server, len(p.Changes), len(p.Blocked))
	for _, c := range p.Changes {
		fmt.Fprintf(&b, "  %s\n", c.Summary)
	}
	for _, bl := range p.Blocked {
		fmt.Fprintf(&b, "  blocked: %s %s %s: %s\n", bl.Action, bl.Kind, bl.Key, bl.Reason)
	}
	return b.String()
}

// Failure is a change that could not be applied.
type Failure struct {
	Change Change `json:"change"`
	Error  string `json:"error"`

	err error
}

// Err returns the underlying error (errors.Is works with discord sentinels).
func (f Failure) Err() error {
	if f.err != nil {
		return f.err
	}
	return errors.New(f.Error)
}

// Result is the outcome of Apply.
type Result struct {
	Applied []Change  `json:"applied"`
	Failed  []Failure `json:"failed"`
}

// DriftEntry is one differing field (spec/api.md §3). Field "exists" means the
// resource is missing in Discord (or not created yet), "order" a reorder,
// "managed" a mapping that is about to be dropped.
type DriftEntry struct {
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Field    string `json:"field"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

// ───────────────────────────────────────────────────────────── apply data

type phase int

const (
	phaseServer phase = iota
	phaseRoles
	phaseRolePositions
	phaseCategories
	phaseChannels
	phaseServerRefs        // channel references, Community on/off
	phaseCommunityChannels // announcement/stage channels created after Community is enabled
	phaseChannelPositions
	phaseWidget
	phaseWelcome
	phaseOnboarding
	phaseAutoMod
	phaseExpressions
	phaseDeleteAutoMod
	phaseDeleteExpressions
	phaseDeleteChannels
	phaseDeleteCategories
	phaseDeleteRoles
	phaseUnmanage
)

// target is a symbolic overwrite target, resolved at apply time.
type target struct {
	roleKey string // role key (resolved via the working map)
	id      string // literal ID (@everyone → server ID, member:<id>)
	typ     discord.OverwriteType
}

type owSpec struct {
	target      target
	allow, deny discord.Permissions
}

// chanSpec is the desired state of a category or channel, with symbolic references.
type chanSpec struct {
	name       string
	typ        discord.ChannelType
	parentKey  string // category key, "" = none
	topic      *string
	nsfw       *bool
	slowmode   *int
	bitrate    *int
	userLimit  *int
	overwrites []owSpec

	rtcRegion       *string
	videoQuality    *string
	autoArchive     *int
	threadSlowmode  *int
	tags            *[]discord.ForumTag
	defaultReaction *string
	sortOrder       *string
	layout          *string
}

type serverSpec struct {
	name, description, verification, notifications *string
	icon                                           []byte
	iconHash                                       string
	systemKey, rulesKey                            *string // "" = clear

	banner, splash         []byte
	bannerHash, splashHash string
	explicit, locale       *string
	progressBar            *bool
	flags                  *discord.SystemChannelFlags
	afkTimeout             *int
	afkKey, updatesKey     *string
	safetyKey              *string
	features               *[]string
}

// op is the data Apply needs for a change.
type op struct {
	phase phase
	rk    state.ResourceKind // role / category / channel ("" for server, everyone, reorder)

	// role / everyone: the parameters to send (only differing fields for updates)
	role *discord.RoleParams

	// category / channel: the full desired spec and which fields to send on update
	ch     *chanSpec
	send   map[string]bool
	server *serverSpec

	// role icon image (uploaded with the role params)
	roleIcon     []byte
	roleIconHash string

	widget     *widgetWant
	automod    *automodWant
	onboarding *onboardingWant
	welcome    *welcomeWant
	expr       *exprWant
}
