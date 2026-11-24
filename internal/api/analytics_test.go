package api

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/state"
)

const analyticsPath = "/api/v1/servers/" + serverKey + "/analytics/"

// startAnalytics starts the API with the analytics store of the runtime.
func startAnalytics(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	return startWith(t, func(o *Options) {
		o.Analytics = o.Runtime.Store().Analytics()
		if mod != nil {
			mod(o)
		}
	})
}

func TestAnalyticsAccess(t *testing.T) {
	e := startAnalytics(t, nil)
	paths := []string{"summary", "series?metrics=messages.sent", "catalog"}

	anon := e.client()
	for _, p := range paths {
		if r := anon.do("GET", analyticsPath+p, nil); r.status != 401 {
			t.Errorf("anonymous %s = %d %s", p, r.status, r.body)
		}
	}
	alice := e.seed.Users[0]
	if err := e.world.AddMember(e.serverID(), alice.ID); err != nil {
		t.Fatal(err)
	}
	member := e.client()
	member.login(alice.ID)
	for _, p := range paths {
		if r := member.do("GET", analyticsPath+p, nil); r.status != 403 || r.errCode() != "forbidden" {
			t.Errorf("member %s = %d %s", p, r.status, r.body)
		}
	}
	mod := e.client()
	mod.login(e.moderator().ID)
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	for _, c := range []*client{mod, owner} {
		for _, p := range paths {
			c.expect(200, "GET", analyticsPath+p, nil)
		}
	}
	if r := owner.do("GET", "/api/v1/servers/nope/analytics/catalog", nil); r.status != 404 || r.errCode() != "not_found" {
		t.Errorf("unknown server = %d %s", r.status, r.body)
	}

	// Without an analytics store: 404 analytics_disabled (after the access check).
	e2 := start(t)
	o2 := e2.client()
	o2.login(e2.seed.Owner.ID)
	for _, p := range paths {
		if r := o2.do("GET", analyticsPath+p, nil); r.status != 404 || r.errCode() != "analytics_disabled" {
			t.Errorf("no store %s = %d %s", p, r.status, r.body)
		}
	}
	anon2 := e2.client()
	if r := anon2.do("GET", analyticsPath+"catalog", nil); r.status != 401 {
		t.Errorf("anonymous without store = %d", r.status)
	}
}

func TestAnalyticsValidation(t *testing.T) {
	e := startAnalytics(t, nil)
	c := e.client()
	c.login(e.seed.Owner.ID)
	for _, q := range []string{
		"series",
		"series?metrics=nope",
		"series?metrics=messages.sent&step=month",
		"series?metrics=messages.sent&from=yesterday",
		"series?metrics=messages.sent&from=2024-01-01T00:00:00Z&to=2026-01-01T00:00:00Z",
		"series?metrics=messages.sent&step=hour&from=2026-01-01T00:00:00Z&to=2026-06-01T00:00:00Z",
		"series?metrics=messages.sent&top=0",
		"summary?period=1y",
	} {
		r := c.do("GET", analyticsPath+q, nil)
		var body struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		r.json(t, &body)
		if r.status != 400 || body.Error.Code != "invalid" || body.Error.Message == "" {
			t.Errorf("%s = %d %s", q, r.status, r.body)
		}
	}
}

func TestAnalyticsData(t *testing.T) {
	var asked []string
	e := startAnalytics(t, nil)
	a := e.rt.Store().Analytics()
	ctx := t.Context()
	general, ok := e.rt.ResourceID(serverKey, state.KindChannel, "general")
	if !ok {
		t.Fatal("no general channel")
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)
	rec := func(kind state.AnalyticsKind, metric, dim string, at time.Time, v float64) {
		t.Helper()
		if err := a.Record(ctx, kind, state.AnalyticsPoint{Server: serverKey, Metric: metric, Dim: dim, Bucket: at, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	rec(state.AnalyticsCounter, analytics.MessagesSent, general, yesterday.Add(10*time.Hour), 7)
	rec(state.AnalyticsCounter, analytics.MessagesSent, "999", yesterday.Add(11*time.Hour), 2)
	rec(state.AnalyticsCounter, analytics.MembersJoined, "", yesterday.Add(9*time.Hour), 3)
	rec(state.AnalyticsGaugeLast, analytics.MembersTotal, "", yesterday.Add(12*time.Hour), 42)
	if err := a.MarkActive(ctx, serverKey, analytics.ActiveSetAll, analytics.ActiveDay(time.Now()), "tok"); err != nil {
		t.Fatal(err)
	}

	c := e.client()
	c.login(e.moderator().ID)

	var series analytics.Series
	q := url.Values{"metrics": {"messages.sent,members.active"}, "from": {yesterday.Format(time.RFC3339)}, "to": {today.AddDate(0, 0, 1).Format(time.RFC3339)}}
	c.expect(200, "GET", analyticsPath+"series?"+q.Encode(), nil).json(t, &series)
	if series.Server != serverKey || series.Step != "day" || !series.From.Equal(yesterday) || len(series.Series) != 4 {
		t.Fatalf("series = %+v", series)
	}
	want := map[string][2]float64{"": {9, 0}, general: {7, 0}, "999": {2, 0}}
	for _, s := range series.Series[:3] {
		w, ok := want[s.Dim]
		if s.Metric != analytics.MessagesSent || !ok || len(s.Points) != 2 || s.Points[0][1] != w[0] || s.Points[1][1] != w[1] ||
			s.Points[0][0] != float64(yesterday.Unix()) {
			t.Errorf("messages.sent %q = %+v", s.Dim, s.Points)
		}
	}
	if s := series.Series[3]; s.Metric != analytics.MembersActive || len(s.Points) != 1 || s.Points[0] != (analytics.Point{float64(today.Unix()), 1}) {
		t.Errorf("members.active (today live) = %+v", s)
	}
	if l := series.Labels[general]; l.Name != e.repo.Servers[serverKey].Spec.Channels["general"].Name || l.Kind != "channel" || l.Deleted {
		t.Errorf("label of general = %+v", l)
	}
	if l := series.Labels["999"]; !l.Deleted || l.Name != "999" {
		t.Errorf("label of a deleted channel = %+v", l)
	}
	st := series.Status
	if !st.Enabled || st.RetentionDays != 400 || st.Presence || st.Since == nil || !st.Since.Equal(yesterday.Add(9*time.Hour)) || st.Missing == nil {
		t.Errorf("status = %+v", st)
	}

	var sum analytics.Summary
	r := c.expect(200, "GET", analyticsPath+"summary?period=30d", nil)
	r.json(t, &sum)
	if sum.Period != "30d" || len(sum.KPIs) != len(analytics.SummaryKPIs) || sum.KPIs[0].Metric != analytics.MembersTotal || sum.KPIs[0].Value != 42 {
		t.Fatalf("summary = %+v", sum)
	}
	for _, k := range sum.KPIs {
		if k.Metric == analytics.MessagesSent && k.Value != 9 {
			t.Errorf("messages.sent KPI = %v", k.Value)
		}
		if k.Metric == analytics.MembersNet && k.Value != 3 {
			t.Errorf("members.net KPI = %v", k.Value)
		}
	}
	if rk := sum.Rankings[analytics.MessagesSent]; len(rk) != 2 || rk[0].Dim != general || rk[0].Previous != nil {
		t.Errorf("messages ranking = %+v", rk)
	}
	if !strings.Contains(string(r.body), `"invites.uses":[]`) {
		t.Errorf("empty ranking not []: %s", r.body)
	}
	if h := sum.Heatmap[isoWeekdayOf(yesterday)][10]; h != 7 {
		t.Errorf("heatmap = %v", h)
	}

	var cat analytics.CatalogResponse
	c.expect(200, "GET", analyticsPath+"catalog", nil).json(t, &cat)
	if len(cat.Metrics) != len(analytics.Catalog) || !cat.Status.Enabled {
		t.Fatalf("catalog = %+v", cat)
	}
	for _, m := range cat.Metrics {
		if m.Collected == m.Presence {
			t.Errorf("%s collected = %v (presence %v)", m.Name, m.Collected, m.Presence)
		}
	}

	// The runtime's status: injected, with Since still from the store.
	e2 := startAnalytics(t, func(o *Options) {
		o.AnalyticsStatus = func(server string) analytics.Status {
			asked = append(asked, server)
			return analytics.Status{Enabled: true, RetentionDays: 30, Presence: true,
				Missing: []analytics.Missing{{Metrics: []string{analytics.ModerationActions}, Reason: "the bot lacks View Audit Log", Fix: "grant it"}}}
		}
	})
	if err := e2.rt.Store().Analytics().Record(ctx, state.AnalyticsCounter, state.AnalyticsPoint{Server: serverKey, Metric: analytics.MembersJoined, Bucket: yesterday, Value: 1}); err != nil {
		t.Fatal(err)
	}
	c2 := e2.client()
	c2.login(e2.seed.Owner.ID)
	c2.expect(200, "GET", analyticsPath+"catalog", nil).json(t, &cat)
	if len(asked) != 1 || asked[0] != serverKey || cat.Status.RetentionDays != 30 || cat.Status.Since == nil || !cat.Status.Since.Equal(yesterday) {
		t.Errorf("injected status = %+v (asked %v)", cat.Status, asked)
	}
	for _, m := range cat.Metrics {
		if want := m.Name != analytics.ModerationActions; m.Collected != want {
			t.Errorf("%s collected = %v", m.Name, m.Collected)
		}
	}
}

func isoWeekdayOf(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }
