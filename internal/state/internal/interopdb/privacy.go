package interopdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (s Store) textColumn(column string) string {
	if s.Postgres {
		return "convert_from(" + column + ",'UTF8')"
	}
	return column
}
func (s Store) DeadEvent(ctx context.Context, server, id string) (json.RawMessage, error) {
	var data []byte
	err := s.DB.QueryRowContext(ctx, s.q(`SELECT payload FROM tasks WHERE id=$1 AND substr(scope,length(scope)-length($2))='/' || $2 AND event_seq IS NOT NULL AND dead`), id, server).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return data, err
}
func (s Store) PurgeInteropMember(ctx context.Context, server, userID string) (int, error) {
	if server == "" || userID == "" || strings.Trim(userID, "0123456789") != "" {
		return 0, errors.New("invalid server or member")
	}
	// Keep a tombstone until the original receipt expires. Reusing an erased result
	// must never cause an already completed effect to execute for a second time.
	pattern := "%" + userID + "%"
	r, err := s.DB.ExecContext(ctx, s.q("UPDATE interop_actions SET status='erased',result=NULL WHERE server=$1 AND "+s.textColumn("result")+" LIKE $2"), server, pattern)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	count := int(n)
	after := ""
	for {
		batch, err := s.memberBatch(ctx, server, pattern, after)
		if err != nil {
			return count, err
		}
		if len(batch) == 0 {
			return count, nil
		}
		for _, v := range batch {
			body, keep := withoutMember(v.raw, userID)
			var result sql.Result
			if keep {
				result, err = s.DB.ExecContext(ctx, s.q(`UPDATE tasks SET payload=$1,receipt='',available_at=NULL WHERE id=$2 AND payload=$3`), s.blob(body), v.id, s.blob(v.raw))
			} else {
				result, err = s.DB.ExecContext(ctx, s.q(`DELETE FROM tasks WHERE id=$1 AND payload=$2`), v.id, s.blob(v.raw))
			}
			if err != nil {
				return count, err
			}
			removed, _ := result.RowsAffected()
			count += int(removed)
			after = v.id
		}
	}
}

// Shared notification lists retain the other participants. If a member is the
// subject of the envelope, or appears elsewhere in the payload (including free
// text), discard that delivery instead of replaying private data.
func withoutMember(raw []byte, id string) ([]byte, bool) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, false
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(envelope["payload"], &body) != nil {
		return nil, false
	}
	for _, key := range []string{"voters", "attendees", "participants", "winners", "userIds", "entrants"} {
		var entries []json.RawMessage
		if v := body[key]; v == nil || json.Unmarshal(v, &entries) != nil || entries == nil {
			continue
		}
		filtered := entries[:0]
		for _, entry := range entries {
			if !strings.Contains(string(entry), id) {
				filtered = append(filtered, entry)
			}
		}
		if len(filtered) != len(entries) {
			body[key], _ = json.Marshal(filtered)
		}
	}
	envelope["payload"], _ = json.Marshal(body)
	result, err := json.Marshal(envelope)
	return result, err == nil && !strings.Contains(string(result), id)
}

type eventData struct {
	id  string
	raw []byte
}

func (s Store) memberBatch(ctx context.Context, server, pattern, after string) ([]eventData, error) {
	rows, err := s.DB.QueryContext(ctx, s.q("SELECT id,payload FROM tasks WHERE substr(scope,length(scope)-length($1))='/' || $1 AND event_seq IS NOT NULL AND "+s.textColumn("payload")+" LIKE $2 AND id>$3 ORDER BY id LIMIT 16"), server, pattern, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batch []eventData
	for rows.Next() {
		var v eventData
		if err := rows.Scan(&v.id, &v.raw); err != nil {
			return nil, err
		}
		batch = append(batch, v)
	}
	return batch, rows.Err()
}
