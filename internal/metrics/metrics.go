// Package metrics is a small Prometheus instrumentation library: counters, histograms and
// scrape-time (function) metrics, written in the Prometheus text exposition format 0.0.4
// (https://prometheus.io/docs/instrumenting/exposition_formats/). It replaces
// github.com/prometheus/client_golang to keep the runtime's dependency set small; it only
// implements what the runtime needs.
//
// Label values must have a bounded cardinality: configuration keys (server, bot, plugin
// instance), route patterns, fixed enumerations. Never user ids, message content or raw
// request paths.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Type is a metric family type.
type Type string

const (
	Counter   Type = "counter"
	Gauge     Type = "gauge"
	Histogram Type = "histogram"
)

// ContentType is the media type of the text exposition format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

var (
	nameRe  = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// Registry holds metric families. It is safe for concurrent use.
type Registry struct {
	mu       sync.Mutex
	families map[string]collector
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{families: map[string]collector{}} }

// sample is one series of a family at scrape time.
type sample struct {
	labels []string // values, in the order of the family's label names
	value  float64
	hist   *histSnapshot // histograms only
}

type histSnapshot struct {
	counts []uint64 // cumulative, one per bucket upper bound (without +Inf)
	count  uint64
	sum    float64
}

type collector interface {
	describe() *desc
	collect() []sample
}

type desc struct {
	name, help string
	typ        Type
	labels     []string
	buckets    []float64 // histograms
}

func (d *desc) describe() *desc { return d }

// register adds c. Registering a counter or histogram again with the same description
// returns the registered one (an API handler rebuilt on the same registry keeps counting
// into the same series); any other duplicate name panics.
func (r *Registry) register(c collector) collector {
	d := c.describe()
	if !nameRe.MatchString(d.name) {
		panic(fmt.Sprintf("metrics: invalid metric name %q", d.name))
	}
	for _, l := range d.labels {
		if !labelRe.MatchString(l) || strings.HasPrefix(l, "__") || (d.typ == Histogram && l == "le") {
			panic(fmt.Sprintf("metrics: invalid label name %q on %s", l, d.name))
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, dup := r.families[d.name]; dup {
		_, isFunc := c.(*funcMetric)
		if p := prev.describe(); isFunc || p.help != d.help || p.typ != d.typ ||
			!slices.Equal(p.labels, d.labels) || !slices.Equal(p.buckets, d.buckets) {
			panic(fmt.Sprintf("metrics: %s registered twice", d.name))
		}
		return prev
	}
	r.families[d.name] = c
	return c
}

// ───────────────────────────────────────────────────────────── vectors

// seriesKey joins label values (a separator that cannot appear in valid UTF-8 text).
func seriesKey(d *desc, values []string) string {
	if len(values) != len(d.labels) {
		panic(fmt.Sprintf("metrics: %s takes %d label value(s), got %d", d.name, len(d.labels), len(values)))
	}
	return strings.Join(values, "\xff")
}

// CounterVec is a counter with labels. Series appear on first use.
type CounterVec struct {
	desc
	mu     sync.Mutex
	series map[string]*sample
}

// NewCounter registers a counter (its name should end in _total).
func (r *Registry) NewCounter(name, help string, labels ...string) *CounterVec {
	c := &CounterVec{desc: desc{name: name, help: help, typ: Counter, labels: labels}, series: map[string]*sample{}}
	return r.register(c).(*CounterVec)
}

// Add adds v (>= 0) to the series of the label values.
func (c *CounterVec) Add(v float64, values ...string) {
	if v < 0 || math.IsNaN(v) {
		return // counters only go up
	}
	k := seriesKey(&c.desc, values)
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.series[k]
	if s == nil {
		s = &sample{labels: slices.Clone(values)}
		c.series[k] = s
	}
	s.value += v
}

// Inc adds 1.
func (c *CounterVec) Inc(values ...string) { c.Add(1, values...) }

func (c *CounterVec) collect() []sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]sample, 0, len(c.series))
	for _, s := range c.series {
		out = append(out, *s)
	}
	return out
}

// DefBuckets are latency buckets in seconds, from 1 ms to 10 s.
var DefBuckets = []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// HistogramVec is a histogram with labels. Series appear on first use.
type HistogramVec struct {
	desc
	mu     sync.Mutex
	series map[string]*histSeries
}

type histSeries struct {
	labels []string
	counts []uint64 // per bucket, not cumulative
	count  uint64
	sum    float64
}

// NewHistogram registers a histogram with the given bucket upper bounds (sorted, without
// +Inf; nil means DefBuckets).
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...string) *HistogramVec {
	if buckets == nil {
		buckets = DefBuckets
	}
	if !sort.Float64sAreSorted(buckets) || len(slices.Compact(slices.Clone(buckets))) != len(buckets) {
		panic(fmt.Sprintf("metrics: %s buckets must be sorted and distinct", name))
	}
	h := &HistogramVec{desc: desc{name: name, help: help, typ: Histogram, labels: labels, buckets: slices.Clone(buckets)},
		series: map[string]*histSeries{}}
	return r.register(h).(*HistogramVec)
}

// Observe records v in the series of the label values.
func (h *HistogramVec) Observe(v float64, values ...string) {
	if math.IsNaN(v) {
		return
	}
	k := seriesKey(&h.desc, values)
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[k]
	if s == nil {
		s = &histSeries{labels: slices.Clone(values), counts: make([]uint64, len(h.buckets))}
		h.series[k] = s
	}
	if i := sort.SearchFloat64s(h.buckets, v); i < len(h.buckets) {
		s.counts[i]++ // the first bucket whose upper bound is >= v
	}
	s.count++
	s.sum += v
}

func (h *HistogramVec) collect() []sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]sample, 0, len(h.series))
	for _, s := range h.series {
		snap := &histSnapshot{counts: make([]uint64, len(s.counts)), count: s.count, sum: s.sum}
		var cum uint64
		for i, n := range s.counts {
			cum += n
			snap.counts[i] = cum
		}
		out = append(out, sample{labels: s.labels, hist: snap})
	}
	return out
}

// ───────────────────────────────────────────────────────────── scrape-time metrics

// Emit reports one series of a function metric: its value and label values.
type Emit func(value float64, values ...string)

type funcMetric struct {
	desc
	fn func(Emit)
}

// NewFunc registers a counter or gauge computed at every scrape: fn calls emit once per
// series. fn must be quick and safe for concurrent use.
func (r *Registry) NewFunc(typ Type, name, help string, labels []string, fn func(Emit)) {
	if typ != Counter && typ != Gauge {
		panic(fmt.Sprintf("metrics: %s: function metrics are counters or gauges", name))
	}
	r.register(&funcMetric{desc: desc{name: name, help: help, typ: typ, labels: labels}, fn: fn})
}

func (f *funcMetric) collect() []sample {
	var out []sample
	f.fn(func(v float64, values ...string) {
		seriesKey(&f.desc, values) // checks the label count
		out = append(out, sample{labels: slices.Clone(values), value: v})
	})
	return out
}

// ───────────────────────────────────────────────────────────── exposition

// Write writes every family in the text exposition format, sorted by name then labels.
func (r *Registry) Write(w io.Writer) error {
	r.mu.Lock()
	cs := make([]collector, 0, len(r.families))
	for _, c := range r.families {
		cs = append(cs, c)
	}
	r.mu.Unlock()
	sort.Slice(cs, func(i, j int) bool { return cs[i].describe().name < cs[j].describe().name })

	var b strings.Builder
	for _, c := range cs {
		d := c.describe()
		samples := c.collect()
		if len(samples) == 0 {
			continue
		}
		sort.Slice(samples, func(i, j int) bool { return slices.Compare(samples[i].labels, samples[j].labels) < 0 })
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", d.name, escapeHelp(d.help), d.name, d.typ)
		for _, s := range samples {
			if s.hist == nil {
				writeSeries(&b, d.name, d.labels, s.labels, "", "", s.value)
				continue
			}
			for i, ub := range d.buckets {
				writeSeries(&b, d.name+"_bucket", d.labels, s.labels, "le", formatFloat(ub), float64(s.hist.counts[i]))
			}
			writeSeries(&b, d.name+"_bucket", d.labels, s.labels, "le", "+Inf", float64(s.hist.count))
			writeSeries(&b, d.name+"_sum", d.labels, s.labels, "", "", s.hist.sum)
			writeSeries(&b, d.name+"_count", d.labels, s.labels, "", "", float64(s.hist.count))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeSeries(b *strings.Builder, name string, names, values []string, extraName, extraValue string, v float64) {
	b.WriteString(name)
	if len(names) > 0 || extraName != "" {
		b.WriteByte('{')
		sep := ""
		for i, n := range names {
			fmt.Fprintf(b, `%s%s="%s"`, sep, n, escapeLabel(values[i]))
			sep = ","
		}
		if extraName != "" {
			fmt.Fprintf(b, `%s%s="%s"`, sep, extraName, extraValue)
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(formatFloat(v))
	b.WriteByte('\n')
}

var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Handler serves the registry: GET (or HEAD) only, any path it is mounted on.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var b strings.Builder
		if err := r.Write(&b); err != nil {
			http.Error(w, "metrics unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ContentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if req.Method == http.MethodHead {
			return
		}
		_, _ = io.WriteString(w, b.String())
	})
}
