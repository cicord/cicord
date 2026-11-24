package sim

import (
	"strings"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
)

// ───────────────────────────────────────────────────────────── validation

func validateMessage(m discord.MessageCreate) error {
	if utf8.RuneCountInString(m.Content) > 2000 {
		return badRequest("content must be at most 2000 characters")
	}
	if m.Content == "" && len(m.Embeds) == 0 && len(m.Components) == 0 {
		return badRequest("cannot send an empty message")
	}
	if len(m.Embeds) > 10 {
		return badRequest("at most 10 embeds")
	}
	total := 0
	for i, e := range m.Embeds {
		if utf8.RuneCountInString(e.Title) > 256 || utf8.RuneCountInString(e.Description) > 4096 {
			return badRequest("embed %d: title max 256, description max 4096 characters", i)
		}
		if len(e.Fields) > 25 {
			return badRequest("embed %d: at most 25 fields", i)
		}
		total += utf8.RuneCountInString(e.Title) + utf8.RuneCountInString(e.Description)
		for _, f := range e.Fields {
			if f.Name == "" || f.Value == "" || utf8.RuneCountInString(f.Name) > 256 || utf8.RuneCountInString(f.Value) > 1024 {
				return badRequest("embed %d: field name (1-256) and value (1-1024) required", i)
			}
			total += utf8.RuneCountInString(f.Name) + utf8.RuneCountInString(f.Value)
		}
		if e.Footer != nil {
			total += utf8.RuneCountInString(e.Footer.Text)
		}
		if e.Author != nil {
			total += utf8.RuneCountInString(e.Author.Name)
		}
		if title, desc := e.Title, e.Description; title == "" && desc == "" && len(e.Fields) == 0 &&
			e.ImageURL == "" && e.ThumbURL == "" && e.Author == nil && e.Footer == nil {
			return badRequest("embed %d is empty", i)
		}
	}
	if total > 6000 {
		return badRequest("embeds exceed 6000 characters in total")
	}
	return validateComponents(m.Components)
}

func validateComponents(rows []discord.Component) error {
	if len(rows) > 5 {
		return badRequest("at most 5 action rows")
	}
	ids := map[string]bool{}
	checkID := func(id string) error {
		if id == "" || utf8.RuneCountInString(id) > 100 {
			return badRequest("custom ID must be 1-100 characters")
		}
		if ids[id] {
			return badRequest("duplicate custom ID %q", id)
		}
		ids[id] = true
		return nil
	}
	for _, row := range rows {
		if row.Type != discord.ComponentActionRow {
			return badRequest("top-level components must be action rows")
		}
		if len(row.Components) == 0 || len(row.Components) > 5 {
			return badRequest("an action row holds 1-5 components")
		}
		for _, c := range row.Components {
			switch c.Type {
			case discord.ComponentButton:
				if len(row.Components) > 5 {
					return badRequest("at most 5 buttons per row")
				}
				if c.Label == "" && c.Emoji == "" {
					return badRequest("button needs a label or an emoji")
				}
				if utf8.RuneCountInString(c.Label) > 80 {
					return badRequest("button label must be at most 80 characters")
				}
				if c.Style == discord.ButtonLink {
					if c.URL == "" || c.CustomID != "" {
						return badRequest("link buttons need a URL and no custom ID")
					}
					continue
				}
				if c.Style < discord.ButtonPrimary || c.Style > discord.ButtonDanger {
					return badRequest("invalid button style %d", c.Style)
				}
				if c.URL != "" {
					return badRequest("only link buttons have a URL")
				}
				if err := checkID(c.CustomID); err != nil {
					return err
				}
			case discord.ComponentStringSelect:
				if len(row.Components) != 1 {
					return badRequest("a select menu must be alone in its action row")
				}
				if err := checkID(c.CustomID); err != nil {
					return err
				}
				if len(c.Options) == 0 || len(c.Options) > 25 {
					return badRequest("a select menu has 1-25 options")
				}
				values := map[string]bool{}
				for _, o := range c.Options {
					if o.Label == "" || o.Value == "" || utf8.RuneCountInString(o.Label) > 100 || utf8.RuneCountInString(o.Value) > 100 {
						return badRequest("select option label and value must be 1-100 characters")
					}
					if values[o.Value] {
						return badRequest("duplicate select option value %q", o.Value)
					}
					values[o.Value] = true
				}
				minV, maxV := selectBounds(c)
				if minV < 0 || minV > 25 || maxV < 1 || maxV > 25 || minV > maxV || maxV > len(c.Options) {
					return badRequest("invalid select min/max values")
				}
			default:
				return badRequest("component type %d not allowed in messages", c.Type)
			}
		}
	}
	return nil
}

func selectBounds(c discord.Component) (int, int) {
	minV, maxV := 1, 1
	if c.MinValues != nil {
		minV = *c.MinValues
	}
	if c.MaxValues != nil {
		maxV = *c.MaxValues
	}
	return minV, maxV
}

func validateModal(m *discord.Modal) error {
	if m == nil {
		return badRequest("modal response without a modal")
	}
	if m.CustomID == "" || utf8.RuneCountInString(m.CustomID) > 100 {
		return badRequest("modal custom ID must be 1-100 characters")
	}
	if m.Title == "" || utf8.RuneCountInString(m.Title) > 45 {
		return badRequest("modal title must be 1-45 characters")
	}
	if len(m.Components) == 0 || len(m.Components) > 5 {
		return badRequest("a modal has 1-5 action rows")
	}
	ids := map[string]bool{}
	for _, row := range m.Components {
		if row.Type != discord.ComponentActionRow || len(row.Components) != 1 || row.Components[0].Type != discord.ComponentTextInput {
			return badRequest("each modal row must hold exactly one text input")
		}
		ti := row.Components[0]
		if ti.CustomID == "" || ids[ti.CustomID] {
			return badRequest("text inputs need unique custom IDs")
		}
		ids[ti.CustomID] = true
		if ti.Label == "" || utf8.RuneCountInString(ti.Label) > 45 {
			return badRequest("text input label must be 1-45 characters")
		}
		if ti.TextStyle != 1 && ti.TextStyle != 2 {
			return badRequest("text input style must be 1 (short) or 2 (paragraph)")
		}
	}
	return nil
}

// ───────────────────────────────────────────────────────────── create / edit

// newMessage stores a message (no checks) and emits MessageCreate unless ephemeral.
// Caller holds w.mu.
func (w *World) newMessage(c *channelState, author discord.User, m discord.MessageCreate, ephemeralFor string) *messageState {
	msg := discord.Message{
		ID:         w.newID(),
		ChannelID:  c.ch.ID,
		ServerID:   c.ch.ServerID,
		Author:     author,
		Content:    m.Content,
		Embeds:     cloneEmbeds(m.Embeds),
		Components: cloneComponents(m.Components),
		Flags:      m.Flags,
		CreatedAt:  w.now(),
	}
	if s := w.servers[c.ch.ServerID]; s != nil {
		if mem := s.members[author.ID]; mem != nil {
			msg.Member = cloneMemberPtr(mem)
		}
	}
	ms := &messageState{msg: msg}
	w.messages[msg.ID] = ms
	if ephemeralFor != "" {
		ms.ephemeral, ms.ephemeralFor = true, ephemeralFor
		ms.msg.Flags |= discord.MessageFlagEphemeral
		return ms
	}
	ms.msg.Flags &^= discord.MessageFlagEphemeral
	c.msgIDs = append(c.msgIDs, msg.ID)
	w.emit(discord.MessageCreateEvent{Message: cloneMessage(ms.msg)})
	return ms
}

// editMessage replaces content, embeds and components and emits MessageUpdate unless
// ephemeral. Caller holds w.mu.
func (w *World) editMessage(ms *messageState, m discord.MessageCreate) {
	ms.msg.Content = m.Content
	ms.msg.Embeds = cloneEmbeds(m.Embeds)
	ms.msg.Components = cloneComponents(m.Components)
	ms.msg.Flags &^= msgFlagLoading
	now := w.now()
	ms.msg.EditedAt = &now
	if !ms.ephemeral {
		w.emit(discord.MessageUpdateEvent{Message: cloneMessage(ms.msg)})
	}
}

// checkSend verifies actor may post in channel c. Caller holds w.mu.
func (w *World) checkSend(a actor, c *channelState) error {
	if c.ch.Type == discord.ChannelDM {
		if c.dmUsers[0] != a.userID && c.dmUsers[1] != a.userID {
			return forbidden("missing access to channel %s", c.ch.ID)
		}
		if a.userID == c.dmUsers[1] { // the bot writes to the user
			u := w.users[c.dmUsers[0]]
			if u.dmsDisabled || !w.shareServer(c.dmUsers[0], c.dmUsers[1]) {
				return forbidden("cannot send messages to this user")
			}
		}
		return nil
	}
	if !c.ch.Type.IsText() {
		return badRequest("cannot send messages in a %s channel", c.ch.Type)
	}
	need := permSend
	if isThread(c.ch.Type) {
		need = permSendInThreads
	}
	return w.requireChannel(a, c, need)
}

func (w *World) shareServer(a, b string) bool {
	for _, s := range w.servers {
		if s.members[a] != nil && s.members[b] != nil {
			return true
		}
	}
	return false
}

func (w *World) sendMessage(a actor, channelID string, m discord.MessageCreate) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	serverID := ""
	fail := func(err error) (discord.Message, error) {
		return discord.Message{}, w.record(a, "SendMessage", serverID, channelID, "", err)
	}
	c, err := w.channel(channelID)
	if err != nil {
		return fail(err)
	}
	serverID = c.ch.ServerID
	if err := w.checkSend(a, c); err != nil {
		return fail(err)
	}
	if err := validateMessage(m); err != nil {
		return fail(err)
	}
	if m.ReplyTo != "" {
		if r := w.messages[m.ReplyTo]; r == nil || r.ephemeral || r.msg.ChannelID != channelID {
			return fail(badRequest("unknown message to reply to %s", m.ReplyTo))
		}
	}
	m.Flags &^= discord.MessageFlagEphemeral | msgFlagLoading
	ms := w.newMessage(c, w.users[a.userID].user, m, "")
	w.recordOK(a, "SendMessage", serverID, ms.msg.ID, "")
	return cloneMessage(ms.msg), nil
}

func (w *World) channelMessage(channelID, messageID string) (*channelState, *messageState, error) {
	c, err := w.channel(channelID)
	if err != nil {
		return nil, nil, err
	}
	ms := w.messages[messageID]
	if ms == nil || ms.ephemeral || ms.msg.ChannelID != channelID {
		return nil, nil, notFound("unknown message %s", messageID)
	}
	return c, ms, nil
}

func (w *World) editMessageCall(a actor, channelID, messageID string, m discord.MessageCreate) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	serverID := ""
	fail := func(err error) (discord.Message, error) {
		return discord.Message{}, w.record(a, "EditMessage", serverID, messageID, "", err)
	}
	c, ms, err := w.channelMessage(channelID, messageID)
	if err != nil {
		return fail(err)
	}
	serverID = c.ch.ServerID
	if err := w.requireChannel(a, c, 0); err != nil {
		return fail(err)
	}
	if ms.msg.Author.ID != a.userID {
		return fail(forbidden("cannot edit a message authored by another user"))
	}
	if err := validateMessage(m); err != nil {
		return fail(err)
	}
	w.editMessage(ms, m)
	w.recordOK(a, "EditMessage", serverID, messageID, "")
	return cloneMessage(ms.msg), nil
}

func (w *World) deleteMessage(a actor, channelID, messageID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	serverID := ""
	fail := func(err error) error { return w.record(a, "DeleteMessage", serverID, messageID, reason, err) }
	c, ms, err := w.channelMessage(channelID, messageID)
	if err != nil {
		return fail(err)
	}
	serverID = c.ch.ServerID
	var need discord.Permissions
	if ms.msg.Author.ID != a.userID {
		if c.ch.Type == discord.ChannelDM {
			return fail(forbidden("cannot delete another user's message in a DM"))
		}
		need = permManageMessages
	}
	if err := w.requireChannel(a, c, need); err != nil {
		return fail(err)
	}
	w.recordOK(a, "DeleteMessage", serverID, messageID, reason)
	w.removeMessage(c, ms)
	return nil
}

func (w *World) removeMessage(c *channelState, ms *messageState) {
	delete(w.messages, ms.msg.ID)
	c.msgIDs = removeString(c.msgIDs, ms.msg.ID)
	w.emit(discord.MessageDeleteEvent{ID: ms.msg.ID, ChannelID: c.ch.ID, ServerID: c.ch.ServerID})
}

func (w *World) listMessages(a actor, channelID string, p discord.ListMessagesParams) ([]discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, err := w.channel(channelID)
	if err != nil {
		return nil, err
	}
	if c.ch.Type != discord.ChannelDM {
		if err := w.requireChannel(a, c, permReadHistory); err != nil {
			return nil, err
		}
	} else if err := w.requireChannel(a, c, 0); err != nil {
		return nil, err
	}
	// Like the disgo adapter: 0 means the default 50, other values are clamped to 1..100.
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	limit = min(max(limit, 1), 100)
	if p.Before != "" && p.After != "" {
		return nil, badRequest("before and after are mutually exclusive")
	}
	ids := c.msgIDs // oldest first
	var picked []string
	switch {
	case p.After != "":
		for _, id := range ids {
			if snowLess(p.After, id) {
				picked = append(picked, id)
				if len(picked) == limit {
					break
				}
			}
		}
	default:
		for i := len(ids) - 1; i >= 0 && len(picked) < limit; i-- {
			if p.Before == "" || snowLess(ids[i], p.Before) {
				picked = append([]string{ids[i]}, picked...)
			}
		}
	}
	bot := w.botsByUser[a.userID]
	out := make([]discord.Message, 0, len(picked))
	for i := len(picked) - 1; i >= 0; i-- { // newest first, like Discord
		m := w.messages[picked[i]].msg
		if bot != nil {
			m = w.redact(m, a.userID, bot.privileged.Has(intentMessageContent))
		} else {
			m = cloneMessage(m)
		}
		out = append(out, m)
	}
	return out, nil
}

// ───────────────────────────────────────────────────────────── reactions

func validEmoji(e string) bool {
	return e != "" && !strings.ContainsAny(e, " \t\n") && utf8.RuneCountInString(e) <= 100
}

func (w *World) react(a actor, add bool, channelID, messageID, emoji string, log bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	method := "RemoveOwnReaction"
	if add {
		method = "AddReaction"
	}
	serverID := ""
	fail := func(err error) error {
		if log {
			return w.record(a, method, serverID, messageID, "", err)
		}
		return err
	}
	c, ms, err := w.channelMessage(channelID, messageID)
	if err != nil {
		return fail(err)
	}
	serverID = c.ch.ServerID
	if !validEmoji(emoji) {
		return fail(badRequest("unknown emoji %q", emoji))
	}
	var r *reaction
	for _, x := range ms.reactions {
		if x.emoji == emoji {
			r = x
		}
	}
	need := permReadHistory
	if add && r == nil {
		need |= permAddReactions
	}
	if c.ch.Type == discord.ChannelDM {
		need = 0
	}
	if err := w.requireChannel(a, c, need); err != nil {
		return fail(err)
	}
	_ = fail(nil)
	has := r != nil && containsString(r.users, a.userID)
	if add == has {
		return nil // idempotent, like Discord's 204
	}
	if add {
		if r == nil {
			r = &reaction{emoji: emoji}
			ms.reactions = append(ms.reactions, r)
		}
		r.users = append(r.users, a.userID)
	} else {
		r.users = removeString(r.users, a.userID)
		if len(r.users) == 0 {
			kept := ms.reactions[:0:0]
			for _, x := range ms.reactions {
				if x != r {
					kept = append(kept, x)
				}
			}
			ms.reactions = kept
		}
	}
	ev := discord.ReactionEvent{ServerID: serverID, ChannelID: channelID, MessageID: messageID, UserID: a.userID, Emoji: emoji}
	if s := w.servers[serverID]; s != nil && s.members[a.userID] != nil {
		ev.Member = cloneMemberPtr(s.members[a.userID])
	}
	if add {
		w.emit(discord.ReactionAddEvent{ReactionEvent: ev})
	} else {
		w.emit(discord.ReactionRemoveEvent{ReactionEvent: ev})
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ───────────────────────────────────────────────────────────── DMs

func dmKey(userID, botID string) string { return userID + ":" + botID }

// dmChannel returns (creating if needed) the DM between a human and a bot. Caller holds w.mu.
func (w *World) dmChannel(userID, botID string) (*channelState, error) {
	u, b := w.users[userID], w.users[botID]
	if u == nil {
		return nil, notFound("unknown user %s", userID)
	}
	if b == nil {
		return nil, notFound("unknown user %s", botID)
	}
	if u.user.Bot {
		return nil, forbidden("cannot send messages to this user")
	}
	if !b.user.Bot {
		return nil, badRequest("DMs between humans are not simulated")
	}
	if id, ok := w.dms[dmKey(userID, botID)]; ok {
		return w.channels[id], nil
	}
	c := &channelState{ch: discord.Channel{ID: w.newID(), Type: discord.ChannelDM}, dmUsers: [2]string{userID, botID}}
	w.channels[c.ch.ID] = c
	w.dms[dmKey(userID, botID)] = c.ch.ID
	return c, nil
}

func (w *World) createDM(a actor, userID string) (discord.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if userID == a.userID {
		return discord.Channel{}, w.record(a, "CreateDM", "", userID, "", badRequest("cannot DM yourself"))
	}
	c, err := w.dmChannel(userID, a.userID)
	if err != nil {
		return discord.Channel{}, w.record(a, "CreateDM", "", userID, "", err)
	}
	w.recordOK(a, "CreateDM", "", c.ch.ID, "")
	return cloneChannel(c.ch), nil
}

// DMChannel returns the DM channel between a human user and a bot, creating it if
// needed (a human uses it to message the bot with SendMessage).
func (w *World) DMChannel(userID, botUserID string) (discord.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, err := w.dmChannel(userID, botUserID)
	if err != nil {
		return discord.Channel{}, err
	}
	return cloneChannel(c.ch), nil
}

// DMs returns the messages of the DM between userID and botUserID, oldest first
// (nil if no DM channel exists yet).
func (w *World) DMs(userID, botUserID string) []discord.Message {
	w.mu.Lock()
	id, ok := w.dms[dmKey(userID, botUserID)]
	w.mu.Unlock()
	if !ok {
		return nil
	}
	return w.Messages(id)
}

// ───────────────────────────────────────────────────────────── human actions

// SendMessage posts a message as userID (a human in the Discord client) and emits
// MessageCreateEvent. The user needs ViewChannel and SendMessages in server channels.
func (w *World) SendMessage(userID, channelID, content string) (discord.Message, error) {
	return w.SendMessageWith(userID, channelID, discord.MessageCreate{Content: content})
}

// SendMessageWith is SendMessage with a full payload (embeds, reply, …).
func (w *World) SendMessageWith(userID, channelID string, m discord.MessageCreate) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.users[userID] == nil {
		return discord.Message{}, notFound("unknown user %s", userID)
	}
	a := actor{userID: userID}
	c, err := w.channel(channelID)
	if err != nil {
		return discord.Message{}, err
	}
	if err := w.checkSend(a, c); err != nil {
		return discord.Message{}, err
	}
	if err := validateMessage(m); err != nil {
		return discord.Message{}, err
	}
	m.Flags &^= discord.MessageFlagEphemeral | msgFlagLoading
	return cloneMessage(w.newMessage(c, w.users[userID].user, m, "").msg), nil
}

// AddReaction reacts to a message as userID (emits ReactionAddEvent).
func (w *World) AddReaction(userID, channelID, messageID, emoji string) error {
	return w.humanReact(userID, true, channelID, messageID, emoji)
}

// RemoveReaction removes userID's reaction (emits ReactionRemoveEvent).
func (w *World) RemoveReaction(userID, channelID, messageID, emoji string) error {
	return w.humanReact(userID, false, channelID, messageID, emoji)
}

func (w *World) humanReact(userID string, add bool, channelID, messageID, emoji string) error {
	// Human reactions are stimuli, not client calls: they are not logged.
	return w.react(actor{userID: userID}, add, channelID, messageID, emoji, false)
}
