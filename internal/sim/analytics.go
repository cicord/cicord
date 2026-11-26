package sim

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"strconv"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// Server activity the analytics collector observes: voice connections, member
// presences, invites and their uses, scheduled events, audit log entries and AutoMod
// executions.

var (
	_ discord.AnalyticsReader = (*Client)(nil)
	_ discord.MemberLister    = (*Client)(nil)
)

var (
	intentVoiceStates    = discord.MustIntents("ServerVoiceStates")
	intentPresences      = discord.MustIntents("ServerPresences")
	intentModeration     = discord.MustIntents("ServerModeration")
	intentInvites        = discord.MustIntents("ServerInvites")
	intentAutoModExecute = discord.MustIntents("AutoModerationExecution")
	permViewAuditLog     = discord.MustPermissions("ViewAuditLog")
)

type serverActivity struct {
	scheduled  map[string]discord.ScheduledEvent
	interested map[string]map[string]bool
	voice      map[string]discord.VoiceState     // user ID → voice state
	presences  map[string]discord.MemberPresence // user ID → presence (not offline)
	invites    map[string]*discord.InviteUses    // code → invite
	order      []string                          // invite codes, creation order
	vanity     *discord.InviteUses
	events     map[string]*discord.ScheduledEventInterest
	eventIDs   []string
}

// activity returns the activity state of a server. Caller holds w.mu.
func (w *World) activity(serverID string) *serverActivity {
	if w.analytics == nil {
		w.analytics = map[string]*serverActivity{}
	}
	a := w.analytics[serverID]
	if a == nil {
		a = &serverActivity{scheduled: map[string]discord.ScheduledEvent{}, interested: map[string]map[string]bool{}, voice: map[string]discord.VoiceState{}, presences: map[string]discord.MemberPresence{},
			invites: map[string]*discord.InviteUses{}, events: map[string]*discord.ScheduledEventInterest{}}
		w.analytics[serverID] = a
	}
	return a
}

// serverAvailable is the GUILD_CREATE content the analytics read. Caller holds w.mu.
func (w *World) serverAvailable(serverID string) discord.ServerAvailableEvent {
	a := w.activity(serverID)
	ev := discord.ServerAvailableEvent{ServerID: serverID}
	s := w.servers[serverID]
	for _, id := range sortedKeys(a.voice) {
		st := a.voice[id]
		if s != nil {
			st = w.withVoiceMember(s, st)
		}
		ev.VoiceStates = append(ev.VoiceStates, st)
	}
	for _, id := range sortedKeys(a.presences) {
		ev.Presences = append(ev.Presences, clonePresence(a.presences[id]))
	}
	return ev
}

func cloneVoiceState(st discord.VoiceState) discord.VoiceState {
	if st.Member != nil {
		m := cloneMember(*st.Member)
		st.Member = &m
	}
	return st
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
	case discord.CommunityEvent:
		if e.Name == "pollVoteAdd" || e.Name == "pollVoteRemove" {
			return e, has(discord.MustIntents("ServerMessagePolls"))
		}
		return e, has(discord.MustIntents("ServerScheduledEvents"))
	case discord.ServerAvailableEvent:
		out := discord.ServerAvailableEvent{ServerID: e.ServerID}
		if has(intentVoiceStates) {
			for _, st := range e.VoiceStates {
				out.VoiceStates = append(out.VoiceStates, cloneVoiceState(st))
			}
		}
		if has(intentPresences) {
			for _, p := range e.Presences {
				out.Presences = append(out.Presences, clonePresence(p))
			}
		}
		return out, has(intentServers)
	case discord.VoiceStateUpdateEvent:
		e.State = cloneVoiceState(e.State)
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

// JoinVoice connects userID to a voice or stage channel (emits VoiceStateUpdateEvent),
// like the Discord client: the member needs Connect in the channel, and a full channel
// (user limit) only lets in members with MoveMembers there. Joining another channel
// moves the member.
func (w *World) JoinVoice(userID, channelID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.channels[channelID]
	if c == nil {
		return notFound("unknown channel %s", channelID)
	}
	if !voiceLike(c.ch.Type) {
		return badRequest("channel %s is not a voice channel", channelID)
	}
	s := w.servers[c.ch.ServerID]
	if s.members[userID] == nil {
		return notFound("unknown member %s", userID)
	}
	p := w.channelPerms(c, userID)
	if !p.Has(permConnect) {
		return missingErr(p, permConnect)
	}
	if !p.Has(permMoveMembers) && w.voiceFull(c, userID) {
		return forbidden("channel %s is full (user limit %d)", channelID, c.ch.UserLimit)
	}
	w.setVoice(s, userID, channelID)
	return nil
}

// LeaveVoice disconnects userID from voice.
func (w *World) LeaveVoice(serverID, userID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	if _, ok := w.activity(serverID).voice[userID]; !ok {
		return notFound("member %s is not in voice", userID)
	}
	w.setVoice(s, userID, "")
	return nil
}

// voiceFull reports whether a voice channel with a user limit is full for userID (who
// does not count when already in it). Caller holds w.mu.
func (w *World) voiceFull(c *channelState, userID string) bool {
	if c.ch.UserLimit <= 0 {
		return false
	}
	n := 0
	for id, st := range w.activity(c.ch.ServerID).voice {
		if st.ChannelID == c.ch.ID && id != userID {
			n++
		}
	}
	return n >= c.ch.UserLimit
}

// setVoice connects userID to channelID ("" disconnects) and emits the voice state with
// the member. Caller holds w.mu.
func (w *World) setVoice(s *serverState, userID, channelID string) {
	a := w.activity(s.info.ID)
	st := a.voice[userID]
	st.ServerID, st.UserID, st.ChannelID, st.Member = s.info.ID, userID, channelID, nil
	if m := s.members[userID]; m != nil {
		st.Bot = m.User.Bot
	}
	if channelID == "" {
		delete(a.voice, userID)
		st.SelfStream, st.SelfVideo = false, false
	} else {
		a.voice[userID] = st
	}
	w.emit(discord.VoiceStateUpdateEvent{State: w.withVoiceMember(s, st)})
}

// withVoiceMember attaches a copy of the member to a voice state, like Discord's
// VOICE_STATE_UPDATE payload. Caller holds w.mu.
func (w *World) withVoiceMember(s *serverState, st discord.VoiceState) discord.VoiceState {
	if m := s.members[st.UserID]; m != nil {
		cm := cloneMember(*m)
		st.Member = &cm
	}
	return st
}

// disconnectVoice disconnects every member connected to a voice channel (the channel is
// being deleted). Caller holds w.mu.
func (w *World) disconnectVoice(s *serverState, channelID string) {
	a := w.activity(s.info.ID)
	for _, id := range sortedKeys(a.voice) {
		if a.voice[id].ChannelID == channelID {
			w.setVoice(s, id, "")
		}
	}
}

// moveVoice moves (channelID != "") or disconnects (channelID == "") a member connected to
// voice, as UpdateMember's channel_id does: MoveMembers in the member's channel, and
// Connect + MoveMembers in the destination (which ignores its user limit). Caller holds
// w.mu.
func (w *World) moveVoice(a actor, s *serverState, userID, channelID, reason string) error {
	cur, ok := w.activity(s.info.ID).voice[userID]
	if !ok {
		return badRequest("target user is not connected to voice")
	}
	if src := w.channels[cur.ChannelID]; src != nil {
		if err := w.requireChannel(a, src, permMoveMembers); err != nil {
			return err
		}
	}
	if channelID != "" {
		dst := w.channels[channelID]
		if dst == nil || dst.ch.ServerID != s.info.ID {
			return notFound("unknown channel %s", channelID)
		}
		if !voiceLike(dst.ch.Type) {
			return badRequest("channel %s is not a voice channel", channelID)
		}
		if err := w.requireChannel(a, dst, permMoveMembers|permConnect); err != nil {
			return err
		}
	}
	w.setVoice(s, userID, channelID)
	if a.userID != userID && cur.ChannelID != channelID {
		action := discord.AuditMemberMove
		if channelID == "" {
			action = discord.AuditMemberDisconnect
		}
		w.countedAudit(s.info.ID, a.userID, action, "", channelID, a.auditReason(reason))
	}
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
	if stream || video {
		c := w.channels[st.ChannelID]
		if c == nil {
			return notFound("unknown voice channel %s", st.ChannelID)
		}
		if err := w.requireChannel(actor{userID: userID}, c, discord.MustPermissions("Connect", "Stream")); err != nil {
			return err
		}
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
	actor := w.users[w.servers[c.ch.ServerID].info.OwnerID].user
	a.invites[code] = &discord.InviteUses{Code: code, MaxUses: maxUses, ChannelID: channelID, Inviter: &actor}
	a.order = append(a.order, code)
	w.emit(discord.InviteCreateEvent{ServerID: c.ch.ServerID, ChannelID: channelID, Code: code, MaxUses: maxUses, Inviter: &actor})
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
		if inv != nil || a.vanity != nil && a.vanity.Code == code {
			if until := w.servers[sid].incident.InvitesDisabledUntil; until != nil && w.now().Before(*until) {
				return forbidden("invites paused by a server incident")
			}
		}
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
	e := discord.ScheduledEvent{ID: id, Name: name, Start: w.now().Add(time.Hour), Status: "scheduled"}
	a.scheduled[id] = e
	w.emit(discord.CommunityEvent{Name: "scheduledEventCreate", ServerID: serverID, EventID: id, Event: e})
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
		if event, ok := w.activity(serverID).scheduled[eventID]; ok {
			event.Status = status
			w.activity(serverID).scheduled[eventID] = event
			w.emit(discord.CommunityEvent{Name: "scheduledEventUpdate", ServerID: serverID, Event: event, EventID: eventID})
		}
	}
	return nil
}

// ───────────────────────────────────────────────────────────── moderation

// auditLog emits an audit log entry. Caller holds w.mu.
func (w *World) auditLog(serverID, executorID string, actionType int, targetID string, changes ...discord.AuditLogChange) {
	w.auditWithReason(serverID, executorID, actionType, targetID, "", changes...)
}

func (w *World) auditWithReason(serverID, executorID string, actionType int, targetID, reason string, changes ...discord.AuditLogChange) {
	w.appendAudit(serverID, executorID, actionType, targetID, reason, nil, changes...)
}

func (w *World) appendAudit(serverID, executorID string, actionType int, targetID, reason string, options map[string]json.RawMessage, changes ...discord.AuditLogChange) {
	e := discord.AuditLogEntryEvent{ServerID: serverID, ID: w.newID(), ActionType: actionType, UserID: executorID, TargetID: targetID, Changes: changes, Reason: reason, Options: options}
	if u := w.users[executorID]; u != nil {
		actor := u.user
		e.Actor = &actor
	}
	if server := w.servers[serverID]; server != nil {
		server.audit = append(server.audit, e)
	}
	// Like Discord's GUILD_AUDIT_LOG_ENTRY_CREATE: the gateway entry carries only
	// user_id. The REST history keeps the user (Get Guild Audit Log returns users).
	gateway := e
	gateway.Actor = nil
	w.emit(gateway)
}

// Discord merges repeated deletions by one actor in one channel against messages
// by the same author. REST sees the increased count under the original ID; the
// gateway only receives the initial entry and Discord never identifies a message ID.
func (w *World) auditMessageDelete(server, actor, author, channel, reason string) {
	w.countedAudit(server, actor, discord.AuditMessageDelete, author, channel, reason)
}

// countedAudit preserves the first gateway entry and reason. The simulated
// REST history increments a count for subsequent matching actions.
func (w *World) countedAudit(server, actor string, action int, target, channel, reason string) {
	history := w.servers[server].audit
	cutoff := w.now().Add(-30 * time.Second)
	for n := len(history) - 1; n >= 0; n-- {
		e := &history[n]
		if discord.SnowflakeTime(e.ID).Before(cutoff) {
			break
		}
		if e.ActionType != action || e.UserID != actor || e.TargetID != target {
			continue
		}
		var from, count string
		if channel != "" && (json.Unmarshal(e.Options["channel_id"], &from) != nil || from != channel) {
			continue
		}
		if json.Unmarshal(e.Options["count"], &count) != nil {
			continue
		}
		total, err := strconv.Atoi(count)
		if err != nil {
			continue
		}
		e.Options = maps.Clone(e.Options)
		e.Options["count"] = auditValue(strconv.Itoa(total + 1))
		return
	}
	opts := map[string]json.RawMessage{"count": auditValue("1")}
	if channel != "" {
		opts["channel_id"] = auditValue(channel)
	}
	w.appendAudit(server, actor, action, target, reason, opts)
}

// auditMemberUpdate logs a timeout change (MEMBER_UPDATE with communication_disabled_until)
// and nickname changes made by someone else. Caller holds w.mu.
func (w *World) auditMemberUpdate(serverID, executorID string, prev, next discord.Member, reason string) {
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
		w.auditWithReason(serverID, executorID, discord.AuditMemberUpdate, next.User.ID, reason, changes...)
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
	if err := w.requireServer(c.actorFor(ctx), s, permManageServer); err != nil {
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

func auditValue(value any) json.RawMessage { data, _ := json.Marshal(value); return data }

// WebhooksUpdated delivers Discord's channel-scoped notification without inventing
// an audit actor or exposing webhook URLs.
func (w *World) WebhooksUpdated(channelID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch, err := w.channel(channelID)
	if err != nil {
		return err
	}
	if ch.ch.ServerID == "" {
		return badRequest("webhooks must belong to a guild channel")
	}
	w.emit(discord.WebhooksUpdateEvent{ServerID: ch.ch.ServerID, ChannelID: channelID})
	return nil
}
