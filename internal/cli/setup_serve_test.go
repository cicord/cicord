package cli

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"

	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/setup"
	"github.com/npjonath/cicord/internal/sim"
	"github.com/npjonath/cicord/internal/state"
	"github.com/npjonath/cicord/internal/state/sqlite"
)

type cicordProc struct {
	stdout, stderr syncBuffer
	cancel         context.CancelFunc
	done           chan int
}

func startCicord(t *testing.T, args ...string) *cicordProc {
	t.Helper()
	p := &cicordProc{done: make(chan int, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() { p.done <- ExecuteContext(ctx, args, &p.stdout, &p.stderr) }()
	return p
}

func (p *cicordProc) wait(t *testing.T, re *regexp.Regexp) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if m := re.FindStringSubmatch(p.stdout.String()); m != nil {
			return m
		}
		select {
		case code := <-p.done:
			t.Fatalf("cicord exited with %d:\n%s\n%s", code, p.stdout.String(), p.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s:\n%s\n%s", re, p.stdout.String(), p.stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *cicordProc) stop(t *testing.T) {
	t.Helper()
	p.cancel()
	select {
	case code := <-p.done:
		if code != 0 {
			t.Errorf("exit code %d; stderr:\n%s", code, p.stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Error("cicord did not stop")
	}
}

var setupURLRe = regexp.MustCompile(`(http://127\.0\.0\.1:\d+)/admin/setup\?token=(\S+)`)

func newSetupClient(t *testing.T, base string) *setupClient {
	jar, _ := cookiejar.New(nil)
	return &setupClient{t: t, base: base, hc: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// TestServeSetupResumeAndRestart covers `cicord serve` without cicord.yaml: setup mode, the
// wizard resumes after a restart, the embedded repository gets the first commit, the
// runtime activates without restart, and a later restart boots from the repository the
// wizard created (bot token read back from the encrypted secret store).
func TestServeSetupResumeAndRestart(t *testing.T) {
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

	dir, data := t.TempDir(), t.TempDir()
	args := []string{"serve", "--listen", "127.0.0.1:0", "--data-dir", data, "--log-level", "warn", dir}

	// Run 1: token + server, then stop.
	p := startCicord(t, args...)
	m := p.wait(t, setupURLRe)
	c := newSetupClient(t, m[1])
	c.ok("POST", "/api/v1/setup/session", map[string]any{"token": m[2]})
	c.ok("POST", "/api/v1/setup/intent", map[string]any{"intent": "existing"})
	tok, appID := env.CreateBot("Cicord")
	if err := env.SetIntents(appID, discord.PrivilegedIntents); err != nil {
		t.Fatal(err)
	}
	c.ok("POST", "/api/v1/setup/discord/token", map[string]any{"token": tok})
	srv := env.Servers()[0]
	if err := env.Invite(tok, srv.ID, false); err != nil {
		t.Fatal(err)
	}
	if res := c.ok("POST", "/api/v1/setup/server", map[string]any{"serverId": srv.ID}); res["ok"] != true {
		t.Fatalf("server checks = %v", res["checks"])
	}
	if code, _ := c.do("GET", "/api/v1/setup/sim", nil); code != 404 {
		t.Fatalf("sim endpoints in serve = %d", code)
	}
	p.stop(t)
	if fi, err := os.Stat(filepath.Join(data, "secret.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret.key: %v %v", fi, err)
	}

	// Run 2: a new link; the wizard resumes at the Server step.
	p = startCicord(t, args...)
	m2 := p.wait(t, setupURLRe)
	if m2[2] == m[2] {
		t.Fatal("setup token reused across restarts")
	}
	c = newSetupClient(t, m2[1])
	if code, _ := c.do("POST", "/api/v1/setup/session", map[string]any{"token": m[2]}); code != 401 {
		t.Fatalf("old token = %d", code)
	}
	st := c.ok("POST", "/api/v1/setup/session", map[string]any{"token": m2[2]})
	if get(st, "view", "step") != "server" || get(st, "view", "bot", "username") != "Cicord" || get(st, "view", "server", "key") != "pixel-pals" {
		t.Fatalf("resumed view = %v", st["view"])
	}
	if ps := get(st, "view", "providers").([]any); ps[0] != "embedded" || ps[1] != "github" {
		t.Fatalf("providers = %v", ps)
	}
	c.ok("POST", "/api/v1/setup/step", map[string]any{"step": "repository"})
	c.ok("POST", "/api/v1/setup/repo", map[string]any{"provider": "embedded"})
	imp := c.ok("POST", "/api/v1/setup/import", map[string]any{})
	if get(imp, "view", "activated") != true || len(get(imp, "plan", "changes").([]any)) != 0 {
		t.Fatalf("import = %v", imp)
	}
	repo, err := git.PlainOpen(filepath.Join(data, "repo.git"))
	if err != nil {
		t.Fatal(err)
	}
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	if !strings.HasPrefix(commit.Message, "Set up Cicord for Pixel Pals") {
		t.Fatalf("first commit = %q", commit.Message)
	}
	if code, _ := c.do("GET", "/readyz", nil); code != 200 {
		t.Fatalf("readyz after activation = %d", code)
	}
	p.stop(t)

	// Run 3: boots from the embedded repository; the unfinished wizard is resumable.
	p = startCicord(t, args...)
	p.wait(t, regexp.MustCompile(`configuration: embedded repository`))
	m3 := p.wait(t, regexp.MustCompile(`setup wizard not finished: (http://127\.0\.0\.1:\d+)/admin/setup\?token=(\S+)`))
	c = newSetupClient(t, m3[1])
	deadline := time.Now().Add(20 * time.Second)
	for {
		if code, _ := c.do("GET", "/readyz", nil); code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not ready after restart:\n%s", p.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.ok("POST", "/api/v1/setup/session", map[string]any{"token": m3[2]})
	st = c.ok("GET", "/api/v1/status", nil)
	bots := st["bots"].([]any)
	if len(bots) != 1 || bots[0].(map[string]any)["health"] != "Healthy" {
		t.Fatalf("bot after restart = %v (token must come from the secret store)", bots)
	}
	c.ok("POST", "/api/v1/setup/oauth/skip", nil)
	if rep := c.ok("GET", "/api/v1/setup/verify", nil); len(rep["checks"].([]any)) != 9 {
		t.Fatalf("verify = %v", rep)
	}
	c.ok("POST", "/api/v1/setup/finish", nil)
	p.stop(t)
}

func TestSetupBootDecision(t *testing.T) {
	// A configured repository never enters setup mode.
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	var out, errb syncBuffer
	if code := ExecuteContext(context.Background(), []string{"dev", "--setup", "--listen", "127.0.0.1:0", dir}, &out, &errb); code == 0 ||
		!strings.Contains(errb.String(), "already contains a cicord.yaml") {
		t.Fatalf("dev --setup on a repository = %d %s", code, errb.String())
	}
	p := startCicord(t, "dev", "--sim", "--listen", "127.0.0.1:0", "--data-dir", t.TempDir(), "--log-level", "warn", dir)
	p.wait(t, regexp.MustCompile(`listening on http://`))
	if strings.Contains(p.stdout.String(), "setup") {
		t.Fatalf("setup mode with cicord.yaml:\n%s", p.stdout.String())
	}
	p.stop(t)
}

func TestSecretCommandAndDataDirGuard(t *testing.T) {
	data := t.TempDir()
	// A data dir with a sync history belongs to a deployment: no setup mode on it.
	st, err := sqlite.Open(filepath.Join(data, "cicord.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Syncs().Record(context.Background(), state.SyncRecord{Revision: "abc", Target: "revision", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	var out, errb syncBuffer
	if code := ExecuteContext(context.Background(), []string{"serve", "--listen", "127.0.0.1:0", "--data-dir", data, t.TempDir()}, &out, &errb); code == 0 ||
		!strings.Contains(errb.String(), "belongs to an existing deployment") {
		t.Fatalf("serve on a used data dir = %d %s", code, errb.String())
	}

	// cicord secret set / ls / rotate-key / rm.
	run := func(stdin string, args ...string) (int, string) {
		var o, e syncBuffer
		cmd := newRootCmd()
		cmd.SetArgs(append(args, "--data-dir", data))
		cmd.SetIn(strings.NewReader(stdin))
		cmd.SetOut(&o)
		cmd.SetErr(&e)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			return 1, err.Error() + e.String()
		}
		return 0, o.String()
	}
	t.Setenv("CICORD_SECRET_KEY", "")
	if code, o := run("tok-123\n", "secret", "set", "DISCORD_TOKEN_CICORD"); code != 0 || strings.Contains(o, "tok-123") {
		t.Fatalf("set = %d %s", code, o)
	}
	if code, o := run("", "secret", "rotate-key"); code != 0 {
		t.Fatalf("rotate = %s", o)
	}
	if code, o := run("", "secret", "ls"); code != 0 || !strings.Contains(o, "DISCORD_TOKEN_CICORD") || strings.Contains(o, "tok-123") {
		t.Fatalf("ls = %d %s", code, o)
	}
	st, _ = sqlite.Open(filepath.Join(data, "cicord.db"))
	sec, err := openSecrets(context.Background(), st, data, false)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := sec.Get(context.Background(), "DISCORD_TOKEN_CICORD"); v != "tok-123" {
		t.Fatalf("value after rotation = %q", v)
	}
	st.Close()
	if code, o := run("", "secret", "rm", "DISCORD_TOKEN_CICORD"); code != 0 {
		t.Fatal(o)
	}
}
