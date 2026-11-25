package runtime

import (
	"strings"
	"sync"
)

// Change topics: what the admin console has to refetch (GET /api/v1/events).
const (
	// ChangeStatus: the runtime status (revision, bots, servers' health, last error).
	ChangeStatus = "status"
	// ChangeSync: a sync record (history, a server's sync state and drift), or drift seen in a
	// gateway event (with the server: its sync state, plan, resources and drift).
	ChangeSync = "sync"
	// ChangePlugins: a plugin instance's health (crash, restart).
	ChangePlugins = "plugins"
	// ChangeAnalytics: new analytics points were written for a server.
	ChangeAnalytics = "analytics"
)

// Change tells that something the admin console shows changed. It carries no content:
// clients refetch what they display, with their own access checks.
type Change struct {
	Topic  string
	Server string // server key; "" for the runtime as a whole
}

// changeHub fans changes out to subscribers. A slow subscriber loses changes rather
// than blocking the runtime (changes are refetch hints: the next one still arrives).
type changeHub struct {
	mu   sync.Mutex
	next int
	subs map[int]chan Change
}

// SubscribeChanges returns a channel of changes and a function that ends the
// subscription (and closes the channel).
func (r *Runtime) SubscribeChanges() (<-chan Change, func()) {
	h := &r.changes
	ch := make(chan Change, 64)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[int]chan Change{}
	}
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// notify publishes a change without blocking.
func (r *Runtime) notify(topic, server string) {
	if topic == ChangeStatus || topic == ChangePlugins {
		r.recheckCommitSoon()
	}
	h := &r.changes
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- Change{Topic: topic, Server: server}:
		default:
		}
	}
}

// notifySync publishes the change of a sync record ("server:<key>", "bot:<key>" or
// "revision" target).
func (r *Runtime) notifySync(target string) {
	server, _ := strings.CutPrefix(target, "server:")
	if server == target {
		server = ""
	}
	r.notify(ChangeSync, server)
	if server == "" {
		r.notify(ChangeStatus, "")
	}
}
