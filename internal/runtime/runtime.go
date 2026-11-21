// Package runtime is the CODI controller (ARCHITECTURE.md §4–5): it watches a source,
// turns each new revision into a validated, built snapshot, reconciles servers, runs
// plugin instances for every (bot, server) pair and swaps them atomically.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/npjonath/codi/internal/auth"
	"github.com/npjonath/codi/internal/build"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/plugin"
	"github.com/npjonath/codi/internal/reconcile"
	"github.com/npjonath/codi/internal/source"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/version"
)

type Options struct {
	Source    source.Source
	Connector discord.Connector
	Store     state.Store
	// Secrets resolves secretRef names (environment variables when self-hosted).
	Secrets func(name string) (string, bool)
	// Resolver builds the registry plugin resolver for a revision directory (may be nil).
	Resolver func(repoRoot string) config.PluginResolver
	// Signer signs portal links; PortalURL is the public portal origin.
	Signer    *auth.Signer
	PortalURL string
	// Mode is "serve" or "dev"; Simulator reports that Connector is the simulator.
	Mode      string
	Simulator bool
	// ResyncInterval re-runs reconciliation periodically (0 = 5m; negative disables).
	ResyncInterval time.Duration
	PluginLimits   plugin.Limits
	// AllowPrivateNetworks relaxes http.fetch egress checks (dev only).
	AllowPrivateNetworks bool
	Logger               *slog.Logger
}

// Runtime is safe for concurrent use.
type Runtime struct {
	opts Options
	log  *slog.Logger

	syncMu sync.Mutex // serializes revision activation and server syncs

	mu      sync.RWMutex
	active  *active
	lastErr string
	bots    map[string]*botHost
	botErrs sync.Map // bot key → last connection error

	wake  chan struct{}
	sched *scheduler
}

// active is an activated revision.
type active struct {
	rev       source.Revision
	repo      *config.Repo
	builds    map[string]*builtPlugin // by LoadedPlugin.Key
	servers   map[string]*serverRT    // by server key
	instances []*instanceRT
}

type builtPlugin struct {
	code string
	def  *plugin.Definition
	hash string
}

type serverRT struct {
	key   string
	cfg   *config.Server
	cache *serverCache
	rec   *reconcile.Reconciler
	bot   string // reconciling bot key ("" = none)

	mu       sync.Mutex
	health   string
	message  string
	lastSync *time.Time
}

type instanceRT struct {
	cfg      config.Instance
	serverID string
	plugin   *builtPlugin
	inst     *plugin.Instance

	mu       sync.Mutex
	health   string
	message  string
	failures int
}

func (i *instanceRT) id() string { return i.cfg.ID() }

func (i *instanceRT) setHealth(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err == nil {
		i.failures = 0
		i.health, i.message = HealthHealthy, ""
		return
	}
	i.failures++
	i.message = err.Error()
	if i.failures >= 3 {
		i.health = HealthDegraded
	}
}

func New(opts Options) *Runtime {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if opts.Secrets == nil {
		opts.Secrets = os.LookupEnv
	}
	if opts.Mode == "" {
		opts.Mode = "serve"
	}
	if opts.ResyncInterval == 0 {
		opts.ResyncInterval = 5 * time.Minute
	}
	if opts.Signer == nil {
		opts.Signer = auth.NewSigner(auth.RandomKey())
	}
	r := &Runtime{opts: opts, log: opts.Logger, bots: map[string]*botHost{}, wake: make(chan struct{}, 1)}
	r.sched = newScheduler(r)
	return r
}

// Run activates the latest revision and keeps following the source until ctx ends.
func (r *Runtime) Run(ctx context.Context) error {
	defer r.shutdown()
	r.tryFetchAndSync(ctx)
	var resync <-chan time.Time
	if r.opts.ResyncInterval > 0 {
		t := time.NewTicker(r.opts.ResyncInterval)
		defer t.Stop()
		resync = t.C
	}
	go r.sched.run(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.opts.Source.Changes():
			r.tryFetchAndSync(ctx)
		case <-r.wake:
			r.tryFetchAndSync(ctx)
		case <-resync:
			r.resyncServers(ctx)
		}
	}
}

// Trigger asks the runtime to fetch the source now.
func (r *Runtime) Trigger() {
	r.opts.Source.Trigger()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runtime) tryFetchAndSync(ctx context.Context) {
	rev, err := r.opts.Source.Fetch(ctx)
	if err != nil {
		r.setErr(fmt.Sprintf("fetch: %v", err))
		r.log.Error("fetch failed", "err", err)
		return
	}
	if err := r.Activate(ctx, rev, false); err != nil {
		r.log.Error("revision rejected", "revision", rev.ID, "err", err)
	}
}

func (r *Runtime) setErr(msg string) {
	r.mu.Lock()
	r.lastErr = msg
	r.mu.Unlock()
}

func (r *Runtime) record(ctx context.Context, rec state.SyncRecord) {
	if _, err := r.opts.Store.Syncs().Record(ctx, rec); err != nil {
		r.log.Warn("record sync", "err", err)
	}
}

// RejectedError is returned by Activate when a revision fails validation or build.
type RejectedError struct {
	Revision    string
	Diagnostics config.Diagnostics
	Err         error
}

func (e *RejectedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("revision %s rejected: %v", e.Revision, e.Err)
	}
	return fmt.Sprintf("revision %s rejected: %v", e.Revision, e.Diagnostics.Err())
}

// LoadAndBuild validates and builds a repository directory without activating it.
func (r *Runtime) LoadAndBuild(dir string) (*config.Repo, map[string]*builtPlugin, config.Diagnostics, error) {
	var resolver config.PluginResolver
	if r.opts.Resolver != nil {
		resolver = r.opts.Resolver(dir)
	}
	repo, ds := config.Load(dir, config.LoadOptions{EngineVersion: version.Version, Resolver: resolver})
	if ds.HasErrors() || repo == nil {
		return repo, nil, ds, ds.Err()
	}
	builds := map[string]*builtPlugin{}
	keys := make([]string, 0, len(repo.Plugins))
	for k := range repo.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lp := repo.Plugins[k]
		bp, err := buildPlugin(lp)
		if err != nil {
			rel, _ := filepath.Rel(repo.Root, lp.Dir)
			ds = append(ds, config.Diagnostic{Severity: config.SeverityError, File: filepath.ToSlash(rel), Message: err.Error()})
			continue
		}
		builds[k] = bp
	}
	// Per (bot, server): command names must be unique across instances.
	type pair struct{ bot, server string }
	seen := map[pair]map[string]string{}
	for _, in := range repo.Instances {
		bp := builds[in.Plugin.Key]
		if bp == nil {
			continue
		}
		p := pair{in.Bot, in.Server}
		if seen[p] == nil {
			seen[p] = map[string]string{}
		}
		for _, c := range bp.def.Commands {
			if other, dup := seen[p][c.Name]; dup && other != in.Name {
				ds = append(ds, config.Diagnostic{Severity: config.SeverityError, File: repo.Bots[in.Bot].File,
					Message: fmt.Sprintf("command /%s is defined by both %q and %q on server %s", c.Name, other, in.Name, in.Server)})
			}
			seen[p][c.Name] = in.Name
		}
	}
	ds.Sort()
	return repo, builds, ds, ds.Err()
}

func buildPlugin(lp *config.LoadedPlugin) (*builtPlugin, error) {
	res, err := build.Bundle(lp.Dir, lp.Manifest.Spec.Entry)
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	def, err := plugin.Inspect(res.Code)
	if err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}
	m := lp.Manifest.Spec
	var problems []string
	for _, in := range def.RequiredIntents() {
		if !slices.Contains(m.Intents, in) {
			problems = append(problems, fmt.Sprintf("a handler needs the %s intent, which plugin.yaml does not declare", in))
		}
	}
	need := func(cond bool, capability, what string) {
		if cond && !slices.Contains(m.Capabilities, capability) {
			problems = append(problems, fmt.Sprintf("%s requires the %s capability in plugin.yaml", what, capability))
		}
	}
	need(len(def.Schedules) > 0 || len(def.Tasks) > 0, "scheduler", "schedules/tasks")
	need(len(def.Routes) > 0, "portal.routes", "routes")
	need(len(def.Subscriptions) > 0, "events.subscribe", "subscriptions")
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	sum := sha256.Sum256([]byte(res.Code))
	return &builtPlugin{code: res.Code, def: def, hash: hex.EncodeToString(sum[:8])}, nil
}

// Activate makes rev the active revision. force re-activates the same revision.
func (r *Runtime) Activate(ctx context.Context, rev source.Revision, force bool) error {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()

	r.mu.RLock()
	cur := r.active
	r.mu.RUnlock()
	if cur != nil && cur.rev.SHA == rev.SHA && !force {
		return nil
	}
	log := r.log.With("revision", rev.ID)
	log.Info("activating revision", "message", rev.Message)

	repo, builds, ds, err := r.LoadAndBuild(rev.Dir)
	if err != nil {
		details, _ := json.Marshal(ds.Errors())
		r.record(ctx, state.SyncRecord{Revision: rev.ID, Target: "revision", Status: "rejected", Message: firstLine(err.Error()), Details: details})
		r.setErr(err.Error())
		return &RejectedError{Revision: rev.ID, Diagnostics: ds}
	}
	for _, d := range ds {
		log.Warn(d.String())
	}

	next := &active{rev: rev, repo: repo, builds: builds, servers: map[string]*serverRT{}}

	// 1. bot sessions
	r.connectBots(ctx, repo)

	// 2. servers: reconcile with the first connected bot that lists them
	for _, key := range sortedKeys(repo.Servers) {
		srv := repo.Servers[key]
		rt := &serverRT{key: key, cfg: srv, cache: newServerCache(key, srv.Spec.DiscordID), health: HealthProgressing}
		next.servers[key] = rt
		host := r.reconcilingBot(repo, key)
		if host == nil {
			rt.health, rt.message = HealthMissing, "no connected bot is configured for this server"
			continue
		}
		rt.bot = host.key
		if _, err := host.session.GetServer(ctx, srv.Spec.DiscordID); err != nil {
			rt.health, rt.message = HealthMissing, fmt.Sprintf("bot %s cannot access the server: %v", host.key, err)
			continue
		}
		rt.rec = reconcile.New(reconcile.Options{
			Server: srv, RepoRoot: rev.Dir, Client: host.session, Resources: r.opts.Store.Resources(),
			KV: r.opts.Store.KV("reconcile:" + key), BotUserID: host.session.Self().ID,
			Logger: log.With("server", key), Reason: "codi " + rev.ID, Context: context.Background(),
			OnSync: func(p reconcile.Plan, res reconcile.Result, err error) { r.afterSelfHeal(rt, p, res, err) },
		})
		r.syncServer(ctx, rev, rt)
	}

	// 3. plugin instances
	for _, in := range repo.Instances {
		irt := &instanceRT{cfg: in, plugin: builds[in.Plugin.Key], health: HealthProgressing}
		next.instances = append(next.instances, irt)
		host := r.bot(in.Bot)
		srt := next.servers[in.Server]
		switch {
		case host == nil:
			irt.health, irt.message = HealthMissing, "bot is not connected"
			continue
		case srt == nil:
			irt.health, irt.message = HealthMissing, "server not found"
			continue
		}
		irt.serverID = srt.cfg.Spec.DiscordID
		if err := r.startInstance(ctx, host, srt, irt); err != nil {
			irt.health, irt.message = HealthDegraded, err.Error()
			log.Error("plugin instance failed to start", "instance", irt.id(), "err", err)
			continue
		}
		irt.health = HealthHealthy
	}

	// 4. swap routing, sync commands, profiles
	for _, host := range r.hosts() {
		host.install(ctx, next)
	}

	r.mu.Lock()
	old := r.active
	r.active = next
	r.lastErr = ""
	r.mu.Unlock()
	r.sched.reset(next)

	if old != nil {
		go stopActive(old, log)
	}
	r.record(ctx, state.SyncRecord{Revision: rev.ID, Target: "revision", Status: "succeeded", Message: rev.Message})
	log.Info("revision active", "servers", len(next.servers), "instances", len(next.instances))
	return nil
}

func stopActive(a *active, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range a.servers {
		if s.rec != nil {
			s.rec.Close()
		}
	}
	for _, i := range a.instances {
		if i.inst != nil {
			if err := i.inst.Stop(ctx); err != nil {
				log.Warn("stop instance", "instance", i.id(), "err", err)
			}
		}
	}
}

func (r *Runtime) syncServer(ctx context.Context, rev source.Revision, rt *serverRT) {
	plan, res, err := rt.rec.Sync(ctx)
	now := time.Now().UTC()
	rt.mu.Lock()
	rt.lastSync = &now
	switch {
	case err != nil:
		rt.health, rt.message = HealthDegraded, err.Error()
	case len(plan.Blocked) > 0:
		rt.health, rt.message = HealthHealthy, fmt.Sprintf("%d change(s) blocked", len(plan.Blocked))
	default:
		rt.health, rt.message = HealthHealthy, ""
	}
	rt.mu.Unlock()
	status, msg := "succeeded", fmt.Sprintf("%d change(s) applied", len(res.Applied))
	if err != nil {
		status, msg = "failed", err.Error()
	}
	details, _ := json.Marshal(map[string]any{"plan": plan, "applied": len(res.Applied), "failed": len(res.Failed)})
	r.record(ctx, state.SyncRecord{Revision: rev.ID, Target: "server:" + rt.key, Status: status, Message: firstLine(msg), Details: details})
	if host := r.bot(rt.bot); host != nil {
		if err := rt.cache.Refresh(ctx, host.session, r.opts.Store.Resources()); err != nil {
			r.log.Warn("refresh server cache", "server", rt.key, "err", err)
		}
	}
}

func (r *Runtime) afterSelfHeal(rt *serverRT, p reconcile.Plan, res reconcile.Result, err error) {
	if len(res.Applied) == 0 && err == nil {
		return
	}
	r.mu.RLock()
	rev := ""
	if r.active != nil {
		rev = r.active.rev.ID
	}
	r.mu.RUnlock()
	status := "succeeded"
	msg := fmt.Sprintf("self-heal: %d change(s) applied", len(res.Applied))
	if err != nil {
		status, msg = "failed", "self-heal: "+err.Error()
	}
	r.record(context.Background(), state.SyncRecord{Revision: rev, Target: "server:" + rt.key, Status: status, Message: msg})
	if host := r.bot(rt.bot); host != nil {
		_ = rt.cache.Refresh(context.Background(), host.session, r.opts.Store.Resources())
	}
}

func (r *Runtime) resyncServers(ctx context.Context) {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	a := r.Active()
	if a == nil {
		return
	}
	for _, key := range sortedKeys(a.servers) {
		if rt := a.servers[key]; rt.rec != nil {
			r.syncServer(ctx, a.rev, rt)
		}
	}
}

// SyncServer reconciles one server now (API "sync" button).
func (r *Runtime) SyncServer(ctx context.Context, key string) (reconcile.Plan, int, error) {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	a := r.Active()
	if a == nil {
		return reconcile.Plan{}, 0, errors.New("no active revision")
	}
	rt := a.servers[key]
	if rt == nil {
		return reconcile.Plan{}, 0, fmt.Errorf("unknown server %q", key)
	}
	if rt.rec == nil {
		return reconcile.Plan{}, 0, errors.New(rt.message)
	}
	plan, err := rt.rec.Plan(ctx)
	if err != nil {
		return plan, 0, err
	}
	r.syncServer(ctx, a.rev, rt)
	return plan, len(plan.Changes), nil
}

// PlanServer computes the plan of the active revision for one server.
func (r *Runtime) PlanServer(ctx context.Context, key string) (reconcile.Plan, error) {
	a := r.Active()
	if a == nil {
		return reconcile.Plan{}, errors.New("no active revision")
	}
	rt := a.servers[key]
	if rt == nil {
		return reconcile.Plan{}, fmt.Errorf("unknown server %q", key)
	}
	if rt.rec == nil {
		return reconcile.Plan{}, errors.New(rt.message)
	}
	return rt.rec.Plan(ctx)
}

// PlanDir validates a repository directory and plans every server against live
// Discord, without applying (codi plan --remote).
func (r *Runtime) PlanDir(ctx context.Context, dir string) (config.Diagnostics, []reconcile.Plan, error) {
	repo, _, ds, err := r.LoadAndBuild(dir)
	if err != nil {
		return ds, nil, nil
	}
	var plans []reconcile.Plan
	for _, key := range sortedKeys(repo.Servers) {
		srv := repo.Servers[key]
		host := r.reconcilingBot(repo, key)
		if host == nil {
			plans = append(plans, reconcile.Plan{Server: key, Blocked: []reconcile.Blocked{{Action: "sync", Kind: "server", Key: key, Reason: "no connected bot for this server"}}})
			continue
		}
		rec := reconcile.New(reconcile.Options{Server: srv, RepoRoot: dir, Client: host.session,
			Resources: readOnlyResources{r.opts.Store.Resources()}, KV: r.opts.Store.KV("reconcile:" + key),
			BotUserID: host.session.Self().ID, Logger: r.log})
		p, err := rec.Plan(ctx)
		rec.Close()
		if err != nil {
			return ds, plans, err
		}
		plans = append(plans, p)
	}
	return ds, plans, nil
}

// readOnlyResources prevents plan-only reconcilers from mutating the resource map.
type readOnlyResources struct{ state.ResourceMap }

func (readOnlyResources) Put(context.Context, state.Resource) error { return nil }
func (readOnlyResources) Delete(context.Context, string, state.ResourceKind, string) error {
	return nil
}

func (r *Runtime) shutdown() {
	r.mu.Lock()
	a := r.active
	r.active = nil
	hosts := r.bots
	r.bots = map[string]*botHost{}
	r.mu.Unlock()
	if a != nil {
		stopActive(a, r.log)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, h := range hosts {
		_ = h.session.Close(ctx)
	}
}

// ───────────────────────────────────────────────────────────── accessors

// Active returns the active revision (nil before the first successful activation).
func (r *Runtime) Active() *active {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// Repo returns the active repository configuration.
func (r *Runtime) Repo() *config.Repo {
	if a := r.Active(); a != nil {
		return a.repo
	}
	return nil
}

// Revision returns the active revision.
func (r *Runtime) Revision() (source.Revision, bool) {
	if a := r.Active(); a != nil {
		return a.rev, true
	}
	return source.Revision{}, false
}

func (r *Runtime) Store() state.Store   { return r.opts.Store }
func (r *Runtime) Signer() *auth.Signer { return r.opts.Signer }
func (r *Runtime) Mode() string         { return r.opts.Mode }

// Client returns a Discord client able to act on a server (its reconciling bot, or any
// bot that lists it).
func (r *Runtime) Client(serverKey string) (discord.Client, bool) {
	a := r.Active()
	if a == nil {
		return nil, false
	}
	if rt := a.servers[serverKey]; rt != nil && rt.bot != "" {
		if h := r.bot(rt.bot); h != nil {
			return h.session, true
		}
	}
	return nil, false
}

// ServerResources returns the cached live roles/channels and the key mapping helpers.
func (r *Runtime) ServerResources(serverKey string) (roles []discord.Role, channels []discord.Channel, keyOf func(id string) (state.ResourceKind, string, bool), ok bool) {
	a := r.Active()
	if a == nil || a.servers[serverKey] == nil {
		return nil, nil, nil, false
	}
	c := a.servers[serverKey].cache
	roles, channels = c.Snapshot()
	return roles, channels, c.Key, true
}

// Drift returns the drift entries of a server.
func (r *Runtime) Drift(ctx context.Context, serverKey string) ([]DriftEntry, error) {
	a := r.Active()
	if a == nil || a.servers[serverKey] == nil || a.servers[serverKey].rec == nil {
		return nil, nil
	}
	ds, err := a.servers[serverKey].rec.Drift(ctx)
	out := make([]DriftEntry, len(ds))
	for i, d := range ds {
		out[i] = DriftEntry{Kind: d.Kind, Key: d.Key, Field: d.Field, Expected: d.Expected, Actual: d.Actual}
	}
	return out, err
}

// InstanceInfo describes a running plugin instance for the API.
type InstanceInfo struct {
	Config     config.Instance
	Definition *plugin.Definition
	Health     string
	Message    string
}

// Instances lists instances of the active revision (all servers when server == "").
func (r *Runtime) Instances(server string) []InstanceInfo {
	a := r.Active()
	if a == nil {
		return nil
	}
	var out []InstanceInfo
	for _, i := range a.instances {
		if server != "" && i.cfg.Server != server {
			continue
		}
		var def *plugin.Definition
		if i.plugin != nil {
			def = i.plugin.def
		}
		i.mu.Lock()
		out = append(out, InstanceInfo{Config: i.cfg, Definition: def, Health: i.health, Message: i.message})
		i.mu.Unlock()
	}
	return out
}

// HandleRoute runs a plugin portal route.
func (r *Runtime) HandleRoute(ctx context.Context, server, bot, instance, method, path string, req plugin.RouteRequest) (plugin.RouteResponse, plugin.RouteDef, error) {
	a := r.Active()
	if a == nil {
		return plugin.RouteResponse{}, plugin.RouteDef{}, ErrNotFound
	}
	for _, i := range a.instances {
		if i.cfg.Server == server && i.cfg.Bot == bot && i.cfg.Name == instance && i.inst != nil {
			route, _, ok := i.plugin.def.MatchRoute(method, path)
			if !ok {
				return plugin.RouteResponse{}, plugin.RouteDef{}, ErrNotFound
			}
			resp, err := i.inst.HandleRoute(ctx, method, path, req)
			i.setHealth(err)
			return resp, route, err
		}
	}
	return plugin.RouteResponse{}, plugin.RouteDef{}, ErrNotFound
}

// MatchRoute finds the route definition (for access checks before running it).
func (r *Runtime) MatchRoute(server, bot, instance, method, path string) (plugin.RouteDef, bool) {
	a := r.Active()
	if a == nil {
		return plugin.RouteDef{}, false
	}
	for _, i := range a.instances {
		if i.cfg.Server == server && i.cfg.Bot == bot && i.cfg.Name == instance && i.plugin != nil {
			route, _, ok := i.plugin.def.MatchRoute(method, path)
			return route, ok
		}
	}
	return plugin.RouteDef{}, false
}

var ErrNotFound = errors.New("not found")

// Status reports the runtime state (spec/api.md §3).
func (r *Runtime) Status() Status {
	r.mu.RLock()
	a, lastErr := r.active, r.lastErr
	hosts := make(map[string]*botHost, len(r.bots))
	for k, v := range r.bots {
		hosts[k] = v
	}
	r.mu.RUnlock()
	st := Status{Version: version.Version, Mode: r.opts.Mode, Simulator: r.opts.Simulator, LastError: lastErr, Servers: []ServerStatus{}, Bots: []BotStatus{}}
	if a == nil {
		return st
	}
	rev := a.rev
	st.Revision = &rev
	for _, key := range sortedKeys(a.servers) {
		s := a.servers[key]
		s.mu.Lock()
		ss := ServerStatus{Key: key, DiscordID: s.cfg.Spec.DiscordID, Health: s.health, Message: s.message, LastSync: s.lastSync, Bot: s.bot, Drift: []DriftEntry{}}
		s.mu.Unlock()
		ss.Name = s.cache.Server().Name
		if ss.Name == "" && s.cfg.Spec.Profile != nil {
			ss.Name = s.cfg.Spec.Profile.Name
		}
		if ss.Name == "" {
			ss.Name = key
		}
		ss.Sync = SyncUnknown
		if s.rec != nil {
			ss.Sync = SyncSynced
			if s.rec.DriftPending() {
				ss.Sync = SyncOutOfSync
			}
		}
		st.Servers = append(st.Servers, ss)
	}
	for _, key := range sortedKeys(a.repo.Bots) {
		b := a.repo.Bots[key]
		bs := BotStatus{Key: key, Servers: b.Spec.Servers, Plugins: []PluginStatus{}}
		if h := hosts[key]; h != nil {
			bs.Username, bs.UserID = h.session.Self().Username, h.session.Self().ID
			bs.Health = HealthHealthy
		} else {
			bs.Health, bs.Message = HealthMissing, r.botError(key)
		}
		for _, i := range a.instances {
			if i.cfg.Bot != key {
				continue
			}
			i.mu.Lock()
			bs.Plugins = append(bs.Plugins, PluginStatus{Instance: i.cfg.Name, Plugin: i.cfg.Plugin.Manifest.Metadata.Name,
				Version: i.cfg.Plugin.Manifest.Metadata.Version, Server: i.cfg.Server, Health: i.health, Message: i.message})
			if i.health == HealthDegraded && bs.Health == HealthHealthy {
				bs.Health, bs.Message = HealthDegraded, "a plugin instance is degraded"
			}
			i.mu.Unlock()
		}
		st.Bots = append(st.Bots, bs)
	}
	return st
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
