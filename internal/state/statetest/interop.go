package statetest

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

func testInterop(t *testing.T, b Backend) {
	s, clock := open(t, b)
	ctx := t.Context()
	now := clock.Now()
	db := s.Interop()
	q := s.Tasks()
	a := state.EventFanout{ID: "first", Server: "server", Publisher: "polls", Topic: "polls.closed", At: now, Payload: json.RawMessage(`{"voters":["111111111111111111"]}`), Subscribers: []string{"plugin:bot/levels/server", "plugin:bot/audit/server"}}
	must(t, db.PublishEvent(ctx, a))
	must(t, db.PublishEvent(ctx, a))
	a.ID = "second"
	must(t, db.PublishEvent(ctx, a))
	take := func(now time.Time) []state.TaskLease {
		v, err := q.ClaimDue(ctx, now, now.Add(time.Minute), "worker", 10)
		must(t, err)
		return v
	}
	first := take(now)
	if len(first) != 2 || first[0].Scope == first[1].Scope {
		t.Fatal(first)
	}
	if len(take(now)) != 0 {
		t.Fatal("later event overtook lease")
	}
	recovered := take(now.Add(time.Minute))
	if len(recovered) != 2 {
		t.Fatal(recovered)
	}
	used, err := q.Consume(ctx, first[0])
	must(t, err)
	if used {
		t.Fatal("stale worker consumed delivery")
	}
	for _, item := range recovered {
		if !item.Event || item.Attempt != 2 {
			t.Fatal(item)
		}
		must(t, q.Acknowledge(ctx, item))
	}
	for attempt := 0; attempt < 5; attempt++ {
		items := take(now.Add(time.Duration(attempt+3) * time.Minute))
		if len(items) != 2 {
			t.Fatalf("attempt %d: %v", attempt, items)
		}
		for _, item := range items {
			retry := now
			if attempt == 4 {
				retry = time.Time{}
			}
			must(t, q.Release(ctx, item, retry, "failure"))
		}
	}
	dead, err := q.DeadLetters(ctx, "server", 10)
	must(t, err)
	if len(dead) != 2 || dead[0].Failures != 5 {
		t.Fatal(dead)
	}
	ok, err := q.ResolveDead(ctx, "server", dead[0].ID, true, now)
	must(t, err)
	if !ok || len(take(now)) != 1 {
		t.Fatal("manual event recovery failed")
	}
	// Running reservations survive losing a worker. A timed-out former owner
	// cannot commit even one of its buffered writes, or claim another caller's result.
	reserve := func(at time.Time, owner string) (json.RawMessage, bool) {
		out, yes, e := db.ReserveAction(ctx, "action", "server", "request", owner, at)
		must(t, e)
		return out, yes
	}
	if _, ok := reserve(now, "one"); !ok {
		t.Fatal("initial reservation")
	}
	if _, ok := reserve(now, "two"); ok {
		t.Fatal("duplicate owner")
	}
	if _, ok := reserve(now.Add(time.Minute), "two"); !ok {
		t.Fatal("abandoned action did not recover")
	}
	writes := []state.KVWrite{{Key: "balance", Value: json.RawMessage("50"), Changed: true}}
	if err := db.CommitAction(ctx, "action", "one", "scope", writes, json.RawMessage("50")); err == nil {
		t.Fatal("stale owner committed")
	}
	if _, err := s.KV("scope").Get(ctx, "balance"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("partial commit", err)
	}
	must(t, db.CommitAction(ctx, "action", "two", "scope", writes, json.RawMessage("50")))
	if cached, ok := reserve(now.Add(7*24*time.Hour), "three"); ok || string(cached) != "50" {
		t.Fatalf("late replay: %s %v", cached, ok)
	}
	if _, _, err := db.ReserveAction(ctx, "action", "server", "other", "four", now); err == nil {
		t.Fatal("cache leaked to another request")
	}
}

func testInteropPrivacy(t *testing.T, b Backend) {
	s, c := open(t, b)
	ctx := t.Context()
	j := s.Interop()
	q := s.Tasks()
	now := c.Now()
	const user = "111111111111111111"
	const other = "222222222222222222"
	e := state.EventFanout{ID: "shared", Publisher: "a", Topic: "polls.closed", At: now, Subscribers: []string{"plugin:bot/levels/one"}, Payload: json.RawMessage(`{"meta":{"id":"stable"},"payload":{"voters":["111111111111111111","222222222222222222"]}}`)}
	must(t, j.PublishEvent(ctx, e))
	e.ID = "distinct"
	e.Subscribers = []string{"plugin:bot/levels/two"}
	must(t, j.PublishEvent(ctx, e))
	_, _, err := j.ReserveAction(ctx, "receipt", "one", "request", "worker", now)
	must(t, err)
	must(t, j.CommitAction(ctx, "receipt", "worker", "plugin:bot/levels/one", nil, json.RawMessage(`{"userId":"111111111111111111"}`)))
	n, err := j.PurgeInteropMember(ctx, "one", user)
	must(t, err)
	if n != 2 {
		t.Fatal(n)
	}
	if _, ok, err := j.ReserveAction(ctx, "receipt", "one", "request", "retry", now.Add(time.Hour)); !errors.Is(err, state.ErrErasedResult) || ok {
		t.Fatal("erased action reran", ok, err)
	}
	items, err := q.ClaimDue(ctx, now, now.Add(time.Minute), "worker", 10)
	must(t, err)
	if len(items) != 2 {
		t.Fatal(items)
	}
	for _, item := range items {
		if !strings.Contains(string(item.Payload), other) || (strings.HasSuffix(item.Scope, "/one") == strings.Contains(string(item.Payload), user)) {
			t.Fatal(item)
		}
		must(t, q.Release(ctx, item, time.Time{}, "handler failed"))
		otherServer := "one"
		if strings.HasSuffix(item.Scope, "/one") {
			otherServer = "two"
		}
		if data, err := j.DeadEvent(ctx, otherServer, item.ID); err != nil || data != nil {
			t.Fatal("cross-server payload", data, err)
		}
	}
	c.Advance(15 * 24 * time.Hour)
	_, err = s.Sweep(ctx)
	must(t, err)
	dead, err := q.DeadLetters(ctx, "one", 20)
	must(t, err)
	if len(dead) != 0 {
		t.Fatal(dead)
	}
}
