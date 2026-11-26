package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

type privateDiscord struct {
	*fakeDiscord
	denied bool
}

func (d *privateDiscord) AudienceSnapshot(string) discord.AudienceSnapshot {
	p := contentRead
	if d.denied {
		p = 0
	}
	return discord.AudienceSnapshot{OwnerID: "owner", Roles: []discord.Role{{ID: "10", Permissions: p}}, Channels: map[string]discord.Channel{"100": {ID: "100", ServerID: "10", Type: discord.ChannelText}}}
}
func TestPrivateCommandDurableReply(t *testing.T) {
	for _, mode := range []string{"restart", "ambiguous", "french", "completed", "lost-edit", "revoked", "expired", "wrong-source", "erased"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlite.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			sec, err := secrets.Open(ctx, store, make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			records := store.KV("private-replies:test")
			source := `import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{ask:command({description:"test",async run(ctx,it){
 await it.deferTask("question",{id:it.id});}})},tasks:{async question(ctx,p,task){
 await ctx.kv.increment("charges",1);
 if (ctx.config.ambiguous) throw Error("synthetic response lost after provider charge");
 await task.interaction.reply({content:"private answer",ephemeral:true});
 if (ctx.config.completed) throw Error("crash after successful response");
 }}});`
			code := bundleSource(t, source)
			var client *privateDiscord
			mod := func(e *Env) {
				e.KV = store.KV("plugin:test")
				e.Tasks = store.Tasks()
				e.DeferredRecords = records
				e.DeferredSecrets = sec
				e.DeferredSource = "reviewed#hash"
				client = &privateDiscord{fakeDiscord: e.Discord.(*fakeDiscord)}
				client.members["42"] = discord.Member{User: discord.User{ID: "42"}, ServerID: "10"}
				e.Discord = client
				e.Config = map[string]any{"ambiguous": mode == "ambiguous" || mode == "french", "completed": mode == "completed"}
			}
			h := startCode(t, code, mod)
			if err = h.command("ask", nil, discord.Resolved{}); err != nil {
				t.Fatal(err)
			}
			if got := h.responses(); len(got) != 1 || got[0].Type != discord.ResponseDeferredMessage || got[0].Data.Flags != discord.MessageFlagEphemeral {
				t.Fatalf("private defer: %+v", got)
			}
			tasks, err := store.Tasks().Due(ctx, time.Now().Add(time.Minute), 10)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("tasks: %v %v", tasks, err)
			}
			task := tasks[0]
			if strings.Contains(string(task.Payload), "tok") || strings.Contains(string(task.Payload), "private answer") {
				t.Fatal("queue leaked private credential/content")
			}
			entries, _, _ := records.List(ctx, "", 10, "")
			if len(entries) != 1 || strings.Contains(string(entries[0].Value), `"Token"`) {
				t.Fatal("receipt not encrypted")
			}
			if err = h.inst.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			h = startCode(t, code, mod) // new VM over the durable store; no original interaction handle
			switch mode {
			case "revoked":
				client.denied = true
			case "lost-edit":
				client.hook = func(_ context.Context, name string, _ []any) error {
					if name == "EditOriginalResponse" {
						return errors.New("transport failed with token tok")
					}
					return nil
				}
			case "wrong-source":
				h.inst.env.DeferredSource = "reviewed#changed"
			case "erased":
				_ = records.Delete(ctx, entries[0].Key)
			case "french":
				data, _ := sec.OpenEphemeral(h.inst.deferredBinding(task.ID), entries[0].Value)
				var r deferredReceipt
				_ = json.Unmarshal(data, &r)
				r.Locale = "fr-FR"
				data, _ = json.Marshal(r)
				sealed, _ := sec.SealEphemeral(h.inst.deferredBinding(task.ID), data)
				_ = records.Set(ctx, entries[0].Key, sealed, time.Minute)
			case "expired":
				data, _ := sec.OpenEphemeral(h.inst.deferredBinding(task.ID), entries[0].Value)
				var r deferredReceipt
				_ = json.Unmarshal(data, &r)
				r.ExpiresAt = time.Now().Add(-time.Second)
				data, _ = json.Marshal(r)
				sealed, _ := sec.SealEphemeral(h.inst.deferredBinding(task.ID), data)
				_ = records.Set(ctx, entries[0].Key, sealed, time.Minute)
			}
			for attempt := 1; attempt <= 2; attempt++ {
				err = h.inst.RunTask(ctx, task.Name, task.Payload, TaskDelivery{ID: task.ID, Attempt: attempt})
				if mode == "restart" && err != nil {
					t.Fatal(err)
				}
			}
			raw, err := h.inst.env.KV.Get(ctx, "charges")
			if mode == "expired" || mode == "wrong-source" || mode == "erased" {
				if !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("unwanted execution %s %v", raw, err)
				}
			} else if string(raw) != "1" {
				t.Fatalf("provider charge repeated: %s %v", raw, err)
			}
			h.logs.mu.Lock()
			logs, _ := json.Marshal(*h.logs.entries)
			h.logs.mu.Unlock()
			if strings.Contains(string(logs), "transport failed with token tok") {
				t.Fatal("credential leaked to logs")
			}
			edits := client.Calls("EditOriginalResponse")
			if mode == "restart" || mode == "ambiguous" || mode == "french" || mode == "completed" || mode == "lost-edit" {
				if len(edits) != 1 {
					t.Fatalf("edits %d", len(edits))
				}
				content := edits[0].Args[2].(discord.MessageCreate).Content
				if mode == "french" && !strings.Contains(content, "interrompue") {
					t.Fatal("missing French private fallback", content)
				}
				if mode == "ambiguous" && !strings.Contains(content, "interrupted") {
					t.Fatal("missing private fallback", content)
				}
				if (mode == "completed" || mode == "lost-edit") && content != "private answer" {
					t.Fatal("completed answer clobbered", content)
				}
			} else if len(edits) != 0 {
				t.Fatalf("unauthorized/ambiguous reply %v", edits)
			}
		})
	}
}
