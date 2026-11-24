package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/npjonath/cicord/internal/state"
)

const (
	defaultAuditLimit = 50
	defaultSyncLimit  = 50
)

// ───────────────────────────────────────────────────────────── audit

type auditLog struct{ s *Store }

// Append stores e and returns its ID. IDs increase strictly and are never
// reused. A zero Time is set to the store's current time.
func (a auditLog) Append(ctx context.Context, e state.AuditEntry) (int64, error) {
	t, err := toNanos("Time", orNow(e.Time, a.s.now))
	if err != nil {
		return 0, err
	}
	details, err := jsonArg("audit details", e.Details)
	if err != nil {
		return 0, err
	}
	res, err := a.s.w.ExecContext(ctx,
		`INSERT INTO audit_log (time, server, actor, action, target, details) VALUES (?, ?, ?, ?, ?, ?)`,
		t, e.Server, e.Actor, e.Action, e.Target, details)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: append audit: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: append audit: %w", err)
	}
	return id, nil
}

// List returns entries newest first (by ID). Server "" means all servers
// (project-level entries included); Before > 0 returns only entries with
// ID < Before. Limit <= 0 means 50; it is capped at 1000.
func (a auditLog) List(ctx context.Context, f state.AuditFilter) ([]state.AuditEntry, error) {
	q := `SELECT id, time, server, actor, action, target, details FROM audit_log WHERE 1 = 1`
	var args []any
	if f.Server != "" {
		q += ` AND server = ?`
		args = append(args, f.Server)
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, clampLimit(f.Limit, defaultAuditLimit))

	rows, err := a.s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: list audit: %w", err)
	}
	defer rows.Close()
	var out []state.AuditEntry
	for rows.Next() {
		var e state.AuditEntry
		var t int64
		var details sql.NullString
		if err := rows.Scan(&e.ID, &t, &e.Server, &e.Actor, &e.Action, &e.Target, &details); err != nil {
			return nil, fmt.Errorf("state/sqlite: list audit: %w", err)
		}
		e.Time = fromNanos(t)
		e.Details = jsonOut(details)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: list audit: %w", err)
	}
	return out, nil
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
	res, err := l.s.w.ExecContext(ctx,
		`INSERT INTO sync_log (time, revision, target, status, message, details) VALUES (?, ?, ?, ?, ?, ?)`,
		t, r.Revision, r.Target, r.Status, r.Message, details)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: record sync: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: record sync: %w", err)
	}
	return id, nil
}

// List returns records newest first (by ID); target "" means all targets.
// limit <= 0 means 50; it is capped at 1000.
func (l syncLog) List(ctx context.Context, target string, limit int) ([]state.SyncRecord, error) {
	q := `SELECT id, time, revision, target, status, message, details FROM sync_log`
	var args []any
	if target != "" {
		q += ` WHERE target = ?`
		args = append(args, target)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, clampLimit(limit, defaultSyncLimit))

	rows, err := l.s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: list syncs: %w", err)
	}
	defer rows.Close()
	var out []state.SyncRecord
	for rows.Next() {
		var r state.SyncRecord
		var t int64
		var details sql.NullString
		if err := rows.Scan(&r.ID, &t, &r.Revision, &r.Target, &r.Status, &r.Message, &details); err != nil {
			return nil, fmt.Errorf("state/sqlite: list syncs: %w", err)
		}
		r.Time = fromNanos(t)
		r.Details = jsonOut(details)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: list syncs: %w", err)
	}
	return out, nil
}

// ───────────────────────────────────────────────────────────── sessions

type sessionStore struct{ s *Store }

// Put inserts or replaces a session. ExpiresAt is required: a session without
// an expiry is rejected rather than treated as everlasting.
func (ss sessionStore) Put(ctx context.Context, s state.Session) error {
	if s.ID == "" {
		return errors.New("state/sqlite: put session: empty id")
	}
	if s.ExpiresAt.IsZero() {
		return errors.New("state/sqlite: put session: zero ExpiresAt")
	}
	exp, err := toNanos("ExpiresAt", s.ExpiresAt)
	if err != nil {
		return err
	}
	data, err := jsonArg("session data", s.Data)
	if err != nil {
		return err
	}
	_, err = ss.s.w.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, data, expires_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   user_id = excluded.user_id, data = excluded.data, expires_at = excluded.expires_at`,
		s.ID, s.UserID, data, exp)
	if err != nil {
		return fmt.Errorf("state/sqlite: put session: %w", err)
	}
	return nil
}

// Get returns a live session; expired sessions are ErrNotFound and are
// deleted lazily.
func (ss sessionStore) Get(ctx context.Context, id string) (state.Session, error) {
	var s state.Session
	var data sql.NullString
	var exp int64
	err := ss.s.r.QueryRowContext(ctx,
		`SELECT id, user_id, data, expires_at FROM sessions WHERE id = ?`, id).Scan(&s.ID, &s.UserID, &data, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Session{}, notFound("session")
	}
	if err != nil {
		return state.Session{}, fmt.Errorf("state/sqlite: get session: %w", err)
	}
	if now := ss.s.now().UnixNano(); exp <= now {
		_, _ = ss.s.w.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND expires_at <= ?`, id, now)
		return state.Session{}, notFound("session")
	}
	s.Data = jsonOut(data)
	s.ExpiresAt = fromNanos(exp)
	return s, nil
}

// Delete removes a session; deleting a missing session is not an error.
func (ss sessionStore) Delete(ctx context.Context, id string) error {
	if _, err := ss.s.w.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("state/sqlite: delete session: %w", err)
	}
	return nil
}
