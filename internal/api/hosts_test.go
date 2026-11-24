package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/state/sqlite"
)

const (
	portalOrigin = "https://community.example.org"
	adminOrigin  = "https://cicord.example.ts.net"
	portalHost   = "community.example.org"
	adminHost    = "cicord.example.ts.net"
)

// hostEnv is a serve-mode API with a public (portal) URL and a separate admin URL, both
// reached through the same listener, like a reverse proxy and `tailscale serve` would.
type hostEnv struct {
	t  *testing.T
	ts *httptest.Server

	mu        sync.Mutex
	exchanged []string // redirect_uri of each code exchange
}

func newHostEnv(t *testing.T, admin string) *hostEnv {
	t.Helper()
	e := &hostEnv{t: t}
	discordAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			_ = r.ParseForm()
			e.mu.Lock()
			e.exchanged = append(e.exchanged, r.Form.Get("redirect_uri"))
			e.mu.Unlock()
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
		case "/users/@me":
			_, _ = w.Write([]byte(`{"id":"777","username":"zed","global_name":"Zed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(discordAPI.Close)
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h := New(Options{Store: store, Logger: testLogger(), PublicURL: portalOrigin, AdminURL: admin,
		RuntimeToken: testToken, WebhookSecret: testSecret,
		OAuth: OAuth{ClientID: "42", ClientSecret: "shh", AuthorizeURL: discordAPI.URL + "/oauth2/authorize",
			TokenURL: discordAPI.URL + "/oauth2/token", APIURL: discordAPI.URL},
		AdminUI: fstest.MapFS{"index.html": {Data: []byte("<html>admin</html>")}},
		Portal:  fstest.MapFS{"index.html": {Data: []byte("<html>portal</html>")}},
	})
	e.ts = httptest.NewServer(h)
	t.Cleanup(e.ts.Close)
	return e
}

// do sends a request to the test server with the given Host header (rawPath is sent
// as-is, escapes included).
func (e *hostEnv) do(method, host, rawPath string, hdr map[string]string, cookies ...*http.Cookie) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+rawPath, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func cookieNamed(res *http.Response, name string) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestAdminHostMatching(t *testing.T) {
	s := newServer(Options{Logger: testLogger(), PublicURL: portalOrigin, AdminURL: adminOrigin + "/"})
	for host, want := range map[string]bool{
		adminHost:                    true,
		"CICORD.Example.TS.net":      true,
		adminHost + ":443":           true,
		adminHost + ".":              true,
		adminHost + ":8443":          false,
		adminHost + ":80":            false,
		portalHost:                   false,
		"127.0.0.1:7420":             false,
		"evil.cicord.example.ts.net": false,
		"":                           false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		if got := s.onAdminHost(r); got != want {
			t.Errorf("onAdminHost(%q) = %v, want %v", host, got, want)
		}
		wantURL := portalOrigin
		if want {
			wantURL = adminOrigin
		}
		if got := s.publicURL(r); got != wantURL {
			t.Errorf("publicURL(%q) = %q, want %q", host, got, wantURL)
		}
	}
	// A loopback http admin URL with a port: the port must match.
	s = newServer(Options{Logger: testLogger(), PublicURL: portalOrigin, AdminURL: "http://127.0.0.1:7421"})
	for host, want := range map[string]bool{"127.0.0.1:7421": true, "127.0.0.1:7420": false, "127.0.0.1": false} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		if got := s.onAdminHost(r); got != want {
			t.Errorf("http admin: onAdminHost(%q) = %v, want %v", host, got, want)
		}
	}
	// Without an admin URL nothing is the admin host and the public URL is used.
	s = newServer(Options{Logger: testLogger(), PublicURL: portalOrigin})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = adminHost
	if s.onAdminHost(r) || s.publicURL(r) != portalOrigin || !s.hostRouteAllowed(r) {
		t.Error("admin host without an admin URL")
	}
}

// The OAuth login stays on the host it started from: redirect_uri, state cookie,
// callback, code exchange and session cookie.
func TestAdminHostOAuthLogin(t *testing.T) {
	e := newHostEnv(t, adminOrigin)
	for _, c := range []struct{ host, origin string }{{portalHost, portalOrigin}, {adminHost, adminOrigin}} {
		res := e.do("GET", c.host, "/auth/login", nil)
		res.Body.Close()
		loc, _ := url.Parse(res.Header.Get("Location"))
		if res.StatusCode != http.StatusFound || loc.Query().Get("redirect_uri") != c.origin+"/auth/callback" {
			t.Fatalf("%s: login = %d redirect_uri %q", c.host, res.StatusCode, loc.Query().Get("redirect_uri"))
		}
		st := cookieNamed(res, oauthCookie)
		if st == nil || st.Domain != "" || st.Path != "/auth/" || !st.Secure || !st.HttpOnly {
			t.Fatalf("%s: state cookie = %+v", c.host, st)
		}
		// The state cookie is host-only: the callback is on the same host.
		res = e.do("GET", c.host, "/auth/callback?code=c&state="+url.QueryEscape(loc.Query().Get("state")), nil, st)
		res.Body.Close()
		if res.StatusCode != http.StatusFound {
			t.Fatalf("%s: callback = %d", c.host, res.StatusCode)
		}
		e.mu.Lock()
		got := e.exchanged[len(e.exchanged)-1]
		e.mu.Unlock()
		if got != c.origin+"/auth/callback" {
			t.Fatalf("%s: code exchanged with redirect_uri %q", c.host, got)
		}
		sess := cookieNamed(res, sessionCookie)
		if sess == nil || sess.Domain != "" || !sess.Secure || sess.Path != "/" {
			t.Fatalf("%s: session cookie = %+v", c.host, sess)
		}
		res = e.do("GET", c.host, "/api/v1/me", nil, sess)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"id":"777"`) {
			t.Fatalf("%s: me = %d %s", c.host, res.StatusCode, body)
		}
	}
	// A login started on another port of the admin hostname uses the public URL.
	res := e.do("GET", adminHost+":8443", "/auth/login", nil)
	res.Body.Close()
	if loc, _ := url.Parse(res.Header.Get("Location")); loc.Query().Get("redirect_uri") != portalOrigin+"/auth/callback" {
		t.Fatalf("other port: redirect_uri %q", loc.Query().Get("redirect_uri"))
	}
}

func TestAdminHostCSRF(t *testing.T) {
	e := newHostEnv(t, adminOrigin)
	for _, c := range []struct {
		host, origin string
		ok           bool
	}{
		{portalHost, portalOrigin, true},
		{adminHost, adminOrigin, true},
		{adminHost, "https://evil.example", false},
		{portalHost, "https://evil.example", false},
		{adminHost, "http://" + adminHost, false}, // scheme mismatch
	} {
		res := e.do("POST", c.host, "/auth/logout", map[string]string{"Origin": c.origin, "X-Cicord-Request": "1"})
		res.Body.Close()
		if forbidden := res.StatusCode == http.StatusForbidden; forbidden == c.ok {
			t.Errorf("POST /auth/logout on %s from %s = %d", c.host, c.origin, res.StatusCode)
		}
	}
}

func TestAdminHostRoutes(t *testing.T) {
	e := newHostEnv(t, adminOrigin)
	bearer := map[string]string{"Authorization": "Bearer " + testToken}
	adminOnly := []struct{ method, path string }{
		{"GET", "/admin"},
		{"GET", "/admin/"},
		{"GET", "/admin/servers/x"},
		{"GET", "/api/v1/status"},
		{"GET", "/api/v1/history"},
		{"GET", "/api/v1/servers/ma-commu"},
		{"GET", "/api/v1/servers/ma-commu/plugins"},
		{"GET", "/api/v1/servers/ma-commu/diagnostics"},
		{"GET", "/api/v1/catalog"},
		{"GET", "/api/v1/store"},
		{"GET", "/api/v1/store/plugins"},
		{"POST", "/api/v1/registries"},
		{"GET", "/api/v1/setup/status"},
		{"POST", "/api/v1/setup/session"},
		{"POST", "/api/v1/status"}, // wrong method: 404, not 405
	}
	for _, r := range adminOnly {
		res := e.do(r.method, portalHost, r.path, bearer)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"not_found"`) || res.Header.Get("Allow") != "" {
			t.Errorf("public host: %s %s = %d %s", r.method, r.path, res.StatusCode, body)
		}
		res = e.do(r.method, adminHost, r.path, bearer)
		res.Body.Close()
		// (setup/session without a wizard and POST status are 404 on the admin host too)
		if res.StatusCode == http.StatusNotFound && r.path != "/api/v1/setup/session" && r.method+r.path != "POST/api/v1/status" {
			t.Errorf("admin host: %s %s = 404", r.method, r.path)
		}
	}
	// Status codes on the admin host: the UI is served, the API asks for a login.
	for path, want := range map[string]int{"/admin/": 200, "/admin/servers/x": 200, "/admin": 301, "/api/v1/status": 401, "/api/v1/catalog": 401} {
		res := e.do("GET", adminHost, path, nil)
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("admin host: GET %s = %d, want %d", path, res.StatusCode, want)
		}
	}
	// Paths that the mux routes to an admin handler once cleaned or unescaped.
	for _, p := range []string{
		"/api/v1/portal/../status",
		"/api//v1/status",
		"/api/v1/servers/a%2F..%2F..%2Fportal%2Fx/plan",
		"/api/v1/portal/x/../../servers/ma-commu",
		"/admin/../admin/",
	} {
		res := e.do("GET", portalHost, p, bearer)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("public host: GET %s = %d, want 404", p, res.StatusCode)
		}
	}
	// Shared routes answer on both hosts.
	shared := []struct {
		method, path string
		hdr          map[string]string
		want         int
	}{
		{"GET", "/", nil, 200},
		{"GET", "/some/portal/page", nil, 200},
		{"GET", "/api/v1/portal/servers", nil, 0},
		{"GET", "/api/v1/me", nil, 401},
		{"GET", "/auth/login", nil, 302},
		{"POST", "/auth/logout", map[string]string{"X-Cicord-Request": "1"}, 0},
		{"GET", "/healthz", nil, 200},
		{"GET", "/readyz", nil, 0},
		{"POST", "/api/v1/sync", bearer, 0},
		{"POST", "/hooks/github", nil, 0},
	}
	for _, host := range []string{portalHost, adminHost} {
		for _, r := range shared {
			res := e.do(r.method, host, r.path, r.hdr)
			res.Body.Close()
			if res.StatusCode == http.StatusNotFound || (r.want != 0 && res.StatusCode != r.want) {
				t.Errorf("%s: %s %s = %d, want %d", host, r.method, r.path, res.StatusCode, r.want)
			}
		}
	}
}

// Without an admin URL, every route answers on every host (no behavior change).
func TestNoAdminURLNoHostRestriction(t *testing.T) {
	e := newHostEnv(t, "")
	for _, host := range []string{portalHost, adminHost, "127.0.0.1:7420"} {
		for path, want := range map[string]int{"/admin/": 200, "/api/v1/status": 401, "/api/v1/catalog": 401, "/": 200} {
			res := e.do("GET", host, path, nil)
			res.Body.Close()
			if res.StatusCode != want {
				t.Errorf("%s: GET %s = %d, want %d", host, path, res.StatusCode, want)
			}
		}
		res := e.do("GET", host, "/auth/login", nil)
		res.Body.Close()
		if loc, _ := url.Parse(res.Header.Get("Location")); loc.Query().Get("redirect_uri") != portalOrigin+"/auth/callback" {
			t.Errorf("%s: redirect_uri %q", host, loc.Query().Get("redirect_uri"))
		}
	}
}

func TestSetupModeAdminHost(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sec, err := secrets.Open(t.Context(), store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	w := sim.NewWorld()
	senv, _ := setup.NewSimEnv(w)
	wz := setup.New(setup.Options{Mode: "serve", Store: store, Secrets: sec, Connector: w, Probe: senv.Probe(),
		PublicURL: portalOrigin, AdminURL: adminOrigin})
	tok := wz.IssueToken()
	ts := httptest.NewServer(NewSetup(Options{Store: store, Setup: wz, PublicURL: portalOrigin, AdminURL: adminOrigin,
		AdminUI: fstest.MapFS{"index.html": {Data: []byte("<html>admin</html>")}}}))
	defer ts.Close()
	e := &hostEnv{t: t, ts: ts}
	csrf := map[string]string{"X-Cicord-Request": "1"}

	// Public host: only the health endpoints; never the setup UI, API or token exchange.
	for _, r := range []struct{ method, path string }{
		{"GET", "/"}, {"GET", "/admin"}, {"GET", "/admin/setup"}, {"GET", "/api/v1/setup/status"},
		{"POST", "/api/v1/setup/session"}, {"GET", "/api/v1/me"}, {"GET", "/auth/login"}, {"GET", "/api/v1/portal/servers"},
	} {
		res := e.do(r.method, portalHost, r.path, csrf)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound || strings.Contains(string(body), "setup") {
			t.Errorf("public host (setup mode): %s %s = %d %s", r.method, r.path, res.StatusCode, body)
		}
	}
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 503} {
		res := e.do("GET", portalHost, path, nil)
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("public host (setup mode): GET %s = %d, want %d", path, res.StatusCode, want)
		}
	}

	// Admin host: the wizard works as usual.
	res := e.do("GET", adminHost, "/", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/admin/setup" {
		t.Fatalf("admin host: / = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res = e.do("GET", adminHost, "/admin/setup", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin host: /admin/setup = %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup/session", strings.NewReader(`{"token":"`+tok+`"}`))
	req.Host = adminHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cicord-Request", "1")
	req.Header.Set("Origin", adminOrigin)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || cookieNamed(res, setupCookie) == nil {
		t.Fatalf("admin host: setup session = %d", res.StatusCode)
	}
	res = e.do("GET", adminHost, "/api/v1/setup/status", nil, cookieNamed(res, setupCookie))
	var st struct {
		Authenticated bool `json:"authenticated"`
		View          struct {
			Login struct {
				RedirectURL  string   `json:"redirectUrl"`
				RedirectURLs []string `json:"redirectUrls"`
			} `json:"login"`
		} `json:"view"`
	}
	_ = json.NewDecoder(res.Body).Decode(&st)
	res.Body.Close()
	if !st.Authenticated || st.View.Login.RedirectURL != adminOrigin+"/auth/callback" ||
		strings.Join(st.View.Login.RedirectURLs, " ") != portalOrigin+"/auth/callback "+adminOrigin+"/auth/callback" {
		t.Fatalf("admin host: setup status = %+v", st)
	}
}

// Paths browsers would resolve to another host are not local redirects.
func TestIsLocalRedirect(t *testing.T) {
	for target, want := range map[string]bool{
		"/": true, "/admin/": true, "/portal?x=1#y": true,
		"": false, "//evil.example": false, "/\\evil.example": false, "https://evil.example/": false,
		"evil.example": false, "javascript:alert(1)": false, "/%2F%2Fevil": true,
	} {
		if got := isLocalRedirect(target); got != want {
			t.Errorf("isLocalRedirect(%q) = %v, want %v", target, got, want)
		}
	}
}
