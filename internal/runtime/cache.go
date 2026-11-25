package runtime

import (
	"context"
	"sync"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// serverCache keeps, per server, the key ↔ Discord ID mapping (from the resource map)
// and a live copy of channels and roles (refreshed after syncs and kept current from
// gateway events). Plugins resolve `ctx.server.channel("general")` through it.
type serverCache struct {
	key string
	id  string

	mu       sync.RWMutex
	keyToID  map[state.ResourceKind]map[string]string
	idToKey  map[string]resourceRef
	channels map[string]discord.Channel
	roles    map[string]discord.Role
	server   discord.Server

	// members is the member listing of role.members() (its own lock).
	members *memberList
}

type resourceRef struct {
	kind  state.ResourceKind
	key   string
	owner string // state.Resource.Owner
}

func newServerCache(key, id string) *serverCache {
	return &serverCache{key: key, id: id,
		keyToID:  map[state.ResourceKind]map[string]string{},
		idToKey:  map[string]resourceRef{},
		channels: map[string]discord.Channel{},
		roles:    map[string]discord.Role{},
		members:  newMemberList(),
	}
}

// Refresh reloads mappings and live resources.
func (c *serverCache) Refresh(ctx context.Context, cl discord.Client, rm state.ResourceMap) error {
	res, err := rm.List(ctx, c.key)
	if err != nil {
		return err
	}
	var (
		chans []discord.Channel
		roles []discord.Role
		srv   discord.Server
	)
	if cl != nil {
		if chans, err = cl.ListChannels(ctx, c.id); err != nil {
			return err
		}
		if roles, err = cl.ListRoles(ctx, c.id); err != nil {
			return err
		}
		if srv, err = cl.GetServer(ctx, c.id); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keyToID = map[state.ResourceKind]map[string]string{}
	c.idToKey = map[string]resourceRef{}
	for _, r := range res {
		if c.keyToID[r.Kind] == nil {
			c.keyToID[r.Kind] = map[string]string{}
		}
		c.keyToID[r.Kind][r.Key] = r.ID
		c.idToKey[r.ID] = resourceRef{r.Kind, r.Key, r.Owner}
	}
	if cl != nil {
		c.channels = map[string]discord.Channel{}
		for _, ch := range chans {
			c.channels[ch.ID] = ch
		}
		c.roles = map[string]discord.Role{}
		for _, r := range roles {
			c.roles[r.ID] = r
		}
		c.server = srv
	}
	return nil
}

// Observe updates live resources from gateway events.
func (c *serverCache) Observe(ev discord.Event) {
	c.members.Observe(ev)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e := ev.(type) {
	case discord.ChannelCreateEvent:
		c.channels[e.Channel.ID] = e.Channel
	case discord.ChannelUpdateEvent:
		c.channels[e.Channel.ID] = e.Channel
	case discord.ChannelDeleteEvent:
		delete(c.channels, e.Channel.ID)
	case discord.RoleCreateEvent:
		c.roles[e.Role.ID] = e.Role
	case discord.RoleUpdateEvent:
		c.roles[e.Role.ID] = e.Role
	case discord.RoleDeleteEvent:
		delete(c.roles, e.RoleID)
	case discord.ServerUpdateEvent:
		c.server = e.Server
	}
}

// Remember records a mapping created at runtime (plugin-owned resources). It stays
// until the next Refresh, also once the plugin deletes the resource: the analytics
// collector reads the owner of a channel when it sees the channel deleted.
func (c *serverCache) Remember(kind state.ResourceKind, key, id, owner string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keyToID[kind] == nil {
		c.keyToID[kind] = map[string]string{}
	}
	c.keyToID[kind][key] = id
	c.idToKey[id] = resourceRef{kind, key, owner}
}

func (c *serverCache) ID(kind state.ResourceKind, key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.keyToID[kind][key]
	return id, ok
}

func (c *serverCache) Key(id string) (state.ResourceKind, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.idToKey[id]
	return r.kind, r.key, ok
}

// Owner returns the owner of a mapped resource ("" when unknown).
func (c *serverCache) Owner(id string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.idToKey[id].owner
}

func (c *serverCache) Channel(id string) (discord.Channel, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ch, ok := c.channels[id]
	return ch, ok
}

func (c *serverCache) Role(id string) (discord.Role, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.roles[id]
	return r, ok
}

func (c *serverCache) Server() discord.Server {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.server
}

// Snapshot returns copies of live roles and channels (for the API resources endpoint).
func (c *serverCache) Snapshot() ([]discord.Role, []discord.Channel) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	roles := make([]discord.Role, 0, len(c.roles))
	for _, r := range c.roles {
		roles = append(roles, r)
	}
	chans := make([]discord.Channel, 0, len(c.channels))
	for _, ch := range c.channels {
		chans = append(chans, ch)
	}
	return roles, chans
}
