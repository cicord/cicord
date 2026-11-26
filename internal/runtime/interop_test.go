package runtime

import (
	"encoding/json"
	"fmt"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/state"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInteropMixedVersionsAndCalls(t *testing.T) {
	var secondApp string
	extra := func(w *sim.World, server string) map[string]string {
		token, bot, app := w.CreateBot("Rewards")
		secondApp = app
		if _, err := w.AddBotToServer(server, bot.ID, discord.MustPermissions("Administrator")); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"SECOND_TOKEN": token}
	}
	e := startExampleBots(t, nil, func(dir string) {
		write := func(path, text string) {
			path = filepath.Join(dir, path)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		makePlugin := func(name, manifest, code string) {
			write("plugins/"+name+"/plugin.yaml", fmt.Sprintf("apiVersion: cicord.dev/v1alpha1\nkind: Plugin\nmetadata: {name: %s, version: 0.2.0}\nspec:\n  engine: '>=0.1.0'\n  entry: ./index.ts\n%s\n", name, manifest))
			write("plugins/"+name+"/index.ts", code)
		}
		makePlugin("polls", `  capabilities: [events.publish, portal.routes, plugins.query:levels.read, plugins.call:levels.credit, plugins.query:missing.read, plugins.query:levels.loop]
  optional: [{feature: optionalBonus, capabilities: [plugins.call:levels.other]}]
  delivery: at-least-once
  interop:
    publishes: [{topic: closed, version: 1, scope: server}, {topic: closed, version: 2, scope: server}]
    consumes: {queries: [levels.read,missing.read,levels.loop], actions: [levels.credit,levels.other]}`, `import {definePlugin,InteropError,PluginUnavailableError} from "@cicord/sdk";
 export default definePlugin({routes:{"POST /probe":{access:"member",async handler(ctx,req){
 const b=req.body;try{
 if(b.op==="emit"){const voters=Array.from({length:4000},(_,n)=>String(110000000000000000n+BigInt(n)));
 for(const version of [1,2])await ctx.events.publish("closed",{voters},{version,idempotencyKey:"poll:1"});return true}
 if(b.op==="badEvent"){const name=b.topic;await ctx.events.publish(name,{voters:3});return true}
 if(b.op==="has")return [ctx.plugins.has("levels"),ctx.plugins.has("missing"),ctx.features.enabled("optionalBonus")];
 if(b.op==="query")return await ctx.plugins.query(b.plugin||"levels",b.name||"read",b.input||{},b.opts);
 return await ctx.plugins.call("levels",b.name||"credit",b.input,b.opts)
 }catch(e){return {code:e.code||e.name, unavailable:e instanceof PluginUnavailableError,typed:e instanceof InteropError}}}}}})`)
		for _, v := range []string{"1", "2"} {
			write("plugins/polls/contracts/closed.v"+v+".json", `{"type":"object","required":["voters"],"properties":{"voters":{"type":"array","items":{"type":"string"}}}}`)
		}
		makePlugin("legacy", `  capabilities: [events.subscribe,storage.kv]`, `import {definePlugin} from "@cicord/sdk";export default definePlugin({subscriptions:{"polls.closed":async(ctx,p,meta)=>{await ctx.kv.increment("credited",p.voters.length);throw Error("legacy post-effect failure")}}})`)
		makePlugin("levels", `  capabilities: [events.subscribe,storage.kv,plugins.query:levels.loop]
  delivery: at-least-once
  interop:
    subscribes: [{topic: polls.closed,version: 2}]
    provides: {queries: [read,loop], actions: [credit]}
    consumes: {queries: [levels.loop]}`, `import {definePlugin,query,action} from "@cicord/sdk";
 export default definePlugin({
 queries:{read:query({async run(ctx,input,call){if(input.write){await ctx.kv.set("forbidden",true)};if(input.wait){await new Promise(r=>setTimeout(r,200))};return {caller:call.caller,onBehalfOf:call.onBehalfOf||null,total:await ctx.kv.get("balance")||0}}}),
 loop:query({async run(ctx){return ctx.plugins.query("levels","loop",{})}})},
 actions:{credit:action({async run(ctx,input,call){const total=await ctx.kv.increment("balance",input.amount);await ctx.kv.set("action",call.idempotencyKey);if(input.fail)throw Error("after staged write");return {total}}})},
 subscriptions:{"polls.closed":async(ctx,p,m)=>{const seen=await ctx.kv.get("event");
 if(!seen){await ctx.kv.set("credited",p.voters.length);await ctx.kv.set("event",m);throw Error("lost acknowledgement")}
 if(seen.id!==m.id||m.version!==2)throw Error("wrong replay");await ctx.kv.set("retried",m.id)}}})`)
		for _, n := range []string{"read", "loop"} {
			write("plugins/levels/contracts/"+n+".v1.json", `{"input":{"type":"object"},"output":{"type":"object"}}`)
		}
		write("plugins/levels/contracts/credit.v1.json", `{"input":{"type":"object","required":["amount"],"properties":{"amount":{"type":"integer"}}},"output":{"type":"object","required":["total"],"properties":{"total":{"type":"integer"}}}}`)
		appendTo(t, filepath.Join(dir, "bots/moderator/bot.yaml"), `
    - name: polls
      source: ../../plugins/polls
      grants: [events.publish, portal.routes, plugins.query:levels.read, plugins.call:levels.credit, plugins.query:missing.read, plugins.query:levels.loop]
    - name: legacy
      source: ../../plugins/legacy
      grants: [events.subscribe,storage.kv]
`)
		write("bots/rewards/bot.yaml", fmt.Sprintf(`apiVersion: cicord.dev/v1alpha1
kind: Bot
metadata: {name: rewards}
spec:
  applicationId: "%s"
  token: {secretRef: SECOND_TOKEN}
  servers: [my-community]
  plugins:
    - name: points
      source: ../../plugins/levels
      grants: [events.subscribe,storage.kv,plugins.query:levels.loop]
`, secondApp))
	}, -1, extra)
	ctx := t.Context()
	caller := func(body string) string {
		resp, _, err := e.rt.HandleRoute(ctx, "my-community", "moderator", "polls", "POST", "/probe", plugin.RouteRequest{Body: json.RawMessage(body), Viewer: &plugin.RouteViewer{User: e.bot, Member: &discord.Member{User: e.bot}}})
		if err != nil {
			t.Error(err)
			return ""
		}
		return string(resp.Body)
	}
	if got := caller(`{"op":"has"}`); got != "[true,false,false]" {
		t.Fatal(got)
	}
	for _, row := range []struct{ body, want string }{
		{`{"op":"query","plugin":"missing"}`, `"unavailable":true`},
		{`{"op":"query","name":"loop"}`, `"code":"cycle"`},
		{`{"op":"query","input":{"write":true}}`, `"code":"handler_error"`},
		{`{"op":"query","input":{"wait":true},"opts":{"timeoutMs":15}}`, `"code":"timeout"`},
		{`{"op":"action","input":{},"opts":{"idempotencyKey":"a"}}`, `"code":"invalid_input"`},
		{`{"op":"action","input":{"amount":1}}`, `"code":"invalid_input"`},
		{`{"op":"action","name":"other","input":{},"opts":{"idempotencyKey":"a"}}`, `"code":"denied"`},
		{`{"op":"action","input":{"amount":2,"fail":true},"opts":{"idempotencyKey":"b"}}`, `"code":"handler_error"`},
		{`{"op":"query","opts":{"caller":{"source":"cicord"},"onBehalfOf":{"access":"admin"}}}`, `"source":""`},
		{`{"op":"badEvent","topic":"closed"}`, `"code":"Error"`},
		{`{"op":"badEvent","topic":"renamed"}`, `"code":"Error"`},
	} {
		if got := caller(row.body); !strings.Contains(got, row.want) {
			t.Fatalf("%s got %s want %s", row.body, got, row.want)
		}
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := caller(`{"op":"action","input":{"amount":50},"opts":{"idempotencyKey":"poll:4:u"}}`); got != `{"total":50}` {
				t.Error(got)
			}
		}()
	}
	wg.Wait()
	read := caller(`{"op":"query"}`)
	if !strings.Contains(read, `"access":"member"`) || !strings.Contains(read, `"total":50`) {
		t.Fatal(read)
	}
	for range 2 {
		if got := caller(`{"op":"emit"}`); got != "true" {
			t.Fatal(got)
		}
	}
	levels := e.rt.Store().KV("plugin:rewards/points/my-community")
	legacy := e.rt.Store().KV("plugin:moderator/legacy/my-community")
	e.waitFor("versioned replay", func() bool { _, err := levels.Get(ctx, "retried"); return err == nil })
	for _, kv := range []state.KV{levels, legacy} {
		v, err := kv.Get(ctx, "credited")
		if err != nil || string(v) != "4000" {
			t.Fatalf("credit %s %v", v, err)
		}
	}
	if _, err := levels.Get(ctx, "forbidden"); err == nil {
		t.Fatal("read-only provider wrote to KV")
	}
	if len(e.rt.InteropGraph("my-community").Edges) == 0 {
		t.Fatal("missing interop edges")
	}
}
