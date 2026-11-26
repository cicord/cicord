package runtime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// Exercise the scheduler, persisted queue and JS handler together. The first worker
// disappears with its lease. The replacement's effect succeeds but the handler fails.
func TestDurableTaskDelivery(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		welcomeWith(t, dir, true, "scheduler")
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  tasks: {
       async expire(ctx, payload, task) {
         if (!(await ctx.kv.get(task.id))) {
           await ctx.kv.set(task.id, true);
           await ctx.kv.increment("effects");
         }
         if (task.attempt < 7) throw new Error("simulated downstream failure");
         await ctx.kv.set("finalAttempt", task.attempt);
       }
     },
     events: {`)
	}, -1)
	ctx := t.Context()
	q := e.rt.Store().Tasks()
	now := time.Now().Add(time.Hour)
	scope := "plugin:moderator/welcome/my-community"
	if err := q.Schedule(ctx, state.Task{ID: "expiry", Scope: scope, Name: "expire", RunAt: now}); err != nil {
		t.Fatal(err)
	}
	abandoned, err := q.ClaimDue(ctx, now, now.Add(time.Minute), "abandoned", 1, onboardingScope)
	if err != nil || len(abandoned) != 1 {
		t.Fatalf("initial claim: %v, %v", abandoned, err)
	}
	for attempt := 2; attempt <= 6; attempt++ {
		now = now.Add(time.Minute)
		e.rt.sched.runTasks(ctx, now)
		e.waitFor("scheduler settled delivery", func() bool { return len(e.rt.sched.slots) == 0 })
	}
	dead, err := q.DeadLetters(ctx, "my-community", 100)
	if err != nil || len(dead) != 1 || dead[0].Failures != 5 || dead[0].Attempts != 6 {
		t.Fatalf("dead: %+v %v", dead, err)
	}
	ok, err := q.ResolveDead(ctx, "my-community", "expiry", true, now)
	if !ok || err != nil {
		t.Fatalf("manual retry: %v %v", ok, err)
	}
	e.rt.sched.runTasks(ctx, now)
	e.waitFor("retry settled", func() bool { return len(e.rt.sched.slots) == 0 })
	for key, want := range map[string]string{"effects": "1", "finalAttempt": "7"} {
		if v, err := e.rt.Store().KV(scope).Get(ctx, key); err != nil || string(v) != want {
			t.Errorf("%s = %s, %v; want %s", key, v, err, want)
		}
	}
}

// A legacy job that was only leased can still start. Once the runtime enters
// its JS handler, even a post-effect error or an expired old lease cannot replay it.
func TestLegacyTaskBeginsOnce(t *testing.T) {
	e := startExampleResync(t, nil, func(dir string) {
		welcomeWith(t, dir, false, "scheduler")
		replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  tasks:{async legacy(ctx){await ctx.kv.increment("runs");throw Error("lost result")}},events: {`)
	}, -1)
	ctx := t.Context()
	q := e.rt.Store().Tasks()
	when := time.Now().Add(time.Hour)
	scope := "plugin:moderator/welcome/my-community"
	if err := q.Schedule(ctx, state.Task{ID: "legacy-1", Name: "legacy", Scope: scope, RunAt: when}); err != nil {
		t.Fatal(err)
	}
	old, err := q.ClaimDue(ctx, when, when.Add(time.Minute), "previous", 1, onboardingScope)
	if err != nil || len(old) != 1 {
		t.Fatal(old, err)
	}
	e.rt.sched.runTasks(ctx, when.Add(2*time.Minute))
	e.waitFor("legacy handler", func() bool { return len(e.rt.sched.slots) == 0 })
	e.rt.sched.runTasks(ctx, when.Add(3*time.Minute))
	if fired, err := q.Consume(ctx, old[0]); err != nil || fired {
		t.Fatal(fired, err)
	}
	if v, err := e.rt.Store().KV(scope).Get(ctx, "runs"); err != nil || string(v) != "1" {
		t.Fatal(v, err)
	}
	if dead, err := q.DeadLetters(ctx, "my-community", 10); err != nil || len(dead) != 0 {
		t.Fatal(dead, err)
	}
}
