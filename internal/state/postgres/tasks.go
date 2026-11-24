package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

type taskQueue struct{ s *Store }

// Schedule inserts the task, or replaces the task with the same ID and scope
// (state.ErrTaskIDConflict when the ID belongs to another scope).
func (q taskQueue) Schedule(ctx context.Context, t state.Task) error {
	switch {
	case t.ID == "":
		return errors.New("state/postgres: schedule task: empty id")
	case t.Scope == "":
		return errors.New("state/postgres: schedule task: empty scope")
	case t.RunAt.IsZero():
		return errors.New("state/postgres: schedule task: zero RunAt")
	}
	runAt, err := toNanos("RunAt", t.RunAt)
	if err != nil {
		return err
	}
	payload, err := jsonArg("task payload", t.Payload)
	if err != nil {
		return err
	}
	tag, err := q.s.pool.Exec(ctx,
		`INSERT INTO tasks (id, scope, name, payload, run_at) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name, payload = EXCLUDED.payload, run_at = EXCLUDED.run_at
		 WHERE tasks.scope = EXCLUDED.scope`,
		t.ID, t.Scope, t.Name, payload, runAt)
	if err != nil {
		return fmt.Errorf("state/postgres: schedule task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", state.ErrTaskIDConflict, t.ID)
	}
	return nil
}

// Cancel removes the task with this scope and ID; a missing task is not an error.
func (q taskQueue) Cancel(ctx context.Context, scope, id string) error {
	if _, err := q.s.pool.Exec(ctx, `DELETE FROM tasks WHERE scope = $1 AND id = $2`, scope, id); err != nil {
		return fmt.Errorf("state/postgres: cancel task: %w", err)
	}
	return nil
}

// Due returns tasks with RunAt <= now, oldest first (ties by ID).
func (q taskQueue) Due(ctx context.Context, now time.Time, limit int) ([]state.Task, error) {
	limit = clampLimit(limit, state.DefaultDueLimit)
	cutoff, ok := storeutil.DueCutoff(now)
	if !ok {
		return nil, nil
	}
	rows, err := q.s.pool.Query(ctx,
		`SELECT id, scope, name, payload, run_at FROM tasks WHERE run_at <= $1 ORDER BY run_at, id LIMIT $2`,
		cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: due tasks: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (state.Task, error) {
		var t state.Task
		var payload []byte
		var runAt int64
		if err := row.Scan(&t.ID, &t.Scope, &t.Name, &payload, &runAt); err != nil {
			return state.Task{}, err
		}
		t.Payload = payload
		t.RunAt = fromNanos(runAt)
		return t, nil
	})
	if err != nil {
		return nil, fmt.Errorf("state/postgres: due tasks: %w", err)
	}
	return emptyNil(out), nil
}

// Complete removes a task after it ran; a missing task is not an error.
func (q taskQueue) Complete(ctx context.Context, id string) error {
	if _, err := q.s.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id); err != nil {
		return fmt.Errorf("state/postgres: complete task: %w", err)
	}
	return nil
}
