package reconcile

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/state"
)

// Options configures a Reconciler for one server.
type Options struct {
	// Server is the desired state (required). Its File is used to resolve the icon.
	Server *config.Server
	// RepoRoot is the repository root (config.Repo.Root); profile.icon is resolved
	// relative to the server YAML file inside it.
	RepoRoot string
	// Client talks to Discord as the bot that manages the server (required).
	Client discord.Client
	// Resources is the resource map (required).
	Resources state.ResourceMap
	// KV stores reconciler bookkeeping (the last uploaded icon). Recommended scope:
	// "reconcile:<server key>". When nil, it is kept in memory.
	KV state.KV
	// BotUserID is the bot's user ID, used for the role hierarchy check. Empty
	// disables the check.
	BotUserID string
	// Logger defaults to a discarding logger.
	Logger *slog.Logger
	// Reason is the audit log reason sent to Discord (default "codi sync").
	Reason string
	// SelfHealDelay debounces event-triggered syncs (default 2s).
	SelfHealDelay time.Duration
	// Context is the parent context of event-triggered syncs (default Background).
	Context context.Context
	// OnSync is called after every event-triggered (selfHeal) sync.
	OnSync func(Plan, Result, error)
}

// Reconciler plans and applies one server. Its methods are safe for concurrent use;
// Plan, Apply, Sync and Drift are serialized.
type Reconciler struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex // serializes plan/apply
	desired *config.Server

	emu     sync.Mutex // event state
	managed map[string]bool
	drifted bool
	timer   *time.Timer
	closed  bool
	running sync.WaitGroup // event-triggered syncs in flight
	iconMem iconState
	imgMem  map[string]iconState
	srvID   string
	key     string         // server key (immutable)
	cfg     *config.Server // copy of desired for event filtering
}

// New creates a reconciler. It panics when a required option is missing.
func New(opts Options) *Reconciler {
	if opts.Server == nil || opts.Client == nil || opts.Resources == nil {
		panic("reconcile.New: Server, Client and Resources are required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Reason == "" {
		opts.Reason = "codi sync"
	}
	if opts.SelfHealDelay <= 0 {
		opts.SelfHealDelay = 2 * time.Second
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	r := &Reconciler{opts: opts, log: opts.Logger, managed: map[string]bool{}, key: opts.Server.Metadata.Name}
	r.setDesired(opts.Server)
	return r
}

func (r *Reconciler) serverKey() string { return r.key }

func (r *Reconciler) setDesired(s *config.Server) {
	r.desired = s
	r.emu.Lock()
	r.cfg, r.srvID = s, s.Spec.DiscordID
	r.emu.Unlock()
}

// SetDesired replaces the desired state (a new revision). The server key and Discord
// ID must not change.
func (r *Reconciler) SetDesired(s *config.Server, repoRoot string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts.RepoRoot = repoRoot
	r.setDesired(s)
}

// Plan fetches the live state and computes the diff. Nothing is applied.
func (r *Reconciler) Plan(ctx context.Context) (Plan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.plan(ctx)
}

func (r *Reconciler) plan(ctx context.Context) (Plan, error) {
	sp := &r.desired.Spec
	mapped, err := r.opts.Resources.List(ctx, r.serverKey())
	if err != nil {
		return Plan{}, fmt.Errorf("list resource map: %w", err)
	}
	snap, err := FetchWith(ctx, r.opts.Client, sp.DiscordID, r.opts.BotUserID, FetchOptionsFor(sp, mapped))
	if err != nil {
		return Plan{}, err
	}
	in := planInput{Desired: r.desired, Live: snap, Mapped: mapped, BotTop: snap.BotTopPosition(),
		Files: map[string][]byte{}, FileState: map[string]iconState{}}
	dir := filepath.Join(r.opts.RepoRoot, filepath.Dir(filepath.FromSlash(r.desired.File)))
	for name, rel := range parityFiles(sp) {
		if in.Files[name], err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			return Plan{}, fmt.Errorf("%s: %w", name, err)
		}
		if in.FileState[name], err = r.loadImageState(ctx, name); err != nil {
			return Plan{}, fmt.Errorf("load %s state: %w", name, err)
		}
	}
	if sp.Profile != nil && sp.Profile.Icon != "" {
		path := filepath.Join(r.opts.RepoRoot, filepath.Dir(filepath.FromSlash(r.desired.File)), filepath.FromSlash(sp.Profile.Icon))
		if in.Icon, err = os.ReadFile(path); err != nil {
			return Plan{}, fmt.Errorf("profile.icon: %w", err)
		}
		if in.IconState, err = r.loadIconState(ctx); err != nil {
			return Plan{}, fmt.Errorf("load icon state: %w", err)
		}
	}
	p, err := computePlan(in)
	if err != nil {
		return Plan{}, err
	}
	r.refreshManaged(mapped)
	r.emu.Lock()
	r.drifted = !p.Empty() || len(p.Blocked) > 0
	r.emu.Unlock()
	return p, nil
}

// Apply executes a plan produced by Plan: in phase order, recording mappings as soon
// as resources are created, continuing past failures. The returned error summarizes
// failed changes (details in Result.Failed).
func (r *Reconciler) Apply(ctx context.Context, p Plan) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applyAndRefresh(ctx, p)
}

func (r *Reconciler) applyAndRefresh(ctx context.Context, p Plan) (Result, error) {
	res, err := r.apply(ctx, p)
	if mapped, lerr := r.opts.Resources.List(ctx, r.serverKey()); lerr == nil {
		r.refreshManaged(mapped)
	}
	r.emu.Lock()
	r.drifted = err != nil
	r.emu.Unlock()
	r.log.Info("reconcile: applied", "server", r.serverKey(), "applied", len(res.Applied), "failed", len(res.Failed))
	return res, err
}

// Sync plans and applies. When the plan is empty nothing is called on Discord
// besides the fetch.
func (r *Reconciler) Sync(ctx context.Context) (Plan, Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.plan(ctx)
	if err != nil {
		return p, Result{Applied: []Change{}, Failed: []Failure{}}, err
	}
	if p.Empty() {
		return p, Result{Applied: []Change{}, Failed: []Failure{}}, nil
	}
	res, err := r.applyAndRefresh(ctx, p)
	return p, res, err
}

// Drift returns the differences between the desired and the live state.
func (r *Reconciler) Drift(ctx context.Context) ([]DriftEntry, error) {
	p, err := r.Plan(ctx)
	if err != nil {
		return nil, err
	}
	return DriftOf(p), nil
}

// DriftOf converts a plan into drift entries: one per differing field of each change
// and of each blocked operation (a blocked delete is "exists: false/true").
func DriftOf(p Plan) []DriftEntry {
	out := []DriftEntry{}
	for _, c := range p.Changes {
		switch c.Action {
		case ActionCreate:
			out = append(out, DriftEntry{Kind: c.Kind, Key: c.Key, Field: "exists", Expected: true, Actual: false})
			continue
		case ActionDelete:
			out = append(out, DriftEntry{Kind: c.Kind, Key: c.Key, Field: "exists", Expected: false, Actual: true})
			continue
		case ActionUnmanage:
			out = append(out, DriftEntry{Kind: c.Kind, Key: c.Key, Field: "managed", Expected: false, Actual: true})
			continue
		case ActionAdopt:
			out = append(out, DriftEntry{Kind: c.Kind, Key: c.Key, Field: "managed", Expected: true, Actual: false})
		}
		names := make([]string, 0, len(c.Fields))
		for f := range c.Fields {
			names = append(names, f)
		}
		sort.Strings(names)
		for _, f := range names {
			fc := c.Fields[f]
			out = append(out, DriftEntry{Kind: c.Kind, Key: c.Key, Field: f, Expected: fc.To, Actual: fc.From})
		}
	}
	// blocked operations are differences too (status stays OutOfSync)
	for _, b := range p.Blocked {
		if b.Action == ActionDelete {
			out = append(out, DriftEntry{Kind: b.Kind, Key: b.Key, Field: "exists", Expected: false, Actual: true})
			continue
		}
		for _, f := range sortedKeys(b.Fields) {
			fc := b.Fields[f]
			out = append(out, DriftEntry{Kind: b.Kind, Key: b.Key, Field: f, Expected: fc.To, Actual: fc.From})
		}
	}
	return out
}

// DriftPending reports whether the last plan had changes or blocked operations, the
// last apply failed, or a relevant gateway event was received since.
func (r *Reconciler) DriftPending() bool {
	r.emu.Lock()
	defer r.emu.Unlock()
	return r.drifted
}

func (r *Reconciler) refreshManaged(mapped []state.Resource) {
	m := make(map[string]bool, len(mapped))
	for _, res := range mapped {
		if res.Owner == state.OwnerGit {
			m[res.ID] = true
		}
	}
	r.emu.Lock()
	r.managed = m
	r.emu.Unlock()
}

// HandleEvent inspects a gateway event. Events about git-managed roles/channels, the
// @everyone role (when spec.everyone is set) or the server profile (when
// spec.profile is set) are relevant: drift is marked and, with syncPolicy.selfHeal,
// a debounced Sync is scheduled. It never blocks on Discord.
//
// The managed set is the one seen by the last Plan/Apply, so call Plan or Sync once
// before relying on it.
func (r *Reconciler) HandleEvent(ev discord.Event) bool {
	r.emu.Lock()
	defer r.emu.Unlock()
	if r.closed || discord.ServerIDOf(ev) != r.srvID {
		return false
	}
	relevant := false
	sp := &r.cfg.Spec
	switch e := ev.(type) {
	case discord.ChannelUpdateEvent:
		relevant = r.managed[e.Channel.ID]
	case discord.ChannelDeleteEvent:
		relevant = r.managed[e.Channel.ID]
	case discord.RoleUpdateEvent:
		relevant = r.managed[e.Role.ID] || e.Role.ID == r.srvID && sp.Everyone != nil
	case discord.RoleDeleteEvent:
		relevant = r.managed[e.RoleID]
	case discord.ServerUpdateEvent:
		relevant = sp.Profile != nil || sp.Community != nil || sp.AFK != nil
	}
	if !relevant {
		return false
	}
	r.drifted = true
	if sp.SyncPolicy.SelfHeal() {
		if r.timer != nil {
			r.timer.Stop()
		}
		r.timer = time.AfterFunc(r.opts.SelfHealDelay, r.selfHeal)
	}
	return true
}

func (r *Reconciler) selfHeal() {
	r.emu.Lock()
	if r.closed {
		r.emu.Unlock()
		return
	}
	r.running.Add(1)
	r.emu.Unlock()
	defer r.running.Done()
	p, res, err := r.Sync(r.opts.Context)
	if err != nil {
		r.log.Warn("reconcile: selfHeal sync failed", "server", r.serverKey(), "err", err)
	} else if !p.Empty() {
		r.log.Info("reconcile: drift reverted", "server", r.serverKey(), "changes", len(p.Changes))
	}
	if r.opts.OnSync != nil {
		r.opts.OnSync(p, res, err)
	}
}

// Close stops pending event-triggered syncs and waits for a running one to finish
// (cancel Options.Context to make it finish sooner). It must not be called from
// OnSync.
func (r *Reconciler) Close() {
	r.emu.Lock()
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.emu.Unlock()
	r.running.Wait()
}
