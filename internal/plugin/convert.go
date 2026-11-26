package plugin

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// ───────────────────────────────────────────────────────────── Go → JS

type userDTO struct {
	CreatedAt   string   `json:"createdAt,omitempty"`
	Flags       []string `json:"flags"`
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	AvatarURL   string   `json:"avatarUrl,omitempty"`
	Bot         bool     `json:"bot"`
}

type roleDTO struct {
	Managed         *bool  `json:"managed,omitempty"`
	DeclaredMembers bool   `json:"declaredMembers"`
	ID              string `json:"id"`
	Key             string `json:"key,omitempty"`
	Name            string `json:"name"`
	Color           string `json:"color,omitempty"`
	Mention         string `json:"mention"`
	// Position and Mentionable are omitted when the role is not known (not cached).
	Position    *int  `json:"position,omitempty"`
	Mentionable *bool `json:"mentionable,omitempty"`
	// Permissions is the role's Discord permission bitfield as a decimal string, like
	// Discord's API. Omitted when the role is not known: unknown is never "no permissions".
	Permissions *string `json:"permissions,omitempty"`
}

type channelDTO struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
	Type string `json:"type"`
	// CategoryID is the channel's category ("" = none, or not known).
	CategoryID string `json:"categoryId,omitempty"`
}

type memberDTO struct {
	Pending       *bool          `json:"pending,omitempty"`
	Flags         []string       `json:"flags"`
	PremiumSince  string         `json:"premiumSince,omitempty"`
	TimedOutUntil string         `json:"timedOutUntil,omitempty"`
	AvatarURL     string         `json:"avatarUrl,omitempty"`
	DeclaredRoles []string       `json:"declaredRoles"`
	subject       discord.Member // host-only permission input, never serialized
	User          userDTO        `json:"user"`
	Nick          string         `json:"nick,omitempty"`
	DisplayName   string         `json:"displayName"`
	Roles         []roleDTO      `json:"roles"`
	JoinedAt      string         `json:"joinedAt,omitempty"`
	// Permissions: server-level permission names (see memberPermissions).
	Permissions []string `json:"permissions"`
	// Access: "member", "moderator" or "admin" (see memberAccess).
	Access string `json:"access"`
}

type attachmentDTO struct {
	Description string `json:"description,omitempty"`
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ContentType string `json:"contentType,omitempty"`
	Size        int    `json:"size"`
}

type messageDTO struct {
	URL         string                    `json:"url"`
	Reactions   []discord.ReactionSummary `json:"reactions"`
	Reference   *discord.MessageReference `json:"reference,omitempty"`
	ID          string                    `json:"id"`
	Content     string                    `json:"content"`
	Author      *userDTO                  `json:"author,omitempty"`
	Member      *memberDTO                `json:"member,omitempty"`
	Channel     channelDTO                `json:"channel"`
	Attachments []attachmentDTO           `json:"attachments"`
	Embeds      []embedDTO                `json:"embeds"`
	CreatedAt   string                    `json:"createdAt,omitempty"`
	Poll        *discord.Poll             `json:"poll,omitempty"`
}

// embedDTO is a received embed, in the shape of the SDK's Embed (the one plugins send).
type embedDTO struct {
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	URL         string          `json:"url,omitempty"`
	Color       string          `json:"color,omitempty"`
	Timestamp   string          `json:"timestamp,omitempty"`
	Fields      []embedFieldDTO `json:"fields,omitempty"`
	Author      *embedAuthorDTO `json:"author,omitempty"`
	Footer      *embedFooterDTO `json:"footer,omitempty"`
	Image       *embedURLDTO    `json:"image,omitempty"`
	Thumbnail   *embedURLDTO    `json:"thumbnail,omitempty"`
}

type embedFieldDTO struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type embedAuthorDTO struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"iconUrl,omitempty"`
}

type embedFooterDTO struct {
	Text    string `json:"text"`
	IconURL string `json:"iconUrl,omitempty"`
}

type embedURLDTO struct {
	URL string `json:"url"`
}

func embedDTOFrom(e discord.Embed) embedDTO {
	out := embedDTO{Title: e.Title, Description: e.Description, URL: e.URL}
	if e.Color != 0 {
		out.Color = fmt.Sprintf("#%06x", e.Color&0xffffff)
	}
	if e.Timestamp != nil {
		out.Timestamp = jsTime(*e.Timestamp)
	}
	for _, f := range e.Fields {
		out.Fields = append(out.Fields, embedFieldDTO{Name: f.Name, Value: f.Value, Inline: f.Inline})
	}
	if a := e.Author; a != nil {
		out.Author = &embedAuthorDTO{Name: a.Name, URL: a.URL, IconURL: a.IconURL}
	}
	if f := e.Footer; f != nil {
		out.Footer = &embedFooterDTO{Text: f.Text, IconURL: f.IconURL}
	}
	if e.ImageURL != "" {
		out.Image = &embedURLDTO{URL: e.ImageURL}
	}
	if e.ThumbURL != "" {
		out.Thumbnail = &embedURLDTO{URL: e.ThumbURL}
	}
	return out
}

func jsTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func flagNames[T ~int | ~uint64](mask T, names []string) []string {
	out := []string{}
	for bit, name := range names {
		if name != "" && mask&(1<<bit) != 0 {
			out = append(out, name)
		}
	}
	return out
}

var publicUserFlagNames = []string{"staff", "partner", "hypesquad", "bugHunterLevel1", "", "", "hypesquadOnlineHouse1", "hypesquadOnlineHouse2", "hypesquadOnlineHouse3", "premiumEarlySupporter", "teamPseudoUser", "", "", "", "bugHunterLevel2", "", "verifiedBot", "verifiedDeveloper", "certifiedModerator", "botHTTPInteractions"}
var memberFlagNames = []string{"didRejoin", "completedOnboarding", "bypassesVerification"}

func optionalTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return jsTime(*t)
}

func (i *Instance) userDTO(u discord.User) userDTO {
	return userDTO{CreatedAt: jsTime(discord.SnowflakeTime(u.ID)), Flags: flagNames(u.PublicFlags, publicUserFlagNames), ID: u.ID, Username: u.Username, DisplayName: u.DisplayName(), AvatarURL: u.AvatarURL, Bot: u.Bot}
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

// roleDTOFrom converts a known role (from the cache or a resolved interaction payload).
func (i *Instance) roleDTOFrom(r discord.Role) roleDTO {
	dto := i.unknownRoleDTO(r)
	pos, mentionable := r.Position, r.Mentionable
	dto.Position, dto.Mentionable = &pos, &mentionable
	dto.Managed = &r.Managed
	perms := strconv.FormatUint(uint64(r.Permissions), 10)
	dto.Permissions = &perms
	dto.DeclaredMembers = slices.Contains(i.env.DeclaredMemberRoles, dto.Key)
	return dto
}

// unknownRoleDTO converts a role whose position and settings are not known: only its id
// (and what the payload carried) is reliable, so position and mentionable are left out.
func (i *Instance) unknownRoleDTO(r discord.Role) roleDTO {
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
	return i.unknownRoleDTO(discord.Role{ID: id})
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
	return channelDTO{ID: ch.ID, Name: ch.Name, Type: channelTypeName(ch.Type), Key: i.resolverKey(ch.ID, state.KindChannel, state.KindCategory),
		CategoryID: ch.ParentID}
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
		subject: m, Pending: m.Pending, Flags: flagNames(m.Flags, memberFlagNames), PremiumSince: optionalTime(m.PremiumSince), TimedOutUntil: optionalTime(m.TimeoutUntil), AvatarURL: m.AvatarURL,
		DeclaredRoles: i.env.DeclaredRolesByUser[m.User.ID],
		User:          i.userDTO(m.User),
		Nick:          m.Nick,
		DisplayName:   m.DisplayName(),
		Roles:         make([]roleDTO, 0, len(m.RoleIDs)),
		JoinedAt:      jsTime(m.JoinedAt),
		Permissions:   i.memberPermissions(m).Names(),
		Access:        i.memberAccess(m),
	}
	for _, id := range m.RoleIDs {
		dto.Roles = append(dto.Roles, i.roleDTO(id))
	}
	return dto
}

// resolvedRoleDTO converts a role of an interaction: the resolved payload, else the cache.
func (i *Instance) resolvedRoleDTO(res discord.Resolved, id string) roleDTO {
	if r, ok := res.Roles[id]; ok {
		return i.roleDTOFrom(r)
	}
	return i.roleDTO(id)
}

const allPermissions = ^discord.Permissions(0)

var permAdministrator = discord.MustPermissions("Administrator")

// ownerID is the server owner's user id ("" when unknown).
func (i *Instance) ownerID() string {
	if i.env.OwnerID == nil {
		return ""
	}
	return i.env.OwnerID()
}

// memberPermissions is the member's server-level permission set: @everyone's permissions
// and those of every role, all of them for the owner and for Administrator. Channel
// overwrites and timeouts are not applied, and roles missing from the cache count for
// nothing (the set errs on the side of fewer permissions).
func (i *Instance) memberPermissions(m discord.Member) discord.Permissions {
	if owner := i.ownerID(); owner != "" && m.User.ID == owner {
		return allPermissions
	}
	if i.env.Resolver == nil {
		return 0
	}
	var p discord.Permissions
	if r, ok := i.env.Resolver.Role(i.env.ServerID); ok {
		p = r.Permissions
	}
	for _, id := range m.RoleIDs {
		if r, ok := i.env.Resolver.Role(id); ok {
			p |= r.Permissions
		}
	}
	if p.Has(permAdministrator) {
		return allPermissions
	}
	return p
}

// memberAccess is the member's Cicord access level, as the portal and the API compute it
// (https://cicord.dev/docs/reference/http-api/authentication/): "admin" for the owner and spec.access.admins, "moderator" for
// spec.access.moderators, else "member".
func (i *Instance) memberAccess(m discord.Member) string {
	if owner := i.ownerID(); owner != "" && m.User.ID == owner {
		return "admin"
	}
	matches := func(users, roleKeys []string) bool {
		if slices.Contains(users, m.User.ID) {
			return true
		}
		if i.env.Resolver == nil {
			return false
		}
		for _, k := range roleKeys {
			if id, ok := i.env.Resolver.ID(state.KindRole, k); ok && m.HasRole(id) {
				return true
			}
		}
		return false
	}
	switch a := i.env.Access; {
	case matches(a.AdminUsers, a.AdminRoles):
		return "admin"
	case matches(a.ModeratorUsers, a.ModeratorRoles):
		return "moderator"
	}
	return "member"
}

func (i *Instance) messageURL(server, channel, id string) string {
	if i.isDMChannel(channel) {
		server = "@me"
	} else if server == "" {
		server = i.env.ServerID
	}
	return "https://discord.com/channels/" + server + "/" + channel + "/" + id
}
func (i *Instance) messageDTO(m discord.Message) messageDTO {
	if m.ServerID == "" && m.ID != "" && i.isDMChannel(m.ChannelID) {
		i.rememberDMMessage(m.ID)
	}
	author := i.userDTO(m.Author)
	dto := messageDTO{
		URL: i.messageURL(m.ServerID, m.ChannelID, m.ID), Reactions: m.Reactions, Reference: m.Reference, ID: m.ID, Content: m.Content, Author: &author, Channel: i.channelDTO(m.ChannelID),
		Attachments: make([]attachmentDTO, 0, len(m.Attachments)), Embeds: make([]embedDTO, 0, len(m.Embeds)),
		CreatedAt: jsTime(m.CreatedAt),
	}
	if dto.Reference == nil && m.ReplyToID != "" {
		dto.Reference = &discord.MessageReference{MessageID: m.ReplyToID, ChannelID: m.ChannelID, ServerID: m.ServerID}
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
	dto.Poll = m.PollResults
	for _, a := range m.Attachments {
		dto.Attachments = append(dto.Attachments, attachmentDTOFrom(a))
	}
	for _, e := range m.Embeds {
		dto.Embeds = append(dto.Embeds, embedDTOFrom(e))
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
	return attachmentDTO{Description: a.Description, ID: a.ID, Filename: a.Filename, URL: a.URL, ContentType: a.ContentType, Size: a.Size}
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
		PreferredLocale string `json:"preferredLocale,omitempty"`
		ID              string `json:"id"`
		Key             string `json:"key"`
		Name            string `json:"name"`
		IconURL         string `json:"iconUrl,omitempty"`
	} `json:"server"`
	Config map[string]any `json:"config"`
}

func (i *Instance) contextDTO() contextDTO {
	var d contextDTO
	d.Plugin.Name, d.Plugin.Version, d.Plugin.Instance = i.env.PluginName, i.env.PluginVersion, i.env.InstanceName
	d.Bot.Key, d.Bot.User = i.env.BotKey, i.userDTO(i.env.Bot)
	d.Server.ID, d.Server.Key, d.Server.Name, d.Server.IconURL = i.env.ServerID, i.env.ServerKey, i.env.ServerName, i.env.ServerIconURL
	d.Server.PreferredLocale = i.env.ServerPreferredLocale
	d.Config = i.env.Config
	if d.Config == nil {
		d.Config = map[string]any{}
	}
	return d
}

type eventDTO struct {
	Structure map[string]any `json:"structure,omitempty"`
	Audit     *auditDTO      `json:"audit,omitempty"`
	Member    *memberDTO     `json:"member,omitempty"`
	Previous  *memberDTO     `json:"previous,omitempty"`
	User      *userDTO       `json:"user,omitempty"`
	Message   *messageDTO    `json:"message,omitempty"`
	ID        string         `json:"id,omitempty"`
	Channel   *channelDTO    `json:"channel,omitempty"`
	Emoji     string         `json:"emoji,omitempty"`
	Server    *serverDTO     `json:"server,omitempty"`
	Voice     *voiceDTO      `json:"voice,omitempty"`
}

// voiceDTO is the voiceStateUpdate event: where the member is now and was before.
type voiceDTO struct {
	Channel   *channelDTO `json:"channel,omitempty"`
	Previous  *channelDTO `json:"previous,omitempty"`
	SelfMute  bool        `json:"selfMute"`
	SelfDeaf  bool        `json:"selfDeaf"`
	Streaming bool        `json:"streaming"`
	Video     bool        `json:"video"`
}

// serverDTO is the server of serverJoin / serverLeave (only id for a leave).
type serverDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	IconURL string `json:"iconUrl,omitempty"`
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
	d.Structure = i.structureEvent(ev)
	switch e := ev.(type) {
	case discord.AuditLogEntryEvent:
		a := i.auditDTO(e)
		d.Audit = &a
	case discord.BanAddEvent:
		u := i.userDTO(e.User)
		d.User = &u
	case discord.BanRemoveEvent:
		u := i.userDTO(e.User)
		d.User = &u
	case discord.MemberJoinEvent:
		m := i.memberDTO(e.Member)
		d.Member = &m
	case discord.MemberLeaveEvent:
		if e.Previous != nil {
			prev := i.memberDTO(*e.Previous)
			d.Previous = &prev
		}
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
	case discord.ServerJoinEvent:
		d.Server = &serverDTO{ID: e.Server.ID, Name: e.Server.Name, IconURL: e.Server.IconURL}
	case discord.ServerLeaveEvent:
		d.Server = &serverDTO{ID: e.ServerID}
	case discord.VoiceStateUpdateEvent:
		st := e.State
		u := i.userDTO(discord.User{ID: st.UserID, Bot: st.Bot})
		if st.Member != nil && st.Member.User.ID == st.UserID {
			m := i.memberDTO(*st.Member)
			d.Member, u = &m, m.User
		}
		d.User = &u
		v := &voiceDTO{SelfMute: st.SelfMute, SelfDeaf: st.SelfDeaf, Streaming: st.SelfStream, Video: st.SelfVideo}
		if st.ChannelID != "" {
			i.markServerChannel(st.ChannelID)
			ch := i.channelDTO(st.ChannelID)
			v.Channel = &ch
		}
		if e.Previous != nil && e.Previous.ChannelID != "" {
			ch := i.channelDTO(e.Previous.ChannelID)
			v.Previous = &ch
		}
		d.Voice = v
	}
	return d
}

// reactionDTO builds a partial message (the gateway only sends IDs): id + channel, no
// author/content. Plugins fetch the full message with channel.message(id) when needed
// (one REST call per reaction is not made for them).
func (i *Instance) reactionDTO(d *eventDTO, e discord.ReactionEvent) {
	i.noteMessageChannel(discord.Message{ChannelID: e.ChannelID, ServerID: e.ServerID})
	d.Message = &messageDTO{URL: i.messageURL(e.ServerID, e.ChannelID, e.MessageID), ID: e.MessageID, Channel: i.channelDTO(e.ChannelID), Attachments: []attachmentDTO{}}
	u := i.userDTO(discord.User{ID: e.UserID})
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
	Selections    map[string][]string `json:"selections,omitempty"`
	TargetMessage *messageDTO         `json:"targetMessage,omitempty"`
	TargetMember  *memberDTO          `json:"targetMember,omitempty"`
	Locale        string              `json:"locale,omitempty"`
	Handle        string              `json:"handle"`
	ID            string              `json:"id"`
	User          userDTO             `json:"user"`
	Member        *memberDTO          `json:"member,omitempty"`
	Channel       *channelDTO         `json:"channel,omitempty"`
	Command       string              `json:"command,omitempty"`
	Group         string              `json:"group,omitempty"`
	Subcommand    string              `json:"subcommand,omitempty"`
	Options       []optionDTO         `json:"options,omitempty"`
	ComponentID   string              `json:"componentId,omitempty"`
	Values        []string            `json:"values,omitempty"`
	Message       *messageDTO         `json:"message,omitempty"`
	ModalID       string              `json:"modalId,omitempty"`
	Fields        map[string]string   `json:"fields,omitempty"`
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
	d := interactionDTO{Locale: it.Locale, Handle: x.handle, ID: it.ID, User: i.userDTO(user)}
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
	case "contextMenu":
		if it.TargetMessage != nil {
			m := i.messageDTO(*it.TargetMessage)
			d.TargetMessage = &m
		}
		if it.TargetMember != nil {
			m := i.memberDTO(*it.TargetMember)
			d.TargetMember = &m
		}
	case "command":
		path, opts := it.CommandPath()
		d.Command = path[0]
		d.Group, d.Subcommand = pathParts(path)
		d.Options = i.optionDTOs(opts, it.Resolved)
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
		d.Selections = it.ModalSelections
		d.Fields = it.ModalFields
		if d.Fields == nil {
			d.Fields = map[string]string{}
		}
	}
	return d
}

// pathParts returns the group and the subcommand of a command path.
func pathParts(path []string) (group, sub string) {
	switch len(path) {
	case 2:
		return "", path[1]
	case 3:
		return path[1], path[2]
	}
	return "", ""
}

func (i *Instance) optionDTOs(opts []discord.InteractionOption, res discord.Resolved) []optionDTO {
	out := make([]optionDTO, 0, len(opts))
	for _, o := range opts {
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
			rd := i.resolvedRoleDTO(res, id)
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
				rd := i.resolvedRoleDTO(res, id)
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

// autocompleteDTO is the AutocompleteInteraction of an option's autocomplete handler. The
// other options are the raw values typed so far (IDs for users, roles, channels): Discord
// sends no resolved data with an autocomplete.
type autocompleteDTO struct {
	Locale     string         `json:"locale,omitempty"`
	ID         string         `json:"id"`
	User       userDTO        `json:"user"`
	Member     *memberDTO     `json:"member,omitempty"`
	Channel    *channelDTO    `json:"channel,omitempty"`
	Command    string         `json:"command"`
	Group      string         `json:"group,omitempty"`
	Subcommand string         `json:"subcommand,omitempty"`
	Options    map[string]any `json:"options"`
	Focused    struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"focused"`
}

func (i *Instance) autocompleteDTO(it discord.Interaction, path []string, opts []discord.InteractionOption, focused discord.InteractionOption) autocompleteDTO {
	user := it.User
	if user.ID == "" && it.Member != nil {
		user = it.Member.User
	}
	d := autocompleteDTO{Locale: it.Locale, ID: it.ID, User: i.userDTO(user), Command: path[0], Options: map[string]any{}}
	d.Group, d.Subcommand = pathParts(path)
	if it.Member != nil {
		m := *it.Member
		if m.User.ID == "" {
			m.User = user
		}
		md := i.memberDTO(m)
		d.Member = &md
	}
	if it.ChannelID != "" {
		ch := i.channelDTO(it.ChannelID)
		d.Channel = &ch
	}
	for _, o := range opts {
		if !o.Focused && o.Value != nil {
			d.Options[o.Name] = o.Value
		}
	}
	d.Focused.Name = focused.Name
	switch v := focused.Value.(type) {
	case string:
		d.Focused.Value = v
	case nil:
	default:
		d.Focused.Value = fmt.Sprint(v)
	}
	return d
}

type routeDTO struct {
	Scope  *auth.ResourceScope `json:"scope,omitempty"`
	Params map[string]string   `json:"params"`
	Query  map[string]string   `json:"query"`
	Body   json.RawMessage     `json:"body,omitempty"`
	Viewer *viewerDTO          `json:"viewer,omitempty"`
}

type viewerDTO struct {
	User   userDTO    `json:"user"`
	Member *memberDTO `json:"member,omitempty"`
}

// TaskDelivery is stable across retries except for the increasing attempt.
type TaskDelivery struct {
	Interaction *deferredInfo `json:"interaction,omitempty"`
	ID          string        `json:"id"`
	Attempt     int           `json:"attempt"`
}
type payloadDTO struct {
	Task    *TaskDelivery   `json:"task,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ───────────────────────────────────────────────────────────── JS → Go: messages

// messageInput mirrors MessagePayload / ReplyInput of cicord-sdk.d.ts.
type messageInput struct {
	Layout          []compIn      `json:"layout"`
	Nonce           string        `json:"nonce"`
	EnforceNonce    bool          `json:"enforceNonce"`
	Files           []fileInput   `json:"files"`
	Content         *string       `json:"content"`
	Embeds          []embedIn     `json:"embeds"`
	Components      []rowIn       `json:"components"`
	AllowedMentions *mentionsIn   `json:"allowedMentions"`
	Ephemeral       *bool         `json:"ephemeral"`
	Poll            *discord.Poll `json:"poll"`
}

type mentionsIn struct {
	Users       mentionList `json:"users"`
	Roles       mentionList `json:"roles"`
	Everyone    bool        `json:"everyone"`
	RepliedUser bool        `json:"repliedUser"`
}

// mentionList is `boolean | Snowflake[]` (allowedMentions.users / .roles).
type mentionList struct {
	all bool
	ids []string // nil unless a list was given
}

func (l *mentionList) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if err := json.Unmarshal(b, &l.all); err == nil {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return argErr("allowedMentions: users and roles must be a boolean or an array of ids")
	}
	l.ids = append([]string{}, ids...)
	return nil
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
	Components    []compIn                `json:"components"`
	Accessory     *compIn                 `json:"accessory"`
	Component     *compIn                 `json:"component"`
	Content       string                  `json:"content"`
	Description   string                  `json:"description"`
	Media         *discord.ComponentMedia `json:"media"`
	File          *discord.ComponentMedia `json:"file"`
	Items         []discord.GalleryItem   `json:"items"`
	Divider       *bool                   `json:"divider"`
	Spacing       int                     `json:"spacing"`
	AccentColor   string                  `json:"accentColor"`
	Spoiler       bool                    `json:"spoiler"`
	DefaultValues []discord.SelectDefault `json:"defaultValues"`
	ChannelTypes  []string                `json:"channelTypes"`
	Required      *bool                   `json:"required"`
	Value         string                  `json:"value"`
	MinLength     *int                    `json:"minLength"`
	MaxLength     *int                    `json:"maxLength"`
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Label         string                  `json:"label"`
	Emoji         string                  `json:"emoji"`
	Style         string                  `json:"style"`
	URL           string                  `json:"url"`
	Disabled      bool                    `json:"disabled"`
	Placeholder   string                  `json:"placeholder"`
	MinValues     *int                    `json:"minValues"`
	MaxValues     *int                    `json:"maxValues"`
	Options       []struct {
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
	v, err := strconv.ParseUint(h, 16, 24) // 6 hex digits: fits an int on any platform
	if err != nil {
		return 0, fmt.Errorf("invalid color %q (expected #RRGGBB)", s)
	}
	return int(v), nil
}

// messageCreate converts a message input. allowEphemeral is true for interaction replies.
func (i *Instance) messageCreate(in messageInput, allowEphemeral bool) (discord.MessageCreate, error) {
	var mc discord.MessageCreate
	if utf8.RuneCountInString(in.Nonce) > 25 || (in.EnforceNonce && in.Nonce == "") {
		return mc, argErr("nonce must be 1–25 characters when enforced")
	}
	if in.Nonce != "" {
		mc.Nonce = in.Nonce
		mc.EnforceNonce = in.EnforceNonce
	}
	var err error
	mc.Files, err = i.messageFiles(in.Files)
	if err != nil {
		return mc, err
	}
	if in.Poll != nil {
		if err := in.Poll.Validate(); err != nil {
			return mc, argErr("%s", err)
		}
		mc.Poll = in.Poll
	}
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
	if in.Layout != nil {
		if in.Content != nil || in.Embeds != nil || in.Components != nil {
			return mc, argErr("layout is exclusive with content, embeds and components")
		}
		if len(in.Layout) == 0 {
			return mc, argErr("layout must contain a component")
		}
		count := 0
		for _, c := range in.Layout {
			out, err := i.layoutComponent(c, "message", &count)
			if err != nil {
				return mc, argErr("layout: %v", err)
			}
			mc.Components = append(mc.Components, out)
		}
		mc.Flags |= discord.MessageFlagComponentsV2
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
	if am := in.AllowedMentions; am != nil {
		mc.AllowedMentions = &discord.AllowedMentions{
			Users: am.Users.all, Roles: am.Roles.all, Everyone: am.Everyone,
			UserIDs: am.Users.ids, RoleIDs: am.Roles.ids, RepliedUser: am.RepliedUser,
		}
		if err := mc.AllowedMentions.Validate(); err != nil {
			return mc, argErr("%s", strings.TrimPrefix(err.Error(), discord.ErrBadRequest.Error()+": "))
		}
	}
	if mc.Poll == nil && mc.Content == "" && len(mc.Embeds) == 0 && len(mc.Components) == 0 && len(mc.Files) == 0 {
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
		case "userSelect", "roleSelect", "channelSelect", "mentionableSelect":
			if len(r.Components) != 1 {
				return row, fmt.Errorf("a select menu must be alone in its row")
			}
			var err error
			comp, err = i.entitySelect(c, false)
			if err != nil {
				return row, err
			}
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
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Fields []compIn `json:"fields"`
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
	ids := map[string]bool{}
	for k, f := range in.Fields {
		fieldID := f.ID
		if f.Type == "label" && f.Component != nil {
			fieldID = f.Component.ID
		}
		if ids[fieldID] {
			return m, argErr("duplicate modal field %q", fieldID)
		}
		ids[fieldID] = true
		if f.Type == "label" {
			component, err := i.modalLabel(f)
			if err != nil {
				return m, argErr("fields[%d]: %v", k, err)
			}
			m.Components = append(m.Components, component)
			continue
		}
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
