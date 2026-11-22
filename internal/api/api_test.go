package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/source"
	"github.com/npjonath/codi/internal/state/sqlite"
	"github.com/npjonath/codi/internal/writeback"
)

const (
	testToken  = "test-runtime-token"
	testSecret = "webhook-secret"
	serverKey  = "ma-commu"
)

type env struct {
	t     *testing.T
	world *sim.World
	rt    *runtime.Runtime
	repo  *config.Repo
	seed  *SimSeed
	dir   string
	ts    *httptest.Server
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			if info.Name() == "node_modules" || info.Name() == ".codi" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testLogger() *slog.Logger {
	if testing.Verbose() {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// start deploys examples/basic on the simulator behind the API.
func start(t *testing.T) *env { t.Helper(); return startWith(t, nil) }

// startWith is start with a hook to adjust the API options.
func startWith(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	return startFull(t, nil, nil, mod)
}

// startFull is start with hooks on the repository (before loading), the runtime options
// and the API options.
func startFull(t *testing.T, prepare func(dir string), rtMod func(*runtime.Options), mod func(*Options)) *env {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, "../../examples/basic", dir)
	if prepare != nil {
		prepare(dir)
	}
	repo, _ := config.Load(dir, config.LoadOptions{})
	if repo == nil || repo.Servers[serverKey] == nil {
		t.Fatal("cannot load examples/basic")
	}
	w := sim.NewWorld()
	seed, err := SeedSimWorld(w, repo)
	if err != nil {
		t.Fatal(err)
	}

	var handler http.Handler
	var hmu sync.RWMutex
	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hmu.RLock()
		h := handler
		hmu.RUnlock()
		h.ServeHTTP(rw, r)
	}))

	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	rtOpts := runtime.Options{
		Source: src, Connector: w, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { v, ok := seed.Secrets[n]; return v, ok },
		PortalURL: ts.URL, Logger: testLogger(), ResyncInterval: -1,
	}
	if rtMod != nil {
		rtMod(&rtOpts)
	}
	rt := runtime.New(rtOpts)
	opts := Options{
		Runtime: rt, Writer: &writeback.DirWriter{Root: dir}, Mode: "dev", Sim: w,
		RuntimeToken: testToken, WebhookSecret: testSecret, Logger: testLogger(), MemberCacheTTL: -1,
		AdminUI: fstest.MapFS{
			"index.html":    {Data: []byte("<html>admin</html>")},
			"assets/app.js": {Data: []byte("console.log('admin')")},
		},
		Portal: fstest.MapFS{"index.html": {Data: []byte("<html>portal</html>")}},
	}
	if mod != nil {
		mod(&opts)
	}
	h := New(opts)
	hmu.Lock()
	handler = h
	hmu.Unlock()

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
	e := &env{t: t, world: w, rt: rt, repo: repo, seed: seed, dir: dir, ts: ts}
	e.waitFor("first revision", func() bool { _, ok := rt.Revision(); return ok })
	return e
}

func (e *env) waitFor(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %s", what)
}

func (e *env) serverID() string { return e.repo.Servers[serverKey].Spec.DiscordID }

// client is an HTTP client with a cookie jar.
type client struct {
	e      *env
	hc     *http.Client
	bearer string
	noCSRF bool
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, hc: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type resp struct {
	status int
	body   []byte
	header http.Header
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r resp) errCode() string {
	var v struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(r.body, &v)
	return v.Error.Code
}

func (c *client) do(method, path string, body any, hdr ...string) resp {
	c.e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.e.ts.URL+path, rd)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !c.noCSRF {
		req.Header.Set("X-Codi-Request", "1")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, body: data, header: res.Header}
}

func (c *client) expect(status int, method, path string, body any) resp {
	c.e.t.Helper()
	r := c.do(method, path, body)
	if r.status != status {
		c.e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, r.status, status, r.body)
	}
	return r
}

func (c *client) login(userID string) meDTO {
	c.e.t.Helper()
	var me meDTO
	c.expect(200, "POST", "/auth/dev-login", map[string]string{"userId": userID}).json(c.e.t, &me)
	return me
}

// moderator creates a member with the "modo" role.
func (e *env) moderator() discord.User {
	e.t.Helper()
	u := e.world.CreateUser("modo-user")
	if err := e.world.AddMember(e.serverID(), u.ID); err != nil {
		e.t.Fatal(err)
	}
	role, ok := e.world.RoleByName(e.serverID(), e.repo.Servers[serverKey].Spec.Roles["modo"].Name)
	if !ok {
		e.t.Fatal("modo role not created")
	}
	if err := e.world.AdminAddMemberRole(e.serverID(), u.ID, role.ID); err != nil {
		e.t.Fatal(err)
	}
	return u
}

// ───────────────────────────────────────────────────────────── tests

func TestAuthMeStatus(t *testing.T) {
	e := start(t)
	c := e.client()
	if r := c.do("GET", "/api/v1/me", nil); r.status != 401 || r.errCode() != "unauthenticated" {
		t.Fatalf("anonymous /me = %d %s", r.status, r.body)
	}
	// CSRF: dev-login without the header is refused.
	c.noCSRF = true
	if r := c.do("POST", "/auth/dev-login", map[string]string{"userId": e.seed.Owner.ID}); r.status != 403 {
		t.Fatalf("dev-login without X-Codi-Request = %d %s", r.status, r.body)
	}
	c.noCSRF = false
	if r := c.do("POST", "/auth/dev-login", map[string]string{"userId": e.seed.Owner.ID}, "Origin", "https://evil.example"); r.status != 403 {
		t.Fatalf("cross-origin dev-login = %d %s", r.status, r.body)
	}

	me := c.login(e.seed.Owner.ID)
	if me.User.ID != e.seed.Owner.ID || len(me.Servers) != 1 || me.Servers[0].Key != serverKey || me.Servers[0].Role != "admin" {
		t.Fatalf("me = %+v", me)
	}
	var st runtime.Status
	c.expect(200, "GET", "/api/v1/status", nil).json(t, &st)
	if st.Mode != "dev" || !st.Simulator || st.Revision == nil || len(st.Servers) != 1 || st.Servers[0].Health != runtime.HealthHealthy {
		t.Fatalf("status = %+v", st)
	}
	if len(st.Bots) != 1 || len(st.Bots[0].Plugins) < 2 {
		t.Fatalf("bots = %+v", st.Bots)
	}
	var hist struct{ Items []syncRecordDTO }
	c.expect(200, "GET", "/api/v1/history?limit=10", nil).json(t, &hist)
	if len(hist.Items) == 0 {
		t.Fatal("empty history")
	}
	var detail map[string]any
	c.expect(200, "GET", "/api/v1/servers/"+serverKey, nil).json(t, &detail)
	if detail["spec"] == nil || detail["key"] != serverKey {
		t.Fatalf("server detail = %v", detail)
	}
	var res struct{ Roles, Categories, Channels []map[string]any }
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/resources", nil).json(t, &res)
	foundGeneral := false
	for _, ch := range res.Channels {
		if ch["key"] == "general" && ch["managed"] == true && ch["category"] == "community" {
			foundGeneral = true
		}
	}
	if !foundGeneral || len(res.Roles) == 0 {
		t.Fatalf("resources = %+v", res)
	}
	var plan map[string]any
	c.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plan", nil).json(t, &plan)
	if plan["server"] != serverKey {
		t.Fatalf("plan = %v", plan)
	}
	c.expect(200, "POST", "/api/v1/servers/"+serverKey+"/sync", nil)
	c.expect(404, "GET", "/api/v1/servers/nope", nil)

	// A plain member is not allowed on admin endpoints.
	alice := e.seed.Users[0]
	if err := e.world.AddMember(e.serverID(), alice.ID); err != nil {
		t.Fatal(err)
	}
	m := e.client()
	if me := m.login(alice.ID); len(me.Servers) != 1 || me.Servers[0].Role != "member" {
		t.Fatalf("alice me = %+v", me)
	}
	m.expect(403, "GET", "/api/v1/status", nil)
	m.expect(403, "GET", "/api/v1/servers/"+serverKey+"/plugins", nil)

	// Bearer token = admin.
	b := e.client()
	b.bearer = testToken
	b.expect(200, "GET", "/api/v1/status", nil)
	b.bearer = "wrong"
	b.expect(401, "GET", "/api/v1/status", nil)

	c.expect(204, "POST", "/auth/logout", nil)
	c.expect(401, "GET", "/api/v1/me", nil)
}

func TestPluginsList(t *testing.T) {
	e := start(t)
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	var list struct{ Items []pluginItemDTO }
	owner.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins", nil).json(t, &list)
	byName := map[string]pluginItemDTO{}
	for _, it := range list.Items {
		byName[it.Instance] = it
	}
	w, ok := byName["welcome"]
	if !ok || byName["onboarding"].Instance == "" {
		t.Fatalf("plugins = %+v", list.Items)
	}
	if w.Config["channel"] != "general" || w.Config["dmOnboarding"] != true || !strings.Contains(string(w.Schema), `"properties"`) {
		t.Fatalf("welcome = %+v", w)
	}
	// The schema keeps the file's property order (the generated form follows it).
	sch := string(w.Schema)
	if i, j := strings.Index(sch, `"channel"`), strings.Index(sch, `"message"`); i < 0 || j < 0 || i > j {
		t.Errorf("schema property order lost: %s", sch)
	}
	if w.BaseConfig["channel"] != "general" || w.Override == nil || w.Override["message"] == nil {
		t.Errorf("base/override = %v / %v", w.BaseConfig, w.Override)
	}
	if byName["onboarding"].Override != nil {
		t.Errorf("onboarding override = %v", byName["onboarding"].Override)
	}
	if strings.Join(w.Editable, ",") != "autoRole,channel,dmOnboarding,message" {
		t.Errorf("admin editable = %v", w.Editable)
	}
	if len(byName["onboarding"].Routes) == 0 {
		t.Errorf("onboarding routes missing")
	}

	mod := e.client()
	mod.login(e.moderator().ID)
	mod.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins", nil).json(t, &list)
	for _, it := range list.Items {
		if it.Instance == "welcome" && strings.Join(it.Editable, ",") != "message" {
			t.Errorf("moderator editable = %v", it.Editable)
		}
	}
}

func TestConfigPut(t *testing.T) {
	e := start(t)
	mod := e.client()
	mod.login(e.moderator().ID)
	var list struct{ Items []pluginItemDTO }
	mod.expect(200, "GET", "/api/v1/servers/"+serverKey+"/plugins", nil).json(t, &list)
	var cfg map[string]any
	for _, it := range list.Items {
		if it.Instance == "welcome" {
			cfg = it.Config
		}
	}
	path := "/api/v1/servers/" + serverKey + "/plugins/moderator/welcome/config"
	rev1, _ := e.rt.Revision()

	// Missing CSRF header.
	mod.noCSRF = true
	if r := mod.do("PUT", path, map[string]any{"config": cfg}); r.status != 403 {
		t.Fatalf("PUT without CSRF header = %d %s", r.status, r.body)
	}
	mod.noCSRF = false

	// Allowed field.
	cfg["message"] = "Hey {member}, welcome to {server}!"
	var out configPutResponse
	mod.expect(200, "PUT", path, map[string]any{"config": cfg, "scope": "server", "revision": rev1.ID}).json(t, &out)
	if out.Mode != "commit" {
		t.Fatalf("result = %+v", out)
	}
	data, _ := os.ReadFile(filepath.Join(e.dir, "bots/moderator/bot.yaml"))
	if !strings.Contains(string(data), "Hey {member}, welcome to {server}!") || !strings.Contains(string(data), "secretRef: DISCORD_TOKEN_MODERATOR") {
		t.Fatalf("bot.yaml not updated:\n%s", data)
	}
	if strings.Contains(string(data), "dmOnboarding: true\n          ") {
		t.Errorf("defaults leaked into the override:\n%s", data)
	}
	rev2, _ := e.rt.Revision()
	if rev2.SHA == rev1.SHA || out.Revision != rev2.ID {
		t.Fatalf("new revision not active: %s → %s (response %s)", rev1.ID, rev2.ID, out.Revision)
	}
	for _, in := range e.rt.Instances(serverKey) {
		if in.Config.Name == "welcome" && in.Config.Config["message"] != cfg["message"] {
			t.Fatalf("active config = %v", in.Config.Config)
		}
	}
	var audit struct{ Items []auditDTO }
	mod.expect(200, "GET", "/api/v1/servers/"+serverKey+"/audit", nil).json(t, &audit)
	if len(audit.Items) == 0 || audit.Items[0].Action != "config.update" || audit.Items[0].Target != "moderator/welcome" ||
		!strings.HasPrefix(audit.Items[0].Actor, "discord:") {
		t.Fatalf("audit = %+v", audit.Items)
	}

	// Stale revision.
	if r := mod.do("PUT", path, map[string]any{"config": cfg, "revision": rev1.ID}); r.status != 409 || r.errCode() != "conflict" {
		t.Fatalf("stale revision = %d %s", r.status, r.body)
	}

	// Forbidden field for a moderator.
	bad := config.CloneConfig(cfg)
	bad["channel"] = "rules"
	if r := mod.do("PUT", path, map[string]any{"config": bad}); r.status != 403 || !strings.Contains(string(r.body), "channel") {
		t.Fatalf("forbidden field = %d %s", r.status, r.body)
	}

	// Invalid config (as admin): unknown channel and wrong type.
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	inv := config.CloneConfig(cfg)
	inv["channel"] = "nowhere"
	inv["dmOnboarding"] = "yes"
	r := owner.do("PUT", path, map[string]any{"config": inv})
	if r.status != 422 {
		t.Fatalf("invalid config = %d %s", r.status, r.body)
	}
	var ie struct {
		Error struct {
			Code        string
			Diagnostics []config.Diagnostic
		}
	}
	r.json(t, &ie)
	if ie.Error.Code != "invalid" || len(ie.Error.Diagnostics) == 0 {
		t.Fatalf("diagnostics = %s", r.body)
	}
	inv["dmOnboarding"] = false
	r = owner.do("PUT", path, map[string]any{"config": inv})
	if r.status != 422 || !strings.Contains(string(r.body), `unknown channel \"nowhere\"`) {
		t.Fatalf("x-codi-ref check = %d %s", r.status, r.body)
	}

	// Admin edits the base config.
	base := config.CloneConfig(cfg)
	base["channel"] = "rules"
	owner.expect(200, "PUT", path, map[string]any{"config": base, "scope": "base"})
	data, _ = os.ReadFile(filepath.Join(e.dir, "bots/moderator/bot.yaml"))
	if !strings.Contains(string(data), "channel: rules") {
		t.Fatalf("base not written:\n%s", data)
	}
	t.Logf("bot.yaml after edits:\n%s", data)
}

func TestPortalLinkAndRoutes(t *testing.T) {
	e := start(t)
	alice := e.seed.Users[0]
	anon := e.client()
	sid := e.serverID()

	// Join through the simulator API.
	anon.expect(204, "POST", "/api/v1/sim/servers/"+sid+"/members", map[string]string{"userId": alice.ID})

	// The welcome message is visible in the channel.
	general := e.repo.Servers[serverKey].Spec.Channels["general"].Name
	var st struct {
		Servers []struct {
			ID       string
			Channels []struct{ ID, Name string }
			Members  []struct{ User userDTO }
		}
	}
	anon.expect(200, "GET", "/api/v1/sim/state", nil).json(t, &st)
	var chID string
	for _, c := range st.Servers[0].Channels {
		if c.Name == general {
			chID = c.ID
		}
	}
	if chID == "" || len(st.Servers[0].Members) < 3 {
		t.Fatalf("sim state = %+v", st)
	}
	e.waitFor("welcome message", func() bool {
		var msgs struct{ Items []messageDTO }
		anon.expect(200, "GET", "/api/v1/sim/channels/"+chID+"/messages", nil).json(t, &msgs)
		for _, m := range msgs.Items {
			if strings.Contains(m.Content, "<@"+alice.ID+">") {
				return true
			}
		}
		return false
	})

	// The DM carries a portal link with a signed token.
	var link string
	e.waitFor("onboarding DM", func() bool {
		var dms struct{ Items []messageDTO }
		anon.expect(200, "GET", "/api/v1/sim/users/"+alice.ID+"/dms", nil).json(t, &dms)
		for _, m := range dms.Items {
			for _, row := range m.Components {
				for _, c := range row.Components {
					if c.Style == "link" {
						link = c.URL
						return true
					}
				}
			}
		}
		return false
	})
	u, err := url.Parse(link)
	if err != nil || !strings.HasPrefix(link, e.ts.URL) {
		t.Fatalf("link = %s", link)
	}
	token := u.Query().Get("codi_token")

	routes := "/api/v1/portal/" + serverKey + "/plugins/moderator/onboarding"
	anon.expect(401, "GET", routes+"/steps", nil)
	anon.expect(401, "POST", "/auth/link", map[string]string{"token": token + "x"})

	c := e.client()
	var me meDTO
	c.expect(200, "POST", "/auth/link", map[string]string{"token": token}).json(t, &me)
	if me.User.ID != alice.ID || len(me.Servers) != 1 || me.Servers[0].Role != "member" {
		t.Fatalf("link me = %+v", me)
	}
	var pme map[string]any
	c.expect(200, "GET", "/api/v1/portal/"+serverKey+"/me", nil).json(t, &pme)
	if pme["role"] != "member" {
		t.Fatalf("portal me = %v", pme)
	}

	var steps struct {
		Steps []struct {
			ID       string
			Required bool
			Options  []struct{ Role string }
		}
	}
	c.expect(200, "GET", routes+"/steps", nil).json(t, &steps)
	if len(steps.Steps) == 0 {
		t.Fatal("no steps")
	}
	answers := map[string][]string{}
	for _, s := range steps.Steps {
		if len(s.Options) > 0 {
			answers[s.ID] = []string{s.Options[0].Role}
		}
	}
	c.noCSRF = true
	if r := c.do("POST", routes+"/complete", map[string]any{"answers": answers}); r.status != 403 {
		t.Fatalf("POST without CSRF header = %d %s", r.status, r.body)
	}
	c.noCSRF = false
	var done map[string]any
	c.expect(200, "POST", routes+"/complete", map[string]any{"answers": answers}).json(t, &done)
	if done["completed"] != true {
		t.Fatalf("complete = %v", done)
	}
	if r := c.do("POST", routes+"/complete", map[string]any{"answers": "nope"}); r.status != 400 {
		t.Fatalf("invalid answers = %d %s", r.status, r.body)
	}
	c.expect(404, "GET", routes+"/unknown", nil)

	// A link session is limited to the portal: no admin role elsewhere.
	c.expect(403, "GET", "/api/v1/status", nil)

	var portals struct{ Items []portalServerDTO }
	anon.expect(200, "GET", "/api/v1/portal/servers", nil).json(t, &portals)
	if len(portals.Items) != 1 || portals.Items[0].Theme.Primary == "" || len(portals.Items[0].Plugins) == 0 {
		t.Fatalf("portal servers = %+v", portals)
	}
	anon.expect(200, "GET", "/api/v1/portal/"+serverKey, nil)
}

func TestSimCommandsAndEvents(t *testing.T) {
	e := start(t)
	c := e.client()
	var cmds struct {
		Items []struct{ Bot, Name string }
	}
	c.expect(200, "GET", "/api/v1/sim/commands?serverId="+e.serverID(), nil).json(t, &cmds)
	found := false
	for _, cm := range cmds.Items {
		if cm.Name == "welcome-preview" && cm.Bot == "moderator" {
			found = true
		}
	}
	if !found {
		t.Fatalf("commands = %+v", cmds.Items)
	}
	general, _ := e.world.ChannelByName(e.serverID(), e.repo.Servers[serverKey].Spec.Channels["general"].Name)
	var res interactionResultDTO
	c.expect(200, "POST", "/api/v1/sim/commands", map[string]any{
		"userId": e.seed.Owner.ID, "serverId": e.serverID(), "channelId": general.ID, "bot": "moderator", "name": "welcome-preview",
	}).json(t, &res)
	if res.Status != "replied" || res.Response == nil || !res.Response.Ephemeral {
		t.Fatalf("interaction = %+v", res)
	}
	var sent messageDTO
	c.expect(201, "POST", "/api/v1/sim/messages", map[string]string{"userId": e.seed.Owner.ID, "channelId": general.ID, "content": "hello"}).json(t, &sent)
	if sent.Content != "hello" {
		t.Fatalf("sent = %+v", sent)
	}
	var u userDTO
	c.expect(201, "POST", "/api/v1/sim/users", map[string]string{"username": "dave"}).json(t, &u)
	c.expect(204, "POST", "/api/v1/sim/servers/"+e.serverID()+"/members", map[string]string{"userId": u.ID})
	c.expect(204, "DELETE", "/api/v1/sim/servers/"+e.serverID()+"/members/"+u.ID, nil)

	// SSE: an initial event, then one after a change.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/sim/events", nil)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if ct := res2.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %s", ct)
	}
	buf := make([]byte, 4096)
	var got strings.Builder
	readEvent := func() {
		for !strings.Contains(got.String(), "event: change") {
			n, err := res2.Body.Read(buf)
			if err != nil {
				t.Fatalf("sse read: %v (%q)", err, got.String())
			}
			got.Write(buf[:n])
		}
		got.Reset()
	}
	readEvent()
	e.world.CreateUser("eve")
	readEvent()
}

func TestGitHubWebhook(t *testing.T) {
	e := start(t)
	c := e.client()
	c.noCSRF = true
	sign := func(body []byte, secret string) string {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(body)
		return "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	push := []byte(`{"ref":"refs/heads/main"}`)
	if r := c.do("POST", "/hooks/github", push, "X-GitHub-Event", "push", "X-Hub-Signature-256", sign(push, "wrong")); r.status != 401 {
		t.Fatalf("bad signature = %d", r.status)
	}
	if r := c.do("POST", "/hooks/github", push, "X-GitHub-Event", "push"); r.status != 401 {
		t.Fatalf("no signature = %d", r.status)
	}
	if r := c.do("POST", "/hooks/github", push, "X-GitHub-Event", "push", "X-Hub-Signature-256", sign(push, testSecret)); r.status != 202 {
		t.Fatalf("push = %d %s", r.status, r.body)
	}
	other := []byte(`{"ref":"refs/heads/feature"}`)
	if r := c.do("POST", "/hooks/github", other, "X-GitHub-Event", "push", "X-Hub-Signature-256", sign(other, testSecret)); r.status != 200 || !strings.Contains(string(r.body), "ignored") {
		t.Fatalf("other branch = %d %s", r.status, r.body)
	}
	ping := []byte(`{"zen":"hi"}`)
	if r := c.do("POST", "/hooks/github", ping, "X-GitHub-Event", "ping", "X-Hub-Signature-256", sign(ping, testSecret)); r.status != 200 {
		t.Fatalf("ping = %d", r.status)
	}
	// Machine endpoints require the bearer token.
	c.noCSRF = false
	c.expect(401, "POST", "/api/v1/sync", nil)
	c.bearer = testToken
	c.expect(202, "POST", "/api/v1/sync", nil)
}

type tarEntry struct {
	name string
	typ  byte
	data string
	link string
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Size: int64(len(e.data)), Linkname: e.link}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if e.typ == tar.TypeDir {
			h.Mode = 0o755
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			_, _ = tw.Write([]byte(e.data))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func dirTarEntries(t *testing.T, dir, prefix string) []tarEntry {
	var out []tarEntry
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		name := prefix + filepath.ToSlash(rel)
		if info.IsDir() {
			out = append(out, tarEntry{name: name + "/", typ: tar.TypeDir})
			return nil
		}
		data, err := os.ReadFile(p)
		out = append(out, tarEntry{name: name, typ: tar.TypeReg, data: string(data)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPlanUpload(t *testing.T) {
	e := start(t)
	c := e.client()
	c.bearer = testToken
	post := func(body []byte) resp {
		return c.do("POST", "/api/v1/plan", body, "Content-Type", "application/gzip")
	}

	// The repo itself, inside a top-level directory, with a renamed channel.
	dir := t.TempDir()
	copyTree(t, e.dir, dir)
	p := filepath.Join(dir, "servers", serverKey+".yaml")
	data, _ := os.ReadFile(p)
	old := "name: " + e.repo.Servers[serverKey].Spec.Channels["general"].Name
	if !strings.Contains(string(data), old) {
		t.Fatalf("%q not in server yaml", old)
	}
	os.WriteFile(p, []byte(strings.Replace(string(data), old, "name: discussions", 1)), 0o644)
	r := post(makeTarGz(t, dirTarEntries(t, dir, "repo/")))
	if r.status != 200 {
		t.Fatalf("plan = %d %s", r.status, r.body)
	}
	var out struct {
		Valid       bool
		Diagnostics []config.Diagnostic
		Plans       []struct {
			Server  string
			Changes []struct{ Action, Kind, Key string }
		}
	}
	r.json(t, &out)
	if !out.Valid || len(out.Plans) != 1 || out.Plans[0].Server != serverKey {
		t.Fatalf("plan = %s", r.body)
	}
	renamed := false
	for _, ch := range out.Plans[0].Changes {
		if ch.Kind == "channel" && ch.Key == "general" && ch.Action == "update" {
			renamed = true
		}
	}
	if !renamed {
		t.Errorf("rename not planned: %s", r.body)
	}
	// Nothing was applied.
	if _, ok := e.world.ChannelByName(e.serverID(), "discussions"); ok {
		t.Fatal("plan applied changes")
	}

	// Invalid repository: diagnostics, no plans.
	os.WriteFile(filepath.Join(dir, "servers", serverKey+".yaml"), []byte("kind: Server\n"), 0o644)
	r = post(makeTarGz(t, dirTarEntries(t, dir, "")))
	r.json(t, &out)
	if r.status != 200 || out.Valid || len(out.Diagnostics) == 0 || len(out.Plans) != 0 {
		t.Fatalf("invalid plan = %d %s", r.status, r.body)
	}

	// Unsafe archives.
	for name, entries := range map[string][]tarEntry{
		"traversal": {{name: "../evil.txt", typ: tar.TypeReg, data: "x"}},
		"absolute":  {{name: "/etc/evil", typ: tar.TypeReg, data: "x"}},
		"symlink":   {{name: "link", typ: tar.TypeSymlink, link: "/etc/passwd"}},
		"hardlink":  {{name: "hard", typ: tar.TypeLink, link: "codi.yaml"}},
		"nested":    {{name: "a/../../evil", typ: tar.TypeReg, data: "x"}},
	} {
		if r := post(makeTarGz(t, entries)); r.status != 400 || r.errCode() != "invalid" {
			t.Errorf("%s: status %d %s", name, r.status, r.body)
		}
	}
	if r := post([]byte("not gzip")); r.status != 400 {
		t.Errorf("garbage: %d", r.status)
	}
	c.bearer = ""
	if r := post(makeTarGz(t, nil)); r.status != 401 {
		t.Errorf("no bearer: %d", r.status)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(os.TempDir()), "evil.txt")); err == nil {
		t.Error("traversal wrote a file")
	}
}

func TestStaticAndHealth(t *testing.T) {
	e := start(t)
	c := e.client()
	if r := c.do("GET", "/healthz", nil); r.status != 200 || strings.TrimSpace(string(r.body)) != "ok" {
		t.Fatalf("healthz = %d %s", r.status, r.body)
	}
	c.expect(200, "GET", "/readyz", nil)
	if r := c.expect(200, "GET", "/admin/servers/ma-commu/plugins", nil); string(r.body) != "<html>admin</html>" || r.header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("admin SPA fallback = %s", r.body)
	}
	if r := c.expect(200, "GET", "/admin/assets/app.js", nil); !strings.Contains(string(r.body), "console.log") {
		t.Fatalf("asset = %s", r.body)
	}
	c.expect(404, "GET", "/admin/assets/missing.js", nil)
	if r := c.expect(301, "GET", "/admin", nil); r.header.Get("Location") != "/admin/" {
		t.Fatalf("redirect = %v", r.header)
	}
	if r := c.expect(200, "GET", "/onboarding?server=ma-commu", nil); string(r.body) != "<html>portal</html>" {
		t.Fatalf("portal fallback = %s", r.body)
	}
	for _, p := range []string{"/api/v1/nope", "/api/nope", "/auth/nope", "/hooks/nope"} {
		if r := c.do("GET", p, nil); r.status != 404 || r.errCode() != "not_found" {
			t.Errorf("%s = %d %s", p, r.status, r.body)
		}
	}
}

func TestOAuthLogin(t *testing.T) {
	discordAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "shh" || r.Form.Get("client_id") != "42" {
				http.Error(w, "bad", 400)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
		case "/users/@me":
			if r.Header.Get("Authorization") != "Bearer at" {
				http.Error(w, "no", 401)
				return
			}
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
	h := New(Options{Store: store, Logger: testLogger(), OAuth: OAuth{ClientID: "42", ClientSecret: "shh",
		AuthorizeURL: discordAPI.URL + "/oauth2/authorize", TokenURL: discordAPI.URL + "/oauth2/token", APIURL: discordAPI.URL}})
	ts := httptest.NewServer(h)
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res, err := hc.Get(ts.URL + "/auth/login?redirect=" + url.QueryEscape("//evil.example/x"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != 302 || loc.Query().Get("scope") != "identify" || loc.Query().Get("client_id") != "42" {
		t.Fatalf("login redirect = %d %s", res.StatusCode, loc)
	}
	state := loc.Query().Get("state")

	// Wrong state (CSRF) is refused.
	res, _ = hc.Get(ts.URL + "/auth/callback?code=good-code&state=forged")
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("forged state = %d", res.StatusCode)
	}
	res, err = hc.Get(ts.URL + "/auth/callback?code=good-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/" {
		t.Fatalf("callback = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = hc.Get(ts.URL + "/api/v1/me")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"id":"777"`) {
		t.Fatalf("me after oauth = %d %s", res.StatusCode, body)
	}
	// The state is single-use.
	res, _ = hc.Get(ts.URL + "/auth/callback?code=good-code&state=" + url.QueryEscape(state))
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("replayed state = %d", res.StatusCode)
	}
}
