package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// rememberAudience records only current inputs to an invocation. The previous member
// of an update and a member retained by JS in a previous request confer no access.
func (inv *invocation) rememberAudience(v any) {
	save := func(m *memberDTO) {
		if m != nil && m.subject.User.ID != "" {
			inv.audienceMembers.Store(m.subject.User.ID, m.subject)
		}
	}
	switch d := v.(type) {
	case routeDTO:
		if d.Viewer != nil {
			save(d.Viewer.Member)
		}
	case interactionDTO:
		save(d.Member)
	case eventDTO:
		save(d.Member)
		if d.Message != nil {
			save(d.Message.Member)
		}
	case memberDTO:
		save(&d)
	case *memberDTO:
		save(d)
	case []memberDTO:
		for j := range d {
			save(&d[j])
		}
	}
}

func (i *Instance) readSnapshot() discord.AudienceSnapshot {
	if r, ok := i.env.Discord.(discord.AudienceSnapshotReader); ok {
		return r.AudienceSnapshot(i.env.ServerID)
	}
	return discord.AudienceSnapshot{}
}
func (i *Instance) audience(s discord.AudienceSnapshot, r ref) *discord.Audience {
	id, err := i.resolve(state.KindChannel, r)
	if err != nil {
		return nil
	}
	ch, ok := s.Channels[id]
	if !ok || ch.ServerID != i.env.ServerID {
		return nil
	}
	// Public threads inherit their parent's access. Private thread membership is
	// additional information and cannot be inferred from parent permissions.
	if ch.Type == discord.ChannelPublicThread || ch.Type == discord.ChannelAnnouncementThread {
		parent, ok := s.Channels[ch.ParentID]
		if !ok || parent.ServerID != ch.ServerID {
			return nil
		}
		ch = parent
	}
	return discord.NewAudience(ch, s.Roles)
}

type audienceDTO struct {
	Fingerprint  string   `json:"fingerprint,omitempty"`
	Known        bool     `json:"known"`
	Public       bool     `json:"public"`
	AllowRoles   []string `json:"allowRoles"`
	AllowMembers []string `json:"allowMembers"`
	DenyMembers  []string `json:"denyMembers"`
}

var contentRead = discord.MustPermissions("ViewChannel", "ReadMessageHistory")

func opAudience(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	if err := i.require(capChannelsRead); err != nil {
		return nil, err
	}
	p, err := decode[struct {
		Ref ref `json:"ref"`
	}](args)
	if err != nil {
		return nil, err
	}
	policy := i.audience(i.readSnapshot(), p.Ref)
	out := audienceDTO{Known: policy != nil, AllowRoles: []string{}, AllowMembers: []string{}, DenyMembers: []string{}}
	if policy == nil {
		return out, nil
	}
	out.Fingerprint = audienceFingerprint(i.ownerID(), policy)

	out.Public = policy.Public()
	for id := range policy.Roles {
		if id == policy.Server {
			continue
		}
		perms, _ := policy.Permissions("ordinary", []string{id}, "owner", false)
		if perms.Has(contentRead) {
			out.AllowRoles = append(out.AllowRoles, id)
		}
	}
	for id, ow := range policy.MemberOverrides {
		perms, _ := policy.Permissions(id, nil, "owner", false)
		if perms.Has(contentRead) {
			out.AllowMembers = append(out.AllowMembers, id)
		}
		if ow.Deny&contentRead != 0 {
			out.DenyMembers = append(out.DenyMembers, id)
		}
	}
	slices.Sort(out.AllowRoles)
	slices.Sort(out.AllowMembers)
	slices.Sort(out.DenyMembers)
	return out, nil
}
func opCompareAudience(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	if err := i.require(capChannelsRead); err != nil {
		return nil, err
	}
	p, err := decode[struct {
		Source      ref `json:"source"`
		Destination ref `json:"destination"`
	}](args)
	if err != nil {
		return nil, err
	}
	s := i.readSnapshot()
	return discord.CompareAudience(i.audience(s, p.Source), i.audience(s, p.Destination)), nil
}
func opPermissionsIn(i *Instance, inv *invocation, args json.RawMessage) (any, error) {
	if err := i.require(capChannelsRead); err != nil {
		return nil, err
	}
	p, err := decode[struct {
		User string `json:"userId"`
		Ref  ref    `json:"ref"`
	}](args)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return []string{}, nil
	}
	subject, ok := inv.audienceMembers.Load(p.User)
	if !ok {
		return []string{}, nil
	}
	m := subject.(discord.Member)
	s := i.readSnapshot()
	policy := i.audience(s, p.Ref)
	perms, _ := policy.Permissions(p.User, m.RoleIDs, s.OwnerID, m.TimeoutUntil != nil && m.TimeoutUntil.After(time.Now()))
	return perms.Names(), nil
}

// Include all principals and all permissions that affect reading. A send-only
// change when archiving a channel does not change its reader policy.
func audienceFingerprint(owner string, p *discord.Audience) string {
	if owner == "" || p == nil {
		return ""
	}
	mask := contentRead | discord.MustPermissions("Administrator")
	roles := map[string]discord.Permissions{}
	for id, perms := range p.Roles {
		roles[id] = perms & mask
	}
	readOverwrites := func(src map[string]discord.Overwrite) map[string][2]discord.Permissions {
		result := map[string][2]discord.Permissions{}
		for id, o := range src {
			a, d := o.Allow&contentRead, o.Deny&contentRead
			if a|d != 0 {
				result[id] = [2]discord.Permissions{a, d}
			}
		}
		return result
	}
	raw, _ := json.Marshal([]any{p.Server, p.Channel, owner, p.NSFW, roles, p.Everyone.Allow & contentRead, p.Everyone.Deny & contentRead, readOverwrites(p.RoleOverrides), readOverwrites(p.MemberOverrides)})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

type readVerification struct {
	Known                    bool   `json:"known"`
	Confined                 bool   `json:"confined"`
	Fingerprint              string `json:"fingerprint"`
	WithoutMemberFingerprint string `json:"withoutMemberFingerprint,omitempty"`
	ViewerCanRead            bool   `json:"viewerCanRead"`
	Destination              string `json:"destination"`
}

func opVerifyReaders(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capChannelsRead); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		Channel               ref
		Roles, Members        []string
		Viewer, WithoutMember string
		Destination           *ref
	}](args)
	if err != nil {
		return nil, err
	}
	if len(a.Roles)+len(a.Members) > 100 {
		return nil, argErr("verifyReaders: too many principals")
	}
	if a.Viewer != "" {
		if err := i.require(capMembersRead); err != nil {
			return nil, err
		}
	}
	if a.Viewer != "" {
		if err := checkSnowflake("member", a.Viewer); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (any, error) {
		out := readVerification{Destination: "unknown"}
		roles, err := i.env.Discord.ListRoles(ctx, i.env.ServerID)
		if err != nil {
			return out, nil
		}
		server, err := i.env.Discord.GetServer(ctx, i.env.ServerID)
		if err != nil || server.OwnerID == "" {
			return out, nil
		}
		policy := func(r ref) *discord.Audience {
			id, e := i.resolve(state.KindChannel, r)
			if e != nil {
				return nil
			}
			channel, e := i.env.Discord.GetChannel(ctx, id)
			if e != nil || channel.ServerID != i.env.ServerID {
				return nil
			}
			return discord.NewAudience(channel, roles)
		}
		source := policy(a.Channel)
		if source == nil {
			return out, nil
		}
		limit := discord.Channel{ID: "reader-ceiling", ServerID: i.env.ServerID, Type: discord.ChannelText, Overwrites: []discord.Overwrite{{Type: discord.OverwriteRole, ID: i.env.ServerID, Deny: contentRead}}}
		for _, id := range a.Roles {
			if err := checkSnowflake("role", id); err != nil || id == i.env.ServerID {
				return out, nil
			}
			limit.Overwrites = append(limit.Overwrites, discord.Overwrite{ID: id, Type: discord.OverwriteRole, Allow: contentRead})
		}
		for _, id := range a.Members {
			if err := checkSnowflake("member", id); err != nil {
				return out, nil
			}
			limit.Overwrites = append(limit.Overwrites, discord.Overwrite{ID: id, Type: discord.OverwriteMember, Allow: contentRead})
		}
		ceiling := discord.NewAudience(limit, roles)
		if ceiling == nil {
			return out, nil
		}
		out.Known = true
		out.Fingerprint = audienceFingerprint(server.OwnerID, source)
		if a.WithoutMember != "" {
			clean := *source
			clean.MemberOverrides = maps.Clone(source.MemberOverrides)
			delete(clean.MemberOverrides, a.WithoutMember)
			out.WithoutMemberFingerprint = audienceFingerprint(server.OwnerID, &clean)
		}
		relation := discord.CompareAudience(ceiling, source)
		out.Confined = relation == "same" || relation == "narrower"
		if a.Destination != nil {
			out.Destination = discord.CompareAudience(source, policy(*a.Destination))
		}
		if a.Viewer != "" && !source.NSFW {
			if _, known := inv.audienceMembers.Load(a.Viewer); !known {
				return out, nil
			}
			member, e := i.env.Discord.GetMember(ctx, i.env.ServerID, a.Viewer)
			if e != nil && !errors.Is(e, discord.ErrNotFound) {
				return out, nil
			}
			if e == nil && member.User.ID == a.Viewer {
				perms, known := source.Permissions(a.Viewer, member.RoleIDs, server.OwnerID, member.TimeoutUntil != nil && member.TimeoutUntil.After(time.Now()))
				out.ViewerCanRead = known && perms.Has(contentRead)
			}
		}
		return out, nil
	}, nil
}
