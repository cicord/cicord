package analytics

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// voiceSession is a member's current voice connection (memory only).
type voiceSession struct {
	channel string
	stage   bool
	since   time.Time // time credited up to
	media   bool      // streaming or camera on
	day     string    // day the member was last marked active in voice
}

// voiceUpdate applies a voice state change. Caller holds c.mu.
func (c *Collector) voiceUpdate(s *collected, st discord.VoiceState, now time.Time) {
	if st.Bot || c.botUsers[st.UserID] {
		return // music bots and the like are not members in voice
	}
	channel := st.ChannelID
	if channel != "" && s.cfg.AFKChannelID != nil && channel == s.cfg.AFKChannelID() {
		channel = "" // the AFK channel is not activity
	}
	old := s.voice[st.UserID]
	if old != nil {
		c.credit(s, old, now)
		if channel == "" || channel != old.channel {
			delete(s.voice, st.UserID)
			old = nil
		}
	}
	if channel != "" && old == nil {
		vs := c.openSession(s, st.UserID, channel, now)
		if !vs.stage {
			c.count(s, VoiceSessions, channel, now, 1)
		}
		old = vs
	}
	if old != nil {
		old.media = st.SelfStream || st.SelfVideo
	}
	c.notePeak(s, now)
}

func (c *Collector) openSession(s *collected, userID, channel string, now time.Time) *voiceSession {
	vs := &voiceSession{channel: channel, since: now, day: dayOf(now)}
	if s.cfg.Channel != nil {
		if ch, ok := s.cfg.Channel(channel); ok && ch.Type == discord.ChannelStage {
			vs.stage = true
		}
	}
	s.voice[userID] = vs
	c.markActive(s, userID, now, ActiveSetVoice, ActiveSetAll)
	return vs
}

// applySnapshot replaces the voice sessions and presences with a server's GUILD_CREATE
// content (after a connect, a reconnect or a restart). Sessions in the same channel
// continue; none counts as a new session. Caller holds c.mu.
func (c *Collector) applySnapshot(s *collected, snap discord.ServerAvailableEvent, now time.Time) {
	in := map[string]discord.VoiceState{}
	for _, st := range snap.VoiceStates {
		if st.Bot || c.botUsers[st.UserID] || st.ChannelID == "" {
			continue
		}
		if s.cfg.AFKChannelID != nil && st.ChannelID == s.cfg.AFKChannelID() {
			continue
		}
		in[st.UserID] = st
	}
	for uid, vs := range s.voice {
		c.credit(s, vs, now)
		if st, ok := in[uid]; !ok || st.ChannelID != vs.channel {
			delete(s.voice, uid)
		}
	}
	for uid, st := range in {
		vs := s.voice[uid]
		if vs == nil {
			vs = c.openSession(s, uid, st.ChannelID, now)
		}
		vs.media = st.SelfStream || st.SelfVideo
	}
	c.notePeak(s, now)
	if s.cfg.presenceCollected() {
		s.presence = map[string]presenceInfo{}
		for _, p := range snap.Presences {
			if info, ok := presenceFrom(p); ok {
				s.presence[p.UserID] = info
			}
		}
	}
}

// credit adds the time of a session since its last credit, split across the hours it
// covers. Caller holds c.mu.
func (c *Collector) credit(s *collected, vs *voiceSession, now time.Time) {
	metric := VoiceSeconds
	if vs.stage {
		metric = StageSeconds
	}
	for t := vs.since; t.Before(now); {
		end := hourOf(t).Add(time.Hour)
		if end.After(now) {
			end = now
		}
		secs := end.Sub(t).Seconds()
		c.count(s, metric, vs.channel, t, secs)
		if vs.media {
			c.count(s, VoiceStreaming, vs.channel, t, secs)
		}
		t = end
	}
	if now.After(vs.since) {
		vs.since = now
	}
}

// endVoice credits every open session (the server stops being collected). Caller holds c.mu.
func (c *Collector) endVoice(s *collected, now time.Time) {
	if !s.cfg.Enabled {
		return
	}
	for _, vs := range s.voice {
		c.credit(s, vs, now)
	}
}

// notePeak records the members in voice now into the hour's peak. Caller holds c.mu.
func (c *Collector) notePeak(s *collected, now time.Time) {
	c.gauge(s, state.AnalyticsGaugeMax, VoicePeak, "", now, float64(len(s.voice)))
}

// ───────────────────────────────────────────────────────────── presence

// presenceInfo is a member's aggregated presence (memory only).
type presenceInfo struct {
	status     string
	platforms  []string
	activities []string
}

var presenceStatuses = []string{"online", "idle", "dnd"}

// presenceFrom keeps what the presence aggregates need; false when the member is offline.
func presenceFrom(p discord.MemberPresence) (presenceInfo, bool) {
	switch p.Status {
	case "online", "idle", "dnd":
	default:
		return presenceInfo{}, false
	}
	info := presenceInfo{status: p.Status}
	for _, pl := range []struct{ name, status string }{{"desktop", p.Desktop}, {"mobile", p.Mobile}, {"web", p.Web}} {
		if pl.status != "" && pl.status != "offline" {
			info.platforms = append(info.platforms, pl.name)
		}
	}
	for _, a := range p.Activities {
		name := strings.TrimSpace(a.Name)
		// Custom statuses are free text written by the member: never a dimension.
		if a.Type == "custom" || a.Type == "" || name == "" {
			continue
		}
		if r := []rune(name); len(r) > 100 {
			name = string(r[:100])
		}
		dim := a.Type + ":" + name
		if !slices.Contains(info.activities, dim) {
			info.activities = append(info.activities, dim)
		}
	}
	return info, true
}

// presenceGauges records the presence aggregates of a server. Caller holds c.mu.
func (c *Collector) presenceGauges(s *collected, now time.Time) {
	status := map[string]int{}
	platform := map[string]int{"desktop": 0, "mobile": 0, "web": 0}
	activity := map[string]int{}
	for _, p := range s.presence {
		status[p.status]++
		for _, pl := range p.platforms {
			platform[pl]++
		}
		for _, a := range p.activities {
			activity[a]++
		}
	}
	for _, st := range presenceStatuses {
		c.gauge(s, state.AnalyticsGaugeLast, PresenceStatus, st, now, float64(status[st]))
	}
	for pl, n := range platform {
		c.gauge(s, state.AnalyticsGaugeLast, PresencePlatform, pl, now, float64(n))
	}
	type kv struct {
		dim string
		n   int
	}
	top := make([]kv, 0, len(activity))
	for d, n := range activity {
		top = append(top, kv{d, n})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].n != top[j].n {
			return top[i].n > top[j].n
		}
		return top[i].dim < top[j].dim
	})
	if len(top) > topActivities {
		top = top[:topActivities]
	}
	for _, a := range top {
		c.gauge(s, state.AnalyticsGaugeLast, PresenceActivity, a.dim, now, float64(a.n))
	}
}
