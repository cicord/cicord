package sqlite

import (
	"fmt"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/statetest"
	"testing"
)

func TestPluginStorageQuota(t *testing.T) {
	s := mustOpen(t, ":memory:")
	_, err := s.w.ExecContext(t.Context(), "INSERT INTO plugin_storage_usage(scope,entries) VALUES('plugin:b/i/s',999999)")
	must(t, err)
	statetest.StorageQuota(t, s.KV("plugin:b/i/s").(state.AdvancedKV))
}

func TestRankAt100k(t *testing.T) {
	s := mustOpen(t, ":memory:")
	_, err := s.w.ExecContext(t.Context(), `WITH RECURSIVE members(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM members WHERE n<100000) INSERT INTO rankings(scope,name,member,score) SELECT 'plugin:b/levels/s','xp',CAST(n AS TEXT),100000-n FROM members`)
	must(t, err)
	statetest.RankAt100k(t, s.KV("plugin:b/levels/s").(state.AdvancedKV))
}

func TestPluginStorageByteQuota(t *testing.T) {
	s := mustOpen(t, ":memory:")
	_, err := s.w.ExecContext(t.Context(), fmt.Sprintf("INSERT INTO plugin_storage_usage(scope,entries,bytes) VALUES('plugin:b/i/s',0,%d)", state.PluginStorageBytes-100))
	must(t, err)
	statetest.StorageByteQuota(t, s.KV("plugin:b/i/s").(state.AdvancedKV))
}

func TestRankingMemberCap(t *testing.T) {
	s := mustOpen(t, ":memory:")
	_, err := s.w.ExecContext(t.Context(), fmt.Sprintf("INSERT INTO ranking_sizes(scope,name,members) VALUES('plugin:b/i/s','xp',%d)", state.MaxRankingMembers-1))
	must(t, err)
	statetest.RankingMemberCap(t, s.KV("plugin:b/i/s").(state.AdvancedKV))
}
