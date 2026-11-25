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

	"github.com/cicord/cicord/internal/state"
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
	latest := ms[len(ms)-1].Version

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

// TestMigrationAnalyticsLabels: a database at schema version 2 (before the
// remembered analytics labels) is upgraded on Open: analytics_labels is created and
// the points are kept.
func TestMigrationAnalyticsLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	must(t, err)
	must(t, s.Analytics().Record(ctx(t), state.AnalyticsCounter,
		state.AnalyticsPoint{Server: "main", Metric: "voice.seconds", Dim: "1", Bucket: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Value: 60}))
	// Back to version 2.
	_, err = s.w.Exec(`DROP TABLE analytics_labels; DELETE FROM schema_migrations WHERE version = 3`)
	must(t, err)
	must(t, s.Close())

	s, err = Open(path)
	must(t, err)
	defer s.Close()
	if v, err := s.SchemaVersion(ctx(t)); err != nil || v < 3 {
		t.Fatalf("schema version %d, %v; want >= 3", v, err)
	}
	ps, err := s.Analytics().Query(ctx(t), state.AnalyticsQuery{Server: "main"})
	must(t, err)
	if len(ps) != 1 || ps[0].Value != 60 {
		t.Errorf("points after the upgrade = %+v", ps)
	}
	l := s.Analytics().(state.AnalyticsLabels)
	must(t, l.PutLabels(ctx(t), state.AnalyticsLabel{Server: "main", Dim: "channel", ID: "1", Name: "Chill room", Type: "voice", Owner: "plugin:main/voice-rooms", Deleted: true}))
	got, err := l.GetLabels(ctx(t), "main", "channel", []string{"1"})
	must(t, err)
	if len(got) != 1 || got[0].Name != "Chill room" || got[0].Owner != "plugin:main/voice-rooms" || !got[0].Deleted {
		t.Errorf("label after the upgrade = %+v", got)
	}
}
