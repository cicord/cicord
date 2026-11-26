package state

import "context"

// ScopeUsage counts the rows one plugin installation scope holds in each table.
type ScopeUsage struct {
	Scope    string `json:"scope"`
	Records  int    `json:"records"`
	Rankings int    `json:"rankings"`
	Index    int    `json:"index"`
	Tasks    int    `json:"tasks"`
}

// Total is the number of rows the scope holds.
func (u ScopeUsage) Total() int { return u.Records + u.Rankings + u.Index + u.Tasks }

// ScopePurger is implemented by the KV of the SQL backends. It lets the runtime
// find and remove the data of plugin installations that are no longer in the
// manifest (uninstalled instances keep their data until an admin purges it).
type ScopePurger interface {
	// PluginScopes lists every plugin scope of one server that still holds rows.
	// It is a server-wide query: the receiver's own scope is not used.
	PluginScopes(ctx context.Context, server string) ([]ScopeUsage, error)
	// PurgeScope deletes every row of the receiver's plugin scope, plus the KV
	// rows of the companion scopes given (same transaction).
	PurgeScope(ctx context.Context, companions ...string) (ScopeUsage, error)
}
