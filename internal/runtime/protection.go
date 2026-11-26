package runtime

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

// Read with the calling bot's token. A guild may have a more privileged reconciler.
func (r *Runtime) protection(ctx context.Context, session discord.Session, s *config.Server) plugin.ServerProtection {
	policy := s.Spec.SyncPolicy
	result := plugin.ServerProtection{SelfHeal: policy.SelfHeal(), DeletionProtection: policy.DeletionProtection(), Paused: r.isPaused(s.Metadata.Name), Unmanaged: []plugin.ProtectionResource{}}
	sid := s.Spec.DiscordID
	guild, err := session.GetServer(ctx, sid)
	if err != nil || guild.ID != sid || guild.OwnerID == "" {
		return result
	}
	bot, err := session.GetMember(ctx, sid, session.Self().ID)
	if err != nil || bot.User.ID != session.Self().ID || bot.ServerID != sid {
		return result
	}
	roles, err := session.ListRoles(ctx, sid)
	if err != nil {
		return result
	}
	channels, err := session.ListChannels(ctx, sid)
	if err != nil {
		return result
	}
	mapped, err := r.opts.Store.Resources().List(ctx, s.Metadata.Name)
	if err != nil {
		return result
	}
	return protectionInventory(result, s.Spec, guild, bot, roles, channels, mapped)
}

func protectionInventory(policy plugin.ServerProtection, spec config.ServerSpec, guild discord.Server, bot discord.Member, roles []discord.Role, channels []discord.Channel, mapped []state.Resource) plugin.ServerProtection {
	out := policy
	byID := make(map[string]discord.Role, len(roles))
	for _, role := range roles {
		byID[role.ID] = role
	}
	if _, ok := byID[guild.ID]; !ok {
		return policy
	}
	top := 0
	for _, id := range bot.RoleIDs {
		role, ok := byID[id]
		if !ok {
			return policy
		}
		top = max(top, role.Position)
	}
	refs := make(map[string]state.Resource, len(mapped))
	for _, resource := range mapped {
		refs[resource.ID] = resource
	}
	add := func(id, name, kind string, typ state.ResourceKind) {
		ref := refs[id]
		if strings.HasPrefix(ref.Owner, "plugin:") {
			return
		}
		declared := false
		if ref.Kind == typ && ref.Owner == state.OwnerGit {
			switch typ {
			case state.KindRole:
				_, declared = spec.Roles[ref.Key]
			case state.KindCategory:
				_, declared = spec.Categories[ref.Key]
			case state.KindChannel:
				_, declared = spec.Channels[ref.Key]
			}
		}
		out.TotalResources++
		if !declared {
			out.Unmanaged = append(out.Unmanaged, plugin.ProtectionResource{ID: id, Name: name, Kind: kind})
		}
	}
	for _, role := range roles {
		if !role.IsEveryone(guild.ID) && !role.Managed {
			add(role.ID, role.Name, "role", state.KindRole)
		}
	}
	timedOut := bot.TimeoutUntil != nil && bot.TimeoutUntil.After(time.Now())
	for _, ch := range channels {
		if ch.ServerID != guild.ID {
			return policy
		}
		if ch.Type == discord.ChannelDM || ch.Type.IsThread() {
			continue
		}
		if strings.HasPrefix(refs[ch.ID].Owner, "plugin:") {
			continue
		}
		audience := discord.NewAudience(ch, roles)
		permissions, known := audience.Permissions(bot.User.ID, bot.RoleIDs, guild.OwnerID, timedOut)
		if !known {
			return policy
		}
		if !permissions.Has(discord.MustPermissions("ViewChannel")) {
			continue
		}
		typ := state.KindChannel
		if ch.Type == discord.ChannelCategory {
			typ = state.KindCategory
		}
		add(ch.ID, ch.Name, "channel", typ)
	}
	slices.SortFunc(out.Unmanaged, func(a, b plugin.ProtectionResource) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.ID, b.ID)
	})
	out.Known = true
	out.BotTopPosition = &top
	return out
}
