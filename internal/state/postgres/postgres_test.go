package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
	"github.com/cicord/cicord/internal/state/statetest"
)

// testURLEnv names the database the tests run against, e.g.
//
//	CICORD_TEST_POSTGRES_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable'
//
// Every test works in its own schema (search_path), dropped afterwards. The
// tests that need a server are skipped when the variable is not set.
const testURLEnv = "CICORD_TEST_POSTGRES_URL"

func testURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv(testURLEnv)
	if u == "" {
		t.Skipf("%s is not set: no PostgreSQL server to test against", testURLEnv)
	}
	return u
}

// newSchema creates an empty schema for t and returns a connection string
// whose search_path is that schema.
func newSchema(t *testing.T) string {
	t.Helper()
	base := testURL(t)
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	name := "cicord_test_" + hex.EncodeToString(b)
	ident := pgx.Identifier{name}.Sanitize()
	admin := func(sql string) {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(c, base)
		if err != nil {
			t.Fatalf("connect to %s: %v", testURLEnv, err)
		}
		defer conn.Close(c)
		if _, err := conn.Exec(c, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	admin("CREATE SCHEMA " + ident)
	t.Cleanup(func() { admin("DROP SCHEMA " + ident + " CASCADE") })
	return withParam(t, base, "search_path", name)
}

// withParam adds a connection parameter to a URL or key=value connection string.
func withParam(t *testing.T, conn, key, value string) string {
	t.Helper()
	if strings.HasPrefix(conn, "postgres://") || strings.HasPrefix(conn, "postgresql://") {
		u, err := url.Parse(conn)
		if err != nil {
			t.Fatalf("%s is not a valid URL", testURLEnv)
		}
		q := u.Query()
		q.Set(key, value)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return conn + " " + key + "=" + value
}

func mustOpen(t *testing.T, conn string, opts ...Option) *Store {
	t.Helper()
	s, err := Open(t.Context(), conn, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestConformance(t *testing.T) {
	testURL(t)
	statetest.Run(t, statetest.Backend{
		NewDatabase: func(t *testing.T) statetest.Opener {
			conn := newSchema(t)
			return func(t *testing.T, now func() time.Time) state.Store {
				return mustOpen(t, conn, WithClock(now))
			}
		},
		SharedHandles: true,
	})
}

func TestMigrationsIdempotent(t *testing.T) {
	conn := newSchema(t)
	ms, err := storeutil.LoadMigrations(migrationFS, "migrations")
	must(t, err)
	if len(ms) == 0 {
		t.Fatal("no embedded migrations")
	}
	for i := range 3 {
		s := mustOpen(t, conn)
		v, err := s.SchemaVersion(t.Context())
		must(t, err)
		if v != storeutil.Latest(ms) {
			t.Errorf("open %d: schema version %d, want %d", i, v, storeutil.Latest(ms))
		}
		var n int
		must(t, s.pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&n))
		if n != len(ms) {
			t.Errorf("open %d: %d migration rows, want %d", i, n, len(ms))
		}
		must(t, s.Close())
	}
}

// Several processes starting at once against an empty database: the advisory
// lock makes exactly one of them apply each migration.
func TestConcurrentOpen(t *testing.T) {
	conn := newSchema(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Go(func() {
			s, err := Open(t.Context(), conn)
			if err != nil {
				errs <- err
				return
			}
			_ = s.Close()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	s := mustOpen(t, conn)
	var rows int
	must(t, s.pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&rows))
	ms, _ := storeutil.LoadMigrations(migrationFS, "migrations")
	if rows != len(ms) {
		t.Errorf("%d migration rows after concurrent opens, want %d", rows, len(ms))
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	conn := newSchema(t)
	s := mustOpen(t, conn)
	_, err := s.pool.Exec(t.Context(), `INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, 'future', 0)`)
	must(t, err)
	must(t, s.Close())
	s2, err := Open(t.Context(), conn)
	if err == nil {
		_ = s2.Close()
		t.Fatal("Open succeeded on a database with a newer schema")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Text columns hold UTF-8 without NUL bytes on PostgreSQL: such values are
// rejected with an error, never stored altered.
func TestTextColumnsRejectInvalidText(t *testing.T) {
	s := mustOpen(t, newSchema(t))
	if err := s.Resources().Put(t.Context(), state.Resource{Server: "s", Kind: state.KindRole, Key: "a\x00b", ID: "1"}); err == nil {
		t.Error("Put with a NUL byte in the key succeeded")
	}
	if _, err := s.Resources().Get(t.Context(), "s", state.KindRole, "a"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a truncated key was stored: err=%v", err)
	}
}

// These need no server: the connection fails before or at the first packet.
func TestErrorsNeverContainThePassword(t *testing.T) {
	const pw = "s3cr3t-Pa55word"
	for _, conn := range []string{
		"postgres://cicord:" + pw + "@127.0.0.1:1/cicord?connect_timeout=2",
		"postgresql://cicord:" + pw + "@127.0.0.1:1/cicord?sslmode=disable&connect_timeout=2",
		"host=127.0.0.1 port=1 user=cicord password=" + pw + " dbname=cicord connect_timeout=2",
		"postgres://cicord:" + pw + "@127.0.0.1:notaport/cicord",
		"postgres://cicord:" + pw + "@[::1/cicord",
		"host=127.0.0.1 password='" + pw + " dbname=cicord",
		"postgres://cicord:" + pw + "@127.0.0.1:1/cicord?pool_max_conns=-1",
	} {
		s, err := Open(t.Context(), conn)
		if err == nil {
			_ = s.Close()
			t.Errorf("Open(%q) succeeded", strings.ReplaceAll(conn, pw, "…"))
			continue
		}
		if strings.Contains(err.Error(), pw) {
			t.Errorf("error leaks the password: %v", err)
		}
	}
	if _, err := Open(t.Context(), "postgres://u:"+pw+"@h:x/d"); !errors.Is(err, ErrInvalidURL) {
		t.Errorf("unparsable URL: err=%v, want ErrInvalidURL", err)
	}
	if _, err := Open(t.Context(), ""); err == nil {
		t.Error("Open(\"\") succeeded")
	}
}

func TestDescribe(t *testing.T) {
	conn := newSchema(t)
	s := mustOpen(t, conn)
	d := s.Describe()
	if !strings.HasPrefix(d, "postgres://") {
		t.Errorf("Describe() = %q", d)
	}
	if pw := passwordOf(conn); pw != "" && strings.Contains(d, pw) {
		t.Error("Describe() contains the password")
	}
	var app string
	must(t, s.pool.QueryRow(t.Context(), `SELECT current_setting('application_name')`).Scan(&app))
	if app != "cicord" {
		t.Errorf("application_name = %q, want cicord", app)
	}
}

func passwordOf(conn string) string {
	cfg, err := pgx.ParseConfig(conn)
	if err != nil {
		return ""
	}
	return cfg.Password
}

// TestMigrationAnalyticsLabels: a database at schema version 2 (before the
// remembered analytics labels) is upgraded on Open: analytics_labels is created and
// the points are kept.
func TestMigrationAnalyticsLabels(t *testing.T) {
	conn := newSchema(t)
	s := mustOpen(t, conn)
	must(t, s.Analytics().Record(t.Context(), state.AnalyticsCounter,
		state.AnalyticsPoint{Server: "main", Metric: "voice.seconds", Dim: "1", Bucket: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Value: 60}))
	// Back to version 2.
	_, err := s.pool.Exec(t.Context(), `DROP TABLE analytics_labels; DELETE FROM schema_migrations WHERE version = 3`)
	must(t, err)
	must(t, s.Close())

	s = mustOpen(t, conn)
	if v, err := s.SchemaVersion(t.Context()); err != nil || v < 3 {
		t.Fatalf("schema version %d, %v; want >= 3", v, err)
	}
	ps, err := s.Analytics().Query(t.Context(), state.AnalyticsQuery{Server: "main"})
	must(t, err)
	if len(ps) != 1 || ps[0].Value != 60 {
		t.Errorf("points after the upgrade = %+v", ps)
	}
	l := s.Analytics().(state.AnalyticsLabels)
	must(t, l.PutLabels(t.Context(), state.AnalyticsLabel{Server: "main", Dim: "channel", ID: "1", Name: "Chill room", Type: "voice", Owner: "plugin:main/voice-rooms", Deleted: true}))
	got, err := l.GetLabels(t.Context(), "main", "channel", []string{"1"})
	must(t, err)
	if len(got) != 1 || got[0].Name != "Chill room" || got[0].Owner != "plugin:main/voice-rooms" || !got[0].Deleted {
		t.Errorf("label after the upgrade = %+v", got)
	}
}
