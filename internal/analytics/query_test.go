package analytics_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// qNow is Tuesday 2026-11-24 15:30 UTC.
var qNow = time.Date(2026, 11, 24, 15, 30, 0, 0, time.UTC)

func qDay(n int) time.Time { return time.Date(2026, 11, 24+n, 0, 0, 0, 0, time.UTC) }

func qStore(t *testing.T) state.Store {
	t.Helper()
	s, err := sqlite.Open(":memory:", sqlite.WithClock(func() time.Time { return qNow }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func qRec(t *testing.T, a state.Analytics, kind state.AnalyticsKind, metric, dim string, bucket time.Time, v float64) {
	t.Helper()
	if err := a.Record(t.Context(), kind, state.AnalyticsPoint{Server: "main", Metric: metric, Dim: dim, Bucket: bucket, Value: v}); err != nil {
		t.Fatal(err)
	}
}

func qCounter(t *testing.T, a state.Analytics, metric, dim string, bucket time.Time, v float64) {
	t.Helper()
	qRec(t, a, state.AnalyticsCounter, metric, dim, bucket, v)
}

func qParse(t *testing.T, q string) analytics.SeriesRequest {
	t.Helper()
	v, _ := url.ParseQuery(q)
	req, err := analytics.ParseSeriesRequest("main", v, qNow)
	if err != nil {
		t.Fatalf("ParseSeriesRequest(%s): %v", q, err)
	}
	return req
}

func TestParseSeriesRequest(t *testing.T) {
	req := qParse(t, "metrics=messages.sent,members.joined,messages.sent")
	if !slices.Equal(req.Metrics, []string{"messages.sent", "members.joined"}) || req.Step != "day" || req.Top != 8 || req.Dim != nil {
		t.Errorf("defaults = %+v", req)
	}
	// to = qNow (aligned up to the next day), from = to - 7 days (aligned down).
	if !req.From.Equal(qDay(-7)) || !req.To.Equal(qDay(1)) {
		t.Errorf("default range = %v .. %v", req.From, req.To)
	}
	// Weeks start on Monday 00:00 UTC.
	req = qParse(t, "metrics=messages.sent&step=week&from=2026-11-04T12:00:00Z&to=2026-11-24T15:30:00%2B01:00")
	if !req.From.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) || !req.To.Equal(qDay(6)) {
		t.Errorf("week range = %v .. %v", req.From, req.To)
	}
	req = qParse(t, "metrics=messages.sent&step=hour&from=2026-11-24T10:15:00Z&to=2026-11-24T12:00:00Z&dim=123&top=3")
	if !req.From.Equal(qDay(0).Add(10*time.Hour)) || !req.To.Equal(qDay(0).Add(12*time.Hour)) || req.Dim == nil || *req.Dim != "123" || req.Top != 3 {
		t.Errorf("hour request = %+v", req)
	}
	// Exactly 400 days and 2000 points are accepted.
	qParse(t, "metrics=messages.sent&from=2025-10-20T00:00:00Z&to=2026-11-24T00:00:00Z")
	qParse(t, "metrics=messages.sent&step=hour&from=2026-09-02T16:00:00Z&to=2026-11-24T08:00:00Z")

	for q, want := range map[string]string{
		"":                                     "metrics is required",
		"metrics=,":                            "metrics is required",
		"metrics=nope":                         "unknown metric",
		"metrics=messages.sent&step=month":     "step must be",
		"metrics=messages.sent&from=yesterday": "from must be an RFC 3339",
		"metrics=messages.sent&to=2026-11-24":  "to must be an RFC 3339",
		"metrics=messages.sent&from=2026-11-24T00:00:00Z&to=2026-11-24T00:00:00Z": "from must be before to",
		"metrics=messages.sent&from=2025-01-01T00:00:00Z&to=2026-11-24T00:00:00Z": "at most 400 days",
		"metrics=messages.sent&step=hour&from=2026-08-01T00:00:00Z":               "points per series",
		"metrics=messages.sent&top=0":                                             "top must be",
		"metrics=messages.sent&top=51":                                            "top must be",
		"metrics=messages.sent&top=x":                                             "top must be",
		"metrics=" + strings.Join(qCatalogNames()[:21], ","):                      "at most 20 metrics",
	} {
		v, _ := url.ParseQuery(q)
		_, err := analytics.ParseSeriesRequest("main", v, qNow)
		var re *analytics.RequestError
		if !qAsRequestError(err, &re) || !strings.Contains(re.Message, want) {
			t.Errorf("%q: err = %v, want a RequestError containing %q", q, err, want)
		}
	}
}

func qCatalogNames() []string {
	var out []string
	for _, m := range analytics.Catalog {
		out = append(out, m.Name)
	}
	return out
}

func qAsRequestError(err error, re **analytics.RequestError) bool { return errors.As(err, re) }

func qPoints(s analytics.Serie) []float64 {
	out := make([]float64, len(s.Points))
	for i, p := range s.Points {
		out[i] = p[1]
	}
	return out
}

func qStarts(s analytics.Serie) []time.Time {
	out := make([]time.Time, len(s.Points))
	for i, p := range s.Points {
		out[i] = time.Unix(int64(p[0]), 0).UTC()
	}
	return out
}

func qFind(t *testing.T, ss []analytics.Serie, metric, dim string) analytics.Serie {
	t.Helper()
	for _, s := range ss {
		if s.Metric == metric && s.Dim == dim {
			return s
		}
	}
	t.Fatalf("no series %s/%q in %+v", metric, dim, ss)
	return analytics.Serie{}
}

func TestSeriesKinds(t *testing.T) {
	st := qStore(t)
	a := st.Analytics()
	r := &analytics.Reader{Store: a, Now: func() time.Time { return qNow }}
	// Counter: 1 + 2 on day -2, 4 on day -1 (two hours), none on day 0.
	qCounter(t, a, analytics.MembersJoined, "", qDay(-2).Add(3*time.Hour), 1)
	qCounter(t, a, analytics.MembersJoined, "", qDay(-2).Add(20*time.Hour), 2)
	qCounter(t, a, analytics.MembersJoined, "", qDay(-1).Add(1*time.Hour), 4)
	// Gauge last: the day's latest sample; gauge max: the day's peak.
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersTotal, "", qDay(-2).Add(1*time.Hour), 100)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersTotal, "", qDay(-2).Add(23*time.Hour), 103)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersTotal, "", qDay(-1).Add(5*time.Hour), 99)
	qRec(t, a, state.AnalyticsGaugeMax, analytics.VoicePeak, "", qDay(-2).Add(2*time.Hour), 7)
	qRec(t, a, state.AnalyticsGaugeMax, analytics.VoicePeak, "", qDay(-2).Add(9*time.Hour), 3)
	// Daily distinct: day -2 and -1 rolled up; today live from the active set.
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersActive, "", qDay(-2), 10)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersActive, "", qDay(-1), 20)
	for _, tok := range []string{"a", "b", "c"} {
		if err := a.MarkActive(t.Context(), "main", analytics.ActiveSetAll, "2026-11-24", tok); err != nil {
			t.Fatal(err)
		}
	}

	res, err := r.Series(t.Context(), qParse(t, "metrics=members.joined,members.total,voice.peak,members.active&from=2026-11-22T00:00:00Z&to=2026-11-25T00:00:00Z"), analytics.Status{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Step != "day" || !res.From.Equal(qDay(-2)) || !res.To.Equal(qDay(1)) || len(res.Series) != 4 {
		t.Fatalf("series = %+v", res)
	}
	joined := qFind(t, res.Series, analytics.MembersJoined, "")
	if got := qPoints(joined); !slices.Equal(got, []float64{3, 4, 0}) {
		t.Errorf("counter per day (zero-filled) = %v", got)
	}
	if got := qStarts(joined); !slices.Equal(got, []time.Time{qDay(-2), qDay(-1), qDay(0)}) {
		t.Errorf("counter buckets = %v", got)
	}
	if got := qPoints(qFind(t, res.Series, analytics.MembersTotal, "")); !slices.Equal(got, []float64{103, 99}) {
		t.Errorf("gauge last per day (sparse) = %v", got)
	}
	if got := qPoints(qFind(t, res.Series, analytics.VoicePeak, "")); !slices.Equal(got, []float64{7}) {
		t.Errorf("gauge max per day = %v", got)
	}
	if got := qPoints(qFind(t, res.Series, analytics.MembersActive, "")); !slices.Equal(got, []float64{10, 20, 3}) {
		t.Errorf("daily distinct per day (today live) = %v", got)
	}
	if res.Status.Since == nil || !res.Status.Since.Equal(qDay(-2)) || res.Status.Missing == nil || res.Labels == nil {
		t.Errorf("status = %+v, labels = %v", res.Status, res.Labels)
	}

	// Weeks: counters summed, daily distinct averaged over the days with data.
	res, err = r.Series(t.Context(), qParse(t, "metrics=members.joined,members.active&step=week&from=2026-11-17T00:00:00Z&to=2026-11-25T00:00:00Z"), analytics.Status{})
	if err != nil {
		t.Fatal(err)
	}
	// Weeks of 2026-11-16 (Sunday 22: 3 joins, 10 active) and 2026-11-23 (Monday 23:
	// 4 joins, 20 active; today 3 active).
	if got := qPoints(qFind(t, res.Series, analytics.MembersJoined, "")); !slices.Equal(got, []float64{3, 4}) {
		t.Errorf("counter per week = %v", got)
	}
	if got := qPoints(qFind(t, res.Series, analytics.MembersActive, "")); !slices.Equal(got, []float64{10, 11.5}) {
		t.Errorf("daily distinct per week = %v", got)
	}

	// Hours.
	res, err = r.Series(t.Context(), qParse(t, "metrics=members.joined&step=hour&from=2026-11-23T00:00:00Z&to=2026-11-23T03:00:00Z"), analytics.Status{})
	if err != nil {
		t.Fatal(err)
	}
	if got := qPoints(res.Series[0]); !slices.Equal(got, []float64{0, 4, 0}) {
		t.Errorf("counter per hour = %v", got)
	}
}

func TestSeriesTopDimensions(t *testing.T) {
	st := qStore(t)
	a := st.Analytics()
	ctx := t.Context()
	qMust(t, st.Resources().Put(ctx, state.Resource{Server: "main", Kind: state.KindChannel, Key: "general", ID: "111", Owner: state.OwnerGit}))
	spec := &config.ServerSpec{Channels: map[string]config.Channel{"general": {Name: "général", Type: "text"}}}
	r := &analytics.Reader{Store: a, Now: func() time.Time { return qNow },
		Labeler: analytics.ResourceLabeler{Resources: st.Resources(), Spec: func(string) *config.ServerSpec { return spec }}}
	// Channels 111 (10+5), 222 (8), 333 (2), 444 (1).
	qCounter(t, a, analytics.MessagesSent, "111", qDay(-1).Add(time.Hour), 10)
	qCounter(t, a, analytics.MessagesSent, "111", qDay(0).Add(time.Hour), 5)
	qCounter(t, a, analytics.MessagesSent, "222", qDay(-1).Add(2*time.Hour), 8)
	qCounter(t, a, analytics.MessagesSent, "333", qDay(0).Add(2*time.Hour), 2)
	qCounter(t, a, analytics.MessagesSent, "444", qDay(0).Add(3*time.Hour), 1)

	res, err := r.Series(ctx, qParse(t, "metrics=messages.sent&from=2026-11-23T00:00:00Z&to=2026-11-25T00:00:00Z&top=2"), analytics.Status{})
	if err != nil {
		t.Fatal(err)
	}
	var dims []string
	for _, s := range res.Series {
		dims = append(dims, s.Dim)
	}
	if !slices.Equal(dims, []string{"", "111", "222", "other"}) {
		t.Fatalf("series dims = %q", dims)
	}
	for dim, want := range map[string][]float64{"": {18, 8}, "111": {10, 5}, "222": {8, 0}, "other": {0, 3}} {
		if got := qPoints(qFind(t, res.Series, analytics.MessagesSent, dim)); !slices.Equal(got, want) {
			t.Errorf("%q = %v, want %v", dim, got, want)
		}
	}
	if l := res.Labels["111"]; l.Name != "général" || l.Kind != "channel" || l.Deleted {
		t.Errorf("label 111 = %+v", l)
	}
	if l := res.Labels["222"]; l.Name != "222" || !l.Deleted {
		t.Errorf("label 222 (unknown) = %+v", l)
	}
	if _, ok := res.Labels["other"]; ok || len(res.Labels) != 2 {
		t.Errorf("labels = %v", res.Labels)
	}
	// No "other" when every dimension fits.
	res, err = r.Series(ctx, qParse(t, "metrics=messages.sent&from=2026-11-23T00:00:00Z&to=2026-11-25T00:00:00Z"), analytics.Status{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) != 5 || res.Series[len(res.Series)-1].Dim == "other" {
		t.Errorf("without others: %+v", res.Series)
	}
	// One dimension.
	res, err = r.Series(ctx, qParse(t, "metrics=messages.sent,members.joined&dim=222&from=2026-11-23T00:00:00Z&to=2026-11-25T00:00:00Z"), analytics.Status{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) != 2 || res.Series[0].Dim != "222" || !slices.Equal(qPoints(res.Series[0]), []float64{8, 0}) || res.Series[1].Dim != "" {
		t.Errorf("dim=222: %+v", res.Series)
	}
}

func qMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func qKPI(t *testing.T, s analytics.Summary, metric string) analytics.KPI {
	t.Helper()
	for _, k := range s.KPIs {
		if k.Metric == metric {
			return k
		}
	}
	t.Fatalf("no KPI %s", metric)
	return analytics.KPI{}
}

func TestSummary(t *testing.T) {
	st := qStore(t)
	a := st.Analytics()
	r := &analytics.Reader{Store: a, Now: func() time.Time { return qNow }}
	ctx := t.Context()

	// Nothing recorded: zero values, previous null, every list present.
	empty, err := r.Summary(ctx, "main", "", analytics.Status{Enabled: true})
	qMust(t, err)
	if empty.Period != "7d" || !empty.From.Equal(qDay(-6)) || !empty.To.Equal(qDay(1)) || len(empty.KPIs) != len(analytics.SummaryKPIs) {
		t.Fatalf("empty summary = %+v", empty)
	}
	data, _ := json.Marshal(empty)
	for _, frag := range []string{`"since":null`, `"missing":[]`, `"labels":{}`, `"messages.sent":[]`, `"spark":[]`, `"previous":null`} {
		if !strings.Contains(string(data), frag) {
			t.Errorf("empty summary JSON lacks %s: %s", frag, data)
		}
	}
	if strings.Contains(string(data), "null,") && strings.Contains(string(data), `"kpis":null`) {
		t.Errorf("null lists: %s", data)
	}

	// The previous 7d period starts on day -13: collection started then.
	qCounter(t, a, analytics.MembersJoined, "", qDay(-13).Add(time.Hour), 4)
	qCounter(t, a, analytics.MembersLeft, "", qDay(-10), 1)
	qCounter(t, a, analytics.MembersJoined, "", qDay(-6), 5)
	qCounter(t, a, analytics.MembersJoined, "", qDay(0).Add(9*time.Hour), 2)
	qCounter(t, a, analytics.MembersLeft, "", qDay(-1), 3)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersTotal, "", qDay(-8), 90)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersTotal, "", qDay(-3), 95)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersTotal, "", qDay(0).Add(14*time.Hour), 100)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersOnline, "", qDay(-2).Add(time.Hour), 10)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersOnline, "", qDay(-2).Add(2*time.Hour), 20)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersActive, "", qDay(-2), 6)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersActive, "", qDay(-1), 12)
	qMust(t, a.MarkActive(ctx, "main", analytics.ActiveSetAll, "2026-11-24", "tok"))
	// Messages: Monday 2026-11-23 10:00 in two channels, Sunday 2026-11-22 23:00.
	qCounter(t, a, analytics.MessagesSent, "111", qDay(-1).Add(10*time.Hour), 7)
	qCounter(t, a, analytics.MessagesSent, "222", qDay(-1).Add(10*time.Hour), 3)
	qCounter(t, a, analytics.MessagesSent, "222", qDay(-2).Add(23*time.Hour), 1)
	qCounter(t, a, analytics.MessagesSent, "222", qDay(-9), 6) // previous period
	qCounter(t, a, analytics.ReactionsAdded, "pepe:123456", qDay(-1), 2)
	qRec(t, a, state.AnalyticsGaugeLast, analytics.PresenceActivity, "playing:Minecraft", qDay(-1), 4)

	s, err := r.Summary(ctx, "main", "7d", analytics.Status{Enabled: true})
	qMust(t, err)
	var order []string
	for _, k := range s.KPIs {
		order = append(order, k.Metric)
	}
	if !slices.Equal(order, analytics.SummaryKPIs) {
		t.Errorf("KPI order = %v", order)
	}
	check := func(metric string, value float64, previous any) {
		t.Helper()
		k := qKPI(t, s, metric)
		if k.Value != value {
			t.Errorf("%s = %v, want %v", metric, k.Value, value)
		}
		switch p := previous.(type) {
		case nil:
			if k.Previous != nil {
				t.Errorf("%s previous = %v, want null", metric, *k.Previous)
			}
		case float64:
			if k.Previous == nil || *k.Previous != p {
				t.Errorf("%s previous = %v, want %v", metric, k.Previous, p)
			}
		}
	}
	check(analytics.MembersTotal, 100, 90.0)
	check(analytics.MembersJoined, 7, 4.0)
	check(analytics.MembersLeft, 3, 1.0)
	check(analytics.MembersNet, 4, 3.0)
	check(analytics.MembersActive, (6+12+1)/3.0, nil) // no data in the previous period
	check(analytics.MessagesSent, 11, 6.0)
	check(analytics.MembersOnline, 15, nil)
	check(analytics.CommandsUsed, 0, 0.0)

	joined := qKPI(t, s, analytics.MembersJoined)
	if got := qPoints(analytics.Serie{Points: joined.Spark}); !slices.Equal(got, []float64{5, 0, 0, 0, 0, 0, 2}) {
		t.Errorf("joined spark = %v", got)
	}
	if got := qPoints(analytics.Serie{Points: qKPI(t, s, analytics.MembersTotal).Spark}); !slices.Equal(got, []float64{95, 100}) {
		t.Errorf("total spark (sparse) = %v", got)
	}
	if got := qPoints(analytics.Serie{Points: qKPI(t, s, analytics.MembersNet).Spark}); !slices.Equal(got, []float64{5, 0, 0, 0, 0, -3, 2}) {
		t.Errorf("net spark = %v", got)
	}
	if s.Heatmap[0][10] != 10 || s.Heatmap[6][23] != 1 {
		t.Errorf("heatmap Monday 10h = %v, Sunday 23h = %v", s.Heatmap[0][10], s.Heatmap[6][23])
	}

	msgs := s.Rankings[analytics.MessagesSent]
	if len(msgs) != 2 || msgs[0].Dim != "111" || msgs[0].Value != 7 || msgs[1].Dim != "222" || msgs[1].Value != 4 {
		t.Fatalf("messages ranking = %+v", msgs)
	}
	if msgs[0].Previous == nil || *msgs[0].Previous != 0 || msgs[1].Previous == nil || *msgs[1].Previous != 6 {
		t.Errorf("messages ranking previous = %v, %v", msgs[0].Previous, msgs[1].Previous)
	}
	for _, m := range analytics.SummaryRankings {
		if s.Rankings[m] == nil {
			t.Errorf("ranking %s missing", m)
		}
	}
	if _, ok := s.Rankings[analytics.PresenceActivity]; ok {
		t.Error("presence ranking without presence")
	}
	if l := s.Labels["pepe:123456"]; l.Name != "pepe" || l.ImageURL != "https://cdn.discordapp.com/emojis/123456.png" {
		t.Errorf("emoji label = %+v", l)
	}
	if !s.Labels["111"].Deleted {
		t.Errorf("unknown channel label = %+v", s.Labels["111"])
	}

	// Presence adds the activities; the previous period is not covered when
	// collection started after its first day.
	s, err = r.Summary(ctx, "main", "7d", analytics.Status{Enabled: true, Presence: true})
	qMust(t, err)
	if acts := s.Rankings[analytics.PresenceActivity]; len(acts) != 1 || acts[0].Dim != "playing:Minecraft" || s.Labels["playing:Minecraft"].Kind != "label" {
		t.Errorf("activities = %+v", acts)
	}
	_, err = a.Purge(ctx, "main", qDay(-12))
	qMust(t, err)
	s, err = r.Summary(ctx, "main", "7d", analytics.Status{})
	qMust(t, err)
	if qKPI(t, s, analytics.MembersJoined).Previous != nil || s.Rankings[analytics.MessagesSent][0].Previous != nil {
		t.Error("previous values without coverage")
	}
	if _, err := r.Summary(ctx, "main", "1y", analytics.Status{}); err == nil {
		t.Error("period 1y accepted")
	}
}

// qNoTotals hides state.AnalyticsTotals: the reader sums Query points itself.
type qNoTotals struct{ state.Analytics }

func TestFallbackWithoutTotalsMatches(t *testing.T) {
	st := qStore(t)
	a := st.Analytics()
	for i, ch := range []string{"1", "2", "3", "4", "5"} {
		for h := range 30 {
			qCounter(t, a, analytics.MessagesSent, ch, qDay(-h/5).Add(time.Duration(h*3%24)*time.Hour), float64(i+h))
		}
		qRec(t, a, state.AnalyticsGaugeLast, analytics.PresenceStatus, ch, qDay(-1).Add(time.Duration(i)*time.Hour), float64(i))
	}
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersOnline, "", qDay(-3), 12)
	clock := func() time.Time { return qNow }
	with := &analytics.Reader{Store: a, Now: clock}
	without := &analytics.Reader{Store: qNoTotals{a}, Now: clock}
	if _, ok := without.Store.(state.AnalyticsTotals); ok {
		t.Fatal("qNoTotals implements AnalyticsTotals")
	}
	req := qParse(t, "metrics=messages.sent,presence.status,members.online&step=hour&from=2026-11-18T00:00:00Z&to=2026-11-25T00:00:00Z&top=2")
	s1, err := with.Series(t.Context(), req, analytics.Status{})
	qMust(t, err)
	s2, err := without.Series(t.Context(), req, analytics.Status{})
	qMust(t, err)
	if !reflect.DeepEqual(s1, s2) {
		t.Errorf("series differ without Totals:\n%+v\n%+v", s1, s2)
	}
	m1, err := with.Summary(t.Context(), "main", "30d", analytics.Status{Presence: true})
	qMust(t, err)
	m2, err := without.Summary(t.Context(), "main", "30d", analytics.Status{Presence: true})
	qMust(t, err)
	if !reflect.DeepEqual(m1, m2) {
		t.Errorf("summaries differ without Totals:\n%+v\n%+v", m1, m2)
	}
}

func TestCatalog(t *testing.T) {
	st := qStore(t)
	r := &analytics.Reader{Store: st.Analytics()}
	res, err := r.Catalog(t.Context(), "main", analytics.Status{Enabled: true,
		Missing: []analytics.Missing{{Metrics: []string{analytics.ModerationActions}, Reason: "no ViewAuditLog", Fix: "grant it"}}})
	qMust(t, err)
	if len(res.Metrics) != len(analytics.Catalog) {
		t.Fatalf("%d entries", len(res.Metrics))
	}
	for _, e := range res.Metrics {
		want := e.Name != analytics.ModerationActions && !e.Presence
		if e.Collected != want {
			t.Errorf("%s collected = %v, want %v", e.Name, e.Collected, want)
		}
	}
	res, err = r.Catalog(t.Context(), "main", analytics.Status{})
	qMust(t, err)
	for _, e := range res.Metrics {
		if e.Collected {
			t.Errorf("%s collected while disabled", e.Name)
		}
	}
}

type qMapLabeler map[string]analytics.Label

func (m qMapLabeler) Labels(_ context.Context, _ string, dims []analytics.DimRef) map[string]analytics.Label {
	out := map[string]analytics.Label{}
	for _, d := range dims {
		if l, ok := m[d.Value]; ok {
			out[d.Value] = l
		}
	}
	return out
}

func TestLabels(t *testing.T) {
	for v, want := range map[string]analytics.Label{
		"pepe:123":    {Name: "pepe", Kind: "emoji", ImageURL: "https://cdn.discordapp.com/emojis/123.png"},
		"a:party:456": {Name: "party", Kind: "emoji", ImageURL: "https://cdn.discordapp.com/emojis/456.gif"},
		"😀":           {Name: "😀", Kind: "emoji"},
		"x:y":         {Name: "x:y", Kind: "emoji"},
		":123":        {Name: ":123", Kind: "emoji"},
	} {
		if got := analytics.FallbackLabel(analytics.DimRef{Kind: "emoji", Value: v}); got != want {
			t.Errorf("emoji %q = %+v, want %+v", v, got, want)
		}
	}
	for d, want := range map[analytics.DimRef]analytics.Label{
		{Kind: "channel", Value: "9"}:       {Name: "9", Kind: "channel", Deleted: true},
		{Kind: "role", Value: "9"}:          {Name: "9", Kind: "role", Deleted: true},
		{Kind: "event", Value: "9"}:         {Name: "9", Kind: "event", Deleted: true},
		{Kind: "rule", Value: "9"}:          {Name: "9", Kind: "label", Deleted: true},
		{Kind: "invite", Value: "abc"}:      {Name: "abc", Kind: "invite"},
		{Kind: "account age", Value: "<1d"}: {Name: "<1d", Kind: "label"},
		{Kind: "command", Value: "ping"}:    {Name: "ping", Kind: "label"},
	} {
		if got := analytics.FallbackLabel(d); got != want {
			t.Errorf("%+v = %+v, want %+v", d, got, want)
		}
	}

	st := qStore(t)
	ctx := t.Context()
	for _, r := range []state.Resource{
		{Server: "main", Kind: state.KindChannel, Key: "voice", ID: "1"},
		{Server: "main", Kind: state.KindRole, Key: "mod", ID: "2"},
		{Server: "main", Kind: state.KindRole, Key: "plain", ID: "3"},
		{Server: "main", Kind: state.KindEmoji, Key: "pepe", ID: "4"},
		{Server: "main", Kind: state.KindAutoMod, Key: "spam", ID: "5"},
		{Server: "main", Kind: state.KindRole, Key: "undeclared", ID: "6"},
	} {
		r.Owner = state.OwnerGit
		qMust(t, st.Resources().Put(ctx, r))
	}
	spec := &config.ServerSpec{
		Channels: map[string]config.Channel{"voice": {Name: "Lounge", Type: "voice"}},
		Roles:    map[string]config.RoleSpec{"mod": {Name: "Modérateur", Color: "#FF00aa"}, "plain": {Name: "Plain", Color: "red"}},
		Emojis:   map[string]config.EmojiSpec{"pepe": {Name: "pepe_happy"}},
		AutoMod:  map[string]config.AutoModSpec{"spam": {Name: "No spam"}},
	}
	rl := analytics.ResourceLabeler{Resources: st.Resources(), Spec: func(string) *config.ServerSpec { return spec }}
	live := qMapLabeler{"1": {Name: "live-lounge", Kind: "voice"}}
	got := analytics.ResolveLabels(ctx, analytics.Labelers{live, nil, rl}, "main", []analytics.DimRef{
		{Kind: "channel", Value: "1"}, {Kind: "role", Value: "2"}, {Kind: "role", Value: "3"},
		{Kind: "emoji", Value: "pepe:4"}, {Kind: "rule", Value: "5"}, {Kind: "role", Value: "6"},
		{Kind: "channel", Value: ""}, {Kind: "channel", Value: "other"},
	})
	want := map[string]analytics.Label{
		"1":      {Name: "live-lounge", Kind: "voice"},
		"2":      {Name: "Modérateur", Kind: "role", Color: "#ff00aa"},
		"3":      {Name: "Plain", Kind: "role"},
		"pepe:4": {Name: "pepe_happy", Kind: "emoji", ImageURL: "https://cdn.discordapp.com/emojis/4.png"},
		"5":      {Name: "No spam", Kind: "label"},
		"6":      {Name: "undeclared", Kind: "role"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labels =\n%+v\nwant\n%+v", got, want)
	}
	// Without the live labeler, the voice channel comes from the configuration.
	if l := analytics.ResolveLabels(ctx, rl, "main", []analytics.DimRef{{Kind: "channel", Value: "1"}})["1"]; l != (analytics.Label{Name: "Lounge", Kind: "voice"}) {
		t.Errorf("config channel label = %+v", l)
	}
}

func TestParseSummaryRequest(t *testing.T) {
	q := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	req, err := analytics.ParseSummaryRequest(q(), qNow)
	if err != nil || req.Period != "7d" {
		t.Fatalf("default = %+v, %v", req, err)
	}
	// Widened to whole hours.
	req, err = analytics.ParseSummaryRequest(q("from", "2026-11-20T10:30:00+01:00", "to", "2026-11-21T08:10:00Z"), qNow)
	if err != nil || req.Period != analytics.CustomPeriod ||
		!req.From.Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)) || !req.To.Equal(time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("custom = %+v, %v", req, err)
	}
	for _, bad := range []url.Values{
		q("period", "7d", "from", "2026-11-20T00:00:00Z", "to", "2026-11-21T00:00:00Z"),
		q("from", "2026-11-20T00:00:00Z"),
		q("from", "yesterday", "to", "2026-11-21T00:00:00Z"),
		q("from", "2026-11-21T00:00:00Z", "to", "2026-11-20T00:00:00Z"),
		q("from", "2025-01-01T00:00:00Z", "to", "2026-11-20T00:00:00Z"),
		q("from", qNow.Add(time.Hour).Format(time.RFC3339), "to", qNow.Add(3*time.Hour).Format(time.RFC3339)),
		q("period", "1y"),
	} {
		if _, err := analytics.ParseSummaryRequest(bad, qNow); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestSummaryCustomRange(t *testing.T) {
	st := qStore(t)
	a := st.Analytics()
	r := &analytics.Reader{Store: a, Now: func() time.Time { return qNow }}
	ctx := t.Context()
	from := qDay(-1).Add(8 * time.Hour)
	to := from.Add(6 * time.Hour)
	qCounter(t, a, analytics.MessagesSent, "111", from.Add(-6*time.Hour), 4) // first hour of the previous range
	qCounter(t, a, analytics.MessagesSent, "111", from.Add(time.Hour), 5)
	qCounter(t, a, analytics.MessagesSent, "111", from.Add(2*time.Hour), 2)
	qCounter(t, a, analytics.MessagesSent, "111", to, 9) // after the range
	qRec(t, a, state.AnalyticsGaugeLast, analytics.MembersBots, "", from.Add(time.Hour), 3)

	s, err := r.SummaryFor(ctx, "main", analytics.SummaryRequest{Period: analytics.CustomPeriod, From: from, To: to}, analytics.Status{Enabled: true})
	qMust(t, err)
	if s.Period != "custom" || s.Step != "hour" || !s.From.Equal(from) || !s.To.Equal(to) {
		t.Fatalf("summary = %+v", s)
	}
	msgs := qKPI(t, s, analytics.MessagesSent)
	if msgs.Value != 7 || msgs.Previous == nil || *msgs.Previous != 4 || len(msgs.Spark) != 6 {
		t.Errorf("messages.sent = %+v", msgs)
	}
	if b := qKPI(t, s, analytics.MembersBots); b.Value != 3 {
		t.Errorf("members.bots = %+v", b)
	}
	// Two days or more: daily sparklines.
	s, err = r.SummaryFor(ctx, "main", analytics.SummaryRequest{Period: analytics.CustomPeriod, From: qDay(-3), To: qDay(0)}, analytics.Status{Enabled: true})
	qMust(t, err)
	if s.Step != "day" || len(qKPI(t, s, analytics.MessagesSent).Spark) != 3 {
		t.Errorf("3-day range: step %s, spark %v", s.Step, qKPI(t, s, analytics.MessagesSent).Spark)
	}
}
