package advanced

import (
	"context"
	"database/sql"
	"encoding/json"
)

// visitRecords streams a pair of columns without retaining member data after the callback.
func visitRecords(ctx context.Context, db runner, q string, args []any, visit func(string, json.RawMessage) error) error {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		if err := visit(key, value); err != nil {
			return err
		}
	}
	return rows.Err()
}

// VisitPrivacy walks one plugin scope, including subjects embedded in values,
// ranking names and members, and both pending and dead task payloads.
func (s Space) VisitPrivacy(ctx context.Context, visit func(string, json.RawMessage) error) error {
	for _, q := range []string{
		"SELECT key,value FROM kv WHERE scope=?1", "SELECT member,NULL FROM rankings WHERE scope=?1",
		"SELECT DISTINCT name,NULL FROM rankings WHERE scope=?1", "SELECT id,tags FROM plugin_index_docs WHERE scope=?1", "SELECT name,payload FROM tasks WHERE scope=?1",
	} {
		if err := visitRecords(ctx, s.Read, s.query(q), []any{s.Scope}, visit); err != nil {
			return err
		}
	}
	return nil
}

func visitTaskPage(ctx context.Context, tx *sql.Tx, query string, server, cursor string, visit func(string, string, json.RawMessage)) error {
	rows, err := tx.QueryContext(ctx, query, server, cursor)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, scope string
		var raw []byte
		if err := rows.Scan(&id, &scope, &raw); err != nil {
			return err
		}
		visit(id, scope, raw)
	}
	return rows.Err()
}

// PurgeTasks deletes matching live, leased and dead tasks across a server,
// including instances removed from the current manifest. Inter-plugin events
// have their own eraser that preserves unaffected members on participant pages.
func (s Space) PurgeTasks(ctx context.Context, server string, separateEvents bool, matches func(string, json.RawMessage) bool) (int, error) {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	ordinary := ""
	if separateEvents {
		ordinary = " AND event_seq IS NULL"
	}
	selectPage := s.query("SELECT id,scope,payload FROM tasks WHERE substr(scope,1,7)='plugin:' AND substr(scope,length(scope)-length(?1))='/' || ?1 AND id>?2" + ordinary + " ORDER BY id LIMIT 128")
	remove := s.query("DELETE FROM tasks WHERE id=?1 AND payload=?2 AND scope=?3" + ordinary)
	var count int
	var cursor string
	for {
		type match struct {
			id, scope string
			raw       json.RawMessage
		}
		var pending []match
		n := 0
		err := visitTaskPage(ctx, tx, selectPage, server, cursor, func(id, scope string, raw json.RawMessage) {
			n++
			cursor = id
			if len(raw) > 0 && matches(scope, raw) {
				pending = append(pending, match{id, scope, raw})
			}
		})
		if err != nil {
			return 0, err
		}
		for _, p := range pending {
			result, err := tx.ExecContext(ctx, remove, p.id, s.bytes(string(p.raw)), p.scope)
			if err != nil {
				return 0, err
			}
			removed, err := result.RowsAffected()
			if err != nil {
				return 0, err
			}
			count += int(removed)
		}
		if n < 128 {
			break
		}
	}
	return count, tx.Commit()
}
