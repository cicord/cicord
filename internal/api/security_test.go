package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevHostAllowList(t *testing.T) {
	h := New(Options{Mode: "dev", Logger: testLogger(), PublicURL: "https://codi.example.com"})
	for host, want := range map[string]int{
		"127.0.0.1:7420":        http.StatusOK,
		"localhost:7420":        http.StatusOK,
		"[::1]:7420":            http.StatusOK,
		"codi.example.com":      http.StatusOK,
		"evil.example.net:7420": http.StatusMisdirectedRequest, // DNS rebinding
		"127.0.0.1.nip.io:7420": http.StatusMisdirectedRequest,
	} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s: status %d, want %d", host, rec.Code, want)
		}
	}
	// serve mode does not restrict hosts (reverse proxies)
	hs := New(Options{Mode: "serve", Logger: testLogger()})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "codi.anything.org"
	rec := httptest.NewRecorder()
	hs.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("serve mode host check: %d", rec.Code)
	}
}

func TestDevLoginRequiresSimOrToken(t *testing.T) {
	h := New(Options{Mode: "dev", Logger: testLogger(), RuntimeToken: "tok"})
	do := func(auth string) int {
		req := httptest.NewRequest(http.MethodPost, "/auth/dev-login", strings.NewReader(`{"userId":"1"}`))
		req.Host = "127.0.0.1:7420"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Codi-Request", "1")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := do(""); got != http.StatusForbidden {
		t.Errorf("dev-login without sim and token: %d, want 403", got)
	}
	if got := do("tok"); got == http.StatusForbidden {
		t.Errorf("dev-login with the runtime token must pass the guard, got %d", got)
	}
}

func TestDevLoopbackOriginsInterchangeable(t *testing.T) {
	s := &server{opts: Options{Mode: "dev", PublicURL: "http://127.0.0.1:7420"}}
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Host = "localhost:7420"
	for origin, want := range map[string]bool{
		"http://127.0.0.1:7420": true,
		"http://localhost:7420": true,
		"http://localhost:5173": false, // other port
		"http://evil.test:7420": false,
	} {
		if got := s.sameOrigin(req, origin); got != want {
			t.Errorf("origin %s: %v, want %v", origin, got, want)
		}
	}
	prod := &server{opts: Options{Mode: "serve", PublicURL: "https://codi.example.com"}}
	if prod.sameOrigin(req, "http://localhost:7420") {
		t.Errorf("serve mode must not accept loopback origins")
	}
}
