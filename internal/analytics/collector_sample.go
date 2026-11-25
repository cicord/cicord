package analytics

import (
	"context"
	"errors"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// Sample reads a server's counts from Discord and records the gauges: approximate
// member and online counts, boosts and boost level, interested members of scheduled
// events, invite uses (diffed with the previous list), the presence aggregates and
// the members in voice. It refreshes the bot's permissions for Status. At most four
// REST calls (counts, events, invites, vanity URL), plus the bot member when the
// permissions are computed from it.
func (c *Collector) Sample(ctx context.Context, server string) {
	c.mu.Lock()
	s := c.byKey[server]
	if s == nil || !s.cfg.Enabled || !s.cfg.Connected || s.cfg.Client == nil {
		c.mu.Unlock()
		return
	}
	cfg := s.cfg
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var (
		counts    *discord.ServerCounts
		events    []discord.ScheduledEventInterest
		eventsErr error
	)
	reader, _ := cfg.Client.(discord.AnalyticsReader)
	if reader != nil {
		if n, err := reader.GetServerCounts(ctx, cfg.ID); err != nil {
			c.log.Warn("analytics: server counts", "server", cfg.Key, "err", err)
		} else {
			counts = &n
		}
		if events, eventsErr = reader.ListScheduledEventInterest(ctx, cfg.ID); eventsErr != nil {
			c.log.Warn("analytics: scheduled events", "server", cfg.Key, "err", eventsErr)
		}
	}
	// The bot members: the whole member list (1000 per request), at most once a day.
	var bots []string
	listBots := false
	if reader != nil && cfg.Intents.Has(intentMembers) {
		c.mu.Lock()
		listBots = s.botsAt.IsZero() || c.now().Sub(s.botsAt) >= BotsRefresh
		c.mu.Unlock()
	}
	if listBots {
		var err error
		if bots, err = reader.ListServerBots(ctx, cfg.ID); err != nil {
			c.log.Warn("analytics: bot members", "server", cfg.Key, "err", err)
			listBots = false
		}
	}
	var (
		perms    discord.Permissions
		permsErr = errors.New("unknown")
	)
	if cfg.Permissions != nil {
		perms, permsErr = cfg.Permissions(ctx)
	}
	c.refreshInvites(ctx, s)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byKey[server] != s {
		return // reconfigured meanwhile
	}
	now := c.now()
	if permsErr == nil {
		s.perms, s.permsKnown = perms, true
	}
	if listBots {
		s.bots, s.botsAt = make(map[string]bool, len(bots)), now
		for _, id := range bots {
			s.bots[id] = true
			delete(s.presence, id)
		}
	}
	if counts != nil {
		humans, online := counts.ApproximateMembers, counts.ApproximateOnline
		if s.bots != nil {
			// Discord's counts include the bots. Online without detailed presence: most
			// bot members keep a gateway connection, so all of them are taken as online.
			humans = max(0, humans-len(s.bots))
			online = max(0, online-len(s.bots))
			c.gauge(s, state.AnalyticsGaugeLast, MembersBots, "", now, float64(len(s.bots)))
		}
		if s.cfg.presenceCollected() {
			online = len(s.presence) // bots excluded, exact
		}
		c.gauge(s, state.AnalyticsGaugeLast, MembersTotal, "", now, float64(humans))
		c.gauge(s, state.AnalyticsGaugeLast, MembersOnline, "", now, float64(online))
		c.gauge(s, state.AnalyticsGaugeLast, MembersBoosting, "", now, float64(counts.Boosts))
		c.gauge(s, state.AnalyticsGaugeLast, ServerBoostTier, "", now, float64(counts.BoostTier))
	}
	for _, e := range events {
		if e.Status == "scheduled" || e.Status == "active" {
			c.gauge(s, state.AnalyticsGaugeLast, EventsInterest, e.ID, now, float64(e.Interested))
			c.pendLabel(s, state.AnalyticsLabel{Dim: "event", ID: e.ID, Name: e.Name, Type: "event"})
		}
	}
	if s.cfg.presenceCollected() {
		c.presenceGauges(s, now)
	}
	c.notePeak(s, now)
}

// ───────────────────────────────────────────────────────────── invites

// inviteState is the last known use count of every invite of a server.
type inviteState struct {
	known   bool
	uses    map[string]discord.InviteUses // code → invite
	deleted map[string]time.Time          // code → deletion time (INVITE_DELETE)
	timer   *time.Timer                   // debounced fetch after joins
}

func (st *inviteState) noteDeleted(code string, now time.Time) {
	if st.deleted == nil {
		st.deleted = map[string]time.Time{}
	}
	st.deleted[code] = now
}

// inviteDim is the MembersInvitedBy dimension of an invite.
func inviteDim(i discord.InviteUses) string {
	if i.Vanity {
		return "vanity"
	}
	return i.Code
}

// DiffInvites returns the uses gained by each invite between two lists. A new code
// counts all its uses; a code that disappeared right after being deleted with one use
// left was used up by a join (Discord deletes it at its last use).
func DiffInvites(prev map[string]discord.InviteUses, cur []discord.InviteUses, deleted map[string]time.Time, since time.Time) map[string]int {
	out := map[string]int{}
	seen := map[string]bool{}
	for _, i := range cur {
		seen[i.Code] = true
		before := 0
		if p, ok := prev[i.Code]; ok {
			before = p.Uses
		}
		if d := i.Uses - before; d > 0 {
			out[inviteDim(i)] += d
		}
	}
	for code, p := range prev {
		if seen[code] || p.MaxUses == 0 || p.Uses+1 != p.MaxUses {
			continue
		}
		if at, ok := deleted[code]; ok && !at.Before(since) {
			out[inviteDim(p)]++
		}
	}
	return out
}

// scheduleInvites fetches the invites InviteDebounce after a join (one fetch for a
// burst of joins). Caller holds c.mu.
func (c *Collector) scheduleInvites(s *collected) {
	if s.invites.timer != nil || c.stopped || !s.cfg.Connected {
		return
	}
	if _, ok := s.cfg.Client.(discord.AnalyticsReader); !ok {
		return
	}
	c.wg.Add(1)
	s.invites.timer = time.AfterFunc(c.opts.InviteDebounce, func() {
		defer c.wg.Done()
		c.mu.Lock()
		s.invites.timer = nil
		c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c.refreshInvites(ctx, s)
	})
}

// refreshInvites lists the invites of a server and counts the uses gained since the
// previous list. The first list is the baseline.
func (c *Collector) refreshInvites(ctx context.Context, s *collected) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	c.mu.Lock()
	cfg := s.cfg
	fetchedAfter := c.now()
	c.mu.Unlock()
	reader, ok := cfg.Client.(discord.AnalyticsReader)
	if !ok || !cfg.Connected {
		return
	}
	list, err := reader.ListInviteUses(ctx, cfg.ID)

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if errors.Is(err, discord.ErrForbidden) {
		s.invitesForbidden = true
		return
	}
	if err != nil {
		c.log.Warn("analytics: list invites", "server", cfg.Key, "err", err)
		return
	}
	s.invitesForbidden = false
	if s.invites.known {
		// Deletions of the last few seconds explain invites used up by the joins.
		since := fetchedAfter.Add(-c.opts.InviteDebounce - 10*time.Second)
		for dim, n := range DiffInvites(s.invites.uses, list, s.invites.deleted, since) {
			c.count(s, MembersInvitedBy, dim, now, float64(n))
		}
	}
	s.invites.uses = make(map[string]discord.InviteUses, len(list))
	for _, i := range list {
		s.invites.uses[i.Code] = i
	}
	s.invites.known = true
	for code, at := range s.invites.deleted {
		if now.Sub(at) > time.Minute {
			delete(s.invites.deleted, code)
		}
	}
}
