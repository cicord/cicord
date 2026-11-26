package plugin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cicord/cicord/internal/discord"
)

func (i *Instance) structural(id string) map[string]any {
	var managed, heal bool
	if i.env.ManagedResource != nil {
		managed, heal = i.env.ManagedResource(id)
	}
	return map[string]any{"managed": managed, "willHeal": heal}
}
func (i *Instance) channelChange(ch discord.Channel, prev *discord.Channel) map[string]any {
	d := i.structural(ch.ID)
	d["channel"] = i.channelDTOFrom(ch)
	if prev != nil {
		d["previous"] = i.channelDTOFrom(*prev)
	}
	return d
}
func (i *Instance) roleChange(r discord.Role, prev *discord.Role) map[string]any {
	d := i.structural(r.ID)
	d["role"] = i.roleDTOFrom(r)
	if prev != nil {
		d["previous"] = i.roleDTOFrom(*prev)
	}
	return d
}
func (i *Instance) threadInfo(ch discord.Channel) map[string]any {
	d := map[string]any{"thread": i.channelDTOFrom(ch)}
	if ch.ParentID != "" {
		d["parent"] = i.channelDTO(ch.ParentID)
	}
	return d
}
func (i *Instance) invite(it discord.InviteUses) map[string]any {
	d := map[string]any{"code": it.Code, "uses": it.Uses, "maxUses": it.MaxUses, "vanity": it.Vanity}
	if it.ChannelID != "" {
		d["channelId"] = it.ChannelID
	}
	if it.Inviter != nil {
		d["inviter"] = i.userDTO(*it.Inviter)
	}
	if it.ExpiresAt != nil {
		d["expiresAt"] = jsTime(*it.ExpiresAt)
	}
	return d
}
func (i *Instance) structureEvent(ev discord.Event) map[string]any {
	switch e := ev.(type) {
	case discord.ChannelCreateEvent:
		if e.Channel.Type.IsThread() {
			return i.threadInfo(e.Channel)
		}
		return i.channelChange(e.Channel, nil)
	case discord.ChannelUpdateEvent:
		return i.channelChange(e.Channel, e.Previous)
	case discord.ChannelDeleteEvent:
		if e.Channel.Type.IsThread() {
			return i.threadInfo(e.Channel)
		}
		return i.channelChange(e.Channel, nil)
	case discord.RoleCreateEvent:
		return i.roleChange(e.Role, nil)
	case discord.RoleUpdateEvent:
		return i.roleChange(e.Role, e.Previous)
	case discord.RoleDeleteEvent:
		if e.Previous != nil {
			return i.roleChange(*e.Previous, e.Previous)
		}
		return i.roleChange(discord.Role{ID: e.RoleID}, nil)
	case discord.ServerUpdateEvent:
		changes := map[string]map[string]string{}
		if p := e.Previous; p != nil {
			add := func(k, a, b string) {
				if a != b {
					changes[k] = map[string]string{"old": a, "new": b}
				}
			}
			add("name", p.Name, e.Server.Name)
			add("icon", p.IconURL, e.Server.IconURL)
			add("verificationLevel", p.VerificationLevel, e.Server.VerificationLevel)
			add("description", p.Description, e.Server.Description)
		}
		return map[string]any{"changes": changes}
	case discord.EmojisUpdateEvent:
		items := make([]map[string]string, 0, len(e.Emojis))
		for _, v := range e.Emojis {
			items = append(items, map[string]string{"id": v.ID, "name": v.Name})
		}
		return map[string]any{"emojis": items}
	case discord.StickersUpdateEvent:
		items := make([]map[string]string, 0, len(e.Stickers))
		for _, v := range e.Stickers {
			items = append(items, map[string]string{"id": v.ID, "name": v.Name})
		}
		return map[string]any{"stickers": items}
	case discord.WebhooksUpdateEvent:
		return map[string]any{"channelId": e.ChannelID}
	case discord.InviteCreateEvent:
		return i.invite(discord.InviteUses{Code: e.Code, Uses: e.Uses, MaxUses: e.MaxUses, ChannelID: e.ChannelID, Inviter: e.Inviter, ExpiresAt: e.ExpiresAt})
	case discord.InviteDeleteEvent:
		return map[string]any{"code": e.Code, "channelId": e.ChannelID}
	case discord.AutoModActionEvent:
		d := map[string]any{"rule": e.RuleID, "userId": e.UserID, "action": e.Action}
		if e.ChannelID != "" {
			d["channelId"] = e.ChannelID
		}
		if i.env.MessageContent && i.grants[capMessagesRead] && e.Matched != "" {
			d["matched"] = e.Matched
		}
		return d
	}
	return nil
}
func opServerInvites(i *Instance, _ *invocation, _ json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capInvitesRead); err != nil {
		return nil, err
	}
	cl, ok := i.env.Discord.(interface {
		ListInviteUses(context.Context, string) ([]discord.InviteUses, error)
	})
	if !ok {
		return nil, errors.New("invite listing unavailable")
	}
	return func(ctx context.Context) (any, error) {
		entries, err := cl.ListInviteUses(ctx, i.env.ServerID)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			out = append(out, i.invite(e))
		}
		return out, nil
	}, nil
}
