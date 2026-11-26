package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

const welcomeScope = "plugin:moderator/welcome/my-community"

func welcomeInstance(e *env) *instanceRT {
	for _, i := range e.rt.Active().instances {
		if i.cfg.Name == "welcome" {
			return i
		}
	}
	e.t.Fatal("welcome instance missing")
	return nil
}

// CORE-R1: a poison task that crash-loops the VM dead-letters itself, and only itself.
// Its innocent neighbours (due at the same time, waiting or running when the VM died)
// are released without a failure, in both delivery modes.
func TestCrashLoopDeadLettersOnlyThePoisonTask(t *testing.T) {
	for _, mode := range []string{"at-least-once", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			old := restartMin
			restartMin = 20 * time.Millisecond // copied by New
			t.Cleanup(func() { restartMin = old })
			e := startExampleResync(t, nil, func(dir string) {
				welcomeWith(t, dir, mode != "legacy", "scheduler")
				replaceIn(t, filepath.Join(dir, "plugins/welcome/src/index.ts"), "  events: {", `  tasks: {
       async boom(ctx) { const a = []; for (;;) a.push("x".repeat(1 << 20) + Math.random()); },
       async unban(ctx, p, task) { if (!(await ctx.kv.get(task.id))) { await ctx.kv.set(task.id, true); await ctx.kv.increment("unbans"); } }
     },
     events: {`)
			}, -1)
			ctx := t.Context()
			q := e.rt.Store().Tasks()
			now := time.Now().Add(time.Hour)
			ids := []string{"poison", "tempban-1", "tempban-2", "tempban-3"}
			for n, id := range ids {
				name := "unban"
				if n == 0 {
					name = "boom"
				}
				if err := q.Schedule(ctx, state.Task{ID: id, Scope: welcomeScope, Name: name, RunAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			wi := welcomeInstance(e)
			for round := 0; round < 12; round++ {
				e.waitFor("instance serving", func() bool { cur := wi.inst(); return cur != nil && cur.Err() == nil })
				e.rt.sched.runTasks(ctx, now)
				e.waitFor("deliveries settled", func() bool { return len(e.rt.sched.slots) == 0 })
				now = now.Add(10 * time.Minute) // past every retry delay
				if dead, _ := q.DeadLetters(ctx, "my-community", 10); len(dead) > 0 {
					break
				}
			}
			dead, err := q.DeadLetters(ctx, "my-community", 10)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "legacy" {
				// At most once: the poison was consumed when its handler started, and is
				// never replayed. No innocent task is parked either.
				left, _ := q.Due(ctx, now, 10)
				if len(dead) != 0 || len(left) != 0 {
					t.Fatalf("legacy: dead = %+v, queued = %+v", dead, left)
				}
				return
			}
			if len(dead) != 1 || dead[0].ID != "poison" || dead[0].Failures != 5 || dead[0].Reason != "handler crashed the instance" {
				t.Fatalf("dead letters = %+v, want only the poison with 5 failures", dead)
			}
			// Innocent tasks keep their whole retry budget and eventually run.
			for round := 0; round < 6; round++ {
				e.waitFor("instance serving", func() bool { cur := wi.inst(); return cur != nil && cur.Err() == nil })
				e.rt.sched.runTasks(ctx, now)
				e.waitFor("deliveries settled", func() bool { return len(e.rt.sched.slots) == 0 })
				now = now.Add(10 * time.Minute)
			}
			left, err := q.Due(ctx, now, 10)
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range left {
				if task.ID != "poison" {
					t.Errorf("innocent task %s still queued", task.ID)
				}
			}
			if dead, _ := q.DeadLetters(ctx, "my-community", 10); len(dead) != 1 {
				t.Fatalf("dead letters after recovery = %+v", dead)
			}
			if mode != "legacy" {
				if v, err := e.rt.Store().KV(welcomeScope).Get(ctx, "unbans"); err != nil || string(v) != "3" {
					t.Fatalf("unbans = %s, %v; want 3", v, err)
				}
			}
		})
	}
}

// CORE-R2 / SPEC-4: SDK-01 asks for five deliveries with a 1 s → 5 min backoff.
func TestRetryDelayFollowsSDK01(t *testing.T) {
	want := []time.Duration{time.Second, 7 * time.Second, 45 * time.Second, 5 * time.Minute}
	for failures, nominal := range want {
		for range 50 {
			d := retryDelay(failures)
			if d > nominal || d < nominal*4/5 {
				t.Fatalf("retryDelay(%d) = %s, want within [%s, %s]", failures, d, nominal*4/5, nominal)
			}
		}
	}
	if d := retryDelay(4); d != 0 {
		t.Fatalf("fifth failure must dead-letter, got retry in %s", d)
	}
	total := time.Duration(0)
	for _, d := range want {
		total += d
	}
	if total < 5*time.Minute {
		t.Fatalf("retry budget %s survives no outage longer than a few seconds", total)
	}
}

func publishTo(t *testing.T, e *env, publisher, id, topic string, version int, at time.Time) {
	t.Helper()
	ev := plugin.Event{Meta: plugin.EventMeta{ID: id, Topic: topic, Version: version, At: at, Publisher: plugin.Caller{Plugin: "polls", Bot: "moderator", Instance: publisher}}, Payload: json.RawMessage(`{}`)}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	err = e.rt.Store().Interop().PublishEvent(t.Context(), state.EventFanout{ID: id, Server: "my-community", Publisher: "plugin:moderator/" + publisher + "/my-community", Payload: payload, Topic: topic, Subscribers: []string{welcomeScope}, At: at})
	if err != nil {
		t.Fatal(err)
	}
}

// subscribingWelcome adds subscription handlers (object entries) to the welcome plugin.
func subscribingWelcome(t *testing.T, handlers string) *env {
	return startExampleResync(t, nil, func(dir string) {
		welcomeWith(t, dir, true, "events.subscribe")
		// The test topics are legacy subscriptions: without spec.interop, welcome receives
		// them as before 0.2 (its manifest declares versioned topics only).
		dropWelcomeSpec(t, dir, "interop")
		// Into welcome's own subscriptions object when it has one (a second key would
		// replace it).
		src := filepath.Join(dir, "plugins/welcome/src/index.ts")
		if data, _ := os.ReadFile(src); strings.Contains(string(data), "\n  subscriptions: {\n") {
			replaceFirst(t, src, "\n  subscriptions: {\n", "\n  subscriptions: {\n    "+handlers+"\n")
		} else {
			replaceIn(t, src, "  events: {", "  subscriptions: { "+handlers+" },\n  events: {")
		}
	}, -1)
}

// CORE-R3: the next event of a stream is claimed as soon as its predecessor is
// acknowledged, not at the next 1 s tick (which capped a stream at 1 event/s).
func TestEventStreamDrainsWithoutWaitingForTicks(t *testing.T) {
	e := subscribingWelcome(t, `"polls.closed": async (ctx) => { await ctx.kv.increment("seen"); },`)
	const n = 60
	at := time.Now().Add(-time.Second)
	for k := range n {
		publishTo(t, e, "polls", fmt.Sprintf("ev-%03d", k), "polls.closed", 1, at)
	}
	start := time.Now()
	kv := e.rt.Store().KV(welcomeScope)
	e.waitFor("stream drained", func() bool {
		v, err := kv.Get(t.Context(), "seen")
		return err == nil && string(v) == fmt.Sprint(n)
	})
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("%d events on one stream took %s", n, took)
	}
}

// CORE-R6: a queued event whose subscription the revision no longer declares is not a
// handler failure. It waits like an undeclared task and is parked after 24 hours.
func TestUndeclaredSubscriptionIsNotAFailure(t *testing.T) {
	e := subscribingWelcome(t, `"polls.closed": async (ctx) => { await ctx.kv.increment("seen"); },`)
	ctx := t.Context()
	q := e.rt.Store().Tasks()
	// Version 2 is not declared: legacy subscribers only receive version 1.
	publishTo(t, e, "polls", "fresh", "polls.closed", 2, time.Now())
	now := time.Now()
	for range 6 {
		now = now.Add(2 * time.Minute)
		e.rt.sched.runTasks(ctx, now)
		e.waitFor("settled", func() bool { return len(e.rt.sched.slots) == 0 })
	}
	if dead, _ := q.DeadLetters(ctx, "my-community", 10); len(dead) != 0 {
		t.Fatalf("an undeclared subscription dead-lettered its events: %+v", dead)
	}
	leases, err := q.ClaimDue(ctx, now.Add(time.Hour), now.Add(2*time.Hour), "probe", 10, onboardingScope)
	if err != nil || len(leases) != 1 || leases[0].Failures != 0 {
		t.Fatalf("leases = %+v, %v; want the event with no failure", leases, err)
	}
	if err := q.Release(ctx, leases[0], time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	for _, i := range e.rt.Status().Bots[0].Plugins {
		if i.Instance == "welcome" && i.Health != HealthHealthy {
			t.Fatalf("welcome health = %s (%s)", i.Health, i.Message)
		}
	}
	// Another stream: "fresh" is still the head of the first one.
	publishTo(t, e, "other-polls", "old", "polls.closed", 2, time.Now().Add(-25*time.Hour))
	e.rt.sched.runTasks(ctx, time.Now())
	e.waitFor("settled", func() bool { return len(e.rt.sched.slots) == 0 })
	dead, err := q.DeadLetters(ctx, "my-community", 10)
	if err != nil || len(dead) != 1 || dead[0].Attempts != 1 || dead[0].Reason != "subscription no longer declared for 24 hours" {
		t.Fatalf("dead = %+v, %v", dead, err)
	}
}

// CORE-R12: dead letters carry a host-defined error category, never plugin text.
func TestDeliveryFailureCategories(t *testing.T) {
	for _, row := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("task %q: %w: %w: oom", "boom", plugin.ErrCrashedByHandler, plugin.ErrCrashed), "handler crashed the instance"},
		{fmt.Errorf("x: %w", plugin.ErrHandlerTimeout), "handler timed out"},
		{fmt.Errorf("x: %w", plugin.ErrBudgetExceeded), "handler exceeded its execution budget"},
		{&plugin.ScriptError{Name: "TypeError", Message: "member 1234 has no roles"}, "handler failed: TypeError"},
		{&plugin.ScriptError{Name: "Alice#1234", Message: "x"}, "handler failed: script error"},
		{&plugin.ScriptError{Name: "InteropError", InteropCode: "timeout"}, "handler failed: interop timeout"},
		{&plugin.ScriptError{Name: "InteropError", InteropCode: "member 1234"}, "handler failed: interop error"},
		{&plugin.ScriptError{Name: "CapabilityError", Capability: "discord.ban"}, "handler failed: capability denied"},
		{errors.New("member 1234 not found"), "handler failed"},
	} {
		if got := deliveryFailure(row.err); got != row.want {
			t.Errorf("deliveryFailure(%v) = %q, want %q", row.err, got, row.want)
		}
	}
}
