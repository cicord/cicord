package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/state"
)

// applier executes one plan.
type applier struct {
	r        *Reconciler
	desired  *config.Server
	serverID string
	ids      map[state.ResourceKind]map[string]string // working key → ID map
	res      Result
}

var errNoOp = errors.New("reconcile: change has no apply data (plan not produced by this reconciler)")

func (r *Reconciler) apply(ctx context.Context, p Plan) (Result, error) {
	a := &applier{
		r:        r,
		desired:  r.desired,
		serverID: r.desired.Spec.DiscordID,
		ids: map[state.ResourceKind]map[string]string{
			state.KindRole: {}, state.KindCategory: {}, state.KindChannel: {},
		},
		res: Result{Applied: []Change{}, Failed: []Failure{}},
	}
	mapped, err := r.opts.Resources.List(ctx, r.serverKey())
	if err != nil {
		return a.res, fmt.Errorf("list resource map: %w", err)
	}
	for _, m := range mapped {
		if m.Owner == state.OwnerGit && a.ids[m.Kind] != nil {
			a.ids[m.Kind][m.Key] = m.ID
		}
	}
	byPhase := map[phase][]Change{}
	needRolePos, needChanPos := false, false
	for _, c := range p.Changes {
		if c.op == nil {
			a.fail(c, errNoOp)
			continue
		}
		if c.Action == ActionCreate && c.op.rk != "" {
			delete(a.ids[c.op.rk], c.Key) // stale mapping: resolved once created
		}
		switch {
		case c.Kind == KindRole && c.Action != ActionDelete && c.Action != ActionUnmanage:
			needRolePos = true
		case (c.Kind == KindCategory || c.Kind == KindChannel) && c.Action != ActionDelete && c.Action != ActionUnmanage:
			needChanPos = true
		}
		byPhase[c.op.phase] = append(byPhase[c.op.phase], c)
	}

	for ph := phaseServer; ph <= phaseUnmanage; ph++ {
		switch ph {
		case phaseRolePositions:
			if needRolePos {
				a.positions(ctx, byPhase[ph], KindRole, a.rolePositions)
			}
			continue
		case phaseChannelPositions:
			if needChanPos {
				a.positions(ctx, byPhase[ph], KindChannel, a.channelPositions)
			}
			continue
		}
		for _, c := range byPhase[ph] {
			if err := ctx.Err(); err != nil {
				a.fail(c, err)
				continue
			}
			if err := a.run(ctx, c); err != nil {
				a.fail(c, err)
			} else {
				a.res.Applied = append(a.res.Applied, c)
			}
		}
	}

	if n := len(a.res.Failed); n > 0 {
		errs := make([]error, 0, n)
		for _, f := range a.res.Failed {
			errs = append(errs, fmt.Errorf("%s %s %s: %w", f.Change.Action, f.Change.Kind, f.Change.Key, f.Err()))
		}
		return a.res, fmt.Errorf("reconcile %s: %d change(s) failed: %w", r.serverKey(), n, errors.Join(errs...))
	}
	return a.res, nil
}

func (a *applier) fail(c Change, err error) {
	a.r.log.Warn("reconcile: change failed", "server", a.r.serverKey(), "action", c.Action, "kind", c.Kind, "key", c.Key, "err", err)
	a.res.Failed = append(a.res.Failed, Failure{Change: c, Error: err.Error(), err: err})
}

// positions runs a position recompute and accounts it on the phase's reorder changes.
func (a *applier) positions(ctx context.Context, changes []Change, kind string, fn func(context.Context) error) {
	err := fn(ctx)
	if len(changes) == 0 {
		if err != nil {
			a.fail(Change{Action: ActionReorder, Kind: kind, Summary: "update " + kind + " positions"}, err)
		}
		return
	}
	for _, c := range changes {
		if err != nil {
			a.fail(c, err)
		} else {
			a.res.Applied = append(a.res.Applied, c)
		}
	}
}

func (a *applier) reason() string { return a.r.opts.Reason }

func (a *applier) record(ctx context.Context, rk state.ResourceKind, key, id string) error {
	err := a.r.opts.Resources.Put(ctx, state.Resource{
		Server: a.r.serverKey(), Kind: rk, Key: key, ID: id, Owner: state.OwnerGit, UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("record mapping %s/%s → %s: %w", rk, key, id, err)
	}
	a.ids[rk][key] = id
	return nil
}

// forget removes a mapping if it still points to id.
func (a *applier) forget(ctx context.Context, rk state.ResourceKind, key, id string) error {
	m, err := a.r.opts.Resources.Get(ctx, a.r.serverKey(), rk, key)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.ID != id || m.Owner != state.OwnerGit {
		return nil
	}
	if a.ids[rk][key] == id {
		delete(a.ids[rk], key)
	}
	return a.r.opts.Resources.Delete(ctx, a.r.serverKey(), rk, key)
}

func (a *applier) run(ctx context.Context, c Change) error {
	o := c.op
	c0 := a.r.opts.Client
	switch {
	case c.Action == ActionUnmanage:
		return a.forget(ctx, o.rk, c.Key, c.ID)

	case c.Action == ActionDelete:
		var err error
		if o.rk == state.KindRole {
			err = c0.DeleteRole(ctx, a.serverID, c.ID, a.reason())
		} else {
			err = c0.DeleteChannel(ctx, c.ID, a.reason())
		}
		if err != nil && !errors.Is(err, discord.ErrNotFound) {
			return err
		}
		return a.forget(ctx, o.rk, c.Key, c.ID)

	case c.Kind == KindServer:
		return a.server(ctx, o.server)

	case c.Kind == KindEveryone:
		_, err := c0.UpdateRole(ctx, a.serverID, a.serverID, *o.role, a.reason())
		return err

	case c.Kind == KindRole:
		switch c.Action {
		case ActionCreate:
			role, err := c0.CreateRole(ctx, a.serverID, *o.role, a.reason())
			if err != nil {
				return err
			}
			return a.record(ctx, state.KindRole, c.Key, role.ID)
		case ActionAdopt:
			if err := a.record(ctx, state.KindRole, c.Key, c.ID); err != nil {
				return err
			}
		}
		if o.role == nil {
			return nil
		}
		_, err := c0.UpdateRole(ctx, a.serverID, c.ID, *o.role, a.reason())
		return err

	case c.Kind == KindCategory || c.Kind == KindChannel:
		switch c.Action {
		case ActionCreate:
			params, err := a.channelParams(o.ch, nil)
			if err != nil {
				return err
			}
			ch, err := c0.CreateChannel(ctx, a.serverID, params, a.reason())
			if err != nil {
				return withCommunityHint(params.Type, err)
			}
			return a.record(ctx, o.rk, c.Key, ch.ID)
		case ActionAdopt:
			if err := a.record(ctx, o.rk, c.Key, c.ID); err != nil {
				return err
			}
		}
		if len(o.send) == 0 {
			return nil
		}
		params, err := a.channelParams(o.ch, o.send)
		if err != nil {
			return err
		}
		_, err = c0.UpdateChannel(ctx, c.ID, params, a.reason())
		return err
	}
	return fmt.Errorf("reconcile: unsupported change %s %s", c.Action, c.Kind)
}

func (a *applier) server(ctx context.Context, s *serverSpec) error {
	sp := discord.ServerParams{
		Name: s.name, Description: s.description, VerificationLevel: s.verification, DefaultNotifications: s.notifications,
	}
	if s.icon != nil {
		icon := s.icon
		sp.Icon = &icon
	}
	resolve := func(key *string) (*string, error) {
		if key == nil {
			return nil, nil
		}
		id, ok := a.ids[state.KindChannel][*key]
		if !ok {
			return nil, fmt.Errorf("unresolved channel %q", *key)
		}
		return &id, nil
	}
	var err error
	if sp.SystemChannelID, err = resolve(s.systemKey); err != nil {
		return err
	}
	if sp.RulesChannelID, err = resolve(s.rulesKey); err != nil {
		return err
	}
	srv, err := a.r.opts.Client.UpdateServer(ctx, a.serverID, sp)
	if err != nil {
		return err
	}
	if s.icon != nil {
		return a.r.saveIconState(ctx, iconState{Hash: s.iconHash, URL: srv.IconURL})
	}
	return nil
}

// channelParams builds create (send == nil: every field) or update parameters.
func (a *applier) channelParams(cs *chanSpec, send map[string]bool) (discord.ChannelParams, error) {
	all := send == nil
	p := discord.ChannelParams{Type: cs.typ}
	if all || send["name"] {
		n := cs.name
		p.Name = &n
	}
	if cs.typ != discord.ChannelCategory && (all && cs.parentKey != "" || send["parent"]) {
		pid := ""
		if cs.parentKey != "" {
			var ok bool
			if pid, ok = a.ids[state.KindCategory][cs.parentKey]; !ok {
				return p, fmt.Errorf("unresolved category %q", cs.parentKey)
			}
		}
		p.ParentID = &pid
	}
	opt := func(field string, v *int, dst **int, skipZero bool) {
		if v != nil && (send[field] || all && !(skipZero && *v == 0)) {
			x := *v
			*dst = &x
		}
	}
	if cs.topic != nil && (send["topic"] || all && *cs.topic != "") {
		t := *cs.topic
		p.Topic = &t
	}
	if cs.nsfw != nil && (send["nsfw"] || all && *cs.nsfw) {
		b := *cs.nsfw
		p.NSFW = &b
	}
	opt("slowmode", cs.slowmode, &p.RateLimitPerUser, true)
	opt("bitrate", cs.bitrate, &p.Bitrate, false)
	opt("userLimit", cs.userLimit, &p.UserLimit, true)
	if all || send["overwrites"] {
		// always explicit on create: Discord would otherwise copy the category's
		ows := make([]discord.Overwrite, 0, len(cs.overwrites))
		for _, o := range cs.overwrites {
			id := o.target.id
			if o.target.roleKey != "" {
				var ok bool
				if id, ok = a.ids[state.KindRole][o.target.roleKey]; !ok {
					return p, fmt.Errorf("unresolved role %q in overwrites", o.target.roleKey)
				}
			}
			ows = append(ows, discord.Overwrite{ID: id, Type: o.target.typ, Allow: o.allow, Deny: o.deny})
		}
		p.Overwrites = &ows
	}
	return p, nil
}

// ───────────────────────────────────────────────────────────── positions

func (a *applier) rolePositions(ctx context.Context) error {
	c := a.r.opts.Client
	roles, err := c.ListRoles(ctx, a.serverID)
	if err != nil {
		return err
	}
	botTop := math.MaxInt
	if a.r.opts.BotUserID != "" {
		m, err := c.GetMember(ctx, a.serverID, a.r.opts.BotUserID)
		if err != nil {
			return fmt.Errorf("get bot member: %w", err)
		}
		botTop = Snapshot{Roles: roles, BotRoleIDs: m.RoleIDs, HasBot: true}.BotTopPosition()
	}
	byID := map[string]discord.Role{}
	for _, r := range roles {
		byID[r.ID] = r
	}
	seq := roleSeq(roles, a.serverID, botTop)
	in := map[string]bool{}
	for _, id := range seq {
		in[id] = true
	}
	var want []string
	for _, key := range desiredRoleOrder(&a.desired.Spec) {
		if id, ok := a.ids[state.KindRole][key]; ok && in[id] && !byID[id].Managed {
			want = append(want, id)
		}
	}
	newSeq, changed := slotOrder(seq, want)
	if !changed {
		return nil
	}
	var pos []discord.RolePosition
	for i, id := range newSeq {
		p := len(newSeq) - i
		if byID[id].Position != p {
			pos = append(pos, discord.RolePosition{ID: id, Position: p})
		}
	}
	if len(pos) == 0 {
		return nil
	}
	return c.SetRolePositions(ctx, a.serverID, pos)
}

func (a *applier) channelPositions(ctx context.Context) error {
	c := a.r.opts.Client
	all, err := c.ListChannels(ctx, a.serverID)
	if err != nil {
		return err
	}
	var chans []discord.Channel
	byID := map[string]discord.Channel{}
	for _, ch := range all {
		if !isThread(ch.Type) {
			chans = append(chans, ch)
			byID[ch.ID] = ch
		}
	}
	groups := channelGroups(chans)
	sp := &a.desired.Spec
	type posGroup struct {
		gid  string
		want []string
	}
	var gs []posGroup
	member := func(gid, id string) bool {
		ch, ok := byID[id]
		return ok && groupOf(ch) == gid
	}
	var cw []string
	for _, k := range desiredCategoryOrder(sp) {
		if id, ok := a.ids[state.KindCategory][k]; ok && member(catGroup, id) {
			cw = append(cw, id)
		}
	}
	gs = append(gs, posGroup{catGroup, cw})
	for _, ck := range append([]string{""}, desiredCategoryOrder(sp)...) {
		pid := ""
		if ck != "" {
			var ok bool
			if pid, ok = a.ids[state.KindCategory][ck]; !ok {
				continue
			}
		}
		for _, bucket := range []string{"/text", "/voice"} {
			gid := pid + bucket
			var w []string
			for _, k := range desiredChannelOrder(sp, ck, bucket) {
				if id, ok := a.ids[state.KindChannel][k]; ok && member(gid, id) {
					w = append(w, id)
				}
			}
			gs = append(gs, posGroup{gid, w})
		}
	}
	var pos []discord.ChannelPosition
	for _, g := range gs {
		newSeq, changed := slotOrder(groups[g.gid], g.want)
		if !changed {
			continue
		}
		for i, id := range newSeq {
			if byID[id].Position != i {
				pos = append(pos, discord.ChannelPosition{ID: id, Position: i})
			}
		}
	}
	if len(pos) == 0 {
		return nil
	}
	return c.SetChannelPositions(ctx, a.serverID, pos)
}

// ───────────────────────────────────────────────────────────── icon state

const iconKVKey = "icon"

func (r *Reconciler) loadIconState(ctx context.Context) (iconState, error) {
	if r.opts.KV == nil {
		r.emu.Lock()
		defer r.emu.Unlock()
		return r.iconMem, nil
	}
	raw, err := r.opts.KV.Get(ctx, iconKVKey)
	if errors.Is(err, state.ErrNotFound) {
		return iconState{}, nil
	}
	if err != nil {
		return iconState{}, err
	}
	var v struct {
		Hash string `json:"hash"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return iconState{}, nil // corrupt: upload again
	}
	return iconState{Hash: v.Hash, URL: v.URL}, nil
}

func (r *Reconciler) saveIconState(ctx context.Context, s iconState) error {
	if r.opts.KV == nil {
		r.emu.Lock()
		r.iconMem = s
		r.emu.Unlock()
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"hash": s.Hash, "url": s.URL})
	return r.opts.KV.Set(ctx, iconKVKey, raw, 0)
}

// withCommunityHint explains Discord's opaque "invalid type" error for channel types that
// need the server's Community feature.
func withCommunityHint(t discord.ChannelType, err error) error {
	if t == discord.ChannelAnnouncement && errors.Is(err, discord.ErrBadRequest) {
		return fmt.Errorf("%w (announcement channels require the Community feature: Server Settings → Enable Community, then sync again)", err)
	}
	return err
}
