package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// openAtVersion builds a database at an earlier schema version, as an older
// binary of this branch left it.
func openAtVersion(t *testing.T, version int) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	ms, err := loadMigrations()
	must(t, err)
	_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`)
	must(t, err)
	for _, m := range ms {
		if m.Version > version {
			break
		}
		_, err := db.Exec(m.SQL)
		must(t, err)
		_, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, m.Version, m.Name, time.Now().UnixNano())
		must(t, err)
	}
	return db, path
}

// 0.2.0-rc.1 (schema 10) upgrades to byte quotas, unmaterialized rankings and
// owner-filtered search without losing ranks, usage or documents.
func TestUpgradeStorageFromRC1(t *testing.T) {
	db, path := openAtVersion(t, 10)
	const scope = "plugin:b/levels/s"
	for _, q := range []string{
		`INSERT INTO kv(scope,key,value,version) VALUES('plugin:b/levels/s','profile','"ééé"','legacy'),('plugin:b/levels/s','n','12','legacy'),('core','x','1','legacy')`,
		`INSERT INTO rankings(scope,name,member,score,position) VALUES('plugin:b/levels/s','xp','alice',30,1),('plugin:b/levels/s','xp','bob',20,2),('plugin:b/levels/s','xp','carol',20,3),('plugin:b/levels/s','week','bob',1,1)`,
		`INSERT INTO plugin_index_docs(scope,name,id,lang,title,body,tags,meta,weight) VALUES('plugin:b/levels/s','faq','1','en','Rules','Be kind to everyone','[]','null',100),('plugin:b/other/s','faq','2','en','Rules','Be kind always','[]','null',100)`,
	} {
		_, err := db.Exec(q)
		must(t, err)
	}
	must(t, db.Close())

	s := mustOpen(t, path)
	var entries, bytes int64
	must(t, s.r.QueryRow(`SELECT entries, bytes FROM plugin_storage_usage WHERE scope=?`, scope).Scan(&entries, &bytes))
	// "profile" + `"ééé"` (8 bytes) + "n" + "12", then 4 members: name+member+16 each.
	wantBytes := int64(len("profile")+len(`"ééé"`)+len("n")+len("12")) + int64(len("xpalice")+len("xpbob")+len("xpcarol")+len("weekbob")+4*16)
	if entries != 6 || bytes != wantBytes {
		t.Fatalf("usage after upgrade: %d entries, %d bytes; want 6, %d", entries, bytes, wantBytes)
	}
	kv := s.KV(scope).(state.AdvancedKV)
	for member, want := range map[string]int{"alice": 1, "bob": 2, "carol": 3} {
		r, err := kv.Rank(ctx(t), state.RankOp{Name: "xp", Member: member, Method: "rank"})
		must(t, err)
		if r != want {
			t.Errorf("rank of %s after upgrade: %v, want %d", member, r, want)
		}
	}
	n, err := kv.Rank(ctx(t), state.RankOp{Name: "xp", Method: "count"})
	must(t, err)
	if n != 3 {
		t.Fatalf("count after upgrade: %v", n)
	}
	hits, err := s.Index(scope).Search(ctx(t), "faq", "en", "kind", state.IndexQuery{})
	must(t, err)
	if len(hits) != 1 || hits[0].ID != "1" || hits[0].Score <= 0 {
		t.Fatalf("search after upgrade: %+v", hits)
	}
	// New writes keep the counters exact.
	must(t, kv.Set(ctx(t), "profile", json.RawMessage(`"x"`), 0))
	must(t, s.r.QueryRow(`SELECT bytes FROM plugin_storage_usage WHERE scope=?`, scope).Scan(&bytes))
	if bytes != wantBytes-int64(len(`"ééé"`)-len(`"x"`)) {
		t.Fatalf("bytes after an update: %d", bytes)
	}
}

// The v0.1.11 fixture also gets its byte usage (5 keys "kN" of value "1").
func TestUpgradeFromV0111CountsBytes(t *testing.T) {
	s := mustOpen(t, loadV0111(t))
	var bytes int64
	must(t, s.r.QueryRow(`SELECT bytes FROM plugin_storage_usage WHERE scope=?`, v0111Scope).Scan(&bytes))
	if bytes != 15 {
		t.Fatalf("bytes %d, want 15", bytes)
	}
}

// MJ1: moving a member to the top rewrote every intervening row under the
// writer lock (an atomic of 128 top inserts on 100,000 members took 21.6 s).
// A ranking write now changes a constant number of rows.
func TestRankWriteTouchesConstantRows(t *testing.T) {
	s := mustOpen(t, ":memory:")
	_, err := s.w.ExecContext(ctx(t), `WITH RECURSIVE members(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM members WHERE n<100000) INSERT INTO rankings(scope,name,member,score) SELECT 'plugin:b/levels/s','xp',CAST(n AS TEXT),100000-n FROM members`)
	must(t, err)
	kv := s.KV("plugin:b/levels/s").(state.AdvancedKV)
	changes := func() int64 {
		var n int64
		must(t, s.w.QueryRowContext(ctx(t), `SELECT total_changes()`).Scan(&n))
		return n
	}
	for _, op := range []state.RankOp{
		{Name: "xp", Member: "99999", Method: "set", Score: 1e9},  // bottom to top
		{Name: "xp", Member: "new", Method: "set", Score: 2e9},    // new top
		{Name: "xp", Member: "1", Method: "remove"},               // remove the old top
		{Name: "xp", Member: "50000", Method: "add", Score: -1e6}, // middle to bottom
	} {
		before := changes()
		_, err := kv.Rank(ctx(t), op)
		must(t, err)
		// The member row, its board size and the installation usage, plus the
		// connection's own statements; never the intervening members.
		if d := changes() - before; d > 10 {
			t.Fatalf("%s %s changed %d rows", op.Method, op.Member, d)
		}
	}
	ops := make([]state.KVMutation, 128)
	for i := range ops {
		ops[i] = state.KVMutation{Op: "rankSet", Name: "xp", Key: fmt.Sprintf("top%03d", i), Score: 3e9 + float64(i)}
	}
	start := time.Now()
	_, err = kv.Atomic(ctx(t), nil, ops)
	must(t, err)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("128 top inserts took %s", took)
	}
	for member, want := range map[string]int{"top127": 1, "top000": 128, "new": 129, "99999": 130, "2": 131, "50000": 100128} {
		r, err := kv.Rank(ctx(t), state.RankOp{Name: "xp", Member: member, Method: "rank"})
		must(t, err)
		if r != want {
			t.Errorf("rank of %s: %v, want %d", member, r, want)
		}
	}
}
