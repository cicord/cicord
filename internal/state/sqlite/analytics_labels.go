package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

// Remembered labels of analytics dimensions (state.AnalyticsLabels, schema in
// migrations/0003_analytics_labels.sql).

// PutLabels upserts labels in one transaction; an empty owner keeps the stored one.
func (a analytics) PutLabels(ctx context.Context, labels ...state.AnalyticsLabel) (err error) {
	rows, err := storeutil.LabelRows(labels, a.s.now)
	if err != nil {
		return fmt.Errorf("state/sqlite: put analytics labels: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	tx, err := a.s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state/sqlite: put analytics labels: %w", err)
	}
	defer rollback(tx, &err)
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO analytics_labels (server, dim, id, name, type, color, owner, deleted, seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (server, dim, id) DO UPDATE SET
		   name = excluded.name, type = excluded.type, color = excluded.color,
		   owner = CASE WHEN excluded.owner = '' THEN analytics_labels.owner ELSE excluded.owner END,
		   deleted = excluded.deleted, seen_at = excluded.seen_at`)
	if err != nil {
		return fmt.Errorf("state/sqlite: put analytics labels: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		deleted := 0
		if r.Deleted {
			deleted = 1
		}
		if _, err := stmt.ExecContext(ctx, r.Server, r.Dim, r.ID, r.Name, r.Type, r.Color, r.Owner, deleted, r.SeenAt); err != nil {
			return fmt.Errorf("state/sqlite: put analytics labels: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state/sqlite: put analytics labels: %w", err)
	}
	return nil
}

// GetLabels returns the stored labels of a server's dimension for ids.
func (a analytics) GetLabels(ctx context.Context, server, dim string, ids []string) ([]state.AnalyticsLabel, error) {
	if err := storeutil.CheckLabelIDs(ids); err != nil {
		return nil, fmt.Errorf("state/sqlite: get analytics labels: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, server, dim)
	for _, id := range ids {
		args = append(args, id)
	}
	//nolint:gosec // G202: inList holds placeholders only
	rows, err := a.s.r.QueryContext(ctx,
		`SELECT id, name, type, color, owner, deleted, seen_at FROM analytics_labels
		 WHERE server = ? AND dim = ? AND id IN `+inList(len(ids)), args...)
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: get analytics labels: %w", err)
	}
	defer rows.Close()
	var out []state.AnalyticsLabel
	for rows.Next() {
		l := state.AnalyticsLabel{Server: server, Dim: dim}
		var deleted int
		var seen int64
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &l.Color, &l.Owner, &deleted, &seen); err != nil {
			return nil, fmt.Errorf("state/sqlite: get analytics labels: %w", err)
		}
		l.Deleted, l.SeenAt = deleted != 0, storeutil.FromNanos(seen)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: get analytics labels: %w", err)
	}
	return out, nil
}

// PruneLabels deletes the labels of a server seen before before and those whose ID
// is no point's dimension any more.
func (a analytics) PruneLabels(ctx context.Context, server string, before time.Time) (int64, error) {
	b, err := storeutil.ToNanos("analytics labels before", before)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: prune analytics labels: %w", err)
	}
	res, err := a.s.w.ExecContext(ctx,
		`DELETE FROM analytics_labels WHERE server = ? AND (seen_at < ?
		   OR id NOT IN (SELECT DISTINCT dim FROM analytics_points WHERE server = ?))`,
		server, b, server)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: prune analytics labels: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
