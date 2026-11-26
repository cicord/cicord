package plugin

import (
	"crypto/sha256"
	"encoding/hex"
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
