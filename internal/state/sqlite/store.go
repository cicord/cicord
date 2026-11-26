// Package sqlite implements state.Store on SQLite using the pure-Go driver
// modernc.org/sqlite, so the cicord binary builds with CGO_ENABLED=0.
//
// Connection model:
//
//   - File databases use WAL mode with two pools: a single-connection writer pool
//     (every write and read-modify-write goes through it, so writers in this
//     process never contend for the SQLite write lock) and a read-only pool of
//     several connections for plain reads. Writer transactions use BEGIN IMMEDIATE
//     so a read-then-write transaction cannot fail on lock upgrade when another
//     process (e.g. the CLI) writes the same file; busy_timeout covers the wait.
//   - ":memory:" uses one connection that is never recycled (an in-memory
//     database lives exactly as long as its connection). All operations are
//     serialized on it.
//
// Times are stored as INTEGER unix nanoseconds in UTC and returned with
// Location() == time.UTC; JSON values are stored as TEXT (NULL when absent).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/advanced"
	"github.com/cicord/cicord/internal/state/internal/interopdb"
	"github.com/cicord/cicord/internal/state/internal/storeutil"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var _ state.Store = (*Store)(nil)

const (
	defaultBusyTimeout = 5 * time.Second
	memoryPath         = ":memory:"
)

// Store is a SQLite-backed state.Store. It is safe for concurrent use.
type Store struct {
	w   *sql.DB // writes; exactly one connection
	r   *sql.DB // reads; same handle as w for in-memory databases
	now func() time.Time
}

// Option configures Open.
type Option func(*options)

type options struct {
	clock       func() time.Time
	busyTimeout time.Duration
	maxReaders  int
}

// WithClock replaces time.Now as the source of "now" for TTL expiry, session
// expiry and default timestamps. Intended for tests.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.clock = now }
}

// WithBusyTimeout sets how long a connection waits for a lock held by another
// connection or process before failing with SQLITE_BUSY (default 5s).
func WithBusyTimeout(d time.Duration) Option {
	return func(o *options) { o.busyTimeout = d }
}

// WithMaxReaders sets the size of the read pool for file databases
// (default max(4, GOMAXPROCS)). Ignored for ":memory:".
func WithMaxReaders(n int) Option {
	return func(o *options) { o.maxReaders = n }
}

// Open opens (creating if needed) the database at path and applies pending
// schema migrations. path ":memory:" opens a private in-memory database.
func Open(path string, opts ...Option) (*Store, error) {
	o := options{
		clock:       time.Now,
		busyTimeout: defaultBusyTimeout,
		maxReaders:  max(4, runtime.GOMAXPROCS(0)),
	}
	for _, opt := range opts {
		opt(&o)
	}
	if path == "" {
		return nil, errors.New("state/sqlite: empty database path")
	}
	if o.clock == nil {
		return nil, errors.New("state/sqlite: nil clock")
	}
	if o.maxReaders < 1 {
		o.maxReaders = 1
	}
	busyMS := strconv.FormatInt(max(o.busyTimeout.Milliseconds(), 0), 10)

	s := &Store{now: o.clock}
	if path == memoryPath {
		q := url.Values{}
		q.Add("_pragma", "busy_timeout("+busyMS+")")
		q.Add("_pragma", "foreign_keys(1)")
		db, err := sql.Open("sqlite", memoryPath+"?"+q.Encode())
		if err != nil {
			return nil, fmt.Errorf("state/sqlite: open: %w", err)
		}
		// The database lives as long as its only connection: never recycle it.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		db.SetConnMaxIdleTime(0)
		s.w, s.r = db, db
	} else {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("state/sqlite: resolve path: %w", err)
		}
		base := "file:" + (&url.URL{Path: filepath.ToSlash(abs)}).EscapedPath()

		wq := url.Values{}
		wq.Add("_pragma", "busy_timeout("+busyMS+")")
		wq.Add("_pragma", "foreign_keys(1)")
		wq.Add("_pragma", "journal_mode(WAL)")
		wq.Add("_pragma", "synchronous(NORMAL)")
		wq.Set("_txlock", "immediate")
		w, err := sql.Open("sqlite", base+"?"+wq.Encode())
		if err != nil {
			return nil, fmt.Errorf("state/sqlite: open: %w", err)
		}
		w.SetMaxOpenConns(1)
		w.SetMaxIdleConns(1)
		w.SetConnMaxLifetime(0)
		w.SetConnMaxIdleTime(0)
		s.w = w

		// Establish WAL mode and the schema through the writer before any reader
		// connection exists.
		if err := w.PingContext(context.Background()); err != nil {
			w.Close()
			return nil, fmt.Errorf("state/sqlite: open %s: %w", path, err)
		}
		var mode string
		if err := w.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
			w.Close()
			return nil, fmt.Errorf("state/sqlite: journal mode: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			w.Close()
			return nil, fmt.Errorf("state/sqlite: could not enable WAL mode (journal_mode=%s)", mode)
		}

		rq := url.Values{}
		rq.Add("_pragma", "busy_timeout("+busyMS+")")
		rq.Add("_pragma", "foreign_keys(1)")
		rq.Set("_query_only", "1")
		r, err := sql.Open("sqlite", base+"?"+rq.Encode())
		if err != nil {
			w.Close()
			return nil, fmt.Errorf("state/sqlite: open: %w", err)
		}
		r.SetMaxOpenConns(o.maxReaders)
		r.SetMaxIdleConns(o.maxReaders)
		s.r = r
	}

	if err := s.migrate(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database. It is safe to call more than once.
func (s *Store) Close() error {
	var errs []error
	if s.r != nil && s.r != s.w {
		errs = append(errs, s.r.Close())
	}
	if s.w != nil {
		errs = append(errs, s.w.Close())
	}
	return errors.Join(errs...)
}

// Sweep deletes expired KV entries and sessions (see state.Store). It returns
// the number of rows removed.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	now := s.now().UnixNano()
	var total int64
	for _, q := range []string{
		`DELETE FROM interop_events WHERE expires_at <= ?`,
		`DELETE FROM interop_actions WHERE expires_at <= ?`,
		`DELETE FROM tasks WHERE dead AND event_seq IS NOT NULL AND dead_at <= ? - 1209600000000000`,
		`DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at <= ?`,
		`DELETE FROM sessions WHERE expires_at <= ?`,
	} {
		res, err := s.w.ExecContext(ctx, q, now)
		if err != nil {
			return total, fmt.Errorf("state/sqlite: sweep: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	if err := s.r.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("state/sqlite: schema version: %w", err)
	}
	return int(v.Int64), nil
}

func (s *Store) Resources() state.ResourceMap { return resourceMap{s} }
func (s *Store) KV(scope string) state.KV {
	return advanced.Space{Read: s.r, Write: s.w, Scope: scope, Now: s.now}
}
func (s *Store) BufferedScope(_ context.Context, scope string) (state.KV, func(context.Context, state.KV) error) {
	return s.KV(scope).(advanced.Space).Buffer()
}
func (s *Store) WithAtomicScope(ctx context.Context, scope string, fn func(state.KV) error) error {
	return s.KV(scope).(advanced.Space).Transaction(ctx, fn)
}
func (s *Store) Tasks() state.TaskQueue       { return taskQueue{s} }
func (s *Store) Audit() state.AuditLog        { return auditLog{s} }
func (s *Store) Syncs() state.SyncLog         { return syncLog{s} }
func (s *Store) Sessions() state.SessionStore { return sessionStore{s} }

// ───────────────────────────────────────────────────────────── migrations

func loadMigrations() ([]storeutil.Migration, error) {
	return storeutil.LoadMigrations(migrationFS, "migrations")
}

func (s *Store) migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("state/sqlite: load migrations: %w", err)
	}
	if _, err := s.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("state/sqlite: create schema_migrations: %w", err)
	}

	latest := storeutil.Latest(ms)
	var current sql.NullInt64
	if err := s.w.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("state/sqlite: read schema version: %w", err)
	}
	if int(current.Int64) > latest {
		return fmt.Errorf("state/sqlite: database schema version %d is newer than this binary supports (%d)", current.Int64, latest)
	}

	for _, m := range ms {
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration applies m in its own transaction unless it is already
// recorded. The check runs inside the (IMMEDIATE) transaction so two processes
// opening the same file concurrently cannot both apply it.
func (s *Store) applyMigration(ctx context.Context, m storeutil.Migration) (err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state/sqlite: migration %s: %w", m.Name, err)
	}
	defer rollback(tx, &err)

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&n); err != nil {
		return fmt.Errorf("state/sqlite: migration %s: %w", m.Name, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("state/sqlite: migration %s: %w", m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.Version, m.Name, s.now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("state/sqlite: migration %s: %w", m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state/sqlite: migration %s: %w", m.Name, err)
	}
	return nil
}

// ───────────────────────────────────────────────────────────── helpers

// toNanos converts t to unix nanoseconds, rejecting times that do not fit
// (roughly before 1678 or after 2262), including the zero time.
func toNanos(field string, t time.Time) (int64, error) {
	n, err := storeutil.ToNanos(field, t)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: %w", err)
	}
	return n, nil
}

var (
	orNow     = storeutil.OrNow
	fromNanos = storeutil.FromNanos
	notFound  = storeutil.NotFound
)

// jsonArg validates an optional JSON payload and converts it to a SQL argument
// (NULL when empty).
func jsonArg(field string, v []byte) (any, error) {
	if err := storeutil.CheckJSON(field, v); err != nil {
		return nil, fmt.Errorf("state/sqlite: %w", err)
	}
	if len(v) == 0 {
		return nil, nil
	}
	return string(v), nil
}

// jsonOut converts a nullable TEXT column back to a json.RawMessage (nil for NULL).
func jsonOut(v sql.NullString) []byte {
	if !v.Valid {
		return nil
	}
	return []byte(v.String)
}

// clampLimit returns def for n <= 0 and caps n at state.MaxListLimit.
func clampLimit(n, def int) int { return storeutil.ClampLimit(n, def) }

// rollback rolls tx back when it was not committed, joining a rollback failure
// into *err. Use it as `defer rollback(tx, &err)` with a named error result.
func rollback(tx *sql.Tx, err *error) {
	if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
		*err = errors.Join(*err, fmt.Errorf("state/sqlite: rollback: %w", rbErr))
	}
}

func (s *Store) Interop() state.InteropStore { return interopdb.Store{DB: s.w} }
