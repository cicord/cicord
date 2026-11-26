package plugin

import (
	"encoding/json"
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

type audienceSnapshot interface {
	Snapshot() ([]discord.Role, []discord.Channel)
}
type readSnapshot struct {
	roles    []discord.Role
	channels map[string]discord.Channel
}

func (i *Instance) readSnapshot() readSnapshot {
	s := readSnapshot{channels: map[string]discord.Channel{}}
	if r, ok := i.env.Resolver.(audienceSnapshot); ok {
		roles, channels := r.Snapshot()
		s.roles = roles
		for _, ch := range channels {
			if ch.ServerID == i.env.ServerID {
				s.channels[ch.ID] = ch
			}
		}
	}
	return s
}
func (i *Instance) audience(s readSnapshot, r ref) *discord.Audience {
	id, err := i.resolve(state.KindChannel, r)
	if err != nil {
		return nil
	}
	ch, ok := s.channels[id]
	if !ok {
		return nil
	}
	// Public threads inherit their parent's access. Private thread membership is
	// additional information and cannot be inferred from parent permissions.
	if ch.Type == discord.ChannelPublicThread || ch.Type == discord.ChannelAnnouncementThread {
		parent, ok := s.channels[ch.ParentID]
		if !ok {
			return nil
		}
		ch = parent
	}
	return discord.NewAudience(ch, s.roles)
}

type audienceDTO struct {
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
	if inv == nil || i.env.OwnerID == nil {
		return []string{}, nil
	}
	subject, ok := inv.audienceMembers.Load(p.User)
	if !ok {
		return []string{}, nil
	}
	m := subject.(discord.Member)
	policy := i.audience(i.readSnapshot(), p.Ref)
	perms, _ := policy.Permissions(p.User, m.RoleIDs, i.env.OwnerID(), m.TimeoutUntil != nil && m.TimeoutUntil.After(time.Now()))
	return perms.Names(), nil
}
