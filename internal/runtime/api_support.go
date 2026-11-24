package runtime

import (
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// Accessors used by the HTTP API (internal/api). They only read the active revision.

// ServerConfig returns the Server resource of the active revision.
func (r *Runtime) ServerConfig(key string) (*config.Server, bool) {
	a := r.Active()
	if a == nil || a.servers[key] == nil {
		return nil, false
	}
	return a.servers[key].cfg, true
}

// ServerInfo returns the live Discord server (owner, name, icon) from the cache. ok is
// false when the server is unknown or has not been fetched yet.
func (r *Runtime) ServerInfo(key string) (discord.Server, bool) {
	a := r.Active()
	if a == nil || a.servers[key] == nil {
		return discord.Server{}, false
	}
	s := a.servers[key].cache.Server()
	return s, s.ID != ""
}

// ResourceID resolves a resource key of a server to its Discord ID (resource map).
func (r *Runtime) ResourceID(serverKey string, kind state.ResourceKind, key string) (string, bool) {
	a := r.Active()
	if a == nil || a.servers[serverKey] == nil {
		return "", false
	}
	return a.servers[serverKey].cache.ID(kind, key)
}

// ServerKeyByDiscordID returns the key of the server with that Discord ID.
func (r *Runtime) ServerKeyByDiscordID(id string) (string, bool) {
	a := r.Active()
	if a == nil {
		return "", false
	}
	for k, s := range a.servers {
		if s.cfg.Spec.DiscordID == id {
			return k, true
		}
	}
	return "", false
}

// ServerKeys returns the server keys of the active revision, sorted.
func (r *Runtime) ServerKeys() []string {
	a := r.Active()
	if a == nil {
		return nil
	}
	return sortedKeys(a.servers)
}

// Ready reports whether a revision is active.
func (r *Runtime) Ready() bool { return r.Active() != nil }

// Simulator reports whether the connector is the Discord simulator.
func (r *Runtime) Simulator() bool { return r.opts.Simulator }

// AnyClient returns a Discord client of any connected bot (for user lookups).
func (r *Runtime) AnyClient() (discord.Client, bool) {
	for _, h := range r.hosts() {
		return h.session, true
	}
	return nil, false
}
