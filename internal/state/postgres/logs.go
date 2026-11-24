package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
)

// ───────────────────────────────────────────────────────────── audit

type auditLog struct{ s *Store }

// Append stores e and returns its ID (from a sequence: increasing, never
// reused). A zero Time is set to the store's current time.
func (a auditLog) Append(ctx context.Context, e state.AuditEntry) (int64, error) {
	t, err := toNanos("Time", orNow(e.Time, a.s.now))
	if err != nil {
		return 0, err
	}
	details, err := jsonArg("audit details", e.Details)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := a.s.pool.QueryRow(ctx,
		`INSERT INTO audit_log (time, server, actor, action, target, details) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		t, e.Server, e.Actor, e.Action, e.Target, details).Scan(&id); err != nil {
		return 0, fmt.Errorf("state/postgres: append audit: %w", err)
	}
	return id, nil
}

// List returns entries newest first (by ID). Server "" means all servers
// (project-level entries included); Before > 0 returns only entries with
// ID < Before.
func (a auditLog) List(ctx context.Context, f state.AuditFilter) ([]state.AuditEntry, error) {
	var q strings.Builder
	q.WriteString(`SELECT id, time, server, actor, action, target, details FROM audit_log WHERE TRUE`)
	var args []any
	if f.Server != "" {
		args = append(args, f.Server)
		q.WriteString(` AND server = $` + strconv.Itoa(len(args)))
	}
	if f.Before > 0 {
		args = append(args, f.Before)
		q.WriteString(` AND id < $` + strconv.Itoa(len(args)))
	}
	args = append(args, clampLimit(f.Limit, state.DefaultAuditLimit))
	q.WriteString(` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args)))

	rows, err := a.s.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: list audit: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (state.AuditEntry, error) {
		var e state.AuditEntry
		var t int64
		var details []byte
		if err := row.Scan(&e.ID, &t, &e.Server, &e.Actor, &e.Action, &e.Target, &details); err != nil {
			return state.AuditEntry{}, err
		}
		e.Time = fromNanos(t)
		e.Details = details
		return e, nil
	})
	if err != nil {
		return nil, fmt.Errorf("state/postgres: list audit: %w", err)
	}
	return emptyNil(out), nil
}

// ───────────────────────────────────────────────────────────── sync history

type syncLog struct{ s *Store }

// Record stores r and returns its ID. A zero Time is set to the store's
// current time.
func (l syncLog) Record(ctx context.Context, r state.SyncRecord) (int64, error) {
	t, err := toNanos("Time", orNow(r.Time, l.s.now))
	if err != nil {
		return 0, err
	}
	details, err := jsonArg("sync details", r.Details)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := l.s.pool.QueryRow(ctx,
		`INSERT INTO sync_log (time, revision, target, status, message, details) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		t, r.Revision, r.Target, r.Status, r.Message, details).Scan(&id); err != nil {
		return 0, fmt.Errorf("state/postgres: record sync: %w", err)
	}
	return id, nil
}

// List returns records newest first (by ID); target "" means all targets.
func (l syncLog) List(ctx context.Context, target string, limit int) ([]state.SyncRecord, error) {
	q := `SELECT id, time, revision, target, status, message, details FROM sync_log`
	var args []any
	if target != "" {
		args = append(args, target)
		q += ` WHERE target = $1`
	}
	args = append(args, clampLimit(limit, state.DefaultSyncLimit))
	q += ` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := l.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: list syncs: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (state.SyncRecord, error) {
		var r state.SyncRecord
		var t int64
		var details []byte
		if err := row.Scan(&r.ID, &t, &r.Revision, &r.Target, &r.Status, &r.Message, &details); err != nil {
			return state.SyncRecord{}, err
		}
		r.Time = fromNanos(t)
		r.Details = details
		return r, nil
	})
	if err != nil {
		return nil, fmt.Errorf("state/postgres: list syncs: %w", err)
	}
	return emptyNil(out), nil
}

// emptyNil returns nil for an empty result, like the SQLite backend (callers
// may encode results as JSON, where nil and empty differ).
func emptyNil[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

// ───────────────────────────────────────────────────────────── sessions

type sessionStore struct{ s *Store }

// Put inserts or replaces a session. ExpiresAt is required: a session without
// an expiry is rejected rather than treated as everlasting.
func (ss sessionStore) Put(ctx context.Context, s state.Session) error {
	if s.ID == "" {
		return errors.New("state/postgres: put session: empty id")
	}
	if s.ExpiresAt.IsZero() {
		return errors.New("state/postgres: put session: zero ExpiresAt")
	}
	exp, err := toNanos("ExpiresAt", s.ExpiresAt)
	if err != nil {
		return err
	}
	data, err := jsonArg("session data", s.Data)
	if err != nil {
		return err
	}
	_, err = ss.s.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, data, expires_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (id) DO UPDATE SET
		   user_id = EXCLUDED.user_id, data = EXCLUDED.data, expires_at = EXCLUDED.expires_at`,
		s.ID, s.UserID, data, exp)
	if err != nil {
		return fmt.Errorf("state/postgres: put session: %w", err)
	}
	return nil
}

// Get returns a live session; expired sessions are ErrNotFound and are
// deleted lazily.
func (ss sessionStore) Get(ctx context.Context, id string) (state.Session, error) {
	var s state.Session
	var data []byte
	var exp int64
	err := ss.s.pool.QueryRow(ctx,
		`SELECT id, user_id, data, expires_at FROM sessions WHERE id = $1`, id).Scan(&s.ID, &s.UserID, &data, &exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return state.Session{}, notFound("session")
	}
	if err != nil {
		return state.Session{}, fmt.Errorf("state/postgres: get session: %w", err)
	}
	if now := ss.s.now().UnixNano(); exp <= now {
		_, _ = ss.s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1 AND expires_at <= $2`, id, now)
		return state.Session{}, notFound("session")
	}
	s.Data = data
	s.ExpiresAt = fromNanos(exp)
	return s, nil
}

// Delete removes a session; deleting a missing session is not an error.
func (ss sessionStore) Delete(ctx context.Context, id string) error {
	if _, err := ss.s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("state/postgres: delete session: %w", err)
	}
	return nil
}
