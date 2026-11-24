package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

// kvSpace is the KV space of one scope (semantics: state.KV).
type kvSpace struct {
	s     *Store
	scope string
}

func (k kvSpace) Get(ctx context.Context, key string) (json.RawMessage, error) {
	var value []byte
	var expires *int64
	err := k.s.pool.QueryRow(ctx,
		`SELECT value, expires_at FROM kv WHERE scope = $1 AND key = $2`, k.scope, bytea(key)).Scan(&value, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("kv %s/%s", k.scope, key)
	}
	if err != nil {
		return nil, fmt.Errorf("state/postgres: kv get: %w", err)
	}
	if now := k.s.now().UnixNano(); expires != nil && *expires <= now {
		// Lazy cleanup; the expiry guard keeps a concurrent fresh Set intact.
		_, _ = k.s.pool.Exec(ctx,
			`DELETE FROM kv WHERE scope = $1 AND key = $2 AND expires_at IS NOT NULL AND expires_at <= $3`,
			k.scope, bytea(key), now)
		return nil, notFound("kv %s/%s", k.scope, key)
	}
	return json.RawMessage(value), nil
}

func (k kvSpace) Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error {
	if err := storeutil.CheckKVSet(key, value, ttl); err != nil {
		return fmt.Errorf("state/postgres: kv set %s/%s: %w", k.scope, key, err)
	}
	var expires any
	if ttl > 0 {
		n, err := toNanos("expiry", k.s.now().Add(ttl))
		if err != nil {
			return err
		}
		expires = n
	}
	_, err := k.s.pool.Exec(ctx,
		`INSERT INTO kv (scope, key, value, expires_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (scope, key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at`,
		k.scope, bytea(key), []byte(value), expires)
	if err != nil {
		return fmt.Errorf("state/postgres: kv set: %w", err)
	}
	return nil
}

// Delete removes key; deleting a missing key is not an error.
func (k kvSpace) Delete(ctx context.Context, key string) error {
	if _, err := k.s.pool.Exec(ctx, `DELETE FROM kv WHERE scope = $1 AND key = $2`, k.scope, bytea(key)); err != nil {
		return fmt.Errorf("state/postgres: kv delete: %w", err)
	}
	return nil
}

// maxIncrementAttempts bounds the lock-or-insert loop of Increment. Each extra
// attempt needs another transaction to insert and delete the same key between
// two of our statements, so the bound is only reached under pathological churn.
const maxIncrementAttempts = 16

// Increment is a read-modify-write in one READ COMMITTED transaction, with the
// arithmetic in Go (storeutil.AddNumber) so the stored encoding is the same as
// on SQLite:
//
//  1. SELECT … FOR UPDATE locks the row if it exists; concurrent increments of
//     the same key queue on that lock and each re-reads the committed value.
//  2. If there is no row, INSERT … ON CONFLICT DO NOTHING creates it. When a
//     concurrent transaction inserted the same key first, the INSERT waits for
//     it and then does nothing, and the loop goes back to step 1, which now
//     sees (and locks) the committed row. No update is ever lost.
func (k kvSpace) Increment(ctx context.Context, key string, by float64) (float64, error) {
	if err := storeutil.CheckIncrement(key, by); err != nil {
		return 0, fmt.Errorf("state/postgres: kv increment %s/%s: %w", k.scope, key, err)
	}
	var result float64
	err := pgx.BeginTxFunc(ctx, k.s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		now := k.s.now().UnixNano()
		for range maxIncrementAttempts {
			var raw []byte
			var expires *int64
			err := tx.QueryRow(ctx,
				`SELECT value, expires_at FROM kv WHERE scope = $1 AND key = $2 FOR UPDATE`,
				k.scope, bytea(key)).Scan(&raw, &expires)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				r, encoded, err := storeutil.AddNumber(nil, by)
				if err != nil {
					return k.incrementErr(key, err)
				}
				tag, err := tx.Exec(ctx,
					`INSERT INTO kv (scope, key, value, expires_at) VALUES ($1, $2, $3, NULL)
					 ON CONFLICT (scope, key) DO NOTHING`,
					k.scope, bytea(key), encoded)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 1 {
					result = r
					return nil
				}
				continue // inserted concurrently: lock that row
			case err != nil:
				return err
			}
			if expires != nil && *expires <= now {
				raw, expires = nil, nil // expired: start over without TTL
			}
			r, encoded, err := storeutil.AddNumber(raw, by)
			if err != nil {
				return k.incrementErr(key, err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE kv SET value = $3, expires_at = $4 WHERE scope = $1 AND key = $2`,
				k.scope, bytea(key), encoded, expires); err != nil {
				return err
			}
			result = r
			return nil
		}
		return fmt.Errorf("gave up after %d attempts (the key is being created and deleted concurrently)", maxIncrementAttempts)
	})
	if err != nil {
		var ie incrementError
		if errors.As(err, &ie) {
			return 0, ie.err
		}
		return 0, fmt.Errorf("state/postgres: kv increment: %w", err)
	}
	return result, nil
}

// incrementError carries a validation error of Increment out of the
// transaction unchanged.
type incrementError struct{ err error }

func (e incrementError) Error() string { return e.err.Error() }
func (e incrementError) Unwrap() error { return e.err }

func (k kvSpace) incrementErr(key string, err error) error {
	return incrementError{fmt.Errorf("state/postgres: kv increment %s/%s: %w", k.scope, key, err)}
}

// List returns live items whose key starts with prefix, ordered by key (byte
// order), strictly after cursor; limit <= 0 means state.DefaultKVListLimit and
// it is capped at state.MaxListLimit. The returned cursor is the last returned
// key, or "" when nothing remains.
func (k kvSpace) List(ctx context.Context, prefix string, limit int, cursor string) ([]state.KVItem, string, error) {
	limit = clampLimit(limit, state.DefaultKVListLimit)

	// Prefix match as a byte range [prefix, PrefixEnd) on BYTEA (byte order).
	var q strings.Builder
	q.WriteString(`SELECT key, value FROM kv WHERE scope = $1 AND (expires_at IS NULL OR expires_at > $2)`)
	args := []any{k.scope, k.s.now().UnixNano()}
	arg := func(cond string, v any) {
		args = append(args, v)
		q.WriteString(" AND key " + cond + " $" + strconv.Itoa(len(args)))
	}
	if prefix != "" {
		arg(">=", bytea(prefix))
	}
	if cursor != "" {
		arg(">", bytea(cursor))
	}
	if end, ok := storeutil.PrefixEnd(prefix); ok {
		arg("<", bytea(end))
	}
	args = append(args, limit+1)
	q.WriteString(" ORDER BY key LIMIT $" + strconv.Itoa(len(args)))

	rows, err := k.s.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("state/postgres: kv list: %w", err)
	}
	defer rows.Close()
	items := make([]state.KVItem, 0, min(limit, 64))
	for rows.Next() {
		var key, value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, "", fmt.Errorf("state/postgres: kv list: %w", err)
		}
		items = append(items, state.KVItem{Key: string(key), Value: json.RawMessage(value)})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("state/postgres: kv list: %w", err)
	}
	var next string
	if len(items) > limit {
		items = items[:limit]
		next = items[limit-1].Key
	}
	return items, next, nil
}
