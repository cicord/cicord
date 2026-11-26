package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"

	"github.com/cicord/cicord/internal/discord"
)

// Definition is the static part of a plugin, extracted from `definePlugin({...})` by Inspect.
// All lists are sorted by name, except command options (Discord order).
type Definition struct {
	Commands      []CommandDef
	Events        []string // SDK event names: memberJoin, messageCreate, …
	Components    []string // local component ids (handler keys)
	Modals        []string // local modal ids (handler keys)
	Schedules     []ScheduleDef
	Tasks         []string
	Subscriptions []string // "<plugin>.<topic>"
	Routes        []RouteDef
	HasOnStart    bool
	HasOnStop     bool
}

// CommandDef is a slash command declared under `commands`.
type CommandDef struct {
	Name        string
	Description string
	// Ephemeral: replies are ephemeral by default and auto-defer is ephemeral (subcommands
	// included).
	Ephemeral bool
	// Permissions are Cicord permission names (default member permissions).
	Permissions []string
	// Options in Discord order: required options first, then declaration order. A command
	// with subcommands holds only discord.OptionSubcommand / OptionSubcommandGroup options
	// (subcommands first, then groups, in declaration order), whose Options are theirs.
	Options []discord.CommandOption
}

// HasSubcommands reports whether the command is made of subcommands (and groups).
func (c CommandDef) HasSubcommands() bool {
	return len(c.Options) > 0 && c.Options[0].IsSubcommand()
}

// Leaf returns the options of the invoked leaf for a command path (["poll"], ["polls",
// "close"], ["polls", "admin", "purge"], as from discord.Interaction.CommandPath), or false
// when the path does not name an invocable command or subcommand of c.
func (c CommandDef) Leaf(path []string) ([]discord.CommandOption, bool) {
	if len(path) == 0 || path[0] != c.Name {
		return nil, false
	}
	opts := c.Options
	for _, name := range path[1:] {
		found := false
		for _, o := range opts {
			if o.IsSubcommand() && o.Name == name {
				opts, found = o.Options, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	if len(opts) > 0 && opts[0].IsSubcommand() {
		return nil, false // a command or group with subcommands is not invocable itself
	}
	return opts, true
}

// Leaves lists the invocable command paths ("poll", "polls close", "polls admin purge").
func (c CommandDef) Leaves() []string {
	if !c.HasSubcommands() {
		return []string{c.Name}
	}
	var out []string
	for _, o := range c.Options {
		switch o.Type {
		case discord.OptionSubcommand:
			out = append(out, c.Name+" "+o.Name)
		case discord.OptionSubcommandGroup:
			for _, sub := range o.Options {
				out = append(out, c.Name+" "+o.Name+" "+sub.Name)
			}
		}
	}
	return out
}

// ScheduleDef is a recurring job declared under `schedules` (cron, UTC).
type ScheduleDef struct {
	Name string
	Cron string
}

// RouteDef is a portal route declared under `routes` ("<METHOD> <path>").
type RouteDef struct {
	Method string // GET | POST | PUT | PATCH | DELETE
	Path   string // e.g. /answers/:id
	Access string // public | member | moderator | admin
}

// Key returns the route key as written in the plugin ("POST /answers/:id").
func (r RouteDef) Key() string { return r.Method + " " + r.Path }

// Command returns the command named name.
func (d *Definition) Command(name string) (CommandDef, bool) {
	for _, c := range d.Commands {
		if c.Name == name {
			return c, true
		}
	}
	return CommandDef{}, false
}

// HasEvent reports whether the plugin has a handler for the SDK event name.
func (d *Definition) HasEvent(name string) bool { return slices.Contains(d.Events, name) }

// HasTask reports whether a task handler named name exists.
func (d *Definition) HasTask(name string) bool { return slices.Contains(d.Tasks, name) }

// DiscordCommand converts the command for SetServerCommands.
func (c CommandDef) DiscordCommand() discord.Command {
	cmd := discord.Command{Name: c.Name, Description: c.Description, Options: c.Options}
	if len(c.Permissions) > 0 {
		p, err := discord.ParsePermissions(c.Permissions) // validated by Inspect
		if err == nil {
			cmd.DefaultMemberPermissions = &p
		}
	}
	return cmd
}

// DiscordCommands returns all commands, ready for SetServerCommands.
func (d *Definition) DiscordCommands() []discord.Command {
	out := make([]discord.Command, len(d.Commands))
	for i, c := range d.Commands {
		out[i] = c.DiscordCommand()
	}
	return out
}

// ───────────────────────────────────────────────────────────── intents

var eventIntents = map[string][]string{
	"memberJoin":        {"ServerMembers"},
	"memberLeave":       {"ServerMembers"},
	"memberUpdate":      {"ServerMembers"},
	"messageCreate":     {"ServerMessages"},
	"messageUpdate":     {"ServerMessages"},
	"messageDelete":     {"ServerMessages"},
	"reactionAdd":       {"ServerMessageReactions"},
	"reactionRemove":    {"ServerMessageReactions"},
	"auditLogEntry":     {"ServerModeration"},
	"channelCreate":     {},
	"channelUpdate":     {},
	"channelDelete":     {},
	"roleCreate":        {},
	"roleUpdate":        {},
	"roleDelete":        {},
	"threadCreate":      {},
	"threadDelete":      {},
	"serverUpdate":      {},
	"expressionsUpdate": {"ServerExpressions"},
	"webhooksUpdate":    {"ServerWebhooks"},
	"inviteCreate":      {"ServerInvites"},
	"inviteDelete":      {"ServerInvites"},
	"automodAction":     {"AutoModerationExecution"},
	"banAdd":            {"ServerModeration"},
	"banRemove":         {"ServerModeration"},
	"voiceStateUpdate":  {"ServerVoiceStates"},
	// Servers is always enabled (config.Repo.GatewayIntents): nothing to declare.
	"serverJoin":  {},
	"serverLeave": {},
}

// EventNames lists the SDK event handler names (EventHandlers in cicord-sdk.d.ts).
func EventNames() []string {
	out := make([]string, 0, len(eventIntents))
	for n := range eventIntents {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EventIntents returns the gateway intents a handler for event needs (the @intent tags of
// cicord-sdk.d.ts), nil for unknown events. MessageContent is optional (it only fills
// message.content) and therefore not included.
func EventIntents(event string) []string { return slices.Clone(eventIntents[event]) }

// RequiredIntents is the sorted union of the intents needed by the plugin's handlers.
func (d *Definition) RequiredIntents() []string {
	var out []string
	for _, e := range d.Events {
		for _, in := range eventIntents[e] {
			if !slices.Contains(out, in) {
				out = append(out, in)
			}
		}
	}
	sort.Strings(out)
	return out
}

// EventName maps a gateway event to the SDK handler name ("" when plugins cannot handle it;
// interactions go through Instance.HandleInteraction).
func EventName(ev discord.Event) string {
	switch e := ev.(type) {
	case discord.ChannelCreateEvent:
		if e.Channel.Type.IsThread() {
			if !e.NewThread {
				return ""
			}
			return "threadCreate"
		}
		return "channelCreate"
	case discord.ChannelUpdateEvent:
		return "channelUpdate"
	case discord.ChannelDeleteEvent:
		if e.Channel.Type.IsThread() {
			return "threadDelete"
		}
		return "channelDelete"
	case discord.RoleCreateEvent:
		return "roleCreate"
	case discord.RoleUpdateEvent:
		return "roleUpdate"
	case discord.RoleDeleteEvent:
		return "roleDelete"
	case discord.ServerUpdateEvent:
		return "serverUpdate"
	case discord.EmojisUpdateEvent, discord.StickersUpdateEvent:
		return "expressionsUpdate"
	case discord.WebhooksUpdateEvent:
		return "webhooksUpdate"
	case discord.InviteCreateEvent:
		return "inviteCreate"
	case discord.InviteDeleteEvent:
		return "inviteDelete"
	case discord.AutoModActionEvent:
		return "automodAction"
	case discord.AuditLogEntryEvent:
		return "auditLogEntry"
	case discord.BanAddEvent:
		return "banAdd"
	case discord.BanRemoveEvent:
		return "banRemove"
	case discord.MemberJoinEvent:
		return "memberJoin"
	case discord.MemberLeaveEvent:
		return "memberLeave"
	case discord.MemberUpdateEvent:
		return "memberUpdate"
	case discord.MessageCreateEvent:
		return "messageCreate"
	case discord.MessageUpdateEvent:
		return "messageUpdate"
	case discord.MessageDeleteEvent:
		return "messageDelete"
	case discord.ReactionAddEvent:
		return "reactionAdd"
	case discord.ReactionRemoveEvent:
		return "reactionRemove"
	case discord.ServerJoinEvent:
		return "serverJoin"
	case discord.ServerLeaveEvent:
		return "serverLeave"
	case discord.VoiceStateUpdateEvent:
		return "voiceStateUpdate"
	}
	return ""
}

// ───────────────────────────────────────────────────────────── cron

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseCron parses a schedule expression: standard 5-field cron or a descriptor
// (@hourly, @daily, @every 10m…). Time zone prefixes are refused: schedules run in UTC.
func ParseCron(expr string) (cron.Schedule, error) {
	e := strings.TrimSpace(expr)
	if strings.HasPrefix(e, "TZ=") || strings.HasPrefix(e, "CRON_TZ=") {
		return nil, fmt.Errorf("invalid cron %q: time zones are not supported (schedules run in UTC)", expr)
	}
	s, err := cronParser.Parse(e)
	if err != nil {
		return nil, fmt.Errorf("invalid cron %q: %w", expr, err)
	}
	return s, nil
}

// ValidateCron reports whether expr is a valid schedule expression.
func ValidateCron(expr string) error {
	_, err := ParseCron(expr)
	return err
}

// NextRun returns the first activation of expr strictly after after, in UTC.
func NextRun(expr string, after time.Time) (time.Time, error) {
	s, err := ParseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	return s.Next(after.UTC()), nil
}

// ───────────────────────────────────────────────────────────── routes

// MatchRoute finds the route for method and path and extracts its parameters. Static
// segments take precedence over parameters ("/answers/top" beats "/answers/:id").
func (d *Definition) MatchRoute(method, path string) (RouteDef, map[string]string, bool) {
	segs := splitPath(path)
	var (
		best       RouteDef
		bestParams map[string]string
		bestScore  []bool
		found      bool
	)
	for _, r := range d.Routes {
		if r.Method != strings.ToUpper(method) {
			continue
		}
		pat := splitPath(r.Path)
		if len(pat) != len(segs) {
			continue
		}
		params := map[string]string{}
		score := make([]bool, len(pat)) // true = static segment
		ok := true
		for i, p := range pat {
			if name, isParam := strings.CutPrefix(p, ":"); isParam {
				if segs[i] == "" {
					ok = false
					break
				}
				params[name] = segs[i]
				continue
			}
			if p != segs[i] {
				ok = false
				break
			}
			score[i] = true
		}
		if !ok {
			continue
		}
		if !found || betterRoute(score, bestScore) {
			best, bestParams, bestScore, found = r, params, score, true
		}
	}
	return best, bestParams, found
}

func betterRoute(a, b []bool) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i]
		}
	}
	return false
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// ───────────────────────────────────────────────────────────── extraction

// describedPlugin is what the SDK prelude's describe() returns (JSON).
type describedPlugin struct {
	Keys       []string          `json:"keys"`
	OnStart    string            `json:"onStart"` // typeof
	OnStop     string            `json:"onStop"`
	Events     []describedFn     `json:"events"`
	Commands   []describedCmd    `json:"commands"`
	Components []describedFn     `json:"components"`
	Modals     []describedFn     `json:"modals"`
	Schedules  []describedCron   `json:"schedules"`
	Tasks      []describedFn     `json:"tasks"`
	Subs       []describedFn     `json:"subscriptions"`
	Routes     []describedFn     `json:"routes"`
	Sections   map[string]string `json:"sections"` // section name → typeof
}

type describedFn struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // typeof the handler
	Access string `json:"access,omitempty"`
}

type describedCron struct {
	Name string `json:"name"`
	Cron any    `json:"cron"`
	Type string `json:"type"`
}

// describedLeaf is a command or a subcommand: what can be invoked.
type describedLeaf struct {
	Name        string         `json:"name"`
	Description any            `json:"description"`
	Run         string         `json:"run"`
	Options     []describedOpt `json:"options"`
	OptionsType string         `json:"optionsType"`
}

type describedCmd struct {
	describedLeaf
	Ephemeral       any              `json:"ephemeral"`
	Permissions     any              `json:"permissions"`
	Subcommands     []describedLeaf  `json:"subcommands"`
	SubcommandsType string           `json:"subcommandsType"`
	Groups          []describedGroup `json:"groups"`
	GroupsType      string           `json:"groupsType"`
}

type describedGroup struct {
	Name            string          `json:"name"`
	Description     any             `json:"description"`
	Subcommands     []describedLeaf `json:"subcommands"`
	SubcommandsType string          `json:"subcommandsType"`
}

type describedOpt struct {
	Name         string          `json:"name"`
	Type         any             `json:"type"`
	Description  any             `json:"description"`
	Required     any             `json:"required"`
	Choices      json.RawMessage `json:"choices"`
	Min          any             `json:"min"`
	Max          any             `json:"max"`
	MinLength    any             `json:"minLength"`
	MaxLength    any             `json:"maxLength"`
	Autocomplete string          `json:"autocomplete"` // typeof
}

var knownSections = map[string]bool{
	"onStart": true, "onStop": true, "events": true, "commands": true, "components": true,
	"modals": true, "schedules": true, "tasks": true, "subscriptions": true, "routes": true,
}

var (
	commandNameRe = regexp.MustCompile(`^[-_\p{L}\p{N}]{1,32}$`)
	paramNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	pathSegRe     = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
	subKeyRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}\.[A-Za-z0-9._:-]{1,100}$`)
	topicRe       = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
)

var optionTypes = map[string]discord.CommandOptionType{
	"string": discord.OptionString, "integer": discord.OptionInteger, "number": discord.OptionNumber,
	"boolean": discord.OptionBoolean, "user": discord.OptionUser, "channel": discord.OptionChannel,
	"role": discord.OptionRole, "mentionable": discord.OptionMentionable, "attachment": discord.OptionAttachment,
}

var routeMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
var routeAccess = map[string]bool{"public": true, "scoped": true, "member": true, "moderator": true, "admin": true}

// buildDefinition validates the described plugin and converts it. All problems are
// reported at once.
func buildDefinition(dp describedPlugin) (*Definition, error) {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	d := &Definition{}

	for _, k := range dp.Keys {
		if !knownSections[k] {
			fail("unknown key %q in definePlugin() (expected one of onStart, onStop, events, commands, components, modals, schedules, tasks, subscriptions, routes)", k)
		}
	}
	for name, typ := range dp.Sections {
		if typ != "object" && typ != "undefined" {
			fail("%s must be an object, got %s", name, typ)
		}
	}
	checkFn := func(section, name, typ string) bool {
		if typ != "function" {
			fail("%s.%s must be a function, got %s", section, name, typ)
			return false
		}
		return true
	}
	switch dp.OnStart {
	case "undefined":
	case "function":
		d.HasOnStart = true
	default:
		fail("onStart must be a function, got %s", dp.OnStart)
	}
	switch dp.OnStop {
	case "undefined":
	case "function":
		d.HasOnStop = true
	default:
		fail("onStop must be a function, got %s", dp.OnStop)
	}

	for _, e := range dp.Events {
		if _, ok := eventIntents[e.Name]; !ok {
			fail("unknown event handler events.%s (known: %s)", e.Name, strings.Join(EventNames(), ", "))
			continue
		}
		if checkFn("events", e.Name, e.Type) {
			d.Events = append(d.Events, e.Name)
		}
	}

	for _, c := range dp.Commands {
		cmd, cerrs := buildCommand(c)
		errs = append(errs, cerrs...)
		if len(cerrs) == 0 {
			d.Commands = append(d.Commands, cmd)
		}
	}

	for _, c := range dp.Components {
		if checkLocalID("components", c.Name, &errs) && checkFn("components", c.Name, c.Type) {
			d.Components = append(d.Components, c.Name)
		}
	}
	for _, m := range dp.Modals {
		if checkLocalID("modals", m.Name, &errs) && checkFn("modals", m.Name, m.Type) {
			d.Modals = append(d.Modals, m.Name)
		}
	}
	for _, s := range dp.Schedules {
		expr, ok := s.Cron.(string)
		if !ok {
			fail("schedules.%s.cron must be a string", s.Name)
			continue
		}
		if err := ValidateCron(expr); err != nil {
			fail("schedules.%s: %v", s.Name, err)
			continue
		}
		if s.Type != "function" {
			fail("schedules.%s.run must be a function, got %s", s.Name, s.Type)
			continue
		}
		d.Schedules = append(d.Schedules, ScheduleDef{Name: s.Name, Cron: expr})
	}
	for _, t := range dp.Tasks {
		if t.Name == "" {
			fail("tasks: empty task name")
			continue
		}
		if checkFn("tasks", t.Name, t.Type) {
			d.Tasks = append(d.Tasks, t.Name)
		}
	}
	for _, s := range dp.Subs {
		if !subKeyRe.MatchString(s.Name) {
			fail(`subscriptions: invalid key %q (expected "<plugin>.<topic>")`, s.Name)
			continue
		}
		if checkFn("subscriptions", s.Name, s.Type) {
			d.Subscriptions = append(d.Subscriptions, s.Name)
		}
	}
	seenPatterns := map[string]string{}
	for _, r := range dp.Routes {
		rd, err := parseRouteKey(r.Name)
		if err != nil {
			fail("routes: %v", err)
			continue
		}
		if !routeAccess[r.Access] {
			fail("routes[%q].access must be one of public, scoped, member, moderator, admin (got %q)", r.Name, r.Access)
			continue
		}
		rd.Access = r.Access
		if r.Type != "function" {
			fail("routes[%q].handler must be a function, got %s", r.Name, r.Type)
			continue
		}
		norm := rd.Method + " " + normalizePattern(rd.Path)
		if prev, dup := seenPatterns[norm]; dup {
			fail("routes: %q and %q match the same paths", prev, r.Name)
			continue
		}
		seenPatterns[norm] = r.Name
		d.Routes = append(d.Routes, rd)
	}

	sort.Slice(d.Commands, func(a, b int) bool { return d.Commands[a].Name < d.Commands[b].Name })
	sort.Strings(d.Events)
	sort.Strings(d.Components)
	sort.Strings(d.Modals)
	sort.Slice(d.Schedules, func(a, b int) bool { return d.Schedules[a].Name < d.Schedules[b].Name })
	sort.Strings(d.Tasks)
	sort.Strings(d.Subscriptions)
	sort.Slice(d.Routes, func(a, b int) bool { return d.Routes[a].Key() < d.Routes[b].Key() })

	if len(errs) > 0 {
		return nil, &DefinitionError{Errs: errs}
	}
	return d, nil
}

// DefinitionError lists every problem found in a plugin definition.
type DefinitionError struct{ Errs []error }

func (e *DefinitionError) Error() string {
	parts := make([]string, len(e.Errs))
	for i, err := range e.Errs {
		parts[i] = err.Error()
	}
	return "invalid plugin definition:\n  " + strings.Join(parts, "\n  ")
}

func (e *DefinitionError) Unwrap() []error { return e.Errs }

func checkLocalID(section, id string, errs *[]error) bool {
	switch {
	case id == "":
		*errs = append(*errs, fmt.Errorf("%s: empty id", section))
	case len(id) > 80:
		*errs = append(*errs, fmt.Errorf("%s.%s: id longer than 80 bytes", section, id))
	default:
		return true
	}
	return false
}

func parseRouteKey(key string) (RouteDef, error) {
	method, path, ok := strings.Cut(key, " ")
	if !ok || !routeMethods[method] {
		return RouteDef{}, fmt.Errorf(`invalid route key %q (expected "<GET|POST|PUT|PATCH|DELETE> /path")`, key)
	}
	if !strings.HasPrefix(path, "/") {
		return RouteDef{}, fmt.Errorf("route %q: path must start with /", key)
	}
	seen := map[string]bool{}
	for _, seg := range splitPath(path) {
		if name, isParam := strings.CutPrefix(seg, ":"); isParam {
			if !paramNameRe.MatchString(name) {
				return RouteDef{}, fmt.Errorf("route %q: invalid parameter name %q", key, name)
			}
			if seen[name] {
				return RouteDef{}, fmt.Errorf("route %q: duplicate parameter %q", key, name)
			}
			seen[name] = true
			continue
		}
		if !pathSegRe.MatchString(seg) {
			return RouteDef{}, fmt.Errorf("route %q: invalid path segment %q", key, seg)
		}
	}
	clean := "/" + strings.Join(splitPath(path), "/")
	return RouteDef{Method: method, Path: clean}, nil
}

func normalizePattern(p string) string {
	segs := splitPath(p)
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			segs[i] = ":"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// maxStringOption is the longest value of a string option (min/maxLength bound).
const maxStringOption = 6000

func validName(n string) bool { return commandNameRe.MatchString(n) && strings.ToLower(n) == n }

func validDesc(d any) (string, bool) {
	s, ok := d.(string)
	return s, ok && s != "" && utf8.RuneCountInString(s) <= 100
}

func buildCommand(c describedCmd) (CommandDef, []error) {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("command %q: "+format, append([]any{c.Name}, args...)...))
	}
	cmd := CommandDef{Name: c.Name}
	if !validName(c.Name) {
		fail("invalid name (lowercase letters, digits, - and _, 1-32 characters)")
	}
	desc, ok := validDesc(c.Description)
	if !ok {
		fail("description is required (1-100 characters)")
	}
	cmd.Description = desc
	switch e := c.Ephemeral.(type) {
	case nil:
	case bool:
		cmd.Ephemeral = e
	default:
		fail("ephemeral must be a boolean")
	}
	if c.Permissions != nil {
		list, ok := c.Permissions.([]any)
		if !ok {
			fail("permissions must be an array of permission names")
		}
		for _, p := range list {
			s, ok := p.(string)
			if !ok {
				fail("permissions must be an array of permission names")
				break
			}
			cmd.Permissions = append(cmd.Permissions, s)
		}
		if _, err := discord.ParsePermissions(cmd.Permissions); err != nil {
			fail("%v", err)
		}
	}
	for _, t := range []struct{ name, typ string }{{"subcommands", c.SubcommandsType}, {"groups", c.GroupsType}} {
		if t.typ != "" && t.typ != "undefined" && t.typ != "object" {
			fail("%s must be an object, got %s", t.name, t.typ)
		}
	}
	if len(c.Subcommands) == 0 && len(c.Groups) == 0 {
		if c.SubcommandsType == "object" || c.GroupsType == "object" {
			fail("subcommands/groups declared but empty")
		}
		cmd.Options = buildLeafOptions(c.describedLeaf, fail)
		return cmd, errs
	}

	// A command with subcommands: Discord does not let the command itself be invoked.
	if c.Run != "undefined" {
		fail("a command with subcommands has no run (each subcommand has its own)")
	}
	if c.OptionsType != "undefined" {
		fail("a command with subcommands has no options (declare them on its subcommands)")
	}
	if n := len(c.Subcommands) + len(c.Groups); n > 25 {
		fail("at most 25 subcommands and groups (got %d)", n)
	}
	names := map[string]bool{}
	for _, sc := range c.Subcommands {
		names[sc.Name] = true
		cmd.Options = append(cmd.Options, buildSubcommand(sc, func(format string, args ...any) {
			fail("subcommand %q: "+format, append([]any{sc.Name}, args...)...)
		}))
	}
	for _, g := range c.Groups {
		gfail := func(format string, args ...any) {
			fail("group %q: "+format, append([]any{g.Name}, args...)...)
		}
		if names[g.Name] {
			gfail("a subcommand has the same name")
		}
		names[g.Name] = true
		group := discord.CommandOption{Type: discord.OptionSubcommandGroup, Name: g.Name}
		if !validName(g.Name) {
			gfail("invalid name (lowercase letters, digits, - and _, 1-32 characters)")
		}
		gdesc, ok := validDesc(g.Description)
		if !ok {
			gfail("description is required (1-100 characters)")
		}
		group.Description = gdesc
		if g.SubcommandsType != "object" || len(g.Subcommands) == 0 {
			gfail("a group needs subcommands (an object of subcommand())")
		}
		if len(g.Subcommands) > 25 {
			gfail("at most 25 subcommands (got %d)", len(g.Subcommands))
		}
		for _, sc := range g.Subcommands {
			group.Options = append(group.Options, buildSubcommand(sc, func(format string, args ...any) {
				gfail("subcommand %q: "+format, append([]any{sc.Name}, args...)...)
			}))
		}
		cmd.Options = append(cmd.Options, group)
	}
	return cmd, errs
}

func buildSubcommand(sc describedLeaf, fail func(string, ...any)) discord.CommandOption {
	opt := discord.CommandOption{Type: discord.OptionSubcommand, Name: sc.Name}
	if !validName(sc.Name) {
		fail("invalid name (lowercase letters, digits, - and _, 1-32 characters)")
	}
	desc, ok := validDesc(sc.Description)
	if !ok {
		fail("description is required (1-100 characters)")
	}
	opt.Description = desc
	opt.Options = buildLeafOptions(sc, fail)
	return opt
}

// buildLeafOptions checks run and the options of a command or subcommand and returns the
// options in Discord order (required first).
func buildLeafOptions(l describedLeaf, fail func(string, ...any)) []discord.CommandOption {
	if l.Run != "function" {
		fail("run must be a function, got %s", l.Run)
	}
	if l.OptionsType != "undefined" && l.OptionsType != "object" {
		fail("options must be an object, got %s", l.OptionsType)
	}
	if len(l.Options) > 25 {
		fail("at most 25 options")
	}
	var required, optional []discord.CommandOption
	for _, o := range l.Options {
		opt, oerrs := buildOption(o)
		for _, e := range oerrs {
			fail("option %q: %v", o.Name, e)
		}
		if len(oerrs) > 0 {
			continue
		}
		if opt.Required {
			required = append(required, opt)
		} else {
			optional = append(optional, opt)
		}
	}
	return slices.Concat(required, optional)
}

func buildOption(o describedOpt) (discord.CommandOption, []error) {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	opt := discord.CommandOption{Name: o.Name}
	if !validName(o.Name) {
		fail("invalid name (lowercase letters, digits, - and _, 1-32 characters)")
	}
	typName, _ := o.Type.(string)
	typ, ok := optionTypes[typName]
	if !ok {
		fail("invalid type %v (string, integer, number, boolean, user, channel, role, mentionable, attachment)", o.Type)
	}
	opt.Type = typ
	desc, ok := o.Description.(string)
	if !ok || desc == "" || utf8.RuneCountInString(desc) > 100 {
		fail("description is required (1-100 characters)")
	}
	opt.Description = desc
	switch r := o.Required.(type) {
	case nil:
	case bool:
		opt.Required = r
	default:
		fail("required must be a boolean")
	}
	numeric := typ == discord.OptionInteger || typ == discord.OptionNumber
	for _, mm := range []struct {
		name string
		v    any
		dst  **float64
	}{{"min", o.Min, &opt.MinValue}, {"max", o.Max, &opt.MaxValue}} {
		if mm.v == nil {
			continue
		}
		f, isNum := mm.v.(float64)
		switch {
		case !isNum:
			fail("%s must be a number", mm.name)
		case !numeric:
			fail("%s is only supported on integer and number options", mm.name)
		default:
			*mm.dst = &f
		}
	}
	if opt.MinValue != nil && opt.MaxValue != nil && *opt.MinValue > *opt.MaxValue {
		fail("min is greater than max")
	}
	for _, ml := range []struct {
		name  string
		v     any
		least int
		dst   **int
	}{{"minLength", o.MinLength, 0, &opt.MinLength}, {"maxLength", o.MaxLength, 1, &opt.MaxLength}} {
		if ml.v == nil {
			continue
		}
		f, isNum := ml.v.(float64)
		switch {
		case !isNum || f != float64(int(f)):
			fail("%s must be an integer", ml.name)
		case typ != discord.OptionString:
			fail("%s is only supported on string options", ml.name)
		case f < float64(ml.least) || f > maxStringOption:
			fail("%s must be between %d and %d", ml.name, ml.least, maxStringOption)
		default:
			n := int(f)
			*ml.dst = &n
		}
	}
	if opt.MinLength != nil && opt.MaxLength != nil && *opt.MinLength > *opt.MaxLength {
		fail("minLength is greater than maxLength")
	}
	switch o.Autocomplete {
	case "", "undefined":
	case "function":
		switch {
		case typ != discord.OptionString && !numeric:
			fail("autocomplete is only supported on string, integer and number options")
		case len(o.Choices) > 0 && string(o.Choices) != "null":
			fail("autocomplete and choices are mutually exclusive")
		default:
			opt.Autocomplete = true
		}
	default:
		fail("autocomplete must be a function, got %s", o.Autocomplete)
	}
	if len(o.Choices) > 0 && string(o.Choices) != "null" {
		var choices []struct {
			Name  any `json:"name"`
			Value any `json:"value"`
		}
		if err := json.Unmarshal(o.Choices, &choices); err != nil {
			fail("choices must be an array of {name, value}")
		} else {
			if typ != discord.OptionString && !numeric {
				fail("choices are only supported on string, integer and number options")
			}
			if len(choices) > 25 {
				fail("at most 25 choices")
			}
			for _, ch := range choices {
				name, ok := ch.Name.(string)
				if !ok || name == "" || utf8.RuneCountInString(name) > 100 {
					fail("choice name must be 1-100 characters")
					continue
				}
				switch v := ch.Value.(type) {
				case string:
					if typ != discord.OptionString {
						fail("choice %q: value must be a number", name)
						continue
					}
				case float64:
					if typ == discord.OptionString {
						fail("choice %q: value must be a string", name)
						continue
					}
					if typ == discord.OptionInteger && v != float64(int64(v)) {
						fail("choice %q: value must be an integer", name)
						continue
					}
				default:
					fail("choice %q: value must be a string or a number", name)
					continue
				}
				opt.Choices = append(opt.Choices, discord.CommandChoice{Name: name, Value: ch.Value})
			}
		}
	}
	return opt, errs
}

var errNoDefault = errors.New("the plugin has no default export: use `export default definePlugin({...})`")
