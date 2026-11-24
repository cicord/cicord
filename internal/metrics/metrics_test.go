package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func scrape(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	if err := r.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestExpositionFormat(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("test_requests_total", "Requests.\nSecond line with a \\ backslash.", "route", "status")
	c.Inc("/b", "2xx")
	c.Add(2, "/a", "5xx")
	c.Inc("/a", "5xx")
	c.Add(-1, "/a", "5xx") // ignored: counters only go up
	c.Inc(`quote " back \ nl `+"\n", "4xx")
	h := r.NewHistogram("test_duration_seconds", "Durations.", []float64{0.1, 1}, "kind")
	h.Observe(0.05, "event")
	h.Observe(0.1, "event") // le is inclusive
	h.Observe(0.5, "event")
	h.Observe(3, "event")
	r.NewFunc(Gauge, "test_up", "Up.", nil, func(emit Emit) { emit(1) })
	r.NewFunc(Gauge, "test_empty", "Never emitted.", []string{"x"}, func(Emit) {})

	want := `# HELP test_duration_seconds Durations.
# TYPE test_duration_seconds histogram
test_duration_seconds_bucket{kind="event",le="0.1"} 2
test_duration_seconds_bucket{kind="event",le="1"} 3
test_duration_seconds_bucket{kind="event",le="+Inf"} 4
test_duration_seconds_sum{kind="event"} 3.65
test_duration_seconds_count{kind="event"} 4
# HELP test_requests_total Requests.\nSecond line with a \\ backslash.
# TYPE test_requests_total counter
test_requests_total{route="/a",status="5xx"} 3
test_requests_total{route="/b",status="2xx"} 1
test_requests_total{route="quote \" back \\ nl \n",status="4xx"} 1
# HELP test_up Up.
# TYPE test_up gauge
test_up 1
`
	if got := scrape(t, r); got != want {
		t.Errorf("exposition:\n%s\nwant:\n%s", got, want)
	}
}

func TestRegistrationErrors(t *testing.T) {
	cases := map[string]func(r *Registry){
		"duplicate": func(r *Registry) {
			r.NewCounter("dup_total", "")
			r.NewCounter("dup_total", "")
		},
		"bad name":       func(r *Registry) { r.NewCounter("bad-name", "") },
		"bad label":      func(r *Registry) { r.NewCounter("ok_total", "", "bad-label") },
		"reserved label": func(r *Registry) { r.NewCounter("ok_total", "", "__name") },
		"le":             func(r *Registry) { r.NewHistogram("h_seconds", "", nil, "le") },
		"unsorted":       func(r *Registry) { r.NewHistogram("h_seconds", "", []float64{1, 0.5}) },
		"label count":    func(r *Registry) { r.NewCounter("n_total", "", "a").Inc() },
		"func histogram": func(r *Registry) { r.NewFunc(Histogram, "f", "", nil, func(Emit) {}) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected a panic")
				}
			}()
			fn(NewRegistry())
		})
	}
}

func TestConcurrentUse(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("c_total", "", "k")
	h := r.NewHistogram("h_seconds", "", nil, "k")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				c.Inc("a")
				h.Observe(0.01, "a")
				_ = r.Write(&strings.Builder{})
			}
		})
	}
	wg.Wait()
	out := scrape(t, r)
	for _, line := range []string{`c_total{k="a"} 8000`, `h_seconds_count{k="a"} 8000`} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("missing %q in\n%s", line, out)
		}
	}
}

func TestHandler(t *testing.T) {
	r := NewRegistry()
	r.NewFunc(Gauge, "x", "X.", nil, func(emit Emit) { emit(42) })
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != ContentType {
		t.Fatalf("GET: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/metrics", nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", resp2.StatusCode)
	}
}

func TestStandardCollectors(t *testing.T) {
	r := NewRegistry()
	RegisterBuildInfo(r, "1.2.3", "abc1234")
	RegisterGo(r)
	RegisterProcess(r)
	out := scrape(t, r)
	for _, want := range []string{
		`cicord_build_info{version="1.2.3",commit="abc1234",goversion="go`,
		"# TYPE go_goroutines gauge\ngo_goroutines ",
		"# TYPE go_gc_cycles_total counter\n",
		"go_memstats_heap_alloc_bytes ",
		"go_memstats_sys_bytes ",
		"process_start_time_seconds ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
