package plugin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/state"
)

// ───────────────────────────────────────────────────────────── Go → JS

type userDTO struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Bot         bool   `json:"bot"`
}

type roleDTO struct {
	ID      string `json:"id"`
	Key     string `json:"key,omitempty"`
	Name    string `json:"name"`
	Color   string `json:"color,omitempty"`
	Mention string `json:"mention"`
}

type channelDTO struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type memberDTO struct {
	User        userDTO   `json:"user"`
	Nick        string    `json:"nick,omitempty"`
	DisplayName string    `json:"displayName"`
	Roles       []roleDTO `json:"roles"`
	JoinedAt    string    `json:"joinedAt,omitempty"`
}

type attachmentDTO struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ContentType string `json:"contentType,omitempty"`
	Size        int    `json:"size"`
}

type messageDTO struct {
	ID          string          `json:"id"`
	Content     string          `json:"content"`
	Author      *userDTO        `json:"author,omitempty"`
	Member      *memberDTO      `json:"member,omitempty"`
	Channel     channelDTO      `json:"channel"`
	Attachments []attachmentDTO `json:"attachments"`
	CreatedAt   string          `json:"createdAt,omitempty"`
}

func jsTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (i *Instance) userDTO(u discord.User) userDTO {
	return userDTO{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName(), AvatarURL: u.AvatarURL, Bot: u.Bot}
}

func colorString(c int) string {
	if c == 0 {
		return ""
	}
	return fmt.Sprintf("#%06x", c&0xFFFFFF)
}

func (i *Instance) resolverKey(id string, kinds ...state.ResourceKind) string {
	if i.env.Resolver == nil {
		return ""
	}
	kind, key, ok := i.env.Resolver.Key(id)
	if !ok {
		return ""
	}
	for _, k := range kinds {
		if k == kind {
			return key
		}
	}
	return ""
}

func (i *Instance) roleDTOFrom(r discord.Role) roleDTO {
	dto := roleDTO{ID: r.ID, Name: r.Name, Color: colorString(r.Color), Key: i.resolverKey(r.ID, state.KindRole)}
	if r.ID == i.env.ServerID {
		dto.Mention = "@everyone"
		if dto.Name == "" {
			dto.Name = "@everyone"
		}
	} else {
		dto.Mention = "<@&" + r.ID + ">"
	}
	return dto
}

func (i *Instance) roleDTO(id string) roleDTO {
	if i.env.Resolver != nil {
		if r, ok := i.env.Resolver.Role(id); ok {
			return i.roleDTOFrom(r)
		}
	}
	return i.roleDTOFrom(discord.Role{ID: id})
}

func channelTypeName(t discord.ChannelType) string {
	switch t {
	case discord.ChannelText, discord.ChannelVoice, discord.ChannelAnnouncement, discord.ChannelStage,
		discord.ChannelForum, discord.ChannelMedia, discord.ChannelDM, discord.ChannelCategory:
		return t.String()
	case discord.ChannelAnnouncementThread, discord.ChannelPublicThread, discord.ChannelPrivateThread:
		return "thread"
	}
	return "text"
}

func (i *Instance) channelDTOFrom(ch discord.Channel) channelDTO {
	return channelDTO{ID: ch.ID, Name: ch.Name, Type: channelTypeName(ch.Type), Key: i.resolverKey(ch.ID, state.KindChannel, state.KindCategory)}
}

func (i *Instance) channelDTO(id string) channelDTO {
	if i.env.Resolver != nil {
		if ch, ok := i.env.Resolver.Channel(id); ok {
			return i.channelDTOFrom(ch)
		}
	}
	dto := channelDTO{ID: id, Type: "text", Key: i.resolverKey(id, state.KindChannel)}
	if i.isDMChannel(id) {
		dto.Type = "dm"
	}
	if dto.Name == "" {
		dto.Name = dto.Key
	}
	return dto
}

func (i *Instance) memberDTO(m discord.Member) memberDTO {
	dto := memberDTO{
		User:        i.userDTO(m.User),
		Nick:        m.Nick,
		DisplayName: m.DisplayName(),
		Roles:       make([]roleDTO, 0, len(m.RoleIDs)),
		JoinedAt:    jsTime(m.JoinedAt),
	}
	for _, id := range m.RoleIDs {
		dto.Roles = append(dto.Roles, i.roleDTO(id))
	}
	return dto
}

func (i *Instance) messageDTO(m discord.Message) messageDTO {
	author := i.userDTO(m.Author)
	dto := messageDTO{
		ID: m.ID, Content: m.Content, Author: &author, Channel: i.channelDTO(m.ChannelID),
		Attachments: make([]attachmentDTO, 0, len(m.Attachments)), CreatedAt: jsTime(m.CreatedAt),
	}
	if m.Member != nil {
		mem := *m.Member
		if mem.User.ID == "" {
			mem.User = m.Author
		}
		md := i.memberDTO(mem)
		dto.Member = &md
	}
	if !i.contentVisible(m) {
		dto.Content = ""
		return dto
	}
	for _, a := range m.Attachments {
		dto.Attachments = append(dto.Attachments, attachmentDTOFrom(a))
	}
	return dto
}

// contentVisible applies the MessageContent gating per plugin (Env.MessageContent).
func (i *Instance) contentVisible(m discord.Message) bool {
	if i.env.MessageContent || m.Author.ID == i.env.Bot.ID || i.isDMChannel(m.ChannelID) {
		return true
	}
	id := i.env.Bot.ID
	return id != "" && (strings.Contains(m.Content, "<@"+id+">") || strings.Contains(m.Content, "<@!"+id+">"))
}

func attachmentDTOFrom(a discord.Attachment) attachmentDTO {
	return attachmentDTO{ID: a.ID, Filename: a.Filename, URL: a.URL, ContentType: a.ContentType, Size: a.Size}
}

// ───────────────────────────────────────────────────────────── handler arguments

type contextDTO struct {
	Plugin struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Instance string `json:"instance"`
	} `json:"plugin"`
	Bot struct {
		Key  string  `json:"key"`
		User userDTO `json:"user"`
	} `json:"bot"`
	Server struct {
		ID      string `json:"id"`
		Key     string `json:"key"`
		Name    string `json:"name"`
		IconURL string `json:"iconUrl,omitempty"`
	} `json:"server"`
	Config map[string]any `json:"config"`
}

func (i *Instance) contextDTO() contextDTO {
	var d contextDTO
	d.Plugin.Name, d.Plugin.Version, d.Plugin.Instance = i.env.PluginName, i.env.PluginVersion, i.env.InstanceName
	d.Bot.Key, d.Bot.User = i.env.BotKey, i.userDTO(i.env.Bot)
	d.Server.ID, d.Server.Key, d.Server.Name, d.Server.IconURL = i.env.ServerID, i.env.ServerKey, i.env.ServerName, i.env.ServerIconURL
	d.Config = i.env.Config
	if d.Config == nil {
		d.Config = map[string]any{}
	}
	return d
}

type eventDTO struct {
	Member   *memberDTO  `json:"member,omitempty"`
	Previous *memberDTO  `json:"previous,omitempty"`
	User     *userDTO    `json:"user,omitempty"`
	Message  *messageDTO `json:"message,omitempty"`
	ID       string      `json:"id,omitempty"`
	Channel  *channelDTO `json:"channel,omitempty"`
	Emoji    string      `json:"emoji,omitempty"`
}

func (i *Instance) noteMessageChannel(m discord.Message) {
	switch m.ServerID {
	case i.env.ServerID:
		i.markServerChannel(m.ChannelID)
	case "":
		i.markDMChannel(m.ChannelID)
	}
}

func (i *Instance) eventDTO(ev discord.Event) eventDTO {
	var d eventDTO
	switch e := ev.(type) {
	case discord.MemberJoinEvent:
		m := i.memberDTO(e.Member)
		d.Member = &m
	case discord.MemberLeaveEvent:
		u := i.userDTO(e.User)
		d.User = &u
	case discord.MemberUpdateEvent:
		m := i.memberDTO(e.Member)
		d.Member = &m
		if e.Previous != nil {
			p := i.memberDTO(*e.Previous)
			d.Previous = &p
		}
	case discord.MessageCreateEvent:
		i.noteMessageChannel(e.Message)
		m := i.messageDTO(e.Message)
		d.Message = &m
	case discord.MessageUpdateEvent:
		i.noteMessageChannel(e.Message)
		m := i.messageDTO(e.Message)
		d.Message = &m
	case discord.MessageDeleteEvent:
		i.noteMessageChannel(discord.Message{ChannelID: e.ChannelID, ServerID: e.ServerID})
		ch := i.channelDTO(e.ChannelID)
		d.ID, d.Channel = e.ID, &ch
	case discord.ReactionAddEvent:
		i.reactionDTO(&d, e.ReactionEvent)
	case discord.ReactionRemoveEvent:
		i.reactionDTO(&d, e.ReactionEvent)
	}
	return d
}

// reactionDTO builds a partial message (the gateway only sends IDs; discord.Client has no
// GetMessage): id + channel, no author/content.
func (i *Instance) reactionDTO(d *eventDTO, e discord.ReactionEvent) {
	i.noteMessageChannel(discord.Message{ChannelID: e.ChannelID, ServerID: e.ServerID})
	d.Message = &messageDTO{ID: e.MessageID, Channel: i.channelDTO(e.ChannelID), Attachments: []attachmentDTO{}}
	u := userDTO{ID: e.UserID}
	if e.Member != nil && (e.Member.User.ID == e.UserID || e.Member.User.ID == "") {
		mu := e.Member.User
		mu.ID = e.UserID
		u = i.userDTO(mu)
	}
	d.User = &u
	d.Emoji = e.Emoji
}

type optionDTO struct {
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Value      any            `json:"value,omitempty"`
	User       *userDTO       `json:"user,omitempty"`
	Role       *roleDTO       `json:"role,omitempty"`
	Channel    *channelDTO    `json:"channel,omitempty"`
	Attachment *attachmentDTO `json:"attachment,omitempty"`
}

type interactionDTO struct {
	Handle      string            `json:"handle"`
	ID          string            `json:"id"`
	User        userDTO           `json:"user"`
	Member      *memberDTO        `json:"member,omitempty"`
	Channel     *channelDTO       `json:"channel,omitempty"`
	Command     string            `json:"command,omitempty"`
	Options     []optionDTO       `json:"options,omitempty"`
	ComponentID string            `json:"componentId,omitempty"`
	Values      []string          `json:"values,omitempty"`
	Message     *messageDTO       `json:"message,omitempty"`
	ModalID     string            `json:"modalId,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
}

var optionTypeNames = map[discord.CommandOptionType]string{}

func init() {
	for n, t := range optionTypes {
		optionTypeNames[t] = n
	}
}

func (i *Instance) interactionDTO(x *interaction, localID string) interactionDTO {
	it := x.it
	user := it.User
	if user.ID == "" && it.Member != nil {
		user = it.Member.User
	}
	d := interactionDTO{Handle: x.handle, ID: it.ID, User: i.userDTO(user)}
	if it.Member != nil {
		m := *it.Member
		if m.User.ID == "" {
			m.User = user
		}
		md := i.memberDTO(m)
		d.Member = &md
	}
	if it.ChannelID != "" {
		switch it.ServerID {
		case i.env.ServerID:
			i.markServerChannel(it.ChannelID)
		case "":
			i.markDMChannel(it.ChannelID)
		}
		ch := i.channelDTO(it.ChannelID)
		d.Channel = &ch
	}
	switch x.kind {
	case "command":
		d.Command = localID
		d.Options = i.optionDTOs(it)
	case "component":
		d.ComponentID = localID
		d.Values = it.Values
		if it.Message != nil {
			i.noteMessageChannel(*it.Message)
			m := i.messageDTO(*it.Message)
			d.Message = &m
		}
	case "modal":
		d.ModalID = localID
		d.Fields = it.ModalFields
		if d.Fields == nil {
			d.Fields = map[string]string{}
		}
	}
	return d
}

func (i *Instance) optionDTOs(it discord.Interaction) []optionDTO {
	out := make([]optionDTO, 0, len(it.Options))
	res := it.Resolved
	for _, o := range it.Options {
		dto := optionDTO{Name: o.Name, Type: optionTypeNames[o.Type]}
		id, _ := o.Value.(string)
		switch o.Type {
		case discord.OptionUser:
			u := discord.User{ID: id}
			if ru, ok := res.Users[id]; ok {
				u = ru
			} else if rm, ok := res.Members[id]; ok && rm.User.ID != "" {
				u = rm.User
			}
			ud := i.userDTO(u)
			dto.User = &ud
		case discord.OptionRole:
			r := discord.Role{ID: id}
			if rr, ok := res.Roles[id]; ok {
				r = rr
			} else if i.env.Resolver != nil {
				if rr, ok := i.env.Resolver.Role(id); ok {
					r = rr
				}
			}
			rd := i.roleDTOFrom(r)
			dto.Role = &rd
		case discord.OptionChannel:
			var cd channelDTO
			if ch, ok := res.Channels[id]; ok {
				if ch.ServerID == "" || ch.ServerID == i.env.ServerID {
					i.markServerChannel(id)
				}
				cd = i.channelDTOFrom(ch)
			} else {
				cd = i.channelDTO(id)
			}
			dto.Channel = &cd
		case discord.OptionMentionable:
			if u, ok := res.Users[id]; ok {
				ud := i.userDTO(u)
				dto.User = &ud
			} else {
				r := discord.Role{ID: id}
				if rr, ok := res.Roles[id]; ok {
					r = rr
				}
				rd := i.roleDTOFrom(r)
				dto.Role = &rd
			}
		case discord.OptionAttachment:
			if a, ok := res.Attachments[id]; ok {
				ad := attachmentDTOFrom(a)
				dto.Attachment = &ad
			}
		default:
			dto.Value = o.Value
		}
		out = append(out, dto)
	}
	return out
}

type routeDTO struct {
	Params map[string]string `json:"params"`
	Query  map[string]string `json:"query"`
	Body   json.RawMessage   `json:"body,omitempty"`
	Viewer *viewerDTO        `json:"viewer,omitempty"`
}

type viewerDTO struct {
	User   userDTO    `json:"user"`
	Member *memberDTO `json:"member,omitempty"`
}

type payloadDTO struct {
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ───────────────────────────────────────────────────────────── JS → Go: messages

// messageInput mirrors MessagePayload / ReplyInput of cicord-sdk.d.ts.
type messageInput struct {
	Content         *string     `json:"content"`
	Embeds          []embedIn   `json:"embeds"`
	Components      []rowIn     `json:"components"`
	AllowedMentions *mentionsIn `json:"allowedMentions"`
	Ephemeral       *bool       `json:"ephemeral"`
}

type mentionsIn struct {
	Users    bool `json:"users"`
	Roles    bool `json:"roles"`
	Everyone bool `json:"everyone"`
}

type embedIn struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Color       any    `json:"color"`
	Timestamp   any    `json:"timestamp"`
	Fields      []struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	} `json:"fields"`
	Author *struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		IconURL string `json:"iconUrl"`
	} `json:"author"`
	Footer *struct {
		Text    string `json:"text"`
		IconURL string `json:"iconUrl"`
	} `json:"footer"`
	Image *struct {
		URL string `json:"url"`
	} `json:"image"`
	Thumbnail *struct {
		URL string `json:"url"`
	} `json:"thumbnail"`
}

type rowIn struct {
	Type       string   `json:"type"`
	Components []compIn `json:"components"`
}

type compIn struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	Emoji       string `json:"emoji"`
	Style       string `json:"style"`
	URL         string `json:"url"`
	Disabled    bool   `json:"disabled"`
	Placeholder string `json:"placeholder"`
	MinValues   *int   `json:"minValues"`
	MaxValues   *int   `json:"maxValues"`
	Options     []struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
		Emoji       string `json:"emoji"`
		Default     bool   `json:"default"`
	} `json:"options"`
}

var buttonStyles = map[string]discord.ButtonStyle{
	"": discord.ButtonPrimary, "primary": discord.ButtonPrimary, "secondary": discord.ButtonSecondary,
	"success": discord.ButtonSuccess, "danger": discord.ButtonDanger,
}

// ParseColor converts "#RRGGBB" to 0xRRGGBB.
func ParseColor(s string) (int, error) {
	h, ok := strings.CutPrefix(s, "#")
	if !ok || len(h) != 6 {
		return 0, fmt.Errorf("invalid color %q (expected #RRGGBB)", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid color %q (expected #RRGGBB)", s)
	}
	return int(v), nil
}

// messageCreate converts a message input. allowEphemeral is true for interaction replies.
func (i *Instance) messageCreate(in messageInput, allowEphemeral bool) (discord.MessageCreate, error) {
	var mc discord.MessageCreate
	if in.Ephemeral != nil && !allowEphemeral {
		return mc, argErr("ephemeral is only supported in interaction replies")
	}
	if in.Content != nil {
		mc.Content = *in.Content
	}
	if n := utf8.RuneCountInString(mc.Content); n > 2000 {
		return mc, argErr("message content is %d characters long (max 2000)", n)
	}
	if len(in.Embeds) > 10 {
		return mc, argErr("at most 10 embeds per message")
	}
	for k, e := range in.Embeds {
		em, err := convertEmbed(e)
		if err != nil {
			return mc, argErr("embeds[%d]: %v", k, err)
		}
		mc.Embeds = append(mc.Embeds, em)
	}
	if len(in.Components) > 5 {
		return mc, argErr("at most 5 component rows per message")
	}
	for k, r := range in.Components {
		row, err := i.convertRow(r)
		if err != nil {
			return mc, argErr("components[%d]: %v", k, err)
		}
		mc.Components = append(mc.Components, row)
	}
	if in.AllowedMentions != nil {
		mc.AllowedMentions = &discord.AllowedMentions{Users: in.AllowedMentions.Users, Roles: in.AllowedMentions.Roles, Everyone: in.AllowedMentions.Everyone}
	}
	if mc.Content == "" && len(mc.Embeds) == 0 && len(mc.Components) == 0 {
		return mc, argErr("message is empty (set content, embeds or components)")
	}
	return mc, nil
}

func convertEmbed(e embedIn) (discord.Embed, error) {
	em := discord.Embed{Title: e.Title, Description: e.Description, URL: e.URL}
	switch c := e.Color.(type) {
	case nil:
	case string:
		v, err := ParseColor(c)
		if err != nil {
			return em, err
		}
		em.Color = v
	default:
		return em, fmt.Errorf("color must be a \"#RRGGBB\" string")
	}
	switch ts := e.Timestamp.(type) {
	case nil:
	case string:
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return em, fmt.Errorf("invalid timestamp %q (expected a Date or an ISO 8601 string)", ts)
		}
		em.Timestamp = &t
	default:
		return em, fmt.Errorf("timestamp must be a Date or an ISO 8601 string")
	}
	if len(e.Fields) > 25 {
		return em, fmt.Errorf("at most 25 fields")
	}
	for _, f := range e.Fields {
		if f.Name == "" || f.Value == "" {
			return em, fmt.Errorf("fields need a name and a value")
		}
		em.Fields = append(em.Fields, discord.EmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
	}
	if e.Author != nil {
		em.Author = &discord.EmbedAuthor{Name: e.Author.Name, URL: e.Author.URL, IconURL: e.Author.IconURL}
	}
	if e.Footer != nil {
		em.Footer = &discord.EmbedFooter{Text: e.Footer.Text, IconURL: e.Footer.IconURL}
	}
	if e.Image != nil {
		em.ImageURL = e.Image.URL
	}
	if e.Thumbnail != nil {
		em.ThumbURL = e.Thumbnail.URL
	}
	return em, nil
}

func (i *Instance) convertRow(r rowIn) (discord.Component, error) {
	row := discord.Component{Type: discord.ComponentActionRow}
	if r.Type != "row" {
		return row, fmt.Errorf(`type must be "row"`)
	}
	if len(r.Components) == 0 || len(r.Components) > 5 {
		return row, fmt.Errorf("a row holds 1 to 5 buttons or a single select menu")
	}
	for k, c := range r.Components {
		var comp discord.Component
		switch c.Type {
		case "button":
			style, ok := buttonStyles[c.Style]
			if !ok {
				return row, fmt.Errorf("components[%d]: invalid button style %q", k, c.Style)
			}
			id, err := EncodeCustomID(i.env.InstanceName, c.ID)
			if err != nil {
				return row, fmt.Errorf("components[%d]: %w", k, err)
			}
			if c.Label == "" && c.Emoji == "" {
				return row, fmt.Errorf("components[%d]: a button needs a label or an emoji", k)
			}
			comp = discord.Component{Type: discord.ComponentButton, CustomID: id, Label: c.Label, Emoji: c.Emoji, Style: style, Disabled: c.Disabled}
		case "link":
			if !strings.HasPrefix(c.URL, "https://") && !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "discord://") {
				return row, fmt.Errorf("components[%d]: a link button needs an http(s) url", k)
			}
			if c.Label == "" && c.Emoji == "" {
				return row, fmt.Errorf("components[%d]: a button needs a label or an emoji", k)
			}
			comp = discord.Component{Type: discord.ComponentButton, Style: discord.ButtonLink, URL: c.URL, Label: c.Label, Emoji: c.Emoji, Disabled: c.Disabled}
		case "select":
			if len(r.Components) != 1 {
				return row, fmt.Errorf("a select menu must be alone in its row")
			}
			id, err := EncodeCustomID(i.env.InstanceName, c.ID)
			if err != nil {
				return row, fmt.Errorf("components[%d]: %w", k, err)
			}
			if len(c.Options) == 0 || len(c.Options) > 25 {
				return row, fmt.Errorf("components[%d]: a select menu needs 1 to 25 options", k)
			}
			comp = discord.Component{Type: discord.ComponentStringSelect, CustomID: id, Placeholder: c.Placeholder, MinValues: c.MinValues, MaxValues: c.MaxValues, Disabled: c.Disabled}
			for _, o := range c.Options {
				if o.Value == "" || o.Label == "" {
					return row, fmt.Errorf("components[%d]: select options need a value and a label", k)
				}
				comp.Options = append(comp.Options, discord.SelectOption{Label: o.Label, Value: o.Value, Description: o.Description, Emoji: o.Emoji, Default: o.Default})
			}
		default:
			return row, fmt.Errorf("components[%d]: unknown component type %q (button, link, select)", k, c.Type)
		}
		row.Components = append(row.Components, comp)
	}
	return row, nil
}

// modalInput mirrors Modal of cicord-sdk.d.ts.
type modalInput struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Fields []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Style       string `json:"style"`
		Required    *bool  `json:"required"`
		Placeholder string `json:"placeholder"`
		Value       string `json:"value"`
		MinLength   *int   `json:"minLength"`
		MaxLength   *int   `json:"maxLength"`
	} `json:"fields"`
}

func (i *Instance) convertModal(in modalInput) (discord.Modal, error) {
	id, err := EncodeCustomID(i.env.InstanceName, in.ID)
	if err != nil {
		return discord.Modal{}, argErr("showModal(): %v", err)
	}
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 45 {
		return discord.Modal{}, argErr("showModal(): title must be 1-45 characters")
	}
	if len(in.Fields) == 0 || len(in.Fields) > 5 {
		return discord.Modal{}, argErr("showModal(): a modal has 1 to 5 fields")
	}
	m := discord.Modal{CustomID: id, Title: in.Title}
	for k, f := range in.Fields {
		if f.ID == "" || len(f.ID) > MaxCustomIDLength {
			return m, argErr("showModal(): fields[%d].id must be 1-%d characters", k, MaxCustomIDLength)
		}
		if f.Label == "" || utf8.RuneCountInString(f.Label) > 45 {
			return m, argErr("showModal(): fields[%d].label must be 1-45 characters", k)
		}
		style := 1
		switch f.Style {
		case "", "short":
		case "paragraph":
			style = 2
		default:
			return m, argErr("showModal(): fields[%d].style must be short or paragraph", k)
		}
		required := true
		if f.Required != nil {
			required = *f.Required
		}
		input := discord.Component{
			Type: discord.ComponentTextInput, CustomID: f.ID, Label: f.Label, TextStyle: style,
			Required: required, Placeholder: f.Placeholder, Value: f.Value, MinLength: f.MinLength, MaxLength: f.MaxLength,
		}
		m.Components = append(m.Components, discord.Component{Type: discord.ComponentActionRow, Components: []discord.Component{input}})
	}
	return m, nil
}
