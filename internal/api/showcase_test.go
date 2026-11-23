package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/source"
	"github.com/npjonath/codi/internal/state/sqlite"
	"github.com/npjonath/codi/internal/writeback"
)

// TestShowcaseRouteAccess drives the plugin routes of examples/showcase through the HTTP
// API, which enforces each route's access level (spec/api.md §6): public, member,
// moderator and admin routes, for anonymous, member, moderator and admin viewers.
// internal/runtime/showcase_test.go covers the plugins themselves.
func TestShowcaseRouteAccess(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, "../../examples/showcase", dir)
	repo, ds := config.Load(dir, config.LoadOptions{})
	if repo == nil || ds.HasErrors() {
		t.Fatalf("cannot load examples/showcase: %v", ds)
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
	rt := runtime.New(runtime.Options{
		Source: src, Connector: w, Store: store, Mode: "dev", Simulator: true,
		Secrets:   func(n string) (string, bool) { v, ok := seed.Secrets[n]; return v, ok },
		PortalURL: ts.URL, Logger: testLogger(), ResyncInterval: -1,
	})
	h := New(Options{
		Runtime: rt, Writer: &writeback.DirWriter{Root: dir}, Mode: "dev", Sim: w,
		RuntimeToken: testToken, Logger: testLogger(), MemberCacheTTL: -1,
		AdminUI: fstest.MapFS{"index.html": {Data: []byte("admin")}},
		Portal:  fstest.MapFS{"index.html": {Data: []byte("portal")}},
	})
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

	sid := repo.Servers["showcase"].Spec.DiscordID
	join := func(name string, roles ...string) string {
		u := w.CreateUser(name)
		if err := w.AddMember(sid, u.ID); err != nil {
			t.Fatal(err)
		}
		for _, key := range roles {
			r, ok := w.RoleByName(sid, repo.Servers["showcase"].Spec.Roles[key].Name)
			if !ok {
				t.Fatalf("role %s not created", key)
			}
			if err := w.AdminAddMemberRole(sid, u.ID, r.ID); err != nil {
				t.Fatal(err)
			}
		}
		return u.ID
	}
	viewers := map[string]*client{"anonymous": e.client()}
	for name, roles := range map[string][]string{"member": {"member"}, "moderator": {"mod"}, "admin": {"admin"}} {
		c := e.client()
		c.login(join(name, roles...))
		viewers[name] = c
	}
	outsider := e.client()
	outsider.login(w.CreateUser("outsider").ID) // not a member of the server
	viewers["outsider"] = outsider

	base := "/api/v1/portal/showcase/plugins/codi/"
	routes := []struct {
		method, path string
		want         map[string]int // viewer → status
	}{
		{"GET", "levels/leaderboard", map[string]int{"anonymous": 200, "outsider": 200, "member": 200, "moderator": 200, "admin": 200}},
		{"GET", "polls/polls/1", map[string]int{"anonymous": 404, "member": 404}}, // public route, unknown poll
		{"GET", "levels/me", map[string]int{"anonymous": 401, "outsider": 403, "member": 200, "moderator": 200}},
		{"GET", "tickets/tickets", map[string]int{"anonymous": 401, "member": 403, "moderator": 200, "admin": 200}},
		{"GET", "moderation/warnings/1", map[string]int{"member": 403, "moderator": 200}},
		{"DELETE", "moderation/warnings/1", map[string]int{"anonymous": 401, "member": 403, "moderator": 403, "admin": 200}},
	}
	for _, r := range routes {
		for viewer, want := range r.want {
			res := viewers[viewer].do(r.method, base+r.path, nil)
			if res.status != want {
				t.Errorf("%s %s as %s = %d, want %d: %s", r.method, r.path, viewer, res.status, want, res.body)
			}
		}
	}
	res := viewers["anonymous"].do("GET", base+"levels/leaderboard", nil)
	if !strings.Contains(string(res.body), `"items":[]`) {
		t.Errorf("empty leaderboard = %s", res.body)
	}

	// Dot segments in {path...} never reach another instance's route: encoded ones are
	// literal parameter values (no such poll), literal ones are answered by the mux with
	// a redirect to the cleaned URL, never by the polls instance.
	for _, p := range []string{
		"polls/polls/%2E%2E%2F%2E%2E%2Flevels%2Fleaderboard",
		"polls/polls/%2e%2e",
		"polls/polls/%2E%2E/%2E%2E/levels/leaderboard",
		"polls/polls/..%2F..%2Flevels%2Fleaderboard",
	} {
		res := viewers["admin"].do("GET", base+p, nil)
		if res.status != http.StatusNotFound || strings.Contains(string(res.body), "items") {
			t.Errorf("GET %s = %d %s, want 404 (a literal poll id)", p, res.status, res.body)
		}
	}
	for _, p := range []string{"polls/polls/../../levels/leaderboard", "polls/../levels/leaderboard"} {
		res := viewers["admin"].do("GET", base+p, nil)
		if res.status < 300 || res.status >= 400 || strings.Contains(string(res.body), "items") {
			t.Errorf("GET %s = %d %s, want a redirect to the cleaned path", p, res.status, res.body)
		}
	}
}
