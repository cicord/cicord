package disgoadapter

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"runtime/debug"
	"strconv"
	"sync"

	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"

	discordapi "github.com/disgoorg/disgo/discord"

	"github.com/npjonath/codi/internal/discord"
)

func readAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }

func pathEscape(s string) string { return url.PathEscape(s) }

func parseSnowflake(s string) (snowflake.ID, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return snowflake.ID(v), nil
}

// ───────────────────────────────────────────────────────────── translation

// translate turns one gateway dispatch (event name + raw "d" JSON) into zero or
// more CODI events, updating the member cache on the way. Unknown or irrelevant
// dispatches return nil.
func (s *session) translate(t string, d []byte) ([]discord.Event, error) {
	switch t {
	case "READY":
		var r wireReady
		if err := json.Unmarshal(d, &r); err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(r.Guilds))
		for _, g := range r.Guilds {
			ids = append(ids, g.ID)
		}
		return []discord.Event{discord.ReadyEvent{User: userFromWire(r.User), ServerIDs: ids}}, nil

	case "GUILD_CREATE":
		var g wireGuildCreate
		if err := json.Unmarshal(d, &g); err != nil {
			return nil, err
		}
		if !g.Unavailable {
			for _, m := range g.Members {
				s.members.put(memberFromWire(g.ID, m, nil))
			}
			if s.chunkingEnabled() {
				s.requestMembers(g.ID)
			}
		}
		return nil, nil

	case "GUILD_DELETE":
		var g wireGuildDelete
		if err := json.Unmarshal(d, &g); err != nil {
			return nil, err
		}
		if !g.Unavailable {
			s.members.dropServer(g.ID)
		}
		return nil, nil

	case "GUILD_MEMBERS_CHUNK":
		var c wireMembersChunk
		if err := json.Unmarshal(d, &c); err != nil {
			return nil, err
		}
		for _, m := range c.Members {
			s.members.put(memberFromWire(c.GuildID, m, nil))
		}
		return nil, nil

	case "GUILD_UPDATE":
		var g wireGuild
		if err := json.Unmarshal(d, &g); err != nil {
			return nil, err
		}
		return []discord.Event{discord.ServerUpdateEvent{Server: serverFromWire(g)}}, nil

	case "GUILD_MEMBER_ADD":
		var m wireMember
		if err := json.Unmarshal(d, &m); err != nil {
			return nil, err
		}
		mem := memberFromWire(m.GuildID, m, nil)
		s.members.put(mem)
		return []discord.Event{discord.MemberJoinEvent{Member: mem}}, nil

	case "GUILD_MEMBER_UPDATE":
		var m wireMember
		if err := json.Unmarshal(d, &m); err != nil {
			return nil, err
		}
		mem := memberFromWire(m.GuildID, m, nil)
		prev := s.members.swap(mem)
		return []discord.Event{discord.MemberUpdateEvent{Member: mem, Previous: prev}}, nil

	case "GUILD_MEMBER_REMOVE":
		var r wireMemberRemove
		if err := json.Unmarshal(d, &r); err != nil {
			return nil, err
		}
		s.members.remove(r.GuildID, r.User.ID)
		return []discord.Event{discord.MemberLeaveEvent{ServerID: r.GuildID, User: userFromWire(r.User)}}, nil

	case "MESSAGE_CREATE", "MESSAGE_UPDATE":
		var m wireMessage
		if err := json.Unmarshal(d, &m); err != nil {
			return nil, err
		}
		msg := messageFromWire(m)
		if t == "MESSAGE_CREATE" {
			return []discord.Event{discord.MessageCreateEvent{Message: msg}}, nil
		}
		return []discord.Event{discord.MessageUpdateEvent{Message: msg}}, nil

	case "MESSAGE_DELETE", "MESSAGE_DELETE_BULK":
		var m wireMessageDelete
		if err := json.Unmarshal(d, &m); err != nil {
			return nil, err
		}
		ids := m.IDs
		if t == "MESSAGE_DELETE" {
			ids = []string{m.ID}
		}
		out := make([]discord.Event, 0, len(ids))
		for _, id := range ids {
			out = append(out, discord.MessageDeleteEvent{ID: id, ChannelID: m.ChannelID, ServerID: m.GuildID})
		}
		return out, nil

	case "MESSAGE_REACTION_ADD", "MESSAGE_REACTION_REMOVE":
		var r wireReaction
		if err := json.Unmarshal(d, &r); err != nil {
			return nil, err
		}
		ev := discord.ReactionEvent{
			ServerID:  r.GuildID,
			ChannelID: r.ChannelID,
			MessageID: r.MessageID,
			UserID:    r.UserID,
			Emoji:     emojiFromWire(&r.Emoji),
		}
		if r.Member != nil && r.GuildID != "" {
			mem := memberFromWire(r.GuildID, *r.Member, nil)
			ev.Member = &mem
		}
		if t == "MESSAGE_REACTION_ADD" {
			return []discord.Event{discord.ReactionAddEvent{ReactionEvent: ev}}, nil
		}
		return []discord.Event{discord.ReactionRemoveEvent{ReactionEvent: ev}}, nil

	case "INTERACTION_CREATE":
		var in wireInteraction
		if err := json.Unmarshal(d, &in); err != nil {
			return nil, err
		}
		return []discord.Event{discord.InteractionCreateEvent{Interaction: interactionFromWire(in)}}, nil

	// Threads are channels with a thread type (10/11/12); they are reported through
	// the channel events, like the simulator does.
	case "CHANNEL_CREATE", "THREAD_CREATE":
		c, err := decodeChannel(d)
		if err != nil {
			return nil, err
		}
		return []discord.Event{discord.ChannelCreateEvent{Channel: c}}, nil
	case "CHANNEL_UPDATE", "THREAD_UPDATE":
		c, err := decodeChannel(d)
		if err != nil {
			return nil, err
		}
		return []discord.Event{discord.ChannelUpdateEvent{Channel: c}}, nil
	case "CHANNEL_DELETE", "THREAD_DELETE":
		c, err := decodeChannel(d)
		if err != nil {
			return nil, err
		}
		return []discord.Event{discord.ChannelDeleteEvent{Channel: c}}, nil

	case "GUILD_ROLE_CREATE", "GUILD_ROLE_UPDATE":
		var r wireRoleEvent
		if err := json.Unmarshal(d, &r); err != nil {
			return nil, err
		}
		if t == "GUILD_ROLE_CREATE" {
			return []discord.Event{discord.RoleCreateEvent{ServerID: r.GuildID, Role: roleFromWire(r.Role)}}, nil
		}
		return []discord.Event{discord.RoleUpdateEvent{ServerID: r.GuildID, Role: roleFromWire(r.Role)}}, nil
	case "GUILD_ROLE_DELETE":
		var r wireRoleEvent
		if err := json.Unmarshal(d, &r); err != nil {
			return nil, err
		}
		return []discord.Event{discord.RoleDeleteEvent{ServerID: r.GuildID, RoleID: r.RoleID}}, nil
	}
	return nil, nil
}

func decodeChannel(d []byte) (discord.Channel, error) {
	var c wireChannel
	if err := json.Unmarshal(d, &c); err != nil {
		return discord.Channel{}, err
	}
	return channelFromWire(c), nil
}

func (s *session) chunkingEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chunk
}

// ───────────────────────────────────────────────────────────── presence

var activityTypes = map[string]discordapi.ActivityType{
	"playing":   discordapi.ActivityTypeGame,
	"streaming": discordapi.ActivityTypeStreaming,
	"listening": discordapi.ActivityTypeListening,
	"watching":  discordapi.ActivityTypeWatching,
	"custom":    discordapi.ActivityTypeCustom,
	"competing": discordapi.ActivityTypeCompeting,
}

func presenceToGateway(p discord.Presence) (gateway.MessageDataPresenceUpdate, error) {
	out := gateway.MessageDataPresenceUpdate{Activities: []discordapi.Activity{}}
	switch p.Status {
	case "", "online":
		out.Status = discordapi.OnlineStatusOnline
	case "idle":
		out.Status = discordapi.OnlineStatusIdle
	case "dnd":
		out.Status = discordapi.OnlineStatusDND
	case "invisible":
		out.Status = discordapi.OnlineStatusInvisible
	default:
		return out, fmt.Errorf("discord SetPresence: %w: unknown status %q", discord.ErrBadRequest, p.Status)
	}
	if p.ActivityType == "" {
		return out, nil
	}
	at, ok := activityTypes[p.ActivityType]
	if !ok {
		return out, fmt.Errorf("discord SetPresence: %w: unknown activity type %q", discord.ErrBadRequest, p.ActivityType)
	}
	a := discordapi.Activity{Name: p.ActivityName, Type: at}
	if at == discordapi.ActivityTypeCustom {
		state := p.ActivityName
		a.Name = "Custom Status"
		a.State = &state
	}
	if at == discordapi.ActivityTypeStreaming && p.ActivityURL != "" {
		u := p.ActivityURL
		a.URL = &u
	}
	out.Activities = append(out.Activities, a)
	return out, nil
}

// ───────────────────────────────────────────────────────────── member cache

// memberCache keeps the last known state of members (server → user → member),
// fed by GUILD_CREATE, GUILD_MEMBERS_CHUNK and member events. It only exists to
// fill MemberUpdateEvent.Previous; it is touched from the gateway goroutine only,
// but guarded anyway for safety.
type memberCache struct {
	mu sync.Mutex
	m  map[string]map[string]discord.Member
}

func newMemberCache() *memberCache { return &memberCache{m: map[string]map[string]discord.Member{}} }

func (c *memberCache) put(m discord.Member) {
	if m.ServerID == "" || m.User.ID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	srv := c.m[m.ServerID]
	if srv == nil {
		srv = map[string]discord.Member{}
		c.m[m.ServerID] = srv
	}
	srv[m.User.ID] = m
}

// swap stores m and returns the previous state, or nil when not cached.
func (c *memberCache) swap(m discord.Member) *discord.Member {
	c.mu.Lock()
	var prev *discord.Member
	if old, ok := c.m[m.ServerID][m.User.ID]; ok {
		prev = &old
	}
	c.mu.Unlock()
	c.put(m)
	return prev
}

func (c *memberCache) remove(serverID, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m[serverID], userID)
}

func (c *memberCache) dropServer(serverID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, serverID)
}

// ───────────────────────────────────────────────────────────── ordered queue

// eventQueue is an unbounded FIFO between disgo's reader goroutine (push never
// blocks) and the single dispatcher goroutine (run).
type eventQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []discord.Event
	closed bool
}

func newEventQueue() *eventQueue {
	q := &eventQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *eventQueue) push(e discord.Event) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, e)
		q.cond.Signal()
	}
	q.mu.Unlock()
}

func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.items = nil
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *eventQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// run calls handler for every event in order until close. A panicking handler is
// logged and does not stop dispatching.
func (q *eventQueue) run(handler func(discord.Event), logger *slog.Logger) {
	for {
		q.mu.Lock()
		for len(q.items) == 0 && !q.closed {
			q.cond.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		e := q.items[0]
		q.items[0] = nil
		q.items = q.items[1:]
		q.mu.Unlock()
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("discord: event handler panicked", slog.Any("panic", r), slog.String("event", fmt.Sprintf("%T", e)), slog.String("stack", string(debug.Stack())))
				}
			}()
			handler(e)
		}()
	}
}
