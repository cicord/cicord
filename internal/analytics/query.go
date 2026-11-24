package analytics

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// The read side of the analytics API (api.go has the JSON contract): series
// rollups, the summary, the catalog, request validation and limits.

// Limits and defaults of the analytics API.
const (
	MaxRangeDays     = 400  // longest from..to of a series request
	MaxSeriesPoints  = 2000 // most points of one series
	MaxSeriesMetrics = 20   // most metrics of one series request
	DefaultSeriesTop = 8    // dimensions of a series without dim
	MaxSeriesTop     = 50
	RankingSize      = 10 // entries of each summary ranking
)

// Steps of a series.
const (
	StepHour = "hour"
	StepDay  = "day"
	StepWeek = "week"
)

// RequestError is an invalid analytics request (the API answers 400).
type RequestError struct{ Message string }

func (e *RequestError) Error() string { return e.Message }

func badRequest(format string, args ...any) error {
	return &RequestError{Message: fmt.Sprintf(format, args...)}
}

// Reader answers the analytics API from the store.
type Reader struct {
	Store state.Analytics
	// Labeler resolves dimensions (nil: FallbackLabel only).
	Labeler Labeler
	// Now defaults to time.Now.
	Now func() time.Time
}

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// SeriesRequest is a validated …/analytics/series request. From and To are aligned
// on Step.
type SeriesRequest struct {
	Server  string
	Metrics []string
	From    time.Time
	To      time.Time
	Step    string
	// Dim selects one dimension of the metrics that have one (nil: top dimensions).
	Dim *string
	Top int
}

// ParseSeriesRequest validates the query of …/analytics/series: metrics (required,
// comma-separated catalog names), from and to (RFC 3339; to defaults to now, from
// to to minus 7 days), step (hour, day or week; default day), dim and top
// (1-50, default 8). The range is aligned on the step and limited to MaxRangeDays
// days and MaxSeriesPoints points.
func ParseSeriesRequest(server string, q url.Values, now time.Time) (SeriesRequest, error) {
	req := SeriesRequest{Server: server, Step: q.Get("step"), Top: DefaultSeriesTop}
	for m := range strings.SplitSeq(q.Get("metrics"), ",") {
		m = strings.TrimSpace(m)
		if m == "" || slices.Contains(req.Metrics, m) {
			continue
		}
		if _, ok := Lookup(m); !ok {
			return req, badRequest("unknown metric %q (see …/analytics/catalog)", m)
		}
		req.Metrics = append(req.Metrics, m)
	}
	switch {
	case len(req.Metrics) == 0:
		return req, badRequest("metrics is required (comma-separated metric names)")
	case len(req.Metrics) > MaxSeriesMetrics:
		return req, badRequest("at most %d metrics per request", MaxSeriesMetrics)
	}
	switch req.Step {
	case "":
		req.Step = StepDay
	case StepHour, StepDay, StepWeek:
	default:
		return req, badRequest("step must be hour, day or week")
	}
	parse := func(name string) (time.Time, error) {
		t, err := time.Parse(time.RFC3339Nano, q.Get(name))
		if err != nil {
			return t, badRequest("%s must be an RFC 3339 time, e.g. 2026-11-24T00:00:00Z", name)
		}
		return t.UTC(), nil
	}
	to, from := now.UTC(), time.Time{}
	var err error
	if q.Get("to") != "" {
		if to, err = parse("to"); err != nil {
			return req, err
		}
	}
	from = to.AddDate(0, 0, -7)
	if q.Get("from") != "" {
		if from, err = parse("from"); err != nil {
			return req, err
		}
	}
	if !from.Before(to) {
		return req, badRequest("from must be before to")
	}
	if to.Sub(from) > MaxRangeDays*24*time.Hour {
		return req, badRequest("at most %d days per request", MaxRangeDays)
	}
	req.From, req.To = stepFloor(from, req.Step), stepCeil(to, req.Step)
	if n := stepCount(req.From, req.To, req.Step); n > MaxSeriesPoints {
		return req, badRequest("%d points per series with step=%s, at most %d: use a larger step or a shorter range", n, req.Step, MaxSeriesPoints)
	}
	if d := q.Get("dim"); d != "" {
		req.Dim = &d
	}
	if t := q.Get("top"); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 1 || n > MaxSeriesTop {
			return req, badRequest("top must be an integer between 1 and %d", MaxSeriesTop)
		}
		req.Top = n
	}
	return req, nil
}

// ───────────────────────────────────────────────────────────── time steps

func dayFloor(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// stepFloor is the start of the step containing t (UTC hour, day, ISO week).
func stepFloor(t time.Time, step string) time.Time {
	switch step {
	case StepHour:
		return t.UTC().Truncate(time.Hour)
	case StepWeek:
		d := dayFloor(t)
		return d.AddDate(0, 0, -isoWeekday(d))
	}
	return dayFloor(t)
}

// stepCeil is t when it starts a step, otherwise the start of the next step.
func stepCeil(t time.Time, step string) time.Time {
	f := stepFloor(t, step)
	if f.Equal(t) {
		return f
	}
	return stepNext(f, step)
}

func stepNext(t time.Time, step string) time.Time {
	switch step {
	case StepHour:
		return t.Add(time.Hour)
	case StepWeek:
		return t.AddDate(0, 0, 7)
	}
	return t.AddDate(0, 0, 1)
}

func stepCount(from, to time.Time, step string) int {
	h := int(to.Sub(from) / time.Hour)
	switch step {
	case StepDay:
		return h / 24
	case StepWeek:
		return h / (7 * 24)
	}
	return h
}

// isoWeekday is 0 for Monday … 6 for Sunday.
func isoWeekday(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

// ActiveDay is the day key of the active-member sets (state.Analytics MarkActive)
// for t: its UTC date, "YYYY-MM-DD".
func ActiveDay(t time.Time) string { return t.UTC().Format("2006-01-02") }

// ───────────────────────────────────────────────────────────── rollups

// agg is how hourly values combine into a step.
type agg int

const (
	aggSum  agg = iota // counters: sum, every step present (0 without data)
	aggLast            // gauge last: the latest value of the step
	aggMax             // gauge max: the largest value of the step
	aggAvg             // daily distinct counts per week: the average of the days
)

// dailyDistinct reports whether a metric is a daily distinct count (written once
// per day at 00:00 UTC, today counted live from the active set).
func dailyDistinct(metric string) (set string, ok bool) {
	for s, m := range ActiveSetMetrics {
		if m == metric {
			return s, true
		}
	}
	return "", false
}

func aggOf(m Metric, step string) agg {
	if _, ok := dailyDistinct(m.Name); ok && step == StepWeek {
		return aggAvg
	}
	switch m.Kind {
	case state.AnalyticsGaugeLast:
		return aggLast
	case state.AnalyticsGaugeMax:
		return aggMax
	}
	return aggSum
}

// hourly is the hourly values of one series, ordered by bucket.
type hourly []state.AnalyticsPoint

// rollup combines hourly values into steps over [from, to). Counter steps are
// all present; gauge steps only when they have data.
func rollup(hs hourly, a agg, from, to time.Time, step string) []Point {
	out := []Point{}
	i := 0
	for i < len(hs) && hs[i].Bucket.Before(from) {
		i++
	}
	for s := from; s.Before(to); s = stepNext(s, step) {
		next := stepNext(s, step)
		n, v := 0, 0.0
		for ; i < len(hs) && hs[i].Bucket.Before(next); i++ {
			x := hs[i].Value
			switch {
			case a == aggLast, a == aggMax && n == 0:
				v = x
			case a == aggMax:
				v = math.Max(v, x)
			default: // sum, average
				v += x
			}
			n++
		}
		switch {
		case n == 0 && a != aggSum:
			continue
		case a == aggAvg:
			v /= float64(n)
		}
		out = append(out, Point{float64(s.Unix()), v})
	}
	return out
}

// totals returns the hourly sums over every dimension of the metrics in
// [from, to), keyed by metric (in the database when the store implements
// state.AnalyticsTotals).
func (r *Reader) totals(ctx context.Context, server string, metrics []string, from, to time.Time) (map[string]hourly, error) {
	q := state.AnalyticsQuery{Server: server, Metrics: metrics, From: from, To: to}
	var ps []state.AnalyticsPoint
	var err error
	if t, ok := r.Store.(state.AnalyticsTotals); ok {
		ps, err = t.Totals(ctx, q)
	} else {
		ps, err = r.Store.Query(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	sums := map[string]map[int64]float64{}
	for _, p := range ps {
		if sums[p.Metric] == nil {
			sums[p.Metric] = map[int64]float64{}
		}
		sums[p.Metric][p.Bucket.Unix()] += p.Value
	}
	out := map[string]hourly{}
	for m, byBucket := range sums {
		hs := make(hourly, 0, len(byBucket))
		for b, v := range byBucket {
			hs = append(hs, state.AnalyticsPoint{Server: server, Metric: m, Bucket: time.Unix(b, 0).UTC(), Value: v})
		}
		slices.SortFunc(hs, func(a, b state.AnalyticsPoint) int { return a.Bucket.Compare(b.Bucket) })
		out[m] = hs
	}
	return out, nil
}

// dimHourly returns the hourly values of one dimension of a metric.
func (r *Reader) dimHourly(ctx context.Context, server, metric, dim string, from, to time.Time) (hourly, error) {
	ps, err := r.Store.Query(ctx, state.AnalyticsQuery{Server: server, Metrics: []string{metric}, Dim: &dim, From: from, To: to})
	return hourly(ps), err
}

// withToday adds today's live distinct count of a daily metric (the collector
// rolls up only past days) when today is in [from, to).
func (r *Reader) withToday(ctx context.Context, server, metric string, hs hourly, from, to time.Time) (hourly, error) {
	set, ok := dailyDistinct(metric)
	if !ok {
		return hs, nil
	}
	today := dayFloor(r.now())
	if today.Before(from) || !today.Before(to) {
		return hs, nil
	}
	n, err := r.Store.CountActive(ctx, server, set, ActiveDay(today))
	if err != nil || n == 0 {
		return hs, err
	}
	for i := range hs {
		if hs[i].Bucket.Equal(today) {
			hs[i].Value = math.Max(hs[i].Value, float64(n))
			return hs, nil
		}
	}
	hs = append(slices.Clone(hs), state.AnalyticsPoint{Server: server, Metric: metric, Bucket: today, Value: float64(n)})
	slices.SortFunc(hs, func(a, b state.AnalyticsPoint) int { return a.Bucket.Compare(b.Bucket) })
	return hs, nil
}

// minus returns total minus every series of parts, bucket by bucket, where total
// has data (the "other" dimensions).
func minus(total hourly, parts []hourly) hourly {
	sub := map[int64]float64{}
	for _, p := range parts {
		for _, h := range p {
			sub[h.Bucket.Unix()] += h.Value
		}
	}
	out := make(hourly, 0, len(total))
	for _, h := range total {
		v := h.Value - sub[h.Bucket.Unix()]
		if v < 1e-9 {
			v = 0
		}
		h.Dim = "other"
		h.Value = v
		out = append(out, h)
	}
	return out
}

// ───────────────────────────────────────────────────────────── status

// withSince normalizes a Status (non-nil lists) and fills Since from the store.
func (r *Reader) withSince(ctx context.Context, server string, st Status) (Status, error) {
	st.Missing = normalizeMissing(st.Missing)
	since, err := r.Store.Since(ctx, server)
	if err != nil {
		return st, err
	}
	st.Since = nil
	if !since.IsZero() {
		st.Since = &since
	}
	return st, nil
}

func normalizeMissing(ms []Missing) []Missing {
	out := make([]Missing, 0, len(ms))
	for _, m := range ms {
		if m.Metrics == nil {
			m.Metrics = []string{}
		}
		out = append(out, m)
	}
	return out
}

// ───────────────────────────────────────────────────────────── series

// Series answers …/analytics/series (see api.go). status is the collection status
// of the server; its Since is read from the store.
func (r *Reader) Series(ctx context.Context, req SeriesRequest, status Status) (Series, error) {
	st, err := r.withSince(ctx, req.Server, status)
	if err != nil {
		return Series{}, err
	}
	out := Series{Server: req.Server, From: req.From, To: req.To, Step: req.Step, Series: []Serie{}, Status: st}
	var refs []DimRef
	for _, name := range req.Metrics {
		m, ok := Lookup(name)
		if !ok {
			return Series{}, badRequest("unknown metric %q", name)
		}
		ss, dims, err := r.metricSeries(ctx, req, m)
		if err != nil {
			return Series{}, err
		}
		out.Series = append(out.Series, ss...)
		for _, d := range dims {
			refs = append(refs, DimRef{Kind: m.Dim, Value: d})
		}
	}
	out.Labels = ResolveLabels(ctx, r.Labeler, req.Server, refs)
	return out, nil
}

// metricSeries returns the series of one metric and the dimensions they show.
func (r *Reader) metricSeries(ctx context.Context, req SeriesRequest, m Metric) ([]Serie, []string, error) {
	a := aggOf(m, req.Step)
	serie := func(dim string, hs hourly) Serie {
		return Serie{Metric: m.Name, Dim: dim, Points: rollup(hs, a, req.From, req.To, req.Step)}
	}
	if m.Dim == "" || req.Dim != nil {
		dim := ""
		if m.Dim != "" {
			dim = *req.Dim
		}
		hs, err := r.dimHourly(ctx, req.Server, m.Name, dim, req.From, req.To)
		if err == nil {
			hs, err = r.withToday(ctx, req.Server, m.Name, hs, req.From, req.To)
		}
		if err != nil {
			return nil, nil, err
		}
		var dims []string
		if dim != "" {
			dims = []string{dim}
		}
		return []Serie{serie(dim, hs)}, dims, nil
	}

	// Top dimensions, "other" and the total.
	ranked, err := r.Store.Dims(ctx, req.Server, m.Name, req.From, req.To, req.Top+2)
	if err != nil {
		return nil, nil, err
	}
	var dims []string
	for _, d := range ranked {
		if d.Dim != "" {
			dims = append(dims, d.Dim)
		}
	}
	others := len(dims) > req.Top
	dims = dims[:min(len(dims), req.Top)]
	tot, err := r.totals(ctx, req.Server, []string{m.Name}, req.From, req.To)
	if err != nil {
		return nil, nil, err
	}
	total := tot[m.Name]
	out := []Serie{serie("", total)}
	parts := make([]hourly, 0, len(dims))
	for _, d := range dims {
		hs, err := r.dimHourly(ctx, req.Server, m.Name, d, req.From, req.To)
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, hs)
		out = append(out, serie(d, hs))
	}
	if others {
		out = append(out, serie("other", minus(total, parts)))
	}
	return out, dims, nil
}

// ───────────────────────────────────────────────────────────── catalog

// Catalog answers …/analytics/catalog: every metric, and whether it is collected
// given the status (enabled, presence, missing intents or permissions).
func (r *Reader) Catalog(ctx context.Context, server string, status Status) (CatalogResponse, error) {
	st, err := r.withSince(ctx, server, status)
	if err != nil {
		return CatalogResponse{}, err
	}
	missing := map[string]bool{}
	for _, m := range st.Missing {
		for _, name := range m.Metrics {
			missing[name] = true
		}
	}
	out := CatalogResponse{Metrics: make([]CatalogEntry, 0, len(Catalog)), Status: st}
	for _, m := range Catalog {
		out.Metrics = append(out.Metrics, CatalogEntry{
			Name: m.Name, Kind: string(m.Kind), Dim: m.Dim, Unit: m.Unit, Source: m.Source, Presence: m.Presence,
			Collected: st.Enabled && (!m.Presence || st.Presence) && !missing[m.Name],
		})
	}
	return out, nil
}
