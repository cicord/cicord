package statetest

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// The analytics part of the suite (state.Analytics and state.AnalyticsTotals).

var analyticsTests = []struct {
	name string
	fn   func(t *testing.T, b Backend)
}{
	{"AnalyticsMerge", testAnalyticsMerge},
	{"AnalyticsHourTruncation", testAnalyticsTruncation},
	{"AnalyticsQuery", testAnalyticsQuery},
	{"AnalyticsQueryOrderIsByteOrder", testAnalyticsOrder},
	{"AnalyticsDims", testAnalyticsDims},
	{"AnalyticsTotals", testAnalyticsTotals},
	{"AnalyticsValidation", testAnalyticsValidation},
	{"AnalyticsActive", testAnalyticsActive},
	{"AnalyticsPurgeAndSince", testAnalyticsPurge},
	{"AnalyticsConcurrent", testAnalyticsConcurrent},
	{"AnalyticsConcurrentAcrossHandles", testAnalyticsAcrossHandles},
	{"AnalyticsEmptyResults", testAnalyticsEmpty},
}

// hour0 is an hour bucket used by the analytics tests.
var hour0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func hourN(n int) time.Time { return hour0.Add(time.Duration(n) * time.Hour) }

func pt(server, metric, dim string, bucket time.Time, v float64) state.AnalyticsPoint {
	return state.AnalyticsPoint{Server: server, Metric: metric, Dim: dim, Bucket: bucket, Value: v}
}

func record(t *testing.T, a state.Analytics, kind state.AnalyticsKind, ps ...state.AnalyticsPoint) {
	t.Helper()
	must(t, a.Record(t.Context(), kind, ps...))
}

func query(t *testing.T, a state.Analytics, q state.AnalyticsQuery) []state.AnalyticsPoint {
	t.Helper()
	ps, err := a.Query(t.Context(), q)
	must(t, err)
	for _, p := range ps {
		if p.Bucket.Location() != time.UTC {
			t.Errorf("bucket location %v, want UTC", p.Bucket.Location())
		}
		if p.Server != q.Server {
			t.Errorf("point server %q, want %q", p.Server, q.Server)
		}
	}
	return ps
}

// fmtPoints renders points as "metric/dim@hour=value" for comparisons.
func fmtPoints(ps []state.AnalyticsPoint) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = fmt.Sprintf("%s/%s@%d=%g", p.Metric, p.Dim, int(p.Bucket.Sub(hour0)/time.Hour), p.Value)
	}
	return out
}

func wantPoints(t *testing.T, what string, got []state.AnalyticsPoint, want ...string) {
	t.Helper()
	if g := fmtPoints(got); !slices.Equal(g, want) {
		t.Errorf("%s =\n  %v\nwant\n  %v", what, g, want)
	}
}

func testAnalyticsMerge(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	all := state.AnalyticsQuery{Server: "main"}

	// Counters add, across calls and within one call (same row twice).
	record(t, a, state.AnalyticsCounter, pt("main", "c", "", hourN(0), 2), pt("main", "c", "", hourN(0), 3))
	record(t, a, state.AnalyticsCounter, pt("main", "c", "", hourN(0), 0.5), pt("main", "c", "", hourN(1), 1))
	// Max keeps the largest value.
	record(t, a, state.AnalyticsGaugeMax, pt("main", "m", "", hourN(0), 5), pt("main", "m", "", hourN(0), 9), pt("main", "m", "", hourN(0), 7))
	record(t, a, state.AnalyticsGaugeMax, pt("main", "m", "", hourN(0), 8))
	record(t, a, state.AnalyticsGaugeMax, pt("main", "m", "", hourN(1), -3))
	// Last keeps the most recent write; within one call the last point wins.
	record(t, a, state.AnalyticsGaugeLast, pt("main", "l", "x", hourN(0), 4), pt("main", "l", "x", hourN(0), 1))
	record(t, a, state.AnalyticsGaugeLast, pt("main", "l", "y", hourN(0), 10))
	record(t, a, state.AnalyticsGaugeLast, pt("main", "l", "y", hourN(0), 6))
	// No points: no-op.
	record(t, a, state.AnalyticsCounter)

	wantPoints(t, "merged points", query(t, a, all),
		"c/@0=5.5", "c/@1=1", "l/x@0=1", "l/y@0=6", "m/@0=9", "m/@1=-3")
}

func testAnalyticsTruncation(t *testing.T, b Backend) {
	s, clk := open(t, b)
	a := s.Analytics()
	ist := time.FixedZone("IST", 5*3600+1800)
	// 16:07:12.5 IST = 10:37:12.5 UTC → bucket 10:00 UTC.
	record(t, a, state.AnalyticsCounter,
		pt("main", "c", "", time.Date(2026, 11, 2, 16, 7, 12, 500, ist), 1),
		pt("main", "c", "", hourN(0).Add(59*time.Minute+59*time.Second+999999999), 1),
		pt("main", "c", "", hourN(1), 1))
	ps := query(t, a, state.AnalyticsQuery{Server: "main"})
	wantPoints(t, "truncated", ps, "c/@0=2", "c/@1=1")
	if len(ps) > 0 {
		assertTime(t, "bucket", ps[0].Bucket, hour0)
	}
	// A zero bucket is the store's clock, truncated.
	record(t, a, state.AnalyticsCounter, pt("other", "z", "", time.Time{}, 1))
	ps = query(t, a, state.AnalyticsQuery{Server: "other"})
	if len(ps) != 1 {
		t.Fatalf("zero bucket: %v", fmtPoints(ps))
	}
	assertTime(t, "zero bucket", ps[0].Bucket, clk.Now().UTC().Truncate(time.Hour))
}

func testAnalyticsQuery(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	for h := range 5 {
		record(t, a, state.AnalyticsCounter,
			pt("main", "msg", "c1", hourN(h), float64(h+1)),
			pt("main", "msg", "c2", hourN(h), 10),
			pt("main", "join", "", hourN(h), 1),
			pt("other", "msg", "c1", hourN(h), 100))
	}
	record(t, a, state.AnalyticsGaugeLast, pt("main", "total", "", hourN(2), 42))

	// [From, To): From included, To excluded; times inside an hour round up.
	wantPoints(t, "range", query(t, a, state.AnalyticsQuery{Server: "main", Metrics: []string{"join"}, From: hourN(1), To: hourN(3)}),
		"join/@1=1", "join/@2=1")
	wantPoints(t, "unaligned range", query(t, a, state.AnalyticsQuery{Server: "main", Metrics: []string{"join"},
		From: hourN(1).Add(time.Nanosecond), To: hourN(3).Add(time.Nanosecond)}),
		"join/@2=1", "join/@3=1")
	// Zero From / To: unbounded.
	if ps := query(t, a, state.AnalyticsQuery{Server: "main", Metrics: []string{"join"}, To: hourN(2)}); len(ps) != 2 {
		t.Errorf("zero From: %v", fmtPoints(ps))
	}
	if ps := query(t, a, state.AnalyticsQuery{Server: "main", Metrics: []string{"join"}, From: hourN(3)}); len(ps) != 2 {
		t.Errorf("zero To: %v", fmtPoints(ps))
	}
	// Metrics filter, ordered by metric, dim, bucket.
	wantPoints(t, "two metrics", query(t, a, state.AnalyticsQuery{Server: "main", Metrics: []string{"total", "msg"}, From: hourN(3)}),
		"msg/c1@3=4", "msg/c1@4=5", "msg/c2@3=10", "msg/c2@4=10")
	// Dim filter, including the empty dimension.
	c2, none := "c2", ""
	wantPoints(t, "dim", query(t, a, state.AnalyticsQuery{Server: "main", Dim: &c2, From: hourN(4)}), "msg/c2@4=10")
	wantPoints(t, "empty dim", query(t, a, state.AnalyticsQuery{Server: "main", Dim: &none, From: hourN(2), To: hourN(3)}),
		"join/@2=1", "total/@2=42")
	// Servers are isolated.
	wantPoints(t, "other server", query(t, a, state.AnalyticsQuery{Server: "other", From: hourN(4)}), "msg/c1@4=100")
	if ps := query(t, a, state.AnalyticsQuery{Server: "nope"}); len(ps) != 0 {
		t.Errorf("unknown server: %v", fmtPoints(ps))
	}
	if ps := query(t, a, state.AnalyticsQuery{Server: "main", Metrics: []string{"nope"}}); len(ps) != 0 {
		t.Errorf("unknown metric: %v", fmtPoints(ps))
	}
}

// Dimensions are ordered in byte order, like the rest of the store, whatever the
// database collation.
func testAnalyticsOrder(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	dims := []string{">=1y", "<1d", "B", "a", "_x", "é", "😀", "1234567890123456789", "name:123", ""}
	var ps []state.AnalyticsPoint
	for _, d := range dims {
		ps = append(ps, pt("main", "m", d, hourN(0), 1))
	}
	record(t, a, state.AnalyticsCounter, ps...)
	got := query(t, a, state.AnalyticsQuery{Server: "main"})
	var order []string
	for _, p := range got {
		order = append(order, p.Dim)
	}
	want := slices.Clone(dims)
	slices.Sort(want)
	if !slices.Equal(order, want) {
		t.Errorf("Query dim order = %q, want byte order %q", order, want)
	}
	// Dims ties (equal values) are in byte order too.
	ds, err := a.Dims(t.Context(), "main", "m", time.Time{}, time.Time{}, 0)
	must(t, err)
	order = order[:0]
	for _, d := range ds {
		order = append(order, d.Dim)
	}
	if !slices.Equal(order, want) {
		t.Errorf("Dims tie order = %q, want %q", order, want)
	}
}

func dimsOf(t *testing.T, a state.Analytics, metric string, from, to time.Time, limit int) []string {
	t.Helper()
	ds, err := a.Dims(t.Context(), "main", metric, from, to, limit)
	must(t, err)
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = fmt.Sprintf("%s=%g", d.Dim, d.Value)
	}
	return out
}

func testAnalyticsDims(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	// Counter: summed over the range.
	record(t, a, state.AnalyticsCounter,
		pt("main", "msg", "a", hourN(0), 1), pt("main", "msg", "a", hourN(1), 5),
		pt("main", "msg", "b", hourN(0), 4), pt("main", "msg", "b", hourN(1), 3),
		pt("main", "msg", "c", hourN(1), 7), pt("main", "msg", "d", hourN(5), 100),
		pt("other", "msg", "z", hourN(0), 1000))
	if got, want := dimsOf(t, a, "msg", hourN(0), hourN(2), 0), []string{"b=7", "c=7", "a=6"}; !slices.Equal(got, want) {
		t.Errorf("counter dims = %v, want %v", got, want)
	}
	if got, want := dimsOf(t, a, "msg", hourN(0), hourN(2), 2), []string{"b=7", "c=7"}; !slices.Equal(got, want) {
		t.Errorf("limited dims = %v, want %v", got, want)
	}
	if got, want := dimsOf(t, a, "msg", time.Time{}, time.Time{}, -1), []string{"d=100", "b=7", "c=7", "a=6"}; !slices.Equal(got, want) {
		t.Errorf("unbounded dims = %v, want %v", got, want)
	}
	// Gauges: the max over the range, not the sum.
	record(t, a, state.AnalyticsGaugeLast,
		pt("main", "status", "online", hourN(0), 10), pt("main", "status", "online", hourN(1), 12),
		pt("main", "status", "idle", hourN(0), 11), pt("main", "status", "idle", hourN(1), 2))
	record(t, a, state.AnalyticsGaugeMax, pt("main", "peak", "", hourN(0), 3), pt("main", "peak", "", hourN(1), 8))
	if got, want := dimsOf(t, a, "status", hourN(0), hourN(2), 0), []string{"online=12", "idle=11"}; !slices.Equal(got, want) {
		t.Errorf("gauge last dims = %v, want %v", got, want)
	}
	if got, want := dimsOf(t, a, "peak", hourN(0), hourN(2), 0), []string{"=8"}; !slices.Equal(got, want) {
		t.Errorf("gauge max dims = %v, want %v", got, want)
	}
	// Default limit 100.
	var many []state.AnalyticsPoint
	for i := range 150 {
		many = append(many, pt("main", "many", fmt.Sprintf("d%03d", i), hourN(0), float64(i)))
	}
	record(t, a, state.AnalyticsCounter, many...)
	if got := dimsOf(t, a, "many", time.Time{}, time.Time{}, 0); len(got) != 100 || got[0] != "d149=149" {
		t.Errorf("default limit: %d dims, first %v", len(got), got[:min(1, len(got))])
	}
	if got := dimsOf(t, a, "many", time.Time{}, time.Time{}, 120); len(got) != 120 {
		t.Errorf("limit 120: %d dims", len(got))
	}
}

func testAnalyticsTotals(t *testing.T, b Backend) {
	s, _ := open(t, b)
	tot, ok := s.Analytics().(state.AnalyticsTotals)
	if !ok {
		t.Fatal("the backend's Analytics does not implement state.AnalyticsTotals")
	}
	a := s.Analytics()
	record(t, a, state.AnalyticsCounter,
		pt("main", "msg", "a", hourN(0), 1), pt("main", "msg", "b", hourN(0), 2),
		pt("main", "msg", "a", hourN(1), 4), pt("main", "join", "", hourN(1), 1),
		pt("other", "msg", "a", hourN(0), 100))
	record(t, a, state.AnalyticsGaugeLast, pt("main", "status", "online", hourN(0), 5), pt("main", "status", "idle", hourN(0), 6))
	a1 := "a"
	ps, err := tot.Totals(t.Context(), state.AnalyticsQuery{Server: "main", Dim: &a1, To: hourN(2)})
	must(t, err)
	wantPoints(t, "totals (Dim ignored)", ps, "join/@1=1", "msg/@0=3", "msg/@1=4", "status/@0=11")
	ps, err = tot.Totals(t.Context(), state.AnalyticsQuery{Server: "main", Metrics: []string{"msg"}, From: hourN(1)})
	must(t, err)
	wantPoints(t, "totals of one metric", ps, "msg/@1=4")
}

func testAnalyticsValidation(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	good := pt("main", "m", "", hourN(0), 1)
	for name, c := range map[string]struct {
		kind state.AnalyticsKind
		p    state.AnalyticsPoint
	}{
		"unknown kind": {"sum", good},
		"empty kind":   {"", good},
		"empty server": {state.AnalyticsCounter, pt("", "m", "", hourN(0), 1)},
		"empty metric": {state.AnalyticsCounter, pt("main", "", "", hourN(0), 1)},
		"NaN":          {state.AnalyticsCounter, pt("main", "m", "", hourN(0), math.NaN())},
		"+Inf":         {state.AnalyticsGaugeLast, pt("main", "m", "", hourN(0), math.Inf(1))},
		"-Inf":         {state.AnalyticsGaugeMax, pt("main", "m", "", hourN(0), math.Inf(-1))},
		"out of range": {state.AnalyticsCounter, pt("main", "m", "", time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), 1)},
	} {
		// The valid point of the same call is not written either (atomic).
		if err := a.Record(t.Context(), c.kind, good, c.p); err == nil {
			t.Errorf("%s: Record succeeded", name)
		}
	}
	if ps := query(t, a, state.AnalyticsQuery{Server: "main"}); len(ps) != 0 {
		t.Errorf("rejected Records wrote %v", fmtPoints(ps))
	}
	for _, bad := range []string{"", "2026-1-2", "2026-13-01", "20261102", "2026-11-02T00", "2026-11-0x"} {
		if err := a.MarkActive(t.Context(), "main", "all", bad, "tok"); err == nil {
			t.Errorf("MarkActive with day %q succeeded", bad)
		}
		if _, err := a.DropActive(t.Context(), bad); err == nil {
			t.Errorf("DropActive(%q) succeeded", bad)
		}
	}
	for name, args := range map[string][3]string{"server": {"", "all", "t"}, "set": {"main", "", "t"}, "token": {"main", "all", ""}} {
		if err := a.MarkActive(t.Context(), args[0], args[1], "2026-11-02", args[2]); err == nil {
			t.Errorf("MarkActive with empty %s succeeded", name)
		}
	}
}

func countActive(t *testing.T, a state.Analytics, server, set, day string) int64 {
	t.Helper()
	n, err := a.CountActive(t.Context(), server, set, day)
	must(t, err)
	return n
}

func testAnalyticsActive(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	c := t.Context()
	for _, m := range [][4]string{
		{"main", "all", "2026-11-01", "t1"},
		{"main", "all", "2026-11-01", "t2"},
		{"main", "all", "2026-11-01", "t1"}, // again: no-op
		{"main", "chat", "2026-11-01", "t1"},
		{"main", "all", "2026-11-02", "t1"},
		{"main", "all", "2026-11-03", "t3"},
		{"other", "all", "2026-11-01", "t1"},
	} {
		must(t, a.MarkActive(c, m[0], m[1], m[2], m[3]))
	}
	for _, w := range []struct {
		server, set, day string
		n                int64
	}{
		{"main", "all", "2026-11-01", 2}, {"main", "chat", "2026-11-01", 1}, {"main", "voice", "2026-11-01", 0},
		{"main", "all", "2026-11-02", 1}, {"other", "all", "2026-11-01", 1}, {"nope", "all", "2026-11-01", 0},
	} {
		if n := countActive(t, a, w.server, w.set, w.day); n != w.n {
			t.Errorf("CountActive(%s, %s, %s) = %d, want %d", w.server, w.set, w.day, n, w.n)
		}
	}
	// Strictly before: 2026-11-01 of every server and set goes, 2026-11-02 stays.
	n, err := a.DropActive(c, "2026-11-02")
	must(t, err)
	if n != 4 {
		t.Errorf("DropActive removed %d tokens, want 4", n)
	}
	if countActive(t, a, "main", "all", "2026-11-01") != 0 || countActive(t, a, "other", "all", "2026-11-01") != 0 ||
		countActive(t, a, "main", "chat", "2026-11-01") != 0 {
		t.Error("tokens of the dropped day remain")
	}
	if countActive(t, a, "main", "all", "2026-11-02") != 1 || countActive(t, a, "main", "all", "2026-11-03") != 1 {
		t.Error("DropActive removed tokens of later days")
	}
	if n, err := a.DropActive(c, "2026-11-02"); err != nil || n != 0 {
		t.Errorf("second DropActive = %d, %v", n, err)
	}
}

func testAnalyticsPurge(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	c := t.Context()
	if since, err := a.Since(c, "main"); err != nil || !since.IsZero() {
		t.Errorf("Since of an empty server = %v, %v; want the zero time", since, err)
	}
	for h := range 4 {
		record(t, a, state.AnalyticsCounter, pt("main", "m", "x", hourN(h), 1), pt("main", "n", "", hourN(h), 1),
			pt("other", "m", "", hourN(h), 1))
	}
	since, err := a.Since(c, "main")
	must(t, err)
	assertTime(t, "Since", since, hourN(0))

	// Strictly before: the bucket equal to before stays.
	n, err := a.Purge(c, "main", hourN(2))
	must(t, err)
	if n != 4 {
		t.Errorf("Purge(main) removed %d rows, want 4", n)
	}
	wantPoints(t, "after purge", query(t, a, state.AnalyticsQuery{Server: "main"}), "m/x@2=1", "m/x@3=1", "n/@2=1", "n/@3=1")
	since, err = a.Since(c, "main")
	must(t, err)
	assertTime(t, "Since after purge", since, hourN(2))
	if ps := query(t, a, state.AnalyticsQuery{Server: "other"}); len(ps) != 4 {
		t.Errorf("Purge(main) touched other: %v", fmtPoints(ps))
	}
	// A time inside an hour keeps that hour's bucket.
	if n, err := a.Purge(c, "main", hourN(2).Add(-time.Nanosecond)); err != nil || n != 0 {
		t.Errorf("Purge just before a bucket = %d, %v", n, err)
	}
	// Every server.
	n, err = a.Purge(c, "", hourN(3))
	must(t, err)
	if n != 5 {
		t.Errorf("Purge(all) removed %d rows, want 5", n)
	}
	wantPoints(t, "main after purge all", query(t, a, state.AnalyticsQuery{Server: "main"}), "m/x@3=1", "n/@3=1")
	wantPoints(t, "other after purge all", query(t, a, state.AnalyticsQuery{Server: "other"}), "m/@3=1")
	n, err = a.Purge(c, "", hourN(10))
	must(t, err)
	if n != 3 {
		t.Errorf("final Purge removed %d rows, want 3", n)
	}
	if since, err := a.Since(c, "other"); err != nil || !since.IsZero() {
		t.Errorf("Since after purging everything = %v, %v", since, err)
	}
}

// analyticsHammer records counter increments from many goroutines, each call
// touching the same rows in a different order, and checks that nothing is lost
// (and that the backend does not deadlock).
func analyticsHammer(t *testing.T, stores []state.Store) {
	const workers, perG = 8, 20
	keys := []state.AnalyticsPoint{
		pt("main", "msg", "a", hourN(0), 1), pt("main", "msg", "b", hourN(0), 1),
		pt("main", "msg", "a", hourN(1), 1), pt("main", "join", "", hourN(0), 1),
	}
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		a := stores[w%len(stores)].Analytics()
		wg.Go(func() {
			for i := range perG {
				ps := slices.Clone(keys)
				if (w+i)%2 == 1 {
					slices.Reverse(ps)
				}
				if err := a.Record(t.Context(), state.AnalyticsCounter, ps...); err != nil {
					errs <- err
					return
				}
				if err := a.MarkActive(t.Context(), "main", "all", "2026-11-02", fmt.Sprintf("t%d", i)); err != nil {
					errs <- err
					return
				}
				if err := a.Record(t.Context(), state.AnalyticsGaugeMax, pt("main", "peak", "", hourN(0), float64(w*perG+i))); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	a := stores[0].Analytics()
	const n = workers * perG
	wantPoints(t, "concurrent records", query(t, a, state.AnalyticsQuery{Server: "main"}),
		fmt.Sprintf("join/@0=%d", n), fmt.Sprintf("msg/a@0=%d", n), fmt.Sprintf("msg/a@1=%d", n), fmt.Sprintf("msg/b@0=%d", n),
		fmt.Sprintf("peak/@0=%d", n-1))
	if c := countActive(t, a, "main", "all", "2026-11-02"); c != perG {
		t.Errorf("concurrent MarkActive: %d tokens, want %d", c, perG)
	}
}

func testAnalyticsConcurrent(t *testing.T, b Backend) {
	s, _ := open(t, b)
	analyticsHammer(t, []state.Store{s})
}

func testAnalyticsAcrossHandles(t *testing.T, b Backend) {
	if !b.SharedHandles {
		t.Skip("backend has no shared handles")
	}
	o := b.NewDatabase(t)
	clk := NewClock()
	analyticsHammer(t, []state.Store{o(t, clk.Now), o(t, clk.Now), o(t, clk.Now)})
}

// Empty results are nil, like the other list methods.
func testAnalyticsEmpty(t *testing.T, b Backend) {
	s, _ := open(t, b)
	a := s.Analytics()
	c := t.Context()
	if ps, err := a.Query(c, state.AnalyticsQuery{Server: "none"}); err != nil || ps != nil {
		t.Errorf("Query = %#v, %v; want nil", ps, err)
	}
	if ds, err := a.Dims(c, "none", "m", time.Time{}, time.Time{}, 0); err != nil || ds != nil {
		t.Errorf("Dims = %#v, %v; want nil", ds, err)
	}
	if tot, ok := a.(state.AnalyticsTotals); ok {
		if ps, err := tot.Totals(c, state.AnalyticsQuery{Server: "none"}); err != nil || ps != nil {
			t.Errorf("Totals = %#v, %v; want nil", ps, err)
		}
	}
	if n, err := a.CountActive(c, "none", "all", "2026-11-02"); err != nil || n != 0 {
		t.Errorf("CountActive = %d, %v", n, err)
	}
	if n, err := a.Purge(c, "none", hourN(0)); err != nil || n != 0 {
		t.Errorf("Purge = %d, %v", n, err)
	}
}
