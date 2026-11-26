package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/state"
)

const overlayTaskScope = "runtime:overlays"

func (r *Runtime) overlayState(ctx context.Context, server string) (reconcile.OverlayState, error) {
	var doc reconcile.OverlayState
	raw, err := r.opts.Store.KV(overlayTaskScope).Get(ctx, server)
	if errors.Is(err, state.ErrNotFound) {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	err = json.Unmarshal(raw, &doc)
	return doc, err
}
func (r *Runtime) saveOverlays(ctx context.Context, server string, d reconcile.OverlayState) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return r.opts.Store.KV(overlayTaskScope).Set(ctx, server, raw, 0)
}
func (r *Runtime) scheduleOverlay(ctx context.Context, server string, at time.Time) error {
	return r.opts.Store.Tasks().Schedule(ctx, state.Task{Scope: overlayTaskScope, ID: overlayTaskScope + "/" + server, Name: server, RunAt: at, Payload: json.RawMessage("null")})
}
func (r *Runtime) Overlays(ctx context.Context, key, origin string) ([]reconcile.Overlay, error) {
	d, err := r.overlayState(ctx, key)
	out := []reconcile.Overlay{}
	for _, o := range d.Active {
		if o.Until.After(time.Now()) && (origin == "" || o.Origin == origin) {
			out = append(out, o)
		}
	}
	return out, err
}

// updateOverlay writes the wakeup before committing desired state. A crash between
// the two leaves a harmless wakeup; a crash afterwards always leaves a delivery.
func (r *Runtime) updateOverlay(ctx context.Context, rt *serverRT, origin, id string, v *reconcile.Overlay, actor string) error {
	r.overlayMu.Lock()
	err := r.persistOverlay(ctx, rt, origin, id, v, actor)
	r.overlayMu.Unlock()
	if err != nil {
		return err
	}
	// An activation may itself be running plugin startup handlers. It will reconcile
	// the persisted state, and the queued task also runs against the active revision.
	if r.syncMu.TryLock() {
		defer r.syncMu.Unlock()
		return r.flushOverlay(ctx, rt.key)
	}
	return nil
}
func (r *Runtime) persistOverlay(ctx context.Context, rt *serverRT, origin, id string, v *reconcile.Overlay, actor string) error {
	key := rt.key
	d, err := r.overlayState(ctx, key)
	if err != nil {
		return err
	}
	if d.Pending == nil {
		d.Pending = reconcile.OverlayTargets{}
	}
	idx := slices.IndexFunc(d.Active, func(o reconcile.Overlay) bool { return o.Origin == origin && o.ID == id })
	if v == nil && idx < 0 {
		return nil
	}
	if idx >= 0 {
		d.Pending.Add(d.Active[idx])
		d.Active = slices.Delete(d.Active, idx, idx+1)
	}
	action := "overlay.remove"
	if v != nil {
		if err := v.Validate(time.Now()); err != nil {
			return err
		}
		if err := overlayQuota(d.Active, origin); err != nil {
			return err
		}
		if v.Server != nil && d.OriginalVerification == "" {
			// A cached gateway snapshot can predate a human change. Capture the
			// current level before managing an otherwise unconstrained field.
			host := r.bot(rt.bot)
			if host == nil {
				return errors.New("managing bot is unavailable")
			}
			srv, err := host.session.GetServer(ctx, rt.cfg.Spec.DiscordID)
			if err != nil {
				return err
			}
			base := srv.VerificationLevel
			if base == "" {
				return errors.New("cannot capture current verification level")
			}
			d.OriginalVerification = base
		}
		v.Origin = origin
		d.Active = append(d.Active, *v)
		if overlayOverwrites(*v) && rt.rec != nil {
			// Refuse a self-lockout now rather than letting the preflight block
			// every overlay sync of the server until it expires.
			targets := reconcile.OverlayTargets{}
			targets.Add(*v)
			if err := rt.rec.CheckOverlay(ctx, d, targets); err != nil {
				return err
			}
		}
		d.Pending.Add(*v)
		action = "overlay.apply"
		sort.Slice(d.Active, func(i, j int) bool {
			return d.Active[i].Origin+"/"+d.Active[i].ID < d.Active[j].Origin+"/"+d.Active[j].ID
		})
	}
	if err := r.scheduleOverlay(ctx, key, time.Now()); err != nil {
		return err
	}
	if err := r.saveOverlays(ctx, key, d); err != nil {
		return err
	}
	_, err = r.opts.Store.Audit().Append(ctx, state.AuditEntry{Time: time.Now(), Actor: actor, Server: key, Action: action, Target: origin + "/" + id})
	if err == nil {
		r.notify(ChangeStatus, "")
	}
	return err
}

// Overlay quotas. The server document is bounded, and each plugin installation
// (origin) has its own share so one plugin's many quarantines or channel locks
// cannot starve another's raid overlay: an origin holds at most
// overlayOriginLimit overlays, the server overlayServerLimit, and an origin with
// fewer than overlayOriginReserve overlays may still apply past the server limit
// up to overlayHardLimit.
const (
	overlayServerLimit   = 256
	overlayOriginLimit   = 128
	overlayOriginReserve = 8
	overlayHardLimit     = overlayServerLimit + 8*overlayOriginReserve
)

func overlayQuota(active []reconcile.Overlay, origin string) error {
	own := 0
	for _, o := range active {
		if o.Origin == origin {
			own++
		}
	}
	switch {
	case own >= overlayOriginLimit:
		return fmt.Errorf("plugin overlay limit reached (%d active overlays)", overlayOriginLimit)
	case len(active) >= overlayHardLimit, len(active) >= overlayServerLimit && own >= overlayOriginReserve:
		return errors.New("server overlay limit reached")
	}
	return nil
}

func overlayOverwrites(o reconcile.Overlay) bool {
	for _, c := range o.Channels {
		if len(c.Overwrites) > 0 {
			return true
		}
	}
	return false
}

// Caller holds syncMu. Active revision selection, planning, applying, and settling
// dirty fields form a single operation with respect to other overlay changes.
func (r *Runtime) flushOverlay(ctx context.Context, key string) error {
	r.overlayMu.Lock()
	defer r.overlayMu.Unlock()
	a := r.Active()
	if a == nil || a.servers[key] == nil || a.servers[key].rec == nil {
		return errors.New("no active server reconciler")
	}
	rt := a.servers[key]
	d, err := r.overlayState(ctx, key)
	if err != nil {
		return err
	}
	if len(d.Pending) == 0 && len(d.Active) == 0 {
		return nil
	}
	now := time.Now()
	if d.Pending == nil {
		d.Pending = reconcile.OverlayTargets{}
	}
	var next time.Time
	d.Active = slices.DeleteFunc(d.Active, func(o reconcile.Overlay) bool {
		if !o.Until.After(now) {
			d.Pending.Add(o)
			return true
		}
		if next.IsZero() || o.Until.Before(next) {
			next = o.Until
		}
		return false
	})
	// Persist expiration and dirty fields before any external Discord call.
	if err = r.saveOverlays(ctx, key, d); err != nil {
		return err
	}
	if len(d.Pending) > 0 && !r.isPaused(key) {
		if err = rt.rec.SyncOverlay(ctx, d.Pending); err != nil && !errors.Is(err, reconcile.ErrPaused) {
			return err
		}
		if err == nil {
			d.Pending = nil
			if !slices.ContainsFunc(d.Active, func(o reconcile.Overlay) bool { return o.Server != nil }) {
				d.OriginalVerification = ""
			}
			if err = r.saveOverlays(ctx, key, d); err != nil {
				return err
			}
		}
	}
	if !next.IsZero() {
		if err = r.scheduleOverlay(ctx, key, next); err != nil {
			return err
		}
	}
	r.refreshPlan(ctx, rt)
	r.notify(ChangeStatus, "")
	return nil
}
func (r *Runtime) deliverOverlay(ctx context.Context, t state.TaskLease) {
	defer func() { <-r.sched.slots }()
	r.syncMu.Lock()
	err := r.flushOverlay(ctx, t.Name)
	r.syncMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		r.log.Warn("overlay convergence failed", "server", t.Name, "err", err)
		err = r.opts.Store.Tasks().Release(ctx, t, time.Now().Add(time.Minute), "overlay convergence failed")
	} else {
		err = r.opts.Store.Tasks().Acknowledge(ctx, t)
	}
	if err != nil {
		r.log.Warn("settle overlay", "err", err)
	}
}
func (r *Runtime) RemoveOverlay(ctx context.Context, key, origin, id, actor string) error {
	a := r.Active()
	if a == nil || a.servers[key] == nil {
		return fmt.Errorf("unknown server %s", key)
	}
	if len(strings.Split(origin, "/")) != 2 {
		return errors.New("invalid overlay origin")
	}
	return r.updateOverlay(ctx, a.servers[key], origin, id, nil, actor)
}

func mustJSONReason(reason string) json.RawMessage {
	v, _ := json.Marshal(map[string]string{"reason": reason})
	return v
}
