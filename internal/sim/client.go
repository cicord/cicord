package sim

import (
	"context"
	"sort"

	"github.com/cicord/cicord/internal/discord"
)

// Client is a discord.Client acting as one user. Bot sessions embed it; UserClient
// returns one for a human user (who then has the permissions of that user, the owner
// bypassing everything). Methods that only make sense for applications (commands,
// interactions) fail with ErrForbidden for humans.
type Client struct {
	w *World
	a actor
}

// UserClient returns a discord.Client acting as userID, e.g. a moderator clicking in
// the Discord client. Its mutating calls are recorded in the call log under userID.
func (w *World) UserClient(userID string) *Client {
	return &Client{w: w, a: actor{userID: userID}}
}

// AdminClient returns a discord.Client acting as the owner of serverID.
func (w *World) AdminClient(serverID string) *Client {
	w.mu.Lock()
	defer w.mu.Unlock()
	owner := ""
	if s := w.servers[serverID]; s != nil {
		owner = s.info.OwnerID
	}
	return &Client{w: w, a: actor{userID: owner}}
}

func (c *Client) GetUser(ctx context.Context, userID string) (discord.User, error) {
	if err := ctx.Err(); err != nil {
		return discord.User{}, err
	}
	return c.w.getUser(userID)
}

func (c *Client) UpdateSelf(ctx context.Context, p discord.SelfParams) (discord.User, error) {
	if err := ctx.Err(); err != nil {
		return discord.User{}, err
	}
	return c.w.updateSelf(c.actorFor(ctx), p)
}

func (c *Client) GetServer(ctx context.Context, serverID string) (discord.Server, error) {
	if err := ctx.Err(); err != nil {
		return discord.Server{}, err
	}
	return c.w.getServer(c.actorFor(ctx), serverID)
}

func (c *Client) UpdateServer(ctx context.Context, serverID string, p discord.ServerParams) (discord.Server, error) {
	if err := ctx.Err(); err != nil {
		return discord.Server{}, err
	}
	return c.w.updateServer(c.actorFor(ctx), serverID, p)
}

func (c *Client) ListRoles(ctx context.Context, serverID string) ([]discord.Role, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listRoles(c.actorFor(ctx), serverID)
}

func (c *Client) CreateRole(ctx context.Context, serverID string, p discord.RoleParams, reason string) (discord.Role, error) {
	if err := ctx.Err(); err != nil {
		return discord.Role{}, err
	}
	return c.w.createRole(c.actorFor(ctx), serverID, p, reason)
}

func (c *Client) UpdateRole(ctx context.Context, serverID, roleID string, p discord.RoleParams, reason string) (discord.Role, error) {
	if err := ctx.Err(); err != nil {
		return discord.Role{}, err
	}
	return c.w.updateRole(c.actorFor(ctx), serverID, roleID, p, reason)
}

func (c *Client) DeleteRole(ctx context.Context, serverID, roleID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteRole(c.actorFor(ctx), serverID, roleID, reason)
}

func (c *Client) SetRolePositions(ctx context.Context, serverID string, positions []discord.RolePosition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.setRolePositions(c.actorFor(ctx), serverID, positions)
}

func (c *Client) ListChannels(ctx context.Context, serverID string) ([]discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listChannels(c.actorFor(ctx), serverID)
}

func (c *Client) GetChannel(ctx context.Context, channelID string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.getChannel(c.actorFor(ctx), channelID)
}

func (c *Client) CreateChannel(ctx context.Context, serverID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.createChannel(c.actorFor(ctx), serverID, p, reason)
}

func (c *Client) UpdateChannel(ctx context.Context, channelID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.updateChannel(c.actorFor(ctx), channelID, p, reason)
}

func (c *Client) DeleteChannel(ctx context.Context, channelID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteChannel(c.actorFor(ctx), channelID, reason)
}

func (c *Client) SetChannelPositions(ctx context.Context, serverID string, positions []discord.ChannelPosition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.setChannelPositions(c.actorFor(ctx), serverID, positions)
}

func (c *Client) CreateThread(ctx context.Context, channelID string, p discord.ThreadParams) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.createThread(c.actorFor(ctx), channelID, p)
}

func (c *Client) GetMember(ctx context.Context, serverID, userID string) (discord.Member, error) {
	if err := ctx.Err(); err != nil {
		return discord.Member{}, err
	}
	return c.w.getMember(c.actorFor(ctx), serverID, userID)
}

// ListMembers lists every member of a server (discord.MemberLister).
func (c *Client) ListMembers(ctx context.Context, serverID string) ([]discord.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listMembers(c.actorFor(ctx), serverID)
}

func (c *Client) UpdateMember(ctx context.Context, serverID, userID string, p discord.MemberParams, reason string) (discord.Member, error) {
	if err := ctx.Err(); err != nil {
		return discord.Member{}, err
	}
	return c.w.updateMember(c.actorFor(ctx), serverID, userID, p, reason)
}

func (c *Client) AddMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.memberRole(c.actorFor(ctx), true, serverID, userID, roleID, reason)
}

func (c *Client) RemoveMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.memberRole(c.actorFor(ctx), false, serverID, userID, roleID, reason)
}

func (c *Client) KickMember(ctx context.Context, serverID, userID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.kickMember(c.actorFor(ctx), serverID, userID, reason)
}

func (c *Client) UnbanMember(ctx context.Context, serverID, userID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.unbanMember(c.actorFor(ctx), serverID, userID, reason)
}
func (c *Client) ListAuditLog(ctx context.Context, id string, q discord.AuditQuery) ([]discord.AuditLogEntryEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	server, err := c.w.server(id)
	if err != nil {
		return nil, err
	}
	if err := c.w.requireServer(c.actorFor(ctx), server, permViewAuditLog); err != nil {
		return nil, err
	}
	if q.Limit < 1 || q.Limit > 100 {
		return nil, badRequest("invalid audit limit")
	}
	out := []discord.AuditLogEntryEvent{}
	for j := len(server.audit) - 1; j >= 0 && len(out) < q.Limit; j-- {
		e := server.audit[j]
		if (q.Before == "" || discord.SnowflakeLess(e.ID, q.Before)) && (q.ActorID == "" || e.UserID == q.ActorID) && (q.Action == 0 || e.ActionType == q.Action) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (c *Client) ListBans(ctx context.Context, serverID, after string, limit int) ([]discord.Ban, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	s, err := c.w.server(serverID)
	if err != nil {
		return nil, err
	}
	if err := c.w.requireServer(c.actorFor(ctx), s, permBan); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		return nil, badRequest("invalid ban limit")
	}
	ids := sortedKeys(s.bans)
	sort.Slice(ids, func(i, j int) bool { return discord.SnowflakeLess(ids[i], ids[j]) })
	result := []discord.Ban{}
	for _, id := range ids {
		if after != "" && !discord.SnowflakeLess(after, id) {
			continue
		}
		result = append(result, s.bans[id])
		if len(result) == limit {
			break
		}
	}
	return result, nil
}
func (c *Client) BanMember(ctx context.Context, serverID, userID string, deleteMessageSeconds int, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.banMember(c.actorFor(ctx), serverID, userID, deleteMessageSeconds, reason)
}

func (c *Client) CreateDM(ctx context.Context, userID string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	if !c.a.isBot() {
		return discord.Channel{}, forbidden("DMs between humans are not simulated")
	}
	return c.w.createDM(c.actorFor(ctx), userID)
}

func (c *Client) SendMessage(ctx context.Context, channelID string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.sendMessage(c.actorFor(ctx), channelID, m)
}

func (c *Client) EditMessage(ctx context.Context, channelID, messageID string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.editMessageCall(c.actorFor(ctx), channelID, messageID, m)
}

func (c *Client) BulkDeleteMessages(ctx context.Context, channelID string, ids []string, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.bulkDeleteMessages(c.actorFor(ctx), channelID, ids, reason)
}
func (c *Client) DeleteMessage(ctx context.Context, channelID, messageID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteMessage(c.actorFor(ctx), channelID, messageID, reason)
}

func (c *Client) ListMessages(ctx context.Context, channelID string, p discord.ListMessagesParams) ([]discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listMessages(c.actorFor(ctx), channelID, p)
}

func (c *Client) GetMessage(ctx context.Context, channelID, messageID string) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.getMessage(c.actorFor(ctx), channelID, messageID)
}

func (c *Client) AddReaction(ctx context.Context, channelID, messageID, emoji string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.react(c.actorFor(ctx), true, channelID, messageID, emoji, true)
}

func (c *Client) RemoveOwnReaction(ctx context.Context, channelID, messageID, emoji string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.react(c.actorFor(ctx), false, channelID, messageID, emoji, true)
}

func (c *Client) SetServerCommands(ctx context.Context, applicationID, serverID string, cmds []discord.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.setServerCommands(c.actorFor(ctx), applicationID, serverID, cmds)
}

func (c *Client) RespondInteraction(ctx context.Context, interactionID, token string, r discord.InteractionResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.respondInteraction(c.actorFor(ctx), interactionID, token, r)
}

func (c *Client) EditOriginalResponse(ctx context.Context, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.editOriginalResponse(c.actorFor(ctx), applicationID, token, m)
}

func (c *Client) CreateFollowup(ctx context.Context, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.createFollowup(c.actorFor(ctx), applicationID, token, m)
}

// SetPresence stores the bot's presence (see World.Presence).
func (s *Session) SetPresence(ctx context.Context, p discord.Presence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.w.setPresence(s.a, p)
}

func (c *Client) actorFor(ctx context.Context) actor {
	a := c.a
	if discord.Operations(ctx) != nil {
		a.operationContext = ctx
	}
	return a
}
