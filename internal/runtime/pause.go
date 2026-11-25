package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/state"
)

// Pausing enforcement is the emergency switch of a server (https://cicord.dev/docs/concepts/reconciliation/):
// while a server is paused nothing is applied to it, whatever the trigger (a new revision,
// Sync now, the periodic resync, a gateway event with syncPolicy.selfHeal). Plans and drift
// are still computed, so the admin console shows what differs from git. The pause is
// runtime state (KV scope pauseKVScope, one key per server), not configuration: it
// survives restarts and new revisions until it is resumed explicitly.

const pauseKVScope = "enforcement-pause"

// PauseInfo describes a paused server: who paused it, when and why.
type PauseInfo struct {
	// By is the actor (audit log form: "discord:<id>", "runtime-token").
	By string `json:"by"`
	// ByName is the display name of the Discord user, when known.
	ByName string    `json:"byName,omitempty"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

// Who is the name to show for the actor.
func (p PauseInfo) Who() string {
	if p.ByName != "" {
		return p.ByName
	}
	return p.By
}

// PausedError is returned when an apply is refused because the server is paused.
type PausedError struct {
	Server string
	Info   PauseInfo
}

func (e *PausedError) Error() string {
	msg := fmt.Sprintf("enforcement is paused on server %s by %s since %s", e.Server, e.Info.Who(), e.Info.At.UTC().Format(time.RFC3339))
	if e.Info.Reason != "" {
		msg += fmt.Sprintf(" (%s)", e.Info.Reason)
	}
	return msg + ": nothing is applied until it is resumed"
}

func (e *PausedError) Unwrap() error { return reconcile.ErrPaused }

// MaxPauseReason bounds the length of a pause reason (characters).
const MaxPauseReason = 500

// pauseState is the in-memory copy of the pauses (server key → info), loaded from the
// store before the first activation and written through on every change.
type pauseState struct {
	loaded bool
	m      map[string]PauseInfo
}

// loadPauses reads the pauses from the store (once). It is called with syncMu held.
func (r *Runtime) loadPauses(ctx context.Context) error {
	r.pauseMu.RLock()
	loaded := r.pauses.loaded
	r.pauseMu.RUnlock()
	if loaded || r.opts.Store == nil {
		return nil
	}
	kv := r.opts.Store.KV(pauseKVScope)
	m := map[string]PauseInfo{}
	cursor := ""
	for {
		items, next, err := kv.List(ctx, "", 500, cursor)
		if err != nil {
			return fmt.Errorf("load paused servers: %w", err)
		}
		for _, it := range items {
			var p PauseInfo
			if err := json.Unmarshal(it.Value, &p); err != nil {
				r.log.Warn("invalid pause record", "server", it.Key, "err", err)
				p = PauseInfo{By: "unknown"} // still paused: a pause is never dropped silently
			}
			m[it.Key] = p
		}
		if next == "" {
			break
		}
		cursor = next
	}
	r.pauseMu.Lock()
	r.pauses = pauseState{loaded: true, m: m}
	r.pauseMu.Unlock()
	return nil
}

// Paused returns the pause of a server.
func (r *Runtime) Paused(server string) (PauseInfo, bool) {
	r.pauseMu.RLock()
	defer r.pauseMu.RUnlock()
	p, ok := r.pauses.m[server]
	return p, ok
}

// isPaused is the reconcilers' Options.Paused.
func (r *Runtime) isPaused(server string) bool {
	_, ok := r.Paused(server)
	return ok
}

// Pause pauses enforcement on a server. It returns the pause in effect and whether it was
// created by this call (pausing a paused server changes nothing). When it returns,
// nothing more is applied to the server: a self-heal sync in flight has finished.
func (r *Runtime) Pause(ctx context.Context, server string, p PauseInfo) (PauseInfo, bool, error) {
	a := r.Active()
	if a == nil {
		return PauseInfo{}, false, errors.New("no active revision")
	}
	rt := a.servers[server]
	if rt == nil {
		return PauseInfo{}, false, fmt.Errorf("unknown server %q", server)
	}
	p.Reason = strings.TrimSpace(p.Reason)
	if len([]rune(p.Reason)) > MaxPauseReason {
		return PauseInfo{}, false, fmt.Errorf("the reason is longer than %d characters", MaxPauseReason)
	}
	if p.At.IsZero() {
		p.At = time.Now().UTC()
	}
	r.pauseMu.Lock()
	if cur, ok := r.pauses.m[server]; ok {
		r.pauseMu.Unlock()
		return cur, false, nil
	}
	data, _ := json.Marshal(p)
	// Written to the store first: a pause that is not persisted would end at the next
	// restart without anyone resuming it.
	if err := r.opts.Store.KV(pauseKVScope).Set(ctx, server, data, 0); err != nil {
		r.pauseMu.Unlock()
		return PauseInfo{}, false, fmt.Errorf("store the pause: %w", err)
	}
	if r.pauses.m == nil {
		r.pauses.m = map[string]PauseInfo{}
	}
	r.pauses.m[server] = p
	r.pauseMu.Unlock()

	if rt.rec != nil {
		rt.rec.Quiesce()
	}
	msg := "enforcement paused by " + p.Who()
	if p.Reason != "" {
		msg += ": " + p.Reason
	}
	details, _ := json.Marshal(p)
	r.record(ctx, state.SyncRecord{Revision: a.rev.ID, Target: "server:" + server, Status: SyncRecordPaused, Message: msg, Details: details})
	r.log.Warn("enforcement paused", "server", server, "by", p.By, "reason", p.Reason)
	r.refreshPlan(ctx, rt)
	r.notify(ChangeStatus, "")
	return p, true, nil
}

// Resume ends the pause of a server and returns it (false when the server was not
// paused). Nothing is applied by Resume itself: the next revision, Sync now, periodic
// resync (selfHeal) or drift event does, as without a pause.
func (r *Runtime) Resume(ctx context.Context, server, by, byName string) (PauseInfo, bool, error) {
	a := r.Active()
	if a == nil {
		return PauseInfo{}, false, errors.New("no active revision")
	}
	rt := a.servers[server]
	if rt == nil {
		return PauseInfo{}, false, fmt.Errorf("unknown server %q", server)
	}
	r.pauseMu.Lock()
	cur, ok := r.pauses.m[server]
	if !ok {
		r.pauseMu.Unlock()
		return PauseInfo{}, false, nil
	}
	if err := r.opts.Store.KV(pauseKVScope).Delete(ctx, server); err != nil && !errors.Is(err, state.ErrNotFound) {
		r.pauseMu.Unlock()
		return PauseInfo{}, false, fmt.Errorf("remove the pause: %w", err)
	}
	delete(r.pauses.m, server)
	r.pauseMu.Unlock()

	who := byName
	if who == "" {
		who = by
	}
	details, _ := json.Marshal(map[string]any{"by": by, "byName": byName, "pause": cur})
	r.record(ctx, state.SyncRecord{Revision: a.rev.ID, Target: "server:" + server, Status: SyncRecordResumed,
		Message: fmt.Sprintf("enforcement resumed by %s (paused by %s for %s)", who, cur.Who(), time.Since(cur.At).Round(time.Second)), Details: details})
	r.log.Info("enforcement resumed", "server", server, "by", by)
	r.refreshPlan(ctx, rt)
	r.notify(ChangeStatus, "")
	return cur, true, nil
}

// refreshPlan re-plans a server so that its status message matches the pause.
func (r *Runtime) refreshPlan(ctx context.Context, rt *serverRT) {
	if rt.rec == nil {
		return
	}
	if _, err := rt.rec.Plan(ctx); err != nil {
		r.log.Warn("server plan failed", "server", rt.key, "err", err)
	}
}

// Sync record statuses besides "succeeded", "failed" and "rejected".
const (
	// SyncRecordPaused / SyncRecordResumed: enforcement was paused / resumed on a server.
	SyncRecordPaused  = "paused"
	SyncRecordResumed = "resumed"
	// SyncRecordSkipped: a revision was not applied to a paused server.
	SyncRecordSkipped = "skipped"
)
