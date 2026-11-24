package gitauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
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

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// The test key is generated at test time: never commit a PEM file (secret scanners).
var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

func pkcs1PEM(t *testing.T) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA " + "PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key(t))})
}

func pkcs8PEM(t *testing.T) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key(t))
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE " + "KEY", Bytes: der})
}

// fakeGitHub is the App part of the GitHub API: it verifies the JWT of every request.
type fakeGitHub struct {
	t         *testing.T
	pub       *rsa.PublicKey
	appID     string
	now       func() time.Time
	ttl       time.Duration
	exchanges atomic.Int32
	lookups   atomic.Int32
	lastBody  atomic.Value // string
	installed map[string]int64
	srv       *httptest.Server
}

func newFakeGitHub(t *testing.T, now func() time.Time) *fakeGitHub {
	f := &fakeGitHub{t: t, pub: &key(t).PublicKey, appID: "4242", now: now, ttl: time.Hour,
		installed: map[string]int64{"acme/community": 77}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		if !f.checkJWT(w, r) {
			return
		}
		f.lookups.Add(1)
		id, ok := f.installed[r.PathValue("owner")+"/"+r.PathValue("repo")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if !f.checkJWT(w, r) {
			return
		}
		if r.PathValue("id") != "77" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.lastBody.Store(string(body))
		n := f.exchanges.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": fmt.Sprintf("inst-token-%d", n),
			"expires_at": f.now().Add(f.ttl).UTC().Format(time.RFC3339)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// checkJWT verifies the RS256 signature with the public key and the iat/exp/iss claims.
func (f *fakeGitHub) checkJWT(w http.ResponseWriter, r *http.Request) bool {
	fail := func(msg string) bool {
		f.t.Errorf("JWT: %s", msg)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"A JSON web token could not be decoded"}`)
		return false
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return fail("no bearer")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return fail("not three parts")
	}
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if string(hdr) != `{"alg":"RS256","typ":"JWT"}` {
		return fail("header " + string(hdr))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fail("signature encoding")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(f.pub, crypto.SHA256, sum[:], sig); err != nil {
		return fail("bad signature")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return fail("claims " + string(raw))
	}
	now := f.now().Unix()
	if c.Iss != f.appID || c.Iat > now-30 || c.Iat < now-120 || c.Exp <= now || c.Exp-c.Iat > 600 {
		return fail(fmt.Sprintf("claims iat=%d exp=%d iss=%q (now %d)", c.Iat, c.Exp, c.Iss, now))
	}
	if r.Header.Get("Accept") != "application/vnd.github+json" {
		return fail("accept header")
	}
	return true
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestGitHubAppTokenAndRefresh(t *testing.T) {
	clk := &clock{t: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)}
	f := newFakeGitHub(t, clk.now)
	app, err := NewGitHubApp(AppConfig{AppID: "4242", InstallationID: "77", PrivateKey: pkcs1PEM(t), Repo: "acme/community", API: f.srv.URL, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tok, err := app.Token(ctx)
	if err != nil || tok != "inst-token-1" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if f.lookups.Load() != 0 {
		t.Fatal("a configured installation must not be looked up")
	}
	if b, _ := f.lastBody.Load().(string); b != `{"repositories":["community"]}` {
		t.Fatalf("token scope = %q (always the configuration repository)", b)
	}
	clk.add(54 * time.Minute) // 6 minutes left: cached
	if tok, _ := app.Token(ctx); tok != "inst-token-1" {
		t.Fatalf("cached token = %q", tok)
	}
	clk.add(2 * time.Minute) // 4 minutes left: renewed
	if tok, _ := app.Token(ctx); tok != "inst-token-2" {
		t.Fatalf("renewed token = %q", tok)
	}
	if n := f.exchanges.Load(); n != 2 {
		t.Fatalf("exchanges = %d", n)
	}
}

func TestGitHubAppDiscoveryAndScope(t *testing.T) {
	clk := &clock{t: time.Now()}
	f := newFakeGitHub(t, clk.now)
	app, err := NewGitHubApp(AppConfig{AppID: "4242", PrivateKey: pkcs8PEM(t), Repo: "acme/community", API: f.srv.URL, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := app.InstallationID(ctx)
	if err != nil || id != 77 {
		t.Fatalf("installation = %d, %v", id, err)
	}
	if _, err := app.Token(ctx); err != nil {
		t.Fatal(err)
	}
	if f.lookups.Load() != 1 {
		t.Fatalf("lookups = %d (the discovered installation is cached)", f.lookups.Load())
	}
	if b, _ := f.lastBody.Load().(string); b != `{"repositories":["community"]}` {
		t.Fatalf("token scope = %q", b)
	}

	other, _ := NewGitHubApp(AppConfig{AppID: "4242", PrivateKey: pkcs8PEM(t), Repo: "acme/elsewhere", API: f.srv.URL, Now: clk.now})
	if _, err := other.Token(ctx); !errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), "acme/elsewhere") {
		t.Fatalf("not installed: %v", err)
	}
	wrong, _ := NewGitHubApp(AppConfig{AppID: "4242", InstallationID: "78", PrivateKey: pkcs8PEM(t), Repo: "acme/community", API: f.srv.URL, Now: clk.now})
	if _, err := wrong.Token(ctx); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("unknown installation: %v", err)
	}
}

func TestGitHubAppRejectedJWT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
	}))
	defer srv.Close()
	app, err := NewGitHubApp(AppConfig{AppID: "1", InstallationID: "77", PrivateKey: pkcs1PEM(t), Repo: "acme/community", API: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.Token(context.Background())
	if !errors.Is(err, ErrJWTRejected) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "BEGIN") {
		t.Fatal("the error leaks the key")
	}
}

func TestGitHubAppConcurrentTokens(t *testing.T) {
	clk := &clock{t: time.Now()}
	f := newFakeGitHub(t, clk.now)
	app, err := NewGitHubApp(AppConfig{AppID: "4242", PrivateKey: pkcs1PEM(t), Repo: "acme/community", API: f.srv.URL, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	toks := make([]string, 32)
	errs := make([]error, 32)
	for i := range toks {
		wg.Go(func() { toks[i], errs[i] = app.Token(context.Background()) })
	}
	wg.Wait()
	for i := range toks {
		if errs[i] != nil || toks[i] != "inst-token-1" {
			t.Fatalf("token %d = %q, %v", i, toks[i], errs[i])
		}
	}
	if f.exchanges.Load() != 1 || f.lookups.Load() != 1 {
		t.Fatalf("exchanges = %d, lookups = %d (concurrent callers share one)", f.exchanges.Load(), f.lookups.Load())
	}
	// A waiting caller honors its context.
	app.sem <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := app.Token(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked caller = %v", err)
	}
	<-app.sem
}

func TestParsePrivateKey(t *testing.T) {
	for name, data := range map[string][]byte{
		"pkcs1":   pkcs1PEM(t),
		"pkcs8":   pkcs8PEM(t),
		"escaped": []byte(strings.ReplaceAll(strings.TrimSpace(string(pkcs1PEM(t))), "\n", `\n`)),
		"spaces":  append(append([]byte("  \n"), pkcs8PEM(t)...), "\n\n"...),
	} {
		k, err := ParsePrivateKey(data)
		if err != nil || !k.Equal(key(t)) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ParsePrivateKey([]byte("not a key")); err == nil || !strings.Contains(err.Error(), "not PEM") {
		t.Errorf("garbage: %v", err)
	}
	if _, err := ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE " + "KEY", Bytes: []byte("junk")})); err == nil {
		t.Error("junk DER accepted")
	}
}

func TestNewGitHubAppValidation(t *testing.T) {
	k := pkcs1PEM(t)
	cases := map[string]AppConfig{
		"no app id":       {PrivateKey: k, Repo: "a/b"},
		"bad app id":      {AppID: "12 34", PrivateKey: k, Repo: "a/b"},
		"bad install":     {AppID: "1", InstallationID: "abc", PrivateKey: k},
		"no repo/install": {AppID: "1", PrivateKey: k},
		"bad repo":        {AppID: "1", PrivateKey: k, Repo: "a/b/c"},
	}
	for name, cfg := range cases {
		if _, err := NewGitHubApp(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestResolve(t *testing.T) {
	k := string(pkcs1PEM(t))
	env := func(m map[string]string) func(string) (string, bool) {
		return func(n string) (string, bool) { v, ok := m[n]; return v, ok }
	}
	ts, m, err := Resolve(Options{Lookup: env(nil)})
	if ts != nil || m != MethodNone || err != nil {
		t.Fatalf("nothing = %v %q %v", ts, m, err)
	}
	ts, m, err = Resolve(Options{Lookup: env(map[string]string{TokenSecret: " pat \n"})})
	if err != nil || m != MethodToken {
		t.Fatalf("token = %q %v", m, err)
	}
	if tok, _ := ts.Token(context.Background()); tok != "pat" {
		t.Fatalf("token value = %q", tok)
	}
	ts, m, err = Resolve(Options{Lookup: env(map[string]string{"OTHER": "x"}), TokenSecret: "OTHER"})
	if err != nil || m != MethodToken || ts == nil {
		t.Fatalf("token ref = %q %v", m, err)
	}
	// The App takes precedence over the token.
	ts, m, err = Resolve(Options{Lookup: env(map[string]string{TokenSecret: "pat", AppIDSecret: "4242", AppPrivateKeySecret: k}), Repo: "acme/community"})
	if err != nil || m != MethodApp {
		t.Fatalf("app = %q %v", m, err)
	}
	if _, ok := ts.(*GitHubApp); !ok {
		t.Fatalf("app source = %T", ts)
	}
	// Partial configurations are errors that name what is missing.
	for _, vals := range []map[string]string{
		{AppIDSecret: "4242"},
		{AppPrivateKeySecret: k},
		{AppInstallationIDSecret: "77", TokenSecret: "pat"},
	} {
		if _, _, err := Resolve(Options{Lookup: env(vals), Repo: "acme/community"}); err == nil || !strings.Contains(err.Error(), "GitHub App") {
			t.Errorf("%v: %v", keys(vals), err)
		}
	}
	// No repository on github.com: an App is refused, even with an installation ID.
	if _, _, err := Resolve(Options{Lookup: env(map[string]string{AppIDSecret: "4242", AppPrivateKeySecret: k, AppInstallationIDSecret: "77"})}); err == nil ||
		!strings.Contains(err.Error(), "github.com") {
		t.Errorf("no repo: %v", err)
	}
	// The key file takes precedence over the secret.
	p := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(p, pkcs8PEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, m, err := Resolve(Options{Lookup: env(map[string]string{AppIDSecret: "4242", AppPrivateKeySecret: "garbage"}), KeyFile: p, Repo: "a/b"}); err != nil || m != MethodApp {
		t.Errorf("key file = %q %v", m, err)
	}
	if _, _, err := Resolve(Options{Lookup: env(map[string]string{AppIDSecret: "4242"}), KeyFile: p + ".missing", Repo: "a/b"}); err == nil {
		t.Error("missing key file accepted")
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGitAuthAndBearer(t *testing.T) {
	ctx := context.Background()
	if a, err := GitAuth(ctx, nil); a != nil || err != nil {
		t.Fatalf("nil source = %v %v", a, err)
	}
	if Static("  ") != nil {
		t.Fatal("an empty static token must be nil (anonymous)")
	}
	a, err := GitAuth(ctx, Static("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if ba, ok := a.(*githttp.BasicAuth); !ok || ba.Username != "x-access-token" || ba.Password != "abc" {
		t.Fatalf("auth = %#v", a)
	}
	if h, _ := Bearer(ctx, Static("abc")); h != "Bearer abc" {
		t.Fatalf("bearer = %q", h)
	}
	if h, err := Bearer(ctx, nil); h != "" || err != nil {
		t.Fatalf("nil bearer = %q %v", h, err)
	}
}

func TestCommitStatuses(t *testing.T) {
	type post struct {
		path, auth string
		body       map[string]string
	}
	var (
		mu    sync.Mutex
		posts []post
		code  = http.StatusCreated
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		posts = append(posts, post{r.Method + " " + r.URL.Path, r.Header.Get("Authorization"), b})
		c := code
		mu.Unlock()
		w.WriteHeader(c)
		_, _ = io.WriteString(w, `{"message":"Resource not accessible by integration"}`)
	}))
	defer srv.Close()
	var logs strings.Builder
	cs := &CommitStatuses{Repo: "acme/community", Tokens: Static("pat"), TargetURL: "https://cicord.example.org/admin/",
		API: srv.URL, Logger: slogTo(&logs)}
	sha := strings.Repeat("ab", 20)
	ctx := context.Background()
	cs.Report(ctx, sha, StatePending, "Applying")
	long := "rejected: " + strings.Repeat("é", 300) + "\nsecond line"
	cs.Report(ctx, sha, StateFailure, long)
	mu.Lock()
	if len(posts) != 2 {
		t.Fatalf("posts = %+v", posts)
	}
	p := posts[0]
	if p.path != "POST /repos/acme/community/statuses/"+sha || p.auth != "Bearer pat" || p.body["state"] != "pending" ||
		p.body["context"] != "cicord/sync" || p.body["target_url"] != "https://cicord.example.org/admin/" {
		t.Fatalf("post = %+v", p)
	}
	if d := posts[1].body["description"]; len([]rune(d)) != 140 || strings.Contains(d, "second") || !strings.HasSuffix(d, "…") {
		t.Fatalf("description (%d runes) = %q", len([]rune(d)), d)
	}
	code = http.StatusForbidden
	mu.Unlock()
	// Failures are logged with a permission hint, never returned or fatal.
	cs.Report(ctx, sha, StateSuccess, "ok")
	if !strings.Contains(logs.String(), "Commit statuses") || strings.Contains(logs.String(), "Bearer") {
		t.Fatalf("log = %s", logs.String())
	}
	// The next permission errors are not repeated as warnings.
	cs.Report(ctx, sha, StateSuccess, "ok")
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 {
		t.Fatalf("%d warnings: %s", n, logs.String())
	}
	// A token error is logged too.
	(&CommitStatuses{Repo: "acme/community", Tokens: failing{}, API: srv.URL, Logger: slogTo(&logs)}).Report(ctx, sha, StateSuccess, "ok")
	if !strings.Contains(logs.String(), "no token today") {
		t.Fatalf("log = %s", logs.String())
	}
	// Without a token source or a repository, nothing is posted.
	mu.Lock()
	n := len(posts)
	mu.Unlock()
	(&CommitStatuses{Repo: "acme/community", API: srv.URL}).Report(ctx, sha, StateSuccess, "ok")
	var nilCS *CommitStatuses
	nilCS.Report(ctx, sha, StateSuccess, "ok")
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != n {
		t.Fatal("posted without a token source")
	}
}

func slogTo(w io.Writer) *slog.Logger { return slog.New(slog.NewTextHandler(w, nil)) }

type failing struct{}

func (failing) Token(context.Context) (string, error) { return "", errors.New("no token today") }

// TestCommitStatusesAsync: the queued reporter returns at once (even while GitHub is slow)
// and posts the states of a revision in order.
func TestCommitStatusesAsync(t *testing.T) {
	release := make(chan struct{})
	var (
		mu     sync.Mutex
		states []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		states = append(states, b["state"])
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	cs := &CommitStatuses{Repo: "acme/community", Tokens: Static("pat"), API: srv.URL, Timeout: 5 * time.Second}
	report := cs.Async(8)
	sha := strings.Repeat("cd", 20)
	start := time.Now()
	report(context.Background(), sha, StatePending, "Applying")
	report(context.Background(), sha, StateSuccess, "Applied to 1 server(s)")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Async blocked for %s", d)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), states...)
		mu.Unlock()
		if len(got) == 2 {
			if got[0] != StatePending || got[1] != StateSuccess {
				t.Fatalf("states = %v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("states = %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestGitHubAppNeedsGitHubRepository: an App is never built without the github.com
// repository its tokens are scoped to (a token for every repository of the installation,
// sent to another git host, would leak far more than the configuration repository).
func TestGitHubAppNeedsGitHubRepository(t *testing.T) {
	for _, repo := range []string{"", "  ", "/"} {
		if _, err := NewGitHubApp(AppConfig{AppID: "4242", InstallationID: "77", PrivateKey: pkcs8PEM(t), Repo: repo}); err == nil ||
			!strings.Contains(err.Error(), "github.com") {
			t.Errorf("repo %q: err = %v", repo, err)
		}
	}
}
