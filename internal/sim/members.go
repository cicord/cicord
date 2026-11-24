package sim

import (
	"sort"
	"time"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
)

// maxTimeout is Discord's maximum member timeout.
const maxTimeout = 28 * 24 * time.Hour

// AddMember makes userID join serverID (emits MemberJoinEvent). Joining a server the
// user is already in is a no-op; banned users cannot join.
func (w *World) AddMember(serverID, userID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.addMember(serverID, userID)
	return err
}

func (w *World) addMember(serverID, userID string) (*discord.Member, error) {
	s, err := w.server(serverID)
	if err != nil {
		return nil, err
	}
	u := w.users[userID]
	if u == nil {
		return nil, notFound("unknown user %s", userID)
	}
	if m := s.members[userID]; m != nil {
		return m, nil
	}
	if s.bans[userID] {
		return nil, forbidden("user %s is banned from server %s", userID, serverID)
	}
	m := &discord.Member{ServerID: serverID, User: u.user, RoleIDs: []string{}, JoinedAt: w.now()}
	s.members[userID] = m
	s.memberOrder = append(s.memberOrder, userID)
	w.emit(discord.MemberJoinEvent{Member: cloneMember(*m)})
	return m, nil
}

// RemoveMember makes userID leave serverID (emits MemberLeaveEvent). When a bot leaves,
// its managed role is deleted, like Discord does.
func (w *World) RemoveMember(serverID, userID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	if s.members[userID] == nil {
		return notFound("unknown member %s", userID)
	}
	if userID == s.info.OwnerID {
		return badRequest("the owner cannot leave the server")
	}
	w.dropMember(s, userID)
	return nil
}

func (w *World) dropMember(s *serverState, userID string) {
	m := s.members[userID]
	// Removed first: a bot that leaves gets no event about itself (Discord would send
	// GUILD_DELETE, which the contract does not model).
	delete(s.members, userID)
	s.memberOrder = removeString(s.memberOrder, userID)
	w.emit(discord.MemberLeaveEvent{ServerID: s.info.ID, User: m.User})
	for roleID, botID := range s.managedBy {
		if botID == userID {
			w.removeRole(s, roleID)
		}
	}
}

// AddBotToServer simulates an OAuth bot invite: the bot joins the server and, when
// perms is non-zero, gets a managed role named after it with those permissions,
// placed at the top of the hierarchy. It returns the managed role (zero Role if none).
func (w *World) AddBotToServer(serverID, botUserID string, perms discord.Permissions) (discord.Role, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return discord.Role{}, err
	}
	u := w.users[botUserID]
	if u == nil || !u.user.Bot {
		return discord.Role{}, badRequest("%s is not a bot user", botUserID)
	}
	if s.members[botUserID] != nil {
		return discord.Role{}, badRequest("bot %s is already in server %s", botUserID, serverID)
	}
	if s.bans[botUserID] {
		return discord.Role{}, forbidden("bot %s is banned from server %s", botUserID, serverID)
	}
	var role discord.Role
	if perms != 0 {
		snap := snapshotRoles(s)
		r := &discord.Role{ID: w.newID(), Name: u.user.Username, Permissions: perms, Managed: true}
		s.roles[r.ID] = r
		s.managedBy[r.ID] = botUserID
		w.layoutRoles(s, map[string]int{r.ID: len(s.roles) - 1}, []string{r.ID})
		w.emit(discord.RoleCreateEvent{ServerID: serverID, Role: *r})
		w.emitRoleChanges(s, snap, r.ID)
		role = *r
	}
	m := &discord.Member{ServerID: serverID, User: u.user, RoleIDs: []string{}, JoinedAt: w.now()}
	if role.ID != "" {
		m.RoleIDs = append(m.RoleIDs, role.ID)
	}
	s.members[botUserID] = m
	s.memberOrder = append(s.memberOrder, botUserID)
	w.emit(discord.MemberJoinEvent{Member: cloneMember(*m)})
	return role, nil
}

func (w *World) getMember(a actor, serverID, userID string) (discord.Member, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return discord.Member{}, err
	}
	if s.members[a.userID] == nil {
		return discord.Member{}, forbidden("missing access to server %s", serverID)
	}
	m := s.members[userID]
	if m == nil {
		return discord.Member{}, notFound("unknown member %s", userID)
	}
	return cloneMember(*m), nil
}

func (w *World) updateMember(a actor, serverID, userID string, p discord.MemberParams, reason string) (discord.Member, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) (discord.Member, error) {
		return discord.Member{}, w.record(a, "UpdateMember", serverID, userID, reason, err)
	}
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if s.members[a.userID] == nil {
		return fail(forbidden("missing access to server %s", serverID))
	}
	m := s.members[userID]
	if m == nil {
		return fail(notFound("unknown member %s", userID))
	}
	prev := cloneMember(*m)
	next := cloneMember(*m)
	if p.Nick != nil {
		if utf8.RuneCountInString(*p.Nick) > 32 {
			return fail(badRequest("nickname must be at most 32 characters"))
		}
		if userID == a.userID {
			have := w.basePerms(s, a.userID)
			if !have.Has(permChangeNickname) && !have.Has(permManageNicknames) {
				return fail(missingErr(have, permChangeNickname))
			}
		} else {
			if err := w.requireServer(a, s, permManageNicknames); err != nil {
				return fail(err)
			}
			if err := w.requireMemberAbove(a, s, userID); err != nil {
				return fail(err)
			}
		}
		next.Nick = *p.Nick
	}
	if p.TimeoutUntil != nil {
		if err := w.requireServer(a, s, permModerate); err != nil {
			return fail(err)
		}
		if userID == a.userID {
			return fail(forbidden("cannot time out yourself"))
		}
		if err := w.requireMemberAbove(a, s, userID); err != nil {
			return fail(err)
		}
		if p.TimeoutUntil.IsZero() {
			next.TimeoutUntil = nil
		} else {
			if w.rawBasePerms(s, userID) == allPermissions {
				return fail(forbidden("cannot time out an administrator"))
			}
			until := *p.TimeoutUntil
			if until.After(w.now().Add(maxTimeout)) {
				return fail(badRequest("timeout cannot exceed 28 days"))
			}
			next.TimeoutUntil = &until
		}
	}
	*m = next
	w.recordOK(a, "UpdateMember", serverID, userID, reason)
	w.emit(discord.MemberUpdateEvent{Member: cloneMember(*m), Previous: &prev})
	return cloneMember(*m), nil
}

func (w *World) memberRole(a actor, add bool, serverID, userID, roleID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	method := "RemoveMemberRole"
	if add {
		method = "AddMemberRole"
	}
	fail := func(err error) error { return w.record(a, method, serverID, userID, reason, err) }
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permManageRoles); err != nil {
		return fail(err)
	}
	m := s.members[userID]
	if m == nil {
		return fail(notFound("unknown member %s", userID))
	}
	r := s.roles[roleID]
	if r == nil {
		return fail(notFound("unknown role %s", roleID))
	}
	if roleID == serverID {
		return fail(badRequest("cannot assign @everyone"))
	}
	if r.Managed {
		return fail(forbidden("cannot assign or remove managed role %s", r.Name))
	}
	if err := w.requireAbove(a, s, r.Position, "role "+r.Name); err != nil {
		return fail(err)
	}
	w.recordOK(a, method, serverID, userID, reason)
	if m.HasRole(roleID) == add {
		return nil // already in the desired state: Discord returns 204 without an event
	}
	prev := cloneMember(*m)
	if add {
		m.RoleIDs = append(m.RoleIDs, roleID)
	} else {
		m.RoleIDs = removeString(m.RoleIDs, roleID)
	}
	w.emit(discord.MemberUpdateEvent{Member: cloneMember(*m), Previous: &prev})
	return nil
}

func (w *World) kickMember(a actor, serverID, userID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) error { return w.record(a, "KickMember", serverID, userID, reason, err) }
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permKick); err != nil {
		return fail(err)
	}
	if s.members[userID] == nil {
		return fail(notFound("unknown member %s", userID))
	}
	if userID == a.userID {
		return fail(badRequest("cannot kick yourself"))
	}
	if err := w.requireMemberAbove(a, s, userID); err != nil {
		return fail(err)
	}
	w.recordOK(a, "KickMember", serverID, userID, reason)
	w.dropMember(s, userID)
	return nil
}

func (w *World) banMember(a actor, serverID, userID string, deleteSeconds int, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	fail := func(err error) error { return w.record(a, "BanMember", serverID, userID, reason, err) }
	s, err := w.server(serverID)
	if err != nil {
		return fail(err)
	}
	if err := w.requireServer(a, s, permBan); err != nil {
		return fail(err)
	}
	if w.users[userID] == nil {
		return fail(notFound("unknown user %s", userID))
	}
	if userID == a.userID {
		return fail(badRequest("cannot ban yourself"))
	}
	if s.members[userID] != nil {
		if err := w.requireMemberAbove(a, s, userID); err != nil {
			return fail(err)
		}
	}
	deleteSeconds = min(max(deleteSeconds, 0), 604800) // clamped, like the disgo adapter
	w.recordOK(a, "BanMember", serverID, userID, reason)
	s.bans[userID] = true
	if s.members[userID] != nil {
		w.dropMember(s, userID)
	}
	if deleteSeconds > 0 {
		cutoff := w.now().Add(-time.Duration(deleteSeconds) * time.Second)
		for _, cid := range s.channelIDs {
			c := w.channels[cid]
			kept := c.msgIDs[:0:0]
			for _, mid := range c.msgIDs {
				ms := w.messages[mid]
				if ms.msg.Author.ID == userID && !ms.msg.CreatedAt.Before(cutoff) {
					delete(w.messages, mid)
					w.emit(discord.MessageDeleteEvent{ID: mid, ChannelID: cid, ServerID: s.info.ID})
					continue
				}
				kept = append(kept, mid)
			}
			c.msgIDs = kept
		}
	}
	return nil
}

// ───────────────────────────────────────────────────────────── users

func (w *World) getUser(userID string) (discord.User, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := w.users[userID]
	if u == nil {
		return discord.User{}, notFound("unknown user %s", userID)
	}
	return u.user, nil
}

func (w *World) updateSelf(a actor, p discord.SelfParams) (discord.User, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := w.users[a.userID]
	if u == nil {
		return discord.User{}, notFound("unknown user %s", a.userID)
	}
	if p.Username != nil {
		if n := utf8.RuneCountInString(*p.Username); n < 2 || n > 32 {
			return discord.User{}, w.record(a, "UpdateSelf", "", a.userID, "", badRequest("username must be 2-32 characters"))
		}
		u.user.Username = *p.Username
	}
	if p.Avatar != nil {
		if len(*p.Avatar) == 0 {
			u.user.AvatarURL = ""
		} else {
			u.user.AvatarURL = "https://cdn.discordapp.com/avatars/" + a.userID + "/" + imageHash(*p.Avatar) + ".png"
		}
	}
	if p.Banner != nil {
		if len(*p.Banner) == 0 {
			u.bannerURL = ""
		} else {
			u.bannerURL = "https://cdn.discordapp.com/banners/" + a.userID + "/" + imageHash(*p.Banner) + ".png"
		}
	}
	// Keep member copies in sync with the user.
	for _, s := range w.servers {
		if m := s.members[a.userID]; m != nil {
			m.User = u.user
		}
	}
	w.recordOK(a, "UpdateSelf", "", a.userID, "")
	return u.user, nil
}

var (
	presenceStatuses = map[string]bool{"online": true, "idle": true, "dnd": true, "invisible": true}
	activityTypes    = map[string]bool{"": true, "playing": true, "streaming": true, "listening": true, "watching": true, "competing": true, "custom": true}
)

func (w *World) setPresence(a actor, p discord.Presence) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.users[a.userID] == nil {
		return notFound("unknown user %s", a.userID)
	}
	if !presenceStatuses[p.Status] {
		return w.record(a, "SetPresence", "", a.userID, "", badRequest("invalid status %q", p.Status))
	}
	if !activityTypes[p.ActivityType] {
		return w.record(a, "SetPresence", "", a.userID, "", badRequest("invalid activity type %q", p.ActivityType))
	}
	w.users[a.userID].presence = &p
	return w.record(a, "SetPresence", "", a.userID, "", nil)
}

// UnbanMember lifts a ban (as the server owner).
func (w *World) UnbanMember(serverID, userID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, err := w.server(serverID)
	if err != nil {
		return err
	}
	if !s.bans[userID] {
		return notFound("unknown ban %s", userID)
	}
	delete(s.bans, userID)
	w.changed()
	return nil
}

// Bans returns the banned user IDs of a server, sorted.
func (w *World) Bans(serverID string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.servers[serverID]
	if s == nil {
		return nil
	}
	out := sortedKeys(s.bans)
	sort.Strings(out)
	return out
}
