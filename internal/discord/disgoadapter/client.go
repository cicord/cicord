package disgoadapter

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/rest"

	"github.com/cicord/cicord/internal/discord"
)

// checkIDs rejects IDs that are not snowflakes before they are spliced into a URL.
func checkIDs(op string, ids ...string) error {
	for _, id := range ids {
		if !isSnowflake(id) {
			return fmt.Errorf("discord %s: %w: invalid id %q", op, discord.ErrBadRequest, id)
		}
	}
	return nil
}

func checkToken(op, token string) error {
	if token == "" {
		return fmt.Errorf("discord %s: %w: empty interaction token", op, discord.ErrBadRequest)
	}
	return nil
}

// ───────────────────────────────────────────────────────────── users

func (s *session) GetUser(ctx context.Context, userID string) (discord.User, error) {
	if err := checkIDs("GetUser", userID); err != nil {
		return discord.User{}, err
	}
	var u wireUser
	if err := s.do(ctx, "GetUser", rest.GetUser.Compile(nil, userID), nil, &u); err != nil {
		return discord.User{}, err
	}
	return userFromWire(u), nil
}

func (s *session) UpdateSelf(ctx context.Context, p discord.SelfParams) (discord.User, error) {
	body := map[string]any{}
	if p.Username != nil {
		body["username"] = *p.Username
	}
	if p.Avatar != nil {
		body["avatar"] = nullIfEmpty(imageDataURI(*p.Avatar))
	}
	if p.Banner != nil {
		body["banner"] = nullIfEmpty(imageDataURI(*p.Banner))
	}
	var u wireUser
	if err := s.do(ctx, "UpdateSelf", rest.UpdateCurrentUser.Compile(nil), body, &u); err != nil {
		return discord.User{}, err
	}
	self := userFromWire(u)
	s.mu.Lock()
	s.self = self
	s.mu.Unlock()
	return self, nil
}

// ───────────────────────────────────────────────────────────── servers

func (s *session) GetServer(ctx context.Context, serverID string) (discord.Server, error) {
	if err := checkIDs("GetServer", serverID); err != nil {
		return discord.Server{}, err
	}
	var g wireGuild
	if err := s.do(ctx, "GetServer", rest.GetGuild.Compile(nil, serverID), nil, &g); err != nil {
		return discord.Server{}, err
	}
	return serverFromWire(g), nil
}

func (s *session) UpdateServer(ctx context.Context, serverID string, p discord.ServerParams) (discord.Server, error) {
	if err := checkIDs("UpdateServer", serverID); err != nil {
		return discord.Server{}, err
	}
	body, err := serverParamsBody(p)
	if err != nil {
		return discord.Server{}, fmt.Errorf("discord UpdateServer: %w", err)
	}
	var g wireGuild
	if err := s.do(ctx, "UpdateServer", rest.UpdateGuild.Compile(nil, serverID), body, &g); err != nil {
		return discord.Server{}, err
	}
	return serverFromWire(g), nil
}

// ───────────────────────────────────────────────────────────── roles

func (s *session) ListRoles(ctx context.Context, serverID string) ([]discord.Role, error) {
	if err := checkIDs("ListRoles", serverID); err != nil {
		return nil, err
	}
	var rs []wireRole
	if err := s.do(ctx, "ListRoles", rest.GetRoles.Compile(nil, serverID), nil, &rs); err != nil {
		return nil, err
	}
	out := make([]discord.Role, 0, len(rs))
	for _, r := range rs {
		out = append(out, roleFromWire(r))
	}
	normalizeRolePositions(serverID, out)
	return out, nil
}

// normalizeRolePositions rewrites positions as Discord's effective hierarchy rank.
// Discord lets several roles share a position (seen live: every role at 1 after
// a drag in the client) and then ranks the older role (lower ID) higher. The rest
// of Cicord compares positions numerically, so ties are broken here, once: @everyone
// (ID = server ID) stays 0 and the others get distinct ranks 1..n in Discord's order.
func normalizeRolePositions(serverID string, roles []discord.Role) {
	idx := make([]int, 0, len(roles))
	for i, r := range roles {
		if r.ID != serverID {
			idx = append(idx, i)
		}
	}
	slices.SortFunc(idx, func(a, b int) int {
		ra, rb := roles[a], roles[b]
		if ra.Position != rb.Position {
			return cmp.Compare(ra.Position, rb.Position)
		}
		return snowflakeCmp(rb.ID, ra.ID) // tie: the higher ID ranks lower
	})
	for rank, i := range idx {
		roles[i].Position = rank + 1
	}
}

// snowflakeCmp orders snowflakes numerically (shorter strings are smaller).
func snowflakeCmp(a, b string) int {
	if len(a) != len(b) {
		return cmp.Compare(len(a), len(b))
	}
	return cmp.Compare(a, b)
}

func (s *session) CreateRole(ctx context.Context, serverID string, p discord.RoleParams, reason string) (discord.Role, error) {
	if err := checkIDs("CreateRole", serverID); err != nil {
		return discord.Role{}, err
	}
	var r wireRole
	if err := s.do(ctx, "CreateRole", rest.CreateRole.Compile(nil, serverID), roleParamsBody(p), &r, reason); err != nil {
		return discord.Role{}, err
	}
	return roleFromWire(r), nil
}

func (s *session) UpdateRole(ctx context.Context, serverID, roleID string, p discord.RoleParams, reason string) (discord.Role, error) {
	if err := checkIDs("UpdateRole", serverID, roleID); err != nil {
		return discord.Role{}, err
	}
	var r wireRole
	if err := s.do(ctx, "UpdateRole", rest.UpdateRole.Compile(nil, serverID, roleID), roleParamsBody(p), &r, reason); err != nil {
		return discord.Role{}, err
	}
	return roleFromWire(r), nil
}

func (s *session) DeleteRole(ctx context.Context, serverID, roleID, reason string) error {
	if err := checkIDs("DeleteRole", serverID, roleID); err != nil {
		return err
	}
	return s.do(ctx, "DeleteRole", rest.DeleteRole.Compile(nil, serverID, roleID), nil, nil, reason)
}

func (s *session) SetRolePositions(ctx context.Context, serverID string, positions []discord.RolePosition) error {
	if err := checkIDs("SetRolePositions", serverID); err != nil {
		return err
	}
	body := make([]map[string]any, 0, len(positions))
	for _, p := range positions {
		if err := checkIDs("SetRolePositions", p.ID); err != nil {
			return err
		}
		body = append(body, map[string]any{"id": p.ID, "position": p.Position})
	}
	return s.do(ctx, "SetRolePositions", rest.UpdateRolePositions.Compile(nil, serverID), body, nil)
}

// ───────────────────────────────────────────────────────────── channels

func (s *session) ListChannels(ctx context.Context, serverID string) ([]discord.Channel, error) {
	if err := checkIDs("ListChannels", serverID); err != nil {
		return nil, err
	}
	var cs []wireChannel
	if err := s.do(ctx, "ListChannels", rest.GetGuildChannels.Compile(nil, serverID), nil, &cs); err != nil {
		return nil, err
	}
	out := make([]discord.Channel, 0, len(cs))
	for _, c := range cs {
		if c.GuildID == "" {
			c.GuildID = serverID
		}
		out = append(out, channelFromWire(c))
	}
	return out, nil
}

func (s *session) GetChannel(ctx context.Context, channelID string) (discord.Channel, error) {
	if err := checkIDs("GetChannel", channelID); err != nil {
		return discord.Channel{}, err
	}
	var c wireChannel
	if err := s.do(ctx, "GetChannel", rest.GetChannel.Compile(nil, channelID), nil, &c); err != nil {
		return discord.Channel{}, err
	}
	return channelFromWire(c), nil
}

func (s *session) CreateChannel(ctx context.Context, serverID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	if err := checkIDs("CreateChannel", serverID); err != nil {
		return discord.Channel{}, err
	}
	body, err := channelCreateBody(p)
	if err != nil {
		return discord.Channel{}, fmt.Errorf("discord CreateChannel: %w", err)
	}
	var c wireChannel
	if err := s.do(ctx, "CreateChannel", rest.CreateGuildChannel.Compile(nil, serverID), body, &c, reason); err != nil {
		return discord.Channel{}, err
	}
	if c.GuildID == "" {
		c.GuildID = serverID
	}
	return channelFromWire(c), nil
}

// UpdateChannel modifies a channel. Discord's Modify Channel has no "sync with
// category" flag, so LockPermissions=true is implemented by copying the parent
// category's overwrites (the parent being p.ParentID when set, else the current
// one) into permission_overwrites, which is exactly what Discord considers synced.
// LockPermissions=false is a no-op.
func (s *session) UpdateChannel(ctx context.Context, channelID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	if err := checkIDs("UpdateChannel", channelID); err != nil {
		return discord.Channel{}, err
	}
	body, err := channelUpdateBody(p)
	if err != nil {
		return discord.Channel{}, fmt.Errorf("discord UpdateChannel: %w", err)
	}
	if p.LockPermissions != nil && *p.LockPermissions {
		parentID := ""
		if p.ParentID != nil {
			parentID = *p.ParentID
		} else {
			cur, err := s.GetChannel(ctx, channelID)
			if err != nil {
				return discord.Channel{}, err
			}
			parentID = cur.ParentID
		}
		if parentID != "" {
			parent, err := s.GetChannel(ctx, parentID)
			if err != nil {
				return discord.Channel{}, err
			}
			body["permission_overwrites"] = overwritesToWire(parent.Overwrites)
		}
	}
	var c wireChannel
	if err := s.do(ctx, "UpdateChannel", rest.UpdateChannel.Compile(nil, channelID), body, &c, reason); err != nil {
		return discord.Channel{}, err
	}
	return channelFromWire(c), nil
}

func (s *session) DeleteChannel(ctx context.Context, channelID, reason string) error {
	if err := checkIDs("DeleteChannel", channelID); err != nil {
		return err
	}
	return s.do(ctx, "DeleteChannel", rest.DeleteChannel.Compile(nil, channelID), nil, nil, reason)
}

func (s *session) SetChannelPositions(ctx context.Context, serverID string, positions []discord.ChannelPosition) error {
	if err := checkIDs("SetChannelPositions", serverID); err != nil {
		return err
	}
	for _, p := range positions {
		if err := checkIDs("SetChannelPositions", p.ID); err != nil {
			return err
		}
	}
	return s.do(ctx, "SetChannelPositions", rest.UpdateChannelPositions.Compile(nil, serverID), channelPositionsBody(positions), nil)
}

func (s *session) CreateThread(ctx context.Context, channelID string, p discord.ThreadParams) (discord.Channel, error) {
	if err := checkIDs("CreateThread", channelID); err != nil {
		return discord.Channel{}, err
	}
	if p.Name == "" {
		return discord.Channel{}, fmt.Errorf("discord CreateThread: %w: thread name is required", discord.ErrBadRequest)
	}
	var c wireChannel
	if p.MessageID != "" {
		if p.Private {
			return discord.Channel{}, fmt.Errorf("discord CreateThread: %w: private threads cannot start from a message", discord.ErrBadRequest)
		}
		if err := checkIDs("CreateThread", p.MessageID); err != nil {
			return discord.Channel{}, err
		}
		err := s.do(ctx, "CreateThread", rest.CreateThreadWithMessage.Compile(nil, channelID, p.MessageID),
			map[string]any{"name": p.Name}, &c)
		if err != nil {
			return discord.Channel{}, err
		}
		return channelFromWire(c), nil
	}
	typ := discord.ChannelPublicThread
	if p.Private {
		typ = discord.ChannelPrivateThread
	}
	err := s.do(ctx, "CreateThread", rest.CreateThread.Compile(nil, channelID),
		map[string]any{"name": p.Name, "type": int(typ)}, &c)
	if err != nil {
		return discord.Channel{}, err
	}
	return channelFromWire(c), nil
}

// ───────────────────────────────────────────────────────────── members

func (s *session) GetMember(ctx context.Context, serverID, userID string) (discord.Member, error) {
	if err := checkIDs("GetMember", serverID, userID); err != nil {
		return discord.Member{}, err
	}
	var m wireMember
	if err := s.do(ctx, "GetMember", rest.GetMember.Compile(nil, serverID, userID), nil, &m); err != nil {
		return discord.Member{}, err
	}
	return memberFromWire(serverID, m, nil), nil
}

func memberParamsBody(p discord.MemberParams) map[string]any {
	body := map[string]any{}
	if p.Nick != nil {
		body["nick"] = nullIfEmpty(*p.Nick)
	}
	if p.TimeoutUntil != nil {
		if p.TimeoutUntil.IsZero() {
			body["communication_disabled_until"] = nil
		} else {
			body["communication_disabled_until"] = p.TimeoutUntil.UTC().Format(time.RFC3339)
		}
	}
	if p.VoiceChannelID != nil {
		body["channel_id"] = nullIfEmpty(*p.VoiceChannelID) // null disconnects
	}
	return body
}

func (s *session) UpdateMember(ctx context.Context, serverID, userID string, p discord.MemberParams, reason string) (discord.Member, error) {
	if err := checkIDs("UpdateMember", serverID, userID); err != nil {
		return discord.Member{}, err
	}
	var m wireMember
	if err := s.do(ctx, "UpdateMember", rest.UpdateMember.Compile(nil, serverID, userID), memberParamsBody(p), &m, reason); err != nil {
		return discord.Member{}, err
	}
	return memberFromWire(serverID, m, nil), nil
}

func (s *session) AddMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	if err := checkIDs("AddMemberRole", serverID, userID, roleID); err != nil {
		return err
	}
	return s.do(ctx, "AddMemberRole", rest.AddMemberRole.Compile(nil, serverID, userID, roleID), nil, nil, reason)
}

func (s *session) RemoveMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	if err := checkIDs("RemoveMemberRole", serverID, userID, roleID); err != nil {
		return err
	}
	return s.do(ctx, "RemoveMemberRole", rest.RemoveMemberRole.Compile(nil, serverID, userID, roleID), nil, nil, reason)
}

func (s *session) KickMember(ctx context.Context, serverID, userID, reason string) error {
	if err := checkIDs("KickMember", serverID, userID); err != nil {
		return err
	}
	return s.do(ctx, "KickMember", rest.RemoveMember.Compile(nil, serverID, userID), nil, nil, reason)
}

// BanMember bans a user. deleteMessageSeconds is clamped to Discord's 0..604800.
func (s *session) BanMember(ctx context.Context, serverID, userID string, deleteMessageSeconds int, reason string) error {
	if err := checkIDs("BanMember", serverID, userID); err != nil {
		return err
	}
	deleteMessageSeconds = max(0, min(deleteMessageSeconds, 604800))
	body := map[string]any{"delete_message_seconds": deleteMessageSeconds}
	return s.do(ctx, "BanMember", rest.AddBan.Compile(nil, serverID, userID), body, nil, reason)
}

func (s *session) CreateDM(ctx context.Context, userID string) (discord.Channel, error) {
	if err := checkIDs("CreateDM", userID); err != nil {
		return discord.Channel{}, err
	}
	var c wireChannel
	if err := s.do(ctx, "CreateDM", rest.CreateDMChannel.Compile(nil), map[string]any{"recipient_id": userID}, &c); err != nil {
		return discord.Channel{}, err
	}
	return channelFromWire(c), nil
}

// ───────────────────────────────────────────────────────────── messages

func (s *session) SendMessage(ctx context.Context, channelID string, m discord.MessageCreate) (discord.Message, error) {
	if err := checkIDs("SendMessage", channelID); err != nil {
		return discord.Message{}, err
	}
	if m.ReplyTo != "" {
		if err := checkIDs("SendMessage", m.ReplyTo); err != nil {
			return discord.Message{}, err
		}
	}
	var out wireMessage
	if err := s.do(ctx, "SendMessage", rest.CreateMessage.Compile(nil, channelID), messageSendToWire(m), &out); err != nil {
		return discord.Message{}, err
	}
	return messageFromWire(out), nil
}

func (s *session) EditMessage(ctx context.Context, channelID, messageID string, m discord.MessageCreate) (discord.Message, error) {
	if err := checkIDs("EditMessage", channelID, messageID); err != nil {
		return discord.Message{}, err
	}
	var out wireMessage
	if err := s.do(ctx, "EditMessage", rest.UpdateMessage.Compile(nil, channelID, messageID), messageEditToWire(m), &out); err != nil {
		return discord.Message{}, err
	}
	return messageFromWire(out), nil
}

func (s *session) DeleteMessage(ctx context.Context, channelID, messageID, reason string) error {
	if err := checkIDs("DeleteMessage", channelID, messageID); err != nil {
		return err
	}
	return s.do(ctx, "DeleteMessage", rest.DeleteMessage.Compile(nil, channelID, messageID), nil, nil, reason)
}

func (s *session) ListMessages(ctx context.Context, channelID string, p discord.ListMessagesParams) ([]discord.Message, error) {
	if err := checkIDs("ListMessages", channelID); err != nil {
		return nil, err
	}
	if p.Before != "" && p.After != "" {
		// Discord accepts only one of before/after/around.
		return nil, fmt.Errorf("discord ListMessages: %w: before and after are mutually exclusive", discord.ErrBadRequest)
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 100)
	q := map[string]any{"limit": strconv.Itoa(limit)}
	if p.Before != "" {
		if err := checkIDs("ListMessages", p.Before); err != nil {
			return nil, err
		}
		q["before"] = p.Before
	}
	if p.After != "" {
		if err := checkIDs("ListMessages", p.After); err != nil {
			return nil, err
		}
		q["after"] = p.After
	}
	var ms []wireMessage
	if err := s.do(ctx, "ListMessages", rest.GetMessages.Compile(q, channelID), nil, &ms); err != nil {
		return nil, err
	}
	out := make([]discord.Message, 0, len(ms))
	for _, m := range ms {
		out = append(out, messageFromWire(m))
	}
	return out, nil
}

func (s *session) GetMessage(ctx context.Context, channelID, messageID string) (discord.Message, error) {
	if err := checkIDs("GetMessage", channelID, messageID); err != nil {
		return discord.Message{}, err
	}
	var m wireMessage
	if err := s.do(ctx, "GetMessage", rest.GetMessage.Compile(nil, channelID, messageID), nil, &m); err != nil {
		return discord.Message{}, err
	}
	return messageFromWire(m), nil
}

func (s *session) AddReaction(ctx context.Context, channelID, messageID, emoji string) error {
	if err := checkIDs("AddReaction", channelID, messageID); err != nil {
		return err
	}
	e, err := reactionEmojiPath(emoji)
	if err != nil {
		return fmt.Errorf("discord AddReaction: %w", err)
	}
	return s.do(ctx, "AddReaction", rest.AddReaction.Compile(nil, channelID, messageID, e), nil, nil)
}

func (s *session) RemoveOwnReaction(ctx context.Context, channelID, messageID, emoji string) error {
	if err := checkIDs("RemoveOwnReaction", channelID, messageID); err != nil {
		return err
	}
	e, err := reactionEmojiPath(emoji)
	if err != nil {
		return fmt.Errorf("discord RemoveOwnReaction: %w", err)
	}
	return s.do(ctx, "RemoveOwnReaction", rest.RemoveOwnReaction.Compile(nil, channelID, messageID, e), nil, nil)
}

// ───────────────────────────────────────────────────────────── commands & interactions

func (s *session) SetServerCommands(ctx context.Context, applicationID, serverID string, cmds []discord.Command) error {
	if err := checkIDs("SetServerCommands", applicationID, serverID); err != nil {
		return err
	}
	return s.do(ctx, "SetServerCommands", rest.SetGuildCommands.Compile(nil, applicationID, serverID), commandsToWire(cmds), nil)
}

func (s *session) RespondInteraction(ctx context.Context, interactionID, token string, r discord.InteractionResponse) error {
	if err := checkIDs("RespondInteraction", interactionID); err != nil {
		return err
	}
	if err := checkToken("RespondInteraction", token); err != nil {
		return err
	}
	body, err := interactionResponseToWire(r)
	if err != nil {
		return fmt.Errorf("discord RespondInteraction: %w", err)
	}
	return s.do(ctx, "RespondInteraction", rest.CreateInteractionResponse.Compile(nil, interactionID, pathEscape(token)), body, nil)
}

func (s *session) EditOriginalResponse(ctx context.Context, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := checkIDs("EditOriginalResponse", applicationID); err != nil {
		return discord.Message{}, err
	}
	if err := checkToken("EditOriginalResponse", token); err != nil {
		return discord.Message{}, err
	}
	var out wireMessage
	err := s.do(ctx, "EditOriginalResponse", rest.UpdateInteractionResponse.Compile(nil, applicationID, pathEscape(token)), messageEditToWire(m), &out)
	if err != nil {
		return discord.Message{}, err
	}
	return messageFromWire(out), nil
}

func (s *session) CreateFollowup(ctx context.Context, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := checkIDs("CreateFollowup", applicationID); err != nil {
		return discord.Message{}, err
	}
	if err := checkToken("CreateFollowup", token); err != nil {
		return discord.Message{}, err
	}
	body := messageSendToWire(m)
	body.MessageReference = nil // not supported on webhook messages
	var out wireMessage
	err := s.do(ctx, "CreateFollowup", rest.CreateFollowupMessage.Compile(nil, applicationID, pathEscape(token)), body, &out)
	if err != nil {
		return discord.Message{}, err
	}
	return messageFromWire(out), nil
}
