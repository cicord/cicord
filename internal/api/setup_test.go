package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/npjonath/codi/internal/secrets"
	"github.com/npjonath/codi/internal/setup"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/state/sqlite"
)

func TestSetupModeHandler(t *testing.T) {
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
	env, _ := setup.NewSimEnv(w)
	wz := setup.New(setup.Options{Mode: "serve", Store: store, Secrets: sec, Connector: w, Probe: env.Probe(), PublicURL: "https://codi.example.org"})
	tok := wz.IssueToken()
	ts := httptest.NewServer(NewSetup(Options{Store: store, Setup: wz, PublicURL: "https://codi.example.org"}))
	defer ts.Close()

	post := func(path, body string, hdr map[string]string) *http.Response {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// CSRF: the exchange needs X-Codi-Request like every unsafe request.
	if res := post("/api/v1/setup/session", `{"token":"`+tok+`"}`, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("exchange without X-Codi-Request = %d", res.StatusCode)
	}
	res := post("/api/v1/setup/session", `{"token":"`+tok+`"}`, map[string]string{"X-Codi-Request": "1"})
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || strings.Contains(string(body), tok) {
		t.Fatalf("exchange = %d %s", res.StatusCode, body)
	}
	var c *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == setupCookie {
			c = ck
		}
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || !c.Secure || c.Path != "/" {
		t.Fatalf("setup cookie = %+v", c)
	}
	// Everything but the wizard answers setup_required.
	for _, p := range []string{"/api/v1/status", "/api/v1/me", "/auth/login"} {
		r, _ := http.Get(ts.URL + p)
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 503 || !strings.Contains(string(b), "setup_required") {
			t.Fatalf("%s = %d %s", p, r.StatusCode, b)
		}
	}
	// A setup session ID is not accepted as a Discord session cookie.
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/setup/status", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.Value})
	r, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(r.Body)
	if strings.Contains(string(b), `"authenticated":true`) {
		t.Fatalf("setup session accepted under codi_session: %s", b)
	}
}

func TestSetupOAuthTestSeesDiscordSession(t *testing.T) {
	store, _ := sqlite.Open(":memory:")
	defer store.Close()
	sec, _ := secrets.Open(t.Context(), store, make([]byte, 32))
	w := sim.NewWorld()
	env, _ := setup.NewSimEnv(w)
	wz := setup.New(setup.Options{Mode: "serve", Store: store, Secrets: sec, Connector: w, Probe: env.Probe()})
	tok := wz.IssueToken()
	sess, err := wz.Exchange(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewSetup(Options{Store: store, Setup: wz}))
	defer ts.Close()
	get := func(cookies ...*http.Cookie) string {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/setup/oauth/test", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	setupC := &http.Cookie{Name: setupCookie, Value: sess.ID}
	if b := get(setupC); !strings.Contains(b, `"ok":false`) {
		t.Fatalf("no Discord login yet: %s", b)
	}
	_ = store.Sessions().Put(t.Context(), state.Session{ID: "discord-sess", UserID: "42",
		Data: []byte(`{"username":"jo","method":"oauth"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if b := get(setupC, &http.Cookie{Name: sessionCookie, Value: "discord-sess"}); !strings.Contains(b, `"ok":true`) || !strings.Contains(b, `"username":"jo"`) {
		t.Fatalf("after login: %s", b)
	}
}
