package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cicord/cicord/internal/discord"
)

// Read the same live overwrite table that a subsequent edit will patch. Returning
// only the requested member's row keeps role and other members' rows private.
func opChannelMemberOverwrite(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capChannelsRead); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		ChannelID string `json:"channelId"`
		UserID    string `json:"userId"`
	}](args)
	if err != nil {
		return nil, err
	}
	if err := checkSnowflake("channel", a.ChannelID); err != nil {
		return nil, err
	}
	if err := checkSnowflake("member", a.UserID); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		ch, err := i.env.Discord.GetChannel(ctx, a.ChannelID)
		if err != nil {
			return nil, err
		}
		if ch.Type == discord.ChannelDM || ch.ServerID == "" || ch.ServerID != i.env.ServerID {
			return nil, fmt.Errorf("memberOverwrite: channel does not belong to server %s", i.env.ServerID)
		}
		for _, row := range ch.Overwrites {
			if row.ID == a.UserID && row.Type == discord.OverwriteMember {
				return map[string][]string{"allow": row.Allow.Names(), "deny": row.Deny.Names()}, nil
			}
		}
		return nil, nil
	}, nil
}
