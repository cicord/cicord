// Package postgres implements state.Store on PostgreSQL with pgx (pgxpool).
//
// It has exactly the semantics of the SQLite backend (both run the conformance
// suite in state/statetest); the schema notes in migrations/0001_init.sql
// explain how the data is represented.
//
// Connection: Open takes a libpq-style connection string, as a URL
// (postgres://user:password@host:5432/db?sslmode=verify-full) or as key=value
// pairs. pgx options work in it (pool_max_conns, sslmode, search_path to use a
// dedicated schema, default_query_exec_mode=exec behind PgBouncer in
// transaction mode, ...). The password is never included in errors or logs
// produced here; use Describe for a printable form.
//
// Migrations run at Open in one transaction under a transaction-level advisory
// lock, so several processes may start at once; a database whose schema is
// newer than this binary is refused.
//
// Ordering: audit and sync IDs come from sequences (BIGSERIAL). They increase
// strictly and are never reused, and "newest first" means by descending ID;
// with concurrent writers, ID order is the order in which IDs were allocated,
// which can differ slightly from commit order.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/advanced"
	"github.com/cicord/cicord/internal/state/internal/interopdb"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var _ state.Store = (*Store)(nil)

// migrationLockID is the pg_advisory_xact_lock key serializing migrations of
// concurrent processes ("cicord" in ASCII).
const migrationLockID int64 = 0x636963_6f7264

// Store is a PostgreSQL-backed state.Store. It is safe for concurrent use.
type Store struct {
	sqlDB *sql.DB
	pool  *pgxpool.Pool
	now   func() time.Time
	desc  string
}

// Option configures Open.
type Option func(*options)

type options struct {
	clock func() time.Time
}

// WithClock replaces time.Now as the source of "now" for TTL expiry, session
// expiry and default timestamps. Intended for tests.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.clock = now }
}

// ErrInvalidURL is returned by Open for a connection string it cannot parse.
// The parse error itself is not returned: it may quote the password.
var ErrInvalidURL = errors.New("state/postgres: invalid database URL (expected postgres://user:password@host:port/database?options or key=value pairs)")

// Open connects to the database, checks the connection and applies pending
// schema migrations.
func Open(ctx context.Context, connString string, opts ...Option) (*Store, error) {
	o := options{clock: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	if o.clock == nil {
		return nil, errors.New("state/postgres: nil clock")
	}
	if connString == "" {
		return nil, errors.New("state/postgres: empty database URL")
	}
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, ErrInvalidURL
	}
	if _, ok := cfg.ConnConfig.RuntimeParams["application_name"]; !ok {
		cfg.ConnConfig.RuntimeParams["application_name"] = "cicord"
	}
	s := &Store{now: o.clock, desc: describe(cfg)}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: %s: %w", s.desc, err)
	}
	s.pool = pool
	s.sqlDB = stdlib.OpenDBFromPool(pool)
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("state/postgres: connect to %s: %w", s.desc, err)
	}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// describe returns user@host:port/database (no password) for logs and errors.
func describe(cfg *pgxpool.Config) string {
	c := cfg.ConnConfig
	host := net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port)))
	if c.Host != "" && c.Host[0] == '/' { // unix socket directory
		host = c.Host
	}
	return "postgres://" + c.User + "@" + host + "/" + c.Database
}

// Describe returns the database the store is connected to, without the
// password (postgres://user@host:port/database).
func (s *Store) Describe() string { return s.desc }

// Close closes the connection pool. It is safe to call more than once.
func (s *Store) Close() error {
	_ = s.sqlDB.Close()
	s.pool.Close()
	return nil
}

// Sweep deletes expired KV entries and sessions (see state.Store). It returns
// the number of rows removed.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	now := s.now().UnixNano()
	var total int64
	for _, q := range []string{
		`DELETE FROM interop_events WHERE expires_at <= $1`,
		`DELETE FROM interop_actions WHERE expires_at <= $1`,
		`DELETE FROM tasks WHERE dead AND event_seq IS NOT NULL AND dead_at <= $1 - 1209600000000000`,
		`DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at <= $1`,
		`DELETE FROM sessions WHERE expires_at <= $1`,
	} {
		tag, err := s.pool.Exec(ctx, q, now)
		if err != nil {
			return total, fmt.Errorf("state/postgres: sweep: %w", err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("state/postgres: schema version: %w", err)
	}
	return v, nil
}

func (s *Store) Resources() state.ResourceMap { return resourceMap{s} }
func (s *Store) KV(scope string) state.KV {
	return advanced.Space{Read: s.sqlDB, Write: s.sqlDB, Scope: scope, Now: s.now, Postgres: true}
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

// migrate applies every pending migration in one transaction. DDL is
// transactional in PostgreSQL, so a failed migration leaves the schema as it
// was; the advisory lock makes concurrent Opens apply each migration once.
func (s *Store) migrate(ctx context.Context) error {
	ms, err := storeutil.LoadMigrations(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("state/postgres: load migrations: %w", err)
	}
	latest := storeutil.Latest(ms)
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT    NOT NULL,
			applied_at BIGINT  NOT NULL
		)`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
		rows, err := tx.Query(ctx, `SELECT version, name FROM schema_migrations`)
		if err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
		type appliedMigration struct {
			Version int
			Name    string
		}
		applied, err := pgx.CollectRows(rows, pgx.RowToStructByPos[appliedMigration])
		if err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
		done := map[int]bool{}
		names := map[int]string{}
		for _, a := range applied {
			if a.Version > latest {
				return fmt.Errorf("database schema version %d is newer than this binary supports (%d)", a.Version, latest)
			}
			done[a.Version] = true
			names[a.Version] = a.Name
		}
		if err := storeutil.CheckApplied(names, ms); err != nil {
			return err
		}
		for _, m := range ms {
			if done[m.Version] {
				continue
			}
			// No arguments: pgx uses the simple protocol, which allows several
			// statements in one call.
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return fmt.Errorf("migration %s: %w", m.Name, err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, $3)`,
				m.Version, m.Name, s.now().UTC().UnixNano()); err != nil {
				return fmt.Errorf("migration %s: %w", m.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("state/postgres: migrate: %w", err)
	}
	return nil
}

// ───────────────────────────────────────────────────────────── helpers

// toNanos converts t to unix nanoseconds, rejecting times that do not fit
// (roughly before 1678 or after 2262), including the zero time.
func toNanos(field string, t time.Time) (int64, error) {
	n, err := storeutil.ToNanos(field, t)
	if err != nil {
		return 0, fmt.Errorf("state/postgres: %w", err)
	}
	return n, nil
}

var (
	orNow     = storeutil.OrNow
	fromNanos = storeutil.FromNanos
	notFound  = storeutil.NotFound
)

// jsonArg validates an optional JSON payload and converts it to a BYTEA
// argument (NULL when empty).
func jsonArg(field string, v []byte) (any, error) {
	if err := storeutil.CheckJSON(field, v); err != nil {
		return nil, fmt.Errorf("state/postgres: %w", err)
	}
	if len(v) == 0 {
		return nil, nil
	}
	return v, nil
}

// clampLimit returns def for n <= 0 and caps n at state.MaxListLimit.
func clampLimit(n, def int) int { return storeutil.ClampLimit(n, def) }

func (s *Store) Interop() state.InteropStore { return interopdb.Store{DB: s.sqlDB, Postgres: true} }
