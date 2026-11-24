package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/state"
)

func TestAuditLog(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		c := ctx(t)
		a := s.Audit()

		if es, err := a.List(c, state.AuditFilter{}); err != nil || len(es) != 0 {
			t.Fatalf("empty List = %v, %v", es, err)
		}

		var ids []int64
		for i := range 120 {
			server := "main"
			switch i % 3 {
			case 1:
				server = "other"
			case 2:
				server = "" // project-level
			}
			clk.Advance(time.Millisecond)
			id, err := a.Append(c, state.AuditEntry{
				Server: server, Actor: "discord:1", Action: "member.kick", Target: fmt.Sprint(i),
				Details: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
			})
			must(t, err)
			if len(ids) > 0 && id <= ids[len(ids)-1] {
				t.Fatalf("IDs not increasing: %d after %d", id, ids[len(ids)-1])
			}
			ids = append(ids, id)
		}

		// Default limit 50, newest first, all servers.
		es, err := a.List(c, state.AuditFilter{})
		must(t, err)
		if len(es) != defaultAuditLimit {
			t.Fatalf("default List returned %d, want %d", len(es), defaultAuditLimit)
		}
		if es[0].ID != ids[119] || es[0].Target != "119" || string(es[0].Details) != `{"i":119}` || es[0].Actor != "discord:1" || es[0].Action != "member.kick" {
			t.Errorf("newest entry = %+v", es[0])
		}
		assertTime(t, "newest Time", es[0].Time, clk.Now())
		for i := 1; i < len(es); i++ {
			if es[i].ID >= es[i-1].ID {
				t.Fatalf("not newest first at %d", i)
			}
		}

		// Server filter + Before pagination walks every matching entry exactly once.
		var walked []string
		before := int64(0)
		for {
			page, err := a.List(c, state.AuditFilter{Server: "main", Before: before, Limit: 7})
			must(t, err)
			if len(page) == 0 {
				break
			}
			for _, e := range page {
				if e.Server != "main" {
					t.Fatalf("server filter leaked %+v", e)
				}
				walked = append(walked, e.Target)
			}
			before = page[len(page)-1].ID
		}
		if len(walked) != 40 || walked[0] != "117" || walked[39] != "0" {
			t.Errorf("walked %d main entries: first %v last %v", len(walked), walked[0], walked[len(walked)-1])
		}

		es, err = a.List(c, state.AuditFilter{Before: ids[2], Limit: 10})
		must(t, err)
		if len(es) != 2 || es[0].ID != ids[1] || es[1].ID != ids[0] {
			t.Errorf("Before ids[2] = %+v", es)
		}
		es, err = a.List(c, state.AuditFilter{Limit: 5000})
		must(t, err)
		if len(es) != 120 {
			t.Errorf("large limit returned %d", len(es))
		}
	})
}

func TestAuditDetailsValidation(t *testing.T) {
	s := mustOpen(t, ":memory:")
	if _, err := s.Audit().Append(ctx(t), state.AuditEntry{Details: json.RawMessage(`{nope`)}); err == nil {
		t.Error("Append with invalid details succeeded")
	}
	id, err := s.Audit().Append(ctx(t), state.AuditEntry{Action: "x"})
	must(t, err)
	es, err := s.Audit().List(ctx(t), state.AuditFilter{})
	must(t, err)
	if es[0].ID != id || es[0].Details != nil || es[0].Server != "" {
		t.Errorf("entry = %+v", es[0])
	}
}

func TestSyncLog(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		c := ctx(t)
		l := s.Syncs()
		targets := []string{"server:main", "bot:xp", "revision"}
		for i := range 60 {
			clk.Advance(time.Second)
			_, err := l.Record(c, state.SyncRecord{
				Revision: fmt.Sprintf("sha%02d", i), Target: targets[i%3], Status: "succeeded",
				Message: "ok", Details: json.RawMessage(`{"changes":[]}`),
			})
			must(t, err)
		}
		_, err := l.Record(c, state.SyncRecord{Revision: "bad", Target: "server:main", Status: "failed", Message: "boom"})
		must(t, err)

		all, err := l.List(c, "", 0)
		must(t, err)
		if len(all) != defaultSyncLimit || all[0].Revision != "bad" || all[0].Status != "failed" || all[0].Details != nil {
			t.Fatalf("List all: len %d first %+v", len(all), all[0])
		}
		if all[1].Revision != "sha59" || string(all[1].Details) != `{"changes":[]}` || all[1].Message != "ok" {
			t.Errorf("second record = %+v", all[1])
		}

		main, err := l.List(c, "server:main", 5)
		must(t, err)
		if len(main) != 5 {
			t.Fatalf("List server:main returned %d", len(main))
		}
		for i, r := range main {
			if r.Target != "server:main" {
				t.Errorf("target filter leaked %+v", r)
			}
			if i > 0 && r.ID >= main[i-1].ID {
				t.Errorf("not newest first")
			}
		}
		if main[0].Revision != "bad" || main[1].Revision != "sha57" {
			t.Errorf("server:main newest = %s, %s", main[0].Revision, main[1].Revision)
		}
		none, err := l.List(c, "server:nope", 10)
		must(t, err)
		if len(none) != 0 {
			t.Errorf("unknown target returned %d records", len(none))
		}
	})
}

func TestSessions(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		c := ctx(t)
		ss := s.Sessions()
		exp := clk.Now().Add(time.Hour)

		if _, err := ss.Get(c, "nope"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("Get missing: err=%v", err)
		}
		must(t, ss.Put(c, state.Session{ID: "abc", UserID: "123", Data: json.RawMessage(`{"csrf":"x"}`), ExpiresAt: exp}))
		got, err := ss.Get(c, "abc")
		must(t, err)
		if got.ID != "abc" || got.UserID != "123" || string(got.Data) != `{"csrf":"x"}` {
			t.Errorf("Get = %+v", got)
		}
		assertTime(t, "ExpiresAt", got.ExpiresAt, exp)

		// Upsert.
		exp2 := exp.Add(time.Hour)
		must(t, ss.Put(c, state.Session{ID: "abc", UserID: "456", ExpiresAt: exp2}))
		got, err = ss.Get(c, "abc")
		must(t, err)
		if got.UserID != "456" || got.Data != nil {
			t.Errorf("after upsert = %+v", got)
		}
		assertTime(t, "upserted ExpiresAt", got.ExpiresAt, exp2)

		// Expiry.
		clk.Advance(2*time.Hour - time.Nanosecond)
		if _, err := ss.Get(c, "abc"); err != nil {
			t.Fatalf("session expired early: %v", err)
		}
		clk.Advance(time.Nanosecond)
		if _, err := ss.Get(c, "abc"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("expired Get: err=%v, want ErrNotFound", err)
		}
		var n int
		must(t, s.r.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n))
		if n != 0 {
			t.Errorf("expired session not lazily deleted")
		}

		// Delete.
		must(t, ss.Put(c, state.Session{ID: "d", UserID: "1", ExpiresAt: clk.Now().Add(time.Minute)}))
		must(t, ss.Delete(c, "d"))
		must(t, ss.Delete(c, "d"))
		if _, err := ss.Get(c, "d"); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("Get after Delete: err=%v", err)
		}

		// Validation.
		for _, bad := range []state.Session{
			{UserID: "1", ExpiresAt: exp},
			{ID: "x", UserID: "1"},
			{ID: "x", UserID: "1", ExpiresAt: exp, Data: json.RawMessage(`{`)},
		} {
			if err := ss.Put(c, bad); err == nil {
				t.Errorf("Put(%+v) succeeded", bad)
			}
		}
	})
}
