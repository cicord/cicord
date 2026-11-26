package postgres

import (
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/statetest"
	"testing"
)

func TestPluginStorageQuota(t *testing.T) {
	s := mustOpen(t, newSchema(t))
	_, err := s.pool.Exec(t.Context(), "INSERT INTO plugin_storage_usage(scope,entries) VALUES('plugin:b/i/s',999999)")
	must(t, err)
	statetest.StorageQuota(t, s.KV("plugin:b/i/s").(state.AdvancedKV))
}

func TestRankAt100k(t *testing.T) {
	s := mustOpen(t, newSchema(t))
	_, err := s.pool.Exec(t.Context(), `INSERT INTO rankings(scope,name,member,score,position) SELECT 'plugin:b/levels/s','xp',n::text,100000-n,n FROM generate_series(1,100000) n`)
	must(t, err)
	statetest.RankAt100k(t, s.KV("plugin:b/levels/s").(state.AdvancedKV))
}
