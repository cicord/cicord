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

	refreshMu sync.Mutex
	// While a REST refresh is in progress, retain the latest gateway mutation
	// for each resource so that installing the older snapshot cannot undo it.
	duringRefresh map[string]discord.Event
	mu            sync.RWMutex
	keyToID       map[state.ResourceKind]map[string]string
	idToKey       map[string]resourceRef
	channels      map[string]discord.Channel
	roles         map[string]discord.Role
	server        discord.Server

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
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if cl != nil {
		c.mu.Lock()
		c.duringRefresh = make(map[string]discord.Event)
		c.mu.Unlock()
		defer func() {
			c.mu.Lock()
			c.duringRefresh = nil
			c.mu.Unlock()
		}()
	}
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
		for _, ev := range c.duringRefresh {
			c.applyEvent(ev)
		}
	}
	return nil
}

// previousEvent captures the live state from before the gateway update, when known.
func (c *serverCache) previousEvent(ev discord.Event) discord.Event {
	c.mu.RLock()
	defer c.mu.RUnlock()
	switch e := ev.(type) {
	case discord.ChannelUpdateEvent:
		if prev, ok := c.channels[e.Channel.ID]; ok {
			e.Previous = &prev
		}
		return e
	case discord.RoleUpdateEvent:
		if prev, ok := c.roles[e.Role.ID]; ok {
			e.Previous = &prev
		}
		return e
	case discord.RoleDeleteEvent:
		if prev, ok := c.roles[e.RoleID]; ok {
			e.Previous = &prev
		}
		return e
	case discord.ServerUpdateEvent:
		if c.server.ID != "" {
			prev := c.server
			e.Previous = &prev
		}
		return e
	default:
		return ev
	}
}

// Observe updates live resources from gateway events.
func (c *serverCache) Observe(ev discord.Event) {
	c.members.Observe(ev)
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.applyEvent(ev)
	if c.duringRefresh != nil && key != "" {
		c.duringRefresh[key] = ev
	}
}

// applyEvent updates live resources and returns the ID changed. Snowflakes are
// unique across roles and channels. The server shares its ID with @everyone.
func (c *serverCache) applyEvent(ev discord.Event) string {
	switch e := ev.(type) {
	case discord.ChannelCreateEvent:
		c.channels[e.Channel.ID] = e.Channel
		return e.Channel.ID
	case discord.ChannelUpdateEvent:
		c.channels[e.Channel.ID] = e.Channel
		return e.Channel.ID
	case discord.ChannelDeleteEvent:
		delete(c.channels, e.Channel.ID)
		return e.Channel.ID
	case discord.RoleCreateEvent:
		c.roles[e.Role.ID] = e.Role
		return e.Role.ID
	case discord.RoleUpdateEvent:
		c.roles[e.Role.ID] = e.Role
		return e.Role.ID
	case discord.RoleDeleteEvent:
		delete(c.roles, e.RoleID)
		return e.RoleID
	case discord.ServerUpdateEvent:
		c.server = e.Server
		return "server"
	}
	return ""
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
