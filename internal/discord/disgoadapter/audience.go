package disgoadapter

import (
	"maps"
	"slices"
	"sync"

	"github.com/disgoorg/disgo/gateway"

	"github.com/cicord/cicord/internal/discord"
)

// One ordered gateway stream owns the ACLs of this bot. A REST refresh and a
// delayed event from another bot cannot roll back an authorization decision.
type audienceCache struct {
	mu        sync.RWMutex
	sessionID string
	sequence  int
	guilds    map[string]*guildPolicy
}

type guildPolicy struct {
	owner    string
	roles    map[string]discord.Role
	channels map[string]discord.Channel
}

func (p *guildPolicy) putChannel(ch discord.Channel) {
	// Threads use their parent's policy. Ordinary guild channels must supply
	// the overwrite list, including an explicit empty list for a public one.
	if !ch.Type.IsThread() && ch.Overwrites == nil {
		delete(p.channels, ch.ID)
		return
	}
	p.channels[ch.ID] = ch
}

func (c *audienceCache) replace(g wireGuildAvailable) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.guilds == nil {
		c.guilds = make(map[string]*guildPolicy)
	}
	delete(c.guilds, g.ID)
	if g.OwnerID == "" || g.Roles == nil || g.Channels == nil {
		return
	}
	p := &guildPolicy{owner: g.OwnerID, roles: map[string]discord.Role{}, channels: map[string]discord.Channel{}}
	for _, r := range g.Roles {
		p.roles[r.ID] = roleFromWire(r)
	}
	for _, list := range [][]wireChannel{g.Channels, g.Threads} {
		for _, ch := range list {
			ch.GuildID = g.ID
			p.putChannel(channelFromWire(ch))
		}
	}
	c.guilds[g.ID] = p
}

func (c *audienceCache) drop(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.guilds, id)
}

// Only malformed guild policy dispatches can invalidate authorization data.
// A malformed message must not disable unrelated guilds' permission checks.
func policyDispatch(kind gateway.EventType) bool {
	switch kind {
	case "READY", "GUILD_CREATE", "GUILD_DELETE", "GUILD_UPDATE",
		"GUILD_ROLE_CREATE", "GUILD_ROLE_DELETE", "GUILD_ROLE_UPDATE",
		"CHANNEL_CREATE", "CHANNEL_DELETE", "CHANNEL_UPDATE", "THREAD_CREATE", "THREAD_DELETE", "THREAD_UPDATE":
		return true
	}
	return false
}

func (c *audienceCache) observe(sessionID string, sequence int, events []discord.Event, invalid bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if invalid || c.sessionID != sessionID {
		c.guilds = nil
	}
	c.sessionID, c.sequence = sessionID, sequence
	for _, ev := range events {
		if _, ready := ev.(discord.ReadyEvent); ready {
			c.guilds = nil
			continue
		}
		p := c.guilds[discord.ServerIDOf(ev)]
		if p == nil {
			continue
		}
		switch e := ev.(type) {
		case discord.ChannelCreateEvent:
			p.putChannel(e.Channel)
		case discord.ChannelUpdateEvent:
			p.putChannel(e.Channel)
		case discord.ChannelDeleteEvent:
			delete(p.channels, e.Channel.ID)
		case discord.RoleCreateEvent:
			p.roles[e.Role.ID] = e.Role
		case discord.RoleUpdateEvent:
			p.roles[e.Role.ID] = e.Role
		case discord.RoleDeleteEvent:
			delete(p.roles, e.RoleID)
		case discord.ServerUpdateEvent:
			p.owner = e.Server.OwnerID
		}
	}
}

// AudienceSnapshot returns no policy while disconnected, catching up, or
// awaiting GUILD_CREATE after a fresh READY. On RESUMED the replayed ACL events
// have already been applied on the reader before the session becomes usable.
func (s *session) AudienceSnapshot(serverID string) discord.AudienceSnapshot {
	return s.guildPolicySnapshot(serverID, true)
}

func (s *session) GuildOwnerID(serverID string) string {
	return s.guildPolicySnapshot(serverID, false).OwnerID
}

func (s *session) guildPolicySnapshot(serverID string, includeResources bool) discord.AudienceSnapshot {
	s.mu.Lock()
	gw := s.gw
	s.mu.Unlock()
	if gw == nil {
		return discord.AudienceSnapshot{}
	}
	c := &s.audiences
	c.mu.RLock()
	defer c.mu.RUnlock()
	p := c.guilds[serverID]
	if p == nil || p.owner == "" || gw.Status() != gateway.StatusReady {
		return discord.AudienceSnapshot{}
	}
	id, seq := gw.SessionID(), gw.LastSequenceReceived()
	if id == nil || *id != c.sessionID || seq == nil || *seq != c.sequence {
		return discord.AudienceSnapshot{}
	}
	if !includeResources {
		return discord.AudienceSnapshot{OwnerID: p.owner}
	}
	return discord.AudienceSnapshot{OwnerID: p.owner, Roles: slices.Collect(maps.Values(p.roles)), Channels: maps.Clone(p.channels)}
}
