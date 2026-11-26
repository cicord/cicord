package advanced

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/cicord/cicord/internal/state"
)

// Materialized positions let a page seek directly to any offset. Reordering
// only shifts the interval between the member's old and new positions.
func (s Space) Rank(ctx context.Context, op state.RankOp) (any, error) {
	if op.Name == "" || len(op.Name) > 512 {
		return nil, errors.New("ranking: name must be 1–512 bytes")
	}
	if op.Method != "top" && op.Method != "count" && (op.Member == "" || len(op.Member) > 512) {
		return nil, errors.New("ranking: member must be 1–512 bytes")
	}
	switch op.Method {
	case "set", "add", "remove":
		var result any
		err := s.write(ctx, func(s Space) error { var err error; result, err = s.changeRank(ctx, op); return err })
		return result, err
	case "score", "rank":
		defer s.readLock()()
		var e state.RankedMember
		err := s.reader().QueryRowContext(ctx, s.query("SELECT score,position FROM rankings WHERE scope=?1 AND name=?2 AND member=?3"), s.Scope, op.Name, op.Member).Scan(&e.Score, &e.Rank)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if op.Method == "score" {
			return e.Score, nil
		}
		return e.Rank, nil
	case "count":
		defer s.readLock()()
		var n int
		err := s.reader().QueryRowContext(ctx, s.query("SELECT COALESCE(MAX(position),0) FROM rankings WHERE scope=?1 AND name=?2"), s.Scope, op.Name).Scan(&n)
		return n, err
	case "top", "around":
		defer s.readLock()()
		if op.Limit < 0 || op.Limit > 1000 || op.Offset < 0 || op.Offset > math.MaxInt32 || op.Radius < 0 || op.Radius > 499 {
			return nil, errors.New("ranking: invalid page")
		}
		q := "SELECT member,score,position FROM rankings WHERE scope=?1 AND name=?2 AND position>=?3 ORDER BY position LIMIT ?4"
		args := []any{s.Scope, op.Name, op.Offset + 1, op.Limit}
		if op.Method == "around" {
			q = "SELECT r.member,r.score,r.position FROM rankings a JOIN rankings r ON r.scope=a.scope AND r.name=a.name AND r.position BETWEEN a.position-?4 AND a.position+?4 WHERE a.scope=?1 AND a.name=?2 AND a.member=?3 ORDER BY r.position"
			args = []any{s.Scope, op.Name, op.Member, op.Radius}
		}
		rows, err := s.reader().QueryContext(ctx, s.query(q), args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		entries := []state.RankedMember{}
		for rows.Next() {
			var e state.RankedMember
			if err := rows.Scan(&e.Member, &e.Score, &e.Rank); err != nil {
				return nil, err
			}
			entries = append(entries, e)
		}
		return entries, rows.Err()
	default:
		return nil, errors.New("ranking: unknown operation")
	}
}

func (s Space) changeRank(ctx context.Context, op state.RankOp) (any, error) {
	if op.Name == "" || op.Member == "" || len(op.Name) > 512 || len(op.Member) > 512 {
		return nil, errors.New("ranking: invalid name or member")
	}
	var old int
	var score float64
	args := []any{s.Scope, op.Name, op.Member}
	err := s.tx.QueryRowContext(ctx, s.query("SELECT position,score FROM rankings WHERE scope=?1 AND name=?2 AND member=?3"), args...).Scan(&old, &score)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if op.Method == "add" {
		op.Score += score
	}
	if math.IsNaN(op.Score) || math.IsInf(op.Score, 0) {
		return nil, errors.New("ranking: score must be finite")
	}
	if op.Method == "remove" {
		if old == 0 {
			return nil, nil
		}
		if _, err := s.tx.ExecContext(ctx, s.query("DELETE FROM rankings WHERE scope=?1 AND name=?2 AND member=?3"), args...); err != nil {
			return nil, err
		}
		_, err := s.tx.ExecContext(ctx, s.query("UPDATE rankings SET position=position-1 WHERE scope=?1 AND name=?2 AND position>?3"), s.Scope, op.Name, old)
		return nil, err
	}
	if old != 0 && score == op.Score {
		return score, nil
	}
	var previous int
	err = s.tx.QueryRowContext(ctx, s.query("SELECT position FROM rankings WHERE scope=?1 AND name=?2 AND member<>?3 AND (score>?4 OR (score=?4 AND member<?3)) ORDER BY score ASC,member DESC LIMIT 1"), s.Scope, op.Name, op.Member, op.Score).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	pos := previous + 1
	if old > 0 && old <= previous {
		pos--
	}
	switch {
	case old == 0:
		_, err = s.tx.ExecContext(ctx, s.query("UPDATE rankings SET position=position+1 WHERE scope=?1 AND name=?2 AND position>=?3"), s.Scope, op.Name, pos)
	case old < pos:
		_, err = s.tx.ExecContext(ctx, s.query("UPDATE rankings SET position=position-1 WHERE scope=?1 AND name=?2 AND position>?3 AND position<=?4"), s.Scope, op.Name, old, pos)
	case old > pos:
		_, err = s.tx.ExecContext(ctx, s.query("UPDATE rankings SET position=position+1 WHERE scope=?1 AND name=?2 AND position>=?3 AND position<?4"), s.Scope, op.Name, pos, old)
	default:
		err = nil
	}
	if err != nil {
		return nil, err
	}
	_, err = s.tx.ExecContext(ctx, s.query("INSERT INTO rankings(scope,name,member,score,position) VALUES(?1,?2,?3,?4,?5) ON CONFLICT(scope,name,member) DO UPDATE SET score=excluded.score,position=excluded.position"), s.Scope, op.Name, op.Member, op.Score, pos)
	return op.Score, err
}
