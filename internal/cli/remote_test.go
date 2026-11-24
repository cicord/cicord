package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/npjonath/cicord/internal/api"
	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/runtime"
	"github.com/npjonath/cicord/internal/sim"
	"github.com/npjonath/cicord/internal/source"
	"github.com/npjonath/cicord/internal/state/sqlite"
)

const runtimeToken = "test-runtime-token"

// startRuntime deploys examples/basic on the simulator behind the HTTP API (like
// internal/api's tests) and returns the API URL and the deployed directory.
func startRuntime(t *testing.T) (string, string, *sim.World) {
	t.Helper()
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	repo, _ := config.Load(dir, config.LoadOptions{})
	if repo == nil {
		t.Fatal("cannot load examples/basic")
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
	deadline := time.Now().Add(20 * time.Second)
	for !rt.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("runtime did not activate a revision")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ts.URL, dir, w
}

func TestPlanRemote(t *testing.T) {
	url, deployed, _ := startRuntime(t)
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	t.Setenv("CICORD_RUNTIME_URL", "")

	// a pull request renaming a channel, adding a role and a channel
	dir := t.TempDir()
	copyDir(t, deployed, dir)
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=1\n") // never uploaded
	p := filepath.Join(dir, "servers", "ma-commu.yaml")
	data := readFile(t, p)
	data = strings.Replace(data, "name: général", "name: discussions", 1)
	data = strings.Replace(data, "  hierarchy:", "    vip:\n      name: VIP\n      color: \"#ff0000\"\n\n  hierarchy:", 1)
	writeFile(t, p, data)

	code, out, errOut := run(t, "plan", "--remote="+url, dir, "--detailed-exitcode")
	if code != 2 {
		t.Fatalf("plan --remote = %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"Server ma-commu: ", "~ rename channel #général → #discussions", `name: "général" → "discussions"`,
		`+ create role "VIP"`, `color:       "#ff0000"`, "Plan: 1 to create, 1 to update, 0 to delete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output misses %q:\n%s", want, out)
		}
	}
	// bare --remote (followed by another flag) uses CICORD_RUNTIME_URL
	t.Setenv("CICORD_RUNTIME_URL", url)
	code, out, _ = run(t, "plan", "--remote", "-o", "json", dir)
	var res struct {
		Valid bool
		Plans []struct{ Server string }
	}
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || !res.Valid || len(res.Plans) != 1 {
		t.Fatalf("plan --remote -o json = %d\n%s", code, out)
	}
	// URL as a positional argument after a bare --remote
	if code, _, errOut := run(t, "plan", "--remote", url, dir); code != 0 {
		t.Errorf("plan --remote URL dir = %d: %s", code, errOut)
	}

	// invalid configuration: diagnostics, exit 1
	writeFile(t, p, strings.Replace(data, "category: community", "category: nope", 1))
	code, _, errOut = run(t, "plan", "--remote", dir)
	if code != 1 || !strings.Contains(errOut, `unknown category "nope"`) || !strings.Contains(errOut, "invalid") {
		t.Errorf("invalid plan = %d: %s", code, errOut)
	}
	// wrong token
	t.Setenv("CICORD_RUNTIME_TOKEN", "wrong")
	if code, _, errOut := run(t, "plan", "--remote", dir); code != 1 || !strings.Contains(errOut, "401") {
		t.Errorf("wrong token = %d: %s", code, errOut)
	}
}

func TestArchiveExclusions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cicord.yaml"), "x")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.db\nlogs/\n/secret.txt\n")
	for _, f := range []string{"servers/a.yaml", ".env", ".env.local", "x.db", "logs/a.log", "secret.txt", "sub/secret.txt",
		"node_modules/m/i.js", ".git/HEAD", "plugins/p/dist/b.js", ".cicord/state.db", "plugins/p/src/i.ts"} {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(f)), "x")
	}
	os.Symlink("/etc/passwd", filepath.Join(dir, "link"))
	data, n, err := archiveRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := tarNames(t, data)
	for _, want := range []string{"cicord.yaml", "servers/a.yaml", "plugins/p/src/i.ts", "sub/secret.txt", ".gitignore"} {
		if !names[want] {
			t.Errorf("%s missing from the archive: %v", want, names)
		}
	}
	for _, not := range []string{".env", ".env.local", "x.db", "logs/a.log", "secret.txt", "node_modules/m/i.js", ".git/HEAD", "plugins/p/dist/b.js", ".cicord/state.db", "link"} {
		if names[not] {
			t.Errorf("%s must not be uploaded", not)
		}
	}
	if n != 5 {
		t.Errorf("%d files", n)
	}
}

// fakeGitHub records comments of one pull request.
type fakeGitHub struct {
	mu       sync.Mutex
	comments []ghComment
	nextID   int64
	posts    int
	patches  int
	auth     []string
}

func (g *fakeGitHub) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/o/r/issues/7/comments":
			page := 1
			fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
			lo, hi := (page-1)*100, page*100
			out := []ghComment{}
			for i := lo; i < hi && i < len(g.comments); i++ {
				out = append(out, g.comments[i])
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == "POST" && r.URL.Path == "/repos/o/r/issues/7/comments":
			var body struct{ Body string }
			json.NewDecoder(r.Body).Decode(&body)
			g.nextID++
			c := ghComment{ID: 1000 + g.nextID, Body: body.Body, HTMLURL: fmt.Sprintf("https://github.example/c/%d", 1000+g.nextID)}
			c.User.Type = "Bot"
			g.comments = append(g.comments, c)
			g.posts++
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(c)
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/comments/"):
			var body struct{ Body string }
			json.NewDecoder(r.Body).Decode(&body)
			for i := range g.comments {
				if fmt.Sprintf("/repos/o/r/issues/comments/%d", g.comments[i].ID) == r.URL.Path {
					g.comments[i].Body = body.Body
					g.patches++
					json.NewEncoder(w).Encode(g.comments[i])
					return
				}
			}
			w.WriteHeader(404)
		default:
			t.Errorf("unexpected GitHub call %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	})
}

func TestPlanComment(t *testing.T) {
	// a canned runtime answer
	var valid atomic.Bool
	valid.Store(true)
	rts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/plan" || r.Header.Get("Authorization") != "Bearer "+runtimeToken {
			w.WriteHeader(401)
			return
		}
		io.Copy(io.Discard, r.Body)
		if !valid.Load() {
			io.WriteString(w, `{"valid": false, "diagnostics": [{"severity": "error", "file": "servers/ma-commu.yaml", "line": 3, "message": "boom"}], "plans": []}`)
			return
		}
		io.WriteString(w, `{"valid": true, "diagnostics": [], "plans": [{"server": "ma-commu", "changes": [
		  {"action": "delete", "kind": "channel", "key": "old", "summary": "delete channel #old"}], "blocked": []}]}`)
	}))
	defer rts.Close()
	gh := &fakeGitHub{}
	// 100 unrelated comments: the Cicord comment is found on the second page
	for i := 0; i < 100; i++ {
		c := ghComment{ID: int64(i + 1), Body: "lgtm"}
		c.User.Type = "User"
		if i == 50 { // a person quoting the marker: never edited
			c.Body = "> <!-- cicord-plan:ma-commu --> why?"
		}
		gh.comments = append(gh.comments, c)
	}
	ghs := httptest.NewServer(gh.handler(t))
	defer ghs.Close()

	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	event := filepath.Join(t.TempDir(), "event.json")
	writeFile(t, event, `{"action": "synchronize", "number": 7, "pull_request": {"number": 7}}`)
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	t.Setenv("CICORD_RUNTIME_URL", rts.URL)
	t.Setenv("GITHUB_API_URL", ghs.URL)
	t.Setenv("GITHUB_TOKEN", "gh-token")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_EVENT_PATH", event)
	t.Setenv("GITHUB_SHA", "0123456789abcdef0123456789abcdef01234567")

	code, out, errOut := run(t, "plan", "--remote", "--comment", dir)
	if code != 0 || !strings.Contains(out, "- delete channel #old") || !strings.Contains(errOut, "Plan posted on pull request #7") {
		t.Fatalf("plan --comment = %d\n%s\n%s", code, out, errOut)
	}
	code, _, errOut = run(t, "plan", "--remote", "--comment", dir)
	if code != 0 || !strings.Contains(errOut, "Plan updated on pull request #7") {
		t.Fatalf("second plan --comment = %d\n%s", code, errOut)
	}
	valid.Store(false)
	code, _, _ = run(t, "plan", "--remote", "--comment", dir)
	if code != 1 {
		t.Errorf("invalid plan --comment = %d", code)
	}
	// not a pull request: warning, no comment, no failure
	valid.Store(true)
	writeFile(t, event, `{"ref": "refs/heads/main"}`)
	code, _, errOut = run(t, "plan", "--remote", "--comment", dir)
	if code != 0 || !strings.Contains(errOut, "not running for a pull request") {
		t.Errorf("push event: %d %s", code, errOut)
	}
	gh.mu.Lock()
	defer gh.mu.Unlock()
	if gh.posts != 1 || gh.patches != 2 || len(gh.comments) != 101 {
		t.Fatalf("posts=%d patches=%d comments=%d", gh.posts, gh.patches, len(gh.comments))
	}
	body := gh.comments[100].Body
	for _, want := range []string{"<!-- cicord-plan:ma-commu -->", "**Validation failed**", "servers/ma-commu.yaml:3: error: boom", "at 0123456"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment misses %q:\n%s", want, body)
		}
	}
	for _, a := range gh.auth {
		if a != "Bearer gh-token" {
			t.Errorf("GitHub auth header %q", a)
		}
	}

}

// fakeStatus serves a scripted sequence of status / history answers.
type fakeStatus struct {
	mu       sync.Mutex
	statuses []string // successive /status bodies (the last one repeats)
	history  string   // /history?target=revision body
	server   string   // /history?target=server:… body
	calls    int
	syncs    int
}

func (f *fakeStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+runtimeToken {
		w.WriteHeader(401)
		io.WriteString(w, `{"error": {"code": "unauthenticated", "message": "bad token"}}`)
		return
	}
	switch {
	case r.URL.Path == "/api/v1/status":
		i := min(f.calls, len(f.statuses)-1)
		f.calls++
		io.WriteString(w, f.statuses[i])
	case r.URL.Path == "/api/v1/history":
		if strings.HasPrefix(r.URL.Query().Get("target"), "server:") {
			io.WriteString(w, f.server)
			return
		}
		io.WriteString(w, f.history)
	case r.URL.Path == "/api/v1/sync" && r.Method == "POST":
		f.syncs++
		w.WriteHeader(202)
		io.WriteString(w, `{"status": "accepted"}`)
	default:
		w.WriteHeader(404)
	}
}

const sha = "4f0c2d9e8b1a7c6d5e4f3a2b1c0d9e8f7a6b5c4d"

func statusJSON(rev, sync, health, msg string) string {
	revJSON := "null"
	if rev != "" {
		revJSON = fmt.Sprintf(`{"id": %q, "sha": %q, "message": "merge", "author": "dev", "source": "git"}`, rev[:7], rev)
	}
	return fmt.Sprintf(`{"version": "0.1.0", "mode": "serve", "revision": %s, "servers": [
	  {"key": "ma-commu", "name": "Ma Commu", "discordId": "1", "sync": %q, "health": %q, "message": %q, "drift": []}],
	  "bots": [{"key": "moderator", "username": "Gardien", "userId": "2", "health": "Healthy", "servers": ["ma-commu"],
	    "plugins": [{"instance": "welcome", "plugin": "welcome", "version": "0.1.0", "server": "ma-commu", "health": "Healthy"}]}]}`,
		revJSON, sync, health, msg)
}

func TestStatusWait(t *testing.T) {
	old := pollInterval
	pollInterval = 10 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	dir := t.TempDir()

	serve := func(f *fakeStatus) string {
		ts := httptest.NewServer(f)
		t.Cleanup(ts.Close)
		t.Setenv("CICORD_RUNTIME_URL", ts.URL)
		return ts.URL
	}
	const otherSHA = "aaaaaaa000000000000000000000000000000000"

	t.Run("success", func(t *testing.T) {
		f := &fakeStatus{statuses: []string{
			statusJSON("", "Unknown", "Progressing", ""),
			statusJSON(otherSHA, "Synced", "Healthy", ""),
			statusJSON(sha, "OutOfSync", "Progressing", ""),
			statusJSON(sha, "Synced", "Healthy", ""),
		}, history: `{"items": []}`}
		serve(f)
		code, out, errOut := run(t, "status", dir, "--wait", "--revision", sha, "--timeout", "10s")
		if code != 0 || !strings.Contains(errOut, "Revision 4f0c2d9 is active and every server is Synced and Healthy") ||
			!strings.Contains(out, "ma-commu") || !strings.Contains(out, "plugin welcome (welcome@0.1.0) on ma-commu: Healthy") {
			t.Fatalf("wait = %d\n%s\n%s", code, out, errOut)
		}
		if f.calls < 4 {
			t.Errorf("only %d polls", f.calls)
		}
	})
	t.Run("prefix", func(t *testing.T) {
		serve(&fakeStatus{statuses: []string{statusJSON(sha, "Synced", "Healthy", "")}, history: `{"items": []}`})
		if code, _, errOut := run(t, "status", "--wait", "--revision", "4f0c2d9e", dir); code != 0 {
			t.Errorf("prefix wait = %d: %s", code, errOut)
		}
	})
	t.Run("rejected", func(t *testing.T) {
		serve(&fakeStatus{statuses: []string{statusJSON(otherSHA, "Synced", "Healthy", "")},
			history: `{"items": [{"id": 3, "time": "2026-11-21T10:00:00Z", "revision": "4f0c2d9", "target": "revision", "status": "rejected",
			  "message": "1 validation error(s):", "details": [{"severity": "error", "file": "servers/ma-commu.yaml", "line": 12, "message": "unknown role \"x\""}]}]}`})
		code, _, errOut := run(t, "status", "--wait", "--revision", sha, dir)
		if code != 1 || !strings.Contains(errOut, "Revision 4f0c2d9 was rejected") || !strings.Contains(errOut, `servers/ma-commu.yaml:12: error: unknown role "x"`) {
			t.Errorf("rejected = %d: %s", code, errOut)
		}
	})
	t.Run("degraded", func(t *testing.T) {
		serve(&fakeStatus{statuses: []string{statusJSON(sha, "OutOfSync", "Degraded", "missing permissions")}, history: `{"items": []}`,
			server: `{"items": [{"id": 9, "revision": "4f0c2d9", "target": "server:ma-commu", "status": "failed", "message": "create role: missing permissions"}]}`})
		code, _, errOut := run(t, "status", "--wait", "--revision", sha, dir)
		if code != 1 || !strings.Contains(errOut, "ma-commu: OutOfSync Degraded: missing permissions") ||
			!strings.Contains(errOut, "last sync (failed, revision 4f0c2d9): create role: missing permissions") {
			t.Errorf("degraded = %d: %s", code, errOut)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		serve(&fakeStatus{statuses: []string{statusJSON(sha, "OutOfSync", "Healthy", "1 change(s) blocked")}, history: `{"items": []}`})
		code, _, errOut := run(t, "status", "--wait", "--revision", sha, "--timeout", "100ms", dir)
		if code != 1 || !strings.Contains(errOut, "Timed out after 100ms") || !strings.Contains(errOut, "1 change(s) blocked") {
			t.Errorf("timeout = %d: %s", code, errOut)
		}
	})
	t.Run("superseded", func(t *testing.T) {
		serve(&fakeStatus{statuses: []string{statusJSON(otherSHA, "Synced", "Healthy", "")},
			history: `{"items": [{"id": 4, "revision": "aaaaaaa", "target": "revision", "status": "succeeded"}, {"id": 3, "revision": "4f0c2d9", "target": "revision", "status": "succeeded"}]}`})
		code, _, errOut := run(t, "status", "--wait", "--revision", sha, dir)
		if code != 0 || !strings.Contains(errOut, "superseded") {
			t.Errorf("superseded = %d: %s", code, errOut)
		}
	})
	t.Run("plain and sync", func(t *testing.T) {
		f := &fakeStatus{statuses: []string{statusJSON(sha, "Synced", "Healthy", "")}, history: `{"items": []}`}
		serve(f)
		code, out, _ := run(t, "status", dir)
		if code != 0 || !strings.Contains(out, `Revision 4f0c2d9  "merge" by dev`) {
			t.Errorf("status = %d\n%s", code, out)
		}
		code, out, _ = run(t, "status", "-o", "json", dir)
		if code != 0 || !strings.Contains(out, `"sha": "`+sha+`"`) {
			t.Errorf("status -o json = %d\n%s", code, out)
		}
		code, _, errOut := run(t, "sync", "--wait", "--revision", sha[:7], dir)
		if code != 0 || f.syncs != 1 || !strings.Contains(errOut, "Sync requested") {
			t.Errorf("sync --wait = %d (%d syncs): %s", code, f.syncs, errOut)
		}
		t.Setenv("CICORD_RUNTIME_TOKEN", "bad")
		if code, _, errOut := run(t, "status", "--wait", dir); code != 1 || !strings.Contains(errOut, "401") {
			t.Errorf("bad token wait = %d: %s", code, errOut)
		}
	})
}

// TestStatusAgainstRuntime checks the real API on the simulator: --wait succeeds for the
// deployed revision.
func TestStatusAgainstRuntime(t *testing.T) {
	url, _, _ := startRuntime(t)
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	t.Setenv("CICORD_RUNTIME_URL", url)
	code, out, errOut := run(t, "status", "-o", "json")
	var st runtime.Status
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.Revision == nil {
		t.Fatalf("status = %d\n%s\n%s", code, out, errOut)
	}
	code, _, errOut = run(t, "status", "--wait", "--revision", st.Revision.SHA[:10], "--timeout", "20s", "--interval", "50ms")
	if code != 0 {
		t.Fatalf("status --wait = %d: %s", code, errOut)
	}
}

func TestRuntimeURLSecurity(t *testing.T) {
	t.Setenv("CICORD_RUNTIME_TOKEN", runtimeToken)
	t.Setenv("CICORD_RUNTIME_URL", "")
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir) // cicord.yaml has spec.runtime.url

	var hits atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			hits.Add(1)
		}
		io.WriteString(w, `{}`)
	}))
	defer attacker.Close()
	// a pull request pointing cicord.yaml at its own server
	p := filepath.Join(dir, "cicord.yaml")
	writeFile(t, p, strings.Replace(readFile(t, p), "https://cicord.ma-commu.example", attacker.URL, 1))
	for _, args := range [][]string{{"plan", "--remote", dir}, {"status", dir}, {"sync", dir}} {
		code, _, errOut := run(t, args...)
		if code != 1 || !strings.Contains(errOut, "refusing to send the runtime token to a URL read from cicord.yaml") {
			t.Errorf("%v = %d: %s", args, code, errOut)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("the token was sent to the cicord.yaml URL")
	}

	// plain http is only allowed on loopback hosts
	for _, u := range []string{"http://cicord.example.com", "http://10.0.0.1:7420"} {
		code, _, errOut := run(t, "status", "--remote="+u, dir)
		if code != 1 || !strings.Contains(errOut, "plain http") {
			t.Errorf("%s = %d: %s", u, code, errOut)
		}
	}
	for _, u := range []string{"http://localhost:1", "http://127.0.0.1:1", "https://cicord.example.com", "http://[::1]:1"} {
		if _, err := checkRuntimeURL(u); err != nil {
			t.Errorf("%s refused: %v", u, err)
		}
	}
	if _, err := checkRuntimeURL("https://user:pw@cicord.example.com"); err == nil {
		t.Error("credentials in URL accepted")
	}

	// a redirect to another origin is not followed (the token stays home)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	code, _, errOut := run(t, "status", "--remote="+redirector.URL, dir)
	if code != 1 || !strings.Contains(errOut, "redirect") {
		t.Errorf("redirect = %d: %s", code, errOut)
	}
	// 127.0.0.1 vs localhost: different origins too
	local := strings.Replace(attacker.URL, "127.0.0.1", "localhost", 1)
	redirector2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, local+r.URL.Path, http.StatusFound)
	}))
	defer redirector2.Close()
	run(t, "sync", "--remote="+redirector2.URL, dir)
	if hits.Load() != 0 {
		t.Fatalf("the token followed a cross-origin redirect (%d hits)", hits.Load())
	}
}
