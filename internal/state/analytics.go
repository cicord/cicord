package state

import (
	"context"
	"time"
)

// ───────────────────────────────────────────────────────────── analytics

// AnalyticsKind says how points of a metric combine inside one bucket.
type AnalyticsKind string

const (
	// AnalyticsCounter values are summed (messages sent in the hour).
	AnalyticsCounter AnalyticsKind = "counter"
	// AnalyticsGaugeMax keeps the highest value seen in the bucket (peak voice users).
	AnalyticsGaugeMax AnalyticsKind = "max"
	// AnalyticsGaugeLast keeps the most recent value written in the bucket (member count).
	AnalyticsGaugeLast AnalyticsKind = "last"
)

// AnalyticsPoint is one aggregated value of a server's metric in an hourly bucket.
//
// Bucket is the start of a UTC hour (minutes, seconds and nanoseconds are zero).
// Dim is the metric's optional dimension: a Discord channel or role ID, an emoji, a
// command name, an invite code, a fixed label ("<1d", "online"…); "" when the metric
// has none. Metric names are the analytics catalog's (internal/analytics). Never
// a member ID, a username or message content: analytics are aggregates.
type AnalyticsPoint struct {
	Server string // server key
	Metric string
	Dim    string
	Bucket time.Time
	Value  float64
}

// AnalyticsQuery selects points of one server: the metrics (all when empty), the
// buckets in [From, To) and optionally one dimension (Dim) or a set of dimensions
// (Dims, at most MaxListLimit; nil or empty: every dimension). When both are set,
// a point must match both.
type AnalyticsQuery struct {
	Server  string
	Metrics []string
	Dim     *string
	Dims    []string
	From    time.Time
	To      time.Time
}

// Analytics stores per-server activity aggregates (the analytics dashboard). Points
// are hourly; the API rolls them up by day or week when it reads them.
//
// Record merges points into their (Server, Metric, Dim, Bucket) row according to the
// kind: AnalyticsCounter adds Value, AnalyticsGaugeMax keeps the larger value,
// AnalyticsGaugeLast replaces it. Buckets are truncated to the hour by the store.
// Record of several points is atomic.
//
// Distinct members: MarkActive remembers that an opaque token (an HMAC of the member
// ID with a key that changes every day and is never stored with it) was active in a
// set (e.g. "messages" for a day "2026-11-24"), and CountActive returns how many
// distinct tokens a set has. DropActive deletes the tokens of the days before a date
// once they are rolled up into a counter, so no per-member trace outlives the day.
type Analytics interface {
	Record(ctx context.Context, kind AnalyticsKind, points ...AnalyticsPoint) error
	// Query returns the points ordered by metric, dim, then bucket.
	Query(ctx context.Context, q AnalyticsQuery) ([]AnalyticsPoint, error)
	// Dims returns the dimensions recorded for a metric in [from, to), with their sum
	// (counters) or max (gauges), largest first, at most limit (<= 0: 100).
	Dims(ctx context.Context, server, metric string, from, to time.Time, limit int) ([]AnalyticsDim, error)

	MarkActive(ctx context.Context, server, set, day, token string) error
	CountActive(ctx context.Context, server, set, day string) (int64, error)
	// DropActive deletes the active tokens of every day strictly before day ("YYYY-MM-DD").
	DropActive(ctx context.Context, before string) (int64, error)

	// Purge deletes the points of a server older than before (retention) and returns
	// how many rows it removed. server "" purges every server.
	Purge(ctx context.Context, server string, before time.Time) (int64, error)
	// Since returns the oldest bucket recorded for a server (zero time when none).
	Since(ctx context.Context, server string) (time.Time, error)
}

// AnalyticsDim is a dimension of a metric with its aggregated value.
type AnalyticsDim struct {
	Dim   string
	Value float64
}
