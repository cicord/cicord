package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

// A real portal invocation retains stale shared data across an await. Erasure
// must run after that mutation, including the actual host writes, or A returns.
func TestPrivacyWriteFenceAndRecoverableDeadline(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		manifest := filepath.Join(dir, "plugins/welcome/plugin.yaml")
		requireInWelcome(t, dir, []string{"scheduler", "portal.routes"}, nil)
		grantWelcome(t, dir, "scheduler", "portal.routes")
		replaceWelcomeCode(t, dir)
		raw, err := os.ReadFile(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "  privacy:") {
			raw = append(raw, []byte("\n  privacy:\n    records:\n      - {name: records, policy: erase, description: Shared test records}\n")...)
			if err := os.WriteFile(manifest, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		code := `import {definePlugin} from "@cicord/sdk";
 export default definePlugin({
 routes:{"POST /gate":{access:"admin",async handler(ctx){
 try{await ctx.index("notes").remove("a");throw Error("route bypassed grant");}catch(e){if(!String(e).includes("capability"))throw e;}
 const stale=await ctx.kv.get("shared");await ctx.kv.set("inside",true);
 while(!await ctx.kv.get("release"))await new Promise(r=>setTimeout(r,5));
 await ctx.kv.set("shared",stale);return true;
 }}},
 privacy:{async export(ctx,id){return (await ctx.kv.get("shared"))??[];},
 async erase(ctx,id){
 if(await ctx.kv.get("fail"))throw Error("cannot establish enforcement recovery");
 const shared=(await ctx.kv.get("shared"))??[];await ctx.kv.set("shared",shared.filter(s=>s!==id));
 if(await ctx.kv.get("migrate")){
  await ctx.schedule.after(600000,"expire",{case:4});await ctx.kv.delete("migrate");
 }
 await ctx.index("notes").remove("a"); // previously granted, now revoked
 try{await ctx.index("notes").remove("b");throw Error("removed unrelated member");}catch(e){if(!String(e).includes("binding"))throw e;}
 return {erased:1};
 }},
 tasks:{
 async late(ctx){const snapshot=await ctx.kv.get("shared");setTimeout(async()=>{await ctx.kv.set("late-write",snapshot);},300);},
 async expire(ctx){await ctx.kv.set("raw-side-effect",true);}
 }
 });`
		if err := os.WriteFile(filepath.Join(dir, "plugins/welcome/src/index.ts"), []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
	}, -1)
	ctx := t.Context()
	const server = "my-community"
	const scope = "plugin:moderator/welcome/my-community"
	const a = "111111111111111111"
	const b = "222222222222222222"
	st := e.rt.Store()
	kv := st.KV(scope)
	idx := st.Index(scope)
	put := func(k, raw string) {
		t.Helper()
		if err := kv.Set(ctx, k, json.RawMessage(raw), 0); err != nil {
			t.Fatal(err)
		}
	}
	put("shared", `["`+a+`","`+b+`"]`)
	for _, x := range []struct{ id, member string }{{"a", a}, {"b", b}, {"auto", a}} {
		if err := idx.Upsert(ctx, "notes", "en", x.id, state.IndexDocument{Text: "private", Tags: []string{"member-" + x.member}}); err != nil {
			t.Fatal(err)
		}
	}
	var inst *instanceRT
	for _, i := range e.rt.Active().instances {
		if instanceScope(i.cfg) == scope {
			inst = i
		}
	}
	if inst == nil {
		t.Fatal("no instance")
	}
	if err := inst.inst().RunTask(ctx, "late", nil, plugin.TaskDelivery{ID: "late", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, _, err := e.rt.HandleRoute(ctx, server, "moderator", "welcome", "POST", "/gate", plugin.RouteRequest{})
		finished <- err
	}()
	e.waitFor("portal holds old shared value", func() bool { _, err := kv.Get(ctx, "inside"); return err == nil })
	eraseDone := make(chan error, 1)
	go func() { _, err := e.rt.MemberData(ctx, server, a, "erase"); eraseDone <- err }()
	select {
	case <-eraseDone:
		t.Fatal("erase passed a pending portal mutation")
	case <-time.After(50 * time.Millisecond):
	}
	put("release", "true")
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-eraseDone; err != nil {
		t.Fatal(err)
	}
	raw, err := kv.Get(ctx, "shared")
	if err != nil || string(raw) != `["`+b+`"]` {
		t.Fatalf("stale portal write survived: %s, %v", raw, err)
	}
	hits, err := idx.Search(ctx, "notes", "en", "private", state.IndexQuery{})
	if err != nil || len(hits) != 1 || hits[0].ID != "b" {
		t.Fatalf("revoked index cleanup: %v %v", hits, err)
	}
	put("migrate", "true")
	put("fail", "true")
	t0 := time.Now().Add(time.Hour)
	old := state.Task{ID: "original-enforcement", Scope: scope, Name: "expire", RunAt: t0, Payload: json.RawMessage(`{"case":4,"user":"` + a + `"}`)}
	q := st.Tasks()
	if err := q.Schedule(ctx, old); err != nil {
		t.Fatal(err)
	}
	rep, err := e.rt.MemberData(ctx, server, a, "erase")
	if err != nil || rep.Complete || rep.Tasks != 0 {
		t.Fatal(rep, err)
	}
	for n := range 7 {
		leases, err := q.ClaimDue(ctx, t0.Add(time.Duration(n)*time.Minute), t0.Add(time.Duration(n+1)*time.Minute), "test", 1, onboardingScope)
		if err != nil || len(leases) != 1 || leases[0].Failures != 0 {
			t.Fatalf("deadline lost or counted as handler failure: %v %v", leases, err)
		}
		e.rt.sched.slots <- struct{}{}
		e.rt.sched.deliverTask(ctx, inst, leases[0], time.Now().Add(10*time.Second))
	}
	if _, err := kv.Get(ctx, "raw-side-effect"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("raw timer executed", err)
	}
	if err := kv.Delete(ctx, "fail"); err != nil {
		t.Fatal(err)
	}
	rep, err = e.rt.MemberData(ctx, server, a, "erase")
	if err != nil || rep.Tasks != 1 {
		t.Fatal(rep, err)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := kv.Get(ctx, "late-write"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("a detached timer resurrected a pre-erasure snapshot", err)
	}

	due, err := q.Due(ctx, t0.Add(24*time.Hour), 100)
	var tasks []state.Task
	for _, task := range due {
		if task.Scope == scope { // not onboarding's own migration task
			tasks = append(tasks, task)
		}
	}
	if err != nil || len(tasks) != 1 || string(tasks[0].Payload) != `{"case":4}` {
		t.Fatal(tasks, err)
	}
}
