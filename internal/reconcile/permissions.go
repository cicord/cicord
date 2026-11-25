package reconcile

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// Permissions Discord checks when a bot that is not Administrator changes a server.
var (
	permAdministrator     = discord.MustPermissions("Administrator")
	permView              = discord.MustPermissions("ViewChannel")
	permManageRoles       = discord.MustPermissions("ManageRoles")
	permManageChannels    = discord.MustPermissions("ManageChannels")
	permManageServer      = discord.MustPermissions("ManageServer")
	permManageExpressions = discord.MustPermissions("ManageExpressions")
	permCreateExpressions = discord.MustPermissions("CreateExpressions")
	allPermissions        = discord.Permissions(^uint64(0))
)

// BotTarget is the overwrite target that names the bot's own managed role (the role
// Discord creates when the bot is invited). It keeps a bot that is not Administrator in
// the private channels it manages.
const BotTarget = "@bot"

// Requirements is a set of Discord permissions, each with the configuration paths that
// need it ("roles.moderator.permissions", "channels.staff.overwrites.@everyone",
// "plugin moderation: discord.members.moderate"…).
type Requirements struct {
	Permissions discord.Permissions `json:"-"`
	// Sources maps a permission name to the sorted paths that need it.
	Sources map[string][]string `json:"sources"`
}

// Add records that source needs the permissions p.
func (r *Requirements) Add(p discord.Permissions, source string) {
	if p == 0 {
		return
	}
	r.Permissions |= p
	if r.Sources == nil {
		r.Sources = map[string][]string{}
	}
	for _, n := range p.Names() {
		if i, found := slices.BinarySearch(r.Sources[n], source); !found {
			r.Sources[n] = slices.Insert(r.Sources[n], i, source)
		}
	}
}

// Merge adds every requirement of o.
func (r *Requirements) Merge(o Requirements) {
	for n, srcs := range o.Sources {
		p := discord.MustPermissions(n)
		for _, s := range srcs {
			r.Add(p, s)
		}
	}
}

// Missing returns the requirements that have does not cover. An Administrator has
// every permission.
func (r Requirements) Missing(have discord.Permissions) Requirements {
	var out Requirements
	if have.Has(permAdministrator) {
		return out
	}
	for n, srcs := range r.Sources {
		p := discord.MustPermissions(n)
		if have.Has(p) {
			continue
		}
		for _, s := range srcs {
			out.Add(p, s)
		}
	}
	return out
}

// Names are the required permission names (sorted).
func (r Requirements) Names() []string { return r.Permissions.Names() }

// Describe renders one line per permission: "BanMembers (roles.moderator.permissions)".
func (r Requirements) Describe() []string {
	var out []string
	for _, n := range r.Names() {
		srcs := r.Sources[n]
		line := n
		if len(srcs) > 0 {
			shown := srcs
			if len(shown) > 3 {
				shown = shown[:3]
			}
			line += " (" + strings.Join(shown, ", ")
			if extra := len(srcs) - len(shown); extra > 0 {
				line += fmt.Sprintf(" and %d more", extra)
			}
			line += ")"
		}
		out = append(out, line)
	}
	return out
}

// ServerRequirements returns every permission a bot that is not Administrator needs to
// manage the desired state of a server: the permissions granted or denied by roles,
// @everyone and overwrites (Discord only lets a bot grant what it holds), Manage Roles /
// Manage Channels for the roles and channels, Manage Server for the server settings,
// AutoMod, onboarding and the welcome screen, and Manage Expressions for emojis,
// stickers and sounds. A channel overwrite that allows or denies Manage Roles needs
// Administrator (Discord only lets administrators put it in an overwrite).
//
// It is what to give the bot's role. The sync preflight (Plan) checks only what a plan
// actually sends, so an unchanged role holding a permission the bot lacks does not
// block a sync.
func ServerRequirements(sp *config.ServerSpec) (Requirements, error) {
	var r Requirements
	if sp == nil {
		return r, nil
	}
	if sp.Everyone != nil {
		p, err := discord.ParsePermissions(sp.Everyone.Permissions)
		if err != nil {
			return r, fmt.Errorf("everyone.permissions: %w", err)
		}
		r.Add(permManageRoles, "everyone")
		r.Add(p, "everyone.permissions")
	}
	for _, k := range sortedKeys(sp.Roles) {
		p, err := discord.ParsePermissions(sp.Roles[k].Permissions)
		if err != nil {
			return r, fmt.Errorf("roles.%s.permissions: %w", k, err)
		}
		r.Add(permManageRoles, "roles."+k)
		r.Add(p, "roles."+k+".permissions")
	}
	overwrites := func(base string, ows map[string]config.OverwriteSpec) error {
		for _, t := range sortedKeys(ows) {
			allow, err := discord.ParsePermissions(ows[t].Allow)
			if err != nil {
				return fmt.Errorf("%s.overwrites.%s.allow: %w", base, t, err)
			}
			deny, err := discord.ParsePermissions(ows[t].Deny)
			if err != nil {
				return fmt.Errorf("%s.overwrites.%s.deny: %w", base, t, err)
			}
			if allow|deny == 0 {
				continue
			}
			src := base + ".overwrites." + t
			r.Add(permManageRoles, src)
			r.Add(allow|deny, src)
			if (allow|deny)&permManageRoles != 0 {
				r.Add(permAdministrator, src)
			}
		}
		return nil
	}
	for _, k := range sortedKeys(sp.Categories) {
		r.Add(permView|permManageChannels, "categories."+k)
		if err := overwrites("categories."+k, sp.Categories[k].Overwrites); err != nil {
			return r, err
		}
	}
	for _, k := range sortedKeys(sp.Channels) {
		r.Add(permView|permManageChannels, "channels."+k)
		if err := overwrites("channels."+k, sp.Channels[k].Overwrites); err != nil {
			return r, err
		}
	}
	if sp.Profile != nil && *sp.Profile != (config.ServerProfile{}) {
		r.Add(permManageServer, "profile")
	}
	for name, set := range map[string]bool{
		"afk": sp.AFK != nil, "widget": sp.Widget != nil, "community": sp.Community != nil,
		"welcomeScreen": sp.WelcomeScreen != nil,
	} {
		if set {
			r.Add(permManageServer, name)
		}
	}
	if sp.Onboarding != nil {
		r.Add(permManageServer|permManageRoles, "onboarding")
	}
	for _, k := range sortedKeys(sp.AutoMod) {
		r.Add(permManageServer, "automod."+k)
	}
	for _, k := range sortedKeys(sp.Emojis) {
		r.Add(permManageExpressions, "emojis."+k)
	}
	for _, k := range sortedKeys(sp.Stickers) {
		r.Add(permManageExpressions, "stickers."+k)
	}
	for _, k := range sortedKeys(sp.Soundboard) {
		r.Add(permManageExpressions, "soundboard."+k)
	}
	return r, nil
}

// BasePermissions computes the server-level permissions of a member holding roleIDs
// (@everyone included implicitly); the owner and administrators have every permission.
func BasePermissions(serverID string, roles []discord.Role, roleIDs []string, owner bool) discord.Permissions {
	if owner {
		return allPermissions
	}
	var p discord.Permissions
	for _, r := range roles {
		if r.IsEveryone(serverID) || slices.Contains(roleIDs, r.ID) {
			p |= r.Permissions
		}
	}
	if p.Has(permAdministrator) {
		return allPermissions
	}
	return p
}

// BotRole returns the bot's managed role (the integration role Discord gives a bot on
// invite): a managed role the bot member holds.
func (s Snapshot) BotRole() (discord.Role, bool) {
	for _, r := range s.Roles {
		if r.Managed && slices.Contains(s.BotRoleIDs, r.ID) {
			return r, true
		}
	}
	return discord.Role{}, false
}

// ───────────────────────────────────────────────────────────── preflight

// Lockout is a managed category or channel the bot cannot (or, after the plan, could
// no longer) see or manage.
type Lockout struct {
	// Path is "channels.<key>" or "categories.<key>".
	Path string `json:"path"`
	// Lost are the permissions the bot lacks in it.
	Lost []string `json:"lost"`
	// Now is true when the bot already lacks them (Cicord cannot fix that itself);
	// false when the planned overwrites would remove them.
	Now bool `json:"now"`
}

// PermissionsError is returned by Plan and Sync, before anything is applied, when the
// bot is not Administrator and the plan needs permissions it lacks, or would leave the
// bot unable to see or manage a channel it manages.
type PermissionsError struct {
	Server   string
	Missing  Requirements
	Lockouts []Lockout
}

func (e *PermissionsError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "reconcile %s: preflight: ", e.Server)
	var parts []string
	if names := e.Missing.Names(); len(names) > 0 {
		parts = append(parts, "the bot lacks permissions this sync needs: "+strings.Join(names, ", "))
	}
	if len(e.Lockouts) > 0 {
		paths := make([]string, len(e.Lockouts))
		for i, l := range e.Lockouts {
			paths[i] = l.Path
		}
		parts = append(parts, "the bot cannot see or manage "+strings.Join(paths, ", "))
	}
	b.WriteString(strings.Join(parts, "; "))
	for _, l := range e.Missing.Describe() {
		b.WriteString("\n  needs " + l)
	}
	for _, l := range e.Lockouts {
		if l.Now {
			fmt.Fprintf(&b, "\n  %s: the bot lacks %s there now", l.Path, strings.Join(l.Lost, ", "))
		} else {
			fmt.Fprintf(&b, "\n  %s: the planned overwrites would remove %s from the bot (allow them to %q)", l.Path, strings.Join(l.Lost, ", "), BotTarget)
		}
	}
	return b.String()
}

// preflight checks what the plan sends against the bot's live permissions, the way
// Discord does for a bot that is not Administrator: Manage Roles / Manage Channels /
// Manage Server / Manage Expressions for the calls, only permissions the bot holds when
// a role or an overwrite gains them, Administrator for Manage Roles in an overwrite, and
// View Channel + Manage Channels in every channel it edits. Nil when the bot is unknown,
// the owner or Administrator.
func (p *planner) preflight(changes []Change) *PermissionsError {
	live := p.in.Live
	if !live.HasBot {
		return nil
	}
	have := BasePermissions(p.serverID, live.Roles, live.BotRoleIDs, p.in.BotUserID != "" && live.Server.OwnerID == p.in.BotUserID)
	if have.Has(permAdministrator) {
		return nil
	}
	var need Requirements
	var lockouts []Lockout
	for _, c := range changes {
		o := c.op
		if o == nil || c.Action == ActionUnmanage {
			continue
		}
		switch c.Kind {
		case KindServer:
			need.Add(permManageServer, "server")
		case KindWidget, KindWelcomeScreen:
			need.Add(permManageServer, c.Kind)
		case KindOnboarding:
			need.Add(permManageServer|permManageRoles, c.Kind)
		case KindAutoMod:
			need.Add(permManageServer, "automod."+c.Key)
		case KindEmoji, KindSticker, KindSound:
			src := map[string]string{KindEmoji: "emojis.", KindSticker: "stickers.", KindSound: "soundboard."}[c.Kind] + c.Key
			if c.Action == ActionCreate {
				// Create Expressions is enough to upload one (Manage Expressions covers it too).
				if !have.Has(permManageExpressions) {
					need.Add(permCreateExpressions, src)
				}
			} else {
				need.Add(permManageExpressions, src)
			}
		case KindEveryone:
			need.Add(permManageRoles, "everyone")
			if o.role != nil && o.role.Permissions != nil {
				need.Add(*o.role.Permissions&^p.rolesByID[p.serverID].Permissions, "everyone.permissions")
			}
		case KindRole:
			if c.Action == ActionAdopt && o.role == nil {
				continue // only the mapping is recorded
			}
			src := "roles." + c.Key
			if c.Action == ActionReorder {
				src = "hierarchy"
			}
			need.Add(permManageRoles, src)
			if o.role != nil && o.role.Permissions != nil {
				added := *o.role.Permissions
				if c.Action != ActionCreate {
					added &^= p.rolesByID[c.ID].Permissions
				}
				need.Add(added, src+".permissions")
			}
		case KindMemberRole:
			need.Add(permManageRoles, "roles."+c.op.member.roleKey+".members")
		case KindCategory, KindChannel:
			src := map[string]string{KindCategory: "categories.", KindChannel: "channels."}[c.Kind] + c.Key
			if c.Action == ActionReorder {
				need.Add(permManageChannels, "positions of "+c.Key)
				continue
			}
			if (c.Action == ActionUpdate || c.Action == ActionAdopt) && len(o.send) == 0 {
				continue // only the mapping is recorded
			}
			need.Add(permManageChannels, src)
			var liveCh *discord.Channel
			if c.Action != ActionCreate {
				ch := p.chansByID[c.ID]
				liveCh = &ch
				if lost := p.lostIn(have, ch.Overwrites, nil); len(lost) > 0 {
					lockouts = append(lockouts, Lockout{Path: src, Lost: lost, Now: true})
					continue
				}
			}
			if c.Action == ActionDelete || o.ch == nil {
				continue
			}
			if c.Action == ActionCreate || o.send["overwrites"] {
				var old []discord.Overwrite
				if liveCh != nil {
					old = liveCh.Overwrites
				}
				changed := p.changedOverwriteBits(o.ch.overwrites, old)
				if changed != 0 {
					need.Add(permManageRoles|changed, src+".overwrites")
					if changed&permManageRoles != 0 {
						need.Add(permAdministrator, src+".overwrites")
					}
				}
				if lost := p.lostIn(have, nil, o.ch.overwrites); len(lost) > 0 {
					lockouts = append(lockouts, Lockout{Path: src, Lost: lost})
				}
			}
		}
	}
	missing := need.Missing(have)
	if len(missing.Sources) == 0 && len(lockouts) == 0 {
		return nil
	}
	return &PermissionsError{Server: p.serverKey, Missing: missing, Lockouts: lockouts}
}

// resolveOverwrite returns the live ID of a desired overwrite target ("" when the
// target is a role created by this plan).
func (p *planner) resolveOverwrite(o owSpec) string {
	if o.target.roleKey == "" {
		return o.target.id
	}
	return p.ids[state.KindRole][o.target.roleKey]
}

// changedOverwriteBits is what Discord checks when overwrites change: every allow/deny
// bit that differs between the live and the desired overwrites, per target.
func (p *planner) changedOverwriteBits(want []owSpec, live []discord.Overwrite) discord.Permissions {
	old := map[string]discord.Overwrite{}
	for _, o := range live {
		old[o.ID] = o
	}
	var changed discord.Permissions
	for _, w := range want {
		id := p.resolveOverwrite(w)
		o := old[id]
		if id == "" {
			o = discord.Overwrite{}
		}
		changed |= (o.Allow ^ w.allow) | (o.Deny ^ w.deny)
		delete(old, id)
	}
	for _, o := range old {
		changed |= o.Allow | o.Deny
	}
	return changed
}

// lostIn returns the permissions the bot needs in a channel it manages (View Channel,
// Manage Channels, and Manage Roles for the overwrites, among those it holds on the
// server) that the channel's overwrites take away. Either live or desired overwrites
// are given. Discord's order: @everyone overwrite, then the bot's role overwrites, then
// its member overwrite.
func (p *planner) lostIn(base discord.Permissions, live []discord.Overwrite, want []owSpec) []string {
	type ow struct {
		id          string
		typ         discord.OverwriteType
		allow, deny discord.Permissions
	}
	var ows []ow
	for _, o := range live {
		ows = append(ows, ow{o.ID, o.Type, o.Allow, o.Deny})
	}
	for _, o := range want {
		ows = append(ows, ow{p.resolveOverwrite(o), o.target.typ, o.allow, o.deny})
	}
	eff := base
	var roleAllow, roleDeny, memAllow, memDeny discord.Permissions
	for _, o := range ows {
		switch {
		case o.id == "":
		case o.typ == discord.OverwriteRole && o.id == p.serverID:
			eff = eff&^o.deny | o.allow
		case o.typ == discord.OverwriteRole && slices.Contains(p.in.Live.BotRoleIDs, o.id):
			roleAllow |= o.allow
			roleDeny |= o.deny
		case o.typ == discord.OverwriteMember && o.id == p.in.BotUserID:
			memAllow |= o.allow
			memDeny |= o.deny
		}
	}
	eff = eff&^roleDeny | roleAllow
	eff = eff&^memDeny | memAllow
	needed := (permView | permManageChannels | permManageRoles) & base
	needed |= permView
	lost := needed &^ eff
	if eff&permView == 0 {
		lost |= needed // nothing is usable in a channel the bot cannot see
	}
	names := lost.Names()
	sort.Strings(names)
	return names
}
