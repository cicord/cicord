package reconcile

import (
	"fmt"
	"slices"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// Role holders (roles.<key>.members) are additive: each listed user who is in the
// server gets the role when they lack it. Holders who are not listed are left alone,
// and removing a user from the list never removes the role: Cicord does not own the
// member list of a role.

// memberRoleKey is the change key of a role given to a member.
func memberRoleKey(roleKey, userID string) string { return roleKey + "/" + userID }

// memberLabel is "@Alice (123…)" for a fetched member.
func memberLabel(m discord.Member, userID string) string {
	if name := m.DisplayName(); name != "" {
		return "@" + name + " (" + userID + ")"
	}
	return userID
}

// planRoleMembers gives the declared roles to the listed members who lack them. Members
// are only compared when they were fetched (Snapshot.HasMembers).
func (p *planner) planRoleMembers() {
	live := p.in.Live
	if !live.HasMembers {
		return
	}
	for _, key := range sortedKeys(p.sp.Roles) {
		spec := p.sp.Roles[key]
		if len(spec.Members) == 0 {
			continue
		}
		how := p.how[state.KindRole][key]
		if how == "skip" {
			continue // owned by a plugin (blocked by planRoles)
		}
		var role discord.Role
		if how != "create" {
			role = p.rolesByID[p.ids[state.KindRole][key]]
		}
		for _, userID := range spec.Members {
			m, in := live.Members[userID]
			if !in {
				p.notes = append(p.notes, Note{Kind: KindMemberRole, Key: memberRoleKey(key, userID),
					Message: fmt.Sprintf("roles.%s.members: user %s is not in the server; they get the role %q once they join", key, userID, spec.Name)})
				continue
			}
			if (how != "create" && m.HasRole(role.ID)) != p.in.Suspended[userID] {
				continue
			}
			ck := memberRoleKey(key, userID)
			summary := fmt.Sprintf("give role %q to %s", spec.Name, memberLabel(m, userID))
			fields := map[string]FieldChange{"assigned": {From: false, To: true}}
			if p.in.Suspended[userID] {
				summary = fmt.Sprintf("suspend role %q for %s", spec.Name, memberLabel(m, userID))
				fields["assigned"] = FieldChange{From: true, To: false}
			}
			switch {
			case how != "create" && role.Managed:
				p.blocked = append(p.blocked, Blocked{Action: ActionUpdate, Kind: KindMemberRole, Key: ck, ID: userID,
					Reason: "managed: role belongs to an integration", Summary: summary, Fields: fields})
				continue
			case how != "create" && role.Position >= p.botTop:
				p.blocked = append(p.blocked, Blocked{Action: ActionUpdate, Kind: KindMemberRole, Key: ck, ID: userID,
					Reason: fmt.Sprintf("hierarchy: role %q is at or above the bot's highest role", role.Name), Summary: summary, Fields: fields})
				continue
			}
			p.add(Change{
				Action: ActionUpdate, Kind: KindMemberRole, Key: ck, ID: userID,
				Summary: summary,
				Fields:  fields,
				op:      &op{phase: phaseRoleMembers, member: &memberWant{roleKey: key, userID: userID, remove: p.in.Suspended[userID]}},
			})
		}
	}
}

// listedRoles returns the keys of the roles that list userID in members (sorted).
// Caller holds emu.
func (r *Reconciler) listedRoles(userID string) []string {
	var keys []string
	for key, spec := range r.cfg.Spec.Roles {
		if slices.Contains(spec.Members, userID) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// memberDrift reports whether a member event concerns a listed user who lacks one of
// their roles: a listed user who joins, or one whose managed role was taken away.
// Caller holds emu.
func (r *Reconciler) memberDrift(m discord.Member, joined bool) bool {
	keys := r.listedRoles(m.User.ID)
	if joined || len(keys) == 0 {
		return len(keys) > 0
	}
	for _, key := range keys {
		if id, ok := r.roleIDs[key]; ok && m.HasRole(id) == r.suspended[m.User.ID] {
			return true
		}
	}
	return false
}
