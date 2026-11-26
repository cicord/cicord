package statetest

import (
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

func testTaskLeases(t *testing.T, b Backend) {
	store, clock := open(t, b)
	q, c := store.Tasks(), ctx(t)
	now := clock.Now()
	task := state.Task{ID: "expiry", Scope: "plugin:bot/test/server-a", Name: "expire", RunAt: now}
	must(t, q.Schedule(c, task))
	claim := func(worker string, want int) state.TaskLease {
		t.Helper()
		claimed, err := q.ClaimDue(c, now, now.Add(time.Minute), worker, 1)
		must(t, err)
		if len(claimed) != want {
			t.Fatalf("%s claimed %d tasks, want %d", worker, len(claimed), want)
		}
		if want == 0 {
			return state.TaskLease{}
		}
		return claimed[0]
	}
	beforeCrash := claim("worker-a", 1)
	claim("worker-b", 0)
	now = now.Add(time.Minute)
	replay := claim("worker-b", 1)
	if replay.ID != beforeCrash.ID || replay.Attempt != 2 || replay.Receipt == beforeCrash.Receipt {
		t.Fatalf("invalid redelivery: %+v", replay)
	}
	must(t, q.Acknowledge(c, beforeCrash))
	must(t, q.Release(c, replay, now.Add(time.Second), "handler failed"))
	claim("worker-b", 0)
	now = now.Add(time.Second)
	lease := claim("worker-b", 1)
	if lease.Failures != 1 || lease.Attempt != 3 {
		t.Fatalf("retry state: %+v", lease)
	}
	must(t, q.Release(c, lease, time.Time{}, "handler failed"))
	claim("worker-b", 0)
	dead, err := q.DeadLetters(c, "server-a", 10)
	must(t, err)
	if len(dead) != 1 || dead[0].ID != task.ID || dead[0].Failures != 2 {
		t.Fatalf("dead letters: %+v", dead)
	}
	changed, err := q.ResolveDead(c, "server-b", task.ID, true, now)
	must(t, err)
	if changed {
		t.Fatal("another scope retried task")
	}
	changed, err = q.ResolveDead(c, "server-a", task.ID, true, now)
	must(t, err)
	if !changed {
		t.Fatal("operator retry did not find task")
	}
	lease = claim("worker-b", 1)
	if lease.Failures != 0 || lease.Attempt != 4 {
		t.Fatalf("manual retry: %+v", lease)
	}
	// A handler may schedule its successor under the same ID before returning.
	task.Name = "successor"
	must(t, q.Schedule(c, task))
	successor := claim("worker-b", 1)
	must(t, q.Acknowledge(c, lease))
	must(t, q.Release(c, lease, time.Time{}, "stale"))
	if successor.Attempt != 1 || successor.Name != "successor" {
		t.Fatalf("replacement: %+v", successor)
	}
	now = now.Add(time.Minute)
	latest := claim("worker-a", 1)
	must(t, q.Acknowledge(c, latest))
	claim("worker-a", 0)
}
func testTaskConcurrentClaim(t *testing.T, b Backend) {
	store, clock := open(t, b)
	q, c := store.Tasks(), ctx(t)
	now := clock.Now()
	must(t, q.Schedule(c, state.Task{ID: "id", Scope: "scope", RunAt: now}))
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range cap(results) {
		wg.Go(func() {
			claims, err := q.ClaimDue(c, now, now.Add(time.Minute), "worker", 1)
			if err != nil {
				t.Error(err)
				return
			}
			results <- len(claims)
		})
	}
	wg.Wait()
	close(results)
	var claimed int
	for n := range results {
		claimed += n
	}
	if claimed != 1 {
		t.Fatalf("concurrent claims: got %d, want 1", claimed)
	}
}
