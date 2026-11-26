package sim

import (
	"regexp"
	"slices"
	"strings"
	"time"
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
	if m.AllowedMentions != nil {
		if err := m.AllowedMentions.Validate(); err != nil {
			return err
		}
	}
	if m.Flags&discord.MessageFlagComponentsV2 != 0 {
		if m.Content != "" || len(m.Embeds) > 0 {
			return badRequest("layout is exclusive with content and embeds")
		}
		count := 0
		return validateLayout(m.Components, &count)
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
			case discord.ComponentUserSelect, discord.ComponentRoleSelect, discord.ComponentMentionableSelect, discord.ComponentChannelSelect:
				if len(row.Components) != 1 {
					return badRequest("a select must be alone in the row")
				}
				if err := checkID(c.CustomID); err != nil {
					return err
				}
				lo, hi := selectBounds(c)
				if lo < 0 || hi < 1 || hi > 25 || lo > hi {
					return badRequest("invalid select bounds")
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
		if row.Type == discord.ComponentLabel && len(row.Components) == 1 && row.Label != "" {
			child := row.Components[0]
			if child.CustomID == "" || ids[child.CustomID] {
				return badRequest("invalid modal field ID")
			}
			ids[child.CustomID] = true
			if child.Type != discord.ComponentTextInput && child.Type != discord.ComponentStringSelect && !child.Type.EntitySelect() {
				return badRequest("invalid modal field")
			}
			continue
		}
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
func (w *World) newMessage(c *channelState, author discord.User, m discord.MessageCreate, ephemeralFor string, answers ...*discord.MessageInteraction) *messageState {
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
		ReplyToID:  m.ReplyTo,
	}
	for _, id := range c.webhooks {
		if author.ID == id {
			msg.WebhookID = id
			break
		}
	}
	if len(answers) > 0 && answers[0] != nil {
		a := *answers[0]
		msg.Interaction = &a
	}
	if s := w.servers[c.ch.ServerID]; s != nil {
		if mem := s.members[author.ID]; mem != nil {
			msg.Member = cloneMemberPtr(mem)
		}
	}
	ms := &messageState{msg: msg, nonce: m.Nonce}
	w.messages[msg.ID] = ms
	if ephemeralFor != "" {
		ms.ephemeral, ms.ephemeralFor = true, ephemeralFor
		ms.msg.Flags |= discord.MessageFlagEphemeral
		return ms
	}
	ms.msg.Flags &^= discord.MessageFlagEphemeral
	if m.Flags&msgFlagLoading == 0 {
		ms.mentions = w.resolveMentions(c, author.ID, m)
	}
	c.msgIDs = append(c.msgIDs, msg.ID)
	w.emit(discord.MessageCreateEvent{Message: cloneMessage(ms.msg)})
	return ms
}

// editMessage replaces content, embeds and components and emits MessageUpdate unless
// ephemeral. Caller holds w.mu.
func (w *World) editMessage(ms *messageState, m discord.MessageCreate) {
	// The first edit of a deferred response is its real content: it notifies like a new
	// message. Later edits notify no one (Discord does not ping on edits).
	if ms.msg.Flags&msgFlagLoading != 0 && !ms.ephemeral {
		if c := w.channels[ms.msg.ChannelID]; c != nil {
			ms.mentions = w.resolveMentions(c, ms.msg.Author.ID, m)
		}
	}
	ms.msg.Content = m.Content
	ms.msg.Embeds = cloneEmbeds(m.Embeds)
	ms.msg.Components = cloneComponents(m.Components)
	ms.msg.Flags |= m.Flags & discord.MessageFlagComponentsV2
	ms.msg.Flags &^= msgFlagLoading
	now := w.now()
	ms.msg.EditedAt = &now
	if !ms.ephemeral {
		w.emit(discord.MessageUpdateEvent{Message: cloneMessage(ms.msg)})
	}
}

// Mentions is who a message notified: Discord's mentions, mention_roles and
// mention_everyone, after allowed_mentions and the mention permissions are applied.
type Mentions struct {
	UserIDs  []string
	RoleIDs  []string
	Everyone bool
}

var (
	userMentionRe = regexp.MustCompile(`<@!?(\d{1,20})>`)
	roleMentionRe = regexp.MustCompile(`<@&(\d{1,20})>`)
	everyoneRe    = regexp.MustCompile(`@(everyone|here)\b`)
	permMention   = discord.MustPermissions("MentionEveryone")
)

// resolveMentions follows Discord: without allowed_mentions every mention in the content
// notifies; with it, only the parsed types and the listed ids do. A role notifies only
// when it is mentionable or the author has MentionEveryone in the channel; @everyone and
// @here need MentionEveryone too. The author of the replied message is notified unless
// allowed_mentions is set without replied_user. Caller holds w.mu.
func (w *World) resolveMentions(c *channelState, authorID string, m discord.MessageCreate) Mentions {
	var out Mentions
	am := m.AllowedMentions
	all := am == nil
	for _, sm := range userMentionRe.FindAllStringSubmatch(m.Content, -1) {
		id := sm[1]
		if (all || am.Users || slices.Contains(am.UserIDs, id)) && !slices.Contains(out.UserIDs, id) {
			out.UserIDs = append(out.UserIDs, id)
		}
	}
	if m.ReplyTo != "" && (all || am.RepliedUser) {
		if r := w.messages[m.ReplyTo]; r != nil && !slices.Contains(out.UserIDs, r.msg.Author.ID) {
			out.UserIDs = append(out.UserIDs, r.msg.Author.ID)
		}
	}
	s := w.servers[c.ch.ServerID]
	if s == nil { // DMs: no roles, no @everyone
		return out
	}
	canMentionAll := w.channelPerms(c, authorID).Has(permMention)
	for _, sm := range roleMentionRe.FindAllStringSubmatch(m.Content, -1) {
		id := sm[1]
		r := s.roles[id]
		if r == nil || id == s.info.ID || slices.Contains(out.RoleIDs, id) {
			continue
		}
		if (all || am.Roles || slices.Contains(am.RoleIDs, id)) && (r.Mentionable || canMentionAll) {
			out.RoleIDs = append(out.RoleIDs, id)
		}
	}
	out.Everyone = everyoneRe.MatchString(m.Content) && (all || am.Everyone) && canMentionAll
	return out
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
	if m.EnforceNonce && m.Nonce != "" {
		for j := len(c.msgIDs) - 1; j >= 0; j-- {
			old := w.messages[c.msgIDs[j]]
			if old == nil {
				continue
			}
			if w.now().Sub(old.msg.CreatedAt) > 5*time.Minute {
				break
			}
			if old.msg.Author.ID == a.userID && old.nonce == m.Nonce {
				return cloneMessage(old.msg), nil
			}
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

func (w *World) bulkDeleteMessages(a actor, channelID string, ids []string, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch, err := w.channel(channelID)
	if err != nil {
		return err
	}
	if ch.ch.ServerID == "" || len(ids) < 2 || len(ids) > 100 {
		return badRequest("bulk delete expects 2 to 100 guild messages")
	}
	if err := w.requireChannel(a, ch, permManageMessages); err != nil {
		return err
	}
	cutoff := w.now().Add(-14 * 24 * time.Hour)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || !discord.SnowflakeTime(id).After(cutoff) {
			return badRequest("invalid bulk delete message")
		}
		seen[id] = true
	}
	for _, id := range ids {
		if m := w.messages[id]; m != nil && m.msg.ChannelID == channelID {
			w.removeMessage(ch, m)
		}
	}
	w.recordOK(a, "BulkDeleteMessages", ch.ch.ServerID, channelID, reason)
	return nil
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
	author := ms.msg.Author.ID
	w.removeMessage(c, ms)
	if author != a.userID && serverID != "" {
		w.auditLog(serverID, a.userID, discord.AuditMessageDelete, author)
	}
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
	if err := w.readAccess(a, c); err != nil {
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
	out := make([]discord.Message, 0, len(picked))
	for i := len(picked) - 1; i >= 0; i-- { // newest first, like Discord
		m := w.messageFor(a, w.messages[picked[i]])
		out = append(out, m)
	}
	return out, nil
}

// getMessage is GET /channels/{id}/messages/{id}: the permissions and the content
// redaction of listMessages.
func (w *World) getMessage(a actor, channelID, messageID string) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ms, err := w.channelMessage(channelID, messageID)
	if err != nil {
		if c, cerr := w.channel(channelID); cerr == nil {
			// An unknown message of a channel the caller cannot read is a 403, like Discord.
			if perr := w.readAccess(a, c); perr != nil {
				return discord.Message{}, perr
			}
		}
		return discord.Message{}, err
	}
	if err := w.readAccess(a, c); err != nil {
		return discord.Message{}, err
	}
	return w.messageFor(a, ms), nil
}

// readAccess checks the caller may read the history of c (ReadMessageHistory in server
// channels, membership in DMs). Caller holds w.mu.
func (w *World) readAccess(a actor, c *channelState) error {
	if c.ch.Type != discord.ChannelDM {
		return w.requireChannel(a, c, permReadHistory)
	}
	return w.requireChannel(a, c, 0)
}

// ───────────────────────────────────────────────────────────── reactions

func validEmoji(e string) bool {
	return e != "" && !strings.ContainsAny(e, " \t\n") && utf8.RuneCountInString(e) <= 100
}

func (w *World) react(a actor, add bool, channelID, messageID, emoji string, log bool) error {
	return w.reactUser(a, add, channelID, messageID, emoji, log, a.userID)
}
func (w *World) reactUser(a actor, add bool, channelID, messageID, emoji string, log bool, userID string) error {
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
	if userID != a.userID {
		need |= permManageMessages
		method = "RemoveUserReaction"
	}
	if c.ch.Type == discord.ChannelDM {
		need = 0
	}
	if err := w.requireChannel(a, c, need); err != nil {
		return fail(err)
	}
	_ = fail(nil)
	has := r != nil && containsString(r.users, userID)
	if add == has {
		return nil // idempotent, like Discord's 204
	}
	if add {
		if r == nil {
			r = &reaction{emoji: emoji}
			ms.reactions = append(ms.reactions, r)
		}
		r.users = append(r.users, userID)
	} else {
		r.users = removeString(r.users, userID)
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
	ev := discord.ReactionEvent{ServerID: serverID, ChannelID: channelID, MessageID: messageID, UserID: userID, Emoji: emoji}
	if s := w.servers[serverID]; s != nil && s.members[userID] != nil {
		ev.Member = cloneMemberPtr(s.members[userID])
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

func validateLayout(cs []discord.Component, count *int) error {
	if len(cs) == 0 {
		return badRequest("layout needs a component")
	}
	for _, c := range cs {
		*count++
		if *count > 40 {
			return badRequest("layout exceeds 40 components")
		}
		switch c.Type {
		case discord.ComponentActionRow:
			*count += len(c.Components)
			if *count > 40 {
				return badRequest("layout exceeds 40 components")
			}
			if err := validateComponents([]discord.Component{c}); err != nil {
				return err
			}
		case discord.ComponentContainer:
			if err := validateLayout(c.Components, count); err != nil {
				return err
			}
		case discord.ComponentSection:
			if len(c.Components) > 3 || c.Accessory == nil || (c.Accessory.Type != discord.ComponentThumbnail && c.Accessory.Type != discord.ComponentButton) {
				return badRequest("invalid section")
			}
			for _, t := range c.Components {
				if t.Type != discord.ComponentText {
					return badRequest("section must contain text")
				}
			}
			*count++
			if err := validateLayout(c.Components, count); err != nil {
				return err
			}
		case discord.ComponentText:
			if c.Content == "" || utf8.RuneCountInString(c.Content) > 4000 {
				return badRequest("invalid text display")
			}
		case discord.ComponentMediaGallery:
			if len(c.Items) < 1 || len(c.Items) > 10 {
				return badRequest("invalid media gallery")
			}
		case discord.ComponentFile:
			if c.File == nil || !strings.HasPrefix(c.File.URL, "attachment://") {
				return badRequest("invalid file component")
			}
		case discord.ComponentSeparator:
			if c.Spacing < 0 || c.Spacing > 2 {
				return badRequest("invalid spacing")
			}
		default:
			return badRequest("invalid layout component %d", c.Type)
		}
	}
	return nil
}
func (w *World) validEntityChoice(c discord.Component, sid, id string) bool {
	s := w.servers[sid]
	if s == nil {
		return false
	}
	switch c.Type {
	case discord.ComponentUserSelect:
		return s.members[id] != nil
	case discord.ComponentRoleSelect:
		return s.roles[id] != nil
	case discord.ComponentMentionableSelect:
		return s.members[id] != nil || s.roles[id] != nil
	case discord.ComponentChannelSelect:
		ch := w.channels[id]
		return ch != nil && ch.ch.ServerID == sid && (len(c.ChannelTypes) == 0 || slices.Contains(c.ChannelTypes, ch.ch.Type))
	}
	return false
}
