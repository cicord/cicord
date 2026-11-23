package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// setupClient drives the setup wizard through the HTTP API like the admin UI does.
type setupClient struct {
	t      *testing.T
	base   string
	hc     *http.Client
	bodies []string // every response body (leak checks)
}

func (c *setupClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-Codi-Request", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	c.bodies = append(c.bodies, string(raw))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return res.StatusCode, out
}

func (c *setupClient) ok(method, path string, body any) map[string]any {
	c.t.Helper()
	code, out := c.do(method, path, body)
	if code >= 300 {
		c.t.Fatalf("%s %s = %d %v", method, path, code, out)
	}
	return out
}

func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func checkStatus(t *testing.T, checks any, id string) (string, string) {
	t.Helper()
	for _, c := range checks.([]any) {
		cm := c.(map[string]any)
		if cm["id"] == id {
			return cm["status"].(string), cm["message"].(string) + " " + str(cm["hint"])
		}
	}
	t.Fatalf("check %s not found in %v", id, checks)
	return "", ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// TestSetupWizardE2ESim runs the whole wizard over HTTP against `codi dev --sim --setup`:
// setup token → simulated bot → intents → invite + hierarchy fix → local repository →
// import (empty plan) → activation without restart → verify → first plugin → finish.
func TestSetupWizardE2ESim(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "community")
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- ExecuteContext(ctx, []string{"dev", "--sim", "--setup", "--listen", "127.0.0.1:0", "--log-level", "warn", dir}, &stdout, &stderr)
	}()
	defer func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("exit code %d; stderr:\n%s", code, stderr.String())
			}
		case <-time.After(30 * time.Second):
			t.Error("codi dev did not stop")
		}
	}()
	re := regexp.MustCompile(`open the setup wizard: (http://127\.0\.0\.1:\d+)/admin/setup\?token=(\S+)`)
	var base, setupToken string
	deadline := time.Now().Add(30 * time.Second)
	for base == "" {
		if m := re.FindStringSubmatch(stdout.String()); m != nil {
			base, setupToken = m[1], m[2]
			break
		}
		select {
		case code := <-done:
			t.Fatalf("codi dev exited with %d:\n%s\n%s", code, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no setup URL printed:\n%s\n%s", stdout.String(), stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	jar, _ := cookiejar.New(nil)
	c := &setupClient{t: t, base: base, hc: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

	// Setup mode: only the wizard, the admin UI and health.
	if code, out := c.do("GET", "/api/v1/me", nil); code != 503 || get(out, "error", "code") != "setup_required" {
		t.Fatalf("me in setup mode = %d %v", code, out)
	}
	if code, _ := c.do("GET", "/healthz", nil); code != 200 {
		t.Fatalf("healthz = %d", code)
	}
	if code, _ := c.do("GET", "/", nil); code != http.StatusFound {
		t.Fatalf("/ = %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/setup/intent", map[string]any{"intent": "existing"}); code != 401 {
		t.Fatalf("unauthenticated intent = %d", code)
	}
	if st := c.ok("GET", "/api/v1/setup/status", nil); st["authenticated"] != false || st["view"] != nil {
		t.Fatalf("anonymous status = %v", st)
	}
	if code, _ := c.do("POST", "/api/v1/setup/session", map[string]any{"token": "wrong"}); code != 401 {
		t.Fatalf("wrong token = %d", code)
	}
	c.ok("POST", "/api/v1/setup/session", map[string]any{"token": setupToken})
	other := &setupClient{t: t, base: base, hc: &http.Client{}}
	if code, _ := other.do("POST", "/api/v1/setup/session", map[string]any{"token": setupToken}); code != 401 {
		t.Fatalf("setup token reused = %d", code)
	}

	// Welcome → Discord application.
	v := c.ok("POST", "/api/v1/setup/intent", map[string]any{"intent": "existing"})
	if v["step"] != "discord" || v["simulator"] != true {
		t.Fatalf("after intent: %v", v)
	}
	botTok := str(c.ok("POST", "/api/v1/setup/sim/bot", map[string]any{"name": "CODI"})["token"])
	if code, out := c.do("POST", "/api/v1/setup/discord/token", map[string]any{"token": "sim.not-a-real-token-at-all"}); code != 422 || get(out, "error", "code") != "invalid_token" || str(get(out, "error", "hint")) == "" {
		t.Fatalf("bad token = %d %v", code, out)
	}
	leakFrom := len(c.bodies)
	v = c.ok("POST", "/api/v1/setup/discord/token", map[string]any{"token": "  Bot " + botTok + "\n"})
	if get(v, "bot", "key") != "codi" || get(v, "bot", "secretRef") != "DISCORD_TOKEN_CODI" || get(v, "bot", "tokenStored") != true {
		t.Fatalf("bot = %v", v["bot"])
	}
	if get(v, "bot", "intents", "status") != "warn" || !strings.Contains(str(get(v, "bot", "intents", "hint")), "Server Members Intent") {
		t.Fatalf("intents before = %v", get(v, "bot", "intents"))
	}
	c.ok("POST", "/api/v1/setup/sim/intents", map[string]any{"enabled": true})
	v = c.ok("POST", "/api/v1/setup/discord/recheck", nil)
	if get(v, "bot", "intents", "status") != "ok" {
		t.Fatalf("intents after = %v", get(v, "bot", "intents"))
	}
	if !strings.Contains(str(v["inviteUrl"]), "permissions=8") || !strings.Contains(str(v["inviteUrl"]), "applications.commands") {
		t.Fatalf("invite = %v", v["inviteUrl"])
	}

	// Server: invite (bot role placed low), live detection, hierarchy guidance.
	if ss := c.ok("GET", "/api/v1/setup/discord/servers", nil)["servers"].([]any); len(ss) != 0 {
		t.Fatalf("servers before invite = %v", ss)
	}
	c.ok("POST", "/api/v1/setup/sim/invite", map[string]any{"low": true})
	ss := c.ok("GET", "/api/v1/setup/discord/servers", nil)["servers"].([]any)
	if len(ss) != 1 {
		t.Fatalf("servers after invite = %v", ss)
	}
	sid := ss[0].(map[string]any)["id"].(string)
	res := c.ok("POST", "/api/v1/setup/server", map[string]any{"serverId": sid})
	if st, msg := checkStatus(t, res["checks"], "hierarchy"); st != "fail" || !strings.Contains(msg, "drag the role “CODI” above “Admin”") {
		t.Fatalf("hierarchy = %s %s", st, msg)
	}
	if code, _ := c.do("POST", "/api/v1/setup/step", map[string]any{"step": "repository"}); code != 409 {
		t.Fatalf("repository with a bad hierarchy = %d", code)
	}
	c.ok("POST", "/api/v1/setup/sim/raise-role", nil)
	res = c.ok("POST", "/api/v1/setup/server/check", nil)
	if res["ok"] != true {
		t.Fatalf("server checks = %v", res["checks"])
	}
	c.ok("POST", "/api/v1/setup/step", map[string]any{"step": "repository"})

	// Repository (local files) → import → activation without restart.
	if code, _ := c.do("POST", "/api/v1/setup/repo", map[string]any{"provider": "github", "url": "https://github.com/x/y"}); code != 400 {
		t.Fatalf("github in dev = %d", code)
	}
	c.ok("POST", "/api/v1/setup/repo", map[string]any{"provider": "local"})
	imp := c.ok("POST", "/api/v1/setup/import", map[string]any{})
	if get(imp, "view", "activated") != true || get(imp, "view", "step") != "repository" || imp["needsConfirm"] != false {
		t.Fatalf("import = %v", imp)
	}
	if code, _ := c.do("POST", "/api/v1/setup/step", map[string]any{"step": "server"}); code != 409 {
		t.Fatalf("back to server after activation = %d", code)
	}
	c.ok("POST", "/api/v1/setup/step", map[string]any{"step": "login"})
	if n := len(get(imp, "plan", "changes").([]any)); n != 0 {
		t.Fatalf("first plan has %d change(s): %v", n, imp["plan"])
	}
	for _, f := range []string{"codi.yaml", "servers/pixel-pals.yaml", "bots/codi/bot.yaml",
		"servers/assets/pixel-pals/icon.png", "servers/assets/pixel-pals/emojis/pixel.png"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s not written: %v", f, err)
		}
	}
	// The full API is now served, the setup session is an admin principal.
	me := c.ok("GET", "/api/v1/me", nil)
	if srv := me["servers"].([]any); len(srv) != 1 || srv[0].(map[string]any)["role"] != "admin" {
		t.Fatalf("me after activation = %v", me)
	}
	if st := c.ok("GET", "/api/v1/status", nil); len(st["servers"].([]any)) != 1 {
		t.Fatalf("status = %v", st)
	}

	// Admin login: skipped in the simulator (dev login).
	if v := c.ok("POST", "/api/v1/setup/oauth/skip", nil); v["step"] != "verify" {
		t.Fatalf("after skip = %v", v["step"])
	}

	// Verify: everything ok except the gateway event until a message is sent.
	rep := c.ok("GET", "/api/v1/setup/verify", nil)
	if st, _ := checkStatus(t, rep["checks"], "events"); st != "pending" || rep["ok"] != false {
		t.Fatalf("verify before message = %v", rep)
	}
	c.ok("POST", "/api/v1/setup/sim/message", nil)
	deadline = time.Now().Add(10 * time.Second)
	for {
		rep = c.ok("GET", "/api/v1/setup/verify", nil)
		if rep["ok"] == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("verify never ok: %v", rep["checks"])
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, id := range []string{"token", "intents", "member", "permissions", "hierarchy", "commands", "events", "plan", "writeback"} {
		if st, msg := checkStatus(t, rep["checks"], id); st != "ok" {
			t.Fatalf("check %s = %s %s", id, st, msg)
		}
	}
	// Same checks from the admin Diagnostics endpoint.
	if d := c.ok("GET", "/api/v1/servers/pixel-pals/diagnostics", nil); len(d["checks"].([]any)) != 9 {
		t.Fatalf("diagnostics = %v", d)
	}

	// First plugin: the catalog, the intents gate, then install.
	cat := c.ok("GET", "/api/v1/catalog", nil)["plugins"].([]any)
	if len(cat) != 6 {
		t.Fatalf("catalog = %d plugins", len(cat))
	}
	c.ok("POST", "/api/v1/setup/step", map[string]any{"step": "plugin"})
	c.ok("POST", "/api/v1/setup/sim/intents", map[string]any{"enabled": false})
	if code, out := c.do("POST", "/api/v1/setup/plugins/install", map[string]any{"plugin": "welcome", "config": map[string]any{"channel": "general"}}); code != 409 || get(out, "error", "code") != "intents" {
		t.Fatalf("install without intents = %d %v", code, out)
	}
	c.ok("POST", "/api/v1/setup/sim/intents", map[string]any{"enabled": true})
	if code, out := c.do("POST", "/api/v1/setup/plugins/install", map[string]any{"plugin": "welcome", "config": map[string]any{"channel": "nope"}}); code != 422 || len(get(out, "error", "diagnostics").([]any)) == 0 {
		t.Fatalf("install with a bad channel = %d %v", code, out)
	}
	inst := c.ok("POST", "/api/v1/setup/plugins/install", map[string]any{"plugin": "polls", "config": map[string]any{}})
	if get(inst, "result", "health") != "Healthy" || get(inst, "result", "command") == "" || get(inst, "view", "plugin", "instance") != "polls" {
		t.Fatalf("install = %v", inst)
	}
	botYAML, _ := os.ReadFile(filepath.Join(dir, "bots/codi/bot.yaml"))
	if !strings.Contains(string(botYAML), "source: ../../plugins/polls") || !strings.Contains(string(botYAML), "- scheduler") {
		t.Fatalf("bot.yaml = %s", botYAML)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins/polls/plugin.yaml")); err != nil {
		t.Fatal(err)
	}
	if code, _ := c.do("POST", "/api/v1/setup/plugins/install", map[string]any{"plugin": "polls"}); code != 409 {
		t.Fatalf("second install = %d", code)
	}

	// Finish: setup endpoints are gone, the setup session is revoked.
	if v := c.ok("POST", "/api/v1/setup/finish", nil); v["completed"] != true {
		t.Fatalf("finish = %v", v)
	}
	if code, _ := c.do("POST", "/api/v1/setup/step", map[string]any{"step": "verify"}); code != 410 {
		t.Fatalf("after finish = %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/me", nil); code != 401 {
		t.Fatalf("setup session after finish = %d", code)
	}

	// Secrets never leak: neither the bot token (after it was submitted) nor the setup
	// token appear in any response body or in the logs.
	for i, b := range c.bodies {
		if i >= leakFrom && strings.Contains(b, botTok) {
			t.Fatalf("bot token leaked in response %d: %s", i, b)
		}
		if strings.Contains(b, setupToken) {
			t.Fatalf("setup token leaked in response %d", i)
		}
	}
	if strings.Contains(stderr.String(), botTok) || strings.Contains(stderr.String(), setupToken) {
		t.Fatal("secret in logs")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "bots/codi/bot.yaml"))
	if strings.Contains(string(data), botTok) {
		t.Fatal("token written to the repository")
	}
}
