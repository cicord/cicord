package analytics

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// liveCache is a fake of the runtime's cache: channels, roles and owners, changed by
// the test.
type liveCache struct {
	mu       sync.Mutex
	channels map[string]discord.Channel
	roles    map[string]discord.Role
	owners   map[string]string
}

func (c *liveCache) channel(id string) (discord.Channel, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.channels[id]
	return ch, ok
}

func (c *liveCache) role(id string) (discord.Role, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[id]
	return r, ok
}

func (c *liveCache) owner(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owners[id]
}

func (c *liveCache) set(f func(*liveCache)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c)
}

// Labels knows the live channels, like the runtime's labeler.
func (c *liveCache) Labels(_ context.Context, _ string, dims []DimRef) map[string]Label {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]Label{}
	for _, d := range dims {
		if ch, ok := c.channels[d.Value]; ok && d.Kind == "channel" {
			out[d.Value] = Label{Name: ch.Name, Kind: channelLabel(ch).Type}
		}
	}
	return out
}

// countingLabels counts the labels written.
type countingLabels struct {
	state.Analytics
	state.AnalyticsLabels
	mu  sync.Mutex
	put map[string]int // id → writes
}

func (c *countingLabels) PutLabels(ctx context.Context, ls ...state.AnalyticsLabel) error {
	c.mu.Lock()
	for _, l := range ls {
		c.put[l.ID]++
	}
	c.mu.Unlock()
	return c.AnalyticsLabels.PutLabels(ctx, ls...)
}

func (c *countingLabels) writes(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.put[id]
}

// TestCollectorRemembersLabels: a temporary voice room a plugin creates, has activity
// in and deletes keeps its name and its owner; the dashboard groups it with the
// plugin's other rooms and shows it deleted. A deleted role keeps its name too.
func TestCollectorRemembersLabels(t *testing.T) {
	clock := &collectorClock{t: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)}
	db, err := sqlite.Open(":memory:", sqlite.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := &countingLabels{Analytics: db.Analytics(), AnalyticsLabels: db.Analytics().(state.AnalyticsLabels), put: map[string]int{}}
	cache := &liveCache{
		channels: map[string]discord.Channel{"10": {ID: "10", ServerID: "1", Name: "general", Type: discord.ChannelText}},
		roles:    map[string]discord.Role{"r1": {ID: "r1", Name: "Helpers", Color: 0x3366ff}},
		owners:   map[string]string{"10": state.OwnerGit},
	}
	c := NewCollector(CollectorOptions{Store: store, Now: clock.Now, Logger: slog.New(slog.DiscardHandler)})
	c.Configure([]CollectorServer{{Key: "main", ID: "1", Enabled: true, RetentionDays: 400, Bot: "alpha", Connected: true,
		Intents: CollectorIntents, Channel: cache.channel, Role: cache.role, Owner: cache.owner}}, nil)

	// The plugin creates the room: the gateway event comes before the plugin records
	// its ownership (the owner is read again at the flush).
	room := discord.Channel{ID: "500", ServerID: "1", Name: "NPjonath's channel", Type: discord.ChannelVoice}
	c.Handle("alpha", discord.ChannelCreateEvent{Channel: room})
	cache.set(func(lc *liveCache) {
		lc.channels[room.ID] = room
		lc.owners[room.ID] = "plugin:alpha/voice-rooms"
	})
	msg := func(channel, user string) {
		c.Handle("alpha", discord.MessageCreateEvent{Message: discord.Message{ServerID: "1", ChannelID: channel, Author: discord.User{ID: user}}})
	}
	msg("500", "7")
	msg("10", "7")
	c.Handle("alpha", discord.MemberUpdateEvent{Member: discord.Member{ServerID: "1", User: discord.User{ID: "7"}, RoleIDs: []string{"r1"}},
		Previous: &discord.Member{ServerID: "1", User: discord.User{ID: "7"}}})
	c.Flush(ctx)
	// More activity the same day: nothing to write again.
	msg("10", "8")
	msg("500", "8")
	c.Flush(ctx)
	if n := store.writes("10"); n != 1 {
		t.Errorf("general written %d times, want once", n)
	}

	// The plugin deletes the room and forgets it (a sync refreshes the cache before
	// the flush); the role is deleted too (the event carries only its ID).
	c.Handle("alpha", discord.ChannelDeleteEvent{Channel: room})
	c.Handle("alpha", discord.RoleDeleteEvent{ServerID: "1", RoleID: "r1"})
	cache.set(func(lc *liveCache) {
		delete(lc.channels, room.ID)
		delete(lc.owners, room.ID)
		delete(lc.roles, "r1")
	})
	c.Flush(ctx)

	got, err := store.GetLabels(ctx, "main", "channel", []string{"10", "500"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]state.AnalyticsLabel{}
	for _, l := range got {
		byID[l.ID] = l
	}
	if l := byID["500"]; l.Name != "NPjonath's channel" || l.Type != "voice" || l.Owner != "plugin:alpha/voice-rooms" || !l.Deleted {
		t.Errorf("room label = %+v", l)
	}
	if l := byID["10"]; l.Name != "general" || l.Type != "channel" || l.Owner != "" || l.Deleted {
		t.Errorf("general label = %+v (a git channel has no plugin owner)", l)
	}

	// The dashboard: the room is grouped under its plugin and keeps its name.
	stored := StoredLabeler{Store: store, Live: func(string) bool { return true }}
	r := &Reader{Store: store, Now: clock.Now, Labeler: Labelers{cache, stored}, Owners: OwnersChain{stored}}
	s, err := r.Summary(ctx, "main", "7d", Status{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Rankings[MessagesSent]
	// Ties are ordered by dim: "10" before "plugin:…".
	if len(msgs) != 2 || msgs[0].Dim != "10" || msgs[0].Children != nil || msgs[1].Dim != "plugin:alpha/voice-rooms" ||
		msgs[1].Value != 2 || msgs[1].Count != 1 || len(msgs[1].Children) != 1 || msgs[1].Children[0].Dim != "500" {
		t.Fatalf("messages ranking = %+v", msgs)
	}
	if l := s.Labels["500"]; l != (Label{Name: "NPjonath's channel", Kind: "voice", Deleted: true}) {
		t.Errorf("room label = %+v", l)
	}
	if l := s.Labels["plugin:alpha/voice-rooms"]; l != (Label{Name: "voice-rooms", Kind: GroupKind}) {
		t.Errorf("group label = %+v", l)
	}
	if l := s.Labels["10"]; l != (Label{Name: "general", Kind: "channel"}) {
		t.Errorf("general label = %+v", l)
	}
	if l := s.Labels["r1"]; l != (Label{Name: "Helpers", Kind: "role", Color: "#3366ff", Deleted: true}) {
		t.Errorf("role label = %+v", l)
	}

	// The next day: retention prunes the labels without data (a channel created but
	// never used) and the active channels are written again (seen again).
	c.Handle("alpha", discord.ChannelCreateEvent{Channel: discord.Channel{ID: "600", ServerID: "1", Name: "unused", Type: discord.ChannelText}})
	c.Flush(ctx)
	if ls, _ := store.GetLabels(ctx, "main", "channel", []string{"600"}); len(ls) != 1 {
		t.Fatalf("unused channel label = %+v", ls)
	}
	clock.add(24 * time.Hour)
	c.Tick(ctx)
	if ls, _ := store.GetLabels(ctx, "main", "channel", []string{"600"}); len(ls) != 0 {
		t.Errorf("label of a channel without data kept: %+v", ls)
	}
	msg("10", "7")
	c.Flush(ctx)
	if n := store.writes("10"); n != 2 {
		t.Errorf("general written %d times after a day, want twice", n)
	}
	ls, _ := store.GetLabels(ctx, "main", "channel", []string{"10", "500"})
	for _, l := range ls {
		if l.ID == "10" && !l.SeenAt.Equal(clock.Now()) {
			t.Errorf("general seen at %v, want %v", l.SeenAt, clock.Now())
		}
	}
	if len(ls) != 2 {
		t.Errorf("labels with data after the prune = %+v", ls)
	}
}

// TestCollectorLabelsStoreFailure: labels a failing store refused are written at the
// next flush.
func TestCollectorLabelsStoreFailure(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := &failingLabels{Analytics: db.Analytics(), AnalyticsLabels: db.Analytics().(state.AnalyticsLabels), fail: true}
	c := NewCollector(CollectorOptions{Store: store, Logger: slog.New(slog.DiscardHandler)})
	c.Configure([]CollectorServer{{Key: "main", ID: "1", Enabled: true, RetentionDays: 30, Bot: "alpha", Connected: true}}, nil)
	c.Handle("alpha", discord.ChannelCreateEvent{Channel: discord.Channel{ID: "5", ServerID: "1", Name: "news", Type: discord.ChannelText}})
	c.Flush(ctx)
	store.fail = false
	c.Flush(ctx)
	if ls, _ := store.GetLabels(ctx, "main", "channel", []string{"5"}); len(ls) != 1 || ls[0].Name != "news" {
		t.Errorf("labels after the store recovered = %+v", ls)
	}
}

type failingLabels struct {
	state.Analytics
	state.AnalyticsLabels
	fail bool
}

func (f *failingLabels) PutLabels(ctx context.Context, ls ...state.AnalyticsLabel) error {
	if f.fail {
		return context.DeadlineExceeded
	}
	return f.AnalyticsLabels.PutLabels(ctx, ls...)
}
