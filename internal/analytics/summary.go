package analytics

import (
	"context"
	"time"

	"github.com/cicord/cicord/internal/state"
)

// MembersNet is the derived KPI of net member growth: members.joined minus
// members.left.
const MembersNet = "members.net"

// SummaryPeriods are the periods of …/analytics/summary, in days.
var SummaryPeriods = map[string]int{"7d": 7, "30d": 30, "90d": 90, "365d": 365}

// DefaultSummaryPeriod is the period when the request names none.
const DefaultSummaryPeriod = "7d"

// SummaryKPIs are the KPIs of the summary, in order (api.go).
var SummaryKPIs = []string{
	MembersTotal, MembersJoined, MembersLeft, MembersNet, MembersActive, MessagesSent,
	VoiceSeconds, MembersOnline, ReactionsAdded, CommandsUsed, ModerationActions,
}

// SummaryRankings are the rankings of the summary; PresenceActivity is added when
// presence is collected.
var SummaryRankings = []string{
	MessagesSent, VoiceSeconds, ReactionsAdded, MembersInvitedBy, CommandsUsed,
	ModerationActions, AutoModTriggers, MembersRoleAdded, MembersJoinedByAge,
}

// kpiMode is how a KPI reduces hourly totals over a range.
type kpiMode int

const (
	kpiSum        kpiMode = iota // counters
	kpiLast                      // the latest value (members.total)
	kpiDailyAvg                  // the average of the daily values (members.active)
	kpiSampleAvg                 // the average of the hourly samples (members.online)
	kpiDifference                // members.net: joined - left
)

func kpiModeOf(metric string) kpiMode {
	switch metric {
	case MembersTotal:
		return kpiLast
	case MembersActive:
		return kpiDailyAvg
	case MembersOnline:
		return kpiSampleAvg
	case MembersNet:
		return kpiDifference
	}
	return kpiSum
}

// reduce applies a KPI mode to the hourly values in [from, to). ok is false when a
// gauge has no data in the range (a counter without data is 0).
func reduce(mode kpiMode, hs hourly, from, to time.Time) (float64, bool) {
	n, sum, last := 0, 0.0, 0.0
	for _, h := range hs {
		if h.Bucket.Before(from) || !h.Bucket.Before(to) {
			continue
		}
		n++
		sum += h.Value
		last = h.Value
	}
	switch mode {
	case kpiLast:
		return last, n > 0
	case kpiDailyAvg, kpiSampleAvg:
		if n == 0 {
			return 0, false
		}
		return sum / float64(n), true
	}
	return sum, true
}

// ParseSummaryPeriod validates the period of …/analytics/summary ("" is the default).
func ParseSummaryPeriod(period string) (string, int, error) {
	if period == "" {
		period = DefaultSummaryPeriod
	}
	days, ok := SummaryPeriods[period]
	if !ok {
		return "", 0, badRequest("period must be 7d, 30d, 90d or 365d")
	}
	return period, days, nil
}

// Summary answers …/analytics/summary (see api.go). The period is made of whole
// UTC days and ends with today: [today - (days-1), tomorrow). The previous period
// is the same number of days just before; it is "covered" (previous values are
// not null) when collection started before its first day ended.
func (r *Reader) Summary(ctx context.Context, server, period string, status Status) (Summary, error) {
	period, days, err := ParseSummaryPeriod(period)
	if err != nil {
		return Summary{}, err
	}
	st, err := r.withSince(ctx, server, status)
	if err != nil {
		return Summary{}, err
	}
	to := dayFloor(r.now()).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -days)
	prev := from.AddDate(0, 0, -days)
	covered := st.Since != nil && st.Since.Before(prev.Add(24*time.Hour))

	out := Summary{Server: server, Period: period, From: from, To: to, KPIs: []KPI{},
		Rankings: map[string][]Ranked{}, Status: st}

	// Hourly totals of the KPI metrics over both periods.
	var metrics []string
	for _, k := range SummaryKPIs {
		if k != MembersNet {
			metrics = append(metrics, k)
		}
	}
	tot, err := r.totals(ctx, server, metrics, prev, to)
	if err != nil {
		return Summary{}, err
	}
	if tot[MembersActive], err = r.withToday(ctx, server, MembersActive, tot[MembersActive], prev, to); err != nil {
		return Summary{}, err
	}
	value := func(metric string, a, b time.Time) (float64, bool) {
		mode := kpiModeOf(metric)
		if mode == kpiDifference {
			j, _ := reduce(kpiSum, tot[MembersJoined], a, b)
			l, _ := reduce(kpiSum, tot[MembersLeft], a, b)
			return j - l, true
		}
		return reduce(mode, tot[metric], a, b)
	}
	for _, k := range SummaryKPIs {
		kpi := KPI{Metric: k, Spark: []Point{}}
		kpi.Value, _ = value(k, from, to)
		if covered {
			if v, ok := value(k, prev, from); ok {
				kpi.Previous = &v
			}
		}
		for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
			if v, ok := value(k, d, d.AddDate(0, 0, 1)); ok {
				kpi.Spark = append(kpi.Spark, Point{float64(d.Unix()), v})
			}
		}
		out.KPIs = append(out.KPIs, kpi)
	}

	// Heatmap: human messages per weekday and hour.
	for _, h := range tot[MessagesSent] {
		if !h.Bucket.Before(from) && h.Bucket.Before(to) {
			out.Heatmap[isoWeekday(h.Bucket)][h.Bucket.Hour()] += h.Value
		}
	}

	// Rankings.
	rankings := SummaryRankings
	if st.Presence {
		rankings = append(rankings[:len(rankings):len(rankings)], PresenceActivity)
	}
	var refs []DimRef
	for _, metric := range rankings {
		m, _ := Lookup(metric)
		list, err := r.ranking(ctx, server, metric, from, to, prev, covered)
		if err != nil {
			return Summary{}, err
		}
		out.Rankings[metric] = list
		for _, e := range list {
			refs = append(refs, DimRef{Kind: m.Dim, Value: e.Dim})
		}
	}
	out.Labels = ResolveLabels(ctx, r.Labeler, server, refs)
	return out, nil
}

// ranking returns the top RankingSize dimensions of a metric over [from, to), with
// their value over [prev, from) when covered.
func (r *Reader) ranking(ctx context.Context, server, metric string, from, to, prev time.Time, covered bool) ([]Ranked, error) {
	ds, err := r.Store.Dims(ctx, server, metric, from, to, RankingSize+1)
	if err != nil {
		return nil, err
	}
	out := []Ranked{}
	for _, d := range ds {
		if d.Dim != "" && len(out) < RankingSize {
			out = append(out, Ranked{Dim: d.Dim, Value: d.Value})
		}
	}
	if !covered || len(out) == 0 {
		return out, nil
	}
	before, err := r.Store.Dims(ctx, server, metric, prev, from, state.MaxListLimit)
	if err != nil {
		return nil, err
	}
	pv := make(map[string]float64, len(before))
	for _, d := range before {
		pv[d.Dim] = d.Value
	}
	for i := range out {
		v := pv[out[i].Dim]
		out[i].Previous = &v
	}
	return out, nil
}
