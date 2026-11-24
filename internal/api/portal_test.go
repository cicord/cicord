package api

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/state/sqlite"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setPortalPath rewrites cicord.yaml spec.portal of the working copy.
func (e *env) setProjectPortal(t *testing.T, yaml string) {
	t.Helper()
	p := filepath.Join(e.dir, "cicord.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if i := strings.Index(s, "\n  portal:"); i >= 0 {
		s = s[:i+1]
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	writeTestFile(t, p, s+yaml)
}

func TestPortalServerNoCodeFields(t *testing.T) {
	e := start(t)
	c := e.client()
	var p portalServerDTO
	c.expect(200, "GET", "/api/v1/portal/ma-commu", nil).json(t, &p)
	if p.Title != "Bienvenue sur Ma Commu" || !strings.Contains(p.Tagline, "francophone") || !strings.Contains(p.Description, "## Ce que tu trouveras ici") {
		t.Errorf("texts = %q / %q / %q", p.Title, p.Tagline, p.Description)
	}
	if p.Locale != "fr" || !p.Pages.Onboarding || !p.Pages.Profile || !p.Pages.Leaderboard || !p.Pages.Tickets {
		t.Errorf("locale/pages = %q %+v", p.Locale, p.Pages)
	}
	if len(p.Links) != 4 || p.Links[0] != (portalLinkDTO{Label: "Site web", URL: "https://ma-commu.example", Icon: "website"}) {
		t.Errorf("links = %+v", p.Links)
	}
	if !strings.HasPrefix(p.Theme.Logo, "/api/v1/portal/ma-commu/logo?v=") || !strings.HasPrefix(p.Banner, "/api/v1/portal/ma-commu/banner?v=") {
		t.Fatalf("image URLs = %q %q", p.Theme.Logo, p.Banner)
	}

	// The image endpoints serve the real repository files.
	for _, tc := range []struct{ url, file, ct string }{
		{p.Theme.Logo, "assets/logo.svg", "image/svg+xml"},
		{p.Banner, "assets/banner.jpg", "image/jpeg"},
	} {
		r := c.expect(200, "GET", tc.url, nil)
		want, _ := os.ReadFile(filepath.Join("../../examples/basic", tc.file))
		if string(r.body) != string(want) || r.header.Get("Content-Type") != tc.ct {
			t.Errorf("%s: %d bytes, %s", tc.url, len(r.body), r.header.Get("Content-Type"))
		}
		if !strings.Contains(r.header.Get("Content-Security-Policy"), "sandbox") || r.header.Get("Cache-Control") != "public, max-age=300" {
			t.Errorf("%s headers = %v", tc.url, r.header)
		}
	}

	// Pages, defaults and absent images.
	srvFile := filepath.Join(e.dir, "servers", "ma-commu.yaml")
	data, _ := os.ReadFile(srvFile)
	s := strings.Replace(string(data), "    banner: ../assets/banner.jpg\n", "", 1)
	s = strings.Replace(s, "      profile: true\n", "      profile: false\n", 1)
	s = strings.Replace(s, "    title: Bienvenue sur Ma Commu\n", "", 1)
	writeTestFile(t, srvFile, s)
	e.waitFor("profile page disabled", func() bool {
		srv, _ := e.rt.ServerConfig(serverKey)
		return srv.Spec.Portal.Pages != nil && srv.Spec.Portal.Pages.Profile != nil && !*srv.Spec.Portal.Pages.Profile
	})
	c.expect(200, "GET", "/api/v1/portal/ma-commu", nil).json(t, &p)
	if p.Pages.Profile || !p.Pages.Onboarding || p.Banner != "" || p.Title != p.Name {
		t.Errorf("after edit: pages %+v banner %q title %q name %q", p.Pages, p.Banner, p.Title, p.Name)
	}
	c.expect(404, "GET", "/api/v1/portal/ma-commu/banner", nil)
}

func TestPortalImageRejectsEscapes(t *testing.T) {
	e := start(t)
	c := e.client()
	outside := filepath.Join(t.TempDir(), "secret.png")
	writeTestFile(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(e.dir, "assets", "evil.png")); err != nil {
		t.Fatal(err)
	}
	srvFile := filepath.Join(e.dir, "servers", "ma-commu.yaml")
	data, _ := os.ReadFile(srvFile)
	writeTestFile(t, srvFile, strings.Replace(string(data), "../assets/logo.svg", "../assets/evil.png", 1))
	e.waitFor("logo changed", func() bool {
		srv, _ := e.rt.ServerConfig(serverKey)
		return srv.Spec.Portal.Theme.Logo == "../assets/evil.png"
	})
	if r := c.do("GET", "/api/v1/portal/ma-commu/logo", nil); r.status != 404 || strings.Contains(string(r.body), "secret") {
		t.Fatalf("symlinked logo = %d %s", r.status, r.body)
	}
}

func TestCustomPortalFromRevision(t *testing.T) {
	e := start(t)
	c := e.client()
	if r := c.expect(200, "GET", "/", nil); string(r.body) != "<html>portal</html>" {
		t.Fatalf("default portal = %s", r.body)
	}

	// A path that is not built yet keeps the default portal.
	e.setProjectPortal(t, "  portal:\n    path: ./portal/dist\n")
	e.waitFor("portal path", func() bool {
		repo := e.rt.Repo()
		return repo.Project.Spec.Portal != nil && repo.Project.Spec.Portal.Path == "./portal/dist"
	})
	if r := c.expect(200, "GET", "/", nil); string(r.body) != "<html>portal</html>" {
		t.Fatalf("unbuilt custom portal = %s", r.body)
	}

	dist := filepath.Join(e.dir, "portal", "dist")
	writeTestFile(t, filepath.Join(dist, "index.html"), "<html>custom v1</html>")
	writeTestFile(t, filepath.Join(dist, "assets", "index-abc123.js"), "console.log('custom')")
	writeTestFile(t, filepath.Join(dist, "favicon.svg"), "<svg/>")
	e.waitFor("custom portal served", func() bool { return string(c.do("GET", "/", nil).body) == "<html>custom v1</html>" })

	r := c.expect(200, "GET", "/onboarding?server=ma-commu", nil)
	if string(r.body) != "<html>custom v1</html>" || r.header.Get("Cache-Control") != "no-cache" || r.header.Get("X-Cicord-Portal") != "repository" {
		t.Errorf("SPA fallback = %s %v", r.body, r.header)
	}
	r = c.expect(200, "GET", "/assets/index-abc123.js", nil)
	if r.header.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.Contains(r.header.Get("Content-Type"), "javascript") {
		t.Errorf("asset headers = %v", r.header)
	}
	if r := c.expect(200, "GET", "/favicon.svg", nil); r.header.Get("Content-Type") != "image/svg+xml" || r.header.Get("Cache-Control") != "no-cache" {
		t.Errorf("favicon headers = %v", r.header)
	}
	c.expect(404, "GET", "/assets/missing.js", nil)
	for _, p := range []string{"/api/nope", "/auth/nope", "/hooks/nope", "/admin/x"} {
		if r := c.do("GET", p, nil); strings.Contains(string(r.body), "custom") {
			t.Errorf("%s shadowed by the portal: %s", p, r.body)
		}
	}
	if r := c.do("POST", "/healthz", nil); r.status != 404 && r.status != 405 || strings.Contains(string(r.body), "custom") {
		t.Errorf("POST /healthz = %d %s", r.status, r.body)
	}
	if r := c.do("POST", "/", nil); r.status != 405 {
		t.Errorf("POST / = %d", r.status)
	}
	if r := c.expect(200, "GET", "/api/v1/portal/ma-commu", nil); !strings.Contains(string(r.body), `"key":"ma-commu"`) {
		t.Errorf("API shadowed: %s", r.body)
	}

	// A new revision swaps the files.
	writeTestFile(t, filepath.Join(dist, "index.html"), "<html>custom v2</html>")
	e.waitFor("hot swap", func() bool { return string(c.do("GET", "/me", nil).body) == "<html>custom v2</html>" })

	// Removing index.html falls back to the default portal.
	if err := os.Remove(filepath.Join(dist, "index.html")); err != nil {
		t.Fatal(err)
	}
	e.waitFor("fallback", func() bool { return string(c.do("GET", "/", nil).body) == "<html>portal</html>" })
}

func TestOpenPortalDirConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "index.html"), "outside")
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "secret")
	writeTestFile(t, filepath.Join(root, "portal", "dist", "index.html"), "inside")
	writeTestFile(t, filepath.Join(root, "notes.txt"), "repo file")

	if d := openPortalDir(root, "./portal/dist"); d == nil {
		t.Fatal("valid portal dir refused")
	} else {
		d.Close()
	}
	for _, p := range []string{"../outside", "./", "./portal/../../x", "/etc"} {
		if d := openPortalDir(root, p); d != nil {
			d.Close()
			t.Errorf("%q accepted", p)
		}
	}
	// The portal directory itself behind a symlink.
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if d := openPortalDir(root, "./linked"); d != nil {
		d.Close()
		t.Error("symlinked portal dir accepted")
	}
	// Symlinks inside the portal leading out of it, and traversal in the URL.
	dist := filepath.Join(root, "portal", "dist")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dist, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dist, "assets")); err != nil {
		t.Fatal(err)
	}
	d := openPortalDir(root, "./portal/dist")
	if d == nil {
		t.Fatal("portal dir refused")
	}
	defer d.Close()
	for _, p := range []string{"/leak.txt", "/assets/secret.txt", "/../notes.txt", "/../../notes.txt", "/%2e%2e/notes.txt"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path = p
		if u, err := url.PathUnescape(p); err == nil {
			req.URL.Path = u
		}
		w := httptest.NewRecorder()
		serveSPA(w, req, d.FS(), "/")
		if body := w.Body.String(); strings.Contains(body, "secret") || strings.Contains(body, "repo file") {
			t.Errorf("%s leaked: %d %s", p, w.Code, body)
		}
	}
}

func TestTokenModeSessions(t *testing.T) {
	e := start(t)
	const fan = "https://fan.example"
	e.setProjectPortal(t, "  portal:\n    allowedOrigins: [\""+fan+"\"]\n")
	e.waitFor("allowed origins", func() bool {
		repo := e.rt.Repo()
		return repo.Project.Spec.Portal != nil && len(repo.Project.Spec.Portal.AllowedOrigins) == 1
	})
	c := e.client()
	c.noCSRF = true // token-mode requests carry Authorization instead

	// Preflight from the embedding origin: allowed, without credentials.
	r := c.do("OPTIONS", "/auth/dev-login", nil, "Origin", fan, "Access-Control-Request-Method", "POST")
	if r.status != 204 || r.header.Get("Access-Control-Allow-Origin") != fan || r.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("preflight = %d %v", r.status, r.header)
	}
	if r := c.do("OPTIONS", "/auth/dev-login", nil, "Origin", "https://evil.example", "Access-Control-Request-Method", "POST"); r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("evil preflight = %v", r.header)
	}

	// Dev login in token mode from the embedding origin: no cookie, a token.
	r = c.do("POST", "/auth/dev-login", map[string]string{"userId": e.seed.Owner.ID, "mode": "token"}, "Origin", fan, "X-Cicord-Request", "1")
	if r.status != 200 || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("token dev-login = %d %v %s", r.status, r.header, r.body)
	}
	var ts tokenSessionDTO
	r.json(t, &ts)
	if !strings.HasPrefix(ts.Token, tokenPrefix) || ts.Me.User.ID != e.seed.Owner.ID || ts.ExpiresAt.IsZero() {
		t.Fatalf("token session = %+v", ts)
	}

	tc := e.client()
	tc.noCSRF, tc.bearer = true, ts.Token
	// Portal endpoints: full role (the owner is admin of the server).
	var pm struct{ Role string }
	tc.expect(200, "GET", "/api/v1/portal/ma-commu/me", nil).json(t, &pm)
	if pm.Role != "admin" {
		t.Errorf("portal role = %q", pm.Role)
	}
	// Admin endpoints: at most member.
	if r := tc.do("GET", "/api/v1/servers/ma-commu", nil); r.status != 403 {
		t.Errorf("admin endpoint with a token session = %d %s", r.status, r.body)
	}
	var me meDTO
	tc.expect(200, "GET", "/api/v1/me", nil).json(t, &me)
	if len(me.Servers) != 1 || me.Servers[0].Role != "member" {
		t.Errorf("/me servers = %+v", me.Servers)
	}
	// Unsafe request from the embedding origin with the token, no CSRF header needed.
	if r := tc.do("POST", "/api/v1/servers/ma-commu/sync", nil, "Origin", fan); r.status != 403 || r.errCode() != "forbidden" {
		t.Errorf("sync with token = %d %s", r.status, r.body)
	}
	// From a non-allowed origin: refused before auth.
	if r := tc.do("POST", "/auth/logout", nil, "Origin", "https://evil.example"); r.status != 403 {
		t.Errorf("logout from evil origin = %d", r.status)
	}

	// The token is never accepted as a cookie.
	u, _ := url.Parse(e.ts.URL)
	cc := e.client()
	cc.hc.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: ts.Token}})
	if r := cc.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Errorf("token as cookie = %d", r.status)
	}
	// Garbage tokens are 401.
	bad := e.client()
	bad.bearer = tokenPrefix + "nope"
	if r := bad.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Errorf("bad token = %d", r.status)
	}

	// Logout revokes the token.
	tc.expect(204, "POST", "/auth/logout", nil)
	if r := tc.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Errorf("after logout = %d", r.status)
	}
	if r := c.do("POST", "/auth/dev-login", map[string]string{"userId": e.seed.Owner.ID, "mode": "weird"}, "X-Cicord-Request", "1"); r.status != 400 {
		t.Errorf("bad mode = %d", r.status)
	}
}

func TestOAuthTokenMode(t *testing.T) {
	discordAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
		case "/users/@me":
			_, _ = w.Write([]byte(`{"id":"777","username":"zed","global_name":"Zed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer discordAPI.Close()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const fan = "https://fan.example"
	h := New(Options{Store: store, Logger: testLogger(), CORSOrigins: []string{fan}, OAuth: OAuth{ClientID: "42", ClientSecret: "shh",
		AuthorizeURL: discordAPI.URL + "/oauth2/authorize", TokenURL: discordAPI.URL + "/oauth2/token", APIURL: discordAPI.URL}})
	ts := httptest.NewServer(h)
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(u string) *http.Response {
		t.Helper()
		res, err := hc.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}

	// Token mode refuses redirects to other origins instead of silently using "/".
	if res := get(ts.URL + "/auth/login?mode=token&redirect=" + url.QueryEscape("https://evil.example/cb")); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("evil redirect = %d", res.StatusCode)
	}
	res := get(ts.URL + "/auth/login?mode=token&redirect=" + url.QueryEscape(fan+"/cb?x=1"))
	loc, _ := url.Parse(res.Header.Get("Location"))
	res = get(ts.URL + "/auth/callback?code=good&state=" + url.QueryEscape(loc.Query().Get("state")))
	back, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || back.Host != "fan.example" || back.Path != "/cb" || back.Query().Get("x") != "1" || back.Query().Get("cicord_code") == "" {
		t.Fatalf("callback = %d %s", res.StatusCode, back)
	}
	if len(res.Header.Values("Set-Cookie")) > 1 { // only the cleared OAuth state cookie
		t.Errorf("token mode set cookies: %v", res.Header.Values("Set-Cookie"))
	}
	code := back.Query().Get("cicord_code")
	redeem := func(origin string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/auth/token", strings.NewReader(`{"code":"`+code+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Cicord-Request", "1")
		req.Header.Set("Origin", origin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	// Redeemed from the wrong (but allowed) origin: refused and burnt.
	if st, body := redeem(ts.URL); st != 403 {
		t.Fatalf("wrong-origin redeem = %d %s", st, body)
	}
	// Restart the flow and redeem correctly.
	res = get(ts.URL + "/auth/login?mode=token&redirect=" + url.QueryEscape(fan+"/cb"))
	loc, _ = url.Parse(res.Header.Get("Location"))
	res = get(ts.URL + "/auth/callback?code=good&state=" + url.QueryEscape(loc.Query().Get("state")))
	back, _ = url.Parse(res.Header.Get("Location"))
	code = back.Query().Get("cicord_code")
	st, body := redeem(fan)
	if st != 200 || !strings.Contains(body, `"token":"`+tokenPrefix) || !strings.Contains(body, `"id":"777"`) {
		t.Fatalf("redeem = %d %s", st, body)
	}
	if st, _ := redeem(fan); st != 401 {
		t.Fatalf("replayed code = %d", st)
	}
}
