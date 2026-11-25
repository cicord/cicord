package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

// Remembered labels of analytics dimensions (state.AnalyticsLabels, schema in
// migrations/0003_analytics_labels.sql).

// PutLabels upserts labels in one transaction, in key order (no deadlock between
// concurrent writers); an empty owner keeps the stored one.
func (a analytics) PutLabels(ctx context.Context, labels ...state.AnalyticsLabel) error {
	rows, err := storeutil.LabelRows(labels, a.s.now)
	if err != nil {
		return fmt.Errorf("state/postgres: put analytics labels: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	const q = `INSERT INTO analytics_labels (server, dim, id, name, type, color, owner, deleted, seen_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (server, dim, id) DO UPDATE SET
		   name = EXCLUDED.name, type = EXCLUDED.type, color = EXCLUDED.color,
		   owner = CASE WHEN EXCLUDED.owner = '' THEN analytics_labels.owner ELSE EXCLUDED.owner END,
		   deleted = EXCLUDED.deleted, seen_at = EXCLUDED.seen_at`
	err = pgx.BeginTxFunc(ctx, a.s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var b pgx.Batch
		for _, r := range rows {
			b.Queue(q, r.Server, r.Dim, r.ID, r.Name, r.Type, r.Color, r.Owner, r.Deleted, r.SeenAt)
		}
		return tx.SendBatch(ctx, &b).Close()
	})
	if err != nil {
		return fmt.Errorf("state/postgres: put analytics labels: %w", err)
	}
	return nil
}

// GetLabels returns the stored labels of a server's dimension for ids.
func (a analytics) GetLabels(ctx context.Context, server, dim string, ids []string) ([]state.AnalyticsLabel, error) {
	if err := storeutil.CheckLabelIDs(ids); err != nil {
		return nil, fmt.Errorf("state/postgres: get analytics labels: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := a.s.pool.Query(ctx,
		`SELECT id, name, type, color, owner, deleted, seen_at FROM analytics_labels
		 WHERE server = $1 AND dim = $2 AND id = ANY($3)`, server, dim, ids)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: get analytics labels: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (state.AnalyticsLabel, error) {
		l := state.AnalyticsLabel{Server: server, Dim: dim}
		var seen int64
		err := row.Scan(&l.ID, &l.Name, &l.Type, &l.Color, &l.Owner, &l.Deleted, &seen)
		l.SeenAt = storeutil.FromNanos(seen)
		return l, err
	})
	if err != nil {
		return nil, fmt.Errorf("state/postgres: get analytics labels: %w", err)
	}
	return emptyNil(out), nil
}

// PruneLabels deletes the labels of a server seen before before and those whose ID
// is no point's dimension any more.
func (a analytics) PruneLabels(ctx context.Context, server string, before time.Time) (int64, error) {
	b, err := toNanos("analytics labels before", before)
	if err != nil {
		return 0, err
	}
	tag, err := a.s.pool.Exec(ctx,
		`DELETE FROM analytics_labels l WHERE l.server = $1 AND (l.seen_at < $2
		   OR NOT EXISTS (SELECT 1 FROM analytics_points p WHERE p.server = $1 AND p.dim = l.id))`,
		server, b)
	if err != nil {
		return 0, fmt.Errorf("state/postgres: prune analytics labels: %w", err)
	}
	return tag.RowsAffected(), nil
}
