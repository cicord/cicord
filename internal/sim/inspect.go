package sim

import (
	"context"
	"slices"

	"github.com/cicord/cicord/internal/discord"
)

// ───────────────────────────────────────────────────────────── admin mutations
//
// Admin* methods act as the server owner in the Discord client: no permission checks,
// gateway events are emitted, and calls are logged under the owner's user ID. They
// are meant for drift tests ("someone renamed the channel by hand").

func (w *World) ownerOf(serverID string) actor {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s := w.servers[serverID]; s != nil {
		return actor{userID: s.info.OwnerID}
	}
	return actor{}
}

func (w *World) ownerOfChannel(channelID string) actor {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c := w.channels[channelID]; c != nil {
		if s := w.servers[c.ch.ServerID]; s != nil {
			return actor{userID: s.info.OwnerID}
		}
	}
	return actor{}
}

// AdminUpdateServer updates server settings as the owner.
func (w *World) AdminUpdateServer(serverID string, p discord.ServerParams) (discord.Server, error) {
	return w.updateServer(w.ownerOf(serverID), serverID, p)
}

// AdminCreateRole creates a role as the owner.
func (w *World) AdminCreateRole(serverID string, p discord.RoleParams) (discord.Role, error) {
	return w.createRole(w.ownerOf(serverID), serverID, p, "")
}

// AdminUpdateRole updates a role as the owner.
func (w *World) AdminUpdateRole(serverID, roleID string, p discord.RoleParams) (discord.Role, error) {
	return w.updateRole(w.ownerOf(serverID), serverID, roleID, p, "")
}

// AdminDeleteRole deletes a role as the owner.
func (w *World) AdminDeleteRole(serverID, roleID string) error {
	return w.deleteRole(w.ownerOf(serverID), serverID, roleID, "")
}

// AdminSetRolePositions reorders roles as the owner.
func (w *World) AdminSetRolePositions(serverID string, positions []discord.RolePosition) error {
	return w.setRolePositions(w.ownerOf(serverID), serverID, positions)
}

// AdminCreateChannel creates a channel as the owner.
func (w *World) AdminCreateChannel(serverID string, p discord.ChannelParams) (discord.Channel, error) {
	return w.createChannel(w.ownerOf(serverID), serverID, p, "")
}

// AdminUpdateChannel updates a channel as the owner.
func (w *World) AdminUpdateChannel(channelID string, p discord.ChannelParams) (discord.Channel, error) {
	return w.updateChannel(w.ownerOfChannel(channelID), channelID, p, "")
}

// AdminDeleteChannel deletes a channel as the owner.
func (w *World) AdminDeleteChannel(channelID string) error {
	return w.deleteChannel(w.ownerOfChannel(channelID), channelID, "")
}

// AdminSetChannelPositions reorders channels as the owner.
func (w *World) AdminSetChannelPositions(serverID string, positions []discord.ChannelPosition) error {
	return w.setChannelPositions(w.ownerOf(serverID), serverID, positions)
}

// AdminAddMemberRole gives a role to a member as the owner.
func (w *World) AdminAddMemberRole(serverID, userID, roleID string) error {
	return w.memberRole(w.ownerOf(serverID), true, serverID, userID, roleID, "")
}

// AdminRemoveMemberRole removes a role from a member as the owner.
func (w *World) AdminRemoveMemberRole(serverID, userID, roleID string) error {
	return w.memberRole(w.ownerOf(serverID), false, serverID, userID, roleID, "")
}

// AdminUpdateMember changes a member's nickname / timeout as the owner.
func (w *World) AdminUpdateMember(serverID, userID string, p discord.MemberParams) (discord.Member, error) {
	return w.updateMember(w.ownerOf(serverID), serverID, userID, p, "")
}

// AdminKickMember kicks a member as the owner.
func (w *World) AdminKickMember(serverID, userID string) error {
	return w.kickMember(w.ownerOf(serverID), serverID, userID, "")
}

// AdminBanMember bans a user as the owner.
func (w *World) AdminBanMember(serverID, userID string) error {
	return w.banMember(w.ownerOf(serverID), serverID, userID, 0, "")
}

// AdminDeleteMessage deletes any message as the owner.
func (w *World) AdminDeleteMessage(channelID, messageID string) error {
	return w.deleteMessage(w.ownerOfChannel(channelID), channelID, messageID, "")
}

// ───────────────────────────────────────────────────────────── inspection

// Server returns a server.
func (w *World) Server(serverID string) (discord.Server, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.servers[serverID]
	if s == nil {
		return discord.Server{}, false
	}
	return cloneServer(s.info), true
}

// Roles returns a server's roles by ascending position (@everyone first).
func (w *World) Roles(serverID string) []discord.Role {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.servers[serverID]
	if s == nil {
		return nil
	}
	return w.sortedRoles(s)
}

// Role returns a role of a server.
func (w *World) Role(serverID, roleID string) (discord.Role, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s := w.servers[serverID]; s != nil {
		if r := s.roles[roleID]; r != nil {
			return *r, true
		}
	}
	return discord.Role{}, false
}

// RoleByName returns the first role with that name.
func (w *World) RoleByName(serverID, name string) (discord.Role, bool) {
	for _, r := range w.Roles(serverID) {
		if r.Name == name {
			return r, true
		}
	}
	return discord.Role{}, false
}

// Channels returns a server's channels (categories included, threads excluded) in
// Discord client order: uncategorized first, then each category and its children.
func (w *World) Channels(serverID string) []discord.Channel {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.servers[serverID]
	if s == nil {
		return nil
	}
	return w.serverChannels(s, false)
}

// Threads returns a server's threads.
func (w *World) Threads(serverID string) []discord.Channel {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.servers[serverID]
	if s == nil {
		return nil
	}
	var out []discord.Channel
	for _, id := range s.channelIDs {
		if c := w.channels[id]; isThread(c.ch.Type) {
			out = append(out, cloneChannel(c.ch))
		}
	}
	return out
}

// Channel returns any channel (server channel, thread or DM).
func (w *World) Channel(channelID string) (discord.Channel, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.channels[channelID]
	if c == nil {
		return discord.Channel{}, false
	}
	return cloneChannel(c.ch), true
}

// ChannelByName returns the first server channel (or category) with that name.
func (w *World) ChannelByName(serverID, name string) (discord.Channel, bool) {
	for _, c := range w.Channels(serverID) {
		if c.Name == name {
			return c, true
		}
	}
	return discord.Channel{}, false
}

// Member returns a server member.
func (w *World) Member(serverID, userID string) (discord.Member, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s := w.servers[serverID]; s != nil {
		if m := s.members[userID]; m != nil {
			return cloneMember(*m), true
		}
	}
	return discord.Member{}, false
}

// Members returns a server's members in join order.
func (w *World) Members(serverID string) []discord.Member {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.servers[serverID]
	if s == nil {
		return nil
	}
	out := make([]discord.Member, 0, len(s.memberOrder))
	for _, id := range s.memberOrder {
		out = append(out, cloneMember(*s.members[id]))
	}
	return out
}

// Permissions returns userID's effective permissions in a server (channelID == "")
// or in a channel, computed like Discord.
func (w *World) Permissions(serverID, channelID, userID string) discord.Permissions {
	w.mu.Lock()
	defer w.mu.Unlock()
	if channelID != "" {
		if c := w.channels[channelID]; c != nil && c.ch.Type != discord.ChannelDM {
			return w.channelPerms(c, userID)
		}
		return 0
	}
	if s := w.servers[serverID]; s != nil {
		return w.basePerms(s, userID)
	}
	return 0
}

// Messages returns the (non-ephemeral) messages of a channel, oldest first, with full
// content.
func (w *World) Messages(channelID string) []discord.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.channels[channelID]
	if c == nil {
		return nil
	}
	out := make([]discord.Message, 0, len(c.msgIDs))
	for _, id := range c.msgIDs {
		out = append(out, cloneMessage(w.messages[id].msg))
	}
	return out
}

// Message returns any message by ID, ephemeral ones included.
func (w *World) Message(messageID string) (discord.Message, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ms := w.messages[messageID]
	if ms == nil {
		return discord.Message{}, false
	}
	return cloneMessage(ms.msg), true
}

// MessageMentions returns who a message notified (see Mentions).
func (w *World) MessageMentions(messageID string) (Mentions, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ms := w.messages[messageID]
	if ms == nil {
		return Mentions{}, false
	}
	return Mentions{
		UserIDs:  slices.Clone(ms.mentions.UserIDs),
		RoleIDs:  slices.Clone(ms.mentions.RoleIDs),
		Everyone: ms.mentions.Everyone,
	}, true
}

// Reactions returns emoji → user IDs (in reaction order) for a message.
func (w *World) Reactions(messageID string) map[string][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	ms := w.messages[messageID]
	if ms == nil {
		return nil
	}
	out := make(map[string][]string, len(ms.reactions))
	for _, r := range ms.reactions {
		out[r.emoji] = append([]string{}, r.users...)
	}
	return out
}

// OpenSessions returns the number of sessions with an open gateway.
func (w *World) OpenSessions() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sessions)
}

// MustConnect is Connect for tests: it panics on error and returns the concrete type.
func (w *World) MustConnect(token string) *Session {
	s, err := w.Connect(context.Background(), token)
	if err != nil {
		panic(err)
	}
	return s.(*Session)
}
