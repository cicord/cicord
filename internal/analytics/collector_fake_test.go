package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// collectorFakeStore is an in-memory state.Analytics for the collector tests.
type collectorFakeStore struct {
	mu      sync.Mutex
	points  map[collectorPointKey]float64
	records map[state.AnalyticsKind]int // Record calls per kind
	active  map[[3]string]map[string]bool
	marks   int
	dropped []string
	purges  map[string]time.Time
	fail    error
}

type collectorPointKey struct {
	server, metric, dim string
	bucket              time.Time
}

func newCollectorFakeStore() *collectorFakeStore {
	return &collectorFakeStore{points: map[collectorPointKey]float64{}, records: map[state.AnalyticsKind]int{},
		active: map[[3]string]map[string]bool{}, purges: map[string]time.Time{}}
}

func (f *collectorFakeStore) Record(_ context.Context, kind state.AnalyticsKind, pts ...state.AnalyticsPoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.records[kind]++
	for _, p := range pts {
		k := collectorPointKey{p.Server, p.Metric, p.Dim, p.Bucket.UTC().Truncate(time.Hour)}
		old, ok := f.points[k]
		switch {
		case !ok || kind == state.AnalyticsGaugeLast:
			f.points[k] = p.Value
		case kind == state.AnalyticsCounter:
			f.points[k] = old + p.Value
		default:
			f.points[k] = max(old, p.Value)
		}
	}
	return nil
}

func (f *collectorFakeStore) Query(context.Context, state.AnalyticsQuery) ([]state.AnalyticsPoint, error) {
	return nil, errors.New("not implemented")
}

func (f *collectorFakeStore) Dims(context.Context, string, string, time.Time, time.Time, int) ([]state.AnalyticsDim, error) {
	return nil, errors.New("not implemented")
}

func (f *collectorFakeStore) MarkActive(_ context.Context, server, set, day, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	k := [3]string{server, set, day}
	if f.active[k] == nil {
		f.active[k] = map[string]bool{}
	}
	f.active[k][token] = true
	f.marks++
	return nil
}

func (f *collectorFakeStore) CountActive(_ context.Context, server, set, day string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return 0, f.fail
	}
	return int64(len(f.active[[3]string{server, set, day}])), nil
}

func (f *collectorFakeStore) DropActive(_ context.Context, before string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return 0, f.fail
	}
	f.dropped = append(f.dropped, before)
	var n int64
	for k, toks := range f.active {
		if k[2] < before {
			n += int64(len(toks))
			delete(f.active, k)
		}
	}
	return n, nil
}

func (f *collectorFakeStore) Purge(_ context.Context, server string, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return 0, f.fail
	}
	f.purges[server] = before
	return 0, nil
}

func (f *collectorFakeStore) Since(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

func (f *collectorFakeStore) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

// value returns a recorded point (0 when absent).
func (f *collectorFakeStore) value(server, metric, dim string, bucket time.Time) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.points[collectorPointKey{server, metric, dim, bucket.UTC().Truncate(time.Hour)}]
}

// sum returns the sum of a metric over every dimension and bucket.
func (f *collectorFakeStore) sum(server, metric string) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var v float64
	for k, x := range f.points {
		if k.server == server && k.metric == metric {
			v += x
		}
	}
	return v
}

// dims returns the dimensions of a metric with their summed values.
func (f *collectorFakeStore) dims(server, metric string) map[string]float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]float64{}
	for k, x := range f.points {
		if k.server == server && k.metric == metric {
			out[k.dim] += x
		}
	}
	return out
}

func (f *collectorFakeStore) dump() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lines []string
	for k, v := range f.points {
		lines = append(lines, fmt.Sprintf("%s %s[%s] %s = %v", k.server, k.metric, k.dim, k.bucket.Format("01-02T15"), v))
	}
	sort.Strings(lines)
	return fmt.Sprint(lines)
}

// collectorFakeKV is an in-memory state.KV recording TTLs.
type collectorFakeKV struct {
	mu   sync.Mutex
	vals map[string]json.RawMessage
	ttls map[string]time.Duration
}

func newCollectorFakeKV() *collectorFakeKV {
	return &collectorFakeKV{vals: map[string]json.RawMessage{}, ttls: map[string]time.Duration{}}
}

func (k *collectorFakeKV) Get(_ context.Context, key string) (json.RawMessage, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.vals[key]
	if !ok {
		return nil, state.ErrNotFound
	}
	return v, nil
}

func (k *collectorFakeKV) Set(_ context.Context, key string, value json.RawMessage, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.vals[key], k.ttls[key] = value, ttl
	return nil
}

func (k *collectorFakeKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.vals, key)
	return nil
}

func (k *collectorFakeKV) Increment(context.Context, string, float64) (float64, error) {
	return 0, errors.New("not implemented")
}

func (k *collectorFakeKV) List(context.Context, string, int, string) ([]state.KVItem, string, error) {
	return nil, "", errors.New("not implemented")
}

func (k *collectorFakeKV) has(key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.vals[key]
	return ok
}

// collectorFakeClient is a discord.Client that only implements discord.AnalyticsReader.
type collectorFakeClient struct {
	discord.Client
	mu        sync.Mutex
	counts    discord.ServerCounts
	events    []discord.ScheduledEventInterest
	invites   []discord.InviteUses
	inviteErr error
	calls     map[string]int
}

func (f *collectorFakeClient) call(name string) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

func (f *collectorFakeClient) GetServerCounts(context.Context, string) (discord.ServerCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("counts")
	return f.counts, nil
}

func (f *collectorFakeClient) ListScheduledEventInterest(context.Context, string) ([]discord.ScheduledEventInterest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("events")
	return append([]discord.ScheduledEventInterest(nil), f.events...), nil
}

func (f *collectorFakeClient) ListInviteUses(context.Context, string) ([]discord.InviteUses, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("invites")
	if f.inviteErr != nil {
		return nil, f.inviteErr
	}
	return append([]discord.InviteUses(nil), f.invites...), nil
}

func (f *collectorFakeClient) setInvites(invs ...discord.InviteUses) {
	f.mu.Lock()
	f.invites = invs
	f.mu.Unlock()
}

func (f *collectorFakeClient) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

// collectorClock is a settable clock.
type collectorClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *collectorClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *collectorClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *collectorClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// collectorFixture is a collector with a fake store, KV, client and clock, collecting
// server "main" (Discord ID "1") through bot "alpha".
type collectorFixture struct {
	c      *Collector
	store  *collectorFakeStore
	kv     *collectorFakeKV
	client *collectorFakeClient
	clock  *collectorClock
	cfg    CollectorServer
}

func newCollectorFixture(t *testing.T, mutate ...func(*CollectorServer)) *collectorFixture {
	t.Helper()
	f := &collectorFixture{store: newCollectorFakeStore(), kv: newCollectorFakeKV(), client: &collectorFakeClient{},
		clock: &collectorClock{t: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)}}
	f.cfg = CollectorServer{Key: "main", ID: "1", Enabled: true, RetentionDays: 30, Bot: "alpha", BotFile: "bots/alpha.yaml",
		ApplicationID: "900", Connected: true, BotUserID: "600", Client: f.client,
		Intents: CollectorIntents | discord.MustIntents("Servers", "ServerMembers", "MessageContent"),
		Permissions: func(context.Context) (discord.Permissions, error) {
			return discord.MustPermissions("Administrator"), nil
		},
		Channel: func(id string) (discord.Channel, bool) {
			switch id {
			case "40":
				return discord.Channel{ID: id, Type: discord.ChannelStage}, true
			case "30", "31":
				return discord.Channel{ID: id, Type: discord.ChannelVoice}, true
			}
			return discord.Channel{}, false
		},
		AFKChannelID: func() string { return "39" },
	}
	for _, m := range mutate {
		m(&f.cfg)
	}
	f.c = f.newCollector()
	return f
}

func (f *collectorFixture) newCollector() *Collector {
	c := NewCollector(CollectorOptions{Store: f.store, KV: f.kv, Now: f.clock.Now, InviteDebounce: 20 * time.Millisecond, Logger: slog.New(slog.DiscardHandler)})
	c.Configure([]CollectorServer{f.cfg}, []string{"600", "601"})
	return c
}

func (f *collectorFixture) at(hh, mm, ss int) time.Time {
	return time.Date(2026, 11, 24, hh, mm, ss, 0, time.UTC)
}
