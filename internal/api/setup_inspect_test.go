package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// TestSetupInspectFirst drives the wizard's "Inspect first" path over HTTP: the invite
// asks for View Channels, the inspection reports hidden channels, unreadable parts and
// what applying needs, with an empty plan, and the bot makes no mutating call.
func TestSetupInspectFirst(t *testing.T) {
	store, _ := sqlite.Open(":memory:")
	defer store.Close()
	sec, _ := secrets.Open(t.Context(), store, make([]byte, 32))
	w := sim.NewWorld()
	env, _ := setup.NewSimEnv(w)
	wz := setup.New(setup.Options{Mode: "dev", Store: store, Secrets: sec, Connector: w, Probe: env.Probe(), Sim: env})
	sess, err := wz.Exchange(t.Context(), wz.IssueToken())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewSetup(Options{Store: store, Setup: wz}))
	defer ts.Close()
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Cicord-Request", "1")
		// nosemgrep: go.lang.security.audit.net.cookie-missing-httponly.cookie-missing-httponly, go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- request cookie: only Name and Value are sent
		req.AddCookie(&http.Cookie{Name: setupCookie, Value: sess.ID})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out
	}
	ok := func(method, path, body string) map[string]any {
		t.Helper()
		code, out := call(method, path, body)
		if code >= 300 {
			t.Fatalf("%s %s = %d %v", method, path, code, out)
		}
		return out
	}
	ok("POST", "/api/v1/setup/intent", `{"intent":"existing"}`)
	tok := ok("POST", "/api/v1/setup/sim/bot", `{}`)["token"].(string)
	v := ok("POST", "/api/v1/setup/discord/token", `{"token":"`+tok+`"}`)
	if v["access"] != "admin" || !strings.Contains(v["inspectInviteUrl"].(string), "permissions=1024&") {
		t.Fatalf("view = %v", v)
	}
	if code, _ := call("POST", "/api/v1/setup/access", `{"access":"root"}`); code != 400 {
		t.Fatalf("bad access = %d", code)
	}
	if v = ok("POST", "/api/v1/setup/access", `{"access":"inspect"}`); v["access"] != "inspect" {
		t.Fatalf("access = %v", v["access"])
	}
	inv := ok("GET", "/api/v1/setup/discord/invite", "")
	if inv["permissions"] != "ViewChannel" || !strings.Contains(inv["url"].(string), "permissions=1024&") {
		t.Fatalf("invite = %v", inv)
	}
	ok("POST", "/api/v1/setup/sim/invite", `{}`)
	srv := env.Servers()[0]
	ok("POST", "/api/v1/setup/server", `{"serverId":"`+srv.ID+`"}`)
	botID := v["bot"].(map[string]any)["userId"].(string)
	w.ResetCalls()

	out := ok("POST", "/api/v1/setup/inspect", "")
	rep := out["report"].(map[string]any)
	if rep["administrator"] != false || rep["ready"] != false || rep["writes"] != float64(0) {
		t.Fatalf("report = %v", rep)
	}
	hidden := 0
	for _, c := range append(rep["categories"].([]any), rep["channels"].([]any)...) {
		if c.(map[string]any)["visible"] == false {
			hidden++
		}
	}
	if hidden != 2 || len(rep["unreadable"].([]any)) != 3 || len(rep["needs"].([]any)) == 0 {
		t.Fatalf("report = %v", rep)
	}
	if changes := out["plan"].(map[string]any)["changes"].([]any); len(changes) != 0 {
		t.Fatalf("plan = %v", out["plan"])
	}
	if calls := w.Calls(botID); len(calls) != 0 {
		t.Fatalf("the inspection changed Discord: %+v", calls)
	}
	// Continue stays closed until the permissions are upgraded.
	if code, _ := call("POST", "/api/v1/setup/step", `{"step":"repository"}`); code != 409 {
		t.Fatalf("repository before the upgrade = %d", code)
	}
	ok("POST", "/api/v1/setup/sim/grant-admin", "")
	if res := ok("POST", "/api/v1/setup/server/check", ""); res["ok"] != true {
		t.Fatalf("checks after the upgrade = %v", res["checks"])
	}
	ok("POST", "/api/v1/setup/step", `{"step":"repository"}`)
}
