package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// dmFakeSession answers GetMember from members, waiting while the user's gate is closed.
type dmFakeSession struct {
	discord.Session
	mu        sync.Mutex
	members   map[string]bool // server/user
	gates     map[string]chan struct{}
	lookups   atomic.Int32
	sent      []discord.MessageCreate
	responses []discord.InteractionResponse
}

func (f *dmFakeSession) ApplicationID() string { return "app" }
func (f *dmFakeSession) Self() discord.User    { return discord.User{ID: "1", Bot: true} }
func (f *dmFakeSession) GetMember(ctx context.Context, sid, user string) (discord.Member, error) {
	f.lookups.Add(1)
	f.mu.Lock()
	ok, gate := f.members[sid+"/"+user], f.gates[user]
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return discord.Member{}, ctx.Err()
		}
	}
	if !ok {
		return discord.Member{}, discord.ErrNotFound
	}
	return discord.Member{ServerID: sid, User: discord.User{ID: user}}, nil
}
func (f *dmFakeSession) SendMessage(_ context.Context, _ string, m discord.MessageCreate) (discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return discord.Message{ID: fmt.Sprint(len(f.sent))}, nil
}
func (f *dmFakeSession) RespondInteraction(_ context.Context, _, _ string, r discord.InteractionResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, r)
	return nil
}
func (f *dmFakeSession) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sent {
		out = append(out, m.Content)
	}
	return out
}

func dmTestHost(t *testing.T, locale string, servers ...string) (*botHost, *dmFakeSession) {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &dmFakeSession{members: map[string]bool{}, gates: map[string]chan struct{}{}}
	h := &botHost{rt: &Runtime{opts: Options{Store: db}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}, key: "bot", tokenHash: "t", session: f}
	rt := &routing{dmOwner: "support", dmLocale: locale, byName: map[string]map[string]*instanceRT{"support": {}}, caches: map[string]*serverCache{}, serverKeys: map[string]string{}}
	def := &plugin.Definition{Events: []string{"directMessage"}}
	for _, sid := range servers {
		rt.byName["support"][sid] = &instanceRT{serverID: sid, plugin: &builtPlugin{def: def}}
		rt.caches[sid] = newServerCache("key-"+sid, sid)
		rt.serverKeys[sid] = "key-" + sid
	}
	h.routing.Store(rt)
	return h, f
}

func dm(user, text string) discord.Message {
	return discord.Message{ID: user + text, ChannelID: "dm-" + user, Author: discord.User{ID: user}, Content: text}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// m5: a slow membership check of one user does not hold the DMs of another user of the
// same bot (it held every DM, selection and component of the bot before).
func TestDMSlowLookupDoesNotBlockOtherUsers(t *testing.T) {
	h, f := dmTestHost(t, "en", "s1", "s2")
	slow := "100"
	other := "200"
	for n := 201; h.dm.shard(other) == h.dm.shard(slow); n++ {
		other = fmt.Sprint(n)
	}
	gate := make(chan struct{})
	f.gates[slow] = gate
	defer close(gate)
	h.acceptDMMessage(dm(slow, "hello"), time.Now())
	h.acceptDMMessage(dm(other, "hello"), time.Now())
	waitUntil(t, "the other user's answer", func() bool {
		for _, s := range f.texts() {
			if s == "No server accepts your direct messages." {
				return true
			}
		}
		return false
	})
}

// A fresh member listing answers membership without a Discord call.
func TestDMUsesFreshMemberListing(t *testing.T) {
	h, f := dmTestHost(t, "en", "s1")
	l := h.routing.Load().caches["s1"].members
	l.mu.Lock()
	l.members, l.at = map[string]discord.Member{"100": {ServerID: "s1", User: discord.User{ID: "100"}}}, l.now()
	l.mu.Unlock()
	done := make(chan struct{})
	h.acceptDMMessage(dm("100", "hello"), time.Now())
	h.acceptDMMessage(dm("300", "hello"), time.Now()) // listed as a non-member
	h.queueDM("100", func() { close(done) })
	<-done
	waitUntil(t, "non-member answer", func() bool { return len(f.texts()) == 1 })
	if n := f.lookups.Load(); n != 0 {
		t.Fatalf("%d Discord member lookups with a fresh listing", n)
	}
	// A stale listing is not trusted.
	l.mu.Lock()
	l.at = l.at.Add(-memberListTTL)
	l.mu.Unlock()
	f.members["s1/100"] = true
	h.acceptDMMessage(dm("100", "again"), time.Now())
	waitUntil(t, "lookup of a stale listing", func() bool { return f.lookups.Load() == 1 })
}

// A user flooding DMs spends a token per DM before any lookup: beyond the burst, the DMs
// are not routed and the user is told once (localized) to retry, not silently dropped.
func TestDMRateLimitBeforeLookups(t *testing.T) {
	h, f := dmTestHost(t, "fr", "s1", "s2", "s3")
	now := time.Now()
	for n := range 50 {
		h.acceptDMMessage(dm("100", fmt.Sprint(n)), now)
	}
	done := make(chan struct{})
	h.queueDM("100", func() { close(done) })
	<-done
	if n := f.lookups.Load(); n != dmBurst*3 {
		t.Fatalf("%d member lookups for 50 DMs, want %d (burst × servers)", n, dmBurst*3)
	}
	busy := 0
	waitUntil(t, "busy notice", func() bool {
		busy = 0
		for _, s := range f.texts() {
			if s == "Trop de messages en ce moment. Réessayez dans un instant." {
				busy++
			}
		}
		return busy > 0
	})
	if busy != 1 {
		t.Fatalf("%d busy notices, want one per notice period", busy)
	}
	// Tokens come back with time.
	h.acceptDMMessage(dm("100", "later"), now.Add(dmRefill))
	waitUntil(t, "routing after refill", func() bool { return f.lookups.Load() == dmBurst*3+3 })
}

// A full queue answers busy instead of dropping the DM silently (P10); a DM component
// refused the same way is answered within Discord's 3 s.
func TestDMQueueFullRepliesBusy(t *testing.T) {
	h, f := dmTestHost(t, "en", "s1")
	gate := make(chan struct{})
	defer close(gate)
	// One user's work holds the shard; other users of the same shard fill its queue.
	first := "100"
	f.gates[first] = gate
	shard := h.dm.shard(first)
	var same []string
	for n := 1000; len(same) < dmShardQueue+3; n++ {
		if u := fmt.Sprint(n); h.dm.shard(u) == shard {
			same = append(same, u)
			f.gates[u] = gate
		}
	}
	h.acceptDMMessage(dm(first, "hello"), time.Now())
	waitUntil(t, "first lookup", func() bool { return f.lookups.Load() == 1 })
	for _, u := range same[:dmShardQueue+1] {
		h.acceptDMMessage(dm(u, "hello"), time.Now())
	}
	waitUntil(t, "busy reply", func() bool {
		for _, s := range f.texts() {
			if s == "Too many messages right now. Please try again in a moment." {
				return true
			}
		}
		return false
	})
	u := same[dmShardQueue+1]
	h.acceptDMInteraction(discord.Interaction{ID: "i", Token: "t", User: discord.User{ID: u}}, time.Now(), func() { t.Error("queued past the bound") })
	waitUntil(t, "interaction answer", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.responses) == 1 && f.responses[0].Data.Flags&discord.MessageFlagEphemeral != 0
	})
}
