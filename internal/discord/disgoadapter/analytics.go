package disgoadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/disgoorg/disgo/rest"

	discordapi "github.com/disgoorg/disgo/discord"

	"github.com/cicord/cicord/internal/discord"
)

// Gateway payloads and REST reads of the server activity analytics.

var _ discord.AnalyticsReader = (*session)(nil)

// ───────────────────────────────────────────────────────────── wire

type wireVoiceState struct {
	GuildID    string      `json:"guild_id"`
	ChannelID  *string     `json:"channel_id"`
	UserID     string      `json:"user_id"`
	Member     *wireMember `json:"member"`
	SelfMute   bool        `json:"self_mute"`
	SelfDeaf   bool        `json:"self_deaf"`
	SelfStream bool        `json:"self_stream"`
	SelfVideo  bool        `json:"self_video"`
}

type wireActivity struct {
	Name string `json:"name"`
	Type int    `json:"type"`
}

type wireClientStatus struct {
	Desktop string `json:"desktop"`
	Mobile  string `json:"mobile"`
	Web     string `json:"web"`
}

type wirePresence struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	GuildID      string           `json:"guild_id"`
	Status       string           `json:"status"`
	Activities   []wireActivity   `json:"activities"`
	ClientStatus wireClientStatus `json:"client_status"`
}

// wireGuildAvailable is the part of GUILD_CREATE the analytics read.
type wireGuildAvailable struct {
	ID          string           `json:"id"`
	Members     []wireMember     `json:"members"`
	VoiceStates []wireVoiceState `json:"voice_states"`
	Presences   []wirePresence   `json:"presences"`
}

type wireAuditLogChange struct {
	Key      string          `json:"key"`
	OldValue json.RawMessage `json:"old_value,omitempty"`
	NewValue json.RawMessage `json:"new_value,omitempty"`
}

type wireAuditLogEntry struct {
	ID         string               `json:"id"`
	GuildID    string               `json:"guild_id"`
	ActionType int                  `json:"action_type"`
	UserID     *string              `json:"user_id"`
	TargetID   *string              `json:"target_id"`
	Changes    []wireAuditLogChange `json:"changes"`
}

// wireAutoModExecution deliberately has no content / matched_keyword / matched_content
// fields: message content is never decoded.
type wireAutoModExecution struct {
	GuildID         string            `json:"guild_id"`
	Action          wireAutoModAction `json:"action"`
	RuleID          string            `json:"rule_id"`
	RuleTriggerType int               `json:"rule_trigger_type"`
	UserID          string            `json:"user_id"`
	ChannelID       *string           `json:"channel_id"`
	MessageID       *string           `json:"message_id"`
}

type wireInviteEvent struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Code      string `json:"code"`
	MaxUses   int    `json:"max_uses"`
	Uses      int    `json:"uses"`
}

type wireGuildCounts struct {
	ApproximateMemberCount   int  `json:"approximate_member_count"`
	ApproximatePresenceCount int  `json:"approximate_presence_count"`
	PremiumTier              int  `json:"premium_tier"`
	PremiumSubscriptionCount *int `json:"premium_subscription_count"`
}

type wireScheduledEvent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    int    `json:"status"`
	UserCount int    `json:"user_count"`
}

type wireInvite struct {
	Code    string `json:"code"`
	Uses    int    `json:"uses"`
	MaxUses int    `json:"max_uses"`
}

type wireVanity struct {
	Code *string `json:"code"`
	Uses int     `json:"uses"`
}

// ───────────────────────────────────────────────────────────── mapping

var activityTypeNames = map[int]string{0: "playing", 1: "streaming", 2: "listening", 3: "watching", 4: "custom", 5: "competing"}

var scheduledEventStatuses = map[int]string{1: "scheduled", 2: "active", 3: "completed", 4: "canceled"}

func voiceStateFromWire(serverID string, v wireVoiceState) discord.VoiceState {
	out := discord.VoiceState{
		ServerID: serverID, ChannelID: deref(v.ChannelID), UserID: v.UserID,
		SelfStream: v.SelfStream, SelfVideo: v.SelfVideo, SelfMute: v.SelfMute, SelfDeaf: v.SelfDeaf,
	}
	if out.ServerID == "" {
		out.ServerID = v.GuildID
	}
	if v.Member != nil && v.Member.User != nil {
		out.Bot = v.Member.User.Bot
		if out.UserID == "" {
			out.UserID = v.Member.User.ID
		}
	}
	return out
}

func presenceFromWire(p wirePresence) discord.MemberPresence {
	out := discord.MemberPresence{
		UserID: p.User.ID, Status: p.Status,
		Desktop: p.ClientStatus.Desktop, Mobile: p.ClientStatus.Mobile, Web: p.ClientStatus.Web,
	}
	for _, a := range p.Activities {
		t, ok := activityTypeNames[a.Type]
		if !ok {
			continue
		}
		out.Activities = append(out.Activities, discord.Activity{Type: t, Name: a.Name})
	}
	return out
}

func (s *session) translateAnalytics(t string, d []byte) ([]discord.Event, bool, error) {
	switch t {
	case "VOICE_STATE_UPDATE":
		var v wireVoiceState
		if err := json.Unmarshal(d, &v); err != nil {
			return nil, true, err
		}
		if v.GuildID == "" {
			return nil, true, nil // DM calls
		}
		return []discord.Event{discord.VoiceStateUpdateEvent{State: voiceStateFromWire(v.GuildID, v)}}, true, nil

	case "PRESENCE_UPDATE":
		var p wirePresence
		if err := json.Unmarshal(d, &p); err != nil {
			return nil, true, err
		}
		if p.GuildID == "" {
			return nil, true, nil
		}
		return []discord.Event{discord.PresenceUpdateEvent{ServerID: p.GuildID, Presence: presenceFromWire(p)}}, true, nil

	case "GUILD_AUDIT_LOG_ENTRY_CREATE":
		var e wireAuditLogEntry
		if err := json.Unmarshal(d, &e); err != nil {
			return nil, true, err
		}
		ev := discord.AuditLogEntryEvent{ServerID: e.GuildID, ID: e.ID, ActionType: e.ActionType, UserID: deref(e.UserID), TargetID: deref(e.TargetID)}
		for _, c := range e.Changes {
			ev.Changes = append(ev.Changes, discord.AuditLogChange{Key: c.Key, Old: c.OldValue, New: c.NewValue})
		}
		return []discord.Event{ev}, true, nil

	case "AUTO_MODERATION_ACTION_EXECUTION":
		var e wireAutoModExecution
		if err := json.Unmarshal(d, &e); err != nil {
			return nil, true, err
		}
		action := autoModActions[e.Action.Type]
		if action == "" {
			action = "type" + strconv.Itoa(e.Action.Type)
		}
		return []discord.Event{discord.AutoModActionEvent{
			ServerID: e.GuildID, RuleID: e.RuleID, Trigger: autoModTriggers[e.RuleTriggerType], Action: action,
			UserID: e.UserID, ChannelID: deref(e.ChannelID), MessageID: deref(e.MessageID),
		}}, true, nil

	case "INVITE_CREATE", "INVITE_DELETE":
		var e wireInviteEvent
		if err := json.Unmarshal(d, &e); err != nil {
			return nil, true, err
		}
		if e.GuildID == "" {
			return nil, true, nil
		}
		if t == "INVITE_CREATE" {
			return []discord.Event{discord.InviteCreateEvent{ServerID: e.GuildID, ChannelID: e.ChannelID, Code: e.Code, MaxUses: e.MaxUses, Uses: e.Uses}}, true, nil
		}
		return []discord.Event{discord.InviteDeleteEvent{ServerID: e.GuildID, ChannelID: e.ChannelID, Code: e.Code}}, true, nil
	}
	return nil, false, nil
}

// serverAvailable decodes the voice states and presences of a GUILD_CREATE.
func serverAvailable(d []byte) (discord.ServerAvailableEvent, error) {
	var g wireGuildAvailable
	if err := json.Unmarshal(d, &g); err != nil {
		return discord.ServerAvailableEvent{}, err
	}
	ev := discord.ServerAvailableEvent{ServerID: g.ID}
	// The voice states of GUILD_CREATE are partial (no member): the members array,
	// which includes every member in voice, tells the bots.
	bots := map[string]bool{}
	for _, m := range g.Members {
		if m.User != nil && m.User.Bot {
			bots[m.User.ID] = true
		}
	}
	for _, v := range g.VoiceStates {
		if v.ChannelID == nil || *v.ChannelID == "" {
			continue
		}
		st := voiceStateFromWire(g.ID, v)
		st.Bot = st.Bot || bots[st.UserID]
		ev.VoiceStates = append(ev.VoiceStates, st)
	}
	for _, p := range g.Presences {
		ev.Presences = append(ev.Presences, presenceFromWire(p))
	}
	return ev, nil
}

// ───────────────────────────────────────────────────────────── REST

// GetServerCounts is GET /guilds/{id}?with_counts=true.
func (s *session) GetServerCounts(ctx context.Context, serverID string) (discord.ServerCounts, error) {
	if err := checkIDs("GetServerCounts", serverID); err != nil {
		return discord.ServerCounts{}, err
	}
	var g wireGuildCounts
	if err := s.do(ctx, "GetServerCounts", rest.GetGuild.Compile(discordapi.QueryValues{"with_counts": true}, serverID), nil, &g); err != nil {
		return discord.ServerCounts{}, err
	}
	return discord.ServerCounts{
		ApproximateMembers: g.ApproximateMemberCount, ApproximateOnline: g.ApproximatePresenceCount,
		Boosts: derefInt(g.PremiumSubscriptionCount), BoostTier: g.PremiumTier,
	}, nil
}

// maxMemberPages bounds ListServerBots (1000 members per page).
const maxMemberPages = 1000

// ListServerBots is GET /guilds/{id}/members?limit=1000, page after page.
func (s *session) ListServerBots(ctx context.Context, serverID string) ([]string, error) {
	if err := checkIDs("ListServerBots", serverID); err != nil {
		return nil, err
	}
	var bots []string
	after := "0"
	for range maxMemberPages {
		var page []wireMember
		q := discordapi.QueryValues{"limit": 1000, "after": after}
		if err := s.do(ctx, "ListServerBots", rest.GetMembers.Compile(q, serverID), nil, &page); err != nil {
			return nil, err
		}
		for _, m := range page {
			if m.User == nil {
				continue
			}
			if m.User.Bot {
				bots = append(bots, m.User.ID)
			}
			after = m.User.ID // members come in ascending user ID order
		}
		if len(page) < 1000 {
			return bots, nil
		}
	}
	return nil, fmt.Errorf("ListServerBots: server %s has more than %d members", serverID, maxMemberPages*1000)
}

// ListScheduledEventInterest is GET /guilds/{id}/scheduled-events?with_user_count=true.
func (s *session) ListScheduledEventInterest(ctx context.Context, serverID string) ([]discord.ScheduledEventInterest, error) {
	if err := checkIDs("ListScheduledEventInterest", serverID); err != nil {
		return nil, err
	}
	var evs []wireScheduledEvent
	if err := s.do(ctx, "ListScheduledEventInterest", rest.GetGuildScheduledEvents.Compile(discordapi.QueryValues{"with_user_count": true}, serverID), nil, &evs); err != nil {
		return nil, err
	}
	out := make([]discord.ScheduledEventInterest, 0, len(evs))
	for _, e := range evs {
		out = append(out, discord.ScheduledEventInterest{ID: e.ID, Name: e.Name, Status: scheduledEventStatuses[e.Status], Interested: e.UserCount})
	}
	return out, nil
}

// ListInviteUses is GET /guilds/{id}/invites plus GET /guilds/{id}/vanity-url when the
// server has the VANITY_URL feature.
func (s *session) ListInviteUses(ctx context.Context, serverID string) ([]discord.InviteUses, error) {
	if err := checkIDs("ListInviteUses", serverID); err != nil {
		return nil, err
	}
	var invs []wireInvite
	if err := s.do(ctx, "ListInviteUses", rest.GetGuildInvites.Compile(nil, serverID), nil, &invs); err != nil {
		return nil, err
	}
	out := make([]discord.InviteUses, 0, len(invs)+1)
	for _, i := range invs {
		out = append(out, discord.InviteUses{Code: i.Code, Uses: i.Uses, MaxUses: i.MaxUses})
	}
	// The invites call already needed ManageServer: a failing vanity call means the
	// server has no vanity URL (Discord answers it with an error for those).
	var v wireVanity
	if err := s.do(ctx, "ListInviteUses", rest.GetGuildVanityURL.Compile(nil, serverID), nil, &v); err == nil && v.Code != nil && *v.Code != "" {
		out = append(out, discord.InviteUses{Code: *v.Code, Uses: v.Uses, Vanity: true})
	} else if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, nil
}
