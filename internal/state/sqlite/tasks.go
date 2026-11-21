package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/npjonath/codi/internal/state"
)

const (
	defaultDueLimit = 100
	maxDueLimit     = 1000
)

// ErrTaskIDConflict is returned by Schedule when the task ID is already used by
// a task of another scope. Task IDs are global (Complete takes only an ID), so
// one scope must not be able to replace another scope's task.
var ErrTaskIDConflict = errors.New("state/sqlite: task id already used by another scope")

type taskQueue struct{ s *Store }

// Schedule inserts the task, or replaces the task with the same ID and scope.
func (q taskQueue) Schedule(ctx context.Context, t state.Task) error {
	switch {
	case t.ID == "":
		return errors.New("state/sqlite: schedule task: empty id")
	case t.Scope == "":
		return errors.New("state/sqlite: schedule task: empty scope")
	case t.RunAt.IsZero():
		return errors.New("state/sqlite: schedule task: zero RunAt")
	}
	runAt, err := toNanos("RunAt", t.RunAt)
	if err != nil {
		return err
	}
	payload, err := jsonArg("task payload", t.Payload)
	if err != nil {
		return err
	}
	res, err := q.s.w.ExecContext(ctx,
		`INSERT INTO tasks (id, scope, name, payload, run_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   name = excluded.name, payload = excluded.payload, run_at = excluded.run_at
		 WHERE tasks.scope = excluded.scope`,
		t.ID, t.Scope, t.Name, payload, runAt)
	if err != nil {
		return fmt.Errorf("state/sqlite: schedule task: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%w: %s", ErrTaskIDConflict, t.ID)
	}
	return nil
}

// Cancel removes the task with this scope and ID; a missing task is not an error.
func (q taskQueue) Cancel(ctx context.Context, scope, id string) error {
	if _, err := q.s.w.ExecContext(ctx, `DELETE FROM tasks WHERE scope = ? AND id = ?`, scope, id); err != nil {
		return fmt.Errorf("state/sqlite: cancel task: %w", err)
	}
	return nil
}

// Due returns tasks with RunAt <= now, oldest first (ties by ID). limit <= 0
// means 100; it is capped at 1000.
func (q taskQueue) Due(ctx context.Context, now time.Time, limit int) ([]state.Task, error) {
	limit = clampLimit(limit, defaultDueLimit, maxDueLimit)
	var cutoff int64
	switch {
	case now.Before(minTime):
		return nil, nil
	case now.After(maxTime):
		cutoff = maxTime.UnixNano()
	default:
		cutoff = now.UnixNano()
	}
	rows, err := q.s.r.QueryContext(ctx,
		`SELECT id, scope, name, payload, run_at FROM tasks WHERE run_at <= ? ORDER BY run_at, id LIMIT ?`,
		cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: due tasks: %w", err)
	}
	defer rows.Close()
	var out []state.Task
	for rows.Next() {
		var t state.Task
		var payload sql.NullString
		var runAt int64
		if err := rows.Scan(&t.ID, &t.Scope, &t.Name, &payload, &runAt); err != nil {
			return nil, fmt.Errorf("state/sqlite: due tasks: %w", err)
		}
		t.Payload = jsonOut(payload)
		t.RunAt = fromNanos(runAt)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: due tasks: %w", err)
	}
	return out, nil
}

// Complete removes a task after it ran; a missing task is not an error.
func (q taskQueue) Complete(ctx context.Context, id string) error {
	if _, err := q.s.w.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("state/sqlite: complete task: %w", err)
	}
	return nil
}
