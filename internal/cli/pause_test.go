package cli

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/api"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// startRuntimeOf is startRuntime for an example directory, edited before the runtime
// starts. It returns the API URL, the deployed directory and the runtime.
func startRuntimeOf(t *testing.T, example string, edit func(dir string)) (string, string, *runtime.Runtime) {
	t.Helper()
	dir := t.TempDir()
	copyDir(t, example, dir)
	if edit != nil {
		edit(dir)
	}
	repo, _ := config.Load(dir, config.LoadOptions{})
	if repo == nil {
		t.Fatalf("cannot load %s", example)
	}
	w := sim.NewWorld()
	seed, err := api.SeedSimWorld(w, repo)
	if err != nil {
		t.Fatal(err)
	}
	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := runtime.New(runtime.Options{
		Source: src, Connector: w, Store: store, Mode: "serve", Simulator: true, ResyncInterval: -1,
		Secrets: func(n string) (string, bool) { v, ok := seed.Secrets[n]; return v, ok }, Logger: log,
	})
	ts := httptest.NewServer(api.New(api.Options{Runtime: rt, Mode: "serve", RuntimeToken: runtimeToken, Logger: log, MemberCacheTTL: -1}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rt.Run(ctx); close(done) }()
	t.Cleanup(func() {
		ts.Close()
		cancel()
		<-done
		src.Close()
		store.Close()
	})
	waitUntil(t, "a revision", func() bool { return rt.Ready() })
	return ts.URL, dir, rt
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func editFile(t *testing.T, file, old, new string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s: %q not found", file, old)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	ticketsStart  = `ctx.log.info("tickets ready", { staffRoles: ctx.config.staffRoles.length });`
	ticketsBroken = `throw new Error("tickets: database unreachable");`
)

// A plugin failing at startup fails `cicord status --wait` at once, naming the bot and the
// plugin instance; a revision that fixes it makes --wait succeed.
func TestStatusWaitPluginStartFailure(t *testing.T) {
	index := ""
	url, _, rt := startRuntimeOf(t, "../../examples/showcase", func(dir string) {
		index = filepath.Join(dir, "plugins/tickets/src/index.ts")
		editFile(t, index, ticketsStart, ticketsBroken)
	})
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	t.Setenv("CICORD_RUNTIME_URL", url)
	code, out, errOut := run(t, "status", "--wait", "--timeout", "20s", "--interval", "50ms")
	if code != 1 || !strings.Contains(errOut, "is active but not healthy") ||
		!strings.Contains(errOut, "plugin tickets (bot cicord) on showcase: Degraded: plugin: onStart: Error: tickets: database unreachable") ||
		!strings.Contains(out, "cicord               Degraded") {
		t.Fatalf("status --wait = %d\n%s\n%s", code, out, errOut)
	}

	// the fix: a new revision
	broken, _ := rt.Revision()
	editFile(t, index, ticketsBroken, ticketsStart)
	if code, _, errOut := run(t, "sync"); code != 0 {
		t.Fatalf("sync = %d: %s", code, errOut)
	}
	waitUntil(t, "the fixed revision", func() bool { r, _ := rt.Revision(); return r.SHA != broken.SHA })
	fixed, _ := rt.Revision()
	if code, _, errOut := run(t, "status", "--wait", "--revision", fixed.SHA, "--timeout", "20s", "--interval", "50ms"); code != 0 {
		t.Fatalf("status --wait after the fix = %d: %s", code, errOut)
	}
}

// cicord pause / resume against a runtime: a paused server fails --wait; resume shows the
// pending plan and asks for confirmation; resume --yes --sync makes --wait succeed.
func TestPauseResumeCommands(t *testing.T) {
	url, _, rt := startRuntimeOf(t, "../../examples/basic", nil)
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	t.Setenv("CICORD_RUNTIME_URL", "")
	if code, _, errOut := run(t, "pause"); code == 0 || !strings.Contains(errOut, "accepts between 1 and 3 arg(s)") {
		t.Fatalf("pause without a server = %d: %s", code, errOut)
	}
	code, out, errOut := run(t, "pause", "my-community", "--reason", "raid: #general locked by hand", "--remote="+url)
	if code != 0 || !strings.Contains(out, "Enforcement paused on my-community: nothing is applied until `cicord resume my-community`.") {
		t.Fatalf("pause = %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ := run(t, "pause", "my-community", "--remote", url); code != 0 || !strings.Contains(out, "was already paused by runtime-token") {
		t.Fatalf("second pause = %d\n%s", code, out)
	}
	t.Setenv("CICORD_RUNTIME_URL", url)
	code, out, _ = run(t, "status")
	if code != 0 || !strings.Contains(out, "PAUSED") || !strings.Contains(out, "enforcement paused by runtime-token since ") ||
		!strings.Contains(out, "(raid: #general locked by hand)") {
		t.Fatalf("status = %d\n%s", code, out)
	}
	code, _, errOut = run(t, "status", "--wait", "--timeout", "10s", "--interval", "50ms")
	if code != 1 || !strings.Contains(errOut, "my-community: enforcement paused by runtime-token") || !strings.Contains(errOut, "resume with `cicord resume my-community`") {
		t.Fatalf("status --wait while paused = %d: %s", code, errOut)
	}

	// resume needs a confirmation
	withStdin(t, false, "")
	if code, out, errOut := run(t, "resume", "my-community"); code != 1 || !strings.Contains(out, "Server my-community is paused by runtime-token") ||
		!strings.Contains(out, "The next sync applies this plan:") || !strings.Contains(errOut, "pass --yes") {
		t.Fatalf("resume without a terminal = %d\n%s\n%s", code, out, errOut)
	}
	withStdin(t, true, "no\n")
	if code, _, errOut := run(t, "resume", "my-community"); code != 1 || !strings.Contains(errOut, "the server stays paused") {
		t.Fatalf("resume answered no = %d: %s", code, errOut)
	}
	if _, ok := rt.Paused("my-community"); !ok {
		t.Fatal("resumed without confirmation")
	}
	code, out, errOut = run(t, "resume", "my-community", "--yes", "--sync")
	if code != 0 || !strings.Contains(out, "Enforcement resumed on my-community and synced: 0 change(s) applied.") {
		t.Fatalf("resume --yes --sync = %d\n%s\n%s", code, out, errOut)
	}
	if code, _, errOut := run(t, "resume", "my-community", "--yes"); code != 0 || !strings.Contains(errOut, "Server my-community is not paused.") {
		t.Fatalf("resume of a running server = %d: %s", code, errOut)
	}
	if code, _, errOut := run(t, "status", "--wait", "--timeout", "10s", "--interval", "50ms"); code != 0 {
		t.Fatalf("status --wait after resume = %d: %s", code, errOut)
	}
	if code, _, errOut := run(t, "pause", "nope"); code != 1 || !strings.Contains(errOut, "unknown server") {
		t.Fatalf("pause of an unknown server = %d: %s", code, errOut)
	}
}

// --wait fails at once on a Missing bot and on a plugin instance in a restart loop, and a
// timeout on blocked changes says why the server stays OutOfSync.
func TestStatusWaitBotsAndBlocked(t *testing.T) {
	old := pollInterval
	pollInterval = 10 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	serve := func(status string) {
		ts := httptest.NewServer(&fakeStatus{statuses: []string{status}, history: `{"items": []}`, server: `{"items": []}`})
		t.Cleanup(ts.Close)
		t.Setenv("CICORD_RUNTIME_URL", ts.URL)
	}
	healthy := statusJSON(sha, "Synced", "Healthy", "")

	serve(strings.Replace(healthy, `"health": "Healthy", "servers": ["my-community"]`,
		`"health": "Missing", "message": "secret DISCORD_TOKEN_MODERATOR is not set", "servers": ["my-community"]`, 1))
	if code, _, errOut := run(t, "status", "--wait", "--timeout", "5s"); code != 1 ||
		!strings.Contains(errOut, "bot moderator: Missing: secret DISCORD_TOKEN_MODERATOR is not set") {
		t.Errorf("missing bot = %d: %s", code, errOut)
	}

	serve(strings.Replace(healthy, `"server": "my-community", "health": "Healthy"}`,
		`"server": "my-community", "health": "Degraded", "message": "plugin: crashed: memory limit (restarting in 4s)"}`, 1))
	if code, _, errOut := run(t, "status", "--wait", "--timeout", "5s"); code != 1 ||
		!strings.Contains(errOut, "plugin welcome (bot moderator) on my-community: Degraded: plugin: crashed: memory limit (restarting in 4s)") ||
		strings.Contains(errOut, "bot moderator:") {
		t.Errorf("restart loop = %d: %s", code, errOut)
	}

	serve(statusJSON(sha, "OutOfSync", "Healthy", "1 change(s) blocked (delete channel old: deletionProtection is on)"))
	if code, _, errOut := run(t, "status", "--wait", "--timeout", "100ms"); code != 1 ||
		!strings.Contains(errOut, "Server my-community stays OutOfSync: 1 change(s) blocked (delete channel old: deletionProtection is on). Blocked changes need a fix in git or on Discord.") {
		t.Errorf("blocked = %d: %s", code, errOut)
	}
}
