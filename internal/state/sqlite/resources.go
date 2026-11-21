package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/npjonath/codi/internal/state"
)

type resourceMap struct{ s *Store }

const resourceCols = `server, kind, key, id, owner, updated_at`

func scanResource(sc interface{ Scan(...any) error }) (state.Resource, error) {
	var r state.Resource
	var kind string
	var updated int64
	if err := sc.Scan(&r.Server, &kind, &r.Key, &r.ID, &r.Owner, &updated); err != nil {
		return state.Resource{}, err
	}
	r.Kind = state.ResourceKind(kind)
	r.UpdatedAt = fromNanos(updated)
	return r, nil
}

func (m resourceMap) Get(ctx context.Context, server string, kind state.ResourceKind, key string) (state.Resource, error) {
	row := m.s.r.QueryRowContext(ctx,
		`SELECT `+resourceCols+` FROM resources WHERE server = ? AND kind = ? AND key = ?`,
		server, string(kind), key)
	r, err := scanResource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Resource{}, notFound("resource %s/%s/%s", server, kind, key)
	}
	if err != nil {
		return state.Resource{}, fmt.Errorf("state/sqlite: get resource: %w", err)
	}
	return r, nil
}

// GetByID returns the resource of server mapped to Discord ID id. Should several
// keys map to the same ID, the first by (kind, key) is returned.
func (m resourceMap) GetByID(ctx context.Context, server, id string) (state.Resource, error) {
	row := m.s.r.QueryRowContext(ctx,
		`SELECT `+resourceCols+` FROM resources WHERE server = ? AND id = ? ORDER BY kind, key LIMIT 1`,
		server, id)
	r, err := scanResource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Resource{}, notFound("resource %s id %s", server, id)
	}
	if err != nil {
		return state.Resource{}, fmt.Errorf("state/sqlite: get resource by id: %w", err)
	}
	return r, nil
}

// Put inserts or replaces the resource identified by (Server, Kind, Key). A
// zero UpdatedAt is set to the store's current time.
func (m resourceMap) Put(ctx context.Context, r state.Resource) error {
	switch {
	case r.Server == "":
		return errors.New("state/sqlite: put resource: empty server")
	case r.Kind == "":
		return errors.New("state/sqlite: put resource: empty kind")
	case r.Key == "":
		return errors.New("state/sqlite: put resource: empty key")
	case r.ID == "":
		return errors.New("state/sqlite: put resource: empty id")
	}
	updated, err := toNanos("UpdatedAt", orNow(r.UpdatedAt, m.s.now))
	if err != nil {
		return err
	}
	_, err = m.s.w.ExecContext(ctx,
		`INSERT INTO resources (`+resourceCols+`) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (server, kind, key) DO UPDATE SET
		   id = excluded.id, owner = excluded.owner, updated_at = excluded.updated_at`,
		r.Server, string(r.Kind), r.Key, r.ID, r.Owner, updated)
	if err != nil {
		return fmt.Errorf("state/sqlite: put resource: %w", err)
	}
	return nil
}

// Delete removes a resource; deleting a missing resource is not an error.
func (m resourceMap) Delete(ctx context.Context, server string, kind state.ResourceKind, key string) error {
	_, err := m.s.w.ExecContext(ctx,
		`DELETE FROM resources WHERE server = ? AND kind = ? AND key = ?`, server, string(kind), key)
	if err != nil {
		return fmt.Errorf("state/sqlite: delete resource: %w", err)
	}
	return nil
}

func (m resourceMap) List(ctx context.Context, server string) ([]state.Resource, error) {
	rows, err := m.s.r.QueryContext(ctx,
		`SELECT `+resourceCols+` FROM resources WHERE server = ? ORDER BY kind, key`, server)
	if err != nil {
		return nil, fmt.Errorf("state/sqlite: list resources: %w", err)
	}
	defer rows.Close()
	var out []state.Resource
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, fmt.Errorf("state/sqlite: list resources: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state/sqlite: list resources: %w", err)
	}
	return out, nil
}
