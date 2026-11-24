package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/sim"
)

// ───────────────────────────────────────────────────────────── DTOs

type embedFieldDTO struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type embedDTO struct {
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	URL          string          `json:"url,omitempty"`
	Color        string          `json:"color,omitempty"`
	Timestamp    *time.Time      `json:"timestamp,omitempty"`
	Fields       []embedFieldDTO `json:"fields,omitempty"`
	Author       map[string]any  `json:"author,omitempty"`
	Footer       map[string]any  `json:"footer,omitempty"`
	ImageURL     string          `json:"imageUrl,omitempty"`
	ThumbnailURL string          `json:"thumbnailUrl,omitempty"`
}

type selectOptionDTO struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Emoji       string `json:"emoji,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

type componentDTO struct {
	Type        string            `json:"type"` // row | button | select | textInput
	Components  []componentDTO    `json:"components,omitempty"`
	CustomID    string            `json:"customId,omitempty"`
	Label       string            `json:"label,omitempty"`
	Style       string            `json:"style,omitempty"`
	Emoji       string            `json:"emoji,omitempty"`
	URL         string            `json:"url,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	Placeholder string            `json:"placeholder,omitempty"`
	MinValues   *int              `json:"minValues,omitempty"`
	MaxValues   *int              `json:"maxValues,omitempty"`
	Options     []selectOptionDTO `json:"options,omitempty"`
	Required    bool              `json:"required,omitempty"`
	Value       string            `json:"value,omitempty"`
}

type messageDTO struct {
	ID         string         `json:"id"`
	ChannelID  string         `json:"channelId"`
	ServerID   string         `json:"serverId,omitempty"`
	Author     userDTO        `json:"author"`
	Content    string         `json:"content"`
	Embeds     []embedDTO     `json:"embeds"`
	Components []componentDTO `json:"components"`
	Ephemeral  bool           `json:"ephemeral"`
	CreatedAt  time.Time      `json:"createdAt"`
	EditedAt   *time.Time     `json:"editedAt,omitempty"`
}

var buttonStyles = map[discord.ButtonStyle]string{
	discord.ButtonPrimary: "primary", discord.ButtonSecondary: "secondary", discord.ButtonSuccess: "success",
	discord.ButtonDanger: "danger", discord.ButtonLink: "link",
}

func toComponents(cs []discord.Component) []componentDTO {
	out := make([]componentDTO, 0, len(cs))
	for _, c := range cs {
		d := componentDTO{CustomID: c.CustomID, Label: c.Label, Disabled: c.Disabled}
		switch c.Type {
		case discord.ComponentActionRow:
			d.Type, d.Components = "row", toComponents(c.Components)
		case discord.ComponentButton:
			d.Type, d.Style, d.Emoji, d.URL = "button", buttonStyles[c.Style], c.Emoji, c.URL
		case discord.ComponentStringSelect:
			d.Type, d.Placeholder, d.MinValues, d.MaxValues = "select", c.Placeholder, c.MinValues, c.MaxValues
			for _, o := range c.Options {
				d.Options = append(d.Options, selectOptionDTO{Label: o.Label, Value: o.Value, Description: o.Description, Emoji: o.Emoji, Default: o.Default})
			}
		case discord.ComponentTextInput:
			d.Type, d.Required, d.Value, d.Placeholder = "textInput", c.Required, c.Value, c.Placeholder
			if c.TextStyle == 2 {
				d.Style = "paragraph"
			} else {
				d.Style = "short"
			}
		default:
			d.Type = fmt.Sprintf("unknown(%d)", c.Type)
		}
		out = append(out, d)
	}
	return out
}

func toMessage(m discord.Message) messageDTO {
	out := messageDTO{ID: m.ID, ChannelID: m.ChannelID, ServerID: m.ServerID, Author: toUser(m.Author), Content: m.Content,
		Embeds: []embedDTO{}, Components: toComponents(m.Components), Ephemeral: m.Flags&discord.MessageFlagEphemeral != 0,
		CreatedAt: m.CreatedAt, EditedAt: m.EditedAt}
	for _, e := range m.Embeds {
		d := embedDTO{Title: e.Title, Description: e.Description, URL: e.URL, Color: colorHex(e.Color), Timestamp: e.Timestamp,
			ImageURL: e.ImageURL, ThumbnailURL: e.ThumbURL}
		for _, f := range e.Fields {
			d.Fields = append(d.Fields, embedFieldDTO(f))
		}
		if e.Author != nil {
			d.Author = map[string]any{"name": e.Author.Name, "url": e.Author.URL, "iconUrl": e.Author.IconURL}
		}
		if e.Footer != nil {
			d.Footer = map[string]any{"text": e.Footer.Text, "iconUrl": e.Footer.IconURL}
		}
		out.Embeds = append(out.Embeds, d)
	}
	return out
}

func toMessages(ms []discord.Message) []messageDTO {
	out := make([]messageDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, toMessage(m))
	}
	return out
}

// ───────────────────────────────────────────────────────────── handlers

func (s *server) simOnly(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Sim == nil {
			writeError(w, http.StatusNotFound, "not_found", "the simulator is not enabled (cicord dev --sim)")
			return
		}
		fn(w, r)
	}
}

func simErr(err error) error {
	switch {
	case errors.Is(err, discord.ErrNotFound):
		return httpErr(http.StatusNotFound, "not_found", "%v", err)
	case errors.Is(err, discord.ErrForbidden):
		return httpErr(http.StatusForbidden, "forbidden", "%v", err)
	case errors.Is(err, discord.ErrBadRequest):
		return httpErr(http.StatusBadRequest, "invalid", "%v", err)
	}
	return err
}

// simServerIDs are the Discord IDs of the repository's servers present in the world.
func (s *server) simServerIDs() []string {
	var out []string
	if s.rt == nil {
		return out
	}
	repo := s.rt.Repo()
	if repo == nil {
		return out
	}
	for _, k := range s.rt.ServerKeys() {
		if srv := repo.Servers[k]; srv != nil {
			if _, ok := s.opts.Sim.Server(srv.Spec.DiscordID); ok {
				out = append(out, srv.Spec.DiscordID)
			}
		}
	}
	return out
}

func (s *server) handleSimState(w http.ResponseWriter, r *http.Request) {
	wd := s.opts.Sim
	users := []userDTO{}
	for _, u := range wd.Users() {
		users = append(users, toUser(u))
	}
	bots := []userDTO{}
	for _, u := range wd.Bots() {
		bots = append(bots, toUser(u))
	}
	type memberOut struct {
		User  userDTO  `json:"user"`
		Nick  string   `json:"nick"`
		Roles []string `json:"roles"`
	}
	type roleOut struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Color    string `json:"color"`
		Position int    `json:"position"`
		Managed  bool   `json:"managed"`
	}
	type channelOut struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		ParentID string `json:"parentId,omitempty"`
		Position int    `json:"position"`
	}
	type serverOut struct {
		ID       string       `json:"id"`
		Key      string       `json:"key,omitempty"`
		Name     string       `json:"name"`
		OwnerID  string       `json:"ownerId"`
		Channels []channelOut `json:"channels"`
		Roles    []roleOut    `json:"roles"`
		Members  []memberOut  `json:"members"`
	}
	servers := []serverOut{}
	for _, sid := range s.simServerIDs() {
		info, _ := wd.Server(sid)
		so := serverOut{ID: sid, Name: info.Name, OwnerID: info.OwnerID, Channels: []channelOut{}, Roles: []roleOut{}, Members: []memberOut{}}
		so.Key, _ = s.rt.ServerKeyByDiscordID(sid)
		for _, c := range wd.Channels(sid) {
			so.Channels = append(so.Channels, channelOut{ID: c.ID, Name: c.Name, Type: c.Type.String(), ParentID: c.ParentID, Position: c.Position})
		}
		for _, ro := range wd.Roles(sid) {
			so.Roles = append(so.Roles, roleOut{ID: ro.ID, Name: ro.Name, Color: colorHex(ro.Color), Position: ro.Position, Managed: ro.Managed})
		}
		for _, m := range wd.Members(sid) {
			roles := m.RoleIDs
			if roles == nil {
				roles = []string{}
			}
			so.Members = append(so.Members, memberOut{User: toUser(m.User), Nick: m.Nick, Roles: roles})
		}
		servers = append(servers, so)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "bots": bots, "servers": servers})
}

func (s *server) handleSimCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Username == "" || len(body.Username) > 32 {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "username must be 1-32 characters"))
		return
	}
	writeJSON(w, http.StatusCreated, toUser(s.opts.Sim.CreateUser(body.Username)))
}

func (s *server) handleSimJoin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"userId"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.opts.Sim.AddMember(r.PathValue("id"), body.UserID); err != nil {
		s.fail(w, simErr(err))
		return
	}
	s.forgetMember(r.PathValue("id"), body.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// handleSimBoosts sets the number of boosts of a simulated server (boost level and the
// feature flags it unlocks follow, like on Discord).
func (s *server) handleSimBoosts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Count *int `json:"count"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Count == nil {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "count is required"))
		return
	}
	id := r.PathValue("id")
	if err := s.opts.Sim.SetBoosts(id, *body.Count); err != nil {
		s.fail(w, simErr(err))
		return
	}
	srv, _ := s.opts.Sim.Server(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": srv.ID, "boostLevel": srv.PremiumTier, "boostCount": srv.PremiumSubscriptionCount, "features": srv.Features,
	})
}

func (s *server) handleSimLeave(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Sim.RemoveMember(r.PathValue("id"), r.PathValue("userId")); err != nil {
		s.fail(w, simErr(err))
		return
	}
	s.forgetMember(r.PathValue("id"), r.PathValue("userId"))
	w.WriteHeader(http.StatusNoContent)
}

// forgetMember drops the cached member lookup after a simulated join/leave.
func (s *server) forgetMember(serverID, userID string) {
	if key, ok := s.rt.ServerKeyByDiscordID(serverID); ok {
		s.members.mu.Lock()
		delete(s.members.entries, key+"/"+userID)
		s.members.mu.Unlock()
	}
}

func lastN(ms []discord.Message, n int) []discord.Message {
	if len(ms) > n {
		return ms[len(ms)-n:]
	}
	return ms
}

func (s *server) handleSimMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.opts.Sim.Channel(id); !ok {
		notFoundHandler(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": toMessages(lastN(s.opts.Sim.Messages(id), queryLimit(r)))})
}

func (s *server) handleSimDMs(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("id")
	if _, ok := s.opts.Sim.User(uid); !ok {
		notFoundHandler(w, r)
		return
	}
	var all []discord.Message
	for _, b := range s.opts.Sim.Bots() {
		all = append(all, s.opts.Sim.DMs(uid, b.ID)...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt.Before(all[j].CreatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"items": toMessages(lastN(all, queryLimit(r)))})
}

func (s *server) handleSimSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID    string `json:"userId"`
		ChannelID string `json:"channelId"`
		Content   string `json:"content"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.opts.Sim.SendMessage(body.UserID, body.ChannelID, body.Content)
	if err != nil {
		s.fail(w, simErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, toMessage(m))
}

// appID resolves a bot reference: bot key (repository), application ID or bot user ID.
func (s *server) appID(ref string) (string, bool) {
	if s.rt != nil {
		if repo := s.rt.Repo(); repo != nil {
			if b := repo.Bots[ref]; b != nil {
				return b.Spec.ApplicationID, true
			}
		}
	}
	if id, ok := s.opts.Sim.ApplicationID(ref); ok {
		return id, true
	}
	for _, b := range s.opts.Sim.Bots() {
		if id, _ := s.opts.Sim.ApplicationID(b.ID); id == ref {
			return ref, true
		}
	}
	return "", false
}

func (s *server) botKeyOf(appID string) string {
	if s.rt != nil {
		if repo := s.rt.Repo(); repo != nil {
			for k, b := range repo.Bots {
				if b.Spec.ApplicationID == appID {
					return k
				}
			}
		}
	}
	return appID
}

type modalDTO struct {
	CustomID   string         `json:"customId"`
	Title      string         `json:"title"`
	Components []componentDTO `json:"components"`
}

type interactionResultDTO struct {
	Status    string       `json:"status"` // replied | deferred | failed | modal
	Response  *messageDTO  `json:"response"`
	Followups []messageDTO `json:"followups"`
	Modal     *modalDTO    `json:"modal,omitempty"`
	// Choices are the autocomplete results (POST /sim/commands with "autocomplete").
	Choices []choiceDTO `json:"choices,omitempty"`
	Error   string      `json:"error"`
}

type choiceDTO struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

const interactionWait = 5 * time.Second

// interactionResult waits (up to 5 s) for the bot's answer.
func interactionResult(h *sim.InteractionHandle) interactionResultDTO {
	out := interactionResultDTO{Followups: []messageDTO{}}
	if err := h.Err(); err != nil {
		out.Status, out.Error = "failed", err.Error()
		return out
	}
	st, _ := h.WaitUntil(interactionWait, func(st sim.InteractionState) bool {
		if st.Failed || st.Modal != nil {
			return true
		}
		if !st.Acknowledged {
			return false
		}
		return !st.Deferred || st.Edited || len(st.Followups) > 0
	})
	switch {
	case st.Failed:
		out.Status, out.Error = "failed", "the application did not respond"
	case st.Modal != nil:
		out.Status = "modal"
		out.Modal = &modalDTO{CustomID: st.Modal.CustomID, Title: st.Modal.Title, Components: toComponents(st.Modal.Components)}
	case !st.Acknowledged:
		out.Status = "deferred" // still waiting; the UI refreshes on SSE events
	case st.Deferred && !st.Edited && len(st.Followups) == 0:
		out.Status = "deferred"
	default:
		out.Status = "replied"
	}
	if st.Message != nil {
		m := toMessage(*st.Message)
		if st.Ephemeral {
			m.Ephemeral = true
		}
		out.Response = &m
	}
	for _, f := range st.Followups {
		out.Followups = append(out.Followups, toMessage(f))
	}
	if st.ResponseType == discord.ResponseAutocomplete {
		out.Choices = []choiceDTO{}
		for _, c := range st.Choices {
			out.Choices = append(out.Choices, choiceDTO(c))
		}
	}
	return out
}

func (s *server) handleSimCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID    string `json:"userId"`
		ServerID  string `json:"serverId"`
		ChannelID string `json:"channelId"`
		Bot       string `json:"bot"`
		// Name is the command path: "poll", "polls close", "polls admin purge".
		Name    string         `json:"name"`
		Options map[string]any `json:"options"`
		// Autocomplete, when set, types in an autocomplete option instead of running the
		// command: the result carries the bot's choices.
		Autocomplete *struct {
			Option string `json:"option"`
			Value  string `json:"value"`
		} `json:"autocomplete"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	app, ok := s.appID(body.Bot)
	if !ok {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown bot %q", body.Bot))
		return
	}
	var h *sim.InteractionHandle
	if ac := body.Autocomplete; ac != nil {
		h = s.opts.Sim.Autocomplete(body.UserID, body.ServerID, body.ChannelID, app, body.Name, ac.Option, ac.Value, body.Options)
	} else {
		h = s.opts.Sim.InvokeCommand(body.UserID, body.ServerID, body.ChannelID, app, body.Name, body.Options)
	}
	writeJSON(w, http.StatusOK, interactionResult(h))
}

func findComponentType(cs []discord.Component, customID string) (discord.ComponentType, bool) {
	for _, c := range cs {
		if c.Type == discord.ComponentActionRow {
			if t, ok := findComponentType(c.Components, customID); ok {
				return t, true
			}
			continue
		}
		if c.CustomID == customID {
			return c.Type, true
		}
	}
	return 0, false
}

func (s *server) handleSimComponent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID    string   `json:"userId"`
		ChannelID string   `json:"channelId"`
		MessageID string   `json:"messageId"`
		CustomID  string   `json:"customId"`
		Values    []string `json:"values"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	msg, ok := s.opts.Sim.Message(body.MessageID)
	if !ok || (body.ChannelID != "" && msg.ChannelID != body.ChannelID) {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown message %s", body.MessageID))
		return
	}
	var h *sim.InteractionHandle
	if t, _ := findComponentType(msg.Components, body.CustomID); t == discord.ComponentStringSelect {
		h = s.opts.Sim.Select(body.UserID, msg, body.CustomID, body.Values)
	} else {
		h = s.opts.Sim.ClickButton(body.UserID, msg, body.CustomID)
	}
	writeJSON(w, http.StatusOK, interactionResult(h))
}

func (s *server) handleSimModal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID    string            `json:"userId"`
		ServerID  string            `json:"serverId"`
		ChannelID string            `json:"channelId"`
		Bot       string            `json:"bot"`
		CustomID  string            `json:"customId"`
		Fields    map[string]string `json:"fields"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	app, ok := s.appID(body.Bot)
	if !ok {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown bot %q", body.Bot))
		return
	}
	h := s.opts.Sim.SubmitModal(body.UserID, body.ServerID, body.ChannelID, app, body.CustomID, body.Fields)
	writeJSON(w, http.StatusOK, interactionResult(h))
}

var optionTypeNames = map[discord.CommandOptionType]string{
	discord.OptionSubcommand: "subcommand", discord.OptionSubcommandGroup: "subcommandGroup",
	discord.OptionString: "string", discord.OptionInteger: "integer", discord.OptionBoolean: "boolean",
	discord.OptionUser: "user", discord.OptionChannel: "channel", discord.OptionRole: "role",
	discord.OptionMentionable: "mentionable", discord.OptionNumber: "number", discord.OptionAttachment: "attachment",
}

// simOptionOut is a command option of GET /sim/commands. Subcommands and groups nest their
// options.
type simOptionOut struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Type         string         `json:"type"`
	Required     bool           `json:"required"`
	Choices      []choiceDTO    `json:"choices,omitempty"`
	MinValue     *float64       `json:"minValue,omitempty"`
	MaxValue     *float64       `json:"maxValue,omitempty"`
	MinLength    *int           `json:"minLength,omitempty"`
	MaxLength    *int           `json:"maxLength,omitempty"`
	Autocomplete bool           `json:"autocomplete,omitempty"`
	Options      []simOptionOut `json:"options,omitempty"`
}

func simOptions(opts []discord.CommandOption) []simOptionOut {
	out := []simOptionOut{}
	for _, o := range opts {
		oo := simOptionOut{Name: o.Name, Description: o.Description, Type: optionTypeNames[o.Type], Required: o.Required,
			MinValue: o.MinValue, MaxValue: o.MaxValue, MinLength: o.MinLength, MaxLength: o.MaxLength, Autocomplete: o.Autocomplete}
		for _, ch := range o.Choices {
			oo.Choices = append(oo.Choices, choiceDTO(ch))
		}
		if o.IsSubcommand() {
			oo.Options = simOptions(o.Options)
		}
		out = append(out, oo)
	}
	return out
}

func (s *server) handleSimCommands(w http.ResponseWriter, r *http.Request) {
	type cmdOut struct {
		Bot         string         `json:"bot"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Options     []simOptionOut `json:"options"`
	}
	servers := s.simServerIDs()
	if sid := r.URL.Query().Get("serverId"); sid != "" {
		servers = []string{sid}
	}
	items := []cmdOut{}
	for _, sid := range servers {
		for _, b := range s.opts.Sim.Bots() {
			app, ok := s.opts.Sim.ApplicationID(b.ID)
			if !ok {
				continue
			}
			for _, c := range s.opts.Sim.Commands(app, sid) {
				items = append(items, cmdOut{Bot: s.botKeyOf(app), Name: c.Name, Description: c.Description, Options: simOptions(c.Options)})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleSimEvents streams "change" events (Server-Sent Events) whenever the world changes.
func (s *server) handleSimEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	ch, unsubscribe := s.opts.Sim.Subscribe()
	defer unsubscribe()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	seq := 0
	send := func(event string) error {
		seq++
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {}\n\n", strconv.Itoa(seq), event); err != nil {
			return err
		}
		return rc.Flush()
	}
	if err := send("change"); err != nil { // initial load hint
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	// Coalesce bursts: at most one event every 100 ms.
	throttle := time.NewTimer(0)
	<-throttle.C
	pending := false
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.opts.Stopping:
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if !pending {
				pending = true
				throttle.Reset(100 * time.Millisecond)
			}
		case <-throttle.C:
			pending = false
			if err := send("change"); err != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
