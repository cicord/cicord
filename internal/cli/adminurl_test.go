package cli

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/sim"
)

func TestNormalizeAdminURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                      "",
		"https://cicord.example-tailnet.ts.net": "https://cicord.example-tailnet.ts.net",
		"https://Cicord.Example.org/":           "https://cicord.example.org",
		"https://cicord.example.org:8443":       "https://cicord.example.org:8443",
		"http://127.0.0.1:7421":                 "http://127.0.0.1:7421",
		"http://localhost:7421/":                "http://localhost:7421",
		"http://[::1]:7421":                     "http://[::1]:7421",
	} {
		got, err := normalizeAdminURL(in)
		if err != nil || got != want {
			t.Errorf("normalizeAdminURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"http://cicord.example.org", // plain http off loopback
		"ftp://cicord.example.org",
		"cicord.example.org",
		"/admin",
		"https://cicord.example.org/admin",
		"https://cicord.example.org?x=1",
		"https://cicord.example.org?",
		"https://cicord.example.org#x",
		"https://user:pw@cicord.example.org",
		"https://",
	} {
		if got, err := normalizeAdminURL(in); err == nil {
			t.Errorf("normalizeAdminURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestCheckAdminDistinct(t *testing.T) {
	for _, c := range []struct {
		admin, public string
		ok            bool
	}{
		{"", "https://community.example.org", true},
		{"https://cicord.example.ts.net", "https://community.example.org", true},
		{"https://community.example.org:8443", "https://community.example.org", true},
		{"http://127.0.0.1:7421", "http://localhost:7420", true},
		{"https://community.example.org", "https://community.example.org", false},
		{"https://community.example.org", "https://Community.Example.org:443/", false},
	} {
		if err := checkAdminDistinct(c.admin, c.public); (err == nil) != c.ok {
			t.Errorf("checkAdminDistinct(%q, %q) = %v", c.admin, c.public, err)
		}
	}
}

func TestHealthcheckCommand(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL + "/healthz"
	gone.Close() // connection refused

	for _, c := range []struct {
		url    string
		code   int
		stderr string
	}{
		{ok.URL + "/healthz", 0, ""},
		{down.URL + "/healthz", 1, "503"},
		{goneURL, 1, "unhealthy:"},
		{"://bad", 1, "invalid --url"},
	} {
		var out, errb syncBuffer
		code := ExecuteContext(context.Background(), []string{"healthcheck", "--url", c.url, "--timeout", "2s"}, &out, &errb)
		if code != c.code || !strings.Contains(errb.String(), c.stderr) || strings.Contains(errb.String(), "Usage") {
			t.Errorf("healthcheck %s = %d (stdout %q, stderr %q)", c.url, code, out.String(), errb.String())
		}
		if c.code == 0 && strings.TrimSpace(out.String()) != "ok" {
			t.Errorf("healthcheck ok: stdout %q", out.String())
		}
		if c.code != 0 && strings.Count(strings.TrimSpace(errb.String()), "\n") != 0 {
			t.Errorf("healthcheck %s: multi-line error %q", c.url, errb.String())
		}
	}
}

// The setup link and the startup banner point at the admin URL.
func TestServeAdminURLOutput(t *testing.T) {
	world := sim.NewWorld()
	env, err := setup.NewSimEnv(world)
	if err != nil {
		t.Fatal(err)
	}
	old := serveDiscord
	serveDiscord = func(*slog.Logger) (discord.Connector, setup.Probe) { return world, env.Probe() }
	defer func() { serveDiscord = old }()
	t.Setenv("CICORD_SECRET_KEY", "")
	t.Setenv("CICORD_RUNTIME_TOKEN", "")
	t.Setenv("CICORD_ADMIN_URL", "")

	// Setup mode (no cicord.yaml).
	p := startCicord(t, "serve", "--listen", "127.0.0.1:0", "--data-dir", t.TempDir(), "--log-level", "warn",
		"--public-url", "https://community.example.test", "--admin-url", "https://admin.example.test/", t.TempDir())
	p.wait(t, regexp.MustCompile(`open the setup wizard: https://admin\.example\.test/admin/setup\?token=\S+`))
	if strings.Contains(p.stdout.String(), "community.example.test/admin") {
		t.Errorf("setup link on the public URL:\n%s", p.stdout.String())
	}
	p.stop(t)

	// Normal boot (local repository).
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	p = startCicord(t, "serve", "--local", "--listen", "127.0.0.1:0", "--data-dir", filepath.Join(dir, ".cicord", "data"),
		"--log-level", "error", "--public-url", "https://community.example.test", "--admin-url", "https://admin.example.test", dir)
	p.wait(t, regexp.MustCompile(`listening on https://community\.example\.test\n  admin UI: https://admin\.example\.test/admin/\n`))
	p.stop(t)

	// Invalid or ambiguous admin URLs are refused before anything starts.
	for _, admin := range []string{"http://admin.example.test", "https://community.example.test"} {
		var out, errb syncBuffer
		code := ExecuteContext(context.Background(), []string{"serve", "--listen", "127.0.0.1:0", "--data-dir", t.TempDir(),
			"--public-url", "https://community.example.test", "--admin-url", admin, t.TempDir()}, &out, &errb)
		if code == 0 || !strings.Contains(errb.String(), "--admin-url") {
			t.Errorf("--admin-url %s = %d %s", admin, code, errb.String())
		}
	}
}
