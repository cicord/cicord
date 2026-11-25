package analytics_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/state"
)

// qOwners is a fake Owners (the resource map's plugin-created channels).
type qOwners map[string]string

func (o qOwners) Owners(_ context.Context, _ string, ids []string) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		if v, ok := o[id]; ok {
			out[id] = v
		}
	}
	return out
}

const rooms = "plugin:main/voice-rooms"

// TestGroupedChannels: the rooms a plugin created, live (in the resource map) or
// deleted (remembered by the labels), are one entry of the channel rankings and
// series, with the rooms as children; other metrics are not grouped.
func TestGroupedChannels(t *testing.T) {
	s := qStore(t)
	a := s.Analytics()
	labels := a.(state.AnalyticsLabels)
	day := qDay(-1).Add(10 * time.Hour)
	prevDay := qDay(-8).Add(10 * time.Hour)
	qCounter(t, a, analytics.VoiceSeconds, "10", day, 500)     // a git channel
	qCounter(t, a, analytics.VoiceSeconds, "500", day, 300)    // live room
	qCounter(t, a, analytics.VoiceSeconds, "501", day, 400)    // deleted room
	qCounter(t, a, analytics.VoiceSeconds, "502", prevDay, 70) // a room of the previous period only
	qCounter(t, a, analytics.VoiceSeconds, "10", prevDay, 50)
	qCounter(t, a, analytics.MessagesSent, "501", day, 3)
	qCounter(t, a, analytics.MembersRoleAdded, "501", day, 1) // a role dimension is never grouped
	qMust(t, labels.PutLabels(t.Context(),
		state.AnalyticsLabel{Server: "main", Dim: "channel", ID: "501", Name: "NPjonath's channel", Type: "voice", Owner: rooms, Deleted: true},
		state.AnalyticsLabel{Server: "main", Dim: "channel", ID: "502", Name: "Old room", Type: "voice", Owner: rooms, Deleted: true}))
	qMust(t, a.Record(t.Context(), state.AnalyticsGaugeLast, state.AnalyticsPoint{Server: "main", Metric: analytics.MembersTotal, Bucket: qDay(-20), Value: 1}))

	stored := analytics.StoredLabeler{Store: labels}
	r := &analytics.Reader{Store: a, Now: func() time.Time { return qNow },
		Labeler: analytics.Labelers{qMapLabeler{"10": {Name: "lounge", Kind: "voice"}, "500": {Name: "Bob's room", Kind: "voice"}}, stored},
		Owners:  analytics.OwnersChain{qOwners{"500": rooms}, stored}}
	sum, err := r.Summary(t.Context(), "main", "7d", analytics.Status{Enabled: true})
	qMust(t, err)
	voice := sum.Rankings[analytics.VoiceSeconds]
	if len(voice) != 2 {
		t.Fatalf("voice ranking = %+v", voice)
	}
	g := voice[0]
	if g.Dim != rooms || g.Value != 700 || g.Count != 2 || g.Previous == nil || *g.Previous != 70 || len(g.Children) != 2 ||
		g.Children[0].Dim != "501" || g.Children[0].Value != 400 || *g.Children[0].Previous != 0 ||
		g.Children[1].Dim != "500" || g.Children[1].Value != 300 {
		t.Errorf("group = %+v", g)
	}
	if voice[1].Dim != "10" || voice[1].Value != 500 || *voice[1].Previous != 50 || voice[1].Children != nil {
		t.Errorf("git channel = %+v", voice[1])
	}
	if m := sum.Rankings[analytics.MessagesSent]; len(m) != 1 || m[0].Dim != rooms || m[0].Value != 3 || m[0].Count != 1 {
		t.Errorf("messages ranking = %+v", m)
	}
	if roles := sum.Rankings[analytics.MembersRoleAdded]; len(roles) != 1 || roles[0].Dim != "501" {
		t.Errorf("roles ranking = %+v", roles)
	}
	for dim, want := range map[string]analytics.Label{
		rooms: {Name: "voice-rooms", Kind: "group"},
		"500": {Name: "Bob's room", Kind: "voice"},
		"501": {Name: "NPjonath's channel", Kind: "voice", Deleted: true},
		"10":  {Name: "lounge", Kind: "voice"},
	} {
		if got := sum.Labels[dim]; got != want {
			t.Errorf("label %s = %+v, want %+v", dim, got, want)
		}
	}

	// Series: the group is one serie, the sum of its rooms; it can be asked for alone.
	v, _ := url.ParseQuery("metrics=voice.seconds&top=1&from=" + qDay(-1).Format(time.RFC3339) + "&to=" + qDay(0).Format(time.RFC3339))
	req, err := analytics.ParseSeriesRequest("main", v, qNow)
	qMust(t, err)
	series, err := r.Series(t.Context(), req, analytics.Status{Enabled: true})
	qMust(t, err)
	if len(series.Series) != 3 {
		t.Fatalf("series = %+v", series.Series)
	}
	if got := qPoints(qFind(t, series.Series, analytics.VoiceSeconds, rooms)); len(got) != 1 || got[0] != 700 {
		t.Errorf("group serie = %v", got)
	}
	if got := qPoints(qFind(t, series.Series, analytics.VoiceSeconds, "other")); len(got) != 1 || got[0] != 500 {
		t.Errorf("other serie = %v", got)
	}
	if series.Labels[rooms] != (analytics.Label{Name: "voice-rooms", Kind: "group"}) {
		t.Errorf("series labels = %+v", series.Labels)
	}
	v.Set("dim", rooms)
	req, err = analytics.ParseSeriesRequest("main", v, qNow)
	qMust(t, err)
	series, err = r.Series(t.Context(), req, analytics.Status{Enabled: true})
	qMust(t, err)
	if len(series.Series) != 1 || qPoints(series.Series[0])[0] != 700 {
		t.Errorf("series of the group = %+v", series.Series)
	}

	// Without owners, nothing is grouped.
	plain := &analytics.Reader{Store: a, Now: func() time.Time { return qNow }}
	sum, err = plain.Summary(t.Context(), "main", "7d", analytics.Status{Enabled: true})
	qMust(t, err)
	if voice := sum.Rankings[analytics.VoiceSeconds]; len(voice) != 3 || voice[0].Dim != "10" {
		t.Errorf("ungrouped voice ranking = %+v", voice)
	}
}

// TestGroupChildrenCap: a group lists its largest rooms, the rest summed in "other".
func TestGroupChildrenCap(t *testing.T) {
	s := qStore(t)
	a := s.Analytics()
	owners := qOwners{}
	n := analytics.MaxGroupChildren + 3
	for i := range n {
		id := fmt.Sprint(1000 + i)
		owners[id] = rooms
		qCounter(t, a, analytics.VoiceSeconds, id, qDay(-1), float64(100+i))
	}
	r := &analytics.Reader{Store: a, Now: func() time.Time { return qNow }, Owners: owners}
	sum, err := r.Summary(t.Context(), "main", "7d", analytics.Status{Enabled: true})
	qMust(t, err)
	voice := sum.Rankings[analytics.VoiceSeconds]
	if len(voice) != 1 || voice[0].Count != n || len(voice[0].Children) != analytics.MaxGroupChildren+1 {
		t.Fatalf("voice ranking = %+v", voice)
	}
	kids := voice[0].Children
	total := 0.0
	for _, k := range kids {
		total += k.Value
	}
	last := kids[len(kids)-1]
	if kids[0].Value != float64(100+n-1) || last.Dim != "other" || last.Value != 100+101+102 || total != voice[0].Value {
		t.Errorf("children = %+v (total %v of %v)", kids, total, voice[0].Value)
	}
	if _, ok := sum.Labels["other"]; ok {
		t.Error("the other child is labelled")
	}
}
