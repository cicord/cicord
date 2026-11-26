package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	a, _ := r.checkAttribution(ctx, e)
	return a
}

// checkAttribution attributes an entry and reports whether the journal check of an
// entry by a project bot ran to the end. When the check could not run (a state store
// error or timeout), the attribution is cicord/unknown and checked is false: a
// missing proof is then no evidence, and must never raise the CORE-04 alert.
func (r *Runtime) checkAttribution(ctx context.Context, e discord.AuditLogEntryEvent) (_ discord.Attribution, checked bool) {
	a := discord.Attribution{Kind: "unknown", Certainty: "unknown"}
	if e.UserID == "" {
		return a, true
	}
	if _, own := r.ownBot(e.UserID); !own {
		if e.Actor == nil {
			return a, true
		}
		a.Kind = "human"
		if e.Actor.Bot {
			a.Kind = "bot"
		}
		a.Certainty = "confirmed"
		if discord.DescribeAuditAction(e.ActionType).Mergeable {
			a.Certainty = "probable"
		}
		return a, true
	}
	a.Kind = "cicord"
	a.Certainty = "probable"
	at := discord.SnowflakeTime(e.ID)
	if at.IsZero() || e.TargetID == "" || discord.DescribeAuditAction(e.ActionType).Mergeable {
		return a, true
	}
	unchecked := func() (discord.Attribution, bool) {
		return discord.Attribution{Kind: "cicord", Certainty: "unknown"}, false
	}
	action := discord.JournalAction(e.ActionType)
	prefix := operationPrefix(e.UserID, action, e.TargetID)
	kv, ok := r.opts.Store.KV("emitted:" + e.ServerID).(state.AdvancedKV)
	if !ok {
		return unchecked()
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
		return a, true
	} else if !errors.Is(err, state.ErrNotFound) {
		return unchecked()
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
			return unchecked()
		}
		scanned += len(rows)
		for _, row := range rows {
			var op discord.Operation
			if json.Unmarshal(row.Value, &op) != nil {
				return unchecked()
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
					return unchecked()
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
			return unchecked()
		}
		if commit.OK {
			a.Certainty = "confirmed"
			a.Operation = &c.op
			return a, true
		}
		// Another observer bound this entry, or another entry took the operation.
		if op, err := bound(); err == nil {
			a.Certainty = "confirmed"
			a.Operation = op
			return a, true
		} else if !errors.Is(err, state.ErrNotFound) {
			return unchecked()
		}
	}
	return a, true
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
	e, _ = r.enrichAuditChecked(ctx, client, e)
	return e
}

// enrichAuditChecked is enrichAudit, reporting whether the journal check of an entry
// by a project bot ran (see checkAttribution).
func (r *Runtime) enrichAuditChecked(ctx context.Context, client discord.Client, e discord.AuditLogEntryEvent) (discord.AuditLogEntryEvent, bool) {
	if e.UserID != "" && e.Actor == nil {
		// Gateway entries carry only user_id. A project bot is known locally: its
		// attribution does not wait on a Discord lookup.
		if self, own := r.ownBot(e.UserID); own {
			e.Actor = &self
		} else if u, err := r.actors.lookup(ctx, client, e.UserID, r.auditQ.timeout); err == nil {
			e.Actor = &u
		}
	}
	var checked bool
	e.Attribution, checked = r.checkAttribution(ctx, e)
	return e, checked
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

var (
	// auditQueueCap bounds the gateway audit entries accepted for attribution and not
	// finished yet (waiting, being checked, or waiting for their recheck). Discord
	// writes one entry per created role, channel, overwrite and webhook, and a first
	// sync delivers them in one burst: the queue holds the burst of a large server.
	auditQueueCap = 4096
	// auditWorkers is the number of entries attributed at once.
	auditWorkers = 8
	// auditCheckTimeout bounds one attribution: the actor lookup and the journal check.
	// It starts when a worker takes the entry, not when the gateway queued it.
	auditCheckTimeout = 3 * time.Second
	// auditOverflowReport is how often a lasting overflow is reported.
	auditOverflowReport = time.Minute
	// auditGate, when set (tests), runs before each attribution.
	auditGate func()
)

type auditJob struct {
	host       *botHost
	entry      discord.AuditLogEntryEvent
	subscribed bool
	recheck    bool
}

// auditQueue is the FIFO of gateway audit entries waiting for attribution. Workers
// start on demand and stop when it is empty. It never blocks the gateway: an entry
// that does not fit is not checked (see dispatchAudit), and the overflow is counted
// and logged once per episode with the count.
type auditQueue struct {
	// Settings, read from the package variables when the runtime is created.
	capacity, maxWorkers    int
	timeout, recheck, every time.Duration
	gate                    func()

	mu      sync.Mutex
	jobs    []auditJob
	pending int // accepted entries not finished, rechecks included
	workers int
	// Entries not checked since the last report: dropped by an overflow, or whose
	// journal check failed.
	dropped, failed int
	servers         map[string]struct{}
	since           time.Time
}

// enqueueAudit accepts an entry for attribution. A recheck keeps the place its entry
// already holds, so it is never refused.
func (r *Runtime) enqueueAudit(j auditJob) bool {
	q := &r.auditQ
	q.mu.Lock()
	if !j.recheck && q.pending >= q.capacity {
		q.noteUncheckedLocked(j.entry.ServerID, true)
		report := time.Since(q.since) >= q.every
		q.mu.Unlock()
		if report {
			r.reportUncheckedAudit()
		}
		return false
	}
	if !j.recheck {
		q.pending++
	}
	q.jobs = append(q.jobs, j)
	spawn := q.workers < q.maxWorkers
	if spawn {
		q.workers++
	}
	q.mu.Unlock()
	if spawn {
		go r.auditWorker()
	}
	return true
}

func (q *auditQueue) noteUncheckedLocked(server string, dropped bool) {
	if dropped {
		q.dropped++
	} else {
		q.failed++
	}
	if q.since.IsZero() {
		q.since = time.Now()
	}
	if q.servers == nil {
		q.servers = map[string]struct{}{}
	}
	if len(q.servers) < 16 {
		q.servers[server] = struct{}{}
	}
}

// reportUncheckedAudit logs the entries not checked since the last report, once.
func (r *Runtime) reportUncheckedAudit() {
	q := &r.auditQ
	q.mu.Lock()
	dropped, failed, since := q.dropped, q.failed, q.since
	servers := make([]string, 0, len(q.servers))
	for s := range q.servers {
		servers = append(servers, s)
	}
	q.dropped, q.failed, q.servers, q.since = 0, 0, nil, time.Time{}
	q.mu.Unlock()
	if dropped+failed == 0 {
		return
	}
	slices.Sort(servers)
	// Not checked is not unconfirmed: these entries raised no alert.
	r.log.Warn("audit entries not checked for attribution: no alert was raised for them", "queueFull", dropped, "checkFailed", failed,
		"since", since.UTC().Format(time.RFC3339), "servers", strings.Join(servers, ","))
}

func (r *Runtime) auditWorker() {
	q := &r.auditQ
	for {
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.workers--
			q.mu.Unlock()
			return
		}
		j := q.jobs[0]
		q.jobs[0] = auditJob{}
		q.jobs = q.jobs[1:]
		q.mu.Unlock()
		r.attributeJob(j)
	}
}

func (r *Runtime) attributeJob(j auditJob) {
	q := &r.auditQ
	if q.gate != nil {
		q.gate()
	}
	h, e := j.host, j.entry
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	var checked bool
	if j.recheck {
		e.Attribution, checked = r.checkAttribution(ctx, e)
	} else {
		e, checked = r.enrichAuditChecked(ctx, h.session, e)
	}
	cancel()
	if !j.recheck && (!checked || unconfirmedOwn(e) && provable(e)) {
		// Wait outside the workers: a large reconcile must not starve them. A check that
		// could not run gets a second chance too.
		j.entry, j.recheck = e, true
		time.AfterFunc(q.recheck, func() { r.enqueueAudit(j) })
		return
	}
	switch {
	case !checked:
		q.mu.Lock()
		q.noteUncheckedLocked(e.ServerID, false)
		q.mu.Unlock()
	case unconfirmedOwn(e) && provable(e):
		r.log.Warn("unconfirmed audit activity by a project bot: its token may be compromised", "server", e.ServerID, "entry", e.ID, "action", discord.DescribeAuditAction(e.ActionType).Name)
		r.noteUnconfirmedAudit(h.key, e)
	case unconfirmedOwn(e):
		r.log.Info("audit entry by a project bot that the journal cannot prove", "server", e.ServerID, "entry", e.ID, "action", discord.DescribeAuditAction(e.ActionType).Name)
	}
	if j.subscribed {
		h.dispatchPlugins(h.routing.Load(), e)
	}
	q.mu.Lock()
	q.pending--
	drained := q.pending == 0
	q.mu.Unlock()
	if drained {
		r.reportUncheckedAudit()
	}
}

// dispatchAudit queues a gateway audit entry for attribution: the actor lookup, the
// CORE-04 check of the entries of a project bot, then the plugins that subscribe.
// It never blocks the gateway. An entry that does not fit in a full queue is not
// checked: it never raises the alert, and subscribers receive it with unknown
// attribution, which cannot authorize an automatic sanction.
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
	if h.rt.enqueueAudit(auditJob{host: h, entry: e, subscribed: subscribed}) {
		return
	}
	if subscribed {
		e.Attribution = discord.Attribution{Kind: "unknown", Certainty: "unknown"}
		h.dispatchPlugins(h.routing.Load(), e)
	}
}

// actorCache resolves the actors of gateway audit entries (which carry only a user
// ID). A burst of entries by one actor (a raid deleting channels) costs one Discord
// lookup: concurrent lookups of a user share one call, and users are kept a while.
type actorCache struct {
	mu       sync.Mutex
	users    map[string]cachedActor
	inflight map[string]*actorCall
}

type cachedActor struct {
	user    discord.User
	expires time.Time
}

type actorCall struct {
	done chan struct{}
	user discord.User
	err  error
}

const (
	actorTTL      = 10 * time.Minute
	actorCacheMax = 4096
)

func (c *actorCache) lookup(ctx context.Context, client discord.Client, id string, timeout time.Duration) (discord.User, error) {
	c.mu.Lock()
	if u, ok := c.users[id]; ok && time.Now().Before(u.expires) {
		c.mu.Unlock()
		return u.user, nil
	}
	call := c.inflight[id]
	if call == nil {
		call = &actorCall{done: make(chan struct{})}
		if c.inflight == nil {
			c.inflight = map[string]*actorCall{}
		}
		c.inflight[id] = call
		c.mu.Unlock()
		// The shared call outlives the caller that started it: the others still wait.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		call.user, call.err = client.GetUser(lctx, id)
		cancel()
		c.mu.Lock()
		delete(c.inflight, id)
		if call.err == nil {
			if len(c.users) >= actorCacheMax {
				now := time.Now()
				for k, u := range c.users {
					if !now.Before(u.expires) {
						delete(c.users, k)
					}
				}
				if len(c.users) >= actorCacheMax {
					clear(c.users)
				}
			}
			if c.users == nil {
				c.users = map[string]cachedActor{}
			}
			c.users[id] = cachedActor{call.user, time.Now().Add(actorTTL)}
		}
		close(call.done)
	}
	c.mu.Unlock()
	select {
	case <-call.done:
		return call.user, call.err
	case <-ctx.Done():
		return discord.User{}, ctx.Err()
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
