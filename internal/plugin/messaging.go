package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
)

// Discord checks a nonce across the bot's sends. Scope it to the invoking instance and channel.
func (i *Instance) scopeNonce(channel string, m discord.MessageCreate) discord.MessageCreate {
	if m.Nonce != "" {
		sum := sha256.Sum256([]byte(i.env.ServerID + "\x00" + i.env.InstanceName + "\x00" + channel + "\x00" + m.Nonce))
		m.Nonce = hex.EncodeToString(sum[:12])
	}
	return m
}

func messaging(op, grant string) asyncOp {
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if err := i.require(grant); err != nil {
			return nil, err
		}
		a, err := decode[struct {
			ChannelID, MessageID, UserID, Emoji, After string
			Limit                                      *int
			Identity                                   discord.WebhookIdentity
			Message                                    messageInput
		}](args)
		if err != nil {
			return nil, err
		}
		switch op {
		case "pins", "typing", "sendAs":
		default:
			if err = checkSnowflake("message", a.MessageID); err != nil {
				return nil, err
			}
		}
		if a.After != "" {
			if err = checkSnowflake("after", a.After); err != nil {
				return nil, err
			}
		}
		if a.UserID != "" {
			if err = checkSnowflake("user", a.UserID); err != nil {
				return nil, err
			}
		}
		if (op == "reactionUsers" || op == "removeReaction") && a.Emoji == "" {
			return nil, argErr("reaction emoji is required")
		}
		limit := 25
		if a.Limit != nil {
			limit = *a.Limit
			if limit < 1 || limit > 100 {
				return nil, argErr("reactionUsers limit must be 1–100")
			}
		}
		var msg discord.MessageCreate
		if op == "sendAs" {
			if n := utf8.RuneCountInString(a.Identity.Username); n < 1 || n > 80 {
				return nil, argErr("sendAs username must be 1–80 characters")
			}
			if a.Identity.AvatarURL != "" {
				u, e := url.Parse(a.Identity.AvatarURL)
				if e != nil || u.Scheme != "https" || u.Host == "" {
					return nil, argErr("sendAs avatarUrl must be an HTTPS URL")
				}
			}
			msg, err = i.messageCreate(a.Message, false)
			if err != nil {
				return nil, err
			}
		}
		return func(ctx context.Context) (any, error) {
			dm, err := i.channelScope(ctx, a.ChannelID)
			if err != nil {
				return nil, err
			}
			if dm && (op == "sendAs" || op == "crosspost") {
				return nil, argErr("%s requires a server channel", op)
			}
			if dm && op == "typing" {
				if err := i.require(capDMSend); err != nil {
					return nil, err
				}
			}
			client, ok := i.env.Discord.(discord.MessagingClient)
			if !ok {
				return nil, errors.New("Discord client does not support extended messaging")
			}
			switch op {
			case "pin", "unpin":
				return nil, client.SetMessagePin(ctx, a.ChannelID, a.MessageID, op == "pin")
			case "typing":
				return nil, client.Typing(ctx, a.ChannelID)
			case "crosspost":
				return nil, client.Crosspost(ctx, a.ChannelID, a.MessageID)
			case "removeReaction":
				if a.UserID == "" || a.UserID == i.env.Bot.ID {
					return nil, i.env.Discord.RemoveOwnReaction(ctx, a.ChannelID, a.MessageID, a.Emoji)
				}
				return nil, client.RemoveUserReaction(ctx, a.ChannelID, a.MessageID, a.Emoji, a.UserID)
			case "reactionUsers":
				users, err := client.ReactionUsers(ctx, a.ChannelID, a.MessageID, a.Emoji, limit, a.After)
				if err != nil {
					return nil, err
				}
				out := make([]userDTO, 0, len(users))
				for _, u := range users {
					out = append(out, i.userDTO(u))
				}
				return out, nil
			case "pins":
				msgs, err := client.ChannelPins(ctx, a.ChannelID)
				if err != nil {
					return nil, err
				}
				out := make([]messageDTO, 0, len(msgs))
				for _, m := range msgs {
					out = append(out, i.messageDTO(m))
				}
				return out, nil
			case "sendAs":
				m, err := client.SendAs(ctx, a.ChannelID, a.Identity, msg)
				if err != nil {
					return nil, err
				}
				return i.messageDTO(m), nil
			}
			return nil, errors.New("unknown messaging operation")
		}, nil
	}
}
