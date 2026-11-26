package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

type memberEntry struct {
	member  *discord.Member // nil = not a member
	expires time.Time
}

// memberFetch is one Discord member read shared by concurrent lookups of the same key.
type memberFetch struct {
	done   chan struct{}
	member *discord.Member
	err    error
}

type memberCache struct {
	mu       sync.Mutex
	entries  map[string]memberEntry
	inflight map[string]*memberFetch
}

// member fetches (with a short cache) the Discord member of a user in a server; nil
// when the user is not in the server.
func (s *server) member(ctx context.Context, serverKey, userID string) (*discord.Member, error) {
	return s.lookupMember(ctx, serverKey, userID, false)
}

// lookupMember reads a member through the cache. fresh refuses a cached member (the
// caller must see roles and bans as they are now), but still accepts a cached "not a
// member": serving it can only deny access, and it keeps non-members from costing one
// Discord REST call per request. Concurrent lookups of one member share a single read.
func (s *server) lookupMember(ctx context.Context, serverKey, userID string, fresh bool) (*discord.Member, error) {
	key := serverKey + "/" + userID
	caching := s.opts.MemberCacheTTL > 0
	c := &s.members
	c.mu.Lock()
	if caching {
		if e, ok := c.entries[key]; ok && time.Now().Before(e.expires) && (!fresh || e.member == nil) {
			c.mu.Unlock()
			return e.member, nil
		}
	}
	f, joined := c.inflight[key]
	if !joined {
		f = &memberFetch{done: make(chan struct{})}
		if c.inflight == nil {
			c.inflight = map[string]*memberFetch{}
		}
		c.inflight[key] = f
	}
	c.mu.Unlock()
	if !joined {
		// The read outlives a caller that gives up: the others are still waiting for it.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		f.member, f.err = s.fetchMember(fetchCtx, serverKey, userID)
		cancel()
		c.mu.Lock()
		delete(c.inflight, key)
		if caching && f.err == nil {
			if len(c.entries) > 10000 {
				c.entries = map[string]memberEntry{}
			}
			c.entries[key] = memberEntry{member: f.member, expires: time.Now().Add(s.opts.MemberCacheTTL)}
		}
		c.mu.Unlock()
		close(f.done)
	}
	select {
	case <-f.done:
		return f.member, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// dropMember drops a cached member (a plugin route may have changed its roles).
func (s *server) dropMember(serverKey, userID string) {
	s.members.mu.Lock()
	delete(s.members.entries, serverKey+"/"+userID)
	s.members.mu.Unlock()
}

func (s *server) fetchMember(ctx context.Context, serverKey, userID string) (*discord.Member, error) {
	srv, ok := s.rt.ServerConfig(serverKey)
	if !ok {
		return nil, nil
	}
	cl, ok := s.rt.Client(serverKey)
	if !ok {
		return nil, httpErr(http.StatusServiceUnavailable, "unavailable", "no bot is connected to server %s", serverKey)
	}
	m, err := cl.GetMember(ctx, srv.Spec.DiscordID, userID)
	switch {
	case err == nil:
		return &m, nil
	case errors.Is(err, discord.ErrNotFound):
		return nil, nil
	default:
		return nil, fmt.Errorf("get member: %w", err)
	}
}

var errRateLimited = &errHTTP{status: http.StatusTooManyRequests, code: "rate_limited", msg: "too many portal requests: retry in a few seconds"}

// userLimiter is a token bucket per Discord user for portal requests: burst requests
// at once, then rate per second. A nil limiter allows everything.
type userLimiter struct {
	mu      sync.Mutex
	burst   float64
	rate    float64 // tokens per second
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newUserLimiter(perMinute int) *userLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &userLimiter{burst: max(float64(perMinute)/2, 1), rate: float64(perMinute) / 60, buckets: map[string]*bucket{}}
}

func (l *userLimiter) allow(user string, now time.Time) bool {
	if l == nil || user == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[user]
	if !ok {
		if len(l.buckets) > 10000 {
			// Full buckets carry no information: drop them rather than grow without bound.
			for k, v := range l.buckets {
				if v.tokens+now.Sub(v.at).Seconds()*l.rate >= l.burst {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) > 10000 {
				l.buckets = map[string]*bucket{}
			}
		}
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[user] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.rate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
