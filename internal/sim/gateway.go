package sim

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/cicord/cicord/internal/discord"
)

var (
	_ discord.Connector              = (*World)(nil)
	_ discord.Session                = (*Session)(nil)
	_ discord.Client                 = (*Client)(nil)
	_ discord.ApplicationFlagsReader = (*Session)(nil)
)

// Session is a simulated bot session: the REST client of the bot plus a gateway
// connection. Obtain one with World.Connect.
type Session struct {
	Client
	bot *botState

	// guarded by w.mu
	opened  bool
	closed  bool
	intents discord.Intents

	qmu     sync.Mutex
	qcond   *sync.Cond
	queue   []discord.Event
	qclosed bool
	handler func(discord.Event)
}

// Connect validates the token and returns a new session (gateway not opened yet).
// Several sessions may be created for the same token; each opened one receives events.
func (w *World) Connect(ctx context.Context, token string) (discord.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.botsByToken[token]
	if b == nil {
		return nil, errf(discord.ErrInvalidToken, "unknown bot token")
	}
	s := &Session{Client: Client{w: w, a: actor{userID: b.userID, appID: b.appID}}, bot: b}
	s.qcond = sync.NewCond(&s.qmu)
	return s, nil
}

// Self returns the bot user.
func (s *Session) Self() discord.User {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return s.w.users[s.bot.userID].user
}

// ApplicationID returns the bot's application ID.
func (s *Session) ApplicationID() string { return s.bot.appID }

// ApplicationFlags returns the application flags of the privileged intents enabled in
// the simulated developer portal (World.SetPrivilegedIntents).
func (s *Session) ApplicationFlags() int {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return discord.FlagsFromIntents(s.bot.privileged)
}

// Intents returns the intents the gateway was opened with.
func (s *Session) Intents() discord.Intents {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return s.intents
}

// Open connects to the simulated gateway. A ReadyEvent is delivered first. The handler
// runs on a single goroutine per session. ctx only bounds the connection attempt; the
// gateway stays connected until Close. Requesting a privileged intent that is not
// enabled for the application (see World.SetPrivilegedIntents) fails with ErrForbidden,
// as Discord closes the gateway with 4014 "disallowed intents".
func (s *Session) Open(ctx context.Context, intents discord.Intents, handler func(discord.Event)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if handler == nil {
		return badRequest("nil handler")
	}
	w := s.w
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case s.closed:
		return badRequest("session closed")
	case s.opened:
		return badRequest("gateway already open")
	}
	if missing := intents & discord.PrivilegedIntents &^ s.bot.privileged; missing != 0 {
		return forbidden("disallowed intents %s (not enabled for the application)", strings.Join(missing.Names(), ", "))
	}
	s.opened, s.intents = true, intents
	s.handler = handler
	w.sessions[s] = struct{}{}

	var ids []string
	for id, srv := range w.servers {
		if srv.members[s.bot.userID] != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	s.enqueue(discord.ReadyEvent{User: w.users[s.bot.userID].user, ServerIDs: ids})
	for _, id := range ids { // the GUILD_CREATE of each server
		if out, ok := w.filterFor(s, w.serverAvailable(id)); ok {
			s.enqueue(out)
		}
	}
	go s.dispatch()
	return nil
}

// Close disconnects the gateway. Queued events are dropped; a handler already running
// completes. REST calls keep working.
func (s *Session) Close(ctx context.Context) error {
	w := s.w
	w.mu.Lock()
	s.closed = true
	delete(w.sessions, s)
	w.mu.Unlock()

	s.qmu.Lock()
	dropped := len(s.queue)
	s.queue = nil
	s.qclosed = true
	s.qcond.Broadcast()
	s.qmu.Unlock()
	w.idleAdd(-dropped)
	return nil
}

// enqueue appends an event to the session queue. Caller holds w.mu.
func (s *Session) enqueue(ev discord.Event) {
	s.w.idleAdd(1)
	s.qmu.Lock()
	s.queue = append(s.queue, ev)
	s.qcond.Signal()
	s.qmu.Unlock()
}

func (s *Session) dispatch() {
	// If a handler exits its goroutine (runtime.Goexit, e.g. t.Fatal in a test), close
	// the session so that its pending events are released and WaitIdle cannot hang.
	defer func() { _ = s.Close(context.Background()) }()
	for {
		s.qmu.Lock()
		for len(s.queue) == 0 && !s.qclosed {
			s.qcond.Wait()
		}
		if s.qclosed {
			s.qmu.Unlock()
			return
		}
		ev := s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		s.qmu.Unlock()

		func() {
			defer s.w.idleAdd(-1)
			s.handler(ev)
		}()
	}
}

func (w *World) idleAdd(n int) {
	if n == 0 {
		return
	}
	w.idleMu.Lock()
	w.pending += n
	if w.pending <= 0 {
		w.pending = 0
		w.idleCond.Broadcast()
	}
	w.idleMu.Unlock()
}

// WaitIdle blocks until every queued gateway event has been delivered to every session
// handler and the handlers have returned, including events produced by handlers
// themselves. It must not be called from inside a handler.
func (w *World) WaitIdle() {
	w.idleMu.Lock()
	for w.pending > 0 {
		w.idleCond.Wait()
	}
	w.idleMu.Unlock()
}

// WaitIdleContext is WaitIdle bounded by ctx.
func (w *World) WaitIdleContext(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		w.WaitIdle()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Flush is an alias of WaitIdle.
func (w *World) Flush() { w.WaitIdle() }

// ───────────────────────────────────────────────────────────── emission & filtering

var (
	intentServers         = discord.MustIntents("Servers")
	intentMembers         = discord.MustIntents("ServerMembers")
	intentMessages        = discord.MustIntents("ServerMessages")
	intentReactions       = discord.MustIntents("ServerMessageReactions")
	intentDMs             = discord.MustIntents("DirectMessages")
	intentDMReactions     = discord.MustIntents("DirectMessageReactions")
	intentMessageContent  = discord.MustIntents("MessageContent")
	intentAutoModConfig   = discord.MustIntents("AutoModerationConfiguration")
	intentExpressions     = discord.MustIntents("ServerExpressions")
	permView              = discord.MustPermissions("ViewChannel")
	permAdmin             = discord.MustPermissions("Administrator")
	permReadHistory       = discord.MustPermissions("ReadMessageHistory")
	permSend              = discord.MustPermissions("SendMessages")
	permSendInThreads     = discord.MustPermissions("SendMessagesInThreads")
	permManageRoles       = discord.MustPermissions("ManageRoles")
	permManageChannels    = discord.MustPermissions("ManageChannels")
	permManageServer      = discord.MustPermissions("ManageServer")
	permManageMessages    = discord.MustPermissions("ManageMessages")
	permManageNicknames   = discord.MustPermissions("ManageNicknames")
	permChangeNickname    = discord.MustPermissions("ChangeNickname")
	permKick              = discord.MustPermissions("KickMembers")
	permBan               = discord.MustPermissions("BanMembers")
	permModerate          = discord.MustPermissions("ModerateMembers")
	permAddReactions      = discord.MustPermissions("AddReactions")
	permPublicThreads     = discord.MustPermissions("CreatePublicThreads")
	permPrivateThreads    = discord.MustPermissions("CreatePrivateThreads")
	permUseAppCommands    = discord.MustPermissions("UseApplicationCommands")
	permTimeoutRestricted = permView | permReadHistory
)

// emit delivers ev to every open session allowed to see it. Caller holds w.mu.
func (w *World) emit(ev discord.Event) {
	w.changed()
	// Iterate sessions in a stable order so multi-session tests are deterministic.
	sessions := make([]*Session, 0, len(w.sessions))
	for s := range w.sessions {
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].bot.userID < sessions[j].bot.userID })
	for _, s := range sessions {
		if out, ok := w.filterFor(s, ev); ok {
			s.enqueue(out)
		}
	}
}

// emitToBot delivers ev to the open sessions of one bot only (server join/leave, which
// Discord sends to the session concerned). Caller holds w.mu.
func (w *World) emitToBot(botUserID string, ev discord.Event) {
	w.changed()
	for s := range w.sessions {
		if s.bot.userID != botUserID {
			continue
		}
		if out, ok := w.filterFor(s, ev); ok {
			s.enqueue(out)
		}
	}
}

// filterFor decides whether session s receives ev and returns the (possibly redacted)
// copy it receives. Caller holds w.mu.
func (w *World) filterFor(s *Session, ev discord.Event) (discord.Event, bool) {
	botID := s.bot.userID
	if ix, ok := ev.(discord.InteractionCreateEvent); ok {
		// Interactions ignore intents and go to the application only.
		if ix.Interaction.ApplicationID != s.bot.appID {
			return nil, false
		}
		return discord.InteractionCreateEvent{Interaction: cloneInteraction(ix.Interaction)}, true
	}
	switch e := ev.(type) {
	case discord.ServerJoinEvent:
		// Emitted with emitToBot only; the bot is a member by now.
		return discord.ServerJoinEvent{Server: cloneServer(e.Server)}, s.intents.Has(intentServers)
	case discord.ServerLeaveEvent:
		// The bot is no longer a member: no membership check.
		return e, s.intents.Has(intentServers)
	}
	serverID := discord.ServerIDOf(ev)
	if serverID != "" {
		srv := w.servers[serverID]
		if srv == nil || srv.members[botID] == nil {
			return nil, false
		}
	}
	has := func(i discord.Intents) bool { return s.intents.Has(i) }
	// canSee: server channel visible to the bot, or DM the bot participates in.
	canSee := func(channelID string) bool {
		c := w.channels[channelID]
		if c == nil {
			return false
		}
		if c.ch.Type == discord.ChannelDM {
			return c.dmUsers[1] == botID
		}
		return w.channelPerms(c, botID).Has(permView)
	}
	msgIntentOK := func(serverID string) bool {
		if serverID == "" {
			return has(intentDMs)
		}
		return has(intentMessages)
	}

	switch e := ev.(type) {
	case discord.MemberJoinEvent:
		return discord.MemberJoinEvent{Member: cloneMember(e.Member)}, has(intentMembers)
	case discord.MemberLeaveEvent:
		return e, has(intentMembers)
	case discord.MemberUpdateEvent:
		return discord.MemberUpdateEvent{Member: cloneMember(e.Member), Previous: cloneMemberPtr(e.Previous)}, has(intentMembers)
	case discord.MessageCreateEvent:
		if !msgIntentOK(e.Message.ServerID) || !canSee(e.Message.ChannelID) {
			return nil, false
		}
		return discord.MessageCreateEvent{Message: w.redact(e.Message, botID, has(intentMessageContent))}, true
	case discord.MessageUpdateEvent:
		if !msgIntentOK(e.Message.ServerID) || !canSee(e.Message.ChannelID) {
			return nil, false
		}
		return discord.MessageUpdateEvent{Message: w.redact(e.Message, botID, has(intentMessageContent))}, true
	case discord.MessageDeleteEvent:
		if !msgIntentOK(e.ServerID) {
			return nil, false
		}
		if c := w.channels[e.ChannelID]; c != nil && !canSee(e.ChannelID) {
			return nil, false
		}
		return e, true
	case discord.ReactionAddEvent, discord.ReactionRemoveEvent:
		var re discord.ReactionEvent
		if a, ok := e.(discord.ReactionAddEvent); ok {
			re = a.ReactionEvent
		} else {
			re = e.(discord.ReactionRemoveEvent).ReactionEvent
		}
		need := intentReactions
		if re.ServerID == "" {
			need = intentDMReactions
		}
		if !has(need) || !canSee(re.ChannelID) {
			return nil, false
		}
		re.Member = cloneMemberPtr(re.Member)
		if _, ok := e.(discord.ReactionAddEvent); ok {
			return discord.ReactionAddEvent{ReactionEvent: re}, true
		}
		return discord.ReactionRemoveEvent{ReactionEvent: re}, true
	case discord.ChannelCreateEvent:
		return discord.ChannelCreateEvent{Channel: cloneChannel(e.Channel), NewThread: e.NewThread}, has(intentServers)
	case discord.ChannelUpdateEvent:
		return discord.ChannelUpdateEvent{Channel: cloneChannel(e.Channel)}, has(intentServers)
	case discord.ChannelDeleteEvent:
		return discord.ChannelDeleteEvent{Channel: cloneChannel(e.Channel)}, has(intentServers)
	case discord.RoleCreateEvent, discord.RoleUpdateEvent, discord.RoleDeleteEvent, discord.ServerUpdateEvent:
		return e, has(intentServers)
	case discord.AutoModRuleCreateEvent:
		return discord.AutoModRuleCreateEvent{ServerID: e.ServerID, Rule: cloneAutoMod(e.Rule)}, has(intentAutoModConfig)
	case discord.AutoModRuleUpdateEvent:
		return discord.AutoModRuleUpdateEvent{ServerID: e.ServerID, Rule: cloneAutoMod(e.Rule)}, has(intentAutoModConfig)
	case discord.AutoModRuleDeleteEvent:
		return discord.AutoModRuleDeleteEvent{ServerID: e.ServerID, Rule: cloneAutoMod(e.Rule)}, has(intentAutoModConfig)
	case discord.EmojisUpdateEvent:
		emojis := make([]discord.Emoji, 0, len(e.Emojis))
		for _, x := range e.Emojis {
			emojis = append(emojis, cloneEmoji(x))
		}
		return discord.EmojisUpdateEvent{ServerID: e.ServerID, Emojis: emojis}, has(intentExpressions)
	case discord.StickersUpdateEvent:
		return discord.StickersUpdateEvent{ServerID: e.ServerID, Stickers: append([]discord.Sticker{}, e.Stickers...)}, has(intentExpressions)
	case discord.SoundCreateEvent, discord.SoundUpdateEvent, discord.SoundDeleteEvent:
		return e, has(intentExpressions)
	}
	return w.filterAnalytics(s, ev)
}

// redact applies Discord's message-content gating for viewer botID. Caller holds w.mu.
func (w *World) redact(m discord.Message, botID string, contentIntent bool) discord.Message {
	m = cloneMessage(m)
	if contentIntent || m.ServerID == "" || m.Author.ID == botID || mentions(m.Content, botID) {
		return m
	}
	m.Content = ""
	m.Embeds = nil
	m.Components = nil
	m.Attachments = nil
	m.Poll = false
	return m
}

func mentions(content, userID string) bool {
	return strings.Contains(content, "<@"+userID+">") || strings.Contains(content, "<@!"+userID+">")
}
