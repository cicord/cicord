package plugin

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
	instance, _ := i.env.Config["locale"].(string)
	var interaction string
	if inv != nil {
		interaction = inv.locale
	}
	return i.env.Locales.Text(p.Key, p.Vars, p.Locale, instance, interaction, i.env.ServerPreferredLocale), nil
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
		if err := localizeOptions(o.Options, messages, prefix, tag); err != nil {
			return err
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
	return nil
}
