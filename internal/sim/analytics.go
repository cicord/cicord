package sim

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/cicord/cicord/internal/discord"
)

// Server activity the analytics collector observes: voice connections, member
// presences, invites and their uses, scheduled events, audit log entries and AutoMod
// executions.

var _ discord.AnalyticsReader = (*Client)(nil)

var (
	intentVoiceStates    = discord.MustIntents("ServerVoiceStates")
	intentPresences      = discord.MustIntents("ServerPresences")
	intentModeration     = discord.MustIntents("ServerModeration")
	intentInvites        = discord.MustIntents("ServerInvites")
	intentAutoModExecute = discord.MustIntents("AutoModerationExecution")
	permViewAuditLog     = discord.MustPermissions("ViewAuditLog")
)

type serverActivity struct {
	voice     map[string]discord.VoiceState     // user ID → voice state
	presences map[string]discord.MemberPresence // user ID → presence (not offline)
	invites   map[string]*discord.InviteUses    // code → invite
	order     []string                          // invite codes, creation order
	vanity    *discord.InviteUses
	events    map[string]*discord.ScheduledEventInterest
	eventIDs  []string
}

// activity returns the activity state of a server. Caller holds w.mu.
func (w *World) activity(serverID string) *serverActivity {
	if w.analytics == nil {
		w.analytics = map[string]*serverActivity{}
	}
	a := w.analytics[serverID]
	if a == nil {
		a = &serverActivity{voice: map[string]discord.VoiceState{}, presences: map[string]discord.MemberPresence{},
			invites: map[string]*discord.InviteUses{}, events: map[string]*discord.ScheduledEventInterest{}}
		w.analytics[serverID] = a
	}
	return a
}

// serverAvailable is the GUILD_CREATE content the analytics read. Caller holds w.mu.
func (w *World) serverAvailable(serverID string) discord.ServerAvailableEvent {
	a := w.activity(serverID)
	ev := discord.ServerAvailableEvent{ServerID: serverID}
	for _, id := range sortedKeys(a.voice) {
		ev.VoiceStates = append(ev.VoiceStates, a.voice[id])
	}
	for _, id := range sortedKeys(a.presences) {
		ev.Presences = append(ev.Presences, clonePresence(a.presences[id]))
	}
	return ev
}

func clonePresence(p discord.MemberPresence) discord.MemberPresence {
	if p.Activities != nil {
		p.Activities = append([]discord.Activity{}, p.Activities...)
	}
	return p
}

// filterAnalytics is filterFor for the analytics events. Caller holds w.mu.
func (w *World) filterAnalytics(s *Session, ev discord.Event) (discord.Event, bool) {
	has := func(i discord.Intents) bool { return s.intents.Has(i) }
	botID := s.bot.userID
	basePerms := func(serverID string) discord.Permissions {
		if srv := w.servers[serverID]; srv != nil {
			return w.basePerms(srv, botID)
		}
		return 0
	}
	switch e := ev.(type) {
	case discord.ServerAvailableEvent:
		out := discord.ServerAvailableEvent{ServerID: e.ServerID}
		if has(intentVoiceStates) {
			out.VoiceStates = append([]discord.VoiceState(nil), e.VoiceStates...)
		}
		if has(intentPresences) {
			for _, p := range e.Presences {
				out.Presences = append(out.Presences, clonePresence(p))
			}
		}
		return out, has(intentServers)
	case discord.VoiceStateUpdateEvent:
		return e, has(intentVoiceStates)
	case discord.PresenceUpdateEvent:
		return discord.PresenceUpdateEvent{ServerID: e.ServerID, Presence: clonePresence(e.Presence)}, has(intentPresences)
	case discord.AuditLogEntryEvent:
		if !has(intentModeration) || !basePerms(e.ServerID).Has(permViewAuditLog) {
			return nil, false
		}
		e.Changes = append([]discord.AuditLogChange(nil), e.Changes...)
		return e, true
	case discord.AutoModActionEvent:
		return e, has(intentAutoModExecute) && basePerms(e.ServerID).Has(permManageServer)
	case discord.InviteCreateEvent, discord.InviteDeleteEvent:
		if !has(intentInvites) {
			return nil, false
		}
		chID := ""
		if c, ok := e.(discord.InviteCreateEvent); ok {
			chID = c.ChannelID
		} else {
			chID = e.(discord.InviteDeleteEvent).ChannelID
		}
		c := w.channels[chID]
		return e, c != nil && w.channelPerms(c, botID).Has(permManageChannels)
	}
	return nil, false
}

// ───────────────────────────────────────────────────────────── voice

// JoinVoice connects userID to a voice or stage channel (emits VoiceStateUpdateEvent).
// Joining another channel moves the member.
func (w *World) JoinVoice(userID, channelID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.channels[channelID]
	if c == nil {
		return notFound("unknown channel %s", channelID)
	}
	if c.ch.Type != discord.ChannelVoice && c.ch.Type != discord.ChannelStage {
		return badRequest("channel %s is not a voice channel", channelID)
	}
	s := w.servers[c.ch.ServerID]
	m := s.members[userID]
	if m == nil {
		return notFound("unknown member %s", userID)
	}
	a := w.activity(s.info.ID)
	st := a.voice[userID]
	st.ServerID, st.ChannelID, st.UserID, st.Bot = s.info.ID, channelID, userID, m.User.Bot
	a.voice[userID] = st
	w.emit(discord.VoiceStateUpdateEvent{State: st})
	return nil
}

// LeaveVoice disconnects userID from voice.
func (w *World) LeaveVoice(serverID, userID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	a := w.activity(serverID)
	st, ok := a.voice[userID]
	if !ok {
		return notFound("member %s is not in voice", userID)
	}
	delete(a.voice, userID)
	st.ChannelID, st.SelfStream, st.SelfVideo = "", false, false
	w.emit(discord.VoiceStateUpdateEvent{State: st})
	return nil
}

// SetVoiceMedia turns userID's stream (Go Live) and camera on or off.
func (w *World) SetVoiceMedia(serverID, userID string, stream, video bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	a := w.activity(serverID)
	st, ok := a.voice[userID]
	if !ok {
		return notFound("member %s is not in voice", userID)
	}
	st.SelfStream, st.SelfVideo = stream, video
	a.voice[userID] = st
	w.emit(discord.VoiceStateUpdateEvent{State: st})
	return nil
}

// VoiceStates returns the voice states of a server, by user ID.
func (w *World) VoiceStates(serverID string) []discord.VoiceState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.serverAvailable(serverID).VoiceStates
}

// ───────────────────────────────────────────────────────────── presence

// SetMemberPresence sets a member's presence (emits PresenceUpdateEvent). Status
// "offline" (or "invisible") removes it.
func (w *World) SetMemberPresence(serverID string, p discord.MemberPresence) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	if s.members[p.UserID] == nil {
		return notFound("unknown member %s", p.UserID)
	}
	a := w.activity(serverID)
	p = clonePresence(p)
	if p.Status == "invisible" {
		p.Status = "offline"
	}
	if p.Status == "offline" {
		delete(a.presences, p.UserID)
		p.Desktop, p.Mobile, p.Web, p.Activities = "", "", "", nil
	} else {
		a.presences[p.UserID] = p
	}
	w.emit(discord.PresenceUpdateEvent{ServerID: serverID, Presence: p})
	return nil
}

// ───────────────────────────────────────────────────────────── invites

// CreateInvite creates an invite to a channel (as the server owner) and returns its
// code. maxUses 0 = unlimited.
func (w *World) CreateInvite(channelID string, maxUses int) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.channels[channelID]
	if c == nil || c.ch.ServerID == "" {
		return "", notFound("unknown channel %s", channelID)
	}
	a := w.activity(c.ch.ServerID)
	code := "inv" + w.newID()
	a.invites[code] = &discord.InviteUses{Code: code, MaxUses: maxUses}
	a.order = append(a.order, code)
	w.emit(discord.InviteCreateEvent{ServerID: c.ch.ServerID, ChannelID: channelID, Code: code, MaxUses: maxUses})
	return code, nil
}

// DeleteInvite deletes an invite (emits InviteDeleteEvent).
func (w *World) DeleteInvite(serverID, code string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.deleteInvite(serverID, code)
}

func (w *World) deleteInvite(serverID, code string) error {
	a := w.activity(serverID)
	if a.invites[code] == nil {
		return notFound("unknown invite %s", code)
	}
	delete(a.invites, code)
	a.order = removeString(a.order, code)
	ch := ""
	if s := w.servers[serverID]; s != nil && len(s.channelIDs) > 0 {
		for _, id := range s.channelIDs {
			if c := w.channels[id]; c != nil && c.ch.Type.IsText() {
				ch = id
				break
			}
		}
	}
	w.emit(discord.InviteDeleteEvent{ServerID: serverID, ChannelID: ch, Code: code})
	return nil
}

// JoinWithInvite makes userID join the invite's server through the invite: the use
// count goes up and an invite that reaches its max uses is deleted, like Discord.
func (w *World) JoinWithInvite(userID, code string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for sid, a := range w.analytics {
		inv := a.invites[code]
		if inv == nil {
			if a.vanity != nil && a.vanity.Code == code {
				if err := w.addMember(sid, userID); err != nil {
					return err
				}
				a.vanity.Uses++
				return nil
			}
			continue
		}
		if err := w.addMember(sid, userID); err != nil {
			return err
		}
		inv.Uses++
		if inv.MaxUses > 0 && inv.Uses >= inv.MaxUses {
			return w.deleteInvite(sid, code)
		}
		return nil
	}
	return notFound("unknown invite %s", code)
}

// SetVanityURL gives a server a vanity invite code ("" removes it).
func (w *World) SetVanityURL(serverID, code string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.server(serverID); err != nil {
		return err
	}
	a := w.activity(serverID)
	if code == "" {
		a.vanity = nil
		return nil
	}
	a.vanity = &discord.InviteUses{Code: code, Vanity: true}
	return nil
}

// ───────────────────────────────────────────────────────────── scheduled events

// CreateScheduledEvent creates a scheduled event with interested members and returns its ID.
func (w *World) CreateScheduledEvent(serverID, name string, interested int) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.server(serverID); err != nil {
		return "", err
	}
	a := w.activity(serverID)
	id := w.newID()
	a.events[id] = &discord.ScheduledEventInterest{ID: id, Name: name, Status: "scheduled", Interested: interested}
	a.eventIDs = append(a.eventIDs, id)
	return id, nil
}

// SetScheduledEvent updates the interested count and status of a scheduled event
// (status "" keeps it).
func (w *World) SetScheduledEvent(serverID, eventID string, interested int, status string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	e := w.activity(serverID).events[eventID]
	if e == nil {
		return notFound("unknown scheduled event %s", eventID)
	}
	e.Interested = interested
	if status != "" {
		e.Status = status
	}
	return nil
}

// ───────────────────────────────────────────────────────────── moderation

// auditLog emits an audit log entry. Caller holds w.mu.
func (w *World) auditLog(serverID, executorID string, actionType int, targetID string, changes ...discord.AuditLogChange) {
	w.emit(discord.AuditLogEntryEvent{ServerID: serverID, ID: w.newID(), ActionType: actionType, UserID: executorID, TargetID: targetID, Changes: changes})
}

// auditMemberUpdate logs a timeout change (MEMBER_UPDATE with communication_disabled_until)
// and nickname changes made by someone else. Caller holds w.mu.
func (w *World) auditMemberUpdate(serverID, executorID string, prev, next discord.Member) {
	var changes []discord.AuditLogChange
	if (prev.TimeoutUntil == nil) != (next.TimeoutUntil == nil) ||
		prev.TimeoutUntil != nil && next.TimeoutUntil != nil && !prev.TimeoutUntil.Equal(*next.TimeoutUntil) {
		ch := discord.AuditLogChange{Key: "communication_disabled_until"}
		if prev.TimeoutUntil != nil {
			ch.Old, _ = json.Marshal(prev.TimeoutUntil)
		}
		if next.TimeoutUntil != nil {
			ch.New, _ = json.Marshal(next.TimeoutUntil)
		}
		changes = append(changes, ch)
	}
	if prev.Nick != next.Nick && executorID != next.User.ID {
		o, _ := json.Marshal(prev.Nick)
		n, _ := json.Marshal(next.Nick)
		changes = append(changes, discord.AuditLogChange{Key: "nick", Old: o, New: n})
	}
	if len(changes) > 0 {
		w.auditLog(serverID, executorID, discord.AuditMemberUpdate, next.User.ID, changes...)
	}
}

// AuditLog emits an arbitrary audit log entry (actions the simulator does not model).
func (w *World) AuditLog(serverID, executorID string, actionType int, targetID string, changes ...discord.AuditLogChange) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.server(serverID); err != nil {
		return err
	}
	w.auditLog(serverID, executorID, actionType, targetID, changes...)
	return nil
}

// TriggerAutoMod simulates an AutoMod rule firing on userID's message in channelID:
// one AutoModActionEvent per action type of the rule (block, alert, timeout…).
func (w *World) TriggerAutoMod(serverID, ruleID, userID, channelID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	r := s.automod[ruleID]
	if r == nil {
		return notFound("unknown AutoMod rule %s", ruleID)
	}
	for _, act := range r.Actions {
		w.emit(discord.AutoModActionEvent{ServerID: serverID, RuleID: ruleID, Trigger: r.Trigger, Action: act.Type, UserID: userID, ChannelID: channelID})
	}
	return nil
}

// CompleteOnboarding sets the member's onboarding-completed flag (emits MemberUpdateEvent).
func (w *World) CompleteOnboarding(serverID, userID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	m := s.members[userID]
	if m == nil {
		return notFound("unknown member %s", userID)
	}
	prev := cloneMember(*m)
	m.Flags |= discord.MemberFlagCompletedOnboarding
	w.emit(discord.MemberUpdateEvent{Member: cloneMember(*m), Previous: &prev})
	return nil
}

// ───────────────────────────────────────────────────────────── messages

// MessageExtras are the parts of a member message the Discord client can send and
// MessageCreate does not carry.
type MessageExtras struct {
	Attachments int
	StickerIDs  []string
	Poll        bool
}

// SendMessageExtras is SendMessageWith with attachments, stickers or a poll.
func (w *World) SendMessageExtras(userID, channelID string, m discord.MessageCreate, x MessageExtras) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := w.users[userID]
	if u == nil {
		return discord.Message{}, notFound("unknown user %s", userID)
	}
	c := w.channels[channelID]
	if c == nil {
		return discord.Message{}, notFound("unknown channel %s", channelID)
	}
	if err := w.checkSend(actor{userID: userID}, c); err != nil {
		return discord.Message{}, err
	}
	return w.postMessage(c, u.user, m, func(msg *discord.Message) {
		for i := range x.Attachments {
			msg.Attachments = append(msg.Attachments, discord.Attachment{ID: w.newID(), Filename: "file" + strconv.Itoa(i)})
		}
		msg.StickerIDs = append([]string(nil), x.StickerIDs...)
		msg.Poll = x.Poll
	}), nil
}

// SendWebhookMessage posts a message through a webhook of the channel.
func (w *World) SendWebhookMessage(channelID, content string) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.channels[channelID]
	if c == nil || c.ch.ServerID == "" {
		return discord.Message{}, notFound("unknown channel %s", channelID)
	}
	hook := w.newID()
	author := discord.User{ID: hook, Username: "Webhook", Bot: true}
	return w.postMessage(c, author, discord.MessageCreate{Content: content}, func(msg *discord.Message) { msg.WebhookID = hook }), nil
}

// postMessage is newMessage with extra fields set before the MessageCreate event.
// Caller holds w.mu.
func (w *World) postMessage(c *channelState, author discord.User, m discord.MessageCreate, extra func(*discord.Message)) discord.Message {
	// newMessage emits the event: build the message the same way, then emit.
	msg := discord.Message{
		ID: w.newID(), ChannelID: c.ch.ID, ServerID: c.ch.ServerID, Author: author,
		Content: m.Content, Embeds: cloneEmbeds(m.Embeds), Flags: m.Flags &^ discord.MessageFlagEphemeral,
		CreatedAt: w.now(), ReplyToID: m.ReplyTo,
	}
	if s := w.servers[c.ch.ServerID]; s != nil {
		if mem := s.members[author.ID]; mem != nil {
			msg.Member = cloneMemberPtr(mem)
		}
	}
	extra(&msg)
	ms := &messageState{msg: msg}
	w.messages[msg.ID] = ms
	ms.mentions = w.resolveMentions(c, author.ID, m)
	c.msgIDs = append(c.msgIDs, msg.ID)
	w.emit(discord.MessageCreateEvent{Message: cloneMessage(ms.msg)})
	return cloneMessage(ms.msg)
}

// ───────────────────────────────────────────────────────────── REST

// GetServerCounts returns the member count, the members online, idle or dnd (members
// with a presence, and bots with one set), and the boosts.
func (c *Client) GetServerCounts(ctx context.Context, serverID string) (discord.ServerCounts, error) {
	if err := ctx.Err(); err != nil {
		return discord.ServerCounts{}, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return discord.ServerCounts{}, err
	}
	if s.members[c.a.userID] == nil {
		return discord.ServerCounts{}, forbidden("missing access to server %s", serverID)
	}
	online := 0
	a := w.activity(serverID)
	for id := range s.members {
		if p, ok := a.presences[id]; ok && p.Status != "offline" {
			online++
		} else if u := w.users[id]; u != nil && u.presence != nil && u.presence.Status != "invisible" {
			online++
		}
	}
	return discord.ServerCounts{ApproximateMembers: len(s.members), ApproximateOnline: online,
		Boosts: s.info.PremiumSubscriptionCount, BoostTier: s.info.PremiumTier}, nil
}

// ListServerBots returns the user IDs of the server's bot members.
func (c *Client) ListServerBots(ctx context.Context, serverID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if s.members[c.a.userID] == nil {
		return nil, forbidden("missing access to server %s", serverID)
	}
	var bots []string
	for id := range s.members {
		if u := w.users[id]; u != nil && u.user.Bot {
			bots = append(bots, id)
		}
	}
	sort.Strings(bots)
	return bots, nil
}

// ListScheduledEventInterest returns the scheduled events of a server.
func (c *Client) ListScheduledEventInterest(ctx context.Context, serverID string) ([]discord.ScheduledEventInterest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if s.members[c.a.userID] == nil {
		return nil, forbidden("missing access to server %s", serverID)
	}
	a := w.activity(serverID)
	out := make([]discord.ScheduledEventInterest, 0, len(a.eventIDs))
	for _, id := range a.eventIDs {
		out = append(out, *a.events[id])
	}
	return out, nil
}

// ListInviteUses returns the invites and the vanity URL of a server (ManageServer).
func (c *Client) ListInviteUses(ctx context.Context, serverID string) ([]discord.InviteUses, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	if err := w.requireServer(c.a, s, permManageServer); err != nil {
		return nil, err
	}
	a := w.activity(serverID)
	out := make([]discord.InviteUses, 0, len(a.order)+1)
	codes := append([]string(nil), a.order...)
	sort.Strings(codes)
	for _, code := range codes {
		out = append(out, *a.invites[code])
	}
	if a.vanity != nil {
		out = append(out, *a.vanity)
	}
	return out, nil
}
