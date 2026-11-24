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
	// Ephemeral: replies are ephemeral by default and auto-defer is ephemeral.
	Ephemeral bool
	// Permissions are Cicord permission names (default member permissions).
	Permissions []string
	// Options in Discord order: required options first, then declaration order.
	Options []discord.CommandOption
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
	"memberJoin":     {"ServerMembers"},
	"memberLeave":    {"ServerMembers"},
	"memberUpdate":   {"ServerMembers"},
	"messageCreate":  {"ServerMessages"},
	"messageUpdate":  {"ServerMessages"},
	"messageDelete":  {"ServerMessages"},
	"reactionAdd":    {"ServerMessageReactions"},
	"reactionRemove": {"ServerMessageReactions"},
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
	switch ev.(type) {
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

type describedCmd struct {
	Name        string         `json:"name"`
	Description any            `json:"description"`
	Ephemeral   any            `json:"ephemeral"`
	Permissions any            `json:"permissions"`
	Run         string         `json:"run"`
	Options     []describedOpt `json:"options"`
	OptionsType string         `json:"optionsType"`
}

type describedOpt struct {
	Name        string          `json:"name"`
	Type        any             `json:"type"`
	Description any             `json:"description"`
	Required    any             `json:"required"`
	Choices     json.RawMessage `json:"choices"`
	Min         any             `json:"min"`
	Max         any             `json:"max"`
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
var routeAccess = map[string]bool{"public": true, "member": true, "moderator": true, "admin": true}

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
			fail("routes[%q].access must be one of public, member, moderator, admin (got %q)", r.Name, r.Access)
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

func buildCommand(c describedCmd) (CommandDef, []error) {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("command %q: "+format, append([]any{c.Name}, args...)...))
	}
	cmd := CommandDef{Name: c.Name}
	if !commandNameRe.MatchString(c.Name) || strings.ToLower(c.Name) != c.Name {
		fail("invalid name (lowercase letters, digits, - and _, 1-32 characters)")
	}
	desc, ok := c.Description.(string)
	if !ok || desc == "" || utf8.RuneCountInString(desc) > 100 {
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
	if c.Run != "function" {
		fail("run must be a function, got %s", c.Run)
	}
	if c.OptionsType != "undefined" && c.OptionsType != "object" {
		fail("options must be an object, got %s", c.OptionsType)
	}
	if len(c.Options) > 25 {
		fail("at most 25 options")
	}
	var required, optional []discord.CommandOption
	for _, o := range c.Options {
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
	cmd.Options = slices.Concat(required, optional)
	return cmd, errs
}

func buildOption(o describedOpt) (discord.CommandOption, []error) {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	opt := discord.CommandOption{Name: o.Name}
	if !commandNameRe.MatchString(o.Name) || strings.ToLower(o.Name) != o.Name {
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
