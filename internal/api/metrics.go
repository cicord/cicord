package api

import "github.com/cicord/cicord/internal/metrics"

// HTTP metrics (https://cicord.dev/docs/self-hosting/monitoring/). The API never serves
// them: `cicord serve --metrics-listen` exposes the registry on a separate listener.

type apiMetrics struct {
	requests *metrics.CounterVec // route, status
	webhooks *metrics.CounterVec // result
}

// Webhook delivery results (cicord_webhook_deliveries_total).
const (
	webhookAccepted = "accepted" // push to the tracked branch: a fetch was triggered
	webhookIgnored  = "ignored"  // ping, another branch, an event that is not handled
	webhookRejected = "rejected" // bad signature, payload too large or invalid
	webhookDisabled = "disabled" // no webhook secret configured
)

func newAPIMetrics(reg *metrics.Registry) *apiMetrics {
	if reg == nil {
		reg = metrics.NewRegistry()
	}
	return &apiMetrics{
		requests: reg.NewCounter("cicord_http_requests_total",
			`HTTP API requests, by route pattern ("unmatched" when no route was reached) and status class (2xx…5xx).`,
			"route", "status"),
		webhooks: reg.NewCounter("cicord_webhook_deliveries_total",
			"GitHub webhook deliveries, by result: accepted, ignored, rejected (signature, payload) or disabled (no secret).",
			"result"),
	}
}

// routeLabel is the route pattern a request matched, as registered in routes() ("GET
// /api/v1/servers/{server}", "/"): a fixed set, never the request path nor the
// client-chosen method.
func routeLabel(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

// statusClass maps a status code to "2xx"…"5xx" (0, nothing written, is a 200).
func statusClass(code int) string {
	switch {
	case code == 0 || code >= 200 && code < 300:
		return "2xx"
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	}
	return "1xx"
}
