package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

//go:embed testdata/v0.1.11.sql
var v0111Dump string

const v0111Scope = "plugin:moderator/moderation/my-community"

// execRaw runs SQL in the test schema of conn outside a Store.
func execRaw(t *testing.T, conn, sql string) {
	t.Helper()
	c, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := pgx.Connect(c, conn)
	must(t, err)
	defer db.Close(c)
	_, err = db.Exec(c, sql)
	must(t, err)
}

func recordedMigrations(t *testing.T, s *Store) map[int]int64 {
	t.Helper()
	rows, err := s.pool.Query(t.Context(), `SELECT version, applied_at FROM schema_migrations`)
	must(t, err)
	out := map[int]int64{}
	for rows.Next() {
		var v int
		var at int64
		must(t, rows.Scan(&v, &at))
		out[v] = at
	}
	must(t, rows.Err())
	return out
}

// A populated v0.1.11 database upgrades in place: the schema reaches the latest
// version, overdue legacy tasks become leased deliveries, plugin storage usage is
// counted from existing rows, the enforcement pause survives and a second open
// changes nothing.
func TestUpgradeFromPopulatedV0111(t *testing.T) {
	conn := newSchema(t)
	execRaw(t, conn, v0111Dump)
	ms, err := storeutil.LoadMigrations(migrationFS, "migrations")
	must(t, err)

	s := mustOpen(t, conn)
	v, err := s.SchemaVersion(t.Context())
	must(t, err)
	if v != storeutil.Latest(ms) {
		t.Fatalf("schema version %d after upgrade, want %d", v, storeutil.Latest(ms))
	}
	applied := recordedMigrations(t, s)
	if len(applied) != len(ms) {
		t.Fatalf("%d recorded migrations, want %d", len(applied), len(ms))
	}
	var usage int
	must(t, s.pool.QueryRow(t.Context(), `SELECT entries FROM plugin_storage_usage WHERE scope = $1`, v0111Scope).Scan(&usage))
	if usage != 5 {
		t.Errorf("plugin storage usage %d after upgrade, want the 5 existing keys", usage)
	}
	pause, err := s.KV("enforcement-pause").Get(t.Context(), "my-community")
	must(t, err)
	if string(pause) != `{"by":"x"}` {
		t.Errorf("enforcement pause %s after upgrade", pause)
	}
	r, err := s.Resources().Get(t.Context(), "my-community", state.KindRole, "mod")
	must(t, err)
	if r.ID != "42" {
		t.Errorf("managed role ID %q after upgrade", r.ID)
	}

	now := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	leases, err := s.Tasks().ClaimDue(t.Context(), now, now.Add(time.Minute), "worker", 10)
	must(t, err)
	var ids []string
	for _, l := range leases {
		ids = append(ids, l.ID)
		if l.Attempt != 1 || l.Failures != 0 || l.Event || l.Scope != v0111Scope || string(l.Payload) != `{"userId":"1"}` {
			t.Errorf("legacy task lease %+v", l)
		}
	}
	if strings.Join(ids, ",") != "legacy-0,legacy-1,legacy-2" {
		t.Fatalf("claimed %v, want the three overdue legacy tasks in order", ids)
	}
	must(t, s.Tasks().Acknowledge(t.Context(), leases[0]))
	must(t, s.KV(v0111Scope).Set(t.Context(), "new", json.RawMessage(`2`), 0))
	must(t, s.Close())

	s = mustOpen(t, conn)
	again := recordedMigrations(t, s)
	if len(again) != len(ms) {
		t.Errorf("%d recorded migrations after second open, want %d", len(again), len(ms))
	}
	for ver, at := range applied {
		if again[ver] != at {
			t.Errorf("migration %d re-applied on second open", ver)
		}
	}
	must(t, s.pool.QueryRow(t.Context(), `SELECT entries FROM plugin_storage_usage WHERE scope = $1`, v0111Scope).Scan(&usage))
	if usage != 6 {
		t.Errorf("plugin storage usage %d after a new key, want 6", usage)
	}
	due, err := s.Tasks().Due(t.Context(), time.Date(2200, 1, 2, 0, 0, 0, 0, time.UTC), 10)
	must(t, err)
	if len(due) != 3 {
		t.Errorf("%d tasks after reopen, want two leased legacy tasks and the future one", len(due))
	}
}

// See the SQLite test of the same name: recorded migrations must match the
// embedded files by version and name.
func TestOpenRejectsMismatchedMigrationNames(t *testing.T) {
	for _, tc := range []struct {
		name, sql, want string
	}{
		{"renamed", `UPDATE schema_migrations SET name = '0004_hotfix.sql' WHERE version = 4`, `"0004_hotfix.sql", but this binary's migration 4 is "0004_task_leases.sql"`},
		{"unknown version", `INSERT INTO schema_migrations (version, name, applied_at) VALUES (7, '0007_hotfix.sql', 0)`, `migration 7 ("0007_hotfix.sql"), which this binary does not have`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := newSchema(t)
			s := mustOpen(t, conn)
			_, err := s.pool.Exec(t.Context(), tc.sql)
			must(t, err)
			must(t, s.Close())
			s, err = Open(t.Context(), conn)
			if err == nil {
				_ = s.Close()
				t.Fatal("Open accepted a database migrated by an incompatible build")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// A 0.1.x patch release that shipped its own migration 4 must not make 0.2 skip
// 0004_task_leases.sql: the upgrade fails loudly and the transaction leaves the
// schema as it was.
func TestUpgradeRefusesForeignMigrationNumber(t *testing.T) {
	conn := newSchema(t)
	execRaw(t, conn, v0111Dump+"\nINSERT INTO schema_migrations VALUES (4, '0004_patch.sql', 0);")
	if s, err := Open(t.Context(), conn); err == nil {
		_ = s.Close()
		t.Fatal("Open skipped 0004_task_leases.sql because version 4 was recorded under another name")
	} else if !strings.Contains(err.Error(), "0004_patch.sql") {
		t.Errorf("unexpected error: %v", err)
	}
	c, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := pgx.Connect(c, conn)
	must(t, err)
	defer db.Close(c)
	var n int
	must(t, db.QueryRow(c, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n))
	if n != 4 {
		t.Errorf("refused upgrade recorded %d migrations, want the original 4", n)
	}
}
