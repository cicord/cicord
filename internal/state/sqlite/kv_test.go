package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/state"
)

func TestKVBasics(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, _ *fakeClock) {
		c := ctx(t)
		kv := s.KV("plugin:bot/xp/main")

		if _, err := kv.Get(c, "missing"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("Get missing: err=%v, want ErrNotFound", err)
		}
		for _, v := range []string{`{"level":3,"tags":["a","b"]}`, `"text"`, `12.5`, `null`, `[]`, `true`} {
			must(t, kv.Set(c, "k", json.RawMessage(v), 0))
			got, err := kv.Get(c, "k")
			must(t, err)
			if string(got) != v {
				t.Errorf("Get = %s, want %s", got, v)
			}
		}

		// Scopes are isolated.
		other := s.KV("plugin:bot/xp/other")
		if _, err := other.Get(c, "k"); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("key visible from another scope: err=%v", err)
		}
		must(t, other.Set(c, "k", json.RawMessage(`"other"`), 0))
		if got, _ := kv.Get(c, "k"); string(got) != `true` {
			t.Errorf("other scope overwrote value: %s", got)
		}

		must(t, kv.Delete(c, "k"))
		must(t, kv.Delete(c, "k")) // idempotent
		if _, err := kv.Get(c, "k"); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("Get after Delete: err=%v", err)
		}
		if got, _ := other.Get(c, "k"); string(got) != `"other"` {
			t.Errorf("Delete leaked across scopes: %s", got)
		}
	})
}

func TestKVSetValidation(t *testing.T) {
	s := mustOpen(t, ":memory:")
	kv := s.KV("s")
	c := ctx(t)
	for _, v := range []string{``, `{`, `undefined`, `{"a":}`, `1 2`} {
		if err := kv.Set(c, "k", json.RawMessage(v), 0); err == nil {
			t.Errorf("Set(%q) succeeded", v)
		}
	}
	if err := kv.Set(c, "", json.RawMessage(`1`), 0); err == nil {
		t.Error("Set with empty key succeeded")
	}
	if err := kv.Set(c, "k", json.RawMessage(`1`), -time.Second); err == nil {
		t.Error("Set with negative ttl succeeded")
	}
}

func TestKVTTL(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		c := ctx(t)
		kv := s.KV("s")
		must(t, kv.Set(c, "short", json.RawMessage(`1`), time.Minute))
		must(t, kv.Set(c, "long", json.RawMessage(`2`), time.Hour))
		must(t, kv.Set(c, "forever", json.RawMessage(`3`), 0))

		clk.Advance(time.Minute - time.Nanosecond)
		if _, err := kv.Get(c, "short"); err != nil {
			t.Fatalf("key expired early: %v", err)
		}
		clk.Advance(time.Nanosecond) // exactly at expiry: gone
		if _, err := kv.Get(c, "short"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("expired Get: err=%v, want ErrNotFound", err)
		}
		// Lazy cleanup removed the row.
		var n int
		must(t, s.r.QueryRow(`SELECT COUNT(*) FROM kv WHERE key = 'short'`).Scan(&n))
		if n != 0 {
			t.Errorf("expired row not lazily deleted")
		}

		items, _, err := kv.List(c, "", 0, "")
		must(t, err)
		if keys := itemKeys(items); fmt.Sprint(keys) != "[forever long]" {
			t.Errorf("List after expiry = %v", keys)
		}

		// Set without TTL clears a previous TTL.
		must(t, kv.Set(c, "long", json.RawMessage(`4`), 0))
		clk.Advance(2 * time.Hour)
		if v, err := kv.Get(c, "long"); err != nil || string(v) != "4" {
			t.Errorf("re-Set key without TTL: %s, %v", v, err)
		}

		// Sweep removes expired KV rows and sessions only.
		must(t, kv.Set(c, "a", json.RawMessage(`1`), time.Second))
		must(t, kv.Set(c, "b", json.RawMessage(`1`), time.Second))
		must(t, s.Sessions().Put(c, state.Session{ID: "sess", UserID: "u", ExpiresAt: clk.Now().Add(time.Second)}))
		must(t, s.Sessions().Put(c, state.Session{ID: "live", UserID: "u", ExpiresAt: clk.Now().Add(time.Hour)}))
		clk.Advance(time.Second)
		removed, err := s.Sweep(c)
		must(t, err)
		if removed != 3 {
			t.Errorf("Sweep removed %d rows, want 3", removed)
		}
		must(t, s.r.QueryRow(`SELECT COUNT(*) FROM kv`).Scan(&n))
		if n != 2 { // forever, long
			t.Errorf("%d kv rows left after sweep, want 2", n)
		}
		if _, err := s.Sessions().Get(c, "live"); err != nil {
			t.Errorf("Sweep removed live session: %v", err)
		}
		if removed, _ := s.Sweep(c); removed != 0 {
			t.Errorf("second Sweep removed %d rows", removed)
		}
	})
}

func TestKVIncrement(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		c := ctx(t)
		kv := s.KV("s")

		v, err := kv.Increment(c, "n", 1)
		must(t, err)
		if v != 1 {
			t.Errorf("Increment missing = %v, want 1", v)
		}
		v, err = kv.Increment(c, "n", 2.5)
		must(t, err)
		if v != 3.5 {
			t.Errorf("Increment = %v, want 3.5", v)
		}
		v, err = kv.Increment(c, "n", -4)
		must(t, err)
		if v != -0.5 {
			t.Errorf("Increment = %v, want -0.5", v)
		}
		raw, err := kv.Get(c, "n")
		must(t, err)
		if string(raw) != "-0.5" {
			t.Errorf("stored value = %s", raw)
		}

		// Integers stay plain JSON integers.
		must(t, kv.Set(c, "i", json.RawMessage(`41`), 0))
		v, err = kv.Increment(c, "i", 1)
		must(t, err)
		if raw, _ := kv.Get(c, "i"); v != 42 || string(raw) != "42" {
			t.Errorf("Increment(41,1) = %v stored %s", v, raw)
		}

		// Non-numeric values are an error and are left untouched.
		for _, bad := range []string{`"5"`, `null`, `{"n":1}`, `[1]`, `true`} {
			must(t, kv.Set(c, "bad", json.RawMessage(bad), 0))
			if _, err := kv.Increment(c, "bad", 1); err == nil {
				t.Errorf("Increment on %s succeeded", bad)
			}
			if raw, _ := kv.Get(c, "bad"); string(raw) != bad {
				t.Errorf("failed Increment changed value to %s", raw)
			}
		}

		// Invalid deltas and non-finite results.
		for _, by := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			if _, err := kv.Increment(c, "n", by); err == nil {
				t.Errorf("Increment by %v succeeded", by)
			}
		}
		must(t, kv.Set(c, "big", json.RawMessage(`1.7e308`), 0))
		if _, err := kv.Increment(c, "big", 1.7e308); err == nil {
			t.Error("Increment overflowing to +Inf succeeded")
		}

		// A live key keeps its TTL.
		must(t, kv.Set(c, "ttl", json.RawMessage(`10`), time.Minute))
		clk.Advance(30 * time.Second)
		v, err = kv.Increment(c, "ttl", 1)
		must(t, err)
		if v != 11 {
			t.Errorf("Increment live TTL key = %v", v)
		}
		clk.Advance(30 * time.Second)
		if _, err := kv.Get(c, "ttl"); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("Increment extended the TTL: err=%v", err)
		}
		// An expired key counts as 0 and the new value has no TTL.
		v, err = kv.Increment(c, "ttl", 5)
		must(t, err)
		if v != 5 {
			t.Errorf("Increment expired key = %v, want 5", v)
		}
		clk.Advance(24 * time.Hour)
		if raw, err := kv.Get(c, "ttl"); err != nil || string(raw) != "5" {
			t.Errorf("revived key: %s, %v", raw, err)
		}
		// An expired non-numeric value is also treated as missing.
		must(t, kv.Set(c, "oldstr", json.RawMessage(`"x"`), time.Second))
		clk.Advance(time.Second)
		if v, err := kv.Increment(c, "oldstr", 2); err != nil || v != 2 {
			t.Errorf("Increment expired non-numeric = %v, %v", v, err)
		}
	})
}

func TestKVIncrementConcurrent(t *testing.T) {
	const goroutines, perG = 32, 40
	eachBackend(t, func(t *testing.T, s *Store, _ *fakeClock) {
		c := ctx(t)
		var wg sync.WaitGroup
		errs := make(chan error, goroutines)
		for g := range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				kv := s.KV("plugin:bot/counter/main")
				for i := range perG {
					if _, err := kv.Increment(c, "hits", 1); err != nil {
						errs <- err
						return
					}
					// Interleave reads and unrelated writes.
					if i%5 == 0 {
						if _, err := kv.Get(c, "hits"); err != nil {
							errs <- err
							return
						}
						if err := kv.Set(c, fmt.Sprintf("g%d", g), json.RawMessage(`1`), 0); err != nil {
							errs <- err
							return
						}
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		raw, err := s.KV("plugin:bot/counter/main").Get(c, "hits")
		must(t, err)
		if want := fmt.Sprint(goroutines * perG); string(raw) != want {
			t.Fatalf("hits = %s, want %s (lost updates)", raw, want)
		}
	})
}

// Two Store handles on the same file contend like two processes would (separate
// writer connections), exercising BEGIN IMMEDIATE + busy_timeout.
func TestKVIncrementConcurrentAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	stores := []*Store{mustOpen(t, path), mustOpen(t, path), mustOpen(t, path)}
	const perG = 60
	var wg sync.WaitGroup
	errs := make(chan error, len(stores)*2)
	for _, s := range stores {
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range perG {
					if _, err := s.KV("s").Increment(ctx(t), "n", 1); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	raw, err := stores[0].KV("s").Get(ctx(t), "n")
	must(t, err)
	if want := fmt.Sprint(len(stores) * 2 * perG); string(raw) != want {
		t.Fatalf("n = %s, want %s", raw, want)
	}
}

func TestKVListPrefixAndOrder(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, _ *fakeClock) {
		c := ctx(t)
		kv := s.KV("s")
		keys := []string{
			"user:1", "user:10", "user:2", "user_1", "user%1", "User:3", "users", "use",
			"a%b", "a%c", "aXb", "a_b", "abb",
			"\xff\xffz", "\xff\xff", "\xff\xfe", "é:1", "é:2", "e:1",
		}
		for _, k := range keys {
			must(t, kv.Set(c, k, json.RawMessage(`0`), 0))
		}
		must(t, s.KV("other").Set(c, "user:999", json.RawMessage(`0`), 0))

		cases := []struct {
			prefix string
			want   []string
		}{
			{"user:", []string{"user:1", "user:10", "user:2"}},
			{"user", []string{"user%1", "user:1", "user:10", "user:2", "user_1", "users"}},
			{"user_", []string{"user_1"}},  // _ is literal
			{"user%", []string{"user%1"}},  // % is literal
			{"a%", []string{"a%b", "a%c"}}, // not a wildcard
			{"a_", []string{"a_b"}},        // not a single-char wildcard
			{"User", []string{"User:3"}},   // case-sensitive
			{"\xff\xff", []string{"\xff\xff", "\xff\xffz"}},
			{"\xff", []string{"\xff\xfe", "\xff\xff", "\xff\xffz"}},
			{"é", []string{"é:1", "é:2"}},
			{"zzz", nil},
		}
		for _, tc := range cases {
			items, next, err := kv.List(c, tc.prefix, 0, "")
			must(t, err)
			if got := itemKeys(items); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", tc.want) {
				t.Errorf("List(%q) = %q, want %q", tc.prefix, got, tc.want)
			}
			if next != "" {
				t.Errorf("List(%q) next = %q, want \"\"", tc.prefix, next)
			}
		}

		// Empty prefix lists the whole scope in byte order.
		items, _, err := kv.List(c, "", 0, "")
		must(t, err)
		want := append([]string(nil), keys...)
		sort.Strings(want)
		if got := itemKeys(items); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
			t.Errorf("List(\"\") = %q, want %q", got, want)
		}
	})
}

func TestKVListPagination(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *Store, clk *fakeClock) {
		c := ctx(t)
		kv := s.KV("s")
		var want []string
		for i := range 25 {
			k := fmt.Sprintf("item:%03d", i)
			want = append(want, k)
			must(t, kv.Set(c, k, json.RawMessage(fmt.Sprint(i)), 0))
		}
		must(t, kv.Set(c, "other:1", json.RawMessage(`1`), 0))
		must(t, kv.Set(c, "item:010x", json.RawMessage(`1`), time.Second)) // expires below
		clk.Advance(time.Second)

		for _, limit := range []int{1, 7, 10, 24, 25, 26} {
			var got []string
			cursor, pages := "", 0
			for {
				items, next, err := kv.List(c, "item:", limit, cursor)
				must(t, err)
				pages++
				if len(items) > limit {
					t.Fatalf("limit %d: page of %d items", limit, len(items))
				}
				got = append(got, itemKeys(items)...)
				if next == "" {
					break
				}
				if next != items[len(items)-1].Key {
					t.Fatalf("next %q is not the last key %q", next, items[len(items)-1].Key)
				}
				cursor = next
				if pages > 100 {
					t.Fatal("pagination does not terminate")
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("limit %d: walked %v, want %v", limit, got, want)
			}
			if wantPages := (25 + limit - 1) / limit; pages != wantPages {
				t.Errorf("limit %d: %d pages, want %d (next must be \"\" exactly at the end)", limit, pages, wantPages)
			}
		}

		// Values come back with the items.
		items, _, err := kv.List(c, "item:00", 3, "item:000")
		must(t, err)
		if len(items) != 3 || items[0].Key != "item:001" || string(items[0].Value) != "1" {
			t.Errorf("List after cursor = %+v", items)
		}
	})
}

func TestKVListLimits(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "state.db"))
	c := ctx(t)
	kv := s.KV("s")
	// Bulk insert directly for speed.
	tx, err := s.w.Begin()
	must(t, err)
	for i := range 1100 {
		_, err := tx.Exec(`INSERT INTO kv (scope, key, value) VALUES ('s', ?, '0')`, fmt.Sprintf("k%05d", i))
		must(t, err)
	}
	must(t, tx.Commit())

	items, next, err := kv.List(c, "", 0, "")
	must(t, err)
	if len(items) != defaultKVListLimit || next != "k00099" {
		t.Errorf("default limit: %d items, next %q", len(items), next)
	}
	items, next, err = kv.List(c, "", 5000, "")
	must(t, err)
	if len(items) != maxListLimit || next != "k00999" {
		t.Errorf("max limit: %d items, next %q", len(items), next)
	}
	items, next, err = kv.List(c, "", 5000, next)
	must(t, err)
	if len(items) != 100 || next != "" {
		t.Errorf("last page: %d items, next %q", len(items), next)
	}
}

func TestPrefixEnd(t *testing.T) {
	for _, tc := range []struct {
		in, out string
		ok      bool
	}{
		{"", "", false},
		{"a", "b", true},
		{"ab", "ac", true},
		{"a\xff", "b", true},
		{"\xff", "", false},
		{"\xff\xff", "", false},
		{"a\xff\xff", "b", true},
	} {
		out, ok := prefixEnd(tc.in)
		if out != tc.out || ok != tc.ok {
			t.Errorf("prefixEnd(%q) = %q, %v; want %q, %v", tc.in, out, ok, tc.out, tc.ok)
		}
	}
}

func itemKeys(items []state.KVItem) []string {
	var keys []string
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	return keys
}
