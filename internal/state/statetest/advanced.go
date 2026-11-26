package statetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

func testAdvanced(t *testing.T, b Backend) {
	st, clock := open(t, b)
	ctx := t.Context()
	kv := st.KV("plugin:a/one/s").(state.AdvancedKV)
	_, err := kv.Atomic(ctx, nil, []state.KVMutation{{Op: "set", Key: "counter", Value: json.RawMessage("0"), TTL: time.Hour}, {Op: "set", Key: "n", Value: json.RawMessage("null")}})
	must(t, err)
	entries, err := kv.Entries(ctx, []string{"counter", "n", "missing", "counter"})
	must(t, err)
	if entries[0] == nil || entries[0].Version == "" || string(entries[1].Value) != "null" || entries[2] != nil || entries[3].Version != entries[0].Version {
		t.Fatalf("entries %#v", entries)
	}
	var wg sync.WaitGroup
	var winners atomic.Int32
	for range 12 {
		wg.Go(func() {
			res, err := kv.Atomic(ctx, []state.KVCheck{{Key: "counter", Version: &entries[0].Version}}, []state.KVMutation{{Op: "increment", Key: "counter", By: 1}, {Op: "rankSet", Name: "xp", Key: "alice", Score: 1}})
			if err != nil {
				t.Error(err)
			}
			if res.OK {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("CAS winners %d", winners.Load())
	}
	_, err = kv.Atomic(ctx, nil, []state.KVMutation{{Op: "delete", Key: "counter"}, {Op: "rankRemove", Name: "xp", Key: "alice"}, {Op: "increment", Key: "n", By: 1}})
	if err == nil {
		t.Fatal("bad transaction committed")
	}
	r, err := kv.Rank(ctx, state.RankOp{Name: "xp", Member: "alice", Method: "rank"})
	must(t, err)
	if r != 1 {
		t.Fatalf("rank after rollback %v", r)
	}
	clock.Advance(time.Hour)
	got, err := kv.Entries(ctx, []string{"counter"})
	must(t, err)
	if got[0] != nil {
		t.Fatal("increment lost TTL")
	}
	must(t, kv.Set(ctx, "counter", json.RawMessage("0"), 0))
	got, err = kv.Entries(ctx, []string{"counter"})
	must(t, err)
	if got[0].Version == entries[0].Version {
		t.Fatal("ABA")
	}
	for i := range 80 {
		_, err := kv.Rank(ctx, state.RankOp{Name: "board", Member: fmt.Sprintf("m%02d", i), Method: "set", Score: float64((i * 7) % 9)})
		must(t, err)
	}
	for i := range 20 {
		_, err := kv.Rank(ctx, state.RankOp{Name: "board", Member: fmt.Sprintf("m%02d", i), Method: "add", Score: 14})
		must(t, err)
	}
	_, err = kv.Rank(ctx, state.RankOp{Name: "board", Member: "m03", Method: "remove"})
	must(t, err)
	all, err := kv.Rank(ctx, state.RankOp{Name: "board", Method: "top", Limit: 100})
	must(t, err)
	rows := all.([]state.RankedMember)
	if len(rows) != 79 {
		t.Fatalf("rows %d", len(rows))
	}
	for i, r := range rows {
		if r.Rank != i+1 || (i > 0 && (rows[i-1].Score < r.Score || rows[i-1].Score == r.Score && rows[i-1].Member >= r.Member)) {
			t.Fatalf("incorrect order at %d: %v", i, rows)
		}
	}
	for _, op := range []state.RankOp{{Name: "board", Method: "top", Offset: 55, Limit: 5}, {Name: "board", Member: rows[57].Member, Method: "around", Radius: 2}} {
		page, err := kv.Rank(ctx, op)
		must(t, err)
		if !slices.Equal(page.([]state.RankedMember), rows[55:60]) {
			t.Fatal("incorrect rank window")
		}
	}
	err = st.(state.AtomicScope).WithAtomicScope(ctx, "plugin:a/one/s", func(scoped state.KV) error {
		return errors.Join(scoped.Set(ctx, "rolledback", json.RawMessage("true"), 0), errors.New("fail action"))
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	if _, err = kv.Get(ctx, "rolledback"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	type buffering interface {
		BufferedScope(context.Context, string) (state.KV, func(context.Context, state.KV) error)
	}
	buffered, flush := st.(buffering).BufferedScope(ctx, "plugin:a/one/s")
	table := buffered.(state.AdvancedKV)
	must(t, table.Set(ctx, "temporary", json.RawMessage("4"), 0))
	snap, err := table.Entries(ctx, []string{"temporary"})
	must(t, err)
	if snap[0] == nil {
		t.Fatal("buffer did not see its own set")
	}
	res, err := table.Atomic(ctx, []state.KVCheck{{Key: "temporary", Version: &snap[0].Version}}, []state.KVMutation{{Op: "rankSet", Name: "action", Key: "a", Score: 8}})
	must(t, err)
	if !res.OK {
		t.Fatal("self-CAS failed")
	}
	if _, err := kv.Get(ctx, "temporary"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("action leaked uncommitted write")
	}
	must(t, st.(state.AtomicScope).WithAtomicScope(ctx, "plugin:a/one/s", func(tx state.KV) error { return flush(ctx, tx) }))
	x, err := kv.Rank(ctx, state.RankOp{Name: "action", Member: "a", Method: "score"})
	must(t, err)
	if x != float64(8) {
		t.Fatal(x)
	}
	buffered, flush = st.(buffering).BufferedScope(ctx, "plugin:a/one/s")
	_, err = buffered.Get(ctx, "temporary")
	must(t, err)
	must(t, kv.Set(ctx, "temporary", json.RawMessage("7"), 0))
	if err := st.(state.AtomicScope).WithAtomicScope(ctx, "plugin:a/one/s", func(tx state.KV) error { return flush(ctx, tx) }); err == nil {
		t.Fatal("expected read conflict")
	}

	type private interface {
		VisitPrivacy(context.Context, func(string, json.RawMessage) error) error
		PurgeTasks(context.Context, string, bool, func(json.RawMessage) bool) (int, error)
	}
	scope := st.KV("plugin:bot/privacy/server").(private)
	for _, task := range []state.Task{{ID: "a", Scope: "plugin:bot/privacy/server", Name: "remind", Payload: json.RawMessage(`"A"`), RunAt: clock.Now()}, {ID: "b", Scope: "plugin:bot/removed/server", Name: "remind", Payload: json.RawMessage(`"B"`), RunAt: clock.Now()}, {ID: "other", Scope: "plugin:bot/privacy/other", Name: "remind", Payload: json.RawMessage(`"A"`), RunAt: clock.Now()}} {
		must(t, st.Tasks().Schedule(ctx, task))
	}
	seen := false
	must(t, scope.VisitPrivacy(ctx, func(k string, v json.RawMessage) error {
		if k == "remind" && string(v) == `"A"` {
			seen = true
		}
		return nil
	}))
	if !seen {
		t.Fatal("missing task on restore scan")
	}
	count, err := scope.PurgeTasks(ctx, "server", false, func(v json.RawMessage) bool { return string(v) == `"A"` })
	must(t, err)
	if count != 1 {
		t.Fatal(count)
	}
	remaining, err := st.Tasks().Due(ctx, clock.Now(), 10)
	must(t, err)
	if len(remaining) != 2 {
		t.Fatal(remaining)
	}

}

// StorageQuota checks the boundary without allocating 1,000,000 records in CI.
// Backends seed the usage counter at one remaining slot before calling it.
func StorageQuota(t *testing.T, kv state.AdvancedKV) {
	t.Helper()
	ctx := t.Context()
	must(t, kv.Set(ctx, "last-slot", json.RawMessage("1"), 0))
	must(t, kv.Set(ctx, "last-slot", json.RawMessage("2"), 0))
	_, err := kv.Atomic(ctx, nil, []state.KVMutation{{Op: "set", Key: "last-slot", Value: json.RawMessage("3")}, {Op: "rankSet", Name: "xp", Key: "overflow", Score: 1}})
	if err == nil {
		t.Fatal("expected quota error")
	}
	raw, err := kv.Get(ctx, "last-slot")
	must(t, err)
	if string(raw) != "2" {
		t.Fatal("quota failure leaked a write")
	}
	must(t, kv.Delete(ctx, "last-slot"))
	_, err = kv.Rank(ctx, state.RankOp{Name: "xp", Member: "new", Method: "set", Score: 1})
	must(t, err)
}

// RankAt100k exercises random-access ranks and deep pages through the real store.
func RankAt100k(t *testing.T, kv state.AdvancedKV) {
	t.Helper()
	times := make([]time.Duration, 100)
	for i := range times {
		position := 1 + (i*38971)%100000
		member := strconv.Itoa(position)
		start := time.Now()
		r, err := kv.Rank(t.Context(), state.RankOp{Name: "xp", Member: member, Method: "rank"})
		must(t, err)
		if r != position {
			t.Fatal(member, r)
		}
		rows, err := kv.Rank(t.Context(), state.RankOp{Name: "xp", Method: "top", Limit: 10, Offset: position - 1})
		must(t, err)
		if rows.([]state.RankedMember)[0].Member != member {
			t.Fatal("deep page missed member")
		}
		times[i] = time.Since(start)
	}
	slices.Sort(times)
	t.Logf("100,000 members, exact rank + 10-entry arbitrary-offset page, 100 samples: p95 %s", times[94])
	if times[94] >= time.Second {
		t.Fatalf("rank and page p95 %s exceeds 1s", times[94])
	}
}
