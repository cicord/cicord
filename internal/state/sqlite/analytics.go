package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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
	state.AnalyticsCounter:   `analytics_points.value + excluded.value`,
	state.AnalyticsGaugeMax:  `max(analytics_points.value, excluded.value)`,
	state.AnalyticsGaugeLast: `excluded.value`,
}

// Record merges points into their rows in one transaction (see state.Analytics).
func (a analytics) Record(ctx context.Context, kind state.AnalyticsKind, points ...state.AnalyticsPoint) (err error) {
	rows, err := storeutil.AnalyticsRows(kind, points, a.s.now)
	if err != nil {
		return fmt.Errorf("state/sqlite: record analytics: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	tx, err := a.s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state/sqlite: record analytics: %w", err)
	}
	defer rollback(tx, &err)
	//nolint:gosec // G202: upsertSQL holds constant SQL fragments only
	q := `INSERT INTO analytics_points (server, metric, dim, bucket, kind, value) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (server, metric, dim, bucket) DO UPDATE SET
		   value = ` + upsertSQL[kind] + `, kind = excluded.kind`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("state/sqlite: record analytics: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.Server, r.Metric, r.Dim, r.Bucket, string(kind), r.Value); err != nil {
			return fmt.Errorf("state/sqlite: record analytics: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state/sqlite: record analytics: %w", err)
	}
	return nil
}

// analyticsWhere builds the filter of a query: server, metrics, optional dim and the
// bucket range.
func analyticsWhere(q state.AnalyticsQuery, withDim bool) (string, []any) {
	lo, hi := storeutil.AnalyticsRange(q.From, q.To)
	var b strings.Builder
	b.WriteString(`server = ? AND bucket >= ? AND bucket < ?`)
	args := []any{q.Server, lo, hi}
	if len(q.Metrics) > 0 {
		b.WriteString(` AND metric IN (?` + strings.Repeat(`, ?`, len(q.Metrics)-1) + `)`)
		for _, m := range q.Metrics {
			args = append(args, m)
		}
	}
	if withDim && q.Dim != nil {
		b.WriteString(` AND dim = ?`)
		args = append(args, *q.Dim)
	}
	return b.String(), args
}

// Query returns the points ordered by metric, dim, then bucket.
func (a analytics) Query(ctx context.Context, q state.AnalyticsQuery) ([]state.AnalyticsPoint, error) {
	where, args := analyticsWhere(q, true)
	query := `SELECT metric, dim, bucket, value FROM analytics_points WHERE ` + where + ` ORDER BY metric, dim, bucket` //nolint:gosec // G202: where holds placeholders only
	rows, err := a.s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: query analytics: %w", err)
	}
	defer rows.Close()
	var out []state.AnalyticsPoint
	for rows.Next() {
		p := state.AnalyticsPoint{Server: q.Server}
		var bucket int64
		if err := rows.Scan(&p.Metric, &p.Dim, &bucket, &p.Value); err != nil {
			return nil, fmt.Errorf("state/sqlite: query analytics: %w", err)
		}
		p.Bucket = storeutil.FromUnix(bucket)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: query analytics: %w", err)
	}
	return out, nil
}

// Totals sums every dimension per metric and bucket (state.AnalyticsTotals).
func (a analytics) Totals(ctx context.Context, q state.AnalyticsQuery) ([]state.AnalyticsPoint, error) {
	where, args := analyticsWhere(q, false)
	query := `SELECT metric, bucket, SUM(value) FROM analytics_points WHERE ` + where + ` GROUP BY metric, bucket ORDER BY metric, bucket` //nolint:gosec // G202: where holds placeholders only
	rows, err := a.s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: analytics totals: %w", err)
	}
	defer rows.Close()
	var out []state.AnalyticsPoint
	for rows.Next() {
		p := state.AnalyticsPoint{Server: q.Server}
		var bucket int64
		if err := rows.Scan(&p.Metric, &bucket, &p.Value); err != nil {
			return nil, fmt.Errorf("state/sqlite: analytics totals: %w", err)
		}
		p.Bucket = storeutil.FromUnix(bucket)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: analytics totals: %w", err)
	}
	return out, nil
}

// Dims returns the dimensions of a metric in [from, to), largest first (ties by
// dim, byte order). Counter rows are summed, gauge rows give their max.
func (a analytics) Dims(ctx context.Context, server, metric string, from, to time.Time, limit int) ([]state.AnalyticsDim, error) {
	lo, hi := storeutil.AnalyticsRange(from, to)
	rows, err := a.s.r.QueryContext(ctx,
		`SELECT dim, CASE WHEN MIN(kind) = 'counter' THEN SUM(value) ELSE MAX(value) END AS v
		 FROM analytics_points WHERE server = ? AND metric = ? AND bucket >= ? AND bucket < ?
		 GROUP BY dim ORDER BY v DESC, dim LIMIT ?`,
		server, metric, lo, hi, storeutil.AnalyticsDimsLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: analytics dims: %w", err)
	}
	defer rows.Close()
	var out []state.AnalyticsDim
	for rows.Next() {
		var d state.AnalyticsDim
		if err := rows.Scan(&d.Dim, &d.Value); err != nil {
			return nil, fmt.Errorf("state/sqlite: analytics dims: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: analytics dims: %w", err)
	}
	return out, nil
}

// MarkActive remembers a token in a daily set; marking it again is a no-op.
func (a analytics) MarkActive(ctx context.Context, server, set, day, token string) error {
	if err := storeutil.CheckActive(server, set, day, token); err != nil {
		return fmt.Errorf("state/sqlite: mark active: %w", err)
	}
	if _, err := a.s.w.ExecContext(ctx,
		`INSERT INTO analytics_active (server, set_name, day, token) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		server, set, day, token); err != nil {
		return fmt.Errorf("state/sqlite: mark active: %w", err)
	}
	return nil
}

// CountActive returns the number of distinct tokens of a daily set.
func (a analytics) CountActive(ctx context.Context, server, set, day string) (int64, error) {
	var n int64
	if err := a.s.r.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM analytics_active WHERE server = ? AND set_name = ? AND day = ?`,
		server, set, day).Scan(&n); err != nil {
		return 0, fmt.Errorf("state/sqlite: count active: %w", err)
	}
	return n, nil
}

// DropActive deletes the tokens of every day strictly before day.
func (a analytics) DropActive(ctx context.Context, before string) (int64, error) {
	if err := storeutil.CheckDay(before); err != nil {
		return 0, fmt.Errorf("state/sqlite: drop active: %w", err)
	}
	res, err := a.s.w.ExecContext(ctx, `DELETE FROM analytics_active WHERE day < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: drop active: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Purge deletes the points of a server (every server for "") with a bucket
// strictly before before.
func (a analytics) Purge(ctx context.Context, server string, before time.Time) (int64, error) {
	b := storeutil.AnalyticsBefore(before)
	var res sql.Result
	var err error
	if server == "" {
		res, err = a.s.w.ExecContext(ctx, `DELETE FROM analytics_points WHERE bucket < ?`, b)
	} else {
		res, err = a.s.w.ExecContext(ctx, `DELETE FROM analytics_points WHERE server = ? AND bucket < ?`, server, b)
	}
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: purge analytics: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Since returns the oldest bucket of a server (zero time when it has none).
func (a analytics) Since(ctx context.Context, server string) (time.Time, error) {
	var v sql.NullInt64
	err := a.s.r.QueryRowContext(ctx, `SELECT MIN(bucket) FROM analytics_points WHERE server = ?`, server).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("state/sqlite: analytics since: %w", err)
	}
	if !v.Valid {
		return time.Time{}, nil
	}
	return storeutil.FromUnix(v.Int64), nil
}
