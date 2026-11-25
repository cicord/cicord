package analytics

import (
	"context"
	"strings"
	"time"
)

// The community portal's view of the analytics (GET /api/v1/portal/{server}/analytics),
// opt-in per server with spec.analytics.portal ("members" or "public"). It is a curated
// subset of the summary, safe to show to members: server-wide aggregates only, rankings
// limited to channels everyone can see, nothing per member.

// PortalKPIs are the headline numbers of the portal, in order.
var PortalKPIs = []string{
	MembersTotal, MembersJoined, MembersActive, MessagesSent, VoiceSeconds,
	ReactionsAdded, BotsCommands, MembersActiveBots,
}

// PortalPeriods are the periods the portal may ask for.
var PortalPeriods = []string{"7d", "30d", "90d"}

// DefaultPortalPeriod is the portal's period when the request names none.
const DefaultPortalPeriod = "30d"

// PortalChannel is a channel of the portal's ranking.
type PortalChannel struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// PortalCommand is a bot command of the portal's ranking.
type PortalCommand struct {
	Bot     string  `json:"bot"`
	Command string  `json:"command"`
	Value   float64 `json:"value"`
}

// PortalAnalytics is the response of GET /api/v1/portal/{server}/analytics.
type PortalAnalytics struct {
	Server string    `json:"server"`
	Period string    `json:"period"` // "7d", "30d" or "90d"
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	// Since is the oldest data point (null before the first one).
	Since *time.Time `json:"since"`
	// KPIs, in the order of PortalKPIs; spark is per day.
	KPIs []KPI `json:"kpis"`
	// TopChannels are the channels with the most messages among those @everyone can
	// see, top 5.
	TopChannels []PortalChannel `json:"topChannels"`
	// TopBotCommands are the most used bot commands (every bot), top 8.
	TopBotCommands []PortalCommand `json:"topBotCommands"`
	// Heatmap is messages per weekday (0 = Monday) and UTC hour.
	Heatmap [7][24]float64 `json:"heatmap"`
}

// Portal answers the portal's analytics. public tells whether @everyone can see a
// channel and gives its current name; other channels are left out of the rankings.
func (r *Reader) Portal(ctx context.Context, server, period string, public func(channelID string) (name string, ok bool)) (PortalAnalytics, error) {
	if period == "" {
		period = DefaultPortalPeriod
	}
	ok := false
	for _, p := range PortalPeriods {
		ok = ok || p == period
	}
	if !ok {
		return PortalAnalytics{}, badRequest("period must be 7d, 30d or 90d")
	}
	s, err := r.summarize(ctx, server, SummaryRequest{Period: period}, Status{Enabled: true}, PortalKPIs)
	if err != nil {
		return PortalAnalytics{}, err
	}
	out := PortalAnalytics{Server: server, Period: period, From: s.From, To: s.To, Since: s.Status.Since,
		KPIs: s.KPIs, Heatmap: s.Heatmap, TopChannels: []PortalChannel{}, TopBotCommands: []PortalCommand{}}
	for _, e := range s.Rankings[MessagesSent] {
		if len(out.TopChannels) == 5 {
			break
		}
		if name, ok := public(e.Dim); ok {
			out.TopChannels = append(out.TopChannels, PortalChannel{ID: e.Dim, Name: name, Value: e.Value})
		}
	}
	for _, e := range s.Rankings[BotsCommands] {
		if len(out.TopBotCommands) == 8 {
			break
		}
		bot, cmd, found := strings.Cut(e.Dim, " /")
		if !found {
			continue
		}
		out.TopBotCommands = append(out.TopBotCommands, PortalCommand{Bot: bot, Command: "/" + cmd, Value: e.Value})
	}
	return out, nil
}
