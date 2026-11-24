package sim

import (
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
	if len(cmds) > 100 {
		return badRequest("at most 100 commands")
	}
	names := map[string]bool{}
	for _, c := range cmds {
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
	return nil
}

func validateOptions(cmd string, opts []discord.CommandOption) error {
	if len(opts) > 25 {
		return badRequest("command %q: at most 25 options", cmd)
	}
	names := map[string]bool{}
	optional := false
	for _, o := range opts {
		where := "command " + cmd + " option " + o.Name
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
		if len(o.Choices) > 0 && o.Type != discord.OptionString && !numeric {
			return badRequest("%s: choices only apply to string, integer and number options", where)
		}
		if len(o.Choices) > 25 {
			return badRequest("%s: at most 25 choices", where)
		}
		for _, ch := range o.Choices {
			if n := utf8.RuneCountInString(ch.Name); n < 1 || n > 100 {
				return badRequest("%s: choice name must be 1-100 characters", where)
			}
			switch v := ch.Value.(type) {
			case string:
				if o.Type != discord.OptionString || utf8.RuneCountInString(v) > 100 {
					return badRequest("%s: choice %q has a value of the wrong type", where, ch.Name)
				}
			case float64:
				if !numeric || (o.Type == discord.OptionInteger && v != math.Trunc(v)) {
					return badRequest("%s: choice %q has a value of the wrong type", where, ch.Name)
				}
			case int, int64:
				if !numeric {
					return badRequest("%s: choice %q has a value of the wrong type", where, ch.Name)
				}
			default:
				return badRequest("%s: choice %q has a value of the wrong type", where, ch.Name)
			}
		}
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
