package sim

import (
	"math"
	"strings"

	"github.com/cicord/cicord/internal/discord"
)

// actor is who performs a call: a bot (appID set) or a human user.
type actor struct {
	userID string
	appID  string
}

func (a actor) isBot() bool { return a.appID != "" }

// rawBasePerms is the server-level permission set before timeouts. Caller holds w.mu.
func (w *World) rawBasePerms(s *serverState, userID string) discord.Permissions {
	if userID == s.info.OwnerID {
		return allPermissions
	}
	m := s.members[userID]
	if m == nil {
		return 0
	}
	p := s.roles[s.info.ID].Permissions
	for _, id := range m.RoleIDs {
		if r := s.roles[id]; r != nil {
			p |= r.Permissions
		}
	}
	if p&permAdmin != 0 {
		return allPermissions
	}
	return p
}

func (w *World) timedOut(m *discord.Member) bool {
	return m != nil && m.TimeoutUntil != nil && m.TimeoutUntil.After(w.now())
}

// basePerms returns userID's server-level permissions. Caller holds w.mu.
func (w *World) basePerms(s *serverState, userID string) discord.Permissions {
	p := w.rawBasePerms(s, userID)
	if p != allPermissions && w.timedOut(s.members[userID]) {
		p &= permTimeoutRestricted
	}
	return p
}

// channelPerms returns userID's permissions in a server channel, following Discord's
// algorithm: base → @everyone overwrite → role overwrites → member overwrite. Threads
// use their parent's permissions. Caller holds w.mu.
func (w *World) channelPerms(c *channelState, userID string) discord.Permissions {
	if isThread(c.ch.Type) {
		parent := w.channels[c.ch.ParentID]
		if parent == nil {
			return 0
		}
		return w.channelPerms(parent, userID)
	}
	s := w.servers[c.ch.ServerID]
	if s == nil {
		return 0
	}
	p := w.rawBasePerms(s, userID)
	if p == allPermissions {
		return p
	}
	m := s.members[userID]
	if m == nil {
		return 0
	}
	p = discord.ApplyOverwrites(p, s.info.ID, userID, m.RoleIDs, c.ch.Overwrites)
	if w.timedOut(m) {
		p &= permTimeoutRestricted
	}
	if p&permView == 0 {
		return 0
	}
	return p
}

// highest is the position of userID's highest role (owner = +inf). Caller holds w.mu.
func (w *World) highest(s *serverState, userID string) int {
	if userID == s.info.OwnerID {
		return math.MaxInt
	}
	m := s.members[userID]
	if m == nil {
		return -1
	}
	top := 0
	for _, id := range m.RoleIDs {
		if r := s.roles[id]; r != nil && r.Position > top {
			top = r.Position
		}
	}
	return top
}

func missingErr(have, need discord.Permissions) error {
	return forbidden("missing permission %s", strings.Join((need&^have).Names(), ", "))
}

// requireServer checks the actor is a member with the given server permissions.
func (w *World) requireServer(a actor, s *serverState, need discord.Permissions) error {
	if s.members[a.userID] == nil {
		return forbidden("missing access to server %s", s.info.ID)
	}
	if p := w.basePerms(s, a.userID); !p.Has(need) {
		return missingErr(p, need)
	}
	return nil
}

// requireChannel checks the actor's permissions in a channel (ViewChannel always).
func (w *World) requireChannel(a actor, c *channelState, need discord.Permissions) error {
	if c.ch.Type == discord.ChannelDM {
		if c.dmUsers[0] != a.userID && c.dmUsers[1] != a.userID {
			return forbidden("missing access to channel %s", c.ch.ID)
		}
		return nil
	}
	s := w.servers[c.ch.ServerID]
	if s == nil || s.members[a.userID] == nil {
		return forbidden("missing access to channel %s", c.ch.ID)
	}
	p := w.channelPerms(c, a.userID)
	if !p.Has(permView) {
		return forbidden("missing access to channel %s", c.ch.ID)
	}
	if !p.Has(need) {
		return missingErr(p, need)
	}
	return nil
}

// requireAbove checks the actor's highest role is strictly above position.
func (w *World) requireAbove(a actor, s *serverState, position int, what string) error {
	if w.highest(s, a.userID) <= position {
		return forbidden("%s is at or above your highest role", what)
	}
	return nil
}

// requireMemberAbove checks the actor outranks target (owner is untouchable).
func (w *World) requireMemberAbove(a actor, s *serverState, targetID string) error {
	if targetID == s.info.OwnerID {
		return forbidden("cannot act on the server owner")
	}
	if a.userID == s.info.OwnerID {
		return nil
	}
	if w.highest(s, targetID) >= w.highest(s, a.userID) {
		return forbidden("member's highest role is at or above yours")
	}
	return nil
}

func isThread(t discord.ChannelType) bool {
	return t == discord.ChannelPublicThread || t == discord.ChannelPrivateThread || t == discord.ChannelAnnouncementThread
}
