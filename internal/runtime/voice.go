package runtime

import (
	"sort"
	"sync"

	"github.com/cicord/cicord/internal/discord"
)

var intentVoiceStates = discord.MustIntents("ServerVoiceStates")

// voiceCache keeps the voice states of a bot session's servers, for the plugins'
// server.voiceMembers() and the `previous` channel of voiceStateUpdate. It lives with the
// session (botHost), not with a revision: Discord only sends a server's voice states when
// it becomes available (GUILD_CREATE: after a connection, an outage or a join), and
// VOICE_STATE_UPDATE events keep them current. A server is "known" from its first
// snapshot on; a new session starts with nothing known.
type voiceCache struct {
	mu      sync.Mutex
	tracked bool // the session has the ServerVoiceStates intent
	servers map[string]*serverVoice
}

type serverVoice struct {
	known  bool
	states map[string]discord.VoiceState // user ID → state (connected members only)
}

func newVoiceCache(intents discord.Intents) *voiceCache {
	return &voiceCache{tracked: intents.Has(intentVoiceStates), servers: map[string]*serverVoice{}}
}

func (c *voiceCache) server(id string) *serverVoice {
	s := c.servers[id]
	if s == nil {
		s = &serverVoice{states: map[string]discord.VoiceState{}}
		c.servers[id] = s
	}
	return s
}

// Observe updates the cache from a gateway event and returns the event, with
// VoiceStateUpdateEvent.Previous filled in (the state before the update).
func (c *voiceCache) Observe(ev discord.Event) discord.Event {
	if c == nil || !c.tracked {
		return ev
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e := ev.(type) {
	case discord.ServerAvailableEvent:
		s := c.server(e.ServerID)
		s.known = true
		s.states = map[string]discord.VoiceState{}
		for _, st := range e.VoiceStates {
			if st.ChannelID != "" {
				s.states[st.UserID] = st
			}
		}
	case discord.VoiceStateUpdateEvent:
		s := c.server(e.State.ServerID)
		if prev, ok := s.states[e.State.UserID]; ok {
			e.Previous = &prev
		}
		if e.State.ChannelID == "" {
			delete(s.states, e.State.UserID)
		} else {
			st := e.State
			if st.Member == nil && e.Previous != nil {
				st.Member = e.Previous.Member
			}
			s.states[st.UserID] = st
		}
		return e
	case discord.MemberUpdateEvent:
		// Keep the members of voice states current (nickname, roles).
		if s := c.servers[e.Member.ServerID]; s != nil {
			if st, ok := s.states[e.Member.User.ID]; ok {
				m := e.Member
				st.Member = &m
				s.states[st.UserID] = st
			}
		}
	case discord.ServerLeaveEvent:
		delete(c.servers, e.ServerID)
	}
	return ev
}

// States returns the voice states of a server's connected members, in user ID order, and
// whether the server's voice states are known.
func (c *voiceCache) States(serverID string) ([]discord.VoiceState, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.servers[serverID]
	if s == nil || !s.known {
		return nil, false
	}
	out := make([]discord.VoiceState, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return discord.SnowflakeLess(out[i].UserID, out[j].UserID) })
	return out, true
}
