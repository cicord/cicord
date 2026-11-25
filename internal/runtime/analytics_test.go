package runtime

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
	"github.com/cicord/cicord/internal/version"
)

// memAnalytics is a minimal in-memory state.Analytics for the runtime tests.
type memAnalytics struct {
	mu     sync.Mutex
	points map[[3]string]float64 // metric, dim, bucket → value (server "main" only)
	active map[[3]string]map[string]bool
}

func newMemAnalytics() *memAnalytics {
	return &memAnalytics{points: map[[3]string]float64{}, active: map[[3]string]map[string]bool{}}
}

func (m *memAnalytics) Record(_ context.Context, kind state.AnalyticsKind, pts ...state.AnalyticsPoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range pts {
		k := [3]string{p.Server + "/" + p.Metric, p.Dim, p.Bucket.Truncate(time.Hour).Format(time.RFC3339)}
		switch kind {
		case state.AnalyticsCounter:
			m.points[k] += p.Value
		case state.AnalyticsGaugeMax:
			m.points[k] = max(m.points[k], p.Value)
		default:
			m.points[k] = p.Value
		}
	}
	return nil
}

func (m *memAnalytics) Query(context.Context, state.AnalyticsQuery) ([]state.AnalyticsPoint, error) {
	return nil, errors.New("not implemented")
}

func (m *memAnalytics) Dims(context.Context, string, string, time.Time, time.Time, int) ([]state.AnalyticsDim, error) {
	return nil, errors.New("not implemented")
}

func (m *memAnalytics) MarkActive(_ context.Context, server, set, day, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [3]string{server, set, day}
	if m.active[k] == nil {
		m.active[k] = map[string]bool{}
	}
	m.active[k][token] = true
	return nil
}

func (m *memAnalytics) CountActive(_ context.Context, server, set, day string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.active[[3]string{server, set, day}])), nil
}

func (m *memAnalytics) DropActive(context.Context, string) (int64, error) { return 0, nil }
func (m *memAnalytics) Purge(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}
func (m *memAnalytics) Since(context.Context, string) (time.Time, error) { return time.Time{}, nil }

// sum returns the sum of a metric of a server (every bucket), for one dimension or all ("*").
func (m *memAnalytics) sum(server, metric, dim string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var v float64
	for k, x := range m.points {
		if k[0] == server+"/"+metric && (dim == "*" || k[1] == dim) {
			v += x
		}
	}
	return v
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type analyticsEnv struct {
	world   *sim.World
	rt      *Runtime
	store   *memAnalytics
	srv     discord.Server
	general discord.Channel
	voice   discord.Channel
	alpha   discord.User
	beta    discord.User
}

// startAnalytics runs a repository with two bots (alpha, beta) on one server, analytics on.
func startAnalytics(t *testing.T, alphaIntents, serverAnalytics string) *analyticsEnv {
	t.Helper()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Main", owner.ID, sim.WithGeneralChannel())
	general, _ := w.ChannelByName(srv.ID, "general")
	voice, err := w.AdminCreateChannel(srv.ID, discord.ChannelParams{Name: ptrTo("Lounge"), Type: discord.ChannelVoice})
	if err != nil {
		t.Fatal(err)
	}
	tokA, alpha, appA := w.CreateBot("alpha")
	tokB, beta, appB := w.CreateBot("beta")
	for _, b := range []discord.User{alpha, beta} {
		if _, err := w.AddBotToServer(srv.ID, b.ID, discord.MustPermissions("Administrator")); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "cicord.yaml"), "apiVersion: cicord.dev/v1alpha1\nkind: Project\nmetadata:\n  name: test\nspec:\n  engine: \""+version.EngineRange(version.Version)+"\"\n")
	bot := func(name, appID, intents string) string {
		return "apiVersion: cicord.dev/v1alpha1\nkind: Bot\nmetadata:\n  name: " + name + "\nspec:\n  applicationId: \"" + appID + "\"\n  token:\n    secretRef: TOKEN_" + strings.ToUpper(name) + "\n  servers: [main]\n" + intents
	}
	writeTestFile(t, filepath.Join(dir, "bots", "alpha", "bot.yaml"), bot("alpha", appA, alphaIntents))
	writeTestFile(t, filepath.Join(dir, "bots", "beta", "bot.yaml"), bot("beta", appB, ""))
	writeTestFile(t, filepath.Join(dir, "servers", "main.yaml"), "apiVersion: cicord.dev/v1alpha1\nkind: Server\nmetadata:\n  name: main\nspec:\n  discordId: \""+srv.ID+"\"\n"+serverAnalytics)

	src, err := source.NewLocal(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	mem := newMemAnalytics()
	tokens := map[string]string{"TOKEN_ALPHA": tokA, "TOKEN_BETA": tokB}
	rt := New(Options{
		Source: src, Connector: w, Store: st, Mode: "dev", Simulator: true, Analytics: mem,
		Secrets: func(n string) (string, bool) { v, ok := tokens[n]; return v, ok },
		Logger:  slog.New(slog.DiscardHandler), ResyncInterval: -1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rt.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		src.Close()
		st.Close()
	})
	e := &env{t: t, rt: rt}
	e.waitFor("first revision", func() bool { _, ok := rt.Revision(); return ok })
	return &analyticsEnv{world: w, rt: rt, store: mem, srv: srv, general: general, voice: voice, alpha: alpha, beta: beta}
}

func ptrTo[T any](v T) *T { return &v }

func TestAnalyticsEndToEnd(t *testing.T) {
	e := startAnalytics(t, "  intents: [ServerMembers]\n", "  analytics:\n    presence: true\n")
	w := e.world

	// Intents: the collector (alpha, first in sorted order) gets the analytics intents;
	// beta keeps its own; no privileged intent is added.
	alpha, beta := e.rt.bot("alpha"), e.rt.bot("beta")
	if alpha == nil || beta == nil {
		t.Fatalf("bots not connected: %+v", e.rt.Status().Bots)
	}
	if !alpha.intents.Has(analytics.CollectorIntents | discord.MustIntents("ServerMembers")) {
		t.Errorf("alpha intents = %v", alpha.intents.Names())
	}
	if alpha.intents.Has(discord.MustIntents("ServerPresences")) || alpha.intents.Has(discord.MustIntents("MessageContent")) {
		t.Errorf("a privileged intent was added: %v", alpha.intents.Names())
	}
	if beta.intents.Has(discord.MustIntents("ServerVoiceStates")) {
		t.Errorf("beta (not the collector) got analytics intents: %v", beta.intents.Names())
	}

	alice, bob := w.CreateUser("alice"), w.CreateUser("bob")
	for _, u := range []discord.User{alice, bob} {
		if err := w.AddMember(e.srv.ID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := w.SendMessage(alice.ID, e.general.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.SendMessageWith(bob.ID, e.general.ID, discord.MessageCreate{Content: "hi", ReplyTo: msg.ID}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddReaction(bob.ID, e.general.ID, msg.ID, "👍"); err != nil {
		t.Fatal(err)
	}
	if err := w.JoinVoice(alice.ID, e.voice.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := w.LeaveVoice(e.srv.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.AdminKickMember(e.srv.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	w.WaitIdle()
	e.rt.collector.Flush(context.Background())

	s := e.store
	checks := []struct {
		metric, dim string
		want        float64
	}{
		{analytics.MembersJoined, "", 2}, // both bots receive the joins: counted once
		{analytics.MembersLeft, "", 1},
		{analytics.MessagesSent, e.general.ID, 2},
		{analytics.MessagesReplies, e.general.ID, 1},
		{analytics.ReactionsAdded, "👍", 1},
		{analytics.VoiceSessions, e.voice.ID, 1},
		{analytics.ModerationActions, "kick", 1},
	}
	for _, c := range checks {
		if got := s.sum("main", c.metric, c.dim); got != c.want {
			t.Errorf("%s[%s] = %v, want %v", c.metric, c.dim, got, c.want)
		}
	}
	if v := s.sum("main", analytics.VoiceSeconds, e.voice.ID); v <= 0 {
		t.Errorf("voice seconds = %v", v)
	}
	// Cicord's own changes (the reconciler, bots joining) are not moderation actions.
	if v := s.sum("main", analytics.ModerationActions, "*"); v != 1 {
		t.Errorf("moderation actions = %v", v)
	}
	// The first sample ran when the runtime started.
	// members.total counts humans: the two bots of the runtime are members.bots.
	if v := s.sum("main", analytics.MembersTotal, ""); v < 1 || v > 3 { // sampled concurrently with the joins
		t.Errorf("members.total = %v", v)
	}
	if v := s.sum("main", analytics.MembersBots, ""); v != 2 {
		t.Errorf("members.bots = %v", v)
	}
	day := time.Now().UTC().Format(time.DateOnly)
	if n, _ := s.CountActive(context.Background(), "main", analytics.ActiveSetAll, day); n != 2 {
		t.Errorf("active members = %d", n)
	}

	st := e.rt.AnalyticsStatus("main")
	if !st.Enabled || st.Presence || st.RetentionDays != 400 {
		t.Errorf("status = %+v", st)
	}
	var groups []string
	for _, m := range st.Missing {
		groups = append(groups, m.Metrics[0])
	}
	if strings.Join(groups, ",") != analytics.MessagesAttachments+","+analytics.PresenceStatus {
		t.Errorf("missing = %+v", st.Missing)
	}
	if !strings.Contains(st.Missing[1].Reason, "does not request the privileged ServerPresences intent") {
		t.Errorf("presence reason = %q", st.Missing[1].Reason)
	}
}

func TestAnalyticsDisabledAddsNoIntent(t *testing.T) {
	e := startAnalytics(t, "", "  analytics:\n    enabled: false\n")
	if h := e.rt.bot("alpha"); h == nil || h.intents.Has(discord.MustIntents("ServerVoiceStates")) {
		t.Errorf("alpha intents with analytics disabled: %+v", h)
	}
	if st := e.rt.AnalyticsStatus("main"); st.Enabled {
		t.Errorf("status = %+v", st)
	}
	// Without ServerMembers, joins are not collected and Status says how to fix it.
	e2 := startAnalytics(t, "", "")
	st := e2.rt.AnalyticsStatus("main")
	if !st.Enabled || len(st.Missing) == 0 || st.Missing[0].Metrics[0] != analytics.MembersJoined ||
		!strings.Contains(st.Missing[0].Fix, "bots/alpha/bot.yaml") {
		t.Errorf("status = %+v", st)
	}
	if h := e2.rt.bot("alpha"); h.intents.Has(discord.MustIntents("ServerMembers")) {
		t.Error("ServerMembers added implicitly")
	}
}

func TestAnalyticsOffWithoutStore(t *testing.T) {
	r := New(Options{Logger: slog.New(slog.DiscardHandler)})
	if r.collector != nil || r.analyticsIntents(nil, "alpha") != 0 {
		t.Error("collector without an analytics store")
	}
	if st := r.AnalyticsStatus("main"); st.Enabled || st.Missing == nil {
		t.Errorf("status = %+v", st)
	}
}
