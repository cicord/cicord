package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

// kvSpace is the KV space of one scope.
//
// Semantics beyond the state.KV contract:
//   - keys must be non-empty (so the "" cursor/next is unambiguous);
//   - values must be valid JSON; ttl < 0 is rejected, ttl 0 means no expiry;
//   - expired keys are invisible to Get, List and Increment, are deleted lazily
//     by Get, and are reclaimed in bulk by Sweep;
//   - Increment on a live key keeps its TTL; on a missing or expired key it
//     starts from 0 with no TTL. The stored value must be a JSON number.
type kvSpace struct {
	s     *Store
	scope string
}

func (k kvSpace) Get(ctx context.Context, key string) (json.RawMessage, error) {
	var value string
	var expires sql.NullInt64
	err := k.s.r.QueryRowContext(ctx,
		`SELECT value, expires_at FROM kv WHERE scope = ? AND key = ?`, k.scope, key).Scan(&value, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("kv %s/%s", k.scope, key)
	}
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: kv get: %w", err)
	}
	if now := k.s.now().UnixNano(); expires.Valid && expires.Int64 <= now {
		// Lazy cleanup; the expiry guard keeps a concurrent fresh Set intact.
		_, _ = k.s.w.ExecContext(ctx,
			`DELETE FROM kv WHERE scope = ? AND key = ? AND expires_at IS NOT NULL AND expires_at <= ?`,
			k.scope, key, now)
		return nil, notFound("kv %s/%s", k.scope, key)
	}
	return json.RawMessage(value), nil
}

func (k kvSpace) Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error {
	if err := storeutil.CheckKVSet(key, value, ttl); err != nil {
		return fmt.Errorf("state/sqlite: kv set %s/%s: %w", k.scope, key, err)
	}
	var expires any
	if ttl > 0 {
		n, err := toNanos("expiry", k.s.now().Add(ttl))
		if err != nil {
			return err
		}
		expires = n
	}
	_, err := k.s.w.ExecContext(ctx,
		`INSERT INTO kv (scope, key, value, expires_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (scope, key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
		k.scope, key, string(value), expires)
	if err != nil {
		return fmt.Errorf("state/sqlite: kv set: %w", err)
	}
	return nil
}

// Delete removes key; deleting a missing key is not an error.
func (k kvSpace) Delete(ctx context.Context, key string) error {
	if _, err := k.s.w.ExecContext(ctx, `DELETE FROM kv WHERE scope = ? AND key = ?`, k.scope, key); err != nil {
		return fmt.Errorf("state/sqlite: kv delete: %w", err)
	}
	return nil
}

func (k kvSpace) Increment(ctx context.Context, key string, by float64) (_ float64, err error) {
	if err := storeutil.CheckIncrement(key, by); err != nil {
		return 0, fmt.Errorf("state/sqlite: kv increment %s/%s: %w", k.scope, key, err)
	}

	// The writer pool has a single connection and transactions are BEGIN
	// IMMEDIATE, so this read-modify-write is atomic against every other writer.
	tx, err := k.s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: kv increment: %w", err)
	}
	defer rollback(tx, &err)

	now := k.s.now().UnixNano()
	var raw []byte
	var expires sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT value, expires_at FROM kv WHERE scope = ? AND key = ?`, k.scope, key).Scan(&raw, &expires)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		raw = nil
	case err != nil:
		return 0, fmt.Errorf("state/sqlite: kv increment: %w", err)
	case expires.Valid && expires.Int64 <= now:
		raw, expires = nil, sql.NullInt64{} // expired: start over without TTL
	}

	result, encoded, err := storeutil.AddNumber(raw, by)
	if err != nil {
		return 0, fmt.Errorf("state/sqlite: kv increment %s/%s: %w", k.scope, key, err)
	}
	var expArg any
	if expires.Valid {
		expArg = expires.Int64
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO kv (scope, key, value, expires_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (scope, key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
		k.scope, key, string(encoded), expArg); err != nil {
		return 0, fmt.Errorf("state/sqlite: kv increment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("state/sqlite: kv increment: %w", err)
	}
	return result, nil
}

// List returns live items whose key starts with prefix, ordered by key (byte
// order), strictly after cursor. limit <= 0 means 100; it is capped at 1000.
// The returned cursor is the last returned key, or "" when nothing remains.
func (k kvSpace) List(ctx context.Context, prefix string, limit int, cursor string) ([]state.KVItem, string, error) {
	limit = clampLimit(limit, state.DefaultKVListLimit)

	// Prefix match as a byte range [prefix, prefixEnd) rather than LIKE, which
	// treats % and _ as wildcards and folds ASCII case.
	q := `SELECT key, value FROM kv
	      WHERE scope = ? AND key >= ? AND key > ? AND (expires_at IS NULL OR expires_at > ?)`
	args := []any{k.scope, prefix, cursor, k.s.now().UnixNano()}
	if end, ok := storeutil.PrefixEnd(prefix); ok {
		q += ` AND key < ?`
		args = append(args, end)
	}
	q += ` ORDER BY key LIMIT ?`
	args = append(args, limit+1)

	rows, err := k.s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("state/sqlite: kv list: %w", err)
	}
	defer rows.Close()
	items := make([]state.KVItem, 0, min(limit, 64))
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, "", fmt.Errorf("state/sqlite: kv list: %w", err)
		}
		items = append(items, state.KVItem{Key: key, Value: json.RawMessage(value)})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("state/sqlite: kv list: %w", err)
	}
	var next string
	if len(items) > limit {
		items = items[:limit]
		next = items[limit-1].Key
	}
	return items, next, nil
}
