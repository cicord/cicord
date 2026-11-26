package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

const operationRetention = 7 * 24 * time.Hour

func operationPrefix(actor string, action int, target string) string {
	return fmt.Sprintf("%s:%d:%s:", actor, action, target)
}

func operationKey(op discord.Operation) string {
	nonce := op.Nonce
	if nonce == "" {
		nonce = rand.Text()
	}
	return fmt.Sprintf("%s%020d:%s", operationPrefix(op.ActorID, op.Action, op.Target), op.At.UnixMilli(), nonce)
}

func (r *Runtime) operationScope(origin discord.Operation) *discord.OperationScope {
	return &discord.OperationScope{Origin: origin, Record: func(ctx context.Context, op discord.Operation) {
		// Keep journal recovery independent of a caller disconnect, and never turn
		// failure to journal into a repeated action.
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		data, err := json.Marshal(op)
		if err == nil {
			err = r.opts.Store.KV("emitted:"+op.ServerID).Set(bounded, operationKey(op), data, operationRetention)
		}
		if err != nil {
			r.log.Warn("could not record emitted operation", "method", op.Method, "err", err)
		}
	}, Forget: func(ctx context.Context, op discord.Operation) {
		if op.Nonce == "" {
			return
		}
		if err := r.opts.Store.KV("emitted:"+op.ServerID).Delete(ctx, operationKey(op)); err != nil {
			r.log.Warn("could not withdraw rejected operation", "method", op.Method, "err", err)
		}
	}}
}

// ownBot returns the project bot whose user ID performed an audited action.
func (r *Runtime) ownBot(userID string) (discord.User, bool) {
	for _, h := range r.hosts() {
		if self := h.session.Self(); self.ID == userID {
			return self, true
		}
	}
	return discord.User{}, false
}

// maxAuditCandidates bounds the unclaimed operations considered for one entry (and
// four times as many journal rows read).
const maxAuditCandidates = 64

func (r *Runtime) attribute(ctx context.Context, e discord.AuditLogEntryEvent) discord.Attribution {
	a := discord.Attribution{Kind: "unknown", Certainty: "unknown"}
	if e.UserID == "" {
		return a
	}
	if _, own := r.ownBot(e.UserID); !own {
		if e.Actor == nil {
			return a
		}
		a.Kind = "human"
		if e.Actor.Bot {
			a.Kind = "bot"
		}
		a.Certainty = "confirmed"
		if discord.DescribeAuditAction(e.ActionType).Mergeable {
			a.Certainty = "probable"
		}
		return a
	}
	a.Kind = "cicord"
	a.Certainty = "probable"
	at := discord.SnowflakeTime(e.ID)
	if at.IsZero() || e.TargetID == "" || discord.DescribeAuditAction(e.ActionType).Mergeable {
		return a
	}
	action := discord.JournalAction(e.ActionType)
	prefix := operationPrefix(e.UserID, action, e.TargetID)
	kv, ok := r.opts.Store.KV("emitted:" + e.ServerID).(state.AdvancedKV)
	if !ok {
		return a
	}
	// Overwrite entries: a create or update binds the operation of the overwritten
	// role or member; a delete binds the removal marker of the call, which the
	// removals of one call share (the request does not name what it removes).
	shared := e.ActionType == discord.AuditOverwriteDelete
	detail := overwriteDetail(e)
	matches := func(op discord.Operation) bool {
		if op.ActorID != e.UserID || op.Action != action || op.Target != e.TargetID || op.ServerID != e.ServerID {
			return false
		}
		switch {
		case action != discord.AuditOverwriteCreate:
			return true
		case shared:
			return op.Detail == discord.OverwriteRemovals
		case detail != "":
			return op.Detail == detail
		}
		return op.Detail != discord.OverwriteRemovals
	}
	entryKey := "entry:" + e.ID
	// A second observation of the same entry (REST, gateway or another subscriber)
	// returns the original proof even if another operation was emitted since.
	bound := func() (*discord.Operation, error) {
		data, err := kv.Get(ctx, entryKey)
		if err != nil {
			return nil, err
		}
		var key string
		if err = json.Unmarshal(data, &key); err != nil || !strings.HasPrefix(key, prefix) {
			return nil, errors.New("invalid audit binding")
		}
		data, err = kv.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		var op discord.Operation
		if err = json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		if !matches(op) {
			return nil, errors.New("audit binding scope mismatch")
		}
		return &op, nil
	}
	if op, err := bound(); err == nil {
		a.Certainty = "confirmed"
		a.Operation = op
		return a
	} else if !errors.Is(err, state.ErrNotFound) {
		return a
	}
	// Candidates: unclaimed operations on this target within ±30 s. An operation
	// already bound to another entry is skipped, so two identical calls in the window
	// (a replacement ban, two role grants) each confirm their own entry. Binding
	// stays one to one: a forged entry can at most take an operation away from a
	// genuine one, which is then reported probable and alerts.
	type candidate struct {
		key string
		op  discord.Operation
	}
	var candidates []candidate
	cursor := fmt.Sprintf("%s%020d", prefix, at.Add(-30*time.Second).UnixMilli())
	scanned := 0
scan:
	for len(candidates) < maxAuditCandidates && scanned < 4*maxAuditCandidates {
		rows, next, err := kv.List(ctx, prefix, 32, cursor)
		if err != nil {
			return a
		}
		scanned += len(rows)
		for _, row := range rows {
			var op discord.Operation
			if json.Unmarshal(row.Value, &op) != nil {
				return a
			}
			if op.At.After(at.Add(30 * time.Second)) {
				break scan
			}
			if !matches(op) {
				continue
			}
			if !shared {
				if _, err := kv.Get(ctx, "claimed:"+row.Key); err == nil {
					continue
				} else if !errors.Is(err, state.ErrNotFound) {
					return a
				}
			}
			candidates = append(candidates, candidate{row.Key, op})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	// The journal records a call before Discord writes its entry: bind the latest
	// operation at or before the entry, else (clock skew) the earliest one after it.
	pick := func() int {
		best := -1
		for n, c := range candidates {
			if !c.op.At.After(at) {
				best = n
			}
		}
		if best < 0 && len(candidates) > 0 {
			best = 0
		}
		return best
	}
	encodedEntry, _ := json.Marshal(e.ID)
	for n := pick(); n >= 0; n = pick() {
		c := candidates[n]
		candidates = append(candidates[:n], candidates[n+1:]...)
		encodedKey, _ := json.Marshal(c.key)
		checks := []state.KVCheck{{Key: entryKey}}
		writes := []state.KVMutation{{Op: "set", Key: entryKey, Value: encodedKey, TTL: operationRetention}}
		if !shared {
			claimKey := "claimed:" + c.key
			checks = append(checks, state.KVCheck{Key: claimKey})
			writes = append(writes, state.KVMutation{Op: "set", Key: claimKey, Value: encodedEntry, TTL: operationRetention})
		}
		commit, err := kv.Atomic(ctx, checks, writes)
		if err != nil {
			return a
		}
		if commit.OK {
			a.Certainty = "confirmed"
			a.Operation = &c.op
			return a
		}
		// Another observer bound this entry, or another entry took the operation.
		if op, err := bound(); err == nil {
			a.Certainty = "confirmed"
			a.Operation = op
			return a
		}
	}
	return a
}

// overwriteDetail is the overwritten role or member of an overwrite entry.
func overwriteDetail(e discord.AuditLogEntryEvent) string {
	if discord.JournalAction(e.ActionType) != discord.AuditOverwriteCreate {
		return ""
	}
	var id string
	if json.Unmarshal(e.Options["id"], &id) != nil || !discord.IsSnowflake(id) {
		return ""
	}
	return id
}

func (r *Runtime) enrichAudit(ctx context.Context, client discord.Client, e discord.AuditLogEntryEvent) discord.AuditLogEntryEvent {
	if e.UserID != "" && e.Actor == nil {
		// Gateway entries carry only user_id. A project bot is known locally: its
		// attribution does not wait on a Discord lookup.
		if self, own := r.ownBot(e.UserID); own {
			e.Actor = &self
		} else if u, err := client.GetUser(ctx, e.UserID); err == nil {
			e.Actor = &u
		}
	}
	e.Attribution = r.attribute(ctx, e)
	return e
}

func unconfirmedOwn(e discord.AuditLogEntryEvent) bool {
	return e.Attribution.Kind == "cicord" && e.Attribution.Certainty == "probable" && !discord.DescribeAuditAction(e.ActionType).Mergeable
}

// provable reports whether a missing proof for an own entry is evidence: the journal
// records this action on this target. Other own entries (onboarding prompts, entries
// without a target, actions Cicord does not journal) stay probable without an alert.
func provable(e discord.AuditLogEntryEvent) bool {
	return e.TargetID != "" && discord.Journaled(e.ActionType)
}

// auditRecheck is how long an unconfirmed own entry waits for its proof: the journal
// row of a creation is written once Discord answers, which can be after the gateway
// delivered its audit entry.
var auditRecheck = 1500 * time.Millisecond

// Only declared audit subscribers need actor lookups. Cap concurrent lookups so
// slow Discord responses cannot stall the gateway. A saturated queue reports
// unknown attribution and cannot authorize an automatic sanction.
func (h *botHost) dispatchAudit(e discord.AuditLogEntryEvent) {
	subscribed := false
	for _, i := range h.routing.Load().byServer[e.ServerID] {
		if i.plugin.def.HasEvent("auditLogEntry") {
			subscribed = true
			break
		}
	}
	if _, own := h.rt.ownBot(e.UserID); !subscribed && !own {
		return // only entries of a project bot need the CORE-04 check
	}
	select {
	case h.rt.auditSlots <- struct{}{}:
		go func() {
			release := sync.OnceFunc(func() { <-h.rt.auditSlots })
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			entry := h.rt.enrichAudit(ctx, h.session, e)
			if unconfirmedOwn(entry) && provable(entry) {
				// Wait outside the lookup slots: a large reconcile must not starve them.
				release()
				select {
				case <-time.After(auditRecheck):
				case <-ctx.Done():
				}
				recheck, stop := context.WithTimeout(context.Background(), time.Second)
				entry.Attribution = h.rt.attribute(recheck, entry)
				stop()
			}
			switch {
			case unconfirmedOwn(entry) && provable(entry):
				h.rt.log.Warn("unconfirmed audit activity by a project bot: its token may be compromised", "server", e.ServerID, "entry", e.ID, "action", discord.DescribeAuditAction(e.ActionType).Name)
				h.rt.noteUnconfirmedAudit(h.key, entry)
			case unconfirmedOwn(entry):
				h.rt.log.Info("audit entry by a project bot that the journal cannot prove", "server", e.ServerID, "entry", e.ID, "action", discord.DescribeAuditAction(e.ActionType).Name)
			}
			if subscribed {
				h.dispatchPlugins(h.routing.Load(), entry)
			}
		}()
	default:
		// The CORE-04 check of a saturated queue is lost with the lookup: say so.
		h.rt.log.Warn("audit attribution queue full: entry not checked", "server", e.ServerID, "entry", e.ID)
		if subscribed {
			e.Attribution = discord.Attribution{Kind: "unknown", Certainty: "unknown"}
			h.dispatchPlugins(h.routing.Load(), e)
		}
	}
}

// UnconfirmedAudit is the CORE-04 alert of a server: Discord recorded audited actions
// by a project bot that match no operation Cicord emitted. Someone else may be using
// the bot token. It is advisory: it does not change the server's health (so it never
// blocks status --wait or an update), and expires a day after the last entry.
type UnconfirmedAudit struct {
	Bot        string    `json:"bot"`
	Count      int       `json:"count"`
	First      time.Time `json:"first"`
	Last       time.Time `json:"last"`
	LastEntry  string    `json:"lastEntry"`
	LastAction string    `json:"lastAction"`
}

const unconfirmedAuditRetention = 24 * time.Hour

func (r *Runtime) noteUnconfirmedAudit(bot string, e discord.AuditLogEntryEvent) {
	now := time.Now().UTC()
	r.auditMu.Lock()
	if r.unconfirmed == nil {
		r.unconfirmed = map[string]*UnconfirmedAudit{}
	}
	u := r.unconfirmed[e.ServerID]
	if u == nil || now.Sub(u.Last) > unconfirmedAuditRetention {
		u = &UnconfirmedAudit{First: now}
		r.unconfirmed[e.ServerID] = u
	}
	u.Bot, u.Count, u.Last, u.LastEntry, u.LastAction = bot, u.Count+1, now, e.ID, discord.DescribeAuditAction(e.ActionType).Name
	r.auditMu.Unlock()
	r.notify(ChangeStatus, "")
}

// unconfirmedAudit returns the current alert of a server (by Discord ID).
func (r *Runtime) unconfirmedAudit(discordID string) *UnconfirmedAudit {
	r.auditMu.Lock()
	defer r.auditMu.Unlock()
	u := r.unconfirmed[discordID]
	if u == nil || time.Since(u.Last) > unconfirmedAuditRetention {
		return nil
	}
	c := *u
	return &c
}
