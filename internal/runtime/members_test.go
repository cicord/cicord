package runtime

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// listerClient is a discord.Client whose only implemented method is ListMembers.
type listerClient struct {
	discord.Client
	calls   atomic.Int32
	gate    chan struct{} // when set, ListMembers waits for it
	started chan struct{} // when set, receives one value per call
	members []discord.Member
	err     error
}

func (c *listerClient) ListMembers(ctx context.Context, _ string) ([]discord.Member, error) {
	c.calls.Add(1)
	if c.started != nil {
		c.started <- struct{}{}
	}
	if c.gate != nil {
		select {
		case <-c.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return append([]discord.Member(nil), c.members...), c.err
}

func mem(id string, roles ...string) discord.Member {
	return discord.Member{ServerID: "1", User: discord.User{ID: id}, RoleIDs: roles}
}

func ids(ms []discord.Member) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.User.ID
	}
	return out
}

func TestMemberListCachesAndPatches(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newMemberList()
	l.now = func() time.Time { return now }
	cl := &listerClient{members: []discord.Member{mem("30"), mem("100"), mem("9")}}
	ctx := context.Background()

	got, err := l.List(ctx, cl, "1")
	if err != nil || len(got) != 3 || got[0].User.ID != "9" || got[2].User.ID != "100" {
		t.Fatalf("list = %v, %v", ids(got), err)
	}
	// Events patch the cached listing: no new request within the TTL.
	l.Observe(discord.MemberJoinEvent{Member: mem("50", "r")})
	l.Observe(discord.MemberUpdateEvent{Member: mem("9", "r")})
	l.Observe(discord.MemberLeaveEvent{ServerID: "1", User: discord.User{ID: "30"}})
	l.Observe(discord.MessageCreateEvent{}) // ignored
	got, _ = l.List(ctx, cl, "1")
	if want := []string{"9", "50", "100"}; !slices.Equal(ids(got), want) || !got[0].HasRole("r") || cl.calls.Load() != 1 {
		t.Fatalf("patched = %v (calls %d), want %v", ids(got), cl.calls.Load(), want)
	}
	// After the TTL, the server is listed again.
	now = now.Add(memberListTTL)
	if _, err := l.List(ctx, cl, "1"); err != nil || cl.calls.Load() != 2 {
		t.Fatalf("after TTL: calls = %d, %v", cl.calls.Load(), err)
	}
	// Leaving the server drops the listing.
	l.Observe(discord.ServerLeaveEvent{ServerID: "1"})
	if _, err := l.List(ctx, cl, "1"); err != nil || cl.calls.Load() != 3 {
		t.Fatalf("after leave: calls = %d, %v", cl.calls.Load(), err)
	}
}

func TestMemberListErrorsAreNotCached(t *testing.T) {
	l := newMemberList()
	cl := &listerClient{err: discord.ErrForbidden}
	for range 2 {
		if _, err := l.List(context.Background(), cl, "1"); !errors.Is(err, discord.ErrForbidden) {
			t.Fatalf("err = %v", err)
		}
	}
	if cl.calls.Load() != 2 {
		t.Fatalf("calls = %d", cl.calls.Load())
	}
	if _, err := l.List(context.Background(), struct{ discord.Client }{}, "1"); err == nil {
		t.Fatal("a client without ListMembers must fail")
	}
}

func TestMemberListSharesOneListing(t *testing.T) {
	l := newMemberList()
	cl := &listerClient{gate: make(chan struct{}), started: make(chan struct{}, 8), members: []discord.Member{mem("1"), mem("2")}}
	var wg sync.WaitGroup
	results := make([][]string, 5)
	for i := range results {
		wg.Go(func() {
			ms, err := l.List(context.Background(), cl, "1")
			if err != nil {
				t.Error(err)
			}
			results[i] = ids(ms)
		})
	}
	<-cl.started
	// Events seen while the listing is in flight are replayed onto it.
	l.Observe(discord.MemberJoinEvent{Member: mem("3")})
	l.Observe(discord.MemberLeaveEvent{ServerID: "1", User: discord.User{ID: "1"}})
	// A caller that gives up does not cancel the listing.
	short, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.List(short, cl, "1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller: %v", err)
	}
	close(cl.gate)
	wg.Wait()
	for _, r := range results {
		if !slices.Equal(r, []string{"2", "3"}) {
			t.Fatalf("results = %v", results)
		}
	}
	if cl.calls.Load() != 1 {
		t.Fatalf("calls = %d", cl.calls.Load())
	}
}
