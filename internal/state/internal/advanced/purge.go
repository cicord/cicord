package advanced

import (
	"context"
	"errors"
	"strings"

	"github.com/cicord/cicord/internal/state"
)

var scopeTables = []struct{ table, column string }{
	{"kv", "Records"}, {"rankings", "Rankings"}, {"plugin_index_docs", "Index"}, {"tasks", "Tasks"},
}

// PluginScopes lists every plugin scope ("plugin:<bot>/<instance>/<server>") of
// one server that still holds rows, installed or not. The receiver's own scope
// is not used: like PurgeTasks it is a server-wide query.
func (s Space) PluginScopes(ctx context.Context, server string) ([]state.ScopeUsage, error) {
	if server == "" || strings.Contains(server, "/") {
		return nil, errors.New("invalid server")
	}
	byScope := map[string]*state.ScopeUsage{}
	var order []string
	for _, t := range scopeTables {
		err := s.countScopes(ctx, t.table, server, func(scope string, n int) {
			u := byScope[scope]
			if u == nil {
				u = &state.ScopeUsage{Scope: scope}
				byScope[scope] = u
				order = append(order, scope)
			}
			switch t.column {
			case "Records":
				u.Records = n
			case "Rankings":
				u.Rankings = n
			case "Index":
				u.Index = n
			case "Tasks":
				u.Tasks = n
			}
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]state.ScopeUsage, 0, len(order))
	for _, scope := range order {
		out = append(out, *byScope[scope])
	}
	return out, nil
}

func (s Space) countScopes(ctx context.Context, table, server string, visit func(string, int)) error {
	rows, err := s.Read.QueryContext(ctx, s.query("SELECT scope,COUNT(*) FROM "+table+" WHERE substr(scope,1,7)='plugin:' AND substr(scope,length(scope)-length(?1))='/' || ?1 GROUP BY scope ORDER BY scope"), server)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		var n int
		if err := rows.Scan(&scope, &n); err != nil {
			return err
		}
		visit(scope, n)
	}
	return rows.Err()
}

// PurgeScope deletes every row of the receiver's plugin scope (records, rankings,
// index documents, pending and dead tasks and event deliveries) in one
// transaction. The storage and index usage counters are maintained by triggers.
// extra lists other KV scopes of the same installation (for example its DM
// receipts) that are deleted in the same transaction.
func (s Space) PurgeScope(ctx context.Context, extra ...string) (state.ScopeUsage, error) {
	if !strings.HasPrefix(s.Scope, "plugin:") || strings.Count(s.Scope, "/") != 2 {
		return state.ScopeUsage{}, errors.New("not a plugin installation scope")
	}
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return state.ScopeUsage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	u := state.ScopeUsage{Scope: s.Scope}
	for _, t := range scopeTables {
		res, err := tx.ExecContext(ctx, s.query("DELETE FROM "+t.table+" WHERE scope=?1"), s.Scope)
		if err != nil {
			return state.ScopeUsage{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return state.ScopeUsage{}, err
		}
		switch t.column {
		case "Records":
			u.Records = int(n)
		case "Rankings":
			u.Rankings = int(n)
		case "Index":
			u.Index = int(n)
		case "Tasks":
			u.Tasks = int(n)
		}
	}
	if !s.Postgres {
		// SQLite maps each index to its full-text owner token (no member data).
		if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_index_owners WHERE scope=?1", s.Scope); err != nil {
			return state.ScopeUsage{}, err
		}
	}
	for _, scope := range extra {
		if scope == "" || strings.HasPrefix(scope, "plugin:") {
			return state.ScopeUsage{}, errors.New("invalid companion scope")
		}
		res, err := tx.ExecContext(ctx, s.query("DELETE FROM kv WHERE scope=?1"), scope)
		if err != nil {
			return state.ScopeUsage{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return state.ScopeUsage{}, err
		}
		u.Records += int(n)
	}
	return u, tx.Commit()
}

var _ state.ScopePurger = Space{}
