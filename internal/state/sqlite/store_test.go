package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/state"
)

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("Open(\"\") succeeded")
	}
}

func TestMemoryStoresAreIsolated(t *testing.T) {
	a := mustOpen(t, ":memory:")
	b := mustOpen(t, ":memory:")
	must(t, a.KV("s").Set(ctx(t), "k", json.RawMessage(`1`), 0))
	if _, err := b.KV("s").Get(ctx(t), "k"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second in-memory store sees first store's data: err=%v", err)
	}
}

func TestFileStoreUsesWAL(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "state.db"))
	for _, db := range []struct {
		name string
		q    func() (string, error)
	}{
		{"writer", func() (m string, err error) { err = s.w.QueryRow("PRAGMA journal_mode").Scan(&m); return }},
		{"reader", func() (m string, err error) { err = s.r.QueryRow("PRAGMA journal_mode").Scan(&m); return }},
	} {
		mode, err := db.q()
		must(t, err)
		if mode != "wal" {
			t.Errorf("%s journal_mode = %q, want wal", db.name, mode)
		}
	}
	var fk, busy int
	must(t, s.r.QueryRow("PRAGMA foreign_keys").Scan(&fk))
	must(t, s.r.QueryRow("PRAGMA busy_timeout").Scan(&busy))
	if fk != 1 || busy != int(defaultBusyTimeout.Milliseconds()) {
		t.Errorf("reader pragmas: foreign_keys=%d busy_timeout=%d", fk, busy)
	}
	// The read pool must refuse writes.
	if _, err := s.r.Exec(`DELETE FROM kv`); err == nil {
		t.Error("write through read pool succeeded")
	}
}

func TestPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "we?ird #dir%20")
	must(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "state.db")
	s := mustOpen(t, path)
	must(t, s.KV("x").Set(ctx(t), "k", json.RawMessage(`true`), 0))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not created at %q: %v", path, err)
	}
}

func TestMigrationsIdempotentAndPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	ms, err := loadMigrations()
	must(t, err)
	if len(ms) == 0 {
		t.Fatal("no embedded migrations")
	}
	latest := ms[len(ms)-1].version

	when := time.Date(2025, 12, 31, 23, 59, 59, 123456789, time.UTC)
	s1, err := Open(path)
	must(t, err)
	must(t, s1.Resources().Put(ctx(t), state.Resource{Server: "main", Kind: state.KindRole, Key: "mod", ID: "42", Owner: state.OwnerGit, UpdatedAt: when}))
	must(t, s1.KV("p").Set(ctx(t), "k", json.RawMessage(`{"a":1}`), 0))
	id, err := s1.Audit().Append(ctx(t), state.AuditEntry{Server: "main", Actor: "cicord", Action: "sync.apply"})
	must(t, err)
	must(t, s1.Close())

	for i := range 3 {
		s, err := Open(path)
		must(t, err)
		v, err := s.SchemaVersion(ctx(t))
		must(t, err)
		if v != latest {
			t.Errorf("reopen %d: schema version %d, want %d", i, v, latest)
		}
		var n int
		must(t, s.r.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n))
		if n != len(ms) {
			t.Errorf("reopen %d: %d migration rows, want %d", i, n, len(ms))
		}
		r, err := s.Resources().Get(ctx(t), "main", state.KindRole, "mod")
		must(t, err)
		if r.ID != "42" {
			t.Errorf("reopen %d: resource ID %q", i, r.ID)
		}
		assertTime(t, "UpdatedAt after reopen", r.UpdatedAt, when)
		val, err := s.KV("p").Get(ctx(t), "k")
		must(t, err)
		if string(val) != `{"a":1}` {
			t.Errorf("reopen %d: kv value %s", i, val)
		}
		entries, err := s.Audit().List(ctx(t), state.AuditFilter{})
		must(t, err)
		if len(entries) != 1 || entries[0].ID != id {
			t.Errorf("reopen %d: audit entries %+v", i, entries)
		}
		must(t, s.Close())
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	must(t, err)
	_, err = s.w.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, 'future', 0)`)
	must(t, err)
	must(t, s.Close())
	s, err = Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded on a database with a newer schema")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCanceledContext(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, _ *fakeClock) {
		cctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := s.KV("s").Set(cctx, "k", json.RawMessage(`1`), 0); err == nil {
			t.Error("Set with canceled context succeeded")
		}
		if _, err := s.KV("s").Increment(cctx, "k", 1); err == nil {
			t.Error("Increment with canceled context succeeded")
		}
		if _, err := s.Resources().List(cctx, "main"); err == nil {
			t.Error("List with canceled context succeeded")
		}
	})
}

func TestTimeRoundTripExact(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, _ *fakeClock) {
		paris, err := time.LoadLocation("Europe/Paris")
		if err != nil {
			paris = time.FixedZone("CET", 3600)
		}
		// Sub-second precision, a non-UTC location and a monotonic reading.
		times := []time.Time{
			time.Date(2026, 7, 1, 12, 0, 0, 1, paris),
			time.Date(1999, 12, 31, 23, 59, 59, 999999999, time.UTC),
			time.Now(),
		}
		for i, when := range times {
			key := string(rune('a' + i))
			must(t, s.Resources().Put(ctx(t), state.Resource{Server: "srv", Kind: state.KindChannel, Key: key, ID: key, UpdatedAt: when}))
			r, err := s.Resources().Get(ctx(t), "srv", state.KindChannel, key)
			must(t, err)
			assertTime(t, "Resource.UpdatedAt", r.UpdatedAt, when)

			id, err := s.Audit().Append(ctx(t), state.AuditEntry{Time: when, Action: key})
			must(t, err)
			es, err := s.Audit().List(ctx(t), state.AuditFilter{Limit: 1})
			must(t, err)
			if es[0].ID != id {
				t.Fatalf("audit newest id %d, want %d", es[0].ID, id)
			}
			assertTime(t, "AuditEntry.Time", es[0].Time, when)

			_, err = s.Syncs().Record(ctx(t), state.SyncRecord{Time: when, Target: "revision", Status: "succeeded"})
			must(t, err)
			rs, err := s.Syncs().List(ctx(t), "", 1)
			must(t, err)
			assertTime(t, "SyncRecord.Time", rs[0].Time, when)

			must(t, s.Tasks().Schedule(ctx(t), state.Task{ID: "t" + key, Scope: "sc", RunAt: when}))
			ts, err := s.Tasks().Due(ctx(t), when, 1000)
			must(t, err)
			var found bool
			for _, tk := range ts {
				if tk.ID == "t"+key {
					found = true
					assertTime(t, "Task.RunAt", tk.RunAt, when)
				}
			}
			if !found {
				t.Errorf("task %q not due at its own RunAt", "t"+key)
			}
		}
	})
}

func TestOutOfRangeTimesRejected(t *testing.T) {
	s := mustOpen(t, ":memory:")
	far := time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Resources().Put(ctx(t), state.Resource{Server: "s", Kind: state.KindRole, Key: "k", ID: "1", UpdatedAt: far}); err == nil {
		t.Error("Put with year-3000 UpdatedAt succeeded")
	}
	if err := s.Tasks().Schedule(ctx(t), state.Task{ID: "x", Scope: "s", RunAt: far}); err == nil {
		t.Error("Schedule with year-3000 RunAt succeeded")
	}
}

func TestDefaultTimestampsUseClock(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		must(t, s.Resources().Put(ctx(t), state.Resource{Server: "s", Kind: state.KindRole, Key: "k", ID: "1"}))
		r, err := s.Resources().Get(ctx(t), "s", state.KindRole, "k")
		must(t, err)
		assertTime(t, "default UpdatedAt", r.UpdatedAt, clk.Now())

		_, err = s.Audit().Append(ctx(t), state.AuditEntry{Action: "x"})
		must(t, err)
		es, err := s.Audit().List(ctx(t), state.AuditFilter{})
		must(t, err)
		assertTime(t, "default audit Time", es[0].Time, clk.Now())

		_, err = s.Syncs().Record(ctx(t), state.SyncRecord{Target: "revision"})
		must(t, err)
		rs, err := s.Syncs().List(ctx(t), "", 0)
		must(t, err)
		assertTime(t, "default sync Time", rs[0].Time, clk.Now())
	})
}

// An in-memory database lives in its single connection; a context canceled in
// the middle of a transaction must not make database/sql discard it.
func TestMemorySurvivesCanceledTransaction(t *testing.T) {
	s := mustOpen(t, ":memory:")
	must(t, s.KV("s").Set(ctx(t), "k", json.RawMessage(`1`), 0))

	cctx, cancel := context.WithCancel(t.Context())
	tx, err := s.w.BeginTx(cctx, nil)
	must(t, err)
	if _, err := tx.ExecContext(cctx, `INSERT INTO kv (scope, key, value) VALUES ('s', 'tx', '2')`); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := tx.Commit(); err == nil {
		t.Fatal("Commit after cancel succeeded")
	}

	v, err := s.KV("s").Get(ctx(t), "k")
	if err != nil || string(v) != "1" {
		t.Fatalf("after canceled tx: Get = %s, %v (in-memory connection discarded?)", v, err)
	}
	if _, err := s.KV("s").Get(ctx(t), "tx"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("canceled transaction was committed: err=%v", err)
	}
	if _, err := s.KV("s").Increment(ctx(t), "n", 1); err != nil {
		t.Errorf("Increment after canceled tx: %v", err)
	}
}
