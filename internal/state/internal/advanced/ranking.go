package advanced

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/cicord/cicord/internal/state"
)

// Rankings keep no materialized positions: a write touches one row and its
// indexes, whatever the move (review finding MJ1). A rank is counted on read
// over the score-ordered index (scope, name, score DESC, member), and the board
// size is capped at state.MaxRankingMembers, which bounds that count and deep
// pages. Order: higher score first, ties by member in byte order.
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
		score, ok, err := s.memberScore(ctx, s.reader(), op.Name, op.Member)
		if err != nil || !ok {
			return nil, err
		}
		if op.Method == "score" {
			return score, nil
		}
		return s.rankOf(ctx, op.Name, op.Member, score)
	case "count":
		defer s.readLock()()
		var n int
		err := s.reader().QueryRowContext(ctx, s.query("SELECT members FROM ranking_sizes WHERE scope=?1 AND name=?2"), s.Scope, op.Name).Scan(&n)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return n, err
	case "top", "around":
		defer s.readLock()()
		if op.Limit < 0 || op.Limit > 1000 || op.Offset < 0 || op.Offset > math.MaxInt32 || op.Radius < 0 || op.Radius > 499 {
			return nil, errors.New("ranking: invalid page")
		}
		if op.Method == "top" {
			if op.Offset >= state.MaxRankingMembers {
				return []state.RankedMember{}, nil
			}
			return s.rankRows(ctx, op.Offset+1, false, "SELECT member,score FROM rankings WHERE scope=?1 AND name=?2 ORDER BY score DESC,member ASC LIMIT ?3 OFFSET ?4", s.Scope, op.Name, op.Limit, op.Offset)
		}
		return s.around(ctx, op)
	default:
		return nil, errors.New("ranking: unknown operation")
	}
}

func (s Space) memberScore(ctx context.Context, r runner, name, member string) (float64, bool, error) {
	var score float64
	err := r.QueryRowContext(ctx, s.query("SELECT score FROM rankings WHERE scope=?1 AND name=?2 AND member=?3"), s.Scope, name, member).Scan(&score)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return score, err == nil, err
}

// rankOf counts the members ahead: higher scores, then equal scores with a
// smaller member. Two index range counts, each bounded by the board cap.
func (s Space) rankOf(ctx context.Context, name, member string, score float64) (int, error) {
	var above, ties int
	r := s.reader()
	if err := r.QueryRowContext(ctx, s.query("SELECT COUNT(*) FROM rankings WHERE scope=?1 AND name=?2 AND score>?3"), s.Scope, name, score).Scan(&above); err != nil {
		return 0, err
	}
	if err := r.QueryRowContext(ctx, s.query("SELECT COUNT(*) FROM rankings WHERE scope=?1 AND name=?2 AND score=?3 AND member<?4"), s.Scope, name, score, member).Scan(&ties); err != nil {
		return 0, err
	}
	return above + ties + 1, nil
}

// rankRows reads ordered members. first is the rank of the first row read; with
// reverse, rows come in decreasing rank order (first is the rank of the first one).
func (s Space) rankRows(ctx context.Context, first int, reverse bool, q string, args ...any) ([]state.RankedMember, error) {
	rows, err := s.reader().QueryContext(ctx, s.query(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []state.RankedMember{}
	for rows.Next() {
		e := state.RankedMember{Rank: first}
		if err := rows.Scan(&e.Member, &e.Score); err != nil {
			return nil, err
		}
		entries = append(entries, e)
		if reverse {
			first--
		} else {
			first++
		}
	}
	return entries, rows.Err()
}

// around returns up to Radius members on each side of the member, in rank order.
// Each side is two index seeks: equal scores past the member, then the
// neighbouring scores.
func (s Space) around(ctx context.Context, op state.RankOp) ([]state.RankedMember, error) {
	score, ok, err := s.memberScore(ctx, s.reader(), op.Name, op.Member)
	if err != nil || !ok {
		return []state.RankedMember{}, err
	}
	rank, err := s.rankOf(ctx, op.Name, op.Member, score)
	if err != nil {
		return nil, err
	}
	n := op.Radius
	before, err := s.rankRows(ctx, rank-1, true, "SELECT member,score FROM rankings WHERE scope=?1 AND name=?2 AND score=?3 AND member<?4 ORDER BY member DESC LIMIT ?5", s.Scope, op.Name, score, op.Member, n)
	if err != nil {
		return nil, err
	}
	if len(before) < n {
		more, err := s.rankRows(ctx, rank-1-len(before), true, "SELECT member,score FROM rankings WHERE scope=?1 AND name=?2 AND score>?3 ORDER BY score ASC,member DESC LIMIT ?4", s.Scope, op.Name, score, n-len(before))
		if err != nil {
			return nil, err
		}
		before = append(before, more...)
	}
	after, err := s.rankRows(ctx, rank+1, false, "SELECT member,score FROM rankings WHERE scope=?1 AND name=?2 AND score=?3 AND member>?4 ORDER BY member ASC LIMIT ?5", s.Scope, op.Name, score, op.Member, n)
	if err != nil {
		return nil, err
	}
	if len(after) < n {
		more, err := s.rankRows(ctx, rank+1+len(after), false, "SELECT member,score FROM rankings WHERE scope=?1 AND name=?2 AND score<?3 ORDER BY score DESC,member ASC LIMIT ?4", s.Scope, op.Name, score, n-len(after))
		if err != nil {
			return nil, err
		}
		after = append(after, more...)
	}
	out := make([]state.RankedMember, 0, len(before)+1+len(after))
	for i := len(before) - 1; i >= 0; i-- {
		out = append(out, before[i])
	}
	out = append(out, state.RankedMember{Member: op.Member, Score: score, Rank: rank})
	return append(out, after...), nil
}

// changeRank writes one row. The size and storage triggers enforce the board
// cap and the installation quota in the same statement.
func (s Space) changeRank(ctx context.Context, op state.RankOp) (any, error) {
	if op.Name == "" || op.Member == "" || len(op.Name) > 512 || len(op.Member) > 512 {
		return nil, errors.New("ranking: invalid name or member")
	}
	args := []any{s.Scope, op.Name, op.Member}
	if op.Method == "remove" {
		_, err := s.tx.ExecContext(ctx, s.query("DELETE FROM rankings WHERE scope=?1 AND name=?2 AND member=?3"), args...)
		return nil, err
	}
	score, exists, err := s.memberScore(ctx, s.tx, op.Name, op.Member)
	if err != nil {
		return nil, err
	}
	if op.Method == "add" {
		op.Score += score
	}
	if math.IsNaN(op.Score) || math.IsInf(op.Score, 0) {
		return nil, errors.New("ranking: score must be finite")
	}
	if exists && score == op.Score {
		return score, nil
	}
	_, err = s.tx.ExecContext(ctx, s.query("INSERT INTO rankings(scope,name,member,score) VALUES(?1,?2,?3,?4) ON CONFLICT(scope,name,member) DO UPDATE SET score=excluded.score"), s.Scope, op.Name, op.Member, op.Score)
	return op.Score, err
}
