package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// iconState is what was recorded after the last icon upload.
type iconState struct{ Hash, URL string }

// planInput is everything computePlan needs. It is pure data.
type planInput struct {
	Desired *config.Server
	Live    Snapshot
	Mapped  []state.Resource // resource map entries of this server (all owners)
	// BotTop is the position of the bot's highest role (math.MaxInt = no constraint).
	BotTop int
	// Icon is the content of the desired icon file (nil = icon not managed).
	Icon      []byte
	IconState iconState
	// Files are the other repository files the plan needs, by logical name ("banner",
	// "splash", "role:<key>", "emoji:<key>", "sticker:<key>", "sound:<key>"), and
	// FileState what was recorded after their last upload (same names).
	Files     map[string][]byte
	FileState map[string]iconState
}

const reasonHierarchy = "hierarchy: role is at or above the bot's highest role"

// planner holds the indexes used while computing a plan.
type planner struct {
	in        planInput
	sp        *config.ServerSpec
	serverKey string
	serverID  string
	prune     bool
	protect   bool
	botTop    int

	rolesByID map[string]discord.Role
	chansByID map[string]discord.Channel

	git       map[state.ResourceKind]map[string]state.Resource // git-owned mappings
	foreign   map[state.ResourceKind]map[string]state.Resource // other owners
	mappedIDs map[string]bool                                  // IDs in the map, any owner
	consumed  map[string]bool                                  // IDs adopted by this plan
	orphanIDs map[string]bool                                  // git mappings whose key left the config
	keyByID   map[string]string                                // live ID → key (git or adopted)

	// plan-time resolution: key → live ID (mapped & alive, or adopted). Missing = created.
	ids map[state.ResourceKind]map[string]string
	// how each desired key is reached
	how map[state.ResourceKind]map[string]string // "existing" | "adopt" | "create" | "skip"

	buckets map[phase][]Change
	blocked []Blocked

	// Community state: live now, wanted after the plan (and not blocked).
	communityLive, communityAfter bool
	// communityMoves: verification / explicit filter changes go with the Community toggle.
	communityMoves bool
}

func computePlan(in planInput) (Plan, error) {
	if in.Desired == nil {
		return Plan{}, fmt.Errorf("reconcile: no desired server")
	}
	p := &planner{
		in:        in,
		sp:        &in.Desired.Spec,
		serverKey: in.Desired.Metadata.Name,
		serverID:  in.Live.Server.ID,
		prune:     in.Desired.Spec.SyncPolicy.Prune(),
		protect:   in.Desired.Spec.SyncPolicy.DeletionProtection(),
		botTop:    in.BotTop,
		rolesByID: map[string]discord.Role{},
		chansByID: map[string]discord.Channel{},
		git:       map[state.ResourceKind]map[string]state.Resource{},
		foreign:   map[state.ResourceKind]map[string]state.Resource{},
		mappedIDs: map[string]bool{},
		orphanIDs: map[string]bool{},
		consumed:  map[string]bool{},
		keyByID:   map[string]string{},
		ids:       map[state.ResourceKind]map[string]string{},
		how:       map[state.ResourceKind]map[string]string{},
		buckets:   map[phase][]Change{},
	}
	if p.serverID == "" {
		p.serverID = in.Desired.Spec.DiscordID
	}
	for _, k := range allKinds {
		p.git[k], p.foreign[k], p.ids[k], p.how[k] = map[string]state.Resource{}, map[string]state.Resource{}, map[string]string{}, map[string]string{}
	}
	for _, r := range in.Live.Roles {
		p.rolesByID[r.ID] = r
	}
	for _, c := range in.Live.Channels {
		p.chansByID[c.ID] = c
	}
	for _, r := range in.Mapped {
		if _, ok := p.git[r.Kind]; !ok {
			continue
		}
		p.mappedIDs[r.ID] = true
		if r.Owner == state.OwnerGit {
			p.git[r.Kind][r.Key] = r
			p.keyByID[r.ID] = r.Key
		} else {
			p.foreign[r.Kind][r.Key] = r
		}
	}

	// A git-owned resource whose key disappeared from the config can be re-adopted by name
	// under a new key: renaming a key in YAML (or with `cicord mv`) moves the mapping
	// instead of creating a duplicate and orphaning the original.
	for rk, byKey := range p.git {
		for key, m := range byKey {
			if !p.declared(rk, key) {
				p.orphanIDs[m.ID] = true
			}
		}
	}

	if err := p.resolveIdentities(); err != nil {
		return Plan{}, err
	}
	p.resolveParity()
	p.planCommunityState()
	p.planServer()
	if err := p.planEveryone(); err != nil {
		return Plan{}, err
	}
	if err := p.planRoles(); err != nil {
		return Plan{}, err
	}
	p.planRoleOrder()
	if err := p.planChannels(); err != nil {
		return Plan{}, err
	}
	p.planServerRefs()
	p.planChannelOrder()
	p.planWidget()
	p.planWelcomeScreen()
	p.planOnboarding()
	if err := p.planAutoMod(); err != nil {
		return Plan{}, err
	}
	p.planExpressions()
	p.planOrphans()

	plan := Plan{Server: p.serverKey, Changes: []Change{}, Blocked: p.blocked}
	if plan.Blocked == nil {
		plan.Blocked = []Blocked{}
	}
	for ph := phaseServer; ph <= phaseUnmanage; ph++ {
		plan.Changes = append(plan.Changes, p.buckets[ph]...)
	}
	return plan, nil
}

func (p *planner) add(c Change) { p.buckets[c.op.phase] = append(p.buckets[c.op.phase], c) }

func (p *planner) block(action, kind, key, id, reason string) {
	p.blocked = append(p.blocked, Blocked{Action: action, Kind: kind, Key: key, ID: id, Reason: reason})
}

// blockFields is block with the differences that cannot be applied.
func (p *planner) blockFields(action, kind, key, id, reason string, fields map[string]FieldChange) {
	p.blocked = append(p.blocked, Blocked{Action: action, Kind: kind, Key: key, ID: id, Reason: reason, Fields: fields})
}

// ───────────────────────────────────────────────────────────── identities

func (p *planner) resolveIdentities() error {
	// roles
	for _, key := range sortedKeys(p.sp.Roles) {
		spec := p.sp.Roles[key]
		p.resolve(state.KindRole, key, func(id string) bool {
			r, ok := p.rolesByID[id]
			return ok && r.ID != p.serverID
		}, func() string {
			var cands []string
			for _, r := range p.in.Live.Roles {
				if r.ID != p.serverID && !r.Managed && r.Name == spec.Name {
					cands = append(cands, r.ID)
				}
			}
			return p.pick(cands)
		})
	}
	// categories
	for _, key := range sortedKeys(p.sp.Categories) {
		spec := p.sp.Categories[key]
		p.resolve(state.KindCategory, key, func(id string) bool {
			c, ok := p.chansByID[id]
			return ok && c.Type == discord.ChannelCategory
		}, func() string {
			var cands []string
			for _, c := range p.in.Live.Channels {
				if c.Type == discord.ChannelCategory && c.Name == spec.Name {
					cands = append(cands, c.ID)
				}
			}
			return p.pick(cands)
		})
	}
	// channels
	for _, key := range sortedKeys(p.sp.Channels) {
		spec := p.sp.Channels[key]
		typ, err := discord.ParseChannelType(spec.Type)
		if err != nil {
			return fmt.Errorf("channels.%s: %w", key, err)
		}
		p.resolve(state.KindChannel, key, func(id string) bool {
			c, ok := p.chansByID[id]
			return ok && c.Type != discord.ChannelCategory
		}, func() string {
			var cands []string
			for _, c := range p.in.Live.Channels {
				if c.Type == typ && c.Name == normalizeChannelName(typ, spec.Name) {
					cands = append(cands, c.ID)
				}
			}
			return p.pick(cands)
		})
	}
	return nil
}

// resolve decides how a desired key is reached: an existing mapped resource, an
// adoption, a creation, or nothing (owned by a plugin).
func (p *planner) resolve(kind state.ResourceKind, key string, alive func(id string) bool, adopt func() string) {
	if f, ok := p.foreign[kind][key]; ok {
		p.how[kind][key] = "skip"
		p.block(ActionUpdate, string(kind), key, f.ID, "ownership: key is owned by "+f.Owner)
		return
	}
	if m, ok := p.git[kind][key]; ok {
		if alive(m.ID) {
			p.how[kind][key] = "existing"
			p.ids[kind][key] = m.ID
			return
		}
		// The mapped resource was deleted in Discord. Only AutoMod rules may then adopt a
		// matching unmapped one (a once-per-server rule recreated by hand). A role or
		// channel is recreated: adopting a same-named one would hand the managed
		// permissions to whoever created it and its members.
		if kind != state.KindAutoMod {
			p.how[kind][key] = "create"
			return
		}
	}
	if id := adopt(); id != "" {
		p.consumed[id] = true
		p.how[kind][key] = "adopt"
		p.ids[kind][key] = id
		p.keyByID[id] = key
		return
	}
	p.how[kind][key] = "create"
}

// pick returns the first adoptable candidate (ID order).
func (p *planner) pick(cands []string) string {
	slices.SortFunc(cands, idLess)
	for _, id := range cands {
		if (!p.mappedIDs[id] || p.orphanIDs[id]) && !p.consumed[id] {
			return id
		}
	}
	return ""
}

// ───────────────────────────────────────────────────────────── server

func (p *planner) planServer() {
	pr := p.sp.Profile
	if pr == nil {
		pr = &config.ServerProfile{}
	}
	live := p.in.Live.Server
	fields := map[string]FieldChange{}
	ss := &serverSpec{}
	str := func(name, want, have string, dst **string) {
		if want != "" && want != have {
			fields[name] = FieldChange{From: have, To: want}
			w := want
			*dst = &w
		}
	}
	str("name", pr.Name, live.Name, &ss.name)
	str("description", pr.Description, live.Description, &ss.description)
	if !p.communityMoves {
		str("verificationLevel", pr.VerificationLevel, live.VerificationLevel, &ss.verification)
		str("explicitContentFilter", pr.ExplicitContentFilter, live.ExplicitContentFilter, &ss.explicit)
	}
	str("defaultNotifications", pr.DefaultNotifications, live.DefaultNotifications, &ss.notifications)
	str("preferredLocale", pr.PreferredLocale, live.PreferredLocale, &ss.locale)
	if b := pr.PremiumProgressBar; b != nil && *b != live.PremiumProgressBar {
		fields["premiumProgressBar"] = FieldChange{From: live.PremiumProgressBar, To: *b}
		v := *b
		ss.progressBar = &v
	}
	if f := pr.SystemChannelFlags; f != nil {
		want := applySystemFlags(live.SystemChannelFlags, *f)
		if want != live.SystemChannelFlags {
			fields["systemChannelFlags"] = FieldChange{From: systemFlagsDisplay(live.SystemChannelFlags), To: systemFlagsDisplay(want)}
			ss.flags = &want
		}
	}
	if a := p.sp.AFK; a != nil && a.Timeout > 0 && a.Timeout != live.AFKTimeout {
		fields["afk.timeout"] = FieldChange{From: live.AFKTimeout, To: a.Timeout}
		t := a.Timeout
		ss.afkTimeout = &t
	}
	if pr.Icon != "" && p.in.Icon != nil {
		sum := sha256.Sum256(p.in.Icon)
		h := hex.EncodeToString(sum[:])
		st := p.in.IconState
		if live.IconURL == "" || st.Hash != h || st.URL != live.IconURL {
			fc := FieldChange{From: nilIfEmpty(live.IconURL), To: pr.Icon}
			if isGIF(p.in.Icon) && !live.HasBoostFeature(discord.FeatureAnimatedIcon) {
				p.blockFields(ActionUpdate, KindServer, p.serverKey, p.serverID,
					"boost: "+live.BoostReason("an animated icon", discord.FeatureAnimatedIcon), map[string]FieldChange{"icon": fc})
			} else {
				fields["icon"] = fc
				ss.icon, ss.iconHash = p.in.Icon, h
			}
		}
	}
	p.serverImage(fields, "banner", pr.Banner, live.BannerURL, discord.FeatureBanner, &ss.banner, &ss.bannerHash)
	p.serverImage(fields, "splash", pr.Splash, live.SplashURL, discord.FeatureInviteSplash, &ss.splash, &ss.splashHash)
	if len(fields) == 0 {
		return
	}
	name := pr.Name
	if name == "" {
		name = live.Name
	}
	p.add(Change{
		Action: ActionUpdate, Kind: KindServer, Key: p.serverKey, ID: p.serverID,
		Summary: fmt.Sprintf("update server %q: %s", name, fieldList(fields)),
		Fields:  fields,
		op:      &op{phase: phaseServer, server: ss},
	})
}

func (p *planner) planServerRefs() {
	pr := p.sp.Profile
	if pr == nil {
		pr = &config.ServerProfile{}
	}
	live := p.in.Live.Server
	fields := map[string]FieldChange{}
	ss := &serverSpec{}
	blocked := map[string]FieldChange{}
	ref := func(name, key, haveID string, dst **string, community bool) {
		if _, declared := p.sp.Channels[key]; key == "" || !declared {
			return // undeclared keys are reported by validation
		}
		id, ok := p.ids[state.KindChannel][key]
		if ok && id == haveID {
			return
		}
		fc := FieldChange{From: p.channelRefName(haveID), To: key}
		if community && !p.communityAfter {
			blocked[name] = fc
			return
		}
		if p.how[state.KindChannel][key] == "skip" {
			return // channel owned by a plugin or blocked
		}
		fields[name] = fc
		k := key
		*dst = &k
	}
	ref("systemChannel", pr.SystemChannel, live.SystemChannelID, &ss.systemKey, false)
	if a := p.sp.AFK; a != nil {
		ref("afk.channel", a.Channel, live.AFKChannelID, &ss.afkKey, false)
	}
	ref("rulesChannel", p.rulesChannelKey(), live.RulesChannelID, &ss.rulesKey, true)
	if c := p.sp.Community; c != nil {
		ref("community.updatesChannel", c.UpdatesChannel, live.PublicUpdatesChannelID, &ss.updatesKey, true)
		ref("community.safetyAlertsChannel", c.SafetyAlertsChannel, live.SafetyAlertsChannelID, &ss.safetyKey, true)
	}
	if len(blocked) > 0 {
		p.blockFields(ActionUpdate, KindServer, p.serverKey, p.serverID,
			"community: the rules, updates and safety alerts channels require the Community feature (set community.enabled: true)", blocked)
	}
	if p.communityAfter != p.communityLive {
		var feats []string
		for _, f := range live.Features {
			if discord.IsMutableFeature(f) && f != discord.FeatureCommunity && (f != discord.FeatureDiscoverable || p.communityAfter) {
				feats = append(feats, f)
			}
		}
		if p.communityAfter {
			feats = append(feats, discord.FeatureCommunity)
		}
		slices.Sort(feats)
		if feats == nil {
			feats = []string{}
		}
		fields["community"] = FieldChange{From: p.communityLive, To: p.communityAfter}
		ss.features = &feats
		if p.communityMoves {
			str := func(name, want, have string, dst **string) {
				if want != "" && want != have {
					fields[name] = FieldChange{From: have, To: want}
					w := want
					*dst = &w
				}
			}
			str("verificationLevel", pr.VerificationLevel, live.VerificationLevel, &ss.verification)
			str("explicitContentFilter", pr.ExplicitContentFilter, live.ExplicitContentFilter, &ss.explicit)
		}
	}
	if len(fields) == 0 {
		return
	}
	summary := "set server " + fieldList(fields)
	if fc, ok := fields["community"]; ok {
		if fc.To == true {
			summary = "enable Community: " + fieldList(fields)
		} else {
			summary = "disable Community: " + fieldList(fields)
		}
	}
	p.add(Change{
		Action: ActionUpdate, Kind: KindServer, Key: p.serverKey, ID: p.serverID,
		Summary: summary,
		Fields:  fields,
		op:      &op{phase: phaseServerRefs, server: ss},
	})
}

func (p *planner) channelRefName(id string) any {
	if id == "" {
		return nil
	}
	if k, ok := p.keyByID[id]; ok {
		return k
	}
	return "id:" + id
}

// ───────────────────────────────────────────────────────────── roles

type roleWant struct {
	name        string
	color       int
	hoist       bool
	mentionable bool
	perms       discord.Permissions

	// style, managed only when set in the YAML
	colors     *discord.RoleColors // spec.colors (Primary == color)
	iconFile   string              // spec.icon, for display
	icon       []byte
	iconHash   string
	iconState  iconState
	emoji      string // spec.emoji
	manageIcon bool   // icon or emoji declared
}

func parseColor(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	if len(s) != 7 || s[0] != '#' {
		return 0, fmt.Errorf("invalid color %q", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid color %q", s)
	}
	return int(v), nil
}

func colorString(c int) string {
	if c == 0 {
		return ""
	}
	return fmt.Sprintf("#%06x", c)
}

func permNames(p discord.Permissions) []string { return p.Names() }

func (p *planner) planEveryone() error {
	ev := p.sp.Everyone
	if ev == nil {
		return nil
	}
	want, err := discord.ParsePermissions(ev.Permissions)
	if err != nil {
		return fmt.Errorf("everyone.permissions: %w", err)
	}
	live, ok := p.rolesByID[p.serverID]
	if !ok {
		return fmt.Errorf("reconcile: @everyone role of server %s not found", p.serverID)
	}
	if live.Permissions == want {
		return nil
	}
	p.add(Change{
		Action: ActionUpdate, Kind: KindEveryone, Key: "@everyone", ID: p.serverID,
		Summary: "update @everyone permissions",
		Fields:  map[string]FieldChange{"permissions": {From: permNames(live.Permissions), To: permNames(want)}},
		op:      &op{phase: phaseServer, role: &discord.RoleParams{Permissions: &want}},
	})
	return nil
}

func (p *planner) planRoles() error {
	for _, key := range desiredRoleOrder(p.sp) {
		spec := p.sp.Roles[key]
		color, err := parseColor(spec.Color)
		if err != nil {
			return fmt.Errorf("roles.%s.color: %w", key, err)
		}
		perms, err := discord.ParsePermissions(spec.Permissions)
		if err != nil {
			return fmt.Errorf("roles.%s.permissions: %w", key, err)
		}
		w := roleWant{name: spec.Name, color: color, hoist: spec.Hoist, mentionable: spec.Mentionable, perms: perms}
		if err := p.roleStyle(key, spec, &w); err != nil {
			return err
		}
		switch p.how[state.KindRole][key] {
		case "skip":
			continue
		case "create":
			fields := map[string]FieldChange{
				"name": {To: w.name}, "color": {To: colorString(w.color)}, "hoist": {To: w.hoist},
				"mentionable": {To: w.mentionable}, "permissions": {To: permNames(w.perms)},
			}
			rp := &discord.RoleParams{Name: &w.name, Color: &w.color, Hoist: &w.hoist, Mentionable: &w.mentionable, Permissions: &w.perms}
			o := &op{phase: phaseRoles, rk: state.KindRole, role: rp}
			p.roleExtras(key, w, discord.Role{}, fields, rp, o, true)
			p.add(Change{
				Action: ActionCreate, Kind: KindRole, Key: key,
				Summary: fmt.Sprintf("create role %q", w.name),
				Fields:  fields,
				op:      o,
			})
		default:
			live := p.rolesByID[p.ids[state.KindRole][key]]
			adopt := p.how[state.KindRole][key] == "adopt"
			if live.Managed {
				p.block(ActionUpdate, KindRole, key, live.ID, "managed: role belongs to an integration")
				continue
			}
			fields, params := roleDiff(w, live)
			if fields == nil {
				fields, params = map[string]FieldChange{}, &discord.RoleParams{}
			}
			o := &op{phase: phaseRoles, rk: state.KindRole}
			p.roleExtras(key, w, live, fields, params, o, false)
			if len(fields) > 0 && live.Position >= p.botTop {
				p.blockFields(ActionUpdate, KindRole, key, live.ID, reasonHierarchy, fields)
				fields = map[string]FieldChange{}
				o.roleIcon, o.roleIconHash = nil, ""
			}
			if !adopt && len(fields) == 0 {
				continue
			}
			if len(fields) == 0 {
				fields, params = nil, nil
			}
			o.role = params
			c := Change{Action: ActionUpdate, Kind: KindRole, Key: key, ID: live.ID, Fields: fields, op: o}
			if adopt {
				c.Action = ActionAdopt
				c.Summary = fmt.Sprintf("adopt role %q (%s)", live.Name, live.ID)
				if len(fields) > 0 {
					c.Summary += ", update " + fieldList(fields)
				}
			} else {
				c.Summary = updateSummary("role", quote(live.Name), quote(w.name), fields)
			}
			p.add(c)
		}
	}
	return nil
}

func roleDiff(w roleWant, live discord.Role) (map[string]FieldChange, *discord.RoleParams) {
	f := map[string]FieldChange{}
	rp := &discord.RoleParams{}
	if w.name != live.Name {
		f["name"] = FieldChange{From: live.Name, To: w.name}
		rp.Name = &w.name
	}
	if w.color != live.Color && w.colors == nil {
		f["color"] = FieldChange{From: colorString(live.Color), To: colorString(w.color)}
		rp.Color = &w.color
	}
	if w.hoist != live.Hoist {
		f["hoist"] = FieldChange{From: live.Hoist, To: w.hoist}
		rp.Hoist = &w.hoist
	}
	if w.mentionable != live.Mentionable {
		f["mentionable"] = FieldChange{From: live.Mentionable, To: w.mentionable}
		rp.Mentionable = &w.mentionable
	}
	if w.perms != live.Permissions {
		f["permissions"] = FieldChange{From: permNames(live.Permissions), To: permNames(w.perms)}
		rp.Permissions = &w.perms
	}
	if len(f) == 0 {
		return nil, nil
	}
	return f, rp
}

func (p *planner) planRoleOrder() {
	desired := desiredRoleOrder(p.sp)
	// predicted live sequence: orderable live roles, then created roles at the bottom
	seq := roleSeq(p.in.Live.Roles, p.serverID, p.botTop)
	const newPrefix = "\x00new:"
	idOf := map[string]string{} // key → ID in seq
	for _, key := range desired {
		switch p.how[state.KindRole][key] {
		case "create":
			idOf[key] = newPrefix + key
			seq = append(seq, newPrefix+key)
		case "existing", "adopt":
			id := p.ids[state.KindRole][key]
			if r := p.rolesByID[id]; !r.Managed {
				idOf[key] = id
			}
		}
	}
	inSeq := map[string]bool{}
	for _, id := range seq {
		inSeq[id] = true
	}
	// roles above the bot cannot move: check that the desired order does not need it
	for i, key := range desired {
		id, ok := idOf[key]
		if !ok || inSeq[id] {
			continue
		}
		// key is managed but at/above the bot's top role
		for j := 0; j < i; j++ {
			above, ok := idOf[desired[j]]
			if !ok {
				continue
			}
			if inSeq[above] || p.rolesByID[above].Position < p.rolesByID[id].Position {
				p.block(ActionReorder, KindRole, key, id, reasonHierarchy)
				break
			}
		}
	}
	var want, from []string
	for _, key := range desired {
		if id, ok := idOf[key]; ok && inSeq[id] {
			want = append(want, id)
		}
	}
	keyOf := func(id string) string {
		if strings.HasPrefix(id, newPrefix) {
			return strings.TrimPrefix(id, newPrefix)
		}
		return p.keyByID[id]
	}
	wantSet := map[string]bool{}
	for _, id := range want {
		wantSet[id] = true
	}
	for _, id := range seq {
		if wantSet[id] {
			from = append(from, keyOf(id))
		}
	}
	if _, changed := slotOrder(seq, want); !changed {
		return
	}
	to := make([]string, len(want))
	for i, id := range want {
		to[i] = keyOf(id)
	}
	p.add(Change{
		Action: ActionReorder, Kind: KindRole, Key: KeyHierarchy,
		Summary: "reorder roles: " + strings.Join(to, " > "),
		Fields:  map[string]FieldChange{"order": {From: from, To: to}},
		op:      &op{phase: phaseRolePositions},
	})
}

// ───────────────────────────────────────────────────────────── overwrites

func (p *planner) overwrites(where string, ows map[string]config.OverwriteSpec) ([]owSpec, error) {
	out := make([]owSpec, 0, len(ows))
	for _, t := range sortedKeys(ows) {
		o := ows[t]
		allow, err := discord.ParsePermissions(o.Allow)
		if err != nil {
			return nil, fmt.Errorf("%s.overwrites.%s.allow: %w", where, t, err)
		}
		deny, err := discord.ParsePermissions(o.Deny)
		if err != nil {
			return nil, fmt.Errorf("%s.overwrites.%s.deny: %w", where, t, err)
		}
		if allow == 0 && deny == 0 {
			continue
		}
		var tg target
		switch {
		case t == "@everyone":
			tg = target{id: p.serverID, typ: discord.OverwriteRole}
		case strings.HasPrefix(t, "member:"):
			tg = target{id: strings.TrimPrefix(t, "member:"), typ: discord.OverwriteMember}
		default:
			tg = target{roleKey: t, typ: discord.OverwriteRole}
		}
		out = append(out, owSpec{target: tg, allow: allow, deny: deny})
	}
	return out, nil
}

type owVal struct{ allow, deny discord.Permissions }

func normOverwrites(in []discord.Overwrite) map[string]owVal {
	m := map[string]owVal{}
	for _, o := range in {
		if o.Allow == 0 && o.Deny == 0 {
			continue
		}
		m[strconv.Itoa(int(o.Type))+":"+o.ID] = owVal{o.Allow, o.Deny}
	}
	return m
}

// overwritesDiffer compares desired (resolved with plan-time IDs) and live overwrites.
func (p *planner) overwritesDiffer(want []owSpec, live []discord.Overwrite) bool {
	lm := normOverwrites(live)
	if len(lm) != len(want) {
		return true
	}
	for _, o := range want {
		id := o.target.id
		if o.target.roleKey != "" {
			var ok bool
			if id, ok = p.ids[state.KindRole][o.target.roleKey]; !ok {
				return true // role created by this plan
			}
		}
		v, ok := lm[strconv.Itoa(int(o.target.typ))+":"+id]
		if !ok || v.allow != o.allow || v.deny != o.deny {
			return true
		}
	}
	return false
}

type owDisplay struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

func displayWant(ows []owSpec) map[string]owDisplay {
	m := map[string]owDisplay{}
	for _, o := range ows {
		name := o.target.roleKey
		switch {
		case name != "":
		case o.target.typ == discord.OverwriteMember:
			name = "member:" + o.target.id
		default:
			name = "@everyone"
		}
		m[name] = owDisplay{Allow: o.allow.Names(), Deny: o.deny.Names()}
	}
	return m
}

func (p *planner) displayLive(ows []discord.Overwrite) map[string]owDisplay {
	m := map[string]owDisplay{}
	for _, o := range ows {
		if o.Allow == 0 && o.Deny == 0 {
			continue
		}
		var name string
		switch {
		case o.Type == discord.OverwriteMember:
			name = "member:" + o.ID
		case o.ID == p.serverID:
			name = "@everyone"
		default:
			if k, ok := p.roleKeyByID(o.ID); ok {
				name = k
			} else {
				name = "role:" + o.ID
			}
		}
		m[name] = owDisplay{Allow: o.Allow.Names(), Deny: o.Deny.Names()}
	}
	return m
}

func (p *planner) roleKeyByID(id string) (string, bool) {
	for k, v := range p.ids[state.KindRole] {
		if v == id {
			return k, true
		}
	}
	return "", false
}

// ───────────────────────────────────────────────────────────── categories & channels

func hasTopic(t discord.ChannelType) bool {
	switch t {
	case discord.ChannelText, discord.ChannelAnnouncement, discord.ChannelForum, discord.ChannelMedia:
		return true
	}
	return false
}

func hasSlowmode(t discord.ChannelType) bool {
	switch t {
	case discord.ChannelText, discord.ChannelVoice, discord.ChannelStage, discord.ChannelForum, discord.ChannelMedia:
		return true
	}
	return false
}

func isVoice(t discord.ChannelType) bool {
	return t == discord.ChannelVoice || t == discord.ChannelStage
}

func (p *planner) categorySpec(key string) (*chanSpec, error) {
	c := p.sp.Categories[key]
	ows, err := p.overwrites("categories."+key, c.Overwrites)
	if err != nil {
		return nil, err
	}
	return &chanSpec{name: c.Name, typ: discord.ChannelCategory, overwrites: ows}, nil
}

func (p *planner) channelSpec(key string) (*chanSpec, error) {
	c := p.sp.Channels[key]
	typ, err := discord.ParseChannelType(c.Type)
	if err != nil {
		return nil, fmt.Errorf("channels.%s: %w", key, err)
	}
	cs := &chanSpec{name: normalizeChannelName(typ, c.Name), typ: typ, parentKey: c.Category}
	if c.SyncsWithCategory() {
		if c.Category != "" {
			if cs.overwrites, err = p.overwrites("categories."+c.Category, p.sp.Categories[c.Category].Overwrites); err != nil {
				return nil, err
			}
		}
	} else if cs.overwrites, err = p.overwrites("channels."+key, c.Overwrites); err != nil {
		return nil, err
	}
	nsfw := c.NSFW
	cs.nsfw = &nsfw
	if hasTopic(typ) {
		t := c.EffectiveTopic()
		cs.topic = &t
	}
	if err := channelExtras(key, c, cs); err != nil {
		return nil, err
	}
	if hasSlowmode(typ) {
		s := c.Slowmode
		cs.slowmode = &s
	}
	if isVoice(typ) {
		u := c.UserLimit
		cs.userLimit = &u
		if c.Bitrate > 0 {
			b := c.Bitrate
			cs.bitrate = &b
		}
	}
	return cs, nil
}

func (p *planner) planChannels() error {
	for _, key := range desiredCategoryOrder(p.sp) {
		cs, err := p.categorySpec(key)
		if err != nil {
			return err
		}
		p.planChannel(state.KindCategory, KindCategory, key, cs)
	}
	// uncategorized first, then per category in category order
	groups := append([]string{""}, desiredCategoryOrder(p.sp)...)
	for _, g := range groups {
		for _, key := range desiredChannelOrder(p.sp, g, "") {
			cs, err := p.channelSpec(key)
			if err != nil {
				return err
			}
			p.planChannel(state.KindChannel, KindChannel, key, cs)
		}
	}
	return nil
}

func (p *planner) channelLabel(cs *chanSpec, name string) string {
	if cs.typ == discord.ChannelCategory {
		return "category " + quote(name)
	}
	return cs.typ.String() + " channel #" + name
}

func (p *planner) planChannel(rk state.ResourceKind, kind, key string, cs *chanSpec) {
	ph := phaseChannels
	if rk == state.KindCategory {
		ph = phaseCategories
	}
	how := p.how[rk][key]
	if how == "skip" {
		return
	}
	in := ""
	if cs.parentKey != "" {
		in = " in " + quote(p.sp.Categories[cs.parentKey].Name)
	}
	if how == "existing" {
		live := p.chansByID[p.ids[rk][key]]
		if live.Type != cs.typ {
			reason := fmt.Sprintf("type change: %s → %s requires recreating the channel (needs prune: true and deletionProtection: false)", live.Type, cs.typ)
			if !p.prune || p.protect {
				p.blockFields(ActionUpdate, kind, key, live.ID, reason,
					map[string]FieldChange{"type": {From: live.Type.String(), To: cs.typ.String()}})
				return
			}
			// replace: create the new channel now, delete the old one at the end
			delete(p.ids[rk], key)
			p.how[rk][key] = "create"
			how = "create"
			p.add(Change{
				Action: ActionDelete, Kind: kind, Key: key, ID: live.ID,
				Summary: fmt.Sprintf("delete %s (replaced: type change)", p.channelLabel(&chanSpec{typ: live.Type}, live.Name)),
				op:      &op{phase: phaseDeleteChannels, rk: rk},
			})
		}
	}
	if how == "create" && rk == state.KindChannel && needsCommunity(cs.typ) && !p.communityLive {
		if !p.communityAfter {
			p.how[rk][key] = "skip"
			p.blockFields(ActionCreate, kind, key, "", fmt.Sprintf("community: %s channels require the Community feature (set community.enabled: true)", cs.typ),
				map[string]FieldChange{"exists": {From: false, To: true}})
			return
		}
		ph = phaseCommunityChannels // created once Community is enabled
	}
	if how == "create" {
		fields := map[string]FieldChange{"name": {To: cs.name}, "type": {To: cs.typ.String()}}
		if cs.parentKey != "" {
			fields["category"] = FieldChange{To: cs.parentKey}
		}
		if cs.topic != nil && *cs.topic != "" {
			fields["topic"] = FieldChange{To: *cs.topic}
		}
		if cs.nsfw != nil && *cs.nsfw {
			fields["nsfw"] = FieldChange{To: true}
		}
		if cs.slowmode != nil && *cs.slowmode != 0 {
			fields["slowmode"] = FieldChange{To: *cs.slowmode}
		}
		if cs.bitrate != nil {
			fields["bitrate"] = FieldChange{To: *cs.bitrate}
		}
		if cs.userLimit != nil && *cs.userLimit != 0 {
			fields["userLimit"] = FieldChange{To: *cs.userLimit}
		}
		if len(cs.overwrites) > 0 {
			fields["overwrites"] = FieldChange{To: displayWant(cs.overwrites)}
		}
		createExtrasFields(cs, fields)
		p.add(Change{
			Action: ActionCreate, Kind: kind, Key: key,
			Summary: "create " + p.channelLabel(cs, cs.name) + in,
			Fields:  fields,
			op:      &op{phase: ph, rk: rk, ch: cs},
		})
		return
	}
	live := p.chansByID[p.ids[rk][key]]
	fields := map[string]FieldChange{}
	send := map[string]bool{}
	if cs.name != live.Name {
		fields["name"] = FieldChange{From: live.Name, To: cs.name}
		send["name"] = true
	}
	if rk == state.KindChannel {
		pid, ok := "", true
		if cs.parentKey != "" {
			pid, ok = p.ids[state.KindCategory][cs.parentKey]
		}
		if !ok || pid != live.ParentID {
			fields["category"] = FieldChange{From: p.categoryRefName(live.ParentID), To: nilIfEmpty(cs.parentKey)}
			send["parent"] = true
		}
	}
	if cs.topic != nil && *cs.topic != live.Topic {
		fields["topic"] = FieldChange{From: live.Topic, To: *cs.topic}
		send["topic"] = true
	}
	if cs.nsfw != nil && *cs.nsfw != live.NSFW {
		fields["nsfw"] = FieldChange{From: live.NSFW, To: *cs.nsfw}
		send["nsfw"] = true
	}
	if cs.slowmode != nil && *cs.slowmode != live.RateLimitPerUser {
		fields["slowmode"] = FieldChange{From: live.RateLimitPerUser, To: *cs.slowmode}
		send["slowmode"] = true
	}
	if cs.bitrate != nil && *cs.bitrate != live.Bitrate {
		fields["bitrate"] = FieldChange{From: live.Bitrate, To: *cs.bitrate}
		send["bitrate"] = true
	}
	if cs.userLimit != nil && *cs.userLimit != live.UserLimit {
		fields["userLimit"] = FieldChange{From: live.UserLimit, To: *cs.userLimit}
		send["userLimit"] = true
	}
	diffChannelExtras(cs, live, fields, send)
	if p.overwritesDiffer(cs.overwrites, live.Overwrites) {
		fields["overwrites"] = FieldChange{From: p.displayLive(live.Overwrites), To: displayWant(cs.overwrites)}
		send["overwrites"] = true
	}
	adopt := how == "adopt"
	if !adopt && len(fields) == 0 {
		return
	}
	c := Change{Action: ActionUpdate, Kind: kind, Key: key, ID: live.ID, op: &op{phase: ph, rk: rk, ch: cs, send: send}}
	if len(fields) > 0 {
		c.Fields = fields
	}
	if adopt {
		c.Action = ActionAdopt
		c.Summary = fmt.Sprintf("adopt %s (%s)", p.channelLabel(cs, live.Name), live.ID)
		if len(fields) > 0 {
			c.Summary += ", update " + fieldList(fields)
		}
	} else {
		label := "channel"
		oldN, newN := "#"+live.Name, "#"+cs.name
		if rk == state.KindCategory {
			label, oldN, newN = "category", quote(live.Name), quote(cs.name)
		}
		c.Summary = updateSummary(label, oldN, newN, fields)
	}
	p.add(c)
}

func (p *planner) categoryRefName(id string) any {
	if id == "" {
		return nil
	}
	for k, v := range p.ids[state.KindCategory] {
		if v == id {
			return k
		}
	}
	return "id:" + id
}

func (p *planner) planChannelOrder() {
	const newPrefix = "\x00new:"
	live := channelGroups(p.in.Live.Channels)
	keyOf := map[string]string{} // seq ID → key
	idOf := func(rk state.ResourceKind, key string) string {
		if id, ok := p.ids[rk][key]; ok {
			keyOf[id] = key
			return id
		}
		if p.how[rk][key] == "create" {
			keyOf[newPrefix+key] = key
			return newPrefix + key
		}
		return ""
	}
	type group struct {
		gid  string // catGroup or <parent ID>/<bucket> (parent may be new-prefixed)
		want []string
	}
	type parent struct {
		key, label string
		groups     map[string]*group // field name → group
	}
	var parents []parent
	var cw []string
	for _, k := range desiredCategoryOrder(p.sp) {
		if id := idOf(state.KindCategory, k); id != "" {
			cw = append(cw, id)
		}
	}
	parents = append(parents, parent{key: KeyCategories, label: "categories",
		groups: map[string]*group{"order": {gid: catGroup, want: cw}}})
	for _, ck := range append([]string{""}, desiredCategoryOrder(p.sp)...) {
		pr := parent{key: KeyNoCategory, label: "uncategorized channels", groups: map[string]*group{}}
		pid := ""
		if ck != "" {
			pr.key, pr.label = ck, "channels in "+quote(p.sp.Categories[ck].Name)
			if pid = idOf(state.KindCategory, ck); pid == "" {
				continue // category owned by a plugin
			}
		}
		for field, bucket := range map[string]string{"order": "/text", "voiceOrder": "/voice"} {
			g := &group{gid: pid + bucket}
			for _, k := range desiredChannelOrder(p.sp, ck, bucket) {
				if id := idOf(state.KindChannel, k); id != "" {
					g.want = append(g.want, id)
				}
			}
			pr.groups[field] = g
		}
		parents = append(parents, pr)
	}
	// predicted sequences: managed items that move or are created go to the end of
	// their new group
	wantGroup := map[string]string{}
	for _, pr := range parents {
		for _, g := range pr.groups {
			for _, id := range g.want {
				wantGroup[id] = g.gid
			}
		}
	}
	for _, pr := range parents {
		fields := map[string]FieldChange{}
		for field, g := range pr.groups {
			var seq []string
			for _, id := range live[g.gid] {
				if wg, managed := wantGroup[id]; managed && wg != g.gid {
					continue // moves out
				}
				seq = append(seq, id)
			}
			present := map[string]bool{}
			for _, id := range seq {
				present[id] = true
			}
			for _, id := range g.want {
				if !present[id] {
					seq = append(seq, id)
				}
			}
			if _, changed := slotOrder(seq, g.want); !changed {
				continue
			}
			wantSet := map[string]bool{}
			for _, id := range g.want {
				wantSet[id] = true
			}
			var from, to []string
			for _, id := range seq {
				if wantSet[id] {
					from = append(from, keyOf[id])
				}
			}
			for _, id := range g.want {
				to = append(to, keyOf[id])
			}
			fields[field] = FieldChange{From: from, To: to}
		}
		if len(fields) == 0 {
			continue
		}
		kind := KindChannel
		if pr.key == KeyCategories {
			kind = KindCategory
		}
		var parts []string
		for _, f := range []string{"order", "voiceOrder"} {
			if fc, ok := fields[f]; ok {
				parts = append(parts, strings.Join(fc.To.([]string), ", "))
			}
		}
		p.add(Change{
			Action: ActionReorder, Kind: kind, Key: pr.key,
			Summary: "reorder " + pr.label + ": " + strings.Join(parts, " / "),
			Fields:  fields,
			op:      &op{phase: phaseChannelPositions},
		})
	}
}

// ───────────────────────────────────────────────────────────── removed keys

func (p *planner) planOrphans() {
	for _, rk := range []state.ResourceKind{state.KindAutoMod, state.KindEmoji, state.KindSticker, state.KindSound, state.KindChannel, state.KindCategory, state.KindRole} {
		if !p.fetched(rk) {
			continue // not fetched: nothing known about it
		}
		for _, key := range sortedKeys(p.git[rk]) {
			m := p.git[rk][key]
			if p.declared(rk, key) {
				continue
			}
			kind := string(rk)
			var label string
			alive := false
			above := false
			switch rk {
			case state.KindRole:
				if r, ok := p.rolesByID[m.ID]; ok && r.ID != p.serverID {
					alive, label = true, "role "+quote(r.Name)
					above = r.Position >= p.botTop || r.Managed
				}
			case state.KindChannel, state.KindCategory:
				if c, ok := p.chansByID[m.ID]; ok {
					alive = true
					label = p.channelLabel(&chanSpec{typ: c.Type}, c.Name)
				}
			default:
				alive, label = p.parityAlive(rk, m.ID)
			}
			ph := map[state.ResourceKind]phase{state.KindChannel: phaseDeleteChannels, state.KindCategory: phaseDeleteCategories, state.KindRole: phaseDeleteRoles,
				state.KindAutoMod: phaseDeleteAutoMod, state.KindEmoji: phaseDeleteExpressions, state.KindSticker: phaseDeleteExpressions, state.KindSound: phaseDeleteExpressions}[rk]
			switch {
			case p.consumed[m.ID]:
				p.add(Change{Action: ActionUnmanage, Kind: kind, Key: key, ID: m.ID,
					Summary: fmt.Sprintf("move %s mapping %s → %s (key renamed)", kind, key, p.keyByID[m.ID]),
					op:      &op{phase: phaseUnmanage, rk: rk}})
			case !alive:
				p.add(Change{Action: ActionUnmanage, Kind: kind, Key: key, ID: m.ID,
					Summary: fmt.Sprintf("forget %s %s (removed from the config, already gone from Discord)", kind, key),
					op:      &op{phase: phaseUnmanage, rk: rk}})
			case !p.prune:
				p.add(Change{Action: ActionUnmanage, Kind: kind, Key: key, ID: m.ID,
					Summary: fmt.Sprintf("stop managing %s (removed from the config, prune disabled: left in Discord)", label),
					op:      &op{phase: phaseUnmanage, rk: rk}})
			case p.protect:
				p.block(ActionDelete, kind, key, m.ID, "deletionProtection")
			case rk == state.KindAutoMod && p.communityAfter && p.autoModTrigger(m.ID) == discord.AutoModMentionSpam:
				p.block(ActionDelete, kind, key, m.ID, prefixUndeletable+"Discord does not allow deleting the mention spam AutoMod rule of a community server")
			case above:
				p.block(ActionDelete, kind, key, m.ID, reasonHierarchy)
			default:
				p.add(Change{Action: ActionDelete, Kind: kind, Key: key, ID: m.ID,
					Summary: "delete " + label,
					op:      &op{phase: ph, rk: rk}})
			}
		}
	}
}

// autoModTrigger is the trigger of a live AutoMod rule ("" when unknown).
func (p *planner) autoModTrigger(id string) string {
	for _, r := range p.in.Live.AutoMod {
		if r.ID == id {
			return r.Trigger
		}
	}
	return ""
}

func (p *planner) declared(rk state.ResourceKind, key string) bool {
	var ok bool
	switch rk {
	case state.KindRole:
		_, ok = p.sp.Roles[key]
	case state.KindCategory:
		_, ok = p.sp.Categories[key]
	case state.KindChannel:
		_, ok = p.sp.Channels[key]
	case state.KindAutoMod:
		_, ok = p.sp.AutoMod[key]
	case state.KindEmoji:
		_, ok = p.sp.Emojis[key]
	case state.KindSticker:
		_, ok = p.sp.Stickers[key]
	case state.KindSound:
		_, ok = p.sp.Soundboard[key]
	}
	return ok
}

// ───────────────────────────────────────────────────────────── formatting

func quote(s string) string { return strconv.Quote(s) }

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func fieldList(f map[string]FieldChange) string {
	return strings.Join(sortedKeys(f), ", ")
}

func updateSummary(label, oldName, newName string, fields map[string]FieldChange) string {
	rest := map[string]FieldChange{}
	for k, v := range fields {
		if k != "name" {
			rest[k] = v
		}
	}
	var s string
	if _, renamed := fields["name"]; renamed {
		s = fmt.Sprintf("rename %s %s → %s", label, oldName, newName)
		if len(rest) > 0 {
			s += ", update " + fieldList(rest)
		}
		return s
	}
	return fmt.Sprintf("update %s %s: %s", label, newName, fieldList(rest))
}
