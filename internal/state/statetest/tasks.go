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

func testPausedTaskScopes(t *testing.T, b Backend) {
	store, c := open(t, b)
	q := store.Tasks()
	ctx := t.Context()
	now := c.Now()
	for _, scope := range []string{"plugin:one/x/space", "plugin:two/x/space"} {
		must(t, q.Schedule(ctx, state.Task{ID: scope + "/job", Scope: scope, Name: "job", RunAt: now}))
	}
	now = now.Add(96 * time.Hour)
	jobs, err := q.ClaimDue(ctx, now, now.Add(time.Minute), "worker", 10, "plugin:one/x/space")
	must(t, err)
	if len(jobs) != 1 || jobs[0].Scope != "plugin:two/x/space" {
		t.Fatal(jobs)
	}
	must(t, q.Acknowledge(ctx, jobs[0]))
	for range 3 {
		now = now.Add(time.Hour)
		jobs, err = q.ClaimDue(ctx, now, now.Add(time.Minute), "worker", 10, "plugin:one/x/space")
		must(t, err)
		if len(jobs) != 0 {
			t.Fatal(jobs)
		}
	}
	jobs, err = q.ClaimDue(ctx, now, now.Add(time.Minute), "worker", 10)
	must(t, err)
	if len(jobs) != 1 || jobs[0].Attempt != 1 || jobs[0].Failures != 0 {
		t.Fatal(jobs)
	}
}

// Dead letters page by a (run_at, id) keyset: ties on run_at are ordered by id, no page
// repeats or skips a dead letter that stays parked, and letters parked or resolved while
// paging do not shift the remaining pages.
func testDeadLetterPaging(t *testing.T, b Backend) {
	store, clock := open(t, b)
	q, c := store.Tasks(), ctx(t)
	base := clock.Now()
	park := func(id string, at time.Time) {
		t.Helper()
		must(t, q.Schedule(c, state.Task{ID: id, Scope: "plugin:bot/inst/server-a", Name: "n", RunAt: at}))
		leases, err := q.ClaimDue(c, at, at.Add(time.Minute), "w", 100)
		must(t, err)
		for _, l := range leases {
			if l.ID == id {
				must(t, q.Release(c, l, time.Time{}, "failed"))
				return
			}
		}
		t.Fatalf("task %s was not claimed", id)
	}
	// Byte order of ids: "B" < "a" < "b" (a collation-aware sort would differ).
	for _, p := range []struct {
		id  string
		off time.Duration
	}{{"b", 0}, {"a", 0}, {"B", 0}, {"c", time.Second}, {"d", 2 * time.Second}, {"e", 3 * time.Second}} {
		park(p.id, base.Add(p.off))
	}
	must(t, q.Schedule(c, state.Task{ID: "other", Scope: "plugin:bot/inst/server-b", Name: "n", RunAt: base}))

	var seen []string
	var cursor state.DeadCursor
	page := func() []state.DeadTask {
		t.Helper()
		items, err := q.DeadLettersAfter(c, "server-a", cursor, 2)
		must(t, err)
		for _, it := range items {
			seen = append(seen, it.ID)
		}
		if len(items) > 0 {
			last := items[len(items)-1]
			cursor = state.DeadCursor{RunAt: last.RunAt, ID: last.ID}
		}
		return items
	}
	if got := page(); len(got) != 2 {
		t.Fatalf("first page: %+v", got)
	}
	// Meanwhile: a letter parked before the cursor (seen on a later full listing, not on
	// this pass), one after it (seen), and an unseen one resolved (not seen).
	park("0early", base.Add(-time.Second))
	park("f", base.Add(4*time.Second))
	changed, err := q.ResolveDead(c, "server-a", "d", false, clock.Now())
	must(t, err)
	if !changed {
		t.Fatal("discard did not find the dead letter")
	}
	for len(page()) == 2 {
	}
	want := []string{"B", "a", "b", "c", "e", "f"}
	if len(seen) != len(want) {
		t.Fatalf("paged %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("paged %v, want %v", seen, want)
		}
	}
	all, err := q.DeadLetters(c, "server-a", 100)
	must(t, err)
	if len(all) != 7 || all[0].ID != "0early" {
		t.Fatalf("full listing: %+v", all)
	}
}
