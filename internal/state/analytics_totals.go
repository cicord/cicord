package state

import "context"

// AnalyticsTotals is an optional interface of Analytics stores: both backends
// implement it, the analytics API uses it when available and otherwise sums the
// points of Query itself (fakes of Analytics need not implement it).
//
// It exists because the dashboard needs metric totals over long periods (a
// year of hourly points of messages per channel) and summing them in the
// database avoids loading every dimension's rows.
type AnalyticsTotals interface {
	// Totals returns, for each metric and bucket selected by q, the sum of the
	// values of every dimension (q.Dim is ignored), with Dim "", ordered by metric
	// then bucket.
	Totals(ctx context.Context, q AnalyticsQuery) ([]AnalyticsPoint, error)
}
