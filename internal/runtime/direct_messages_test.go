package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// Two bot identities install "support" on both servers. Each has a separate DM
// conversation and each instance has a separate real SQLite namespace.
func TestDirectMessageSelection(t *testing.T) {
	w := sim.NewWorld()
	owner, user := w.CreateUser("owner"), w.CreateUser("alice")
	a, b := w.CreateServer("A", owner.ID), w.CreateServer("B", owner.ID)
	dir := t.TempDir()
	write := func(path, data string) {
		t.Helper()
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	doc := func(kind, key, spec string) string {
		return "apiVersion: cicord.dev/v1alpha1\nkind: " + kind + "\nmetadata: {name: " + key + "}\nspec:\n" + spec + "\n"
	}
	write("cicord.yaml", doc("Project", "support", "  engine: '>=0.1.0'"))
	for key, sid := range map[string]string{"a": a.ID, "b": b.ID} {
		write("servers/"+key+".yaml", doc("Server", key, "  discordId: '"+sid+"'"))
		if err := w.AddMember(sid, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	secrets := map[string]string{}
	bots := map[string]discord.User{}
	for _, key := range []string{"first", "second"} {
		token, bot, app := w.CreateBot(key)
		bots[key] = bot
		secret := "TOKEN_" + map[string]string{"first": "FIRST", "second": "SECOND"}[key]
		secrets[secret] = token
		for _, sid := range []string{a.ID, b.ID} {
			if _, err := w.AddBotToServer(sid, bot.ID, discord.MustPermissions("Administrator")); err != nil {
				t.Fatal(err)
			}
		}
		write("bots/"+key+"/bot.yaml", doc("Bot", key, fmt.Sprintf("  applicationId: '%s'\n  token: {secretRef: %s}\n  servers: [a,b]\n  directMessages: {instance: support}\n  plugins:\n    - {name: support, source: ../../plugins/help, grants: [storage.kv, discord.dm.send, discord.messages.read]}", app, secret)))
	}
	write("plugins/help/plugin.yaml", "apiVersion: cicord.dev/v1alpha1\nkind: Plugin\nmetadata: {name: help, version: 0.1.0}\nspec: {entry: ./src/index.ts, engine: '>=0.1.0', intents: [DirectMessages], capabilities: [storage.kv, discord.dm.send, discord.messages.read]}\n")
	write("plugins/help/src/index.ts", `import {definePlugin} from "@cicord/sdk"; export default definePlugin({events:{async directMessage(ctx,m){if(m.content.startsWith("retract ")){await ctx.server.retractDM(m.content.slice(8))} else {
 const dm=await m.member.send({content:"received "+ctx.server.key,components:[{type:"row",components:[{type:"button",id:"ack",label:"Confirm",style:"primary"}]}]},{retractable:true});await ctx.kv.set("lastReceipt",dm.id)}
 await ctx.kv.set(m.content,ctx.server.id)}},components:{async ack(ctx,i){
 let protectedHistory=false;try{await i.channel.messages({limit:1})}catch{protectedHistory=true}
 await ctx.kv.set("ack",protectedHistory && i.member.id===i.user.id && i.channel.type==="dm");await i.reply("received");
 }}});`)
	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rt := New(Options{Source: src, Connector: w, Store: db, Mode: "dev", Simulator: true, Secrets: func(n string) (string, bool) { v, ok := secrets[n]; return v, ok }, ResyncInterval: -1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rt.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	e := env{t: t, rt: rt}
	e.waitFor("revision", func() bool { _, ok := rt.Revision(); return ok })
	send := func(key, content string) {
		t.Helper()
		ch, err := w.DMChannel(user.ID, bots[key].ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.SendMessage(user.ID, ch.ID, content); err != nil {
			t.Fatal(err)
		}
	}
	prompt := func(key string) discord.Message {
		t.Helper()
		var msg discord.Message
		e.waitFor("server selection", func() bool {
			for _, m := range w.DMs(user.ID, bots[key].ID) {
				if len(m.Components) > 0 && m.Components[0].Components[0].Type == discord.ComponentStringSelect {
					msg = m
					return true
				}
			}
			return false
		})
		return msg
	}
	stored := func(bot, server, content string) bool {
		v, _ := db.KV("plugin:"+bot+"/support/"+server).Get(ctx, content)
		return len(v) > 0
	}
	choose := func(key, sid string, m discord.Message) {
		t.Helper()
		h := w.Select(user.ID, m, m.Components[0].Components[0].CustomID, []string{sid})
		if h.Err() != nil {
			t.Fatal(h.Err())
		}
	}
	send("first", "original")
	m1 := prompt("first")
	send("second", "other")
	m2 := prompt("second")
	for _, bot := range []string{"first", "second"} {
		for _, server := range []string{"a", "b"} {
			if stored(bot, server, "original") || stored(bot, server, "other") {
				t.Fatal("tenant received a held DM")
			}
		}
	}
	choose("first", b.ID, m1)
	choose("second", a.ID, m2)
	e.waitFor("separate selections", func() bool { return stored("first", "b", "original") && stored("second", "a", "other") })
	send("first", "cached")
	e.waitFor("cached server", func() bool { return stored("first", "b", "cached") })
	if stored("first", "a", "original") || stored("second", "b", "other") {
		t.Fatal("crossed tenant boundaries")
	}
	// A retractable DM is private to its issuer, even for another installation on
	// the very same bot DM channel. The receipt survives individual handler calls.
	raw, err := db.KV("plugin:first/support/b").Get(ctx, "lastReceipt")
	if err != nil {
		t.Fatal(err)
	}
	var receipt string
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	for _, m := range w.DMs(user.ID, bots["first"].ID) {
		if m.ID == receipt {
			click := w.ClickButton(user.ID, m, m.Components[0].Components[0].CustomID)
			if err := click.Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.waitFor("scoped DM component", func() bool {
		value, _ := db.KV("plugin:first/support/b").Get(ctx, "ack")
		return string(value) == "true"
	})
	if stored("first", "a", "ack") {
		t.Fatal("DM component dispatched to the wrong tenant")
	}
	text := "retract " + receipt
	send("second", text)
	e.waitFor("foreign retraction ignored", func() bool { return stored("second", "a", text) })
	alive := func() bool {
		for _, m := range w.DMs(user.ID, bots["first"].ID) {
			if m.ID == receipt {
				return true
			}
		}
		return false
	}
	if !alive() {
		t.Fatal("foreign instance retracted a DM")
	}
	send("first", text)
	e.waitFor("own retraction", func() bool { return stored("first", "b", text) && !alive() })
	// Advance the conversation beyond 24h without a day-long sleep.
	h := rt.bot("first")
	barrier := make(chan struct{})
	h.queueDM(user.ID, func() {
		_ = h.saveChoice(ctx, user.ID, "support", b.ID, time.Now().Add(-time.Second))
		close(barrier)
	})
	<-barrier
	send("first", "expired")
	m3 := prompt("first")
	if stored("first", "b", "expired") {
		t.Fatal("expired choice reused")
	}
	if err := w.RemoveMember(a.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	choose("first", a.ID, m3)
	e.waitFor("revoked membership", func() bool {
		for _, m := range w.DMs(user.ID, bots["first"].ID) {
			if m.Content == "This server is unavailable." {
				return true
			}
		}
		return false
	})
	if stored("first", "a", "expired") || stored("first", "b", "expired") {
		t.Fatal("delivered after membership revocation")
	}
}
