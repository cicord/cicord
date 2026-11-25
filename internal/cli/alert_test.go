package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/cicord/cicord/internal/alert"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// alertReceiver is a JSON webhook that records the alerts it receives.
type alertReceiver struct {
	*httptest.Server
	mu     sync.Mutex
	alerts []alert.Message
	bodies []string
}

func newAlertReceiver(t *testing.T) *alertReceiver {
	t.Helper()
	r := &alertReceiver{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var p struct{ Alerts []alert.Message }
		_ = json.Unmarshal(b, &p)
		r.mu.Lock()
		r.alerts = append(r.alerts, p.Alerts...)
		r.bodies = append(r.bodies, string(b))
		r.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *alertReceiver) got() []alert.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]alert.Message(nil), r.alerts...)
}

func TestAlertTestAndSend(t *testing.T) {
	hook := newAlertReceiver(t)
	data := t.TempDir()
	t.Setenv("CICORD_ALERT_WEBHOOK", "")
	t.Setenv("CICORD_SECRET_KEY", "")

	// No webhook anywhere: exit 2.
	if code, _, stderr := run(t, "alert", "test", "--data-dir", data); code != 2 || !strings.Contains(stderr, "no alert webhook") {
		t.Fatalf("alert test without a webhook = %d %s", code, stderr)
	}

	// The webhook in the secret store of the data dir.
	st, err := sqlite.Open(filepath.Join(data, stateDBFile))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	withStdinCmd(t, hook.URL+"/alerts\n", "secret", "set", alertWebhookSecret, "--data-dir", data)
	code, stdout, stderr := run(t, "alert", "test", "--data-dir", data, "--public-url", "https://community.example.org", "--admin-url", "https://cicord.tailnet.ts.net")
	if code != 0 || !strings.Contains(stdout, "Alert sent to the JSON webhook on 127.0.0.1") || strings.Contains(stdout+stderr, "/alerts") {
		t.Fatalf("alert test = %d %q %q", code, stdout, stderr)
	}
	got := hook.got()
	if len(got) != 1 || got[0].State != alert.StateTest || got[0].Runtime != "community.example.org" || got[0].Link != "https://cicord.tailnet.ts.net/admin/" {
		t.Fatalf("test alert = %+v", got)
	}
	if testing.Verbose() {
		t.Logf("test alert payload: %s", hook.bodies[0])
	}

	// cicord alert send (scripts), with the environment taking precedence.
	t.Setenv("CICORD_ALERT_WEBHOOK", hook.URL+"/env")
	code, _, stderr = run(t, "alert", "send", "--data-dir", data, "--public-url", "https://c.example", "--severity", "warning",
		"--title", "Backup failed", "--detail", "cicord.db is missing", "--workflow", "backup")
	if code != 0 {
		t.Fatalf("alert send = %d %s", code, stderr)
	}
	got = hook.got()
	if len(got) != 2 || got[1].Title != "Backup failed" || got[1].Severity != "warning" || got[1].Key != "script/backup" || got[1].Workflow != "backup" {
		t.Fatalf("sent = %+v", got)
	}
	if code, _, _ := run(t, "alert", "send", "--title", "x", "--severity", "loud"); code == 0 {
		t.Error("an invalid severity must be refused")
	}

	// An unreachable webhook: exit 1, and the URL (its token) is not printed.
	t.Setenv("CICORD_ALERT_WEBHOOK", "http://127.0.0.1:1/api/webhooks/1/SECRET")
	if code, stdout, stderr := run(t, "alert", "test"); code != 1 || strings.Contains(stdout+stderr, "SECRET") {
		t.Fatalf("unreachable webhook = %d %q %q", code, stdout, stderr)
	}
}

func withStdinCmd(t *testing.T, stdin string, args ...string) {
	t.Helper()
	var o, e syncBuffer
	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&o)
	cmd.SetErr(&e)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("%v: %v %s", args, err, e.String())
	}
}

// The service check runs `cicord status --wait` against a running runtime: 0 when it serves
// every server, 1 when a bot is missing, 2 without a runtime token. It reads the token from
// the secret store and passes the admin host check of --admin-url.
func TestHealthcheckService(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	t.Setenv("CICORD_RUNTIME_TOKEN", "rt-dev-token")
	p := startCicord(t, "dev", "--sim", "--listen", "127.0.0.1:0", "--data-dir", t.TempDir(), "--log-level", "warn", dir)
	addr := p.wait(t, regexp.MustCompile(`listening on (http://\S+)`))[1]
	defer p.stop(t)
	code, stdout, stderr := run(t, "healthcheck", "--service", "--url", addr+"/healthz", "--timeout", "60s")
	if code != 0 || !strings.Contains(stdout, "Synced") {
		t.Fatalf("service check on a healthy runtime = %d\n%s\n%s", code, stdout, stderr)
	}

	// serve: no Discord connection (the bot is missing), the admin console on another host,
	// the token in the secret store only.
	sdir := t.TempDir()
	copyDir(t, "../../examples/basic", sdir)
	data := filepath.Join(sdir, ".cicord", "data")
	t.Setenv("CICORD_RUNTIME_TOKEN", "")
	t.Setenv("CICORD_SECRET_KEY", "")
	// A first start creates the runtime state and its secret store.
	sp := startCicord(t, "serve", "--local", "--listen", "127.0.0.1:0", "--admin-url", "https://admin.example.test",
		"--public-url", "https://portal.example.test", "--data-dir", data, "--log-level", "error", sdir)
	sp.wait(t, regexp.MustCompile(`listening on`))
	sp.stop(t)
	if code, _, stderr := run(t, "healthcheck", "--service", "--data-dir", data, "--url", "http://127.0.0.1:1/healthz"); code != 1 {
		t.Fatalf("no runtime listening = %d %s", code, stderr)
	}
	withStdinCmd(t, "rt-store-token\n", "secret", "set", "CICORD_RUNTIME_TOKEN", "--data-dir", data)
	ln := freeAddr(t)
	sp = startCicord(t, "serve", "--local", "--listen", ln, "--admin-url", "https://admin.example.test",
		"--public-url", "https://portal.example.test", "--data-dir", data, "--log-level", "error", sdir)
	sp.wait(t, regexp.MustCompile(`listening on`))
	defer sp.stop(t)
	url := "http://" + ln + "/healthz"
	code, stdout, stderr = run(t, "healthcheck", "--service", "--data-dir", data, "--admin-url", "https://admin.example.test", "--url", url, "--timeout", "5s")
	if code != 1 || strings.Contains(stderr, "401") || strings.Contains(stderr, "404") || strings.Contains(stderr, "403") {
		t.Fatalf("service check with a missing bot = %d\n%s\n%s", code, stdout, stderr)
	}
	// Without the admin host, the admin API does not answer.
	if code, _, stderr := run(t, "healthcheck", "--service", "--data-dir", data, "--admin-url", "", "--url", url, "--timeout", "5s"); code != 1 || !strings.Contains(stderr, "HTTP 404") {
		t.Fatalf("service check without the admin host = %d %s", code, stderr)
	}
	// No token at all: exit 2.
	if code, _, stderr := run(t, "healthcheck", "--service", "--url", url); code != 2 || !strings.Contains(stderr, "no runtime token") {
		t.Fatalf("service check without a token = %d %s", code, stderr)
	}
}
