package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

// analytics implements state.Analytics and state.AnalyticsTotals (schema in
// migrations/0002_analytics.sql: buckets are unix seconds).
type analytics struct{ s *Store }

var (
	_ state.Analytics       = analytics{}
	_ state.AnalyticsTotals = analytics{}
)

func (s *Store) Analytics() state.Analytics { return analytics{s} }

// upsertSQL is the merge of a point into its row, per kind.
var upsertSQL = map[state.AnalyticsKind]string{
	state.AnalyticsCounter:   `analytics_points.value + EXCLUDED.value`,
	state.AnalyticsGaugeMax:  `GREATEST(analytics_points.value, EXCLUDED.value)`,
	state.AnalyticsGaugeLast: `EXCLUDED.value`,
}

// Record merges points into their rows in one transaction (see state.Analytics).
// Each point is its own upsert: a multi-row INSERT … ON CONFLICT cannot touch the
// same row twice, and rows are written in key order so that concurrent Records
// cannot deadlock.
func (a analytics) Record(ctx context.Context, kind state.AnalyticsKind, points ...state.AnalyticsPoint) error {
	rows, err := storeutil.AnalyticsRows(kind, points, a.s.now)
	if err != nil {
		return fmt.Errorf("state/postgres: record analytics: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	q := `INSERT INTO analytics_points (server, metric, dim, bucket, kind, value) VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (server, metric, dim, bucket) DO UPDATE SET
		   value = ` + upsertSQL[kind] + `, kind = EXCLUDED.kind`
	err = pgx.BeginTxFunc(ctx, a.s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var b pgx.Batch
		for _, r := range rows {
			b.Queue(q, r.Server, r.Metric, r.Dim, r.Bucket, string(kind), r.Value)
		}
		return tx.SendBatch(ctx, &b).Close()
	})
	if err != nil {
		return fmt.Errorf("state/postgres: record analytics: %w", err)
	}
	return nil
}

// analyticsWhere builds the filter of a query: server, metrics, optional dim and
// the bucket range.
func analyticsWhere(q state.AnalyticsQuery, withDim bool) (string, []any) {
	lo, hi := storeutil.AnalyticsRange(q.From, q.To)
	var b strings.Builder
	b.WriteString(`server = $1 AND bucket >= $2 AND bucket < $3`)
	args := []any{q.Server, lo, hi}
	if len(q.Metrics) > 0 {
		args = append(args, q.Metrics)
		b.WriteString(` AND metric = ANY($` + strconv.Itoa(len(args)) + `)`)
	}
	if withDim && q.Dim != nil {
		args = append(args, *q.Dim)
		b.WriteString(` AND dim = $` + strconv.Itoa(len(args)))
	}
	return b.String(), args
}

func scanPoints(rows pgx.Rows, server string, withDim bool) ([]state.AnalyticsPoint, error) {
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (state.AnalyticsPoint, error) {
		p := state.AnalyticsPoint{Server: server}
		var bucket int64
		var err error
		if withDim {
			err = row.Scan(&p.Metric, &p.Dim, &bucket, &p.Value)
		} else {
			err = row.Scan(&p.Metric, &bucket, &p.Value)
		}
		p.Bucket = storeutil.FromUnix(bucket)
		return p, err
	})
	return emptyNil(out), err
}

// Query returns the points ordered by metric, dim, then bucket.
func (a analytics) Query(ctx context.Context, q state.AnalyticsQuery) ([]state.AnalyticsPoint, error) {
	where, args := analyticsWhere(q, true)
	rows, err := a.s.pool.Query(ctx,
		`SELECT metric, dim, bucket, value FROM analytics_points WHERE `+where+` ORDER BY metric, dim, bucket`, args...)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: query analytics: %w", err)
	}
	out, err := scanPoints(rows, q.Server, true)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: query analytics: %w", err)
	}
	return out, nil
}

// Totals sums every dimension per metric and bucket (state.AnalyticsTotals).
func (a analytics) Totals(ctx context.Context, q state.AnalyticsQuery) ([]state.AnalyticsPoint, error) {
	where, args := analyticsWhere(q, false)
	rows, err := a.s.pool.Query(ctx,
		`SELECT metric, bucket, SUM(value) FROM analytics_points WHERE `+where+` GROUP BY metric, bucket ORDER BY metric, bucket`, args...)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: analytics totals: %w", err)
	}
	out, err := scanPoints(rows, q.Server, false)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: analytics totals: %w", err)
	}
	return out, nil
}

// Dims returns the dimensions of a metric in [from, to), largest first (ties by
// dim, byte order). Counter rows are summed, gauge rows give their max.
func (a analytics) Dims(ctx context.Context, server, metric string, from, to time.Time, limit int) ([]state.AnalyticsDim, error) {
	lo, hi := storeutil.AnalyticsRange(from, to)
	rows, err := a.s.pool.Query(ctx,
		`SELECT dim, CASE WHEN MIN(kind) = 'counter' THEN SUM(value) ELSE MAX(value) END AS v
		 FROM analytics_points WHERE server = $1 AND metric = $2 AND bucket >= $3 AND bucket < $4
		 GROUP BY dim ORDER BY v DESC, dim LIMIT $5`,
		server, metric, lo, hi, storeutil.AnalyticsDimsLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("state/postgres: analytics dims: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (state.AnalyticsDim, error) {
		var d state.AnalyticsDim
		err := row.Scan(&d.Dim, &d.Value)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("state/postgres: analytics dims: %w", err)
	}
	return emptyNil(out), nil
}

// MarkActive remembers a token in a daily set; marking it again is a no-op.
func (a analytics) MarkActive(ctx context.Context, server, set, day, token string) error {
	if err := storeutil.CheckActive(server, set, day, token); err != nil {
		return fmt.Errorf("state/postgres: mark active: %w", err)
	}
	if _, err := a.s.pool.Exec(ctx,
		`INSERT INTO analytics_active (server, set_name, day, token) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		server, set, day, token); err != nil {
		return fmt.Errorf("state/postgres: mark active: %w", err)
	}
	return nil
}

// CountActive returns the number of distinct tokens of a daily set.
func (a analytics) CountActive(ctx context.Context, server, set, day string) (int64, error) {
	var n int64
	if err := a.s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM analytics_active WHERE server = $1 AND set_name = $2 AND day = $3`,
		server, set, day).Scan(&n); err != nil {
		return 0, fmt.Errorf("state/postgres: count active: %w", err)
	}
	return n, nil
}

// DropActive deletes the tokens of every day strictly before day.
func (a analytics) DropActive(ctx context.Context, before string) (int64, error) {
	if err := storeutil.CheckDay(before); err != nil {
		return 0, fmt.Errorf("state/postgres: drop active: %w", err)
	}
	tag, err := a.s.pool.Exec(ctx, `DELETE FROM analytics_active WHERE day < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("state/postgres: drop active: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Purge deletes the points of a server (every server for "") with a bucket
// strictly before before.
func (a analytics) Purge(ctx context.Context, server string, before time.Time) (int64, error) {
	b := storeutil.AnalyticsBefore(before)
	q, args := `DELETE FROM analytics_points WHERE server = $1 AND bucket < $2`, []any{server, b}
	if server == "" {
		q, args = `DELETE FROM analytics_points WHERE bucket < $1`, []any{b}
	}
	tag, err := a.s.pool.Exec(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("state/postgres: purge analytics: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Since returns the oldest bucket of a server (zero time when it has none).
func (a analytics) Since(ctx context.Context, server string) (time.Time, error) {
	var v *int64
	if err := a.s.pool.QueryRow(ctx, `SELECT MIN(bucket) FROM analytics_points WHERE server = $1`, server).Scan(&v); err != nil {
		return time.Time{}, fmt.Errorf("state/postgres: analytics since: %w", err)
	}
	if v == nil {
		return time.Time{}, nil
	}
	return storeutil.FromUnix(*v), nil
}
