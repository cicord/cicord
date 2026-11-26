package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
		write("bots/"+key+"/bot.yaml", doc("Bot", key, fmt.Sprintf("  applicationId: '%s'\n  token: {secretRef: %s}\n  servers: [a,b]\n  directMessages: {instance: support}\n  plugins:\n    - {name: support, source: ../../plugins/help, grants: [storage.kv, discord.dm.send]}", app, secret)))
	}
	write("plugins/help/plugin.yaml", "apiVersion: cicord.dev/v1alpha1\nkind: Plugin\nmetadata: {name: help, version: 0.1.0}\nspec: {entry: ./src/index.ts, engine: '>=0.1.0', intents: [DirectMessages], capabilities: [storage.kv, discord.dm.send]}\n")
	write("plugins/help/src/index.ts", `import {definePlugin} from "@cicord/sdk"; export default definePlugin({events:{async directMessage(ctx,m){await ctx.kv.set(m.content,ctx.server.id); await m.member.send("received "+ctx.server.key)}}});`)
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
				if len(m.Components) > 0 {
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
	// Advance the conversation beyond 24h without a day-long sleep.
	h := rt.bot("first")
	barrier := make(chan struct{})
	h.queueDM(func() {
		h.dm.conversations[user.ID].expires = h.dm.conversations[user.ID].expires.Add(-dmTTL)
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
