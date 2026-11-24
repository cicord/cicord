package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/state"
)

func TestTasks(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		c := ctx(t)
		q := s.Tasks()
		now := clk.Now()

		must(t, q.Schedule(c, state.Task{ID: "late", Scope: "a", Name: "remind", Payload: json.RawMessage(`{"user":"1"}`), RunAt: now.Add(time.Hour)}))
		must(t, q.Schedule(c, state.Task{ID: "t2", Scope: "a", Name: "x", RunAt: now.Add(-time.Minute)}))
		must(t, q.Schedule(c, state.Task{ID: "t1", Scope: "b", Name: "y", RunAt: now.Add(-time.Hour)}))
		must(t, q.Schedule(c, state.Task{ID: "t3", Scope: "b", Name: "z", RunAt: now})) // due exactly now
		must(t, q.Schedule(c, state.Task{ID: "t0", Scope: "b", Name: "tie", RunAt: now}))

		due, err := q.Due(c, now, 0)
		must(t, err)
		if got := taskIDs(due); fmt.Sprint(got) != "[t1 t2 t0 t3]" {
			t.Fatalf("Due = %v, want [t1 t2 t0 t3] (oldest first, ties by id)", got)
		}
		if due[0].Payload != nil {
			t.Errorf("nil payload came back as %q", due[0].Payload)
		}
		due, err = q.Due(c, now, 2)
		must(t, err)
		if got := taskIDs(due); fmt.Sprint(got) != "[t1 t2]" {
			t.Errorf("Due limit 2 = %v", got)
		}

		// Upsert by ID within the same scope.
		must(t, q.Schedule(c, state.Task{ID: "late", Scope: "a", Name: "remind2", Payload: json.RawMessage(`[1]`), RunAt: now.Add(-2 * time.Hour)}))
		due, err = q.Due(c, now, 10)
		must(t, err)
		if len(due) != 5 || due[0].ID != "late" || due[0].Name != "remind2" || string(due[0].Payload) != "[1]" || due[0].Scope != "a" {
			t.Fatalf("after upsert Due[0] = %+v (len %d)", due[0], len(due))
		}
		assertTime(t, "upserted RunAt", due[0].RunAt, now.Add(-2*time.Hour))

		// Another scope cannot take over an existing ID.
		err = q.Schedule(c, state.Task{ID: "late", Scope: "evil", Name: "hijack", RunAt: now})
		if !errors.Is(err, ErrTaskIDConflict) {
			t.Errorf("cross-scope Schedule: err=%v, want ErrTaskIDConflict", err)
		}

		// Cancel is scoped.
		must(t, q.Cancel(c, "a", "t1")) // wrong scope: no-op
		must(t, q.Cancel(c, "b", "t1"))
		must(t, q.Cancel(c, "b", "t1")) // idempotent
		must(t, q.Complete(c, "t2"))
		must(t, q.Complete(c, "missing"))
		due, err = q.Due(c, now, 10)
		must(t, err)
		if got := taskIDs(due); fmt.Sprint(got) != "[late t0 t3]" {
			t.Errorf("after Cancel/Complete Due = %v", got)
		}

		// Nothing due far in the past.
		due, err = q.Due(c, now.Add(-24*time.Hour), 10)
		must(t, err)
		if len(due) != 0 {
			t.Errorf("Due in the past = %v", taskIDs(due))
		}
	})
}

func TestTaskValidation(t *testing.T) {
	s := mustOpen(t, ":memory:")
	c := ctx(t)
	now := time.Now()
	for _, tk := range []state.Task{
		{Scope: "s", RunAt: now},
		{ID: "x", RunAt: now},
		{ID: "x", Scope: "s"},
		{ID: "x", Scope: "s", RunAt: now, Payload: json.RawMessage(`{bad`)},
	} {
		if err := s.Tasks().Schedule(c, tk); err == nil {
			t.Errorf("Schedule(%+v) succeeded", tk)
		}
	}
}

func taskIDs(ts []state.Task) []string {
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	return ids
}
