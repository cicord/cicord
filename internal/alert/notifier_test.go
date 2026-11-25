package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/source"
)

type recorder struct {
	mu   sync.Mutex
	msgs []Message
	err  error
}

func (r *recorder) Send(_ context.Context, msgs []Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.msgs = append(r.msgs, msgs...)
	return nil
}

func (r *recorder) all() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.msgs...)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)} }
func quiet() *slog.Logger                { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func states(ms []Message) (out []string) {
	for _, m := range ms {
		out = append(out, m.State+" "+m.Title)
	}
	return out
}

func healthyStatus() runtime.Status {
	return runtime.Status{
		Revision: &source.Revision{ID: "abc1234"},
		Servers:  []runtime.ServerStatus{{Key: "showcase", Name: "Cicord Showcase", Sync: runtime.SyncSynced, Health: runtime.HealthHealthy, Bot: "cicord"}},
		Bots: []runtime.BotStatus{{Key: "cicord", Health: runtime.HealthHealthy, Plugins: []runtime.PluginStatus{
			{Instance: "tickets", Plugin: "tickets", Version: "1.2.0", Server: "showcase", Health: runtime.HealthHealthy},
		}}},
	}
}

func degradedPlugin(msg string) runtime.Status {
	st := healthyStatus()
	st.Bots[0].Health, st.Bots[0].Message = runtime.HealthDegraded, "a plugin instance is degraded"
	st.Bots[0].Plugins[0].Health, st.Bots[0].Plugins[0].Message = runtime.HealthDegraded, msg
	return st
}

func newTestNotifier(c *clock, r *recorder) *Notifier {
	n := New(Options{Sender: r, PublicURL: "https://community.example.org", AdminURL: "https://cicord.tailnet.ts.net",
		Logger: quiet(), Now: c.now, ResolveAfter: 2 * time.Minute})
	n.revision = func() string { return testRevision }
	return n
}

// testRevision is the active revision seen by the handler hook of newTestNotifier.
var testRevision = "abc1234"

func TestPluginDegradedOnceThenResolved(t *testing.T) {
	c, r := newClock(), &recorder{}
	n := newTestNotifier(c, r)
	in := config.Instance{Bot: "cicord", Name: "tickets", Server: "showcase"}
	n.Evaluate(healthyStatus())
	if len(n.Pending()) != 0 {
		t.Fatalf("healthy runtime queued %v", states(n.Pending()))
	}
	// A crash loop: Degraded, Healthy, Degraded… with handler failures in between.
	for i := range 5 {
		if i == 0 {
			n.ObserveHandler(in, plugin.HandlerResult{Kind: "modal", Name: "open", Err: &plugin.ScriptError{Name: "Error", Message: "secret user text"}})
		}
		n.Evaluate(degradedPlugin("Error: secret user text\n    at open (src/index.ts:12)"))
		c.advance(20 * time.Second)
		if i < 4 {
			n.Evaluate(healthyStatus())
			c.advance(20 * time.Second)
		}
	}
	n.Flush(context.Background())
	got := r.all()
	if len(got) != 1 || got[0].State != StateFiring {
		t.Fatalf("crash loop sent %v, want one firing alert", states(got))
	}
	m := got[0]
	if m.Runtime != "community.example.org" || m.Server != "showcase" || m.ServerName != "Cicord Showcase" || m.Bot != "cicord" ||
		m.Instance != "tickets" || m.Plugin != "tickets@1.2.0" || m.Workflow != "modal open" ||
		m.Link != "https://cicord.tailnet.ts.net/admin/servers/showcase/plugins" {
		t.Errorf("alert = %+v", m)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), "secret user text") || strings.Contains(string(b), "index.ts") {
		t.Errorf("the plugin's error text leaked into the alert: %s", b)
	}
	// Healthy for less than ResolveAfter: still open.
	n.Evaluate(healthyStatus())
	c.advance(time.Minute)
	n.Evaluate(healthyStatus())
	if len(n.Pending()) != 0 {
		t.Fatalf("resolved too early: %v", states(n.Pending()))
	}
	c.advance(90 * time.Second)
	n.Evaluate(healthyStatus())
	n.Evaluate(healthyStatus())
	n.Flush(context.Background())
	got = r.all()
	if len(got) != 2 || got[1].State != StateResolved || got[1].Key != got[0].Key || got[1].Workflow != "modal open" {
		t.Fatalf("messages = %v, want firing then one resolved", states(got))
	}
	if got[0].Title != "Plugin instance degraded" || !strings.Contains(got[0].Detail, "uncaught Error") {
		t.Errorf("firing = %+v", got[0])
	}
	if !strings.Contains(got[1].Detail, "failed again 4 times") {
		t.Errorf("resolved detail = %q", got[1].Detail)
	}
}

func TestServerRevisionPauseConditions(t *testing.T) {
	c, r := newClock(), &recorder{}
	n := newTestNotifier(c, r)
	st := healthyStatus()
	st.LastError = "revision def5678 rejected: servers/showcase.yaml: roles[0].name: required"
	st.Servers[0].Health, st.Servers[0].Message = runtime.HealthDegraded, "sync: 50013 Missing Permissions on https://bot:hunter2@example.com/x?token=abc"
	n.Evaluate(st)
	// A paused server, decoded from the JSON form of the status (the field is added by the
	// runtime): simulated by a status whose JSON carries "paused".
	n.evaluate(withPause(t, st, `{"by":"discord:1","byName":"alice","at":"2026-11-25T12:00:00Z","reason":"migration"}`))
	n.Flush(context.Background())
	got := r.all()
	want := []string{"firing Revision rejected", "firing Server sync failing", "firing Server paused"}
	if fmt.Sprint(states(got)) != fmt.Sprint(want) {
		t.Fatalf("messages = %v, want %v", states(got), want)
	}
	if got[0].Revision != "def5678" || !strings.Contains(got[0].Detail, "previous revision keeps running") {
		t.Errorf("revision alert = %+v", got[0])
	}
	if d := got[1].Detail; strings.Contains(d, "hunter2") || strings.Contains(d, "token=abc") || got[1].Workflow != "sync" || got[1].Bot != "cicord" {
		t.Errorf("server alert = %+v", got[1])
	}
	if !strings.Contains(got[2].Detail, "alice") || !strings.Contains(got[2].Detail, "migration") {
		t.Errorf("pause alert = %+v", got[2])
	}
	// Resume and a good revision resolve at once; the server waits for ResolveAfter.
	c.advance(10 * time.Second)
	n.Evaluate(healthyStatus())
	n.Flush(context.Background())
	got = r.all()[3:]
	if fmt.Sprint(states(got)) != fmt.Sprint([]string{"resolved Revision active again", "resolved Server resumed"}) {
		t.Fatalf("after recovery: %v", states(got))
	}
	c.advance(3 * time.Minute)
	n.Evaluate(healthyStatus())
	n.Flush(context.Background())
	if got := r.all()[5:]; len(got) != 1 || got[0].Title != "Server sync recovered" {
		t.Fatalf("server resolved: %v", states(got))
	}
}

// withPause is the snapshot of st with a paused server: view() reads runtime.Status
// through its JSON form, so the "paused" field is read as soon as the runtime has it.
func withPause(t *testing.T, st runtime.Status, pause string) snapshot {
	t.Helper()
	s := view(st)
	if err := json.Unmarshal([]byte(pause), &s.Servers[0].Paused); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRejectedRevisionsFireAgain(t *testing.T) {
	c, r := newClock(), &recorder{}
	n := newTestNotifier(c, r)
	st := healthyStatus()
	st.LastError = "revision aaa1111 rejected: x"
	n.Evaluate(st)
	st.LastError = "revision bbb2222 rejected: y"
	n.Evaluate(st)
	n.Evaluate(st)
	n.Flush(context.Background())
	got := r.all()
	if len(got) != 2 || got[0].Revision != "aaa1111" || got[1].Revision != "bbb2222" {
		t.Fatalf("messages = %+v", got)
	}
}

func TestBotOffline(t *testing.T) {
	c, r := newClock(), &recorder{}
	n := newTestNotifier(c, r)
	st := healthyStatus()
	st.Bots[0].Health, st.Bots[0].Message = runtime.HealthMissing, "401 Unauthorized"
	n.Evaluate(st)
	n.Flush(context.Background())
	if got := r.all(); len(got) != 1 || got[0].Title != "Bot offline" || got[0].Bot != "cicord" || got[0].Detail != "401 Unauthorized" {
		t.Fatalf("messages = %+v", got)
	}
}

func TestFlushRetriesAfterFailure(t *testing.T) {
	c, r := newClock(), &recorder{err: errors.New("down")}
	n := newTestNotifier(c, r)
	n.Evaluate(degradedPlugin(""))
	n.Flush(context.Background())
	if len(n.Pending()) != 1 {
		t.Fatalf("a failed send must keep the alert queued: %v", n.Pending())
	}
	if d, ok := n.flushDelay(); !ok || d <= 0 {
		t.Errorf("retry delay = %v %v, want a backoff", d, ok)
	}
	r.err = nil
	n.Flush(context.Background())
	if len(r.all()) != 1 || len(n.Pending()) != 0 {
		t.Fatalf("retry: sent %v, pending %v", r.all(), n.Pending())
	}
}

// fakeSource is a runtime whose status the test sets.
type fakeSource struct {
	mu sync.Mutex
	st runtime.Status
	ch chan runtime.Change
}

func (f *fakeSource) Status() runtime.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *fakeSource) Revision() (source.Revision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.st.Revision == nil {
		return source.Revision{}, false
	}
	return *f.st.Revision, true
}

func (f *fakeSource) SubscribeChanges() (<-chan runtime.Change, func()) { return f.ch, func() {} }

func (f *fakeSource) set(st runtime.Status) {
	f.mu.Lock()
	f.st = st
	f.mu.Unlock()
	f.ch <- runtime.Change{Topic: runtime.ChangePlugins, Server: "showcase"}
}

func TestRunFollowsChanges(t *testing.T) {
	r := &recorder{}
	n := New(Options{Sender: r, PublicURL: "https://c.example", Logger: quiet(), Poll: time.Hour, MinInterval: 10 * time.Millisecond, ResolveAfter: 50 * time.Millisecond})
	src := &fakeSource{st: healthyStatus(), ch: make(chan runtime.Change, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx, src); close(done) }()
	defer func() { cancel(); <-done }()
	src.set(degradedPlugin(""))
	waitFor(t, func() bool { return len(r.all()) == 1 })
	src.set(healthyStatus())
	time.Sleep(100 * time.Millisecond)
	src.ch <- runtime.Change{Topic: runtime.ChangeStatus}
	waitFor(t, func() bool { return len(r.all()) == 2 })
	if got := states(r.all()); got[1] != "resolved Plugin instance healthy again" {
		t.Fatalf("messages = %v", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ───────────────────────────────────────────────────────────── webhook

func TestWebhookDiscordAndJSON(t *testing.T) {
	var mu sync.Mutex
	bodies := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies[r.URL.Path] = b
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	msg := Message{Key: "plugin/showcase/cicord/tickets", State: StateFiring, Severity: SeverityCritical, Title: "Plugin instance degraded",
		Runtime: "community.example.org", Server: "showcase", Bot: "cicord", Instance: "tickets", Plugin: "tickets@1.2.0", Workflow: "modal open",
		Link: "https://cicord.tailnet.ts.net/admin/servers/showcase/plugins", Time: time.Now()}

	// The Discord format is chosen from the URL; the test server stands in for discord.com.
	w, err := NewWebhook(srv.URL+"/api/webhooks/1/tok", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	w.discord = true
	if err := w.Send(context.Background(), []Message{msg}); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Embeds []struct {
			Title  string
			Fields []struct{ Name, Value string }
		}
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal(bodies["/api/webhooks/1/tok"], &d); err != nil {
		t.Fatal(err)
	}
	if testing.Verbose() {
		t.Logf("discord payload: %s", bodies["/api/webhooks/1/tok"])
	}
	if len(d.Embeds) != 1 || d.Embeds[0].Title != "[CRITICAL] Plugin instance degraded" || d.AllowedMentions.Parse == nil || len(d.AllowedMentions.Parse) != 0 {
		t.Fatalf("discord payload = %s", bodies["/api/webhooks/1/tok"])
	}

	j, err := NewWebhook(srv.URL+"/hook", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Send(context.Background(), []Message{msg}); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Source, Text string
		Alerts       []Message
	}
	if err := json.Unmarshal(bodies["/hook"], &p); err != nil {
		t.Fatal(err)
	}
	if p.Source != "cicord" || len(p.Alerts) != 1 || p.Alerts[0].Workflow != "modal open" || !strings.Contains(p.Text, "Plugin instance: tickets") {
		t.Fatalf("json payload = %s", bodies["/hook"])
	}
}

func TestWebhookErrorsHideTheURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	w, _ := NewWebhook(srv.URL+"/api/webhooks/1/SECRET-TOKEN", srv.Client())
	w.retry = time.Millisecond
	if err := w.Send(context.Background(), []Message{{Title: "x"}}); err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("err = %v", err)
	}
	srv.Close()
	err := w.Send(context.Background(), []Message{{Title: "x"}})
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") || strings.Contains(err.Error(), "/api/webhooks") {
		t.Fatalf("network error = %v", err)
	}
	if strings.Contains(w.Describe(), "SECRET") {
		t.Errorf("Describe = %s", w.Describe())
	}
}

func TestWebhookValidation(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "http://example.com/hook", "not a url"} {
		if _, err := NewWebhook(u, nil); err == nil {
			t.Errorf("NewWebhook(%q) accepted", u)
		}
	}
	w, err := NewWebhook("https://discord.com/api/webhooks/123/abc", nil)
	if err != nil || !w.discord || w.Describe() != "Discord webhook on discord.com" {
		t.Errorf("discord webhook: %v %+v", err, w)
	}
}

func TestWebhookRetriesOn429(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0.05")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	w, _ := NewWebhook(srv.URL+"/hook", srv.Client())
	if err := w.Send(context.Background(), []Message{{Title: "x"}}); err != nil || calls != 2 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

func TestFailingWorkflowWhileInstanceHealthy(t *testing.T) {
	c, r := newClock(), &recorder{}
	n := newTestNotifier(c, r)
	in := config.Instance{Bot: "cicord", Name: "tickets", Server: "showcase"}
	fail := plugin.HandlerResult{Kind: "modal", Name: "open", Err: errors.New("boom")}
	ok := plugin.HandlerResult{Kind: "component", Name: "open"}
	n.Evaluate(healthyStatus())
	for range 2 {
		n.ObserveHandler(in, fail)
		n.ObserveHandler(in, ok) // another handler succeeds: the streak of modal open goes on
	}
	n.Evaluate(healthyStatus())
	if len(n.Pending()) != 0 {
		t.Fatalf("two failures fired: %v", states(n.Pending()))
	}
	n.ObserveHandler(in, fail)
	n.Evaluate(healthyStatus())
	n.Flush(context.Background())
	if got := r.all(); len(got) != 1 || got[0].Title != "Plugin handler failing" || got[0].Workflow != "modal open" || got[0].Detail == "" {
		t.Fatalf("messages = %+v", got)
	}
	// A success of that handler clears it; ResolveAfter later, it is resolved.
	n.ObserveHandler(in, plugin.HandlerResult{Kind: "modal", Name: "open"})
	n.Evaluate(healthyStatus())
	c.advance(3 * time.Minute)
	n.Evaluate(healthyStatus())
	n.Flush(context.Background())
	if got := r.all(); len(got) != 2 || got[1].State != StateResolved {
		t.Fatalf("messages = %v", states(got))
	}
}

func TestNewRevisionResetsFailures(t *testing.T) {
	c, r := newClock(), &recorder{}
	n := newTestNotifier(c, r)
	in := config.Instance{Bot: "cicord", Name: "tickets", Server: "showcase"}
	n.Evaluate(healthyStatus())
	for range 3 {
		n.ObserveHandler(in, plugin.HandlerResult{Kind: "command", Name: "ticket", Err: errors.New("boom")})
	}
	n.Evaluate(healthyStatus())
	st := healthyStatus()
	st.Revision = &source.Revision{ID: "fixed00"}
	testRevision = "fixed00"
	defer func() { testRevision = "abc1234" }()
	n.Evaluate(st)
	c.advance(3 * time.Minute)
	n.Evaluate(st)
	n.Flush(context.Background())
	if got := states(r.all()); len(got) != 2 || got[0] != "firing Plugin handler failing" || got[1] != "resolved Plugin instance healthy again" {
		t.Fatalf("messages = %v", got)
	}
	if r.all()[0].Workflow != "command /ticket" {
		t.Errorf("workflow = %q", r.all()[0].Workflow)
	}
}
