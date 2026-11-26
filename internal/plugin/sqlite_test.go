package plugin

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// TestWithSQLiteStore runs the storage-backed SDK calls against the real state store.
func TestWithSQLiteStore(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := "plugin:moderator/inst/my-community"
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    run: command({ description: "x", async run(ctx, it) {
      await ctx.kv.set("a:1", { v: 1 });
      await ctx.kv.set("a:2", [2]);
      await ctx.kv.set("a:3", "three", { ttlSeconds: 3600 });
      await ctx.kv.set("b", null);
      const n = await ctx.kv.increment("n", 2.5);
      const p1 = await ctx.kv.list({ prefix: "a:", limit: 2 });
      const p2 = await ctx.kv.list({ prefix: "a:", cursor: p1.cursor });
      const { id } = await ctx.schedule.after(1000, "t", { x: 1 });
      const ch = await ctx.server.createChannel({ name: "ticket" });
      await ctx.server.deleteChannel({ id: ch.id });
      await it.reply(JSON.stringify({ n, p1, p2, id, b: await ctx.kv.get("b") }));
    } }),
  },
  tasks: { t() {} },
});`, func(e *Env) {
		e.KV, e.Tasks, e.Resources, e.TaskScope = store.KV(scope), store.Tasks(), store.Resources(), scope
	})
	if err := h.command("run", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		N      float64 `json:"n"`
		P1, P2 struct {
			Items []struct {
				Key   string          `json:"key"`
				Value json.RawMessage `json:"value"`
			} `json:"items"`
			Cursor string `json:"cursor"`
		}
		ID string          `json:"id"`
		B  json.RawMessage `json:"b"`
	}
	content := contentOf(h.responses()[0])
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatal(err, content)
	}
	if got.N != 2.5 || string(got.B) != "null" {
		t.Fatalf("got %s", content)
	}
	keys := func(p struct {
		Items []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		} `json:"items"`
		Cursor string `json:"cursor"`
	}) (out []string) {
		for _, it := range p.Items {
			out = append(out, it.Key+"="+string(it.Value))
		}
		return
	}
	if k := keys(got.P1); !reflect.DeepEqual(k, []string{`a:1={"v":1}`, "a:2=[2]"}) || got.P1.Cursor == "" {
		t.Fatalf("page 1: %v %q", k, got.P1.Cursor)
	}
	if k := keys(got.P2); !reflect.DeepEqual(k, []string{`a:3="three"`}) || got.P2.Cursor != "" {
		t.Fatalf("page 2: %v %q", k, got.P2.Cursor)
	}
	due, err := store.Tasks().Due(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].ID != got.ID || due[0].Scope != scope || strings.TrimSpace(string(due[0].Payload)) != `{"x":1}` {
		t.Fatalf("due = %+v %v", due, err)
	}
	if list, _ := store.Resources().List(context.Background(), "my-community"); len(list) != 0 {
		t.Fatalf("resources = %+v", list)
	}
	if len(h.dc.Calls("DeleteChannel")) != 1 {
		t.Fatal("owned channel not deleted")
	}
}

func TestAdvancedStorageAndCryptoInVM(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h := start(t, `
 import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{run:command({description:"advanced",async run(ctx,it){
 const k=ctx.kv, r=k.ranking("xp");
 const won=await k.atomic().check("m",null).set("m",null).rankSet("xp","m",10).commit();
 const both=await k.getMany(["m","missing"]);
 if (!won.ok||both[0]!==null||both[1]!==undefined||!(await k.getEntry("m")).version) throw Error("atomic");
 if (await r.rank("m")!==1 || (await r.around("m",0))[0].member!=="m") throw Error("rank");
 const a=new Uint8Array(32); if (crypto.getRandomValues(a)!==a || a.every(x=>x===0)) throw Error("random");
 const id=crypto.randomUUID();if(!/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(id)) throw Error("uuid");
 await it.reply("passed");
 }})}});`, func(e *Env) { e.KV = store.KV("plugin:test/storage/s") })
	if err := h.command("run", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != "passed" {
		t.Fatal(got)
	}
}
