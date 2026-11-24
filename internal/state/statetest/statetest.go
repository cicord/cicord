// Package statetest is the conformance suite of state.Store: every backend runs
// it (state/sqlite always, state/postgres against a real server) so they keep
// exactly the same observable semantics.
//
// Usage, from a backend's tests:
//
//	func TestConformance(t *testing.T) {
//		statetest.Run(t, statetest.Backend{NewDatabase: ..., SharedHandles: true})
//	}
package statetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// Opener opens a new handle (connection pool) on one database. The store must
// use now as its only source of the current time. The backend closes the
// handle when the test ends (t.Cleanup); the suite may close it earlier, so
// Close must tolerate being called twice.
type Opener func(t *testing.T, now func() time.Time) state.Store

// Backend describes the backend under test.
type Backend struct {
	// NewDatabase prepares a fresh, empty database for one test and returns an
	// opener for it.
	NewDatabase func(t *testing.T) Opener
	// SharedHandles reports whether handles from the same Opener see the same
	// data, like two processes would (false for SQLite ":memory:"). It enables
	// the multi-handle tests.
	SharedHandles bool
}

// Run runs the whole suite as parallel subtests of t.
func Run(t *testing.T, b Backend) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(t *testing.T, b Backend)
	}{
		{"KVBasics", testKVBasics},
		{"KVValueRoundTrip", testKVValueRoundTrip},
		{"KVBinaryKeys", testKVBinaryKeys},
		{"KVSetValidation", testKVSetValidation},
		{"KVTTL", testKVTTL},
		{"KVIncrement", testKVIncrement},
		{"KVIncrementConcurrent", testKVIncrementConcurrent},
		{"KVIncrementConcurrentAcrossHandles", testKVIncrementAcrossHandles},
		{"KVListPrefixAndOrder", testKVListPrefixAndOrder},
		{"KVListPagination", testKVListPagination},
		{"KVListLimits", testKVListLimits},
		{"Resources", testResources},
		{"ResourceOrderIsByteOrder", testResourceOrder},
		{"ResourcePutValidation", testResourcePutValidation},
		{"Tasks", testTasks},
		{"TaskTiesAreByteOrder", testTaskTies},
		{"TaskValidation", testTaskValidation},
		{"TaskDueExtremeTimes", testTaskDueExtremes},
		{"AuditLog", testAuditLog},
		{"AuditDetailsValidation", testAuditDetailsValidation},
		{"SyncLog", testSyncLog},
		{"Sessions", testSessions},
		{"Sweep", testSweep},
		{"TimeRoundTripExact", testTimeRoundTripExact},
		{"OutOfRangeTimesRejected", testOutOfRangeTimes},
		{"DefaultTimestampsUseClock", testDefaultTimestamps},
		{"CanceledContext", testCanceledContext},
		{"PersistsAcrossHandles", testPersistsAcrossHandles},
		{"EmptyResults", testEmptyResults},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.fn(t, b)
		})
	}
}

// ───────────────────────────────────────────────────────────── helpers

// Clock is a manually advanced clock, safe for concurrent use.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// NewClock returns a clock set to a fixed instant with nanosecond precision.
func NewClock() *Clock {
	return &Clock{t: time.Date(2026, 3, 14, 15, 9, 26, 535897932, time.UTC)}
}

// Now returns the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// open returns a store on a fresh database with a fake clock.
func open(t *testing.T, b Backend) (state.Store, *Clock) {
	t.Helper()
	clk := NewClock()
	return b.NewDatabase(t)(t, clk.Now), clk
}

func ctx(t *testing.T) context.Context { return t.Context() }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// assertTime checks exact instant equality and UTC location.
func assertTime(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("%s location = %v, want UTC", what, got.Location())
	}
}

func sweep(t *testing.T, s state.Store) int64 {
	t.Helper()
	n, err := s.Sweep(ctx(t))
	must(t, err)
	return n
}

func itemKeys(items []state.KVItem) []string {
	var keys []string
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	return keys
}

func taskIDs(ts []state.Task) []string {
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	return ids
}

// ───────────────────────────────────────────────────────────── KV

func testKVBasics(t *testing.T, b Backend) {
	s, _ := open(t, b)
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
	if items, _, err := other.List(c, "", 0, ""); err != nil || fmt.Sprint(itemKeys(items)) != "[k]" {
		t.Errorf("other scope List = %v, %v", itemKeys(items), err)
	}

	must(t, kv.Delete(c, "k"))
	must(t, kv.Delete(c, "k")) // idempotent
	if _, err := kv.Get(c, "k"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v", err)
	}
	if got, _ := other.Get(c, "k"); string(got) != `"other"` {
		t.Errorf("Delete leaked across scopes: %s", got)
	}
}

// Values come back byte for byte: no JSON normalization (key order,
// whitespace, escapes, number formatting) and no text-encoding validation.
func testKVValueRoundTrip(t *testing.T, b Backend) {
	s, _ := open(t, b)
	c := ctx(t)
	kv := s.KV("s")
	big := make([]byte, 0, 1<<20)
	big = append(big, '"')
	for len(big) < 1<<20-1 {
		big = append(big, "0123456789abcdef"...)
	}
	big = append(big, '"')
	values := []string{
		`{"z":1,"a":2}`,
		"  {\n\t\"spaced\" :  [ 1 , 2 ]\n}  ",
		`{"dup":1,"dup":2}`,
		`1.000`, `1e2`, `-0`, `12345678901234567890123`,
		`"\u0000 nul escape"`, `"😀 surrogate escape"`, `"é 😀 raw unicode"`,
		"\"\xff\xfe invalid utf-8\"",
		`"back\\slash \"quoted\""`,
		string(big),
	}
	for i, v := range values {
		key := fmt.Sprintf("v%02d", i)
		must(t, kv.Set(c, key, json.RawMessage(v), 0))
		got, err := kv.Get(c, key)
		must(t, err)
		if string(got) != v {
			t.Errorf("value %d: Get = %q, want %q", i, truncate(got), truncate([]byte(v)))
		}
	}
	items, _, err := kv.List(c, "v", 0, "")
	must(t, err)
	if len(items) != len(values) {
		t.Fatalf("List returned %d items, want %d", len(items), len(values))
	}
	for i, it := range items {
		if string(it.Value) != values[i] {
			t.Errorf("List value %d = %q, want %q", i, truncate(it.Value), truncate([]byte(values[i])))
		}
	}
}

func truncate(b []byte) string {
	if len(b) > 80 {
		return string(b[:80]) + "…"
	}
	return string(b)
}

// Keys are byte strings: NUL bytes and invalid UTF-8 work everywhere (the
// secret store keeps its sentinel under "\x00sentinel").
func testKVBinaryKeys(t *testing.T, b Backend) {
	s, _ := open(t, b)
	c := ctx(t)
	kv := s.KV("cicord:secrets")
	keys := []string{"\x00sentinel", "\x00", "a\x00b", "a", "a\x00", "\xff\x00\xfe", "\\x00", `\\`}
	for i, k := range keys {
		must(t, kv.Set(c, k, json.RawMessage(fmt.Sprint(i)), 0))
	}
	for i, k := range keys {
		got, err := kv.Get(c, k)
		if err != nil || string(got) != fmt.Sprint(i) {
			t.Errorf("Get(%q) = %s, %v; want %d", k, got, err, i)
		}
	}
	items, _, err := kv.List(c, "", 0, "")
	must(t, err)
	want := slices.Clone(keys)
	slices.Sort(want)
	if got := itemKeys(items); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Errorf("List = %q, want %q", got, want)
	}
	items, _, err = kv.List(c, "\x00", 0, "")
	must(t, err)
	if got := itemKeys(items); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", []string{"\x00", "\x00sentinel"}) {
		t.Errorf("List(\\x00) = %q", got)
	}
	items, _, err = kv.List(c, "a\x00", 0, "")
	must(t, err)
	if got := itemKeys(items); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", []string{"a\x00", "a\x00b"}) {
		t.Errorf("List(a\\x00) = %q", got)
	}
	items, next, err := kv.List(c, "", 2, want[0])
	must(t, err)
	if got := itemKeys(items); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want[1:3]) || next != want[2] {
		t.Errorf("List after cursor %q = %q next %q, want %q next %q", want[0], got, next, want[1:3], want[2])
	}
	if v, err := kv.Increment(c, "\x00n", 2); err != nil || v != 2 {
		t.Errorf("Increment NUL key = %v, %v", v, err)
	}
	must(t, kv.Delete(c, "\x00sentinel"))
	if _, err := kv.Get(c, "\x00sentinel"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v", err)
	}
	if got, err := kv.Get(c, "\x00"); err != nil || string(got) != "1" {
		t.Errorf("Delete removed a neighbor key: %s, %v", got, err)
	}
}

func testKVSetValidation(t *testing.T, b Backend) {
	s, _ := open(t, b)
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
	if _, err := kv.Increment(c, "", 1); err == nil {
		t.Error("Increment with empty key succeeded")
	}
	if _, err := kv.Get(c, "k"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a rejected Set stored a value: err=%v", err)
	}
}

func testKVTTL(t *testing.T, b Backend) {
	s, clk := open(t, b)
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
	// Get deleted the expired row lazily: Sweep has nothing left to remove.
	if n := sweep(t, s); n != 0 {
		t.Errorf("Sweep after lazy delete removed %d rows, want 0", n)
	}

	items, _, err := kv.List(c, "", 0, "")
	must(t, err)
	if keys := itemKeys(items); fmt.Sprint(keys) != "[forever long]" {
		t.Errorf("List after expiry = %v", keys)
	}

	// Set without TTL clears a previous TTL; Set with a TTL adds one.
	must(t, kv.Set(c, "long", json.RawMessage(`4`), 0))
	must(t, kv.Set(c, "forever", json.RawMessage(`5`), time.Hour))
	clk.Advance(2 * time.Hour)
	if v, err := kv.Get(c, "long"); err != nil || string(v) != "4" {
		t.Errorf("re-Set key without TTL: %s, %v", v, err)
	}
	if _, err := kv.Get(c, "forever"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("re-Set key with TTL did not expire: err=%v", err)
	}

	// Sweep removes expired KV rows and sessions only.
	must(t, kv.Set(c, "a", json.RawMessage(`1`), time.Second))
	must(t, s.KV("other").Set(c, "b", json.RawMessage(`1`), time.Second))
	must(t, s.Sessions().Put(c, state.Session{ID: "sess", UserID: "u", ExpiresAt: clk.Now().Add(time.Second)}))
	must(t, s.Sessions().Put(c, state.Session{ID: "live", UserID: "u", ExpiresAt: clk.Now().Add(time.Hour)}))
	clk.Advance(time.Second)
	if n := sweep(t, s); n != 3 {
		t.Errorf("Sweep removed %d rows, want 3", n)
	}
	items, _, err = kv.List(c, "", 0, "")
	must(t, err)
	if keys := itemKeys(items); fmt.Sprint(keys) != "[long]" {
		t.Errorf("kv rows left after sweep = %v, want [long]", keys)
	}
	if _, err := s.Sessions().Get(c, "live"); err != nil {
		t.Errorf("Sweep removed live session: %v", err)
	}
	if n := sweep(t, s); n != 0 {
		t.Errorf("second Sweep removed %d rows", n)
	}
}

func testKVIncrement(t *testing.T, b Backend) {
	s, clk := open(t, b)
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

	// Integers stay plain JSON integers; large values keep Go's encoding.
	must(t, kv.Set(c, "i", json.RawMessage(`41`), 0))
	v, err = kv.Increment(c, "i", 1)
	must(t, err)
	if raw, _ := kv.Get(c, "i"); v != 42 || string(raw) != "42" {
		t.Errorf("Increment(41,1) = %v stored %s", v, raw)
	}
	must(t, kv.Set(c, "e", json.RawMessage(`1e21`), 0))
	if _, err := kv.Increment(c, "e", 0); err != nil {
		t.Fatal(err)
	}
	if raw, _ := kv.Get(c, "e"); string(raw) != "1e+21" {
		t.Errorf("Increment(1e21,0) stored %s, want 1e+21", raw)
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

	// Invalid deltas and non-finite results; a failed Increment of a missing
	// key does not create it.
	for _, by := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := kv.Increment(c, "n", by); err == nil {
			t.Errorf("Increment by %v succeeded", by)
		}
		if _, err := kv.Increment(c, "fresh", by); err == nil {
			t.Errorf("Increment missing key by %v succeeded", by)
		}
	}
	if _, err := kv.Get(c, "fresh"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("failed Increment created the key: err=%v", err)
	}
	must(t, kv.Set(c, "big", json.RawMessage(`1.7e308`), 0))
	if _, err := kv.Increment(c, "big", 1.7e308); err == nil {
		t.Error("Increment overflowing to +Inf succeeded")
	}
	if raw, _ := kv.Get(c, "big"); string(raw) != "1.7e308" {
		t.Errorf("overflowing Increment changed value to %s", raw)
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
	must(t, kv.Set(c, "ttl2", json.RawMessage(`10`), time.Second))
	clk.Advance(time.Second)
	v, err = kv.Increment(c, "ttl2", 5)
	must(t, err)
	if v != 5 {
		t.Errorf("Increment expired key = %v, want 5", v)
	}
	clk.Advance(24 * time.Hour)
	if raw, err := kv.Get(c, "ttl2"); err != nil || string(raw) != "5" {
		t.Errorf("revived key: %s, %v", raw, err)
	}
	// An expired non-numeric value is also treated as missing.
	must(t, kv.Set(c, "oldstr", json.RawMessage(`"x"`), time.Second))
	clk.Advance(time.Second)
	if v, err := kv.Increment(c, "oldstr", 2); err != nil || v != 2 {
		t.Errorf("Increment expired non-numeric = %v, %v", v, err)
	}
}

// incrementHammer runs workers goroutines, each incrementing key perG times
// through its store (stores are reused round-robin), interleaving reads and
// unrelated writes. It fails the test on any error.
func incrementHammer(t *testing.T, stores []state.Store, workers, perG int) {
	t.Helper()
	c := ctx(t)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for g := range workers {
		kv := stores[g%len(stores)].KV("plugin:bot/counter/main")
		wg.Go(func() {
			for i := range perG {
				if _, err := kv.Increment(c, "hits", 1); err != nil {
					errs <- err
					return
				}
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
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	raw, err := stores[0].KV("plugin:bot/counter/main").Get(c, "hits")
	must(t, err)
	if want := fmt.Sprint(workers * perG); string(raw) != want {
		t.Fatalf("hits = %s, want %s (lost updates)", raw, want)
	}
}

// Many goroutines race on the first Increment of a missing key and then on the
// same row: no update may be lost.
func testKVIncrementConcurrent(t *testing.T, b Backend) {
	s, _ := open(t, b)
	incrementHammer(t, []state.Store{s}, 32, 40)
}

// Several handles on one database contend like several processes would.
func testKVIncrementAcrossHandles(t *testing.T, b Backend) {
	if !b.SharedHandles {
		t.Skip("backend has no shared handles")
	}
	o := b.NewDatabase(t)
	clk := NewClock()
	stores := []state.Store{o(t, clk.Now), o(t, clk.Now), o(t, clk.Now)}
	incrementHammer(t, stores, 6, 60)
}

func testKVListPrefixAndOrder(t *testing.T, b Backend) {
	s, _ := open(t, b)
	c := ctx(t)
	kv := s.KV("s")
	keys := []string{
		"user:1", "user:10", "user:2", "user_1", "user%1", "User:3", "users", "use",
		"a%b", "a%c", "aXb", "a_b", "abb", "a b", "a-b", "A",
		"\xff\xffz", "\xff\xff", "\xff\xfe", "é:1", "é:2", "e:1", "E:1",
	}
	for _, k := range keys {
		must(t, kv.Set(c, k, json.RawMessage(`0`), 0))
	}
	must(t, s.KV("other").Set(c, "user:999", json.RawMessage(`0`), 0))
	must(t, s.KV("s2").Set(c, "user:998", json.RawMessage(`0`), 0))

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
		{"a", []string{"a b", "a%b", "a%c", "a-b", "aXb", "a_b", "abb"}},
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

	// Empty prefix lists the whole scope in byte order (not locale order).
	items, _, err := kv.List(c, "", 0, "")
	must(t, err)
	want := slices.Clone(keys)
	slices.Sort(want)
	if got := itemKeys(items); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Errorf("List(\"\") = %q, want %q", got, want)
	}
}

func testKVListPagination(t *testing.T, b Backend) {
	s, clk := open(t, b)
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

	// Values come back with the items; the cursor need not match the prefix.
	items, _, err := kv.List(c, "item:00", 3, "item:000")
	must(t, err)
	if len(items) != 3 || items[0].Key != "item:001" || string(items[0].Value) != "1" {
		t.Errorf("List after cursor = %+v", items)
	}
	items, _, err = kv.List(c, "item:", 0, "a")
	must(t, err)
	if len(items) != 25 {
		t.Errorf("cursor before the prefix range: %d items", len(items))
	}
	items, _, err = kv.List(c, "item:", 0, "zzz")
	must(t, err)
	if len(items) != 0 {
		t.Errorf("cursor after the prefix range: %d items", len(items))
	}
}

func testKVListLimits(t *testing.T, b Backend) {
	s, _ := open(t, b)
	c := ctx(t)
	kv := s.KV("s")
	const n = state.MaxListLimit + state.DefaultKVListLimit
	for i := range n {
		must(t, kv.Set(c, fmt.Sprintf("k%05d", i), json.RawMessage(`0`), 0))
	}
	key := func(i int) string { return fmt.Sprintf("k%05d", i) }

	items, next, err := kv.List(c, "", 0, "")
	must(t, err)
	if len(items) != state.DefaultKVListLimit || next != key(state.DefaultKVListLimit-1) {
		t.Errorf("default limit: %d items, next %q", len(items), next)
	}
	items, next, err = kv.List(c, "", 5000, "")
	must(t, err)
	if len(items) != state.MaxListLimit || next != key(state.MaxListLimit-1) {
		t.Errorf("max limit: %d items, next %q", len(items), next)
	}
	items, next, err = kv.List(c, "", 5000, next)
	must(t, err)
	if len(items) != n-state.MaxListLimit || next != "" {
		t.Errorf("last page: %d items, next %q", len(items), next)
	}
}

// ───────────────────────────────────────────────────────────── resources

func testResources(t *testing.T, b Backend) {
	s, clk := open(t, b)
	rm := s.Resources()
	c := ctx(t)

	if _, err := rm.Get(c, "main", state.KindRole, "mod"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get missing: err=%v, want ErrNotFound", err)
	}
	if _, err := rm.GetByID(c, "main", "1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("GetByID missing: err=%v, want ErrNotFound", err)
	}

	t0 := clk.Now()
	in := []state.Resource{
		{Server: "main", Kind: state.KindRole, Key: "mod", ID: "10", Owner: state.OwnerGit, UpdatedAt: t0},
		{Server: "main", Kind: state.KindChannel, Key: "general", ID: "20", Owner: state.OwnerGit, UpdatedAt: t0},
		{Server: "main", Kind: state.KindCategory, Key: "info", ID: "30", Owner: state.OwnerGit, UpdatedAt: t0},
		{Server: "main", Kind: state.KindChannel, Key: "announce", ID: "21", Owner: "plugin:bot/tickets", UpdatedAt: t0},
		{Server: "other", Kind: state.KindRole, Key: "mod", ID: "99", Owner: state.OwnerGit, UpdatedAt: t0},
	}
	for _, r := range in {
		must(t, rm.Put(c, r))
	}

	got, err := rm.Get(c, "main", state.KindChannel, "announce")
	must(t, err)
	if w := in[3]; got.Server != w.Server || got.Kind != w.Kind || got.Key != w.Key || got.ID != w.ID || got.Owner != w.Owner {
		t.Errorf("Get = %+v, want %+v", got, w)
	}
	assertTime(t, "UpdatedAt", got.UpdatedAt, t0)

	// Same key in another server is a separate resource.
	got, err = rm.Get(c, "other", state.KindRole, "mod")
	must(t, err)
	if got.ID != "99" {
		t.Errorf("other/mod ID = %q", got.ID)
	}

	// GetByID is scoped to the server.
	got, err = rm.GetByID(c, "main", "20")
	must(t, err)
	if got.Kind != state.KindChannel || got.Key != "general" {
		t.Errorf("GetByID(main, 20) = %+v", got)
	}
	if _, err := rm.GetByID(c, "other", "20"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("GetByID(other, 20): err=%v, want ErrNotFound", err)
	}

	// List: ordered by kind, then key; only the requested server.
	list, err := rm.List(c, "main")
	must(t, err)
	want := []string{"category/info", "channel/announce", "channel/general", "role/mod"}
	if len(list) != len(want) {
		t.Fatalf("List returned %d resources, want %d: %+v", len(list), len(want), list)
	}
	for i, r := range list {
		if k := string(r.Kind) + "/" + r.Key; k != want[i] {
			t.Errorf("List[%d] = %s, want %s", i, k, want[i])
		}
	}

	// Put replaces on (server, kind, key).
	clk.Advance(time.Minute)
	must(t, rm.Put(c, state.Resource{Server: "main", Kind: state.KindRole, Key: "mod", ID: "11", Owner: state.OwnerGit}))
	got, err = rm.Get(c, "main", state.KindRole, "mod")
	must(t, err)
	if got.ID != "11" {
		t.Errorf("after replace ID = %q, want 11", got.ID)
	}
	assertTime(t, "replaced UpdatedAt", got.UpdatedAt, clk.Now())
	if _, err := rm.GetByID(c, "main", "10"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("old ID still resolvable: err=%v", err)
	}
	if list, _ := rm.List(c, "main"); len(list) != 4 {
		t.Errorf("replace changed resource count to %d", len(list))
	}

	// Delete, including a missing resource.
	must(t, rm.Delete(c, "main", state.KindRole, "mod"))
	must(t, rm.Delete(c, "main", state.KindRole, "mod"))
	if _, err := rm.Get(c, "main", state.KindRole, "mod"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v", err)
	}
	if _, err := rm.Get(c, "other", state.KindRole, "mod"); err != nil {
		t.Errorf("Delete leaked into another server: %v", err)
	}

	if list, err := rm.List(c, "nobody"); err != nil || len(list) != 0 {
		t.Errorf("List(empty server) = %v, %v", list, err)
	}
}

// List and GetByID order by byte order, whatever the database locale.
func testResourceOrder(t *testing.T, b Backend) {
	s, _ := open(t, b)
	rm := s.Resources()
	c := ctx(t)
	keys := []string{"b", "B", "a", "_x", "a-b", "ab", "a b", "Z", "é", "e", "10", "9"}
	for _, k := range keys {
		must(t, rm.Put(c, state.Resource{Server: "s", Kind: state.KindChannel, Key: k, ID: "same", Owner: state.OwnerGit}))
	}
	must(t, rm.Put(c, state.Resource{Server: "s", Kind: "Upper", Key: "z", ID: "other"}))
	list, err := rm.List(c, "s")
	must(t, err)
	want := append([]string{"Upper/z"}, func() []string {
		ks := slices.Clone(keys)
		slices.Sort(ks)
		out := make([]string, len(ks))
		for i, k := range ks {
			out[i] = "channel/" + k
		}
		return out
	}()...)
	var got []string
	for _, r := range list {
		got = append(got, string(r.Kind)+"/"+r.Key)
	}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Errorf("List = %q, want %q", got, want)
	}
	r, err := rm.GetByID(c, "s", "same")
	must(t, err)
	if r.Key != "10" {
		t.Errorf("GetByID with several keys = %q, want the first by (kind, key) %q", r.Key, "10")
	}
}

func testResourcePutValidation(t *testing.T, b Backend) {
	s, _ := open(t, b)
	for _, r := range []state.Resource{
		{Kind: state.KindRole, Key: "k", ID: "1"},
		{Server: "s", Key: "k", ID: "1"},
		{Server: "s", Kind: state.KindRole, ID: "1"},
		{Server: "s", Kind: state.KindRole, Key: "k"},
	} {
		if err := s.Resources().Put(ctx(t), r); err == nil {
			t.Errorf("Put(%+v) succeeded", r)
		}
	}
}

// ───────────────────────────────────────────────────────────── tasks

func testTasks(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	q := s.Tasks()
	now := clk.Now()

	must(t, q.Schedule(c, state.Task{ID: "late", Scope: "a", Name: "remind", Payload: json.RawMessage(`{"user":"1"}`), RunAt: now.Add(time.Hour)}))
	must(t, q.Schedule(c, state.Task{ID: "t2", Scope: "a", Name: "x", RunAt: now.Add(-time.Minute)}))
	must(t, q.Schedule(c, state.Task{ID: "t1", Scope: "b", Name: "y", RunAt: now.Add(-time.Hour)}))
	must(t, q.Schedule(c, state.Task{ID: "t3", Scope: "b", Name: "z", RunAt: now})) // due exactly now
	must(t, q.Schedule(c, state.Task{ID: "t0", Scope: "b", Name: "tie", RunAt: now}))

	due, err := q.Due(c, now, 0)
	must(t, err)
	if got := taskIDs(due); fmt.Sprint(got) != "[t1 t2 t0 t3]" {
		t.Fatalf("Due = %v, want [t1 t2 t0 t3] (oldest first, ties by id)", got)
	}
	if due[0].Payload != nil {
		t.Errorf("nil payload came back as %q", due[0].Payload)
	}
	if due[0].Scope != "b" || due[0].Name != "y" {
		t.Errorf("Due[0] = %+v", due[0])
	}
	due, err = q.Due(c, now, 2)
	must(t, err)
	if got := taskIDs(due); fmt.Sprint(got) != "[t1 t2]" {
		t.Errorf("Due limit 2 = %v", got)
	}

	// Upsert by ID within the same scope.
	must(t, q.Schedule(c, state.Task{ID: "late", Scope: "a", Name: "remind2", Payload: json.RawMessage(`[1]`), RunAt: now.Add(-2 * time.Hour)}))
	due, err = q.Due(c, now, 10)
	must(t, err)
	if len(due) != 5 || due[0].ID != "late" || due[0].Name != "remind2" || string(due[0].Payload) != "[1]" || due[0].Scope != "a" {
		t.Fatalf("after upsert Due[0] = %+v (len %d)", due[0], len(due))
	}
	assertTime(t, "upserted RunAt", due[0].RunAt, now.Add(-2*time.Hour))

	// Another scope cannot take over an existing ID, and the task is unchanged.
	err = q.Schedule(c, state.Task{ID: "late", Scope: "evil", Name: "hijack", RunAt: now})
	if !errors.Is(err, state.ErrTaskIDConflict) {
		t.Errorf("cross-scope Schedule: err=%v, want state.ErrTaskIDConflict", err)
	}
	due, err = q.Due(c, now, 10)
	must(t, err)
	if due[0].ID != "late" || due[0].Scope != "a" || due[0].Name != "remind2" {
		t.Errorf("rejected Schedule changed the task: %+v", due[0])
	}

	// Cancel is scoped.
	must(t, q.Cancel(c, "a", "t1")) // wrong scope: no-op
	must(t, q.Cancel(c, "b", "t1"))
	must(t, q.Cancel(c, "b", "t1")) // idempotent
	must(t, q.Complete(c, "t2"))
	must(t, q.Complete(c, "missing"))
	due, err = q.Due(c, now, 10)
	must(t, err)
	if got := taskIDs(due); fmt.Sprint(got) != "[late t0 t3]" {
		t.Errorf("after Cancel/Complete Due = %v", got)
	}

	// Nothing due far in the past.
	due, err = q.Due(c, now.Add(-24*time.Hour), 10)
	must(t, err)
	if len(due) != 0 {
		t.Errorf("Due in the past = %v", taskIDs(due))
	}
}

func testTaskTies(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	now := clk.Now()
	ids := []string{"b", "B", "a", "_x", "a-b", "ab", "a b", "Z", "10", "9"}
	for _, id := range ids {
		must(t, s.Tasks().Schedule(c, state.Task{ID: id, Scope: "s", RunAt: now}))
	}
	due, err := s.Tasks().Due(c, now, 0)
	must(t, err)
	want := slices.Clone(ids)
	slices.Sort(want)
	if got := taskIDs(due); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Errorf("Due ties = %q, want byte order %q", got, want)
	}
}

func testTaskValidation(t *testing.T, b Backend) {
	s, _ := open(t, b)
	c := ctx(t)
	now := time.Now()
	for _, tk := range []state.Task{
		{Scope: "s", RunAt: now},
		{ID: "x", RunAt: now},
		{ID: "x", Scope: "s"},
		{ID: "x", Scope: "s", RunAt: now, Payload: json.RawMessage(`{bad`)},
	} {
		if err := s.Tasks().Schedule(c, tk); err == nil {
			t.Errorf("Schedule(%+v) succeeded", tk)
		}
	}
}

func testTaskDueExtremes(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	must(t, s.Tasks().Schedule(c, state.Task{ID: "x", Scope: "s", RunAt: clk.Now()}))
	if due, err := s.Tasks().Due(c, time.Time{}, 0); err != nil || len(due) != 0 {
		t.Errorf("Due(zero time) = %v, %v", taskIDs(due), err)
	}
	if due, err := s.Tasks().Due(c, time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), 0); err != nil || len(due) != 1 {
		t.Errorf("Due(year 3000) = %v, %v", taskIDs(due), err)
	}
	if due, err := s.Tasks().Due(c, clk.Now(), 5000); err != nil || len(due) != 1 {
		t.Errorf("Due(limit 5000) = %v, %v", taskIDs(due), err)
	}
}

// ───────────────────────────────────────────────────────────── audit, sync

func testAuditLog(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	a := s.Audit()

	if es, err := a.List(c, state.AuditFilter{}); err != nil || len(es) != 0 {
		t.Fatalf("empty List = %v, %v", es, err)
	}

	var ids []int64
	for i := range 120 {
		server := "main"
		switch i % 3 {
		case 1:
			server = "other"
		case 2:
			server = "" // project-level
		}
		clk.Advance(time.Millisecond)
		id, err := a.Append(c, state.AuditEntry{
			Server: server, Actor: "discord:1", Action: "member.kick", Target: fmt.Sprint(i),
			Details: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
		must(t, err)
		if len(ids) > 0 && id <= ids[len(ids)-1] {
			t.Fatalf("IDs not increasing: %d after %d", id, ids[len(ids)-1])
		}
		ids = append(ids, id)
	}

	// Default limit, newest first, all servers.
	es, err := a.List(c, state.AuditFilter{})
	must(t, err)
	if len(es) != state.DefaultAuditLimit {
		t.Fatalf("default List returned %d, want %d", len(es), state.DefaultAuditLimit)
	}
	if es[0].ID != ids[119] || es[0].Target != "119" || string(es[0].Details) != `{"i":119}` || es[0].Actor != "discord:1" || es[0].Action != "member.kick" || es[0].Server != "" {
		t.Errorf("newest entry = %+v", es[0])
	}
	assertTime(t, "newest Time", es[0].Time, clk.Now())
	for i := 1; i < len(es); i++ {
		if es[i].ID >= es[i-1].ID {
			t.Fatalf("not newest first at %d", i)
		}
	}

	// Server filter + Before pagination walks every matching entry exactly once.
	var walked []string
	before := int64(0)
	for {
		page, err := a.List(c, state.AuditFilter{Server: "main", Before: before, Limit: 7})
		must(t, err)
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if e.Server != "main" {
				t.Fatalf("server filter leaked %+v", e)
			}
			walked = append(walked, e.Target)
		}
		before = page[len(page)-1].ID
	}
	if len(walked) != 40 || walked[0] != "117" || walked[39] != "0" {
		t.Errorf("walked %d main entries: %v", len(walked), walked)
	}

	es, err = a.List(c, state.AuditFilter{Before: ids[2], Limit: 10})
	must(t, err)
	if len(es) != 2 || es[0].ID != ids[1] || es[1].ID != ids[0] {
		t.Errorf("Before ids[2] = %+v", es)
	}
	es, err = a.List(c, state.AuditFilter{Limit: 5000})
	must(t, err)
	if len(es) != 120 {
		t.Errorf("large limit returned %d", len(es))
	}
}

func testAuditDetailsValidation(t *testing.T, b Backend) {
	s, _ := open(t, b)
	if _, err := s.Audit().Append(ctx(t), state.AuditEntry{Details: json.RawMessage(`{nope`)}); err == nil {
		t.Error("Append with invalid details succeeded")
	}
	id, err := s.Audit().Append(ctx(t), state.AuditEntry{Action: "x"})
	must(t, err)
	es, err := s.Audit().List(ctx(t), state.AuditFilter{})
	must(t, err)
	if len(es) != 1 || es[0].ID != id || es[0].Details != nil || es[0].Server != "" {
		t.Errorf("entries = %+v", es)
	}
}

func testSyncLog(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	l := s.Syncs()
	if rs, err := l.List(c, "", 0); err != nil || len(rs) != 0 {
		t.Fatalf("empty List = %v, %v", rs, err)
	}
	targets := []string{"server:main", "bot:xp", "revision"}
	var last int64
	for i := range 60 {
		clk.Advance(time.Second)
		id, err := l.Record(c, state.SyncRecord{
			Revision: fmt.Sprintf("sha%02d", i), Target: targets[i%3], Status: "succeeded",
			Message: "ok", Details: json.RawMessage(`{"changes":[]}`),
		})
		must(t, err)
		if id <= last {
			t.Fatalf("IDs not increasing: %d after %d", id, last)
		}
		last = id
	}
	_, err := l.Record(c, state.SyncRecord{Revision: "bad", Target: "server:main", Status: "failed", Message: "boom"})
	must(t, err)
	if _, err := l.Record(c, state.SyncRecord{Target: "revision", Details: json.RawMessage(`{`)}); err == nil {
		t.Error("Record with invalid details succeeded")
	}

	all, err := l.List(c, "", 0)
	must(t, err)
	if len(all) != state.DefaultSyncLimit || all[0].Revision != "bad" || all[0].Status != "failed" || all[0].Details != nil || all[0].Message != "boom" {
		t.Fatalf("List all: len %d first %+v", len(all), all[0])
	}
	if all[1].Revision != "sha59" || string(all[1].Details) != `{"changes":[]}` || all[1].Message != "ok" || all[1].Target != "revision" {
		t.Errorf("second record = %+v", all[1])
	}
	assertTime(t, "sync Time", all[1].Time, clk.Now())

	main, err := l.List(c, "server:main", 5)
	must(t, err)
	if len(main) != 5 {
		t.Fatalf("List server:main returned %d", len(main))
	}
	for i, r := range main {
		if r.Target != "server:main" {
			t.Errorf("target filter leaked %+v", r)
		}
		if i > 0 && r.ID >= main[i-1].ID {
			t.Errorf("not newest first")
		}
	}
	if main[0].Revision != "bad" || main[1].Revision != "sha57" {
		t.Errorf("server:main newest = %s, %s", main[0].Revision, main[1].Revision)
	}
	none, err := l.List(c, "server:nope", 10)
	must(t, err)
	if len(none) != 0 {
		t.Errorf("unknown target returned %d records", len(none))
	}
	if big, err := l.List(c, "", 5000); err != nil || len(big) != 61 {
		t.Errorf("large limit returned %d, %v", len(big), err)
	}
}

// ───────────────────────────────────────────────────────────── sessions

func testSessions(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	ss := s.Sessions()
	exp := clk.Now().Add(time.Hour)

	if _, err := ss.Get(c, "nope"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get missing: err=%v", err)
	}
	must(t, ss.Put(c, state.Session{ID: "abc", UserID: "123", Data: json.RawMessage(`{"csrf":"x"}`), ExpiresAt: exp}))
	got, err := ss.Get(c, "abc")
	must(t, err)
	if got.ID != "abc" || got.UserID != "123" || string(got.Data) != `{"csrf":"x"}` {
		t.Errorf("Get = %+v", got)
	}
	assertTime(t, "ExpiresAt", got.ExpiresAt, exp)

	// Upsert.
	exp2 := exp.Add(time.Hour)
	must(t, ss.Put(c, state.Session{ID: "abc", UserID: "456", ExpiresAt: exp2}))
	got, err = ss.Get(c, "abc")
	must(t, err)
	if got.UserID != "456" || got.Data != nil {
		t.Errorf("after upsert = %+v", got)
	}
	assertTime(t, "upserted ExpiresAt", got.ExpiresAt, exp2)

	// Expiry; Get deletes the expired session lazily (Sweep finds nothing).
	clk.Advance(2*time.Hour - time.Nanosecond)
	if _, err := ss.Get(c, "abc"); err != nil {
		t.Fatalf("session expired early: %v", err)
	}
	clk.Advance(time.Nanosecond)
	if _, err := ss.Get(c, "abc"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired Get: err=%v, want ErrNotFound", err)
	}
	if n := sweep(t, s); n != 0 {
		t.Errorf("expired session not lazily deleted (Sweep removed %d)", n)
	}

	// Delete.
	must(t, ss.Put(c, state.Session{ID: "d", UserID: "1", ExpiresAt: clk.Now().Add(time.Minute)}))
	must(t, ss.Delete(c, "d"))
	must(t, ss.Delete(c, "d"))
	if _, err := ss.Get(c, "d"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v", err)
	}

	// Validation.
	for _, bad := range []state.Session{
		{UserID: "1", ExpiresAt: exp},
		{ID: "x", UserID: "1"},
		{ID: "x", UserID: "1", ExpiresAt: exp, Data: json.RawMessage(`{`)},
		{ID: "x", UserID: "1", ExpiresAt: time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if err := ss.Put(c, bad); err == nil {
			t.Errorf("Put(%+v) succeeded", bad)
		}
	}
}

func testSweep(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	if n := sweep(t, s); n != 0 {
		t.Errorf("Sweep on an empty store removed %d", n)
	}
	for i := range 5 {
		must(t, s.KV(fmt.Sprint("scope", i%2)).Set(c, fmt.Sprint("k", i), json.RawMessage(`1`), time.Duration(i+1)*time.Second))
	}
	must(t, s.KV("s").Set(c, "forever", json.RawMessage(`1`), 0))
	must(t, s.Sessions().Put(c, state.Session{ID: "a", ExpiresAt: clk.Now().Add(2 * time.Second)}))
	// Tasks, resources and logs are never swept.
	must(t, s.Tasks().Schedule(c, state.Task{ID: "t", Scope: "s", RunAt: clk.Now()}))
	clk.Advance(2 * time.Second)
	if n := sweep(t, s); n != 3 { // k0, k1, session a
		t.Errorf("Sweep removed %d rows, want 3", n)
	}
	clk.Advance(time.Hour)
	if n := sweep(t, s); n != 3 { // k2, k3, k4
		t.Errorf("Sweep removed %d rows, want 3", n)
	}
	if _, err := s.KV("s").Get(c, "forever"); err != nil {
		t.Errorf("Sweep removed a key without TTL: %v", err)
	}
	if due, err := s.Tasks().Due(c, clk.Now(), 0); err != nil || len(due) != 1 {
		t.Errorf("Sweep touched tasks: %v, %v", taskIDs(due), err)
	}
}

// ───────────────────────────────────────────────────────────── store

func testTimeRoundTripExact(t *testing.T, b Backend) {
	s, _ := open(t, b)
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		paris = time.FixedZone("CET", 3600)
	}
	// Sub-second precision, a non-UTC location, a monotonic reading and the
	// ends of the representable range.
	times := []time.Time{
		time.Date(2026, 7, 1, 12, 0, 0, 1, paris),
		time.Date(1999, 12, 31, 23, 59, 59, 999999999, time.UTC),
		time.Now(),
		time.Unix(0, math.MaxInt64),
		time.Unix(0, math.MinInt64+1),
	}
	for i, when := range times {
		key := string(rune('a' + i))
		must(t, s.Resources().Put(ctx(t), state.Resource{Server: "srv", Kind: state.KindChannel, Key: key, ID: key, UpdatedAt: when}))
		r, err := s.Resources().Get(ctx(t), "srv", state.KindChannel, key)
		must(t, err)
		assertTime(t, "Resource.UpdatedAt", r.UpdatedAt, when)

		id, err := s.Audit().Append(ctx(t), state.AuditEntry{Time: when, Action: key})
		must(t, err)
		es, err := s.Audit().List(ctx(t), state.AuditFilter{Limit: 1})
		must(t, err)
		if es[0].ID != id {
			t.Fatalf("audit newest id %d, want %d", es[0].ID, id)
		}
		assertTime(t, "AuditEntry.Time", es[0].Time, when)

		_, err = s.Syncs().Record(ctx(t), state.SyncRecord{Time: when, Target: "revision", Status: "succeeded"})
		must(t, err)
		rs, err := s.Syncs().List(ctx(t), "", 1)
		must(t, err)
		assertTime(t, "SyncRecord.Time", rs[0].Time, when)

		must(t, s.Tasks().Schedule(ctx(t), state.Task{ID: "t" + key, Scope: "sc", RunAt: when}))
		ts, err := s.Tasks().Due(ctx(t), when, state.MaxListLimit)
		must(t, err)
		var found bool
		for _, tk := range ts {
			if tk.ID == "t"+key {
				found = true
				assertTime(t, "Task.RunAt", tk.RunAt, when)
			}
		}
		if !found {
			t.Errorf("task %q not due at its own RunAt", "t"+key)
		}
		must(t, s.Tasks().Complete(ctx(t), "t"+key))
	}
	// Session expiry keeps nanoseconds too.
	exp := time.Date(2100, 1, 2, 3, 4, 5, 6, paris)
	must(t, s.Sessions().Put(ctx(t), state.Session{ID: "x", ExpiresAt: exp}))
	got, err := s.Sessions().Get(ctx(t), "x")
	must(t, err)
	assertTime(t, "Session.ExpiresAt", got.ExpiresAt, exp)
}

func testOutOfRangeTimes(t *testing.T, b Backend) {
	s, _ := open(t, b)
	c := ctx(t)
	far := time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)
	early := time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, when := range []time.Time{far, early} {
		if err := s.Resources().Put(c, state.Resource{Server: "s", Kind: state.KindRole, Key: "k", ID: "1", UpdatedAt: when}); err == nil {
			t.Errorf("Put with UpdatedAt %v succeeded", when)
		}
		if err := s.Tasks().Schedule(c, state.Task{ID: "x", Scope: "s", RunAt: when}); err == nil {
			t.Errorf("Schedule with RunAt %v succeeded", when)
		}
		if _, err := s.Audit().Append(c, state.AuditEntry{Time: when}); err == nil {
			t.Errorf("Append with Time %v succeeded", when)
		}
		if _, err := s.Syncs().Record(c, state.SyncRecord{Time: when}); err == nil {
			t.Errorf("Record with Time %v succeeded", when)
		}
	}
	if err := s.KV("s").Set(c, "k", json.RawMessage(`1`), time.Duration(math.MaxInt64)); err == nil {
		t.Error("Set with a TTL past 2262 succeeded")
	}
}

func testDefaultTimestamps(t *testing.T, b Backend) {
	s, clk := open(t, b)
	must(t, s.Resources().Put(ctx(t), state.Resource{Server: "s", Kind: state.KindRole, Key: "k", ID: "1"}))
	r, err := s.Resources().Get(ctx(t), "s", state.KindRole, "k")
	must(t, err)
	assertTime(t, "default UpdatedAt", r.UpdatedAt, clk.Now())

	_, err = s.Audit().Append(ctx(t), state.AuditEntry{Action: "x"})
	must(t, err)
	es, err := s.Audit().List(ctx(t), state.AuditFilter{})
	must(t, err)
	assertTime(t, "default audit Time", es[0].Time, clk.Now())

	_, err = s.Syncs().Record(ctx(t), state.SyncRecord{Target: "revision"})
	must(t, err)
	rs, err := s.Syncs().List(ctx(t), "", 0)
	must(t, err)
	assertTime(t, "default sync Time", rs[0].Time, clk.Now())
}

func testCanceledContext(t *testing.T, b Backend) {
	s, _ := open(t, b)
	must(t, s.KV("s").Set(ctx(t), "k", json.RawMessage(`1`), 0))
	cctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.KV("s").Set(cctx, "k", json.RawMessage(`2`), 0); err == nil {
		t.Error("Set with canceled context succeeded")
	}
	if _, err := s.KV("s").Get(cctx, "k"); err == nil {
		t.Error("Get with canceled context succeeded")
	}
	if _, err := s.KV("s").Increment(cctx, "n", 1); err == nil {
		t.Error("Increment with canceled context succeeded")
	}
	if _, _, err := s.KV("s").List(cctx, "", 0, ""); err == nil {
		t.Error("KV List with canceled context succeeded")
	}
	if _, err := s.Resources().List(cctx, "main"); err == nil {
		t.Error("Resources List with canceled context succeeded")
	}
	if _, err := s.Audit().Append(cctx, state.AuditEntry{Action: "x"}); err == nil {
		t.Error("Append with canceled context succeeded")
	}
	if _, err := s.Sweep(cctx); err == nil {
		t.Error("Sweep with canceled context succeeded")
	}
	// The store still works afterwards, with nothing written.
	if v, err := s.KV("s").Get(ctx(t), "k"); err != nil || string(v) != "1" {
		t.Errorf("after canceled calls: Get = %s, %v", v, err)
	}
	if _, err := s.KV("s").Get(ctx(t), "n"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("canceled Increment wrote: err=%v", err)
	}
	if _, err := s.KV("s").Increment(ctx(t), "n", 1); err != nil {
		t.Errorf("Increment after canceled calls: %v", err)
	}
}

func testPersistsAcrossHandles(t *testing.T, b Backend) {
	if !b.SharedHandles {
		t.Skip("backend has no shared handles")
	}
	o := b.NewDatabase(t)
	clk := NewClock()
	c := ctx(t)
	when := time.Date(2025, 12, 31, 23, 59, 59, 123456789, time.UTC)

	s1 := o(t, clk.Now)
	must(t, s1.Resources().Put(c, state.Resource{Server: "main", Kind: state.KindRole, Key: "mod", ID: "42", Owner: state.OwnerGit, UpdatedAt: when}))
	must(t, s1.KV("p").Set(c, "\x00k", json.RawMessage(`{"a":1}`), time.Hour))
	id1, err := s1.Audit().Append(c, state.AuditEntry{Server: "main", Actor: "cicord", Action: "sync.apply"})
	must(t, err)
	must(t, s1.Close())

	s2 := o(t, clk.Now)
	r, err := s2.Resources().Get(c, "main", state.KindRole, "mod")
	must(t, err)
	if r.ID != "42" {
		t.Errorf("reopened resource ID %q", r.ID)
	}
	assertTime(t, "UpdatedAt after reopen", r.UpdatedAt, when)
	val, err := s2.KV("p").Get(c, "\x00k")
	must(t, err)
	if string(val) != `{"a":1}` {
		t.Errorf("reopened kv value %s", val)
	}
	id2, err := s2.Audit().Append(c, state.AuditEntry{Action: "again"})
	must(t, err)
	if id2 <= id1 {
		t.Errorf("audit ID after reopen %d is not after %d", id2, id1)
	}
	must(t, s2.Close())

	// A third handle sees both entries; a still-open handle sees writes of another.
	s3, s4 := o(t, clk.Now), o(t, clk.Now)
	all, err := s3.Audit().List(c, state.AuditFilter{})
	must(t, err)
	if len(all) != 2 || all[0].ID != id2 || all[1].ID != id1 {
		t.Errorf("audit entries after reopen = %+v", all)
	}
	must(t, s4.KV("p").Set(c, "live", json.RawMessage(`true`), 0))
	if v, err := s3.KV("p").Get(c, "live"); err != nil || string(v) != "true" {
		t.Errorf("write of another open handle: %s, %v", v, err)
	}
}

// Empty results have the same shape on every backend (they may be encoded as
// JSON, where nil and empty slices differ): nil for the list methods, a
// non-nil empty slice for KV.List.
func testEmptyResults(t *testing.T, b Backend) {
	s, clk := open(t, b)
	c := ctx(t)
	if rs, err := s.Resources().List(c, "none"); err != nil || rs != nil {
		t.Errorf("Resources.List = %#v, %v; want nil", rs, err)
	}
	if ts, err := s.Tasks().Due(c, clk.Now(), 0); err != nil || ts != nil {
		t.Errorf("Due = %#v, %v; want nil", ts, err)
	}
	if es, err := s.Audit().List(c, state.AuditFilter{}); err != nil || es != nil {
		t.Errorf("Audit.List = %#v, %v; want nil", es, err)
	}
	if rs, err := s.Syncs().List(c, "", 0); err != nil || rs != nil {
		t.Errorf("Syncs.List = %#v, %v; want nil", rs, err)
	}
	if items, next, err := s.KV("s").List(c, "", 0, ""); err != nil || items == nil || len(items) != 0 || next != "" {
		t.Errorf("KV.List = %#v, %q, %v; want an empty non-nil slice", items, next, err)
	}
}
