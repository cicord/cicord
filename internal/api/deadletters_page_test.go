package api

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// A listing that leaves dead letters out says so, with the server's total.
func TestDeadLetterListReportsTruncation(t *testing.T) {
	e := start(t)
	q := e.rt.Store().Tasks()
	at := time.Now().Add(time.Hour)
	for i := range 5 {
		for _, server := range []string{serverKey, "another-server"} {
			task := state.Task{ID: fmt.Sprintf("dead-%s-%d", server, i), Name: "expire", Scope: "plugin:moderator/welcome/" + server,
				RunAt: at.Add(time.Duration(i) * time.Second), Payload: json.RawMessage(`{}`)}
			if err := q.Schedule(t.Context(), task); err != nil {
				t.Fatal(err)
			}
		}
	}
	leases, err := q.ClaimDue(t.Context(), at.Add(time.Minute), at.Add(time.Hour), "test", 100, exampleTaskScopes...)
	if err != nil || len(leases) != 10 {
		t.Fatalf("leases: %d %v", len(leases), err)
	}
	for _, l := range leases {
		if err := q.Release(t.Context(), l, time.Time{}, "handler failed"); err != nil {
			t.Fatal(err)
		}
	}
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	base := "/api/v1/servers/" + serverKey + "/dead-letters"
	var out struct {
		Items     []state.DeadTask
		Total     int
		Truncated bool
	}
	admin.expect(200, "GET", base+"?limit=3", nil).json(t, &out)
	if len(out.Items) != 3 || !out.Truncated || out.Total != 5 || out.Items[0].ID != "dead-"+serverKey+"-0" {
		t.Fatalf("page: %+v", out)
	}
	out = struct {
		Items     []state.DeadTask
		Total     int
		Truncated bool
	}{}
	admin.expect(200, "GET", base, nil).json(t, &out)
	if len(out.Items) != 5 || out.Truncated || out.Total != 5 {
		t.Fatalf("full list: %+v", out)
	}
	for _, bad := range []string{"0", "-1", "501", "x"} {
		admin.expect(400, "GET", base+"?limit="+bad, nil)
	}
}

// Admins page through dead letters with nextCursor (null on the last page). A letter
// discarded while paging does not shift the remaining pages; a bad cursor is a 400.
func TestDeadLetterCursorPaging(t *testing.T) {
	e := start(t)
	q := e.rt.Store().Tasks()
	at := time.Now().Add(time.Hour)
	for i := range 5 {
		task := state.Task{ID: fmt.Sprintf("dead-%d", i), Name: "expire", Scope: "plugin:moderator/welcome/" + serverKey,
			RunAt: at.Add(time.Duration(i/2) * time.Second), Payload: json.RawMessage(`{}`)} // ties on run_at
		if err := q.Schedule(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}
	leases, err := q.ClaimDue(t.Context(), at.Add(time.Minute), at.Add(time.Hour), "test", 100, exampleTaskScopes...)
	if err != nil || len(leases) != 5 {
		t.Fatalf("leases: %d %v", len(leases), err)
	}
	for _, l := range leases {
		if err := q.Release(t.Context(), l, time.Time{}, "handler failed"); err != nil {
			t.Fatal(err)
		}
	}
	admin := e.client()
	admin.login(e.seed.Owner.ID)
	base := "/api/v1/servers/" + serverKey + "/dead-letters?limit=2"
	type page struct {
		Items      []state.DeadTask
		Truncated  bool
		NextCursor *string
	}
	var seen []string
	var p page
	admin.expect(200, "GET", base, nil).json(t, &p)
	for _, it := range p.Items {
		seen = append(seen, it.ID)
	}
	if p.NextCursor == nil || !p.Truncated {
		t.Fatalf("first page: %+v", p)
	}
	admin.expect(204, "POST", "/api/v1/servers/"+serverKey+"/dead-letters/dead-3/discard", nil)
	for p.NextCursor != nil {
		next := *p.NextCursor
		p = page{}
		admin.expect(200, "GET", base+"&cursor="+next, nil).json(t, &p)
		for _, it := range p.Items {
			seen = append(seen, it.ID)
		}
	}
	if fmt.Sprint(seen) != "[dead-0 dead-1 dead-2 dead-4]" || p.Truncated {
		t.Fatalf("paged %v (last %+v)", seen, p)
	}
	var raw map[string]any
	admin.expect(200, "GET", "/api/v1/servers/"+serverKey+"/dead-letters", nil).json(t, &raw)
	if v, ok := raw["nextCursor"]; !ok || v != nil {
		t.Fatalf("last page nextCursor = %#v (present %v)", v, ok)
	}
	for _, bad := range []string{"x!", "e30", "bm90LWpzb24"} {
		admin.expect(400, "GET", base+"&cursor="+bad, nil)
	}
}
