package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cicord/cicord/internal/metrics"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// TestMetricsNeverServedByAPI: /metrics is only exposed by the dedicated listener of
// `cicord serve --metrics-listen`. On the API listener it answers 404 on the public host,
// on the admin host and with the runtime token, instead of falling through to the portal.
func TestMetricsNeverServedByAPI(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for name, admin := range map[string]string{"admin URL": adminOrigin, "no admin URL": ""} {
		t.Run(name, func(t *testing.T) {
			reg := metrics.NewRegistry()
			reg.NewFunc(metrics.Gauge, "secret_gauge", "", nil, func(emit metrics.Emit) { emit(1) })
			ts := httptest.NewServer(New(Options{Store: store, Logger: testLogger(), PublicURL: portalOrigin, AdminURL: admin,
				RuntimeToken: testToken, Metrics: reg,
				Portal: fstest.MapFS{"index.html": {Data: []byte("<html>portal</html>")}}}))
			t.Cleanup(ts.Close)
			for _, host := range []string{portalHost, adminHost} {
				for _, auth := range []string{"", "Bearer " + testToken} {
					for _, path := range []string{"/metrics", "/metrics/", "/Metrics"} {
						req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
						req.Host = host
						if auth != "" {
							req.Header.Set("Authorization", auth)
						}
						res, err := http.DefaultClient.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						body, _ := io.ReadAll(res.Body)
						res.Body.Close()
						if path == "/Metrics" {
							// another path: the portal SPA, never the metrics
							if strings.Contains(string(body), "secret_gauge") {
								t.Errorf("%s %s: metrics served", host, path)
							}
							continue
						}
						if res.StatusCode != http.StatusNotFound || strings.Contains(string(body), "secret_gauge") || strings.Contains(string(body), "portal") {
							t.Errorf("%s %s (auth %v) = %d %s, want a 404", host, path, auth != "", res.StatusCode, body)
						}
					}
				}
			}
			var out strings.Builder
			_ = reg.Write(&out)
			if !strings.Contains(out.String(), `cicord_http_requests_total{route="/metrics",status="4xx"} 4`+"\n") {
				t.Errorf("request metrics:\n%s", out.String())
			}
		})
	}
}

func TestStatusClass(t *testing.T) {
	for code, want := range map[int]string{0: "2xx", 101: "1xx", 200: "2xx", 204: "2xx", 302: "3xx", 404: "4xx", 421: "4xx", 500: "5xx", 503: "5xx"} {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d) = %s, want %s", code, got, want)
		}
	}
	if routeLabel("") != "unmatched" || routeLabel("GET /api/v1/me") != "GET /api/v1/me" {
		t.Error("routeLabel")
	}
}
