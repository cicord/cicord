package postgres

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/internal/storeutil"
)

// 0.2.0-rc.1 (schema 10) upgrades to byte quotas and unmaterialized rankings
// without losing ranks or usage.
func TestUpgradeStorageFromRC1(t *testing.T) {
	conn := newSchema(t)
	ms, err := storeutil.LoadMigrations(migrationFS, "migrations")
	must(t, err)
	var b strings.Builder
	b.WriteString("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at BIGINT NOT NULL);\n")
	for _, m := range ms {
		if m.Version > 10 {
			break
		}
		b.WriteString(m.SQL + ";\n")
		fmt.Fprintf(&b, "INSERT INTO schema_migrations VALUES(%d,'%s',1);\n", m.Version, m.Name)
	}
	b.WriteString(`INSERT INTO kv(scope,key,value,version) VALUES('plugin:b/levels/s','profile',convert_to('"ééé"','UTF8'),'legacy'),('plugin:b/levels/s','n','12','legacy'),('core','x','1','legacy');
INSERT INTO rankings(scope,name,member,score,position) VALUES('plugin:b/levels/s','xp','alice',30,1),('plugin:b/levels/s','xp','bob',20,2),('plugin:b/levels/s','xp','carol',20,3),('plugin:b/levels/s','week','bob',1,1);`)
	execRaw(t, conn, b.String())

	s := mustOpen(t, conn)
	const scope = "plugin:b/levels/s"
	var entries, bytes int64
	must(t, s.pool.QueryRow(t.Context(), `SELECT entries, bytes FROM plugin_storage_usage WHERE scope=$1`, scope).Scan(&entries, &bytes))
	wantBytes := int64(len("profile")+len(`"ééé"`)+len("n")+len("12")) + int64(len("xpalice")+len("xpbob")+len("xpcarol")+len("weekbob")+4*16)
	if entries != 6 || bytes != wantBytes {
		t.Fatalf("usage after upgrade: %d entries, %d bytes; want 6, %d", entries, bytes, wantBytes)
	}
	kv := s.KV(scope).(state.AdvancedKV)
	for member, want := range map[string]int{"alice": 1, "bob": 2, "carol": 3} {
		r, err := kv.Rank(t.Context(), state.RankOp{Name: "xp", Member: member, Method: "rank"})
		must(t, err)
		if r != want {
			t.Errorf("rank of %s after upgrade: %v, want %d", member, r, want)
		}
	}
	n, err := kv.Rank(t.Context(), state.RankOp{Name: "xp", Method: "count"})
	must(t, err)
	if n != 3 {
		t.Fatalf("count after upgrade: %v", n)
	}
	// An upsert that rewrites a value fires the UPDATE trigger only.
	must(t, kv.Set(t.Context(), "profile", json.RawMessage(`"x"`), 0))
	must(t, kv.Set(t.Context(), "fresh", json.RawMessage(`1`), 0))
	_, err = kv.Atomic(t.Context(), nil, []state.KVMutation{{Op: "rankRemove", Name: "week", Key: "bob"}})
	must(t, err)
	must(t, s.pool.QueryRow(t.Context(), `SELECT entries, bytes FROM plugin_storage_usage WHERE scope=$1`, scope).Scan(&entries, &bytes))
	want := wantBytes - int64(len(`"ééé"`)-len(`"x"`)) + int64(len("fresh1")) - int64(len("weekbob")+16)
	if entries != 6 || bytes != want {
		t.Fatalf("usage after writes: %d entries, %d bytes; want 6, %d", entries, bytes, want)
	}
	var boards int
	must(t, s.pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM ranking_sizes WHERE scope=$1`, scope).Scan(&boards))
	if boards != 1 {
		t.Fatalf("%d board sizes, want the empty board dropped", boards)
	}
}
