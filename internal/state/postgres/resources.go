package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/state"
)

type resourceMap struct{ s *Store }

const resourceCols = `server, kind, key, id, owner, updated_at`

func scanResource(row pgx.Row) (state.Resource, error) {
	var r state.Resource
	var kind string
	var updated int64
	if err := row.Scan(&r.Server, &kind, &r.Key, &r.ID, &r.Owner, &updated); err != nil {
		return state.Resource{}, err
	}
	r.Kind = state.ResourceKind(kind)
	r.UpdatedAt = fromNanos(updated)
	return r, nil
}

func (m resourceMap) Get(ctx context.Context, server string, kind state.ResourceKind, key string) (state.Resource, error) {
	r, err := scanResource(m.s.pool.QueryRow(ctx,
		`SELECT `+resourceCols+` FROM resources WHERE server = $1 AND kind = $2 AND key = $3`,
		server, string(kind), key))
	if errors.Is(err, pgx.ErrNoRows) {
		return state.Resource{}, notFound("resource %s/%s/%s", server, kind, key)
	}
	if err != nil {
		return state.Resource{}, fmt.Errorf("state/postgres: get resource: %w", err)
	}
	return r, nil
}

// GetByID returns the resource of server mapped to Discord ID id. Should several
// keys map to the same ID, the first by (kind, key) is returned.
func (m resourceMap) GetByID(ctx context.Context, server, id string) (state.Resource, error) {
	r, err := scanResource(m.s.pool.QueryRow(ctx,
		`SELECT `+resourceCols+` FROM resources WHERE server = $1 AND id = $2 ORDER BY kind, key LIMIT 1`,
		server, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return state.Resource{}, notFound("resource %s id %s", server, id)
	}
	if err != nil {
		return state.Resource{}, fmt.Errorf("state/postgres: get resource by id: %w", err)
	}
	return r, nil
}

// Put inserts or replaces the resource identified by (Server, Kind, Key). A
// zero UpdatedAt is set to the store's current time.
func (m resourceMap) Put(ctx context.Context, r state.Resource) error {
	switch {
	case r.Server == "":
		return errors.New("state/postgres: put resource: empty server")
	case r.Kind == "":
		return errors.New("state/postgres: put resource: empty kind")
	case r.Key == "":
		return errors.New("state/postgres: put resource: empty key")
	case r.ID == "":
		return errors.New("state/postgres: put resource: empty id")
	}
	updated, err := toNanos("UpdatedAt", orNow(r.UpdatedAt, m.s.now))
	if err != nil {
		return err
	}
	_, err = m.s.pool.Exec(ctx,
		`INSERT INTO resources (`+resourceCols+`) VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (server, kind, key) DO UPDATE SET
		   id = EXCLUDED.id, owner = EXCLUDED.owner, updated_at = EXCLUDED.updated_at`,
		r.Server, string(r.Kind), r.Key, r.ID, r.Owner, updated)
	if err != nil {
		return fmt.Errorf("state/postgres: put resource: %w", err)
	}
	return nil
}

// Delete removes a resource; deleting a missing resource is not an error.
func (m resourceMap) Delete(ctx context.Context, server string, kind state.ResourceKind, key string) error {
	_, err := m.s.pool.Exec(ctx,
		`DELETE FROM resources WHERE server = $1 AND kind = $2 AND key = $3`, server, string(kind), key)
	if err != nil {
		return fmt.Errorf("state/postgres: delete resource: %w", err)
	}
	return nil
}

func (m resourceMap) List(ctx context.Context, server string) ([]state.Resource, error) {
	rows, err := m.s.pool.Query(ctx,
		`SELECT `+resourceCols+` FROM resources WHERE server = $1 ORDER BY kind, key`, server)
	if err != nil {
		return nil, fmt.Errorf("state/postgres: list resources: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (state.Resource, error) { return scanResource(row) })
	if err != nil {
		return nil, fmt.Errorf("state/postgres: list resources: %w", err)
	}
	return emptyNil(out), nil
}
