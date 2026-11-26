package api

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/runtime"
)

// countingConnector counts the Discord member reads of the sessions it opens.
type countingConnector struct {
	inner discord.Connector
	n     *atomic.Int64
	delay *atomic.Int64 // nanoseconds added to each member read
}

func (c countingConnector) Connect(ctx context.Context, token string) (discord.Session, error) {
	s, err := c.inner.Connect(ctx, token)
	if err != nil {
		return nil, err
	}
	return countingSession{s, c.n, c.delay}, nil
}

type countingSession struct {
	discord.Session
	n, delay *atomic.Int64
}

func (s countingSession) GetMember(ctx context.Context, server, user string) (discord.Member, error) {
	s.n.Add(1)
	time.Sleep(time.Duration(s.delay.Load()))
	return s.Session.GetMember(ctx, server, user)
}

// Portal reads go through the member cache (a member or a non-member costs one Discord
// read per TTL, not one per request); plugin routes re-read a member, and concurrent
// reads of the same member share one call.
func TestPortalMemberReadsAreBounded(t *testing.T) {
	var n, delay atomic.Int64
	e := startFull(t, func(dir string) {
		writeTestFile(t, filepath.Join(dir, "plugins", "onboarding", "src", "index.ts"), `import { definePlugin } from "@cicord/sdk";
export default definePlugin({ routes: {
  "GET /who": { access:"public", handler(ctx,req){return {member: !!req.viewer?.member};} }
}});`)
	}, func(o *runtime.Options) { o.Connector = countingConnector{o.Connector, &n, &delay} },
		func(o *Options) { o.MemberCacheTTL = time.Hour })
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	before := n.Load()
	for range 50 {
		owner.expect(200, "GET", "/api/v1/portal/"+serverKey+"/me", nil)
	}
	if got := n.Load() - before; got > 1 {
		t.Fatalf("50 portal /me cost %d member reads", got)
	}

	outsider := e.world.CreateUser("outsider")
	stranger := e.client()
	stranger.login(outsider.ID)
	before = n.Load()
	for range 50 {
		stranger.expect(403, "GET", "/api/v1/portal/"+serverKey+"/me", nil)
	}
	route := "/api/v1/portal/" + serverKey + "/plugins/moderator/onboarding/who"
	for range 5 {
		stranger.expect(200, "GET", route, nil)
	}
	if got := n.Load() - before; got > 1 {
		t.Fatalf("a non-member cost %d member reads", got)
	}

	// Plugin routes read the member now (SDK-23).
	before = n.Load()
	owner.expect(200, "GET", route, nil)
	owner.expect(200, "GET", route, nil)
	if got := n.Load() - before; got != 2 {
		t.Fatalf("plugin routes made %d member reads, want 2", got)
	}

	// Concurrent fresh reads of one member share a Discord call.
	delay.Store(int64(200 * time.Millisecond))
	defer delay.Store(0)
	before = n.Load()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { owner.do("GET", route, nil) })
	}
	wg.Wait()
	if got := n.Load() - before; got >= 8 {
		t.Fatalf("8 concurrent requests made %d member reads", got)
	}
}

// One signed-in user cannot flood the portal: past the burst it answers 429 with
// Retry-After, and other users are not affected.
func TestPortalRateLimitPerUser(t *testing.T) {
	e := startWith(t, func(o *Options) { o.PortalRequestsPerMinute = 10 })
	me := "/api/v1/portal/" + serverKey + "/me"
	a := e.client()
	a.login(e.seed.Owner.ID)
	codes := map[int]int{}
	var last resp
	for range 8 {
		last = a.do("GET", me, nil)
		codes[last.status]++
	}
	if codes[200] != 5 || codes[429] != 3 || last.errCode() != "rate_limited" || last.header.Get("Retry-After") == "" {
		t.Fatalf("statuses %v, last %d %s %v", codes, last.status, last.body, last.header)
	}
	b := e.client()
	b.login(e.moderator().ID)
	b.expect(200, "GET", me, nil)
}

func TestUserLimiterRefills(t *testing.T) {
	l := newUserLimiter(60) // burst 30, one per second
	now := time.Unix(0, 0)
	for i := range 30 {
		if !l.allow("u", now) {
			t.Fatalf("request %d refused inside the burst", i)
		}
	}
	if l.allow("u", now) {
		t.Fatal("burst exceeded")
	}
	if !l.allow("u", now.Add(time.Second)) || l.allow("u", now.Add(time.Second)) {
		t.Fatal("refill is not one per second")
	}
	if !l.allow("v", now) {
		t.Fatal("users share a bucket")
	}
	if newUserLimiter(-1).allow("u", now) != true {
		t.Fatal("a disabled limiter refuses")
	}
	for i := range 10050 {
		l.allow(fmt.Sprint("user-", i), now.Add(time.Hour))
	}
	if len(l.buckets) > 10001 {
		t.Fatalf("%d buckets kept", len(l.buckets))
	}
}
