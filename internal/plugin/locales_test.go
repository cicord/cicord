package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/locales"
)

func TestTranslationPriorityAndRegistration(t *testing.T) {
	catalog := locales.Catalog{"en": {"hello": "Hello {name}"}, "fr": {"hello": "Bonjour {name}", "commands.greet.name": "salut", "commands.greet.description": "Saluer", "commands.greet.options.name.description": "Prénom"}}
	def, err := Inspect(bundleSource(t, `import {command,definePlugin} from "@cicord/sdk";export default definePlugin({commands:{greet:command({description:"Greet",options:{name:{type:"string",description:"Name"}},run(){}})}});`))
	if err != nil {
		t.Fatal(err)
	}
	if err := def.ApplyLocales(catalog); err != nil {
		t.Fatal(err)
	}
	c := def.DiscordCommands()[0]
	if c.Name != "greet" || c.Names["fr"] != "salut" || c.Options[0].Descriptions["fr"] != "Prénom" {
		t.Fatalf("%+v", c)
	}
	h := start(t, `import {command,definePlugin} from "@cicord/sdk";export default definePlugin({commands:{greet:command({description:"Greet",run(ctx,it){ctx.log.info("greeting",{text:ctx.i18n.t("hello",{name:"Ada"})})}})}});`, func(e *Env) { e.Config = map[string]any{}; e.Locales = catalog; e.ServerPreferredLocale = "en" })
	it := discord.Interaction{ID: "i", Token: "test", ApplicationID: "app", ServerID: "10", ChannelID: "100", Type: discord.InteractionCommand, User: discord.User{ID: "42"}, Locale: "fr-CA", CommandName: "greet"}
	if err := h.inst.HandleInteraction(context.Background(), it, "command", "greet"); err != nil {
		t.Fatal(err)
	}
	if got := h.logged("greeting").Attrs["text"]; got != "Bonjour Ada" {
		t.Fatalf("%v", got)
	}
	h.inst.env.Config["locale"] = "en"
	got, err := opTranslate(h.inst, &invocation{locale: "fr"}, json.RawMessage(`{"key":"hello","vars":{"name":"Ada"}}`))
	if err != nil || got != "Hello Ada" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestLocalizedContextMenus(t *testing.T) {
	def, err := Inspect(bundleSource(t, `import {definePlugin} from "@cicord/sdk";
 export default definePlugin({contextMenus:{
  "About user":{type:"user",run(){}},
  "Open ticket on message":{type:"message",run(){}}
 }});`))
	if err != nil {
		t.Fatal(err)
	}
	fr := map[string]string{"contextMenus.About user.name": "Ouvrir un Ticket", "contextMenus.Open ticket on message.name": "Ouvrir un Ticket"}
	if err = def.ApplyLocales(locales.Catalog{"fr": fr}); err != nil {
		t.Fatal(err)
	}
	cmds := def.DiscordCommands()
	if len(cmds) != 2 || cmds[0].Name != "About user" || cmds[1].Name != "Open ticket on message" || cmds[0].Type != discord.UserMenu || cmds[1].Type != discord.MessageMenu {
		t.Fatalf("compiled menus %+v", cmds)
	}
	for _, cmd := range cmds {
		if cmd.Names["fr"] != "Ouvrir un Ticket" || len(cmd.Descriptions) != 0 {
			t.Fatalf("localized menu %+v", cmd)
		}
	}
	for _, invalid := range []string{"", strings.Repeat("é", 33)} {
		fr["contextMenus.About user.name"] = invalid
		if err := def.ApplyLocales(locales.Catalog{"fr": fr}); err == nil {
			t.Fatalf("accepted invalid localized menu name %q", invalid)
		}
	}
}

func TestContextMenuCollisionWithFallback(t *testing.T) {
	def := Definition{ContextMenus: []CommandDef{{Name: "About user", Type: discord.UserMenu}, {Name: "Profil", Type: discord.UserMenu}}}
	err := def.ApplyLocales(locales.Catalog{"fr": {"contextMenus.About user.name": "Profil"}})
	if err == nil || !strings.Contains(err.Error(), "fr") {
		t.Fatalf("expected French menu collision, got %v", err)
	}
}

// Fixed choices are translated from commands.<command>.options.<option>.choices.<value>
// (subcommands: commands.<command>.<subcommand>.options...), the value staying the key.
func TestLocalizedChoices(t *testing.T) {
	def, err := Inspect(bundleSource(t, `import {command,definePlugin} from "@cicord/sdk";export default definePlugin({commands:{
 timeout:command({description:"Timeout",options:{duration:{type:"integer",description:"Duration",choices:[{name:"1 hour",value:60},{name:"1 day",value:1440}]}},run(){}}),
 mod:command({description:"Moderation",subcommands:{ban:{description:"Ban",options:{reason:{type:"string",description:"Reason",choices:[{name:"Spam",value:"spam"}]}},run(){}}}})}});`))
	if err != nil {
		t.Fatal(err)
	}
	fr := map[string]string{
		"commands.timeout.options.duration.choices.60": "1 heure",
		"commands.mod.ban.options.reason.choices.spam": "Pourriel",
	}
	if err := def.ApplyLocales(locales.Catalog{"fr": fr, "en": {"commands.timeout.options.duration.choices.1440": "One day"}}); err != nil {
		t.Fatal(err)
	}
	byName := map[string]discord.Command{}
	for _, c := range def.DiscordCommands() {
		byName[c.Name] = c
	}
	choices := byName["timeout"].Options[0].Choices
	if choices[0].Name != "1 hour" || choices[0].Value != float64(60) || choices[0].Names["fr"] != "1 heure" || choices[1].Names["en-US"] != "One day" || choices[1].Names["fr"] != "" {
		t.Fatalf("timeout choices %+v", choices)
	}
	if c := byName["mod"].Options[0].Options[0].Choices[0]; c.Names["fr"] != "Pourriel" || c.Value != "spam" {
		t.Fatalf("subcommand choice %+v", c)
	}
	fr["commands.timeout.options.duration.choices.60"] = strings.Repeat("x", 101)
	if err := def.ApplyLocales(locales.Catalog{"fr": fr}); err == nil {
		t.Fatal("accepted a choice name of 101 characters")
	}
}
