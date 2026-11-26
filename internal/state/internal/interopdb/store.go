// Package interopdb shares the durable delivery and action protocol between backends.
package interopdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cicord/cicord/internal/state"
	"regexp"
	"strings"
	"time"
)

type Store struct {
	DB       *sql.DB
	Postgres bool
}

const retention = 30 * 24 * time.Hour

var positional = regexp.MustCompile(`\$(\d+)`)

func (s Store) q(sql string) string {
	if s.Postgres {
		return sql
	}
	return positional.ReplaceAllString(sql, "?${1}")
}
func (s Store) blob(value []byte) any {
	if s.Postgres {
		return value
	}
	return string(value)
}
func (s Store) PublishEvent(ctx context.Context, event state.EventFanout) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.PublishEventTx(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) PublishEventTx(ctx context.Context, tx *sql.Tx, event state.EventFanout) error {
	var seq int64
	err := tx.QueryRowContext(ctx, s.q(`INSERT INTO interop_events(id,expires_at) VALUES($1,$2) ON CONFLICT(id) DO NOTHING RETURNING seq`), event.ID, event.At.Add(retention).UnixNano()).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, scope := range event.Subscribers {
		id := fmt.Sprintf("event-%x", sha256.Sum256([]byte(event.ID+"/"+scope)))
		_, err = tx.ExecContext(ctx, s.q(`INSERT INTO tasks(id,scope,name,payload,run_at,event_stream,event_seq) VALUES($1,$2,$3,$4,$5,$6,$7)`), id, scope, event.Topic, s.blob(event.Payload), event.At.UnixNano(), event.Publisher+"/"+scope, seq)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s Store) ReserveAction(ctx context.Context, id, server, digest, owner string, now time.Time) (json.RawMessage, bool, error) {
	// Recover an expired lease, or a failed handler, only for the identical caller,
	// principal and input. A response from a previous authority must never be disclosed.
	var status, hash, current string
	var result []byte
	row := s.DB.QueryRowContext(ctx, s.q(`INSERT INTO interop_actions(id,server,digest,status,owner,lease_until,expires_at)
 VALUES($1,$2,$3,'running',$4,$5,$6) ON CONFLICT(id) DO UPDATE SET
 status='running',owner=excluded.owner,lease_until=excluded.lease_until,expires_at=excluded.expires_at,digest=excluded.digest,result=NULL
 WHERE interop_actions.expires_at <= $7 OR (interop_actions.digest=excluded.digest AND interop_actions.status<>'done' AND interop_actions.lease_until <= $7)
 RETURNING status,digest,owner,result`), id, server, digest, owner, now.Add(35*time.Second).UnixNano(), now.Add(retention).UnixNano(), now.UnixNano())
	err := row.Scan(&status, &hash, &current, &result)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.DB.QueryRowContext(ctx, s.q(`SELECT status,digest,owner,result FROM interop_actions WHERE id=$1`), id).Scan(&status, &hash, &current, &result)
	}
	if err != nil {
		return nil, false, err
	}
	if hash != digest {
		return nil, false, errors.New("idempotency key belongs to a different request")
	}
	return result, current == owner && status == "running", nil
}
func (s Store) FailAction(ctx context.Context, id, owner string) error {
	_, err := s.DB.ExecContext(ctx, s.q(`UPDATE interop_actions SET status='failed',lease_until=0,owner='' WHERE id=$1 AND owner=$2`), id, owner)
	return err
}
func (s Store) CommitAction(ctx context.Context, id, owner, scope string, writes []state.KVWrite, result json.RawMessage, events ...state.EventFanout) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	for _, w := range writes {
		var old []byte
		var expiry sql.NullInt64
		key := s.blob([]byte(w.Key))
		err = tx.QueryRowContext(ctx, s.q(`SELECT value,expires_at FROM kv WHERE scope=$1 AND key=$2 AND (expires_at IS NULL OR expires_at>$3)`), scope, key, now.UnixNano()).Scan(&old, &expiry)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !bytes.Equal(old, w.Expected) {
			return errors.New("KV changed during action; retry with the same key")
		}
		if !w.Changed {
			continue
		}
		if w.Value == nil {
			_, err = tx.ExecContext(ctx, s.q(`DELETE FROM kv WHERE scope=$1 AND key=$2`), scope, key)
		} else {
			var expires any
			if w.KeepTTL && expiry.Valid {
				expires = expiry.Int64
			} else if w.TTL > 0 {
				expires = now.Add(w.TTL).UnixNano()
			}
			_, err = tx.ExecContext(ctx, s.q(`INSERT INTO kv(scope,key,value,expires_at) VALUES($1,$2,$3,$4) ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value,expires_at=excluded.expires_at`), scope, key, s.blob(w.Value), expires)
		}
		if err != nil {
			return err
		}
	}
	for _, e := range events {
		if err := s.PublishEventTx(ctx, tx, e); err != nil {
			return err
		}
	}
	r, err := tx.ExecContext(ctx, s.q(`UPDATE interop_actions SET status='done',result=$1,owner='' WHERE id=$2 AND owner=$3 AND status='running'`), s.blob(result), id, owner)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("action lease lost")
	}
	return tx.Commit()
}
func (s Store) PurgeInteropMember(ctx context.Context, server, userID string) (int, error) {
	if userID == "" || server == "" {
		return 0, errors.New("missing server or member")
	}
	// JSON string matching includes the surrounding quotes. IDs cannot match a substring
	// of another snowflake. Receipts store only the digest and public result.
	needle := fmt.Sprintf("%%%q%%", userID)
	var removed int64
	for _, table := range []string{"tasks", "interop_actions"} {
		var q string
		if table == "tasks" {
			q = `DELETE FROM tasks WHERE event_seq IS NOT NULL AND scope LIKE $1 AND CAST(payload AS TEXT) LIKE $2`
		} else {
			q = `DELETE FROM interop_actions WHERE server=$1 AND CAST(result AS TEXT) LIKE $2`
		}
		arg := server
		if table == "tasks" {
			arg = "%/" + server
		}
		if s.Postgres {
			q = strings.ReplaceAll(strings.ReplaceAll(q, "CAST(payload AS TEXT)", "convert_from(payload,'UTF8')"), "CAST(result AS TEXT)", "convert_from(result,'UTF8')")
		}
		r, err := s.DB.ExecContext(ctx, s.q(q), arg, needle)
		if err != nil {
			return int(removed), err
		}
		n, _ := r.RowsAffected()
		removed += n
	}
	return int(removed), nil
}
