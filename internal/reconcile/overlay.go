package reconcile

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
)

type OverwritePatch struct {
	Deny    []string `json:"deny,omitempty"`
	Neutral []string `json:"neutral,omitempty"`
}
type ChannelRestriction struct {
	Overwrites map[string]OverwritePatch `json:"overwrites,omitempty"`
	Slowmode   *int                      `json:"slowmodeSeconds,omitempty"`
}
type ServerRestriction struct {
	Verification string `json:"verificationLevel"`
}
type MemberRestriction struct {
	Suspend bool `json:"suspendDeclaredRoles"`
}
type Overlay struct {
	Origin   string                        `json:"origin,omitempty"`
	ID       string                        `json:"id"`
	Until    time.Time                     `json:"until"`
	Reason   string                        `json:"reason"`
	Channels map[string]ChannelRestriction `json:"channels,omitempty"`
	Server   *ServerRestriction            `json:"server,omitempty"`
	Members  map[string]MemberRestriction  `json:"members,omitempty"`
}

var overlayID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)
var targetID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var memberID = regexp.MustCompile(`^[0-9]{17,20}$`)
var verification = []string{"none", "low", "medium", "high", "highest"}

func (o Overlay) Validate(now time.Time) error {
	if !overlayID.MatchString(o.ID) || strings.TrimSpace(o.Reason) == "" || len([]rune(o.Reason)) > 512 {
		return errors.New("overlay requires an ID and a reason up to 512 characters")
	}
	if !o.Until.After(now) || o.Until.After(now.Add(7*24*time.Hour)) {
		return errors.New("overlay must expire within seven days")
	}
	if o.Server != nil && slices.Index(verification, o.Server.Verification) < 1 {
		return errors.New("invalid overlay verification level")
	}
	if o.Server == nil && len(o.Channels)+len(o.Members) == 0 {
		return errors.New("overlay has no restrictions")
	}
	if len(o.Channels) > 500 || len(o.Members) > 500 {
		return errors.New("overlay exceeds target limit")
	}
	for k, m := range o.Members {
		if !memberID.MatchString(k) || !m.Suspend {
			return errors.New("invalid member suspension")
		}
	}
	for key, ch := range o.Channels {
		if key != "*" && !targetID.MatchString(key) {
			return fmt.Errorf("invalid channel key %q", key)
		}
		if ch.Slowmode != nil && (*ch.Slowmode < 0 || *ch.Slowmode > 21600) {
			return errors.New("invalid slowmode")
		}
		if ch.Slowmode == nil && len(ch.Overwrites) == 0 {
			return errors.New("empty channel restriction")
		}
		for target, patch := range ch.Overwrites {
			member, hasPrefix := strings.CutPrefix(target, "member:")
			validMember := hasPrefix && memberID.MatchString(member)
			if target != "@everyone" && target != "@bot" && target != AllOverwrites && !targetID.MatchString(target) && !validMember {
				return errors.New("invalid overwrite target")
			}
			if len(patch.Deny)+len(patch.Neutral) == 0 {
				return errors.New("an overwrite must specify deny or neutral")
			}
			if target == AllOverwrites && len(patch.Deny) > 0 {
				return errors.New(`the "*" overwrite target accepts neutral only; deny on @everyone instead`)
			}
			deny, err := discord.ParsePermissions(patch.Deny)
			if err != nil {
				return err
			}
			if target == BotTarget && deny&(permView|permManageChannels|permManageRoles) != 0 {
				// The preflight would refuse every sync until the overlay expires.
				return errors.New("an overlay cannot deny the bot View Channel, Manage Channels or Manage Roles")
			}
			if _, err := discord.ParsePermissions(patch.Neutral); err != nil {
				return err
			}
		}
	}
	return nil
}

// AllOverwrites is the overlay overwrite target meaning "every overwrite the
// channel already has". It accepts only neutral: it strips those permissions from
// the allow of each existing role and member overwrite, so a lock (an @everyone
// deny) also holds against roles such as `verified: allow [SendMessages]`. It never
// creates an overwrite and skips @everyone, @bot and the staff of spec.access.
const AllOverwrites = "*"

// staffTargets are the overwrite keys AllOverwrites leaves alone: the roles and
// users of spec.access.admins and spec.access.moderators.
func staffTargets(s *config.Server) map[string]bool {
	out := map[string]bool{"@everyone": true, BotTarget: true}
	if a := s.Spec.Access; a != nil {
		for _, p := range []*config.Principals{a.Admins, a.Moderators} {
			if p == nil {
				continue
			}
			for _, r := range p.Roles {
				out[r] = true
			}
			for _, u := range p.Users {
				out["member:"+u] = true
			}
		}
	}
	return out
}

func neutralizeAllows(ows map[string]config.OverwriteSpec, names []string, skip map[string]bool) {
	neutral, _ := discord.ParsePermissions(names)
	for target, p := range ows {
		if skip[target] {
			continue
		}
		allow, _ := discord.ParsePermissions(p.Allow)
		if allow&neutral == 0 {
			continue
		}
		p.Allow = (allow &^ neutral).Names()
		ows[target] = p
	}
}

// OverlayTargets records the fields to converge, including those to restore after removal.
type OverlayTargets map[string]bool

func (t OverlayTargets) Add(o Overlay) {
	if o.Server != nil {
		t["verification"] = true
	}
	for id := range o.Members {
		t["member/"+id] = true
	}
	for k, c := range o.Channels {
		if c.Slowmode != nil {
			t[k+"/slowmode"] = true
		}
		if len(c.Overwrites) > 0 {
			t[k+"/overwrites"] = true
		}
	}
}

type OverlayState struct {
	Active  []Overlay      `json:"active,omitempty"`
	Pending OverlayTargets `json:"pending,omitempty"`
	// OriginalVerification restores an unmanaged profile after the last overlay.
	OriginalVerification string `json:"originalVerification,omitempty"`
}

func (s OverlayState) effective(base *config.Server, now time.Time) (*config.Server, map[string]bool, []Note) {
	next := *base
	next.Spec.Channels = maps.Clone(base.Spec.Channels)
	profile := config.ServerProfile{}
	if base.Spec.Profile != nil {
		profile = *base.Spec.Profile
	}
	if profile.VerificationLevel == "" && s.OriginalVerification != "" {
		profile.VerificationLevel = s.OriginalVerification
		next.Spec.Profile = &profile
	}
	suspended := map[string]bool{}
	var notes []Note
	staff := staffTargets(base)
	for _, o := range s.Active {
		if !o.Until.After(now) {
			continue
		}
		origin := o.Origin + "/" + o.ID
		notes = append(notes, Note{Kind: "overlay", Key: origin, Message: fmt.Sprintf("Overlay %s until %s: %s", origin, o.Until.UTC().Format(time.RFC3339), o.Reason)})
		if o.Server != nil && slices.Index(verification, o.Server.Verification) > slices.Index(verification, profile.VerificationLevel) {
			profile.VerificationLevel = o.Server.Verification
			next.Spec.Profile = &profile
		}
		for id := range o.Members {
			suspended[id] = true
		}
		for _, k := range sortedKeys(o.Channels) {
			patch := o.Channels[k]
			if _, ok := next.Spec.Channels[k]; k != "*" && !ok {
				notes = append(notes, Note{Kind: "overlay", Key: origin, Message: "Overlay " + origin + ": missing channel " + k})
				continue
			}
			for key, ch := range next.Spec.Channels {
				if k != "*" && k != key {
					continue
				}
				if patch.Slowmode != nil {
					ch.Slowmode = max(ch.Slowmode, *patch.Slowmode)
				}
				if len(patch.Overwrites) > 0 {
					ow := ch.Overwrites
					if ch.SyncsWithCategory() {
						ow = next.Spec.Categories[ch.Category].Overwrites
					}
					ch.Overwrites = maps.Clone(ow)
					if ch.Overwrites == nil {
						ch.Overwrites = map[string]config.OverwriteSpec{}
					}
					off := false
					ch.SyncWithCategory = &off
					for target, v := range patch.Overwrites {
						if target == AllOverwrites {
							neutralizeAllows(ch.Overwrites, v.Neutral, staff)
							continue
						}
						if targetID.MatchString(target) {
							if _, ok := next.Spec.Roles[target]; !ok {
								notes = append(notes, Note{Kind: "overlay", Key: origin, Message: "Overlay " + origin + ": missing role " + target})
								continue
							}
						}
						p := ch.Overwrites[target]
						deny, _ := discord.ParsePermissions(p.Deny)
						additional, _ := discord.ParsePermissions(v.Deny)
						deny |= additional
						neutral, _ := discord.ParsePermissions(v.Neutral)
						allow, _ := discord.ParsePermissions(p.Allow)
						ch.Overwrites[target] = config.OverwriteSpec{Allow: (allow &^ (deny | neutral)).Names(), Deny: deny.Names()}
					}
				}
				next.Spec.Channels[key] = ch
			}
		}
	}
	return &next, suspended, notes
}

// Only update the fields for which this overlay changed desired state. Other pending
// git changes (including creation, pruning and ordering) wait for a regular sync.
func (t OverlayTargets) filter(changes []Change) []Change {
	out := make([]Change, 0, len(changes))
	for _, c := range changes {
		if c.Action != ActionUpdate {
			continue
		}
		switch c.Kind {
		case KindChannel:
			for f := range c.Fields {
				if !t[c.Key+"/"+f] && !t["*/"+f] {
					delete(c.Fields, f)
					delete(c.op.send, f)
				}
			}
			if len(c.Fields) > 0 {
				out = append(out, c)
			}
		case KindMemberRole:
			if t["member/"+c.ID] {
				out = append(out, c)
			}
		case KindServer:
			if _, ok := c.Fields["verificationLevel"]; ok && t["verification"] && c.op.server != nil {
				c.Fields = map[string]FieldChange{"verificationLevel": c.Fields["verificationLevel"]}
				c.op.server = &serverSpec{verification: c.op.server.verification}
				out = append(out, c)
			}
		}
	}
	return out
}
func (r *Reconciler) SyncOverlay(ctx context.Context, t OverlayTargets) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLiveTargets(ctx, t)
	if err != nil {
		return err
	}
	if r.paused() {
		return ErrPaused
	}
	for _, b := range p.Blocked {
		if b.Kind == KindMemberRole && t["member/"+b.ID] {
			return fmt.Errorf("overlay member %s blocked: %s", b.ID, b.Reason)
		}
	}
	if !p.Empty() {
		_, err = r.applyAndRefresh(ctx, p)
	}
	return err
}
