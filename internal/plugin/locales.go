package plugin

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/locales"
)

func opTranslate(i *Instance, inv *invocation, args json.RawMessage) (any, error) {
	p, err := decode[struct {
		Key    string         `json:"key"`
		Vars   map[string]any `json:"vars"`
		Locale string         `json:"locale"`
	}](args)
	if err != nil {
		return nil, err
	}
	if len(p.Key) > 256 || len(p.Vars) > 50 {
		return nil, argErr("invalid translation arguments")
	}
	for _, v := range p.Vars {
		if s, ok := v.(string); ok && len(s) > locales.MaxText {
			return nil, argErr("i18n.t: a variable exceeds %d bytes", locales.MaxText)
		}
	}
	instance, _ := i.env.Config["locale"].(string)
	var interaction string
	if inv != nil {
		interaction = inv.locale
	}
	text, err := i.env.Locales.Text(p.Key, p.Vars, p.Locale, instance, interaction, i.env.ServerPreferredLocale)
	if err != nil {
		return nil, argErr("i18n.t %q: %s", p.Key, err)
	}
	return text, nil
}

func discordLocales(tag string) []string {
	if tag == "en" {
		return []string{"en-US", "en-GB"}
	}
	switch tag {
	case "en-US", "en-GB", "bg", "zh-CN", "zh-TW", "hr", "cs", "da", "nl", "fi", "fr", "de", "el", "hi", "hu", "id", "it", "ja", "ko", "lt", "no", "pl", "pt-BR", "ro", "ru", "es-ES", "es-419", "sv-SE", "th", "tr", "uk", "vi":
		return []string{tag}
	}
	return nil
}
func validLocalizedName(s string) bool {
	if n := utf8.RuneCountInString(s); n < 1 || n > 32 || strings.ToLower(s) != s {
		return false
	}
	for _, r := range s {
		if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) {
			return false
		}
	}
	return true
}
func fillLabels(labels *discord.CommandLabels, messages map[string]string, key, tag string) error {
	for _, field := range []string{"name", "description"} {
		v, ok := messages[key+"."+field]
		if !ok {
			continue
		}
		if field == "name" && !validLocalizedName(v) || field == "description" && (utf8.RuneCountInString(v) > 100 || v == "") {
			return fmt.Errorf("%s in %s has an invalid Discord %s", key, tag, field)
		}
		table := &labels.Names
		if field == "description" {
			table = &labels.Descriptions
		}
		if *table == nil {
			*table = map[string]string{}
		}
		for _, d := range discordLocales(tag) {
			(*table)[d] = v
		}
	}
	return nil
}
func localizeOptions(options []discord.CommandOption, messages map[string]string, key, tag string) error {
	for n := range options {
		o := &options[n]
		prefix := key + ".options." + o.Name
		if o.IsSubcommand() {
			prefix = key + "." + o.Name
		}
		if err := fillLabels(&o.CommandLabels, messages, prefix, tag); err != nil {
			return err
		}
		if err := localizeChoices(o.Choices, messages, prefix, tag); err != nil {
			return err
		}
		if err := localizeOptions(o.Options, messages, prefix, tag); err != nil {
			return err
		}
	}
	return nil
}

// localizeChoices translates the names of an option's fixed choices from
// "<option key>.choices.<value>" (the value as written in the plugin: "60", "ban"). The
// value stays the routing key; only the label shown in Discord changes.
func localizeChoices(choices []discord.CommandChoice, messages map[string]string, key, tag string) error {
	for n := range choices {
		c := &choices[n]
		var value string
		switch v := c.Value.(type) {
		case string:
			value = v
		case float64:
			value = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			continue
		}
		k := key + ".choices." + value
		label, ok := messages[k]
		if !ok {
			continue
		}
		if n := utf8.RuneCountInString(label); n < 1 || n > 100 {
			return fmt.Errorf("%s in %s: a choice name must be 1–100 characters", k, tag)
		}
		if c.Names == nil {
			c.Names = map[string]string{}
		}
		for _, d := range discordLocales(tag) {
			c.Names[d] = label
		}
	}
	return nil
}

// ApplyLocales keeps canonical command names as routing keys.
func (d *Definition) ApplyLocales(c locales.Catalog) error {
	for _, tag := range slices.Sorted(maps.Keys(c)) {
		messages := c[tag]
		if len(discordLocales(tag)) == 0 {
			continue
		}
		for n := range d.ContextMenus {
			menu := &d.ContextMenus[n]
			key := "contextMenus." + menu.Name + ".name"
			value, ok := messages[key]
			if !ok {
				continue
			}
			if !discord.ValidMenuName(value) {
				return fmt.Errorf("%s in %s: context menu name must be 1–32 characters", key, tag)
			}
			if menu.Names == nil {
				menu.Names = map[string]string{}
			}
			for _, locale := range discordLocales(tag) {
				menu.Names[locale] = value
			}
		}
		for n := range d.Commands {
			command := &d.Commands[n]
			key := "commands." + command.Name
			if err := fillLabels(&command.CommandLabels, messages, key, tag); err != nil {
				return err
			}
			if err := localizeOptions(command.Options, messages, key, tag); err != nil {
				return err
			}
		}
	}
	menus := make([]discord.Command, len(d.ContextMenus))
	for i, menu := range d.ContextMenus {
		menus[i] = menu.DiscordCommand()
	}
	if clash := discord.LocalizedMenuCollision(menus); clash != nil {
		return fmt.Errorf("context menus %q and %q both display as %q in %s", menus[clash.First].Name, menus[clash.Second].Name, clash.Name, clash.Locale)
	}
	return nil
}
