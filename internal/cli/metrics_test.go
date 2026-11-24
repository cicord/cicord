package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func httpDo(t *testing.T, method, url string, hdr ...string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(body)
}

// `cicord serve --metrics-listen` serves the metrics on its own listener only: the API
// listener answers 404 on /metrics, even with the runtime token.
func TestServeMetricsListener(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	t.Setenv("CICORD_SECRET_KEY", "")
	t.Setenv("CICORD_RUNTIME_TOKEN", "rt-token")
	t.Setenv("CICORD_METRICS_LISTEN", "")
	addr, metricsAddr := freeAddr(t), freeAddr(t)
	base, metricsURL := "http://"+addr, "http://"+metricsAddr+"/metrics"
	p := startCicord(t, "serve", "--local", "--listen", addr, "--public-url", base, "--metrics-listen", metricsAddr,
		"--data-dir", filepath.Join(dir, ".cicord", "data"), "--log-level", "error", dir)
	p.wait(t, regexp.MustCompile(`listening on `+regexp.QuoteMeta(base)))
	defer p.stop(t)

	if code, _, _ := httpDo(t, http.MethodGet, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("healthz = %d", code)
	}
	for _, auth := range []string{"", "Bearer rt-token"} {
		code, _, body := httpDo(t, http.MethodGet, base+"/metrics", "Authorization", auth)
		if code != http.StatusNotFound || strings.Contains(body, "cicord_build_info") {
			t.Errorf("API /metrics (auth %v) = %d %.100s, want 404", auth != "", code, body)
		}
	}

	code, ctype, body := httpDo(t, http.MethodGet, metricsURL)
	if code != http.StatusOK || ctype != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("metrics = %d %q", code, ctype)
	}
	for _, want := range []string{
		"# TYPE cicord_build_info gauge\ncicord_build_info{",
		`cicord_http_requests_total{route="GET /healthz",status="2xx"} 1`,
		`cicord_http_requests_total{route="/metrics",status="4xx"} 2`,
		"# TYPE cicord_revision_active gauge\ncicord_revision_active ",
		"go_goroutines ",
		"process_start_time_seconds ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics: missing %q", want)
		}
	}
	if code, _, _ := httpDo(t, http.MethodGet, "http://"+metricsAddr+"/"); code != http.StatusNotFound {
		t.Errorf("metrics listener / = %d, want 404", code)
	}
	if code, _, _ := httpDo(t, http.MethodGet, "http://"+metricsAddr+"/api/v1/status"); code != http.StatusNotFound {
		t.Errorf("metrics listener /api/v1/status = %d, want 404", code)
	}
	if code, _, _ := httpDo(t, http.MethodPost, metricsURL); code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d, want 405", code)
	}
}

func TestServeMetricsListenFailure(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	t.Setenv("CICORD_SECRET_KEY", "")
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	p := startCicord(t, "serve", "--local", "--listen", freeAddr(t), "--metrics-listen", busy.Addr().String(),
		"--data-dir", filepath.Join(dir, ".cicord", "data"), "--log-level", "error", dir)
	if code := <-p.done; code == 0 || !strings.Contains(p.stderr.String(), "--metrics-listen") {
		t.Fatalf("exit %d, stderr:\n%s", code, p.stderr.String())
	}
}
