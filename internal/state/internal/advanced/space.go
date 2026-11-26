// Package advanced implements versioned KV and ordered rankings over the two SQL drivers.
package advanced

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

type runner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Space struct {
	Read, Write *sql.DB
	Scope       string
	Postgres    bool
	Now         func() time.Time
	tx          *sql.Tx
	mu          *sync.Mutex
}

func (s Space) query(q string) string {
	if s.Postgres {
		return strings.ReplaceAll(q, "?", "$")
	}
	return q
}

func (s Space) bytes(b string) any {
	if s.Postgres {
		return []byte(b)
	}
	return b
}

func (s Space) reader() runner {
	if s.tx != nil {
		return s.tx
	}
	return s.Read
}

// Transaction serializes writers of one scope, including checks of absent keys.
// SQLite opens IMMEDIATE writer transactions; PostgreSQL uses a scope advisory lock.
func (s Space) Transaction(ctx context.Context, fn func(state.KV) error) error {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.Postgres {
		if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 9351))", s.Scope); err != nil {
			return err
		}
	}
	s.tx = tx
	s.mu = new(sync.Mutex)
	if err = fn(s); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Space) write(ctx context.Context, fn func(Space) error) error {
	if s.tx == nil {
		return s.Transaction(ctx, func(k state.KV) error { return fn(k.(Space)) })
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A handler can catch a failed operation inside an enclosing action transaction.
	if _, err := s.tx.ExecContext(ctx, "SAVEPOINT kv_operation"); err != nil {
		return err
	}
	if err := fn(s); err != nil {
		if _, rollback := s.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT kv_operation"); rollback != nil {
			return errors.Join(err, rollback)
		}
		_, release := s.tx.ExecContext(ctx, "RELEASE SAVEPOINT kv_operation")
		return errors.Join(err, release)
	}
	_, err := s.tx.ExecContext(ctx, "RELEASE SAVEPOINT kv_operation")
	return err
}

func (s Space) readLock() func() {
	if s.mu == nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

func (s Space) Entries(ctx context.Context, keys []string) ([]*state.KVEntry, error) {
	defer s.readLock()()
	if len(keys) > 128 {
		return nil, errors.New("kv.getMany: at most 128 keys")
	}
	result := make([]*state.KVEntry, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	args := []any{s.Scope, s.Now().UnixNano()}
	positions := make(map[string][]int, len(keys))
	vars := make([]string, len(keys))
	for i, key := range keys {
		args = append(args, s.bytes(key))
		positions[key] = append(positions[key], i)
		vars[i] = "?" + strconv.Itoa(len(args))
	}
	q := "SELECT key,value,version FROM kv WHERE scope=?1 AND (expires_at IS NULL OR expires_at>?2) AND key IN (" + strings.Join(vars, ",") + ")"
	rows, err := s.reader().QueryContext(ctx, s.query(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var key string
		var raw []byte
		var version string
		if err := rows.Scan(&key, &raw, &version); err != nil {
			return nil, err
		}
		size += len(raw) * len(positions[key])
		if size > 4<<20 && len(keys) > 1 {
			return nil, errors.New("kv.getMany: result exceeds 4 MiB")
		}
		for _, idx := range positions[key] {
			result[idx] = &state.KVEntry{Value: raw, Version: version}
		}
	}
	return result, rows.Err()
}

func (s Space) Get(ctx context.Context, key string) (json.RawMessage, error) {
	entries, err := s.Entries(ctx, []string{key})
	if err != nil {
		return nil, err
	}
	if entries[0] == nil {
		defer s.readLock()()
		var w runner = s.Write
		if s.tx != nil {
			w = s.tx
		}
		_, _ = w.ExecContext(ctx, s.query("DELETE FROM kv WHERE scope=?1 AND key=?2 AND expires_at<=?3"), s.Scope, s.bytes(key), s.Now().UnixNano())
		return nil, state.ErrNotFound
	}
	return entries[0].Value, nil
}

func (s Space) Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error {
	_, err := s.Atomic(ctx, nil, []state.KVMutation{{Op: "set", Key: key, Value: value, TTL: ttl}})
	return err
}
func (s Space) Delete(ctx context.Context, key string) error {
	_, err := s.Atomic(ctx, nil, []state.KVMutation{{Op: "delete", Key: key}})
	return err
}
func (s Space) Increment(ctx context.Context, key string, by float64) (float64, error) {
	r, err := s.Atomic(ctx, nil, []state.KVMutation{{Op: "increment", Key: key, By: by}})
	return r.Number, err
}

func (s Space) Atomic(ctx context.Context, checks []state.KVCheck, ops []state.KVMutation) (state.KVCommit, error) {
	var r state.KVCommit
	if len(checks)+len(ops) > 128 {
		return r, errors.New("kv.atomic: at most 128 checks and mutations")
	}
	err := s.write(ctx, func(s Space) error {
		now := s.Now()
		for _, c := range checks {
			var actual string
			err := s.tx.QueryRowContext(ctx, s.query("SELECT version FROM kv WHERE scope=?1 AND key=?2 AND (expires_at IS NULL OR expires_at>?3)"), s.Scope, s.bytes(c.Key), now.UnixNano()).Scan(&actual)
			if errors.Is(err, sql.ErrNoRows) {
				if c.Version != nil {
					return nil
				}
				continue
			}
			if err != nil {
				return err
			}
			if c.Version == nil || *c.Version != actual {
				return nil
			}
		}
		for _, op := range ops {
			if op.Op == "rankSet" || op.Op == "rankRemove" {
				method := "set"
				if op.Op == "rankRemove" {
					method = "remove"
				}
				if _, err := s.changeRank(ctx, state.RankOp{Name: op.Name, Method: method, Member: op.Key, Score: op.Score}); err != nil {
					return err
				}
				continue
			}
			key := s.bytes(op.Key)
			if op.Op == "delete" {
				if _, err := s.tx.ExecContext(ctx, s.query("DELETE FROM kv WHERE scope=?1 AND key=?2"), s.Scope, key); err != nil {
					return err
				}
				continue
			}
			var exp *int64
			value := op.Value
			switch op.Op {
			case "set":
				if err := storeutil.CheckKVSet(op.Key, value, op.TTL); err != nil {
					return err
				}
				if op.TTL > 0 {
					n, err := storeutil.ToNanos("expiry", now.Add(op.TTL))
					if err != nil {
						return err
					}
					exp = &n
				}
			case "increment":
				if err := storeutil.CheckIncrement(op.Key, op.By); err != nil {
					return err
				}
				var raw []byte
				err := s.tx.QueryRowContext(ctx, s.query("SELECT value,expires_at FROM kv WHERE scope=?1 AND key=?2 AND (expires_at IS NULL OR expires_at>?3)"), s.Scope, key, now.UnixNano()).Scan(&raw, &exp)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				r.Number, value, err = storeutil.AddNumber(raw, op.By)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown kv mutation %q", op.Op)
			}
			if strings.HasPrefix(s.Scope, "plugin:") && (len(op.Key) > 512 || len(value) > 1<<20) {
				return errors.New("kv: key or value exceeds quota")
			}
			version := op.VersionToken
			if version == "" {
				var token [16]byte
				rand.Read(token[:])
				version = hex.EncodeToString(token[:])
			}
			_, err := s.tx.ExecContext(ctx, s.query("INSERT INTO kv(scope,key,value,expires_at,version) VALUES(?1,?2,?3,?4,?5) ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value,expires_at=excluded.expires_at,version=excluded.version"), s.Scope, key, s.bytes(string(value)), exp, version)
			if err != nil {
				return err
			}
		}
		r.OK = true
		return nil
	})
	if err != nil {
		return state.KVCommit{}, err
	}
	return r, nil
}

func (s Space) List(ctx context.Context, prefix string, limit int, cursor string) ([]state.KVItem, string, error) {
	defer s.readLock()()
	limit = storeutil.ClampLimit(limit, state.DefaultKVListLimit)
	q := "SELECT key,value FROM kv WHERE scope=?1 AND key>=?2 AND key>?3 AND (expires_at IS NULL OR expires_at>?4)"
	args := []any{s.Scope, s.bytes(prefix), s.bytes(cursor), s.Now().UnixNano()}
	if end, ok := storeutil.PrefixEnd(prefix); ok {
		q += " AND key<?5"
		args = append(args, s.bytes(end))
	}
	args = append(args, limit+1)
	q += " ORDER BY key LIMIT ?" + strconv.Itoa(len(args))
	rows, err := s.reader().QueryContext(ctx, s.query(q), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []state.KVItem{}
	for rows.Next() {
		var item state.KVItem
		var value []byte
		if err := rows.Scan(&item.Key, &value); err != nil {
			return nil, "", err
		}
		item.Value = value
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) > limit {
		return items[:limit], items[limit-1].Key, nil
	}
	return items, "", nil
}
