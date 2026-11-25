package analytics

import (
	"context"
	"net/url"
	"slices"
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
	MembersTotal, MembersBots, MembersJoined, MembersLeft, MembersNet, MembersActive, MessagesSent,
	VoiceSeconds, MembersOnline, ReactionsAdded, CommandsUsed, ModerationActions,
}

// SummaryRankings are the rankings of the summary; PresenceActivity is added when
// presence is collected.
var SummaryRankings = []string{
	MessagesSent, VoiceSeconds, ReactionsAdded, MembersInvitedBy, CommandsUsed,
	ModerationActions, AutoModTriggers, MembersRoleAdded, MembersJoinedByAge,
	BotsCommands, BotsCommandChannels, BotsCommandTenure, ComponentsUsed,
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
	case MembersTotal, MembersBots:
		return kpiLast
	case MembersActive, MembersActiveBots:
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

// CustomPeriod is Summary.Period for a from..to range.
const CustomPeriod = "custom"

// hourlySparkBelow: a custom range shorter than this has hourly sparklines.
const hourlySparkBelow = 48 * time.Hour

// SummaryRequest is the range of a summary: a preset Period, or From and To (custom).
type SummaryRequest struct {
	Period   string
	From, To time.Time
}

// ParseSummaryRequest reads ?period= or ?from=&to= (RFC 3339) of …/analytics/summary.
// A custom range is widened to whole UTC hours; it must last 1 hour to MaxRangeDays
// days and start before now.
func ParseSummaryRequest(q url.Values, now time.Time) (SummaryRequest, error) {
	fromS, toS, period := q.Get("from"), q.Get("to"), q.Get("period")
	if fromS == "" && toS == "" {
		period, _, err := ParseSummaryPeriod(period)
		return SummaryRequest{Period: period}, err
	}
	if period != "" {
		return SummaryRequest{}, badRequest("give either period or from and to, not both")
	}
	if fromS == "" || toS == "" {
		return SummaryRequest{}, badRequest("a custom range needs both from and to")
	}
	parse := func(name, v string) (time.Time, error) {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return t, badRequest("%s must be an RFC 3339 time, e.g. 2026-11-24T00:00:00Z", name)
		}
		return t.UTC(), nil
	}
	from, err := parse("from", fromS)
	if err != nil {
		return SummaryRequest{}, err
	}
	to, err := parse("to", toS)
	if err != nil {
		return SummaryRequest{}, err
	}
	from = hourOf(from)
	if c := hourOf(to); c.Before(to) {
		to = c.Add(time.Hour)
	}
	switch {
	case !from.Before(to):
		return SummaryRequest{}, badRequest("from must be before to")
	case to.Sub(from) > MaxRangeDays*24*time.Hour:
		return SummaryRequest{}, badRequest("at most %d days per request", MaxRangeDays)
	case !from.Before(now):
		return SummaryRequest{}, badRequest("from must be in the past")
	}
	return SummaryRequest{Period: CustomPeriod, From: from, To: to}, nil
}

// Summary answers …/analytics/summary for a preset period ("" is the default).
func (r *Reader) Summary(ctx context.Context, server, period string, status Status) (Summary, error) {
	period, _, err := ParseSummaryPeriod(period)
	if err != nil {
		return Summary{}, err
	}
	return r.SummaryFor(ctx, server, SummaryRequest{Period: period}, status)
}

// SummaryFor answers …/analytics/summary (see api.go). A preset period is made of
// whole UTC days and ends with today: [today - (days-1), tomorrow); a custom range is
// the request's. The previous period has the same length just before; it is "covered"
// (previous values are not null) when collection started before its first sparkline
// step ended. Sparklines are per day, or per hour for a custom range shorter than two
// days.
func (r *Reader) SummaryFor(ctx context.Context, server string, req SummaryRequest, status Status) (Summary, error) {
	return r.summarize(ctx, server, req, status, SummaryKPIs)
}

// summarize is SummaryFor with the KPIs to compute (the portal has its own list).
func (r *Reader) summarize(ctx context.Context, server string, req SummaryRequest, status Status, kpis []string) (Summary, error) {
	from, to := req.From, req.To
	period := req.Period
	if period != CustomPeriod {
		var days int
		var err error
		if period, days, err = ParseSummaryPeriod(period); err != nil {
			return Summary{}, err
		}
		to = dayFloor(r.now()).AddDate(0, 0, 1)
		from = to.AddDate(0, 0, -days)
	}
	st, err := r.withSince(ctx, server, status)
	if err != nil {
		return Summary{}, err
	}
	step, stepName := 24*time.Hour, "day"
	if period == CustomPeriod && to.Sub(from) < hourlySparkBelow {
		step, stepName = time.Hour, "hour"
	}
	prev := from.Add(-to.Sub(from))
	covered := st.Since != nil && st.Since.Before(prev.Add(step))

	out := Summary{Server: server, Period: period, Step: stepName, From: from, To: to, KPIs: []KPI{},
		Rankings: map[string][]Ranked{}, Status: st}

	// Hourly totals of the KPI metrics over both periods.
	var metrics []string
	for _, k := range kpis {
		switch k {
		case MembersNet:
			metrics = append(metrics, MembersJoined, MembersLeft)
		case BotsCommands, MessagesSent:
		default:
			metrics = append(metrics, k)
		}
	}
	metrics = append(metrics, MessagesSent, BotsCommands) // the heatmaps
	tot, err := r.totals(ctx, server, slices.Compact(slices.Sorted(slices.Values(metrics))), prev, to)
	if err != nil {
		return Summary{}, err
	}
	for _, k := range kpis {
		if _, daily := dailyDistinct(k); daily {
			if tot[k], err = r.withToday(ctx, server, k, tot[k], prev, to); err != nil {
				return Summary{}, err
			}
		}
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
	for _, k := range kpis {
		kpi := KPI{Metric: k, Spark: []Point{}}
		kpi.Value, _ = value(k, from, to)
		if covered {
			if v, ok := value(k, prev, from); ok {
				kpi.Previous = &v
			}
		}
		for d := from; d.Before(to); d = d.Add(step) {
			if v, ok := value(k, d, d.Add(step)); ok {
				kpi.Spark = append(kpi.Spark, Point{float64(d.Unix()), v})
			}
		}
		out.KPIs = append(out.KPIs, kpi)
	}

	// Heatmaps: human messages, and bot commands, per weekday and hour.
	for _, h := range tot[MessagesSent] {
		if !h.Bucket.Before(from) && h.Bucket.Before(to) {
			out.Heatmap[isoWeekday(h.Bucket)][h.Bucket.Hour()] += h.Value
		}
	}
	for _, h := range tot[BotsCommands] {
		if !h.Bucket.Before(from) && h.Bucket.Before(to) {
			out.BotsHeatmap[isoWeekday(h.Bucket)][h.Bucket.Hour()] += h.Value
		}
	}

	// Rankings.
	rankings := SummaryRankings
	if st.Presence {
		rankings = append(rankings[:len(rankings):len(rankings)], PresenceActivity)
	}
	var refs []DimRef
	owners := r.ownerMemo(server)
	for _, metric := range rankings {
		m, _ := Lookup(metric)
		var list []Ranked
		var err error
		if groupable(m) && r.Owners != nil {
			list, err = r.groupedRanking(ctx, server, metric, from, to, prev, covered, owners)
		} else {
			list, err = r.ranking(ctx, server, metric, from, to, prev, covered)
		}
		if err != nil {
			return Summary{}, err
		}
		out.Rankings[metric] = list
		for _, e := range list {
			refs = append(refs, DimRef{Kind: m.Dim, Value: e.Dim})
			for _, c := range e.Children {
				refs = append(refs, DimRef{Kind: m.Dim, Value: c.Dim})
			}
		}
	}
	out.Labels = ResolveLabels(ctx, r.Labeler, server, refs)
	return out, nil
}

// groupedRanking is ranking for a channel metric whose plugin-created channels are
// grouped per plugin instance: the top RankingSize entries, a group with its
// channels as children (at most MaxGroupChildren, the rest in an "other" child).
func (r *Reader) groupedRanking(ctx context.Context, server, metric string, from, to, prev time.Time, covered bool, owners *ownerMemo) ([]Ranked, error) {
	ds, err := r.Store.Dims(ctx, server, metric, from, to, state.MaxListLimit)
	if err != nil {
		return nil, err
	}
	entries := groupDims(ds, owners.of(ctx, ds), nil)
	entries = entries[:min(len(entries), RankingSize)]
	out := make([]Ranked, 0, len(entries))
	for _, e := range entries {
		rk := Ranked{Dim: e.Dim, Value: e.Value}
		if e.Members != nil {
			rk.Count = len(e.Members)
			rk.Children = []Ranked{}
			rest := 0.0
			for i, m := range e.Members {
				if i < MaxGroupChildren {
					rk.Children = append(rk.Children, Ranked{Dim: m.Dim, Value: m.Value})
				} else {
					rest += m.Value
				}
			}
			if len(e.Members) > MaxGroupChildren {
				rk.Children = append(rk.Children, Ranked{Dim: OtherDim, Value: rest})
			}
		}
		out = append(out, rk)
	}
	if !covered || len(out) == 0 {
		return out, nil
	}
	before, err := r.Store.Dims(ctx, server, metric, prev, from, state.MaxListLimit)
	if err != nil {
		return nil, err
	}
	bOwner := owners.of(ctx, before)
	pv := make(map[string]float64, len(before))
	for _, d := range before {
		pv[d.Dim] = d.Value
		if g, ok := bOwner[d.Dim]; ok {
			pv[g] += d.Value
		}
	}
	ptr := func(v float64) *float64 { return &v }
	for i := range out {
		out[i].Previous = ptr(pv[out[i].Dim])
		shown := 0.0
		for j := range out[i].Children {
			c := &out[i].Children[j]
			if c.Dim == OtherDim {
				c.Previous = ptr(pv[out[i].Dim] - shown)
				continue
			}
			c.Previous = ptr(pv[c.Dim])
			shown += pv[c.Dim]
		}
	}
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
