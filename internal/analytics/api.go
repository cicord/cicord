package analytics

import "time"

// The JSON contract of the analytics API (admin console, @cicord/client).
//
//	GET /api/v1/servers/{server}/analytics/summary?period=7d|30d|90d|365d
//	GET /api/v1/servers/{server}/analytics/summary?from=RFC3339&to=RFC3339
//	    → Summary: the dashboard's headline numbers, each compared with the previous
//	      period of the same length, plus the rankings and the activity heatmap. A
//	      custom from..to (instead of period) is widened to whole UTC hours and lasts
//	      1 hour to 400 days.
//	GET /api/v1/servers/{server}/analytics/series?metrics=a,b&from=RFC3339&to=RFC3339&step=hour|day|week[&dim=…][&top=N]
//	    → Series: time series of the requested metrics. Counters are summed per step;
//	      gauges keep the step's last (or max) value; daily distinct metrics are read
//	      per day (with step=week: the average of the days). Without dim, a metric with
//	      a dimension returns its top N dimensions (default 8) plus "other" and the
//	      total ("").
//	GET /api/v1/servers/{server}/analytics/catalog
//	    → Catalog: every metric with kind, unit, dimension and whether it is collected.
//
// Access: moderator and admin of the server (admin host only, like the other
// /api/v1/servers routes). Times are RFC 3339 UTC; buckets are aligned on UTC hours,
// days (00:00) and ISO weeks (Monday 00:00). from defaults to to minus 7 days, to to
// now; at most 400 days per request and at most 2000 points per series.

// Status describes collection for a server.
type Status struct {
	Enabled bool `json:"enabled"`
	// Presence is true when detailed presence is collected (spec.analytics.presence
	// and the privileged intent granted); false means only the approximate online count.
	Presence bool `json:"presence"`
	// Since is the oldest bucket recorded (null before the first point).
	Since *time.Time `json:"since"`
	// RetentionDays is how long points are kept.
	RetentionDays int `json:"retentionDays"`
	// Missing lists what cannot be collected now and why, e.g. a missing gateway intent
	// or a missing bot permission (ViewAuditLog for moderation actions, ManageServer
	// for invite uses). Empty when everything is collected.
	Missing []Missing `json:"missing"`
}

// Missing is a metric group that is not collected, with the reason and the fix.
type Missing struct {
	Metrics []string `json:"metrics"`
	Reason  string   `json:"reason"`
	Fix     string   `json:"fix"`
}

// Label resolves a dimension to a display name (a channel or role ID to its current
// name and type, a custom emoji to its name and image URL).
type Label struct {
	Name string `json:"name"`
	// Kind is "channel", "voice", "thread", "role", "emoji", "event", "invite" or "label".
	Kind string `json:"kind"`
	// ImageURL is set for custom emojis.
	ImageURL string `json:"imageUrl,omitempty"`
	// Color is set for roles ("#rrggbb").
	Color string `json:"color,omitempty"`
	// Deleted is true when the channel, role or emoji no longer exists.
	Deleted bool `json:"deleted,omitempty"`
}

// Point is one value of a series: [unix seconds of the bucket start, value].
type Point [2]float64

// Serie is one metric (and dimension) over time.
type Serie struct {
	Metric string `json:"metric"`
	// Dim is "" for the metric's total, "other" for the dimensions beyond top.
	Dim    string  `json:"dim"`
	Points []Point `json:"points"`
}

// Series is the response of …/analytics/series.
type Series struct {
	Server string           `json:"server"`
	From   time.Time        `json:"from"`
	To     time.Time        `json:"to"`
	Step   string           `json:"step"`
	Series []Serie          `json:"series"`
	Labels map[string]Label `json:"labels"`
	Status Status           `json:"status"`
}

// KPI is a headline number with the same number for the previous period.
type KPI struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	// Previous is the value over the previous period of the same length (null when
	// that period is not covered by the data).
	Previous *float64 `json:"previous"`
	// Spark is the KPI per Summary.Step over the period, for a sparkline.
	Spark []Point `json:"spark"`
}

// Ranked is one entry of a ranking (top channels, emojis, invites…).
type Ranked struct {
	Dim   string  `json:"dim"`
	Value float64 `json:"value"`
	// Previous is the entry's value over the previous period (null when not covered).
	Previous *float64 `json:"previous"`
}

// Summary is the response of …/analytics/summary.
type Summary struct {
	Server string `json:"server"`
	Period string `json:"period"` // "7d", "30d", "90d", "365d" or "custom"
	// Step is the step of the KPI sparklines: "day", or "hour" for a custom range
	// shorter than two days.
	Step string    `json:"step"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	// KPIs, in this order: members.total (human members, last value), members.bots
	// (last value), members.joined, members.left,
	// net growth ("members.net": joined - left), members.active (average per day),
	// messages.sent, voice.seconds, members.online (average of the samples),
	// reactions.added, commands.used, moderation.actions.
	KPIs []KPI `json:"kpis"`

	// Rankings over the period, top 10 each: messages per channel, voice time per
	// channel, reactions per emoji, invite uses per invite, commands, moderation
	// actions per action, AutoMod triggers per rule, roles added, account ages of
	// joins, bot commands (bot and command), channels of bot commands, tenure of the
	// members using Cicord's commands, components per bot, and (with presence)
	// activities. Keyed by metric name.
	Rankings map[string][]Ranked `json:"rankings"`

	// Heatmap is human messages per weekday and hour (UTC) over the period:
	// Heatmap[weekday][hour], weekday 0 = Monday.
	Heatmap [7][24]float64 `json:"heatmap"`
	// BotsHeatmap is bot commands (bots.commands) per weekday and hour, like Heatmap.
	BotsHeatmap [7][24]float64 `json:"botsHeatmap"`

	Labels map[string]Label `json:"labels"`
	Status Status           `json:"status"`
}

// CatalogEntry is a metric as …/analytics/catalog reports it.
type CatalogEntry struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Dim       string `json:"dim,omitempty"`
	Unit      string `json:"unit"`
	Source    string `json:"source"`
	Presence  bool   `json:"presence,omitempty"`
	Collected bool   `json:"collected"`
}

// CatalogResponse is the response of …/analytics/catalog.
type CatalogResponse struct {
	Metrics []CatalogEntry `json:"metrics"`
	Status  Status         `json:"status"`
}
