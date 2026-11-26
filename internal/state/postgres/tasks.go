package postgres

import (
	"context"
	"crypto/rand"
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
		   name = EXCLUDED.name, payload = EXCLUDED.payload, run_at = EXCLUDED.run_at,
		   available_at = NULL, receipt = '', attempts = 0, failures = 0, last_failure = '', dead = FALSE
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
		`SELECT id, scope, name, payload, run_at FROM tasks WHERE event_seq IS NULL AND NOT dead AND COALESCE(available_at, run_at) <= $1 ORDER BY run_at, id LIMIT $2`,
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

// ClaimDue atomically leases eligible tasks; a new receipt fences late workers.
func (q taskQueue) ClaimDue(ctx context.Context, now, until time.Time, worker string, limit int) ([]state.TaskLease, error) {
	if worker == "" || !until.After(now) {
		return nil, errors.New("invalid task lease")
	}
	expires, err := toNanos("until", until)
	if err != nil {
		return nil, err
	}
	cutoff, ok := storeutil.DueCutoff(now)
	if !ok {
		return nil, nil
	}
	receipt := worker + "/" + rand.Text()
	rows, err := q.s.pool.Query(ctx, `UPDATE tasks SET available_at = $3, receipt = $4, attempts = attempts + 1
 WHERE id IN (SELECT id FROM tasks AS candidate WHERE NOT dead AND (event_seq IS NULL OR NOT EXISTS (SELECT 1 FROM tasks prior WHERE prior.event_stream=candidate.event_stream AND NOT prior.dead AND prior.event_seq<candidate.event_seq)) AND COALESCE(available_at, run_at) <= $1
 ORDER BY COALESCE(available_at, run_at), id LIMIT $2 FOR UPDATE SKIP LOCKED)
 RETURNING id, scope, name, payload, run_at, attempts, failures, event_seq IS NOT NULL`, cutoff, clampLimit(limit, state.DefaultDueLimit), expires, receipt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []state.TaskLease
	for rows.Next() {
		var t state.TaskLease
		var payload []byte
		var runAt int64
		if err := rows.Scan(&t.ID, &t.Scope, &t.Name, &payload, &runAt, &t.Attempt, &t.Failures, &t.Event); err != nil {
			return nil, err
		}
		t.Receipt, t.RunAt, t.Payload = receipt, fromNanos(runAt), payload
		out = append(out, t)
	}
	return out, rows.Err()
}

func (q taskQueue) Acknowledge(ctx context.Context, lease state.TaskLease) error {
	if lease.Receipt == "" {
		return errors.New("missing task receipt")
	}
	_, err := q.s.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1 AND receipt=$2`, lease.ID, lease.Receipt)
	return err
}

func (q taskQueue) Release(ctx context.Context, lease state.TaskLease, retry time.Time, failure string) error {
	if lease.Receipt == "" {
		return errors.New("missing task receipt")
	}
	var next any
	if !retry.IsZero() {
		n, err := toNanos("retry", retry)
		if err != nil {
			return err
		}
		next = n
	}
	added := 0
	if failure != "" {
		added = 1
	}
	_, err := q.s.pool.Exec(ctx, `UPDATE tasks SET receipt='', available_at=$3, failures=failures+$4, last_failure=$5, dead=$6,dead_at=$7 WHERE id=$1 AND receipt=$2`, lease.ID, lease.Receipt, next, added, failure, retry.IsZero(), q.s.now().UnixNano())
	return err
}

// DeadCounts reads only the partial dead-letter index for instance health.
func (q taskQueue) DeadCounts(ctx context.Context) (map[string]int, error) {
	rows, err := q.s.pool.Query(ctx, `SELECT scope,count(*) FROM tasks WHERE dead GROUP BY scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var scope string
		var n int
		if err := rows.Scan(&scope, &n); err != nil {
			return nil, err
		}
		out[scope] = n
	}
	return out, rows.Err()
}

// Plugin scopes have the form plugin:bot/instance/server. Match the entire final
// component, including for instances no longer present in the active revision.
const deadTaskServer = `dead AND substr(scope,1,7)='plugin:' AND substr(scope,length(scope)-length($1))='/' || $1`

func (q taskQueue) DeadLetters(ctx context.Context, server string, limit int) ([]state.DeadTask, error) {
	rows, err := q.s.pool.Query(ctx, `SELECT scope,id,name,run_at,attempts,failures,last_failure,event_seq IS NOT NULL FROM tasks WHERE `+deadTaskServer+` ORDER BY run_at,id LIMIT $2`, server, clampLimit(limit, state.DefaultDueLimit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []state.DeadTask{}
	for rows.Next() {
		var t state.DeadTask
		var runAt int64
		if err := rows.Scan(&t.Scope, &t.ID, &t.Name, &runAt, &t.Attempts, &t.Failures, &t.Reason, &t.Event); err != nil {
			return nil, err
		}
		t.RunAt = fromNanos(runAt)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (q taskQueue) ResolveDead(ctx context.Context, server, id string, retry bool, now time.Time) (bool, error) {
	query := `DELETE FROM tasks WHERE ` + deadTaskServer + ` AND id=$2`
	args := []any{server, id}
	if retry {
		n, err := toNanos("now", now)
		if err != nil {
			return false, err
		}
		query = `UPDATE tasks SET available_at=$3, dead=FALSE, failures=0, last_failure='' WHERE ` + deadTaskServer + ` AND id=$2`
		args = append(args, n)
	}
	res, err := q.s.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

func (q taskQueue) Consume(ctx context.Context, lease state.TaskLease) (bool, error) {
	if lease.Receipt == "" {
		return false, errors.New("missing lease receipt")
	}
	r, err := q.s.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1 AND receipt=$2`, lease.ID, lease.Receipt)
	if err != nil {
		return false, err
	}
	return r.RowsAffected() == 1, nil
}
