package reconcile

import (
	"context"
	"fmt"
	"math"

	"github.com/npjonath/codi/internal/discord"
)

// Snapshot is the live state of a server.
type Snapshot struct {
	Server   discord.Server
	Roles    []discord.Role
	Channels []discord.Channel // categories included, threads excluded
	// BotRoleIDs are the roles of the bot member; nil when no bot user was given.
	BotRoleIDs []string
	// HasBot is true when BotRoleIDs was fetched.
	HasBot bool
}

// Fetch reads the live state of a server. botUserID may be empty, in which case no
// role hierarchy constraint applies.
func Fetch(ctx context.Context, c discord.Client, serverID, botUserID string) (Snapshot, error) {
	var s Snapshot
	var err error
	if s.Server, err = c.GetServer(ctx, serverID); err != nil {
		return s, fmt.Errorf("get server %s: %w", serverID, err)
	}
	if s.Roles, err = c.ListRoles(ctx, serverID); err != nil {
		return s, fmt.Errorf("list roles: %w", err)
	}
	chans, err := c.ListChannels(ctx, serverID)
	if err != nil {
		return s, fmt.Errorf("list channels: %w", err)
	}
	for _, ch := range chans {
		if isThread(ch.Type) {
			continue
		}
		s.Channels = append(s.Channels, ch)
	}
	if botUserID != "" {
		m, err := c.GetMember(ctx, serverID, botUserID)
		if err != nil {
			return s, fmt.Errorf("get bot member: %w", err)
		}
		s.BotRoleIDs = append([]string{}, m.RoleIDs...)
		s.HasBot = true
	}
	return s, nil
}

// BotTopPosition returns the position of the bot's highest role, or math.MaxInt when
// unknown (no hierarchy constraint).
func (s Snapshot) BotTopPosition() int {
	if !s.HasBot {
		return math.MaxInt
	}
	top := 0
	for _, id := range s.BotRoleIDs {
		for _, r := range s.Roles {
			if r.ID == id && r.Position > top {
				top = r.Position
			}
		}
	}
	return top
}

func isThread(t discord.ChannelType) bool {
	switch t {
	case discord.ChannelAnnouncementThread, discord.ChannelPublicThread, discord.ChannelPrivateThread, discord.ChannelDM:
		return true
	}
	return false
}
