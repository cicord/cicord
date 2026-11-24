package sim

import (
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/npjonath/cicord/internal/discord"
)

type interactionState struct {
	ix      discord.Interaction
	bot     *botState
	created time.Time // real time, for the deadline

	acked       bool
	failed      bool
	respType    discord.InteractionResponseType
	ephemeral   bool
	originalID  string // response message, or the component message for (Deferred)UpdateMessage
	loading     bool   // deferred message not edited yet
	edited      bool
	modal       *discord.Modal
	followupIDs []string

	done    chan struct{} // closed on acknowledgement or failure
	changed chan struct{} // closed and replaced on every state change
	timer   *time.Timer
}

type openModal struct {
	modal     discord.Modal
	serverID  string
	channelID string
	messageID string // component message the modal was opened from, if any
}

func modalKey(userID, appID string) string { return userID + ":" + appID }

// InteractionState is a snapshot of an interaction's lifecycle.
type InteractionState struct {
	Interaction discord.Interaction
	// Acknowledged is true once the bot called RespondInteraction successfully.
	Acknowledged bool
	// ResponseType is the initial response type (0 until acknowledged).
	ResponseType discord.InteractionResponseType
	// Deferred is true for ResponseDeferredMessage / ResponseDeferredUpdate.
	Deferred bool
	// Ephemeral reports whether the original response is ephemeral.
	Ephemeral bool
	// Message is the original response message in its current state (after edits).
	// For UpdateMessage / DeferredUpdate it is the component's message. Nil for modals.
	Message *discord.Message
	// Edited is true once EditOriginalResponse succeeded.
	Edited bool
	// Modal is the modal shown by a ResponseModal.
	Modal *discord.Modal
	// Followups are the followup messages, in order. After a deferred message
	// response, the first followup replaces the "thinking" message (like Discord), so
	// Followups[0] is then the original message.
	Followups []discord.Message
	// Failed is true when the interaction was not acknowledged within the deadline
	// ("This interaction failed" in the Discord client).
	Failed bool
}

// InteractionHandle tracks an interaction started by InvokeCommand, ClickButton,
// Select or SubmitModal.
type InteractionHandle struct {
	w   *World
	st  *interactionState
	err error
}

var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// Err reports why the interaction could not be started (unknown command, missing
// option, disabled button …). When it is non-nil, nothing was sent to the bot.
func (h *InteractionHandle) Err() error { return h.err }

// ID returns the interaction ID ("" if Err() != nil).
func (h *InteractionHandle) ID() string {
	if h.st == nil {
		return ""
	}
	return h.st.ix.ID
}

// Interaction returns the interaction as delivered to the bot.
func (h *InteractionHandle) Interaction() discord.Interaction {
	if h.st == nil {
		return discord.Interaction{}
	}
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	return cloneInteraction(h.st.ix)
}

// Done is closed once the interaction is acknowledged or has failed.
func (h *InteractionHandle) Done() <-chan struct{} {
	if h.st == nil {
		return closedChan
	}
	return h.st.done
}

// State returns the current snapshot.
func (h *InteractionHandle) State() InteractionState {
	if h.st == nil {
		return InteractionState{}
	}
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	return h.w.snapshot(h.st)
}

// Wait blocks until the interaction is acknowledged or fails (at most the deadline)
// and returns the state at that point.
func (h *InteractionHandle) Wait() InteractionState {
	<-h.Done()
	return h.State()
}

// WaitUntil blocks until cond holds for the state or timeout elapses. It returns the
// last state and whether cond held. Use it for what happens after the initial
// response, e.g. WaitUntil(time.Second, func(s InteractionState) bool { return s.Edited }).
func (h *InteractionHandle) WaitUntil(timeout time.Duration, cond func(InteractionState) bool) (InteractionState, bool) {
	if h.st == nil {
		return InteractionState{}, false
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		h.w.mu.Lock()
		s := h.w.snapshot(h.st)
		ch := h.st.changed
		h.w.mu.Unlock()
		if cond(s) {
			return s, true
		}
		select {
		case <-ch:
		case <-deadline.C:
			return h.State(), false
		}
	}
}

// Failed reports whether the interaction failed (no acknowledgement in time).
func (h *InteractionHandle) Failed() bool { return h.State().Failed }

// Message returns the original response message in its current state, if any.
func (h *InteractionHandle) Message() *discord.Message { return h.State().Message }

// Followups returns the followup messages.
func (h *InteractionHandle) Followups() []discord.Message { return h.State().Followups }

// Modal returns the modal shown in response, if any.
func (h *InteractionHandle) Modal() *discord.Modal { return h.State().Modal }

// SubmitModal submits the modal this interaction responded with, as the same user.
func (h *InteractionHandle) SubmitModal(fields map[string]string) *InteractionHandle {
	if h.st == nil {
		return &InteractionHandle{w: h.w, err: h.err}
	}
	h.w.mu.Lock()
	modal := cloneModal(h.st.modal)
	ix := h.st.ix
	h.w.mu.Unlock()
	if modal == nil {
		return &InteractionHandle{w: h.w, err: badRequest("interaction did not respond with a modal")}
	}
	return h.w.SubmitModal(ix.User.ID, ix.ServerID, ix.ChannelID, ix.ApplicationID, modal.CustomID, fields)
}

func (w *World) snapshot(st *interactionState) InteractionState {
	s := InteractionState{
		Interaction:  cloneInteraction(st.ix),
		Acknowledged: st.acked,
		ResponseType: st.respType,
		Deferred:     st.respType == discord.ResponseDeferredMessage || st.respType == discord.ResponseDeferredUpdate,
		Ephemeral:    st.ephemeral,
		Edited:       st.edited,
		Modal:        cloneModal(st.modal),
		Failed:       st.failed,
	}
	if ms := w.messages[st.originalID]; ms != nil {
		m := cloneMessage(ms.msg)
		s.Message = &m
	}
	for _, id := range st.followupIDs {
		if ms := w.messages[id]; ms != nil {
			s.Followups = append(s.Followups, cloneMessage(ms.msg))
		}
	}
	return s
}

func (w *World) notify(st *interactionState) {
	w.changed()
	close(st.changed)
	st.changed = make(chan struct{})
}

// startInteraction registers and dispatches an interaction. Caller holds w.mu.
func (w *World) startInteraction(ix discord.Interaction, bot *botState) *interactionState {
	ix.ID = w.newID()
	ix.Token = randomToken("ix.")
	ix.ApplicationID = bot.appID
	st := &interactionState{
		ix: ix, bot: bot, created: time.Now(),
		done: make(chan struct{}), changed: make(chan struct{}),
	}
	w.interactions[ix.ID] = st
	w.ixByToken[ix.Token] = st
	w.changed()
	st.timer = time.AfterFunc(w.deadline, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.expire(st)
	})
	w.emit(discord.InteractionCreateEvent{Interaction: ix})
	return st
}

// expire marks an unacknowledged interaction as failed. Caller holds w.mu.
func (w *World) expire(st *interactionState) {
	if st.acked || st.failed {
		return
	}
	st.failed = true
	close(st.done)
	w.notify(st)
}

// ───────────────────────────────────────────────────────────── bot side

func (w *World) respondInteraction(a actor, interactionID, token string, r discord.InteractionResponse) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) error { return w.record(a, "RespondInteraction", "", interactionID, "", err) }
	st := w.interactions[interactionID]
	if st == nil || st.ix.Token != token {
		return fail(notFound("unknown interaction"))
	}
	if !st.acked && time.Since(st.created) >= w.deadline {
		w.expire(st)
	}
	if st.failed {
		return fail(notFound("unknown interaction (not acknowledged within %s)", w.deadline))
	}
	if st.acked {
		return fail(badRequest("interaction has already been acknowledged"))
	}
	ix := st.ix
	componentLike := ix.Type == discord.InteractionComponent || (ix.Type == discord.InteractionModalSubmit && ix.Message != nil)
	c := w.channels[ix.ChannelID]
	if c == nil && r.Type != discord.ResponseModal {
		return fail(notFound("unknown channel %s", ix.ChannelID))
	}
	botUser := w.users[st.bot.userID].user
	ephemeralFor := func(flags discord.MessageFlags) string {
		if flags&discord.MessageFlagEphemeral != 0 {
			return ix.User.ID
		}
		return ""
	}
	switch r.Type {
	case discord.ResponseMessage:
		if r.Data == nil {
			return fail(badRequest("message response without data"))
		}
		if err := validateMessage(*r.Data); err != nil {
			return fail(err)
		}
		ms := w.newMessage(c, botUser, *r.Data, ephemeralFor(r.Data.Flags))
		st.originalID, st.ephemeral = ms.msg.ID, ms.ephemeral
	case discord.ResponseDeferredMessage:
		var flags discord.MessageFlags
		if r.Data != nil {
			flags = r.Data.Flags & discord.MessageFlagEphemeral
		}
		ms := w.newMessage(c, botUser, discord.MessageCreate{Flags: flags | msgFlagLoading}, ephemeralFor(flags))
		st.originalID, st.ephemeral, st.loading = ms.msg.ID, ms.ephemeral, true
	case discord.ResponseDeferredUpdate, discord.ResponseUpdateMessage:
		if !componentLike {
			return fail(badRequest("update responses are only valid for component interactions"))
		}
		ms := w.messages[ix.Message.ID]
		if ms == nil {
			return fail(notFound("unknown message %s", ix.Message.ID))
		}
		if r.Type == discord.ResponseUpdateMessage {
			if r.Data == nil {
				return fail(badRequest("update response without data"))
			}
			if err := validateMessage(*r.Data); err != nil {
				return fail(err)
			}
			w.editMessage(ms, *r.Data)
		}
		st.originalID, st.ephemeral = ms.msg.ID, ms.ephemeral
	case discord.ResponseModal:
		if ix.Type == discord.InteractionModalSubmit {
			return fail(badRequest("cannot respond to a modal submit with a modal"))
		}
		if err := validateModal(r.Modal); err != nil {
			return fail(err)
		}
		st.modal = cloneModal(r.Modal)
		om := &openModal{modal: *cloneModal(r.Modal), serverID: ix.ServerID, channelID: ix.ChannelID}
		if ix.Message != nil {
			om.messageID = ix.Message.ID
		}
		w.openModals[modalKey(ix.User.ID, st.bot.appID)] = om
	default:
		return fail(badRequest("unsupported interaction response type %d", r.Type))
	}
	st.acked, st.respType = true, r.Type
	st.timer.Stop()
	close(st.done)
	w.notify(st)
	return fail(nil)
}

func (w *World) interactionByToken(a actor, applicationID, token string) (*interactionState, error) {
	st := w.ixByToken[token]
	if st == nil || st.bot.appID != applicationID || a.appID != applicationID {
		return nil, notFound("unknown webhook")
	}
	return st, nil
}

func (w *World) editOriginalResponse(a actor, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Message, error) {
		return discord.Message{}, w.record(a, "EditOriginalResponse", "", "", "", err)
	}
	st, err := w.interactionByToken(a, applicationID, token)
	if err != nil {
		return fail(err)
	}
	if !st.acked || st.originalID == "" {
		return fail(notFound("unknown message: the interaction has no original response"))
	}
	ms := w.messages[st.originalID]
	if ms == nil {
		return fail(notFound("unknown message %s", st.originalID))
	}
	if err := validateMessage(m); err != nil {
		return fail(err)
	}
	w.editMessage(ms, m)
	st.loading, st.edited = false, true
	w.notify(st)
	w.recordOK(a, "EditOriginalResponse", ms.msg.ServerID, ms.msg.ID, "")
	return cloneMessage(ms.msg), nil
}

func (w *World) createFollowup(a actor, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Message, error) {
		return discord.Message{}, w.record(a, "CreateFollowup", "", "", "", err)
	}
	st, err := w.interactionByToken(a, applicationID, token)
	if err != nil {
		return fail(err)
	}
	if !st.acked {
		return fail(notFound("unknown webhook: interaction not acknowledged"))
	}
	if st.respType == discord.ResponseModal {
		return fail(notFound("unknown webhook: no message to follow up after a modal"))
	}
	if err := validateMessage(m); err != nil {
		return fail(err)
	}
	var ms *messageState
	if st.loading {
		// The first followup after a deferred message replaces the "thinking" message.
		ms = w.messages[st.originalID]
		if ms != nil {
			w.editMessage(ms, m)
			st.loading = false
		}
	}
	if ms == nil {
		c := w.channels[st.ix.ChannelID]
		if c == nil {
			return fail(notFound("unknown channel %s", st.ix.ChannelID))
		}
		ephemeralFor := ""
		if m.Flags&discord.MessageFlagEphemeral != 0 {
			ephemeralFor = st.ix.User.ID
		}
		ms = w.newMessage(c, w.users[st.bot.userID].user, m, ephemeralFor)
	}
	st.followupIDs = append(st.followupIDs, ms.msg.ID)
	w.notify(st)
	w.recordOK(a, "CreateFollowup", ms.msg.ServerID, ms.msg.ID, "")
	return cloneMessage(ms.msg), nil
}

// ───────────────────────────────────────────────────────────── user side

func failedHandle(w *World, err error) *InteractionHandle {
	return &InteractionHandle{w: w, err: err}
}

// interactionContext validates the user can interact in the channel and returns the
// user, their member (nil in DMs) and the channel. Caller holds w.mu.
func (w *World) interactionContext(userID, serverID, channelID string) (discord.User, *discord.Member, *channelState, error) {
	u := w.users[userID]
	if u == nil {
		return discord.User{}, nil, nil, notFound("unknown user %s", userID)
	}
	c := w.channels[channelID]
	if c == nil {
		return discord.User{}, nil, nil, notFound("unknown channel %s", channelID)
	}
	if c.ch.ServerID != serverID {
		return discord.User{}, nil, nil, badRequest("channel %s is not in server %q", channelID, serverID)
	}
	if c.ch.Type == discord.ChannelDM {
		if c.dmUsers[0] != userID {
			return discord.User{}, nil, nil, forbidden("user %s is not in DM %s", userID, channelID)
		}
		return u.user, nil, c, nil
	}
	s := w.servers[serverID]
	m := s.members[userID]
	if m == nil {
		return discord.User{}, nil, nil, forbidden("user %s is not a member of server %s", userID, serverID)
	}
	if !w.channelPerms(c, userID).Has(permView) {
		return discord.User{}, nil, nil, forbidden("user %s cannot view channel %s", userID, channelID)
	}
	return u.user, cloneMemberPtr(m), c, nil
}

// InvokeCommand runs a slash command as userID, like typing it in the Discord client.
// options maps option names to values: string, bool, any Go integer or float, or IDs
// (string) / discord.User / discord.Role / discord.Channel / discord.Attachment for the
// corresponding option types. The command must be registered for the application in
// the server, the user must be allowed to use it (DefaultMemberPermissions), and
// options are validated against its definition.
func (w *World) InvokeCommand(userID, serverID, channelID, applicationID, name string, options map[string]any) *InteractionHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	bot := w.botsByApp[applicationID]
	if bot == nil {
		return failedHandle(w, notFound("unknown application %s", applicationID))
	}
	if serverID == "" {
		return failedHandle(w, badRequest("only server commands are simulated"))
	}
	s, err := w.server(serverID)
	if err != nil {
		return failedHandle(w, err)
	}
	user, member, c, err := w.interactionContext(userID, serverID, channelID)
	if err != nil {
		return failedHandle(w, err)
	}
	if !w.channelPerms(c, userID).Has(permUseAppCommands) {
		return failedHandle(w, forbidden("user %s lacks UseApplicationCommands in channel %s", userID, channelID))
	}
	var cmd *discord.Command
	for i := range w.commands[applicationID][serverID] {
		if w.commands[applicationID][serverID][i].Name == name {
			cmd = &w.commands[applicationID][serverID][i]
		}
	}
	if cmd == nil {
		return failedHandle(w, notFound("unknown command %q", name))
	}
	if dmp := cmd.DefaultMemberPermissions; dmp != nil {
		have := w.basePerms(s, userID)
		if (*dmp == 0 && have != allPermissions) || !have.Has(*dmp) {
			return failedHandle(w, forbidden("user %s may not use command %q", userID, name))
		}
	}
	ix := discord.Interaction{
		Type: discord.InteractionCommand, ServerID: serverID, ChannelID: channelID,
		User: user, Member: member, CommandName: name,
	}
	if err := w.buildOptions(s, cmd, options, &ix); err != nil {
		return failedHandle(w, err)
	}
	return &InteractionHandle{w: w, st: w.startInteraction(ix, bot)}
}

func (w *World) buildOptions(s *serverState, cmd *discord.Command, options map[string]any, ix *discord.Interaction) error {
	known := map[string]bool{}
	for _, def := range cmd.Options {
		known[def.Name] = true
	}
	for k := range options {
		if !known[k] {
			return badRequest("command %q has no option %q", cmd.Name, k)
		}
	}
	for _, def := range cmd.Options {
		v, ok := options[def.Name]
		if !ok || v == nil {
			if def.Required {
				return badRequest("missing required option %q", def.Name)
			}
			continue
		}
		val, err := w.convertOption(s, def, v, &ix.Resolved)
		if err != nil {
			return badRequest("option %q: %v", def.Name, err)
		}
		ix.Options = append(ix.Options, discord.InteractionOption{Name: def.Name, Type: def.Type, Value: val})
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func choiceMatches(def discord.CommandOption, v any) bool {
	if len(def.Choices) == 0 {
		return true
	}
	for _, c := range def.Choices {
		if s, ok := v.(string); ok {
			if cs, ok := c.Value.(string); ok && cs == s {
				return true
			}
			continue
		}
		f, _ := v.(float64)
		if cf, ok := toFloat(c.Value); ok && cf == f {
			return true
		}
	}
	return false
}

func idOf(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, x != ""
	case discord.User:
		return x.ID, x.ID != ""
	case discord.Member:
		return x.User.ID, x.User.ID != ""
	case discord.Role:
		return x.ID, x.ID != ""
	case discord.Channel:
		return x.ID, x.ID != ""
	}
	return "", false
}

func (w *World) resolveUser(s *serverState, id string, r *discord.Resolved) bool {
	u := w.users[id]
	if u == nil {
		return false
	}
	if r.Users == nil {
		r.Users = map[string]discord.User{}
	}
	r.Users[id] = u.user
	if m := s.members[id]; m != nil {
		if r.Members == nil {
			r.Members = map[string]discord.Member{}
		}
		r.Members[id] = cloneMember(*m)
	}
	return true
}

func (w *World) resolveRole(s *serverState, id string, r *discord.Resolved) bool {
	role := s.roles[id]
	if role == nil {
		return false
	}
	if r.Roles == nil {
		r.Roles = map[string]discord.Role{}
	}
	r.Roles[id] = *role
	return true
}

func (w *World) convertOption(s *serverState, def discord.CommandOption, v any, r *discord.Resolved) (any, error) {
	switch def.Type {
	case discord.OptionString:
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want a string, got %T", v)
		}
		if utf8.RuneCountInString(str) > 6000 {
			return nil, fmt.Errorf("value too long")
		}
		if !choiceMatches(def, str) {
			return nil, fmt.Errorf("%q is not one of the choices", str)
		}
		return str, nil
	case discord.OptionInteger, discord.OptionNumber:
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("want a number, got %T", v)
		}
		if def.Type == discord.OptionInteger && (f != math.Trunc(f) || math.Abs(f) > 1<<53) {
			return nil, fmt.Errorf("want an integer, got %v", f)
		}
		if (def.MinValue != nil && f < *def.MinValue) || (def.MaxValue != nil && f > *def.MaxValue) {
			return nil, fmt.Errorf("%v out of range", f)
		}
		if !choiceMatches(def, f) {
			return nil, fmt.Errorf("%v is not one of the choices", f)
		}
		return f, nil
	case discord.OptionBoolean:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("want a bool, got %T", v)
		}
		return b, nil
	case discord.OptionUser:
		id, ok := idOf(v)
		if !ok || !w.resolveUser(s, id, r) {
			return nil, fmt.Errorf("unknown user %v", v)
		}
		return id, nil
	case discord.OptionRole:
		id, ok := idOf(v)
		if !ok || !w.resolveRole(s, id, r) {
			return nil, fmt.Errorf("unknown role %v", v)
		}
		return id, nil
	case discord.OptionMentionable:
		id, ok := idOf(v)
		if !ok || (!w.resolveUser(s, id, r) && !w.resolveRole(s, id, r)) {
			return nil, fmt.Errorf("unknown user or role %v", v)
		}
		return id, nil
	case discord.OptionChannel:
		id, ok := idOf(v)
		c := w.channels[id]
		if !ok || c == nil || c.ch.ServerID != s.info.ID {
			return nil, fmt.Errorf("unknown channel %v", v)
		}
		if r.Channels == nil {
			r.Channels = map[string]discord.Channel{}
		}
		r.Channels[id] = cloneChannel(c.ch)
		return id, nil
	case discord.OptionAttachment:
		var att discord.Attachment
		switch x := v.(type) {
		case discord.Attachment:
			att = x
		case string:
			att = discord.Attachment{Filename: x}
		default:
			return nil, fmt.Errorf("want a discord.Attachment or a filename, got %T", v)
		}
		if att.ID == "" {
			att.ID = w.newID()
		}
		if att.URL == "" {
			att.URL = "https://cdn.discordapp.com/attachments/" + att.ID + "/" + att.Filename
		}
		if r.Attachments == nil {
			r.Attachments = map[string]discord.Attachment{}
		}
		r.Attachments[att.ID] = att
		return att.ID, nil
	}
	return nil, fmt.Errorf("unsupported option type %d", def.Type)
}

func findComponent(rows []discord.Component, customID string) *discord.Component {
	for i := range rows {
		if rows[i].CustomID == customID && rows[i].Type != discord.ComponentActionRow {
			return &rows[i]
		}
		if c := findComponent(rows[i].Components, customID); c != nil {
			return c
		}
	}
	return nil
}

// ClickButton clicks a button of msg as userID. The message is looked up by ID, so its
// current state counts (after UpdateMessage, for example), and the interaction goes to
// the application that sent the message.
func (w *World) ClickButton(userID string, msg discord.Message, customID string) *InteractionHandle {
	return w.componentInteraction(userID, msg, customID, discord.ComponentButton, nil)
}

// Select picks values in a string select menu of msg as userID.
func (w *World) Select(userID string, msg discord.Message, customID string, values []string) *InteractionHandle {
	return w.componentInteraction(userID, msg, customID, discord.ComponentStringSelect, values)
}

func (w *World) componentInteraction(userID string, msg discord.Message, customID string, typ discord.ComponentType, values []string) *InteractionHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	ms := w.messages[msg.ID]
	if ms == nil {
		return failedHandle(w, notFound("unknown message %s", msg.ID))
	}
	if ms.ephemeral && ms.ephemeralFor != userID {
		return failedHandle(w, forbidden("ephemeral message %s is not visible to user %s", msg.ID, userID))
	}
	bot := w.botsByUser[ms.msg.Author.ID]
	if bot == nil {
		return failedHandle(w, badRequest("message %s was not sent by an application", msg.ID))
	}
	user, member, _, err := w.interactionContext(userID, ms.msg.ServerID, ms.msg.ChannelID)
	if err != nil {
		return failedHandle(w, err)
	}
	comp := findComponent(ms.msg.Components, customID)
	if comp == nil || comp.Type != typ {
		return failedHandle(w, notFound("message %s has no %s %q", msg.ID, componentName(typ), customID))
	}
	if comp.Disabled {
		return failedHandle(w, badRequest("%s %q is disabled", componentName(typ), customID))
	}
	if typ == discord.ComponentStringSelect {
		minV, maxV := selectBounds(*comp)
		if len(values) < minV || len(values) > maxV {
			return failedHandle(w, badRequest("select %q needs %d-%d values, got %d", customID, minV, maxV, len(values)))
		}
		allowed := map[string]bool{}
		for _, o := range comp.Options {
			allowed[o.Value] = true
		}
		seen := map[string]bool{}
		for _, v := range values {
			if !allowed[v] || seen[v] {
				return failedHandle(w, badRequest("invalid select value %q", v))
			}
			seen[v] = true
		}
	}
	m := cloneMessage(ms.msg)
	ix := discord.Interaction{
		Type: discord.InteractionComponent, ServerID: ms.msg.ServerID, ChannelID: ms.msg.ChannelID,
		User: user, Member: member, CustomID: customID, ComponentType: typ, Message: &m,
	}
	ix.Values = append([]string{}, values...) // empty, not nil, for buttons
	return &InteractionHandle{w: w, st: w.startInteraction(ix, bot)}
}

func componentName(t discord.ComponentType) string {
	if t == discord.ComponentStringSelect {
		return "select menu"
	}
	return "button"
}

// SubmitModal submits a modal as userID. The application must have shown that modal
// (custom ID) to the user in response to an interaction in that server/channel, and
// fields (text input custom ID → value) must satisfy its text inputs (required,
// min/max length). Use InteractionHandle.SubmitModal for the common case.
func (w *World) SubmitModal(userID, serverID, channelID, applicationID, customID string, fields map[string]string) *InteractionHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	bot := w.botsByApp[applicationID]
	if bot == nil {
		return failedHandle(w, notFound("unknown application %s", applicationID))
	}
	key := modalKey(userID, applicationID)
	om := w.openModals[key]
	if om == nil || om.modal.CustomID != customID {
		return failedHandle(w, badRequest("no modal %q open for user %s", customID, userID))
	}
	if om.serverID != serverID || om.channelID != channelID {
		return failedHandle(w, badRequest("modal %q was opened in another channel", customID))
	}
	user, member, _, err := w.interactionContext(userID, serverID, channelID)
	if err != nil {
		return failedHandle(w, err)
	}
	inputs := map[string]discord.Component{}
	for _, row := range om.modal.Components {
		for _, ti := range row.Components {
			inputs[ti.CustomID] = ti
		}
	}
	for k := range fields {
		if _, ok := inputs[k]; !ok {
			return failedHandle(w, badRequest("modal %q has no text input %q", customID, k))
		}
	}
	values := map[string]string{}
	for id, ti := range inputs {
		v := fields[id]
		n := utf8.RuneCountInString(v)
		if ti.Required && v == "" {
			return failedHandle(w, badRequest("text input %q is required", id))
		}
		if v != "" && ((ti.MinLength != nil && n < *ti.MinLength) || (ti.MaxLength != nil && n > *ti.MaxLength) || n > 4000) {
			return failedHandle(w, badRequest("text input %q has an invalid length", id))
		}
		values[id] = v
	}
	delete(w.openModals, key)
	ix := discord.Interaction{
		Type: discord.InteractionModalSubmit, ServerID: serverID, ChannelID: channelID,
		User: user, Member: member, CustomID: customID, ModalFields: values,
	}
	if ms := w.messages[om.messageID]; om.messageID != "" && ms != nil {
		m := cloneMessage(ms.msg)
		ix.Message = &m
	}
	return &InteractionHandle{w: w, st: w.startInteraction(ix, bot)}
}
