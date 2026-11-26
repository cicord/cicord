package discord

// Channel read policies are compared over possible role assignments. A snapshot of
// today's members alone cannot authorize a permanent copy into a channel.
import (
	"maps"
	"slices"
)

var (
	readView        = MustPermissions("ViewChannel")
	readHistory     = MustPermissions("ReadMessageHistory")
	readBoth        = readView | readHistory
	adminPermission = MustPermissions("Administrator")
)

// ApplyOverwrites follows Discord's ordered everyone, combined role, then member
// overwrites. The caller handles server owner / Administrator, timeouts and thread scope.
func ApplyOverwrites(base Permissions, server, user string, roles []string, overwrites []Overwrite) Permissions {
	var roleAllow, roleDeny, userAllow, userDeny Permissions
	for _, o := range overwrites {
		switch {
		case o.Type == OverwriteRole && o.ID == server:
			base = base&^o.Deny | o.Allow
		case o.Type == OverwriteRole && slices.Contains(roles, o.ID):
			roleAllow |= o.Allow
			roleDeny |= o.Deny
		case o.Type == OverwriteMember && o.ID == user:
			userAllow |= o.Allow
			userDeny |= o.Deny
		}
	}
	return ((base&^roleDeny | roleAllow) &^ userDeny) | userAllow
}

// Audience describes the role and member overwrites of one guild channel. Unknown
// roles, private thread membership and cross-server inputs must fail closed.
type Audience struct {
	Server          string
	Channel         string
	NSFW            bool
	Roles           map[string]Permissions
	Everyone        Overwrite
	RoleOverrides   map[string]Overwrite
	MemberOverrides map[string]Overwrite
}

// AudienceSnapshot is the current channel policy of one guild from one bot session.
// An empty snapshot means the gateway cannot currently authorize a read.
type AudienceSnapshot struct {
	OwnerID  string
	Roles    []Role
	Channels map[string]Channel
}

type AudienceSnapshotReader interface {
	AudienceSnapshot(serverID string) AudienceSnapshot
}

// CurrentGuildOwner returns the owner from a synchronized guild session. Clients
// without a current gateway view cannot establish an ownership exemption.
func CurrentGuildOwner(client Client, serverID string) string {
	if reader, ok := client.(interface{ GuildOwnerID(string) string }); ok {
		return reader.GuildOwnerID(serverID)
	}
	return ""
}

func NewAudience(ch Channel, roles []Role) *Audience {
	if ch.ServerID == "" || ch.ID == "" {
		return nil
	}
	switch ch.Type {
	case ChannelText, ChannelVoice, ChannelCategory, ChannelAnnouncement, ChannelStage, ChannelForum, ChannelMedia:
		// These guild channels apply the ordinary overwrite hierarchy.
	default:
		return nil
	}
	a := &Audience{Server: ch.ServerID, Channel: ch.ID, NSFW: ch.NSFW, Roles: map[string]Permissions{}, RoleOverrides: map[string]Overwrite{}, MemberOverrides: map[string]Overwrite{}}
	for _, role := range roles {
		a.Roles[role.ID] = role.Permissions
	}
	if _, ok := a.Roles[ch.ServerID]; !ok {
		return nil
	}
	for _, o := range ch.Overwrites {
		if o.Type == OverwriteMember {
			a.MemberOverrides[o.ID] = o
			continue
		}
		if o.Type != OverwriteRole {
			return nil
		}
		if _, ok := a.Roles[o.ID]; !ok {
			return nil
		}
		if o.ID == ch.ServerID {
			a.Everyone = o
		} else {
			a.RoleOverrides[o.ID] = o
		}
	}
	return a
}

func (a *Audience) Permissions(user string, roles []string, owner string, timedOut bool) (Permissions, bool) {
	// The Discord API does not prove that the portal viewer passed the age gate.
	if a == nil || a.NSFW || owner == "" || user == "" {
		return 0, false
	}
	if user == owner {
		return ^Permissions(0), true
	}
	p := a.Roles[a.Server]
	ovs := []Overwrite{a.Everyone, a.MemberOverrides[user]}
	for _, id := range roles {
		bits, ok := a.Roles[id]
		if !ok {
			return 0, false
		}
		p |= bits
		if o, ok := a.RoleOverrides[id]; ok {
			ovs = append(ovs, o)
		}
	}
	if p.Has(adminPermission) {
		return ^Permissions(0), true
	}
	p = ApplyOverwrites(p, a.Server, user, roles, ovs)
	if !p.Has(readView) {
		return 0, true
	}
	if timedOut {
		p &= readBoth
	}
	return p, true
}

// Public reports whether every possible member, including one with a denying
// overwrite, can read history. An age-restricted channel is never public.
func (a *Audience) Public() bool {
	if a == nil || a.NSFW {
		return false
	}
	if a.Roles[a.Server].Has(adminPermission) {
		return true
	}
	p := a.Roles[a.Server]&^a.Everyone.Deny | a.Everyone.Allow
	if !p.Has(readBoth) {
		return false
	}
	for id, o := range a.RoleOverrides {
		if !a.Roles[id].Has(adminPermission) && (o.Deny&^o.Allow)&readBoth != 0 {
			return false
		}
	}
	for _, o := range a.MemberOverrides {
		if (o.Deny&^o.Allow)&readBoth != 0 {
			return false
		}
	}
	return true
}

// Two permission bits plus allow/deny masks for each channel form ten bits in
// total. Folding equivalent role combinations bounds the comparison at 1024
// states independently of how many roles or members a server has.
type readMask uint16

func readBits(p Permissions) readMask {
	var v readMask
	if p.Has(readView) {
		v |= 1
	}
	if p.Has(readHistory) {
		v |= 2
	}
	return v
}
func contribution(p Permissions, src, dst Overwrite) readMask {
	return readBits(p) | readBits(src.Allow)<<2 | readBits(src.Deny)<<4 | readBits(dst.Allow)<<6 | readBits(dst.Deny)<<8
}

// CompareAudience describes the destination relative to the source. Incomparable
// overlapping policies return unknown. Disjoint refers to ordinary members;
// owners and administrators can read both regardless of overwrites.
func CompareAudience(src, dst *Audience) string {
	if src == nil || dst == nil || src.Server != dst.Server || !maps.Equal(src.Roles, dst.Roles) || src.NSFW && !dst.NSFW {
		return "unknown"
	}
	if src.Channel == dst.Channel || src.Roles[src.Server].Has(adminPermission) {
		return "same"
	}
	masks := []readMask{readBits(src.Roles[src.Server])}
	seen := map[readMask]bool{masks[0]: true}
	unique := map[readMask]bool{}
	for id, p := range src.Roles {
		if id == src.Server || p.Has(adminPermission) {
			continue
		}
		c := contribution(p, src.RoleOverrides[id], dst.RoleOverrides[id])
		if unique[c] {
			continue
		}
		unique[c] = true
		for _, s := range masks {
			combined := s | c
			if !seen[combined] {
				masks = append(masks, combined)
				seen[combined] = true
			}
		}
	}
	// Only explicitly named members differ from the anonymous principal. Classify
	// equivalent pairs of member overwrites once, even with hundreds of targets.
	type pair struct{ sa, sd, da, dd readMask }
	principals := map[pair]bool{{}: true}
	add := func(user string) {
		a, b := src.MemberOverrides[user], dst.MemberOverrides[user]
		principals[pair{readBits(a.Allow), readBits(a.Deny), readBits(b.Allow), readBits(b.Deny)}] = true
	}
	for id := range src.MemberOverrides {
		add(id)
	}
	for id := range dst.MemberOverrides {
		add(id)
	}
	var left, right, both bool
	for m := range principals {
		for _, s := range masks {
			base := s & 3
			// Apply the individual overwrite last, including an individual deny on
			// a role grant. Keep the two bits separate until all layers are applied.
			permits := func(everyone Overwrite, allow, deny, ma, md readMask) bool {
				v := base&^readBits(everyone.Deny) | readBits(everyone.Allow)
				v = (v &^ deny) | allow
				return ((v&^md)|ma)&3 == 3
			}
			l := permits(src.Everyone, (s>>2)&3, (s>>4)&3, m.sa, m.sd)
			r := permits(dst.Everyone, (s>>6)&3, (s>>8)&3, m.da, m.dd)
			left = left || l && !r
			right = right || r && !l
			both = both || l && r
			if left && right && both {
				return "unknown"
			}
		}
	}
	switch {
	case !left && !right:
		return "same"
	case !right:
		return "narrower"
	case !left:
		return "wider"
	default:
		return "disjoint"
	}
}
