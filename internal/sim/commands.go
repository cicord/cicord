package sim

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
)

var commandNameRe = regexp.MustCompile(`^[-_\p{L}\p{N}]{1,32}$`)

func validCommandName(n string) bool {
	return commandNameRe.MatchString(n) && strings.ToLower(n) == n
}

func validDescription(d string) bool {
	n := utf8.RuneCountInString(d)
	return n >= 1 && n <= 100
}

func validateCommands(cmds []discord.Command) error {
	counts := map[discord.CommandType]int{}
	names := map[string]bool{}
	for _, c := range cmds {
		if c.Type == discord.UserMenu || c.Type == discord.MessageMenu {
			key := c.Type.Key(c.Name)
			counts[c.Type]++
			if counts[c.Type] > 15 || names[key] || !discord.ValidMenuName(c.Name) || c.Description != "" || len(c.Options) > 0 {
				return badRequest("invalid or duplicate context menu %q (limit 15 per type)", c.Name)
			}
			for locale, name := range c.Names {
				if !discord.ValidMenuName(name) {
					return badRequest("invalid context menu name %q in %s", name, locale)
				}
			}
			names[key] = true
			continue
		}
		counts[discord.ChatInput]++
		if counts[discord.ChatInput] > 100 {
			return badRequest("at most 100 commands")
		}
		if !validCommandName(c.Name) {
			return badRequest("invalid command name %q (lowercase, 1-32 of letters, digits, - and _)", c.Name)
		}
		if names[c.Name] {
			return badRequest("duplicate command %q", c.Name)
		}
		names[c.Name] = true
		if !validDescription(c.Description) {
			return badRequest("command %q: description must be 1-100 characters", c.Name)
		}
		if err := validateOptions(c.Name, c.Options); err != nil {
			return err
		}
	}
	if clash := discord.LocalizedMenuCollision(cmds); clash != nil {
		return badRequest("duplicate context menu %q in %s", clash.Name, clash.Locale)
	}
	return nil
}

// validateOptions checks the options of a command (depth 0), of a group (1) or of a
// subcommand (2): a command holds plain options or subcommands and groups, a group holds
// only subcommands and a subcommand only plain options.
func validateOptions(cmd string, opts []discord.CommandOption) error {
	return validateOptionsAt(cmd, opts, 0)
}

func validateOptionsAt(where string, opts []discord.CommandOption, depth int) error {
	if len(opts) > 25 {
		return badRequest("command %s: at most 25 options", where)
	}
	subs := 0
	for _, o := range opts {
		if o.IsSubcommand() {
			subs++
		}
	}
	if subs > 0 && subs != len(opts) {
		return badRequest("command %s: subcommands cannot be mixed with other options", where)
	}
	names := map[string]bool{}
	optional := false
	for _, o := range opts {
		where := "command " + where + " option " + o.Name
		if !validCommandName(o.Name) {
			return badRequest("%s: invalid option name", where)
		}
		if names[o.Name] {
			return badRequest("%s: duplicate option", where)
		}
		names[o.Name] = true
		if !validDescription(o.Description) {
			return badRequest("%s: description must be 1-100 characters", where)
		}
		switch o.Type {
		case discord.OptionSubcommandGroup:
			if depth != 0 {
				return badRequest("%s: a group can only be a top-level option", where)
			}
		case discord.OptionSubcommand:
			if depth > 1 {
				return badRequest("%s: subcommands cannot be nested in subcommands", where)
			}
		default:
			if depth == 1 {
				return badRequest("%s: a group holds only subcommands", where)
			}
		}
		if o.IsSubcommand() {
			if o.Required || len(o.Choices) > 0 || o.MinValue != nil || o.MaxValue != nil || o.MinLength != nil || o.MaxLength != nil || o.Autocomplete {
				return badRequest("%s: subcommands and groups have no value settings", where)
			}
			if o.Type == discord.OptionSubcommandGroup && len(o.Options) == 0 {
				return badRequest("%s: a group needs subcommands", where)
			}
			next := 2
			if o.Type == discord.OptionSubcommandGroup {
				next = 1
			}
			if err := validateOptionsAt(strings.TrimPrefix(where, "command "), o.Options, next); err != nil {
				return err
			}
			continue
		}
		if len(o.Options) > 0 {
			return badRequest("%s: only subcommands and groups have options", where)
		}
		if o.Type < discord.OptionString || o.Type > discord.OptionAttachment {
			return badRequest("%s: unsupported option type %d", where, o.Type)
		}
		if o.Required && optional {
			return badRequest("%s: required options must come before optional ones", where)
		}
		if !o.Required {
			optional = true
		}
		numeric := o.Type == discord.OptionInteger || o.Type == discord.OptionNumber
		if (o.MinValue != nil || o.MaxValue != nil) && !numeric {
			return badRequest("%s: min/max values only apply to integer and number options", where)
		}
		if o.MinValue != nil && o.MaxValue != nil && *o.MinValue > *o.MaxValue {
			return badRequest("%s: min value above max value", where)
		}
		if o.MinLength != nil || o.MaxLength != nil {
			if o.Type != discord.OptionString {
				return badRequest("%s: min/max length only apply to string options", where)
			}
			if (o.MinLength != nil && (*o.MinLength < 0 || *o.MinLength > 6000)) || (o.MaxLength != nil && (*o.MaxLength < 1 || *o.MaxLength > 6000)) {
				return badRequest("%s: min length must be 0-6000 and max length 1-6000", where)
			}
			if o.MinLength != nil && o.MaxLength != nil && *o.MinLength > *o.MaxLength {
				return badRequest("%s: min length above max length", where)
			}
		}
		if o.Autocomplete {
			if o.Type != discord.OptionString && !numeric {
				return badRequest("%s: autocomplete only applies to string, integer and number options", where)
			}
			if len(o.Choices) > 0 {
				return badRequest("%s: autocomplete and choices are mutually exclusive", where)
			}
		}
		if len(o.Choices) > 0 && o.Type != discord.OptionString && !numeric {
			return badRequest("%s: choices only apply to string, integer and number options", where)
		}
		if len(o.Choices) > 25 {
			return badRequest("%s: at most 25 choices", where)
		}
		for _, ch := range o.Choices {
			if err := validateChoice(o, ch); err != nil {
				return badRequest("%s: %v", where, err)
			}
		}
	}
	return nil
}

// validateChoice checks a choice (declared or returned by an autocomplete) against its
// option.
func validateChoice(o discord.CommandOption, ch discord.CommandChoice) error {
	numeric := o.Type == discord.OptionInteger || o.Type == discord.OptionNumber
	if n := utf8.RuneCountInString(ch.Name); n < 1 || n > 100 {
		return fmt.Errorf("choice name must be 1-100 characters")
	}
	switch v := ch.Value.(type) {
	case string:
		if o.Type != discord.OptionString || utf8.RuneCountInString(v) > 100 {
			return fmt.Errorf("choice %q has a value of the wrong type", ch.Name)
		}
	case float64:
		if !numeric || (o.Type == discord.OptionInteger && v != math.Trunc(v)) {
			return fmt.Errorf("choice %q has a value of the wrong type", ch.Name)
		}
	case int, int64:
		if !numeric {
			return fmt.Errorf("choice %q has a value of the wrong type", ch.Name)
		}
	default:
		return fmt.Errorf("choice %q has a value of the wrong type", ch.Name)
	}
	return nil
}

func (w *World) setServerCommands(a actor, applicationID, serverID string, cmds []discord.Command) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) error { return w.record(a, "SetServerCommands", serverID, applicationID, "", err) }
	if !a.isBot() || a.appID != applicationID {
		return fail(forbidden("cannot manage commands of application %s", applicationID))
	}
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if s.members[a.userID] == nil {
		return fail(forbidden("missing access to server %s", serverID))
	}
	if err := validateCommands(cmds); err != nil {
		return fail(err)
	}
	stored := make([]discord.Command, len(cmds))
	for i, c := range cmds {
		stored[i] = cloneCommand(c)
	}
	if w.commands[applicationID] == nil {
		w.commands[applicationID] = map[string][]discord.Command{}
	}
	w.commands[applicationID][serverID] = stored
	return fail(nil)
}

// Commands returns the commands an application registered in a server.
func (w *World) Commands(applicationID, serverID string) []discord.Command {
	w.mu.Lock()
	defer w.mu.Unlock()
	cmds := w.commands[applicationID][serverID]
	out := make([]discord.Command, len(cmds))
	for i, c := range cmds {
		out[i] = cloneCommand(c)
	}
	return out
}

// CommandsFor lists the commands shown to a particular member in a channel.
// Commands remains the complete application registry for operator inspection.
func (w *World) CommandsFor(applicationID, serverID, channelID, userID string) ([]discord.Command, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _, ch, err := w.interactionContext(userID, serverID, channelID)
	if err != nil {
		return nil, err
	}
	if ch.ch.Type == discord.ChannelDM {
		return nil, badRequest("guild commands require a server channel")
	}
	perms := w.channelPerms(ch, userID)
	out := []discord.Command{}
	for _, c := range w.commands[applicationID][serverID] {
		if commandAllowed(c, perms) {
			out = append(out, cloneCommand(c))
		}
	}
	return out, nil
}

func commandAllowed(c discord.Command, perms discord.Permissions) bool {
	if !perms.Has(permUseAppCommands) {
		return false
	}
	required := c.DefaultMemberPermissions
	return required == nil || perms == allPermissions || (*required != 0 && perms.Has(*required))
}
