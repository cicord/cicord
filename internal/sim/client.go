package sim

import (
	"context"

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
	return c.w.updateSelf(c.a, p)
}

func (c *Client) GetServer(ctx context.Context, serverID string) (discord.Server, error) {
	if err := ctx.Err(); err != nil {
		return discord.Server{}, err
	}
	return c.w.getServer(c.a, serverID)
}

func (c *Client) UpdateServer(ctx context.Context, serverID string, p discord.ServerParams) (discord.Server, error) {
	if err := ctx.Err(); err != nil {
		return discord.Server{}, err
	}
	return c.w.updateServer(c.a, serverID, p)
}

func (c *Client) ListRoles(ctx context.Context, serverID string) ([]discord.Role, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listRoles(c.a, serverID)
}

func (c *Client) CreateRole(ctx context.Context, serverID string, p discord.RoleParams, reason string) (discord.Role, error) {
	if err := ctx.Err(); err != nil {
		return discord.Role{}, err
	}
	return c.w.createRole(c.a, serverID, p, reason)
}

func (c *Client) UpdateRole(ctx context.Context, serverID, roleID string, p discord.RoleParams, reason string) (discord.Role, error) {
	if err := ctx.Err(); err != nil {
		return discord.Role{}, err
	}
	return c.w.updateRole(c.a, serverID, roleID, p, reason)
}

func (c *Client) DeleteRole(ctx context.Context, serverID, roleID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteRole(c.a, serverID, roleID, reason)
}

func (c *Client) SetRolePositions(ctx context.Context, serverID string, positions []discord.RolePosition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.setRolePositions(c.a, serverID, positions)
}

func (c *Client) ListChannels(ctx context.Context, serverID string) ([]discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listChannels(c.a, serverID)
}

func (c *Client) GetChannel(ctx context.Context, channelID string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.getChannel(c.a, channelID)
}

func (c *Client) CreateChannel(ctx context.Context, serverID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.createChannel(c.a, serverID, p, reason)
}

func (c *Client) UpdateChannel(ctx context.Context, channelID string, p discord.ChannelParams, reason string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.updateChannel(c.a, channelID, p, reason)
}

func (c *Client) DeleteChannel(ctx context.Context, channelID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteChannel(c.a, channelID, reason)
}

func (c *Client) SetChannelPositions(ctx context.Context, serverID string, positions []discord.ChannelPosition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.setChannelPositions(c.a, serverID, positions)
}

func (c *Client) CreateThread(ctx context.Context, channelID string, p discord.ThreadParams) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	return c.w.createThread(c.a, channelID, p)
}

func (c *Client) GetMember(ctx context.Context, serverID, userID string) (discord.Member, error) {
	if err := ctx.Err(); err != nil {
		return discord.Member{}, err
	}
	return c.w.getMember(c.a, serverID, userID)
}

func (c *Client) UpdateMember(ctx context.Context, serverID, userID string, p discord.MemberParams, reason string) (discord.Member, error) {
	if err := ctx.Err(); err != nil {
		return discord.Member{}, err
	}
	return c.w.updateMember(c.a, serverID, userID, p, reason)
}

func (c *Client) AddMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.memberRole(c.a, true, serverID, userID, roleID, reason)
}

func (c *Client) RemoveMemberRole(ctx context.Context, serverID, userID, roleID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.memberRole(c.a, false, serverID, userID, roleID, reason)
}

func (c *Client) KickMember(ctx context.Context, serverID, userID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.kickMember(c.a, serverID, userID, reason)
}

func (c *Client) BanMember(ctx context.Context, serverID, userID string, deleteMessageSeconds int, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.banMember(c.a, serverID, userID, deleteMessageSeconds, reason)
}

func (c *Client) CreateDM(ctx context.Context, userID string) (discord.Channel, error) {
	if err := ctx.Err(); err != nil {
		return discord.Channel{}, err
	}
	if !c.a.isBot() {
		return discord.Channel{}, forbidden("DMs between humans are not simulated")
	}
	return c.w.createDM(c.a, userID)
}

func (c *Client) SendMessage(ctx context.Context, channelID string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.sendMessage(c.a, channelID, m)
}

func (c *Client) EditMessage(ctx context.Context, channelID, messageID string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.editMessageCall(c.a, channelID, messageID, m)
}

func (c *Client) DeleteMessage(ctx context.Context, channelID, messageID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.deleteMessage(c.a, channelID, messageID, reason)
}

func (c *Client) ListMessages(ctx context.Context, channelID string, p discord.ListMessagesParams) ([]discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.w.listMessages(c.a, channelID, p)
}

func (c *Client) GetMessage(ctx context.Context, channelID, messageID string) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.getMessage(c.a, channelID, messageID)
}

func (c *Client) AddReaction(ctx context.Context, channelID, messageID, emoji string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.react(c.a, true, channelID, messageID, emoji, true)
}

func (c *Client) RemoveOwnReaction(ctx context.Context, channelID, messageID, emoji string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.react(c.a, false, channelID, messageID, emoji, true)
}

func (c *Client) SetServerCommands(ctx context.Context, applicationID, serverID string, cmds []discord.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.setServerCommands(c.a, applicationID, serverID, cmds)
}

func (c *Client) RespondInteraction(ctx context.Context, interactionID, token string, r discord.InteractionResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.w.respondInteraction(c.a, interactionID, token, r)
}

func (c *Client) EditOriginalResponse(ctx context.Context, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.editOriginalResponse(c.a, applicationID, token, m)
}

func (c *Client) CreateFollowup(ctx context.Context, applicationID, token string, m discord.MessageCreate) (discord.Message, error) {
	if err := ctx.Err(); err != nil {
		return discord.Message{}, err
	}
	return c.w.createFollowup(c.a, applicationID, token, m)
}

// SetPresence stores the bot's presence (see World.Presence).
func (s *Session) SetPresence(ctx context.Context, p discord.Presence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.w.setPresence(s.a, p)
}
