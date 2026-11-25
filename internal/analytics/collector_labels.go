package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// Remembered labels (state.AnalyticsLabels): the collector keeps the last known name
// of the channels, roles and scheduled events it counts, so that a deleted one keeps
// its name in the dashboard, and the owner of the channels a plugin created, so that
// the API groups them (groups.go) even after they are deleted.
//
// Cost: no REST call. A label is read from the runtime's live cache the first time
// a resource has activity in the day (later activity that day is a map lookup), and
// taken from the gateway's channel and role create, update and delete events; it is
// written at the next flush only when it changed since it was last written. Once a
// day, after retention, PruneLabels drops the labels not seen within the retention
// and those whose dimension has no point left; the memory of what was written is
// then reset, so the resources still active are written again and stay fresh.

// maxPendingLabels bounds the labels waiting to be written when the store keeps failing.
const maxPendingLabels = 20_000

// labelDims maps the catalog metrics whose dimension is a Discord resource to that
// dimension ("channel", "role", "event").
var labelDims = func() map[string]string {
	m := map[string]string{}
	for _, x := range Catalog {
		if storedDims[x.Dim] {
			m[x.Name] = x.Dim
		}
	}
	return m
}()

type labelKey struct{ dim, id string }

// signature is what makes a label worth writing again.
func signature(l state.AnalyticsLabel) string {
	d := "0"
	if l.Deleted {
		d = "1"
	}
	return l.Name + "\x00" + l.Type + "\x00" + l.Color + "\x00" + l.Owner + "\x00" + d
}

// noteActivity remembers the label of a resource that has activity, once a day.
// Caller holds c.mu.
func (c *Collector) noteActivity(s *collected, metric, id string) {
	dim, ok := labelDims[metric]
	if !ok || id == "" || id == OtherDim {
		return
	}
	k := labelKey{dim, id}
	if _, done := s.noted[k]; done {
		return
	}
	var l state.AnalyticsLabel
	switch dim {
	case "channel":
		if s.cfg.Channel == nil {
			return
		}
		ch, ok := s.cfg.Channel(id)
		if !ok {
			return // unknown to the cache: maybe labelled by an event later
		}
		l = channelLabel(ch)
	case "role":
		if s.cfg.Role == nil {
			return
		}
		ro, ok := s.cfg.Role(id)
		if !ok {
			return
		}
		l = roleLabel(ro, false)
	default:
		return // scheduled events: labelled by the samples
	}
	s.noted[k] = struct{}{}
	c.pendLabel(s, l)
}

// labelEvent remembers labels from the gateway's channel and role events. It runs
// before the runtime's cache sees the event (botHost.handle calls Handle first): on
// a role deletion, which carries only the ID, the cache still has the role's name.
// Caller holds c.mu.
func (c *Collector) labelEvent(s *collected, ev discord.Event) {
	switch e := ev.(type) {
	case discord.ChannelCreateEvent:
		c.pendLabel(s, channelLabel(e.Channel))
	case discord.ChannelUpdateEvent:
		c.pendLabel(s, channelLabel(e.Channel))
	case discord.ChannelDeleteEvent:
		l := channelLabel(e.Channel)
		l.Deleted = true
		c.pendLabel(s, l)
	case discord.RoleCreateEvent:
		c.pendLabel(s, roleLabel(e.Role, false))
	case discord.RoleUpdateEvent:
		c.pendLabel(s, roleLabel(e.Role, false))
	case discord.RoleDeleteEvent:
		if s.cfg.Role == nil {
			return
		}
		if ro, ok := s.cfg.Role(e.RoleID); ok {
			c.pendLabel(s, roleLabel(ro, true))
		}
	}
}

func channelLabel(ch discord.Channel) state.AnalyticsLabel {
	kind := "channel"
	switch ch.Type {
	case discord.ChannelVoice, discord.ChannelStage:
		kind = "voice"
	case discord.ChannelPublicThread, discord.ChannelPrivateThread, discord.ChannelAnnouncementThread:
		kind = "thread"
	}
	return state.AnalyticsLabel{Dim: "channel", ID: ch.ID, Name: ch.Name, Type: kind}
}

func roleLabel(ro discord.Role, deleted bool) state.AnalyticsLabel {
	l := state.AnalyticsLabel{Dim: "role", ID: ro.ID, Name: ro.Name, Type: "role", Deleted: deleted}
	if ro.Color != 0 {
		l.Color = colorHex(ro.Color)
	}
	return l
}

// colorHex formats a Discord role color as "#rrggbb".
func colorHex(c int) string { return fmt.Sprintf("#%06x", c&0xffffff) }

// pendLabel queues a label for the next flush, with the channel's owner when the
// cache knows it (looked up again at the flush). Caller holds c.mu.
func (c *Collector) pendLabel(s *collected, l state.AnalyticsLabel) {
	if l.ID == "" || l.Name == "" {
		return
	}
	k := labelKey{l.Dim, l.ID}
	old, ok := s.labels[k]
	if !ok && c.npendingLabels >= maxPendingLabels {
		c.log.Warn("analytics: too many labels waiting, dropping one", "server", s.cfg.Key, "dim", l.Dim)
		return
	}
	l.Server = s.cfg.Key
	if l.Dim == "channel" {
		l.Owner = c.channelOwner(s, l.ID)
	}
	if l.Owner == "" {
		l.Owner = old.Owner
	}
	if !ok {
		c.npendingLabels++
	}
	s.labels[k] = l
}

// channelOwner is the plugin instance that created a channel, from the cache ("" when
// unknown or not a plugin's). Caller holds c.mu.
func (c *Collector) channelOwner(s *collected, id string) string {
	if s.cfg.Owner == nil {
		return ""
	}
	if o := s.cfg.Owner(id); PluginOwner(o) {
		return o
	}
	return ""
}

// takeLabels empties the pending labels, with the owners looked up again. Caller
// holds c.mu.
func (c *Collector) takeLabels(now time.Time) []state.AnalyticsLabel {
	var out []state.AnalyticsLabel
	for _, s := range c.servers {
		for _, l := range s.labels {
			if l.Dim == "channel" && l.Owner == "" {
				l.Owner = c.channelOwner(s, l.ID)
			}
			l.SeenAt = now
			out = append(out, l)
		}
		if len(s.labels) > 0 {
			s.labels = map[labelKey]state.AnalyticsLabel{}
		}
	}
	c.npendingLabels = 0
	return out
}

// flushLabels writes the labels that changed since they were last written. A failing
// store keeps them for the next flush (bounded). Caller holds tickMu.
func (c *Collector) flushLabels(ctx context.Context, labels []state.AnalyticsLabel) {
	store, ok := c.opts.Store.(state.AnalyticsLabels)
	if !ok || len(labels) == 0 {
		return
	}
	var todo []state.AnalyticsLabel
	for _, l := range labels {
		if c.labeled[l.Server][labelKey{l.Dim, l.ID}] != signature(l) {
			todo = append(todo, l)
		}
	}
	if len(todo) == 0 {
		return
	}
	if err := store.PutLabels(ctx, todo...); err != nil {
		c.log.Warn("analytics: record labels", "labels", len(todo), "err", err)
		c.mu.Lock()
		for _, l := range todo {
			if s := c.byKey[l.Server]; s != nil {
				if _, newer := s.labels[labelKey{l.Dim, l.ID}]; !newer && c.npendingLabels < maxPendingLabels {
					s.labels[labelKey{l.Dim, l.ID}] = l
					c.npendingLabels++
				}
			}
		}
		c.mu.Unlock()
		return
	}
	for _, l := range todo {
		if c.labeled[l.Server] == nil {
			c.labeled[l.Server] = map[labelKey]string{}
		}
		c.labeled[l.Server][labelKey{l.Dim, l.ID}] = signature(l)
	}
}

// pruneLabels applies the retention to a server's labels and forgets what was
// written for it, so the resources still active are written again (their SeenAt
// advances). Caller holds tickMu.
func (c *Collector) pruneLabels(ctx context.Context, server string, before time.Time) {
	store, ok := c.opts.Store.(state.AnalyticsLabels)
	if !ok {
		return
	}
	n, err := store.PruneLabels(ctx, server, before)
	if err != nil {
		c.log.Warn("analytics: labels retention", "server", server, "err", err)
		return
	}
	delete(c.labeled, server)
	c.mu.Lock()
	if s := c.byKey[server]; s != nil {
		s.noted = map[labelKey]struct{}{}
	}
	c.mu.Unlock()
	if n > 0 {
		c.log.Info("analytics: labels retention applied", "server", server, "removed", n)
	}
}
