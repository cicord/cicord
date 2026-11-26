// Package runtime is the Cicord controller (https://cicord.dev/docs/project/architecture/–5): it watches a source,
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
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cicord/cicord/internal/analytics"
	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/metrics"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/version"
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
	// ResyncInterval re-plans every server periodically (0 = 5m; negative disables): the
	// plan is applied only for servers with syncPolicy.selfHeal (see shouldApply).
	ResyncInterval time.Duration
	PluginLimits   plugin.Limits
	// AllowPrivateNetworks relaxes http.fetch egress checks (dev only).
	AllowPrivateNetworks bool
	// PluginHTTPClient overrides the HTTP client of http.fetch (tests: route allow-listed
	// hosts to a local server). The allow-list checks still apply; nil uses the egress-safe
	// default client.
	PluginHTTPClient *http.Client
	// CommitStatus reports the evaluation of git revisions (GitHub commit statuses):
	// CommitPending when a revision starts applying, then CommitSuccess (applied to every
	// server) or CommitFailure (rejected, or a server failed). It is called synchronously
	// and in order; it must bound its own duration and never fail the sync. Nil disables it.
	CommitStatus func(ctx context.Context, sha, state, description string)
	// Metrics receives the runtime metrics (nil: a private registry, never exposed).
	Metrics *metrics.Registry
	// Analytics stores the server activity analytics (nil: analytics are off). The
	// collecting bot of each server with analytics enabled gets the non-privileged
	// intents the collector needs (analytics.CollectorIntents).
	Analytics state.Analytics
	// OnHandler receives every plugin handler result after the metrics, e.g. for alerts;
	// it must not block.
	OnHandler func(in config.Instance, res plugin.HandlerResult)
	Logger    *slog.Logger
}

// Commit status states (Options.CommitStatus).
const (
	CommitPending = "pending"
	CommitSuccess = "success"
	CommitFailure = "failure"
)

// Runtime is safe for concurrent use.
type Runtime struct {
	auditSlots chan struct{}
	opts       Options
	log        *slog.Logger

	syncMu sync.Mutex // serializes revision activation and server syncs

	mu      sync.RWMutex
	active  *active
	lastErr string
	bots    map[string]*botHost
	botErrs sync.Map // bot key → last connection error
	// appIntents: bot key → privileged intents enabled for its application (live
	// application flags read when the bot connects).
	appIntents sync.Map

	wake  chan struct{}
	sched *scheduler
	diag  diagnostics

	restart restartPolicy
	metrics *runtimeMetrics

	collector     *analytics.Collector // nil when Options.Analytics is nil
	analyticsDone <-chan struct{}      // closed after the collector's last flush

	changes changeHub // GET /api/v1/events

	overlayMu sync.Mutex
	pauseMu   sync.RWMutex
	pauses    pauseState // paused servers (pause.go)

	// commit is the last commit status posted for the active revision (syncMu).
	commit struct{ sha, state string }
	// commitFailing mirrors commit.state == CommitFailure for notify, which runs without
	// syncMu; rechecking is true while a recheck started by a change is pending.
	commitFailing, rechecking atomic.Bool
}

// active is an activated revision.
type active struct {
	rev         source.Revision
	activatedAt time.Time
	repo        *config.Repo
	builds      map[string]*builtPlugin // by LoadedPlugin.Key
	servers     map[string]*serverRT    // by server key
	instances   []*instanceRT
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

	mu         sync.Mutex
	health     string
	message    string
	lastSync   *time.Time
	lastSyncOK bool // the last full sync succeeded
	applied    bool // a sync of this revision succeeded (the commit status recheck)
}

type instanceRT struct {
	cfg      config.Instance
	serverID string
	plugin   *builtPlugin
	// current is the running instance; the supervisor replaces it after a crash.
	current atomic.Pointer[plugin.Instance]

	swapMu  sync.Mutex
	retired bool          // the revision was replaced: no more restarts
	quit    chan struct{} // closed when retired

	mu       sync.Mutex
	health   string
	message  string
	failures int
}

func (i *instanceRT) id() string { return i.cfg.ID() }

// inst is the running plugin instance (nil when it never started).
func (i *instanceRT) inst() *plugin.Instance { return i.current.Load() }

// setHealth records the outcome of a handler call: three failures in a row make the
// instance Degraded, a success makes it Healthy again. It reports whether the health
// changed.
func (i *instanceRT) setHealth(err error) bool {
	if errors.Is(err, plugin.ErrCrashed) {
		return false // the supervisor reports crashes and restarts
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	before := i.health
	if err == nil {
		i.failures = 0
		i.health, i.message = HealthHealthy, ""
		return before != i.health
	}
	i.failures++
	i.message = err.Error()
	if i.failures >= 3 {
		i.health = HealthDegraded
	}
	return before != i.health
}

// handlerDone is setHealth after a handler call, telling the admin console when the
// instance's health changed.
func (r *Runtime) handlerDone(i *instanceRT, err error) {
	if i.setHealth(err) {
		r.notify(ChangePlugins, i.cfg.Server)
		r.notify(ChangeStatus, "")
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
	if opts.Metrics == nil {
		opts.Metrics = metrics.NewRegistry()
	}
	r := &Runtime{opts: opts, auditSlots: make(chan struct{}, 8), log: opts.Logger, bots: map[string]*botHost{}, wake: make(chan struct{}, 1),
		restart: restartPolicy{min: restartMin, max: restartMax, stable: restartStable}}
	r.sched = newScheduler(r)
	r.metrics = newRuntimeMetrics(opts.Metrics, r)
	if opts.Analytics != nil {
		var kv state.KV
		if opts.Store != nil {
			kv = opts.Store.KV(analyticsKVScope)
		}
		r.collector = analytics.NewCollector(analytics.CollectorOptions{Store: opts.Analytics, KV: kv, Logger: opts.Logger,
			OnWrite: func(server string) { r.notify(ChangeAnalytics, server) }})
	}
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
	r.analyticsDone = r.runAnalytics(ctx)
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
		r.metrics.fetchErrors.Inc()
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
	changed := r.lastErr != msg
	r.lastErr = msg
	r.mu.Unlock()
	if changed {
		r.notify(ChangeStatus, "")
	}
}

func (r *Runtime) record(ctx context.Context, rec state.SyncRecord) {
	if _, err := r.opts.Store.Syncs().Record(ctx, rec); err != nil {
		r.log.Warn("record sync", "err", err)
	}
	r.notifySync(rec.Target)
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
	repo, ds := config.Load(dir, config.LoadOptions{EngineVersion: version.Version, Resolver: resolver, PrivilegedIntents: r.enabledIntents})
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
	if err := def.ApplyLocales(lp.Locales); err != nil {
		return nil, err
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
	need(def.HasEvent("auditLogEntry"), "discord.auditlog.read", "auditLogEntry")
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
	r.commitStatus(ctx, rev, CommitPending, "Validating and applying to Discord")
	if err := r.loadPauses(ctx); err != nil {
		// Without the pauses, a paused server would be applied: activate nothing.
		r.setErr(err.Error())
		r.commitStatus(ctx, rev, CommitFailure, "Not applied: "+firstLine(err.Error()))
		return err
	}

	repo, builds, ds, err := r.LoadAndBuild(rev.Dir)
	if err != nil {
		details, _ := json.Marshal(ds.Errors())
		r.record(ctx, state.SyncRecord{Revision: rev.ID, Target: "revision", Status: "rejected", Message: firstLine(err.Error()), Details: details})
		r.setErr(err.Error())
		r.commitStatus(ctx, rev, CommitFailure, "Rejected: "+firstLine(err.Error()))
		r.metrics.activations.Inc("rejected")
		return &RejectedError{Revision: rev.ID, Diagnostics: ds}
	}
	next := &active{rev: rev, activatedAt: time.Now(), repo: repo, builds: builds, servers: map[string]*serverRT{}}

	// 1. bot sessions
	r.connectBots(ctx, repo)
	for _, d := range ds {
		// Privileged intents are checked against the live application flags when the
		// bots connect (connectBots logs an error when one is missing).
		if d.Code == config.CodePrivilegedIntent {
			continue
		}
		log.Warn(d.String())
	}

	// The old revision's reconcilers stop healing now (and a heal in flight finishes): the
	// syncs below change Discord, and the gateway events of those changes would make them
	// revert the servers to the old desired state right after the swap.
	if cur != nil {
		for _, s := range cur.servers {
			if s.rec != nil {
				s.rec.Close()
			}
		}
	}

	// 2. servers: reconcile with the first connected bot that lists them
	var serverFailures []string // "Server x: reason", for the commit status
	for _, key := range sortedKeys(repo.Servers) {
		srv := repo.Servers[key]
		rt := &serverRT{key: key, cfg: srv, cache: newServerCache(key, srv.Spec.DiscordID), health: HealthProgressing}
		next.servers[key] = rt
		host := r.reconcilingBot(repo, key)
		if host == nil {
			rt.health, rt.message = HealthMissing, "no connected bot is configured for this server"
			serverFailures = append(serverFailures, "Server "+key+": "+rt.message)
			continue
		}
		rt.bot = host.key
		if _, err := host.session.GetServer(ctx, srv.Spec.DiscordID); err != nil {
			rt.health, rt.message = HealthMissing, fmt.Sprintf("bot %s cannot access the server: %v", host.key, err)
			serverFailures = append(serverFailures, "Server "+key+": "+rt.message)
			continue
		}
		rt.rec = reconcile.New(reconcile.Options{
			Overlays:   func(ctx context.Context) (reconcile.OverlayState, error) { return r.overlayState(ctx, key) },
			Operations: r.operationScope(discord.Operation{ServerID: srv.Spec.DiscordID, ActorID: host.session.Self().ID, Bot: host.key, Revision: rev.ID}),
			Server:     srv, RepoRoot: rev.Dir, Client: host.session, Resources: r.opts.Store.Resources(),
			KV: r.opts.Store.KV("reconcile:" + key), BotUserID: host.session.Self().ID,
			Logger: log, Reason: "cicord " + rev.ID, Context: context.Background(),
			OnSync: func(p reconcile.Plan, res reconcile.Result, err error) { r.afterSelfHeal(rt, p, res, err) },
			OnPlan: func(p reconcile.Plan, err error) { r.planned(rt, p, err) },
			Paused: func() bool { return r.isPaused(key) },
		})
		if _, _, applied, err := r.reconcileServer(ctx, rev, rt, triggerRevision); err != nil {
			log.Warn("server sync failed", "server", key, "err", err)
			serverFailures = append(serverFailures, "Server "+key+": "+firstLine(err.Error()))
		} else if p, paused := r.Paused(key); paused && !applied {
			serverFailures = append(serverFailures, fmt.Sprintf("Server %s paused by %s: not applied", key, p.Who()))
		}
	}

	// 3. plugin instances
	var pluginFailures []string // for the commit status
	for _, in := range repo.Instances {
		irt := &instanceRT{cfg: in, plugin: builds[in.Plugin.Key], health: HealthProgressing, quit: make(chan struct{})}
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
		inst, err := r.startInstance(ctx, host, srt, irt)
		if err != nil {
			irt.health, irt.message = HealthDegraded, err.Error()
			log.Error("plugin instance failed to start", "instance", irt.id(), "err", err)
			pluginFailures = append(pluginFailures, fmt.Sprintf("Plugin %s on %s (bot %s) failed to start: %s", in.Name, in.Server, in.Bot, firstLine(err.Error())))
			continue
		}
		irt.current.Store(inst)
		irt.health = HealthHealthy
		go r.supervise(host, srt, irt) //nolint:gosec // outlives the activation request; ends when the revision is retired (irt.quit)
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
	r.configureAnalytics(next)

	if old != nil {
		// The old revision is stopped in the background and must finish even when the
		// activation context ends.
		go stopActive(context.WithoutCancel(ctx), old, log)
	}
	r.record(ctx, state.SyncRecord{Revision: rev.ID, Target: "revision", Status: "succeeded", Message: rev.Message})
	r.metrics.activations.Inc("activated")
	log.Info("revision active", "servers", len(next.servers), "instances", len(next.instances))
	failures := slices.Concat(serverFailures, r.botFailures(repo), pluginFailures)
	st, desc := commitVerdict(failures, len(next.servers))
	r.commitStatus(ctx, rev, st, desc)
	return nil
}

// botFailures lists the bots of repo that are not connected, for the commit status.
func (r *Runtime) botFailures(repo *config.Repo) []string {
	var out []string
	for _, key := range sortedKeys(repo.Bots) {
		if r.bot(key) == nil {
			out = append(out, fmt.Sprintf("Bot %s is not connected: %s", key, firstLine(r.botError(key))))
		}
	}
	return out
}

// commitVerdict is the commit status of a revision from its problems (servers not
// applied, bots not connected, plugin instances that failed to start).
func commitVerdict(failures []string, servers int) (state, description string) {
	switch len(failures) {
	case 0:
		return CommitSuccess, fmt.Sprintf("Applied to %d server(s)", servers)
	case 1:
		return CommitFailure, failures[0]
	default:
		return CommitFailure, fmt.Sprintf("%d problems; %s", len(failures), failures[0])
	}
}

// recheckCommitStatus posts success for the active revision once a problem reported when
// it was activated is gone (a paused server resumed and synced, a server fixed and
// synced). It never turns a success into a failure: later problems are runtime health,
// not the evaluation of the commit. Caller holds syncMu.
func (r *Runtime) recheckCommitStatus(ctx context.Context) {
	a := r.Active()
	if a == nil || r.commit.sha != a.rev.SHA || r.commit.state != CommitFailure {
		return
	}
	var failures []string
	for _, key := range sortedKeys(a.servers) {
		s := a.servers[key]
		s.mu.Lock()
		ok := s.applied && s.health != HealthDegraded && s.health != HealthMissing
		s.mu.Unlock()
		if _, paused := r.Paused(key); paused || !ok {
			failures = append(failures, key)
		}
	}
	failures = append(failures, r.botFailures(a.repo)...)
	for _, i := range a.instances {
		i.mu.Lock()
		if i.health == HealthDegraded || i.health == HealthMissing {
			failures = append(failures, i.id())
		}
		i.mu.Unlock()
	}
	if len(failures) == 0 {
		r.commitStatus(ctx, a.rev, CommitSuccess, fmt.Sprintf("Applied to %d server(s)", len(a.servers)))
	}
}

// commitStatus reports the state of a git revision (Options.CommitStatus).
func (r *Runtime) commitStatus(ctx context.Context, rev source.Revision, st, description string) {
	if r.opts.CommitStatus == nil || rev.Source != "git" || !isCommitSHA(rev.SHA) {
		return
	}
	r.commit.sha, r.commit.state = rev.SHA, st
	r.commitFailing.Store(st == CommitFailure)
	r.opts.CommitStatus(ctx, rev.SHA, st, truncateRunes(description, maxCommitDescription))
}

// recheckCommitSoon rechecks the commit status of the active revision after a health
// change, while it is a failure: a plugin that was still restarting, or a bot reconnecting,
// when its server was synced turns it into a success once healthy, without waiting for the
// next sync. One recheck at a time; it waits for syncMu in its own goroutine.
func (r *Runtime) recheckCommitSoon() {
	if !r.commitFailing.Load() || !r.rechecking.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer r.rechecking.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.syncMu.Lock()
		defer r.syncMu.Unlock()
		r.recheckCommitStatus(ctx)
	}()
}

// maxCommitDescription is GitHub's limit for a commit status description.
const maxCommitDescription = 140

// truncateRunes shortens s to at most n runes, ending with "…" when cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// isCommitSHA reports whether s is a full SHA-1 or SHA-256 commit hash.
func isCommitSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func stopActive(ctx context.Context, a *active, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, s := range a.servers {
		if s.rec != nil {
			s.rec.Close()
		}
	}
	for _, i := range a.instances {
		if inst := i.retire(); inst != nil {
			if err := inst.Stop(ctx); err != nil {
				log.Warn("stop instance", "instance", i.id(), "err", err)
			}
		}
	}
}

// syncTrigger is what starts a reconciliation of a server.
type syncTrigger int

const (
	triggerRevision syncTrigger = iota // a new revision is activated
	triggerManual                      // an explicit sync (API "Sync now")
	triggerResync                      // the periodic resync (Options.ResyncInterval)
)

// shouldApply decides whether a reconciliation started by trigger applies the plan or
// only re-plans (refreshing the drift and the status). Nothing is applied to a paused
// server. Otherwise a new revision and an explicit sync apply; the periodic resync
// applies only with syncPolicy.selfHeal, which reverts drift. Without it, drift is
// reported and the server stays OutOfSync until the next revision or an explicit sync.
func shouldApply(srv *config.Server, t syncTrigger, paused bool) bool {
	if paused {
		return false
	}
	if t == triggerResync {
		return srv.Spec.SyncPolicy.SelfHeal()
	}
	return true
}

// planStatus is the health and message of a server after a plan. pending is the number
// of its changes that were not applied (a plan without a sync); paused tells that
// enforcement is paused on the server.
func planStatus(srv *config.Server, plan reconcile.Plan, pending int, paused bool, err error) (health, message string) {
	if err != nil {
		return HealthDegraded, err.Error()
	}
	var parts []string
	if pending > 0 {
		part := fmt.Sprintf("%d change(s) to apply", pending)
		switch {
		case paused:
			part += " (enforcement is paused: nothing is applied until it is resumed)"
		case !srv.Spec.SyncPolicy.SelfHeal():
			part += " (selfHeal is off: the next revision or Sync now applies them)"
		}
		parts = append(parts, part)
	}
	boost := 0
	for _, b := range plan.Blocked {
		if reconcile.WaitsForBoost(b) {
			boost++
		}
	}
	if n := len(plan.Blocked) - boost; n > 0 {
		parts = append(parts, fmt.Sprintf("%d change(s) blocked (%s)", n, blockedSummary(plan.Blocked)))
	}
	if boost > 0 {
		parts = append(parts, fmt.Sprintf("%d wait for a higher boost level", boost))
		if len(parts) == 1 {
			parts[0] = fmt.Sprintf("%d change(s) wait for a higher boost level", boost)
		}
	}
	return HealthHealthy, strings.Join(parts, ", ")
}

// blockedSummary names the first blocked operation that does not wait for a boost level
// and why ("delete channel old: deletionProtection is on"), with the count of the others.
func blockedSummary(blocked []reconcile.Blocked) string {
	var first *reconcile.Blocked
	n := 0
	for i := range blocked {
		if reconcile.WaitsForBoost(blocked[i]) {
			continue
		}
		if first == nil {
			first = &blocked[i]
		}
		n++
	}
	if first == nil {
		return ""
	}
	out := fmt.Sprintf("%s %s %s: %s", first.Action, first.Kind, first.Key, firstLine(first.Reason))
	if n > 1 {
		out += fmt.Sprintf(" and %d more", n-1)
	}
	return out
}

// setStatus sets the health and message of a server, and tells the admin console when
// they changed.
func (r *Runtime) setStatus(rt *serverRT, health, message string) {
	rt.mu.Lock()
	changed := rt.health != health || rt.message != message
	rt.health, rt.message = health, message
	rt.mu.Unlock()
	if changed {
		r.notify(ChangeStatus, "")
	}
}

// planned is Options.OnPlan of the reconcilers: every plan refreshes the server's status,
// so that its message matches its drift (a plan that is not followed by an apply leaves
// its changes pending).
// A server whose last sync failed stays Degraded, with that error, while the plan still
// has changes to apply: a plan does not retry them.
func (r *Runtime) planned(rt *serverRT, plan reconcile.Plan, err error) {
	rt.mu.Lock()
	failed := rt.lastSync != nil && !rt.lastSyncOK && rt.health == HealthDegraded
	rt.mu.Unlock()
	if failed && err == nil && len(plan.Changes) > 0 {
		return
	}
	h, m := planStatus(rt.cfg, plan, len(plan.Changes), r.isPaused(rt.key), err)
	r.setStatus(rt, h, m)
}

// reconcileServer reconciles a server for trigger: a sync (plan and apply), or only a
// plan when shouldApply says so (the drift and the status are refreshed, nothing is
// applied, nothing is recorded in the sync history, except a new revision that is not
// applied to a paused server). applied tells which.
func (r *Runtime) reconcileServer(ctx context.Context, rev source.Revision, rt *serverRT, t syncTrigger) (plan reconcile.Plan, res reconcile.Result, applied bool, err error) {
	pause, paused := r.Paused(rt.key)
	if !shouldApply(rt.cfg, t, paused) {
		plan, err = rt.rec.Plan(ctx) // OnPlan updates the status
		if paused && t == triggerRevision {
			msg := fmt.Sprintf("not applied: enforcement is paused by %s", pause.Who())
			if n := len(plan.Changes); n > 0 {
				msg += fmt.Sprintf(" (%d change(s) pending)", n)
			}
			r.record(ctx, state.SyncRecord{Revision: rev.ID, Target: "server:" + rt.key, Status: SyncRecordSkipped, Message: msg})
		}
		return plan, res, false, err
	}
	plan, res, err = r.syncServer(ctx, rev, rt)
	if errors.Is(err, reconcile.ErrPaused) {
		// paused while it was planning: nothing was applied
		p, _ := r.Paused(rt.key)
		return plan, res, false, &PausedError{Server: rt.key, Info: p}
	}
	return plan, res, true, err
}

func (r *Runtime) syncServer(ctx context.Context, rev source.Revision, rt *serverRT) (reconcile.Plan, reconcile.Result, error) {
	plan, res, err := rt.rec.Sync(ctx)
	if errors.Is(err, reconcile.ErrPaused) {
		// paused between the trigger and the apply: OnPlan refreshed the status, nothing
		// was applied or recorded
		return plan, res, err
	}
	now := time.Now().UTC()
	health, message := planStatus(rt.cfg, plan, 0, false, err)
	rt.mu.Lock()
	rt.lastSync = &now
	rt.lastSyncOK = err == nil
	rt.applied = rt.applied || err == nil
	changed := rt.health != health || rt.message != message
	rt.health, rt.message = health, message
	rt.mu.Unlock()
	if changed {
		r.notify(ChangeStatus, "")
	}
	status, msg := "succeeded", fmt.Sprintf("%d change(s) applied", len(res.Applied))
	if err != nil {
		status, msg = "failed", err.Error()
	}
	r.metrics.syncs.Inc(rt.key, status)
	r.metrics.reconciled(rt.key, "sync", res)
	details, _ := json.Marshal(map[string]any{"plan": plan, "applied": len(res.Applied), "failed": len(res.Failed)})
	r.record(ctx, state.SyncRecord{Revision: rev.ID, Target: "server:" + rt.key, Status: status, Message: firstLine(msg), Details: details})
	if host := r.bot(rt.bot); host != nil {
		if err := rt.cache.Refresh(ctx, host.session, r.opts.Store.Resources()); err != nil {
			r.log.Warn("refresh server cache", "server", rt.key, "err", err)
		}
	}
	return plan, res, err
}

func (r *Runtime) afterSelfHeal(rt *serverRT, plan reconcile.Plan, res reconcile.Result, err error) {
	r.metrics.reconciled(rt.key, "self_heal", res)
	h, m := planStatus(rt.cfg, plan, 0, false, err)
	r.setStatus(rt, h, m)
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
		if err := rt.cache.Refresh(context.Background(), host.session, r.opts.Store.Resources()); err != nil {
			r.log.Warn("refresh server cache", "server", rt.key, "err", err)
		}
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
		rt := a.servers[key]
		if rt.rec == nil {
			continue
		}
		if _, _, applied, err := r.reconcileServer(ctx, a.rev, rt, triggerResync); err != nil {
			what := "sync"
			if !applied {
				what = "plan"
			}
			r.log.Warn("server "+what+" failed", "server", key, "err", err)
		}
	}
	r.recheckCommitStatus(ctx)
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
	if p, paused := r.Paused(key); paused {
		return reconcile.Plan{}, 0, &PausedError{Server: key, Info: p}
	}
	plan, err := rt.rec.Plan(ctx)
	if err != nil {
		return plan, 0, err
	}
	plan, res, _, err := r.reconcileServer(ctx, a.rev, rt, triggerManual)
	if err == nil {
		r.recheckCommitStatus(ctx)
	}
	return plan, len(res.Applied), err
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
// Discord, without applying (cicord plan --remote).
func (r *Runtime) PlanDir(ctx context.Context, dir string) (config.Diagnostics, []reconcile.Plan, error) {
	// An invalid repository is not a planning failure: its errors are returned as
	// diagnostics (LoadAndBuild's error is ds.Err()).
	repo, _, ds, _ := r.LoadAndBuild(dir)
	if ds.HasErrors() || repo == nil {
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
		rec := reconcile.New(reconcile.Options{Overlays: func(ctx context.Context) (reconcile.OverlayState, error) { return r.overlayState(ctx, key) }, Server: srv, RepoRoot: dir, Client: host.session,
			Resources: readOnlyResources{r.opts.Store.Resources()}, KV: r.opts.Store.KV("reconcile:" + key),
			BotUserID: host.session.Self().ID, Logger: r.log})
		p, err := rec.Plan(ctx)
		rec.Close()
		if pe := (*reconcile.PermissionsError)(nil); errors.As(err, &pe) {
			// the bot lacks permissions: shown on the plan, like the CLI does
			p.Blocked = append(p.Blocked, reconcile.Blocked{Action: "sync", Kind: reconcile.KindServer, Key: key, Reason: firstLine(err.Error())})
			plans = append(plans, p)
			continue
		}
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
		stopActive(context.Background(), a, r.log)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, h := range hosts {
		_ = h.session.Close(ctx)
	}
	if r.analyticsDone != nil {
		select { // Run's context is done: the collector flushes a last time
		case <-r.analyticsDone:
		case <-time.After(analyticsShutdownTimeout):
			r.log.Warn("analytics: last flush timed out")
		}
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

// instanceDeadCounts reads the persistent queue so a restart preserves alerts.
func (r *Runtime) instanceDeadCounts() map[string]int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	counts, err := r.opts.Store.Tasks().DeadCounts(ctx)
	if err != nil {
		r.log.Warn("read dead letter counts", "err", err)
	}
	return counts
}

func (r *Runtime) reportedHealth(i *instanceRT, dead int) (string, string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.health == HealthHealthy && i.cfg.Plugin != nil && i.cfg.Config["enabled"] != false {
		var optional []string
		for _, s := range i.cfg.Plugin.Manifest.Spec.Secrets {
			if _, _, ok := r.instanceSecret(i.cfg, s.Name); !ok {
				if s.Required {
					return HealthDegraded, "Missing required plugin secret " + s.Name
				}
				optional = append(optional, s.Name)
			}
		}
		if len(optional) > 0 && dead == 0 {
			return i.health, "Features requiring these optional secrets are disabled: " + strings.Join(optional, ", ")
		}
	}
	if dead > 0 && i.health == HealthHealthy {
		return HealthDegraded, fmt.Sprintf("%d failed scheduled task(s) need attention in Diagnostics", dead)
	}
	return i.health, i.message
}

// Instances lists instances of the active revision (all servers when server == "").
func (r *Runtime) Instances(server string) []InstanceInfo {
	a := r.Active()
	if a == nil {
		return nil
	}
	dead := r.instanceDeadCounts()
	var out []InstanceInfo
	for _, i := range a.instances {
		if server != "" && i.cfg.Server != server {
			continue
		}
		var def *plugin.Definition
		if i.plugin != nil {
			def = i.plugin.def
		}
		health, message := r.reportedHealth(i, dead[instanceScope(i.cfg)])
		out = append(out, InstanceInfo{Config: i.cfg, Definition: def, Health: health, Message: message})
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
		if i.cfg.Server == server && i.cfg.Bot == bot && i.cfg.Name == instance && i.inst() != nil {
			route, _, ok := i.plugin.def.MatchRoute(method, path)
			if !ok {
				return plugin.RouteResponse{}, plugin.RouteDef{}, ErrNotFound
			}
			resp, err := i.inst().HandleRoute(ctx, method, path, req)
			r.handlerDone(i, err)
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

// Status reports the runtime state (https://cicord.dev/docs/reference/http-api/servers/).
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
	dead := r.instanceDeadCounts()
	for _, key := range sortedKeys(a.servers) {
		s := a.servers[key]
		s.mu.Lock()
		ss := ServerStatus{Key: key, DiscordID: s.cfg.Spec.DiscordID, Health: s.health, Message: s.message, LastSync: s.lastSync, Bot: s.bot, Drift: []DriftEntry{}}
		s.mu.Unlock()
		if p, ok := r.Paused(key); ok {
			ss.Paused = &p
		}
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
		bs := BotStatus{Key: key, Servers: b.Spec.ServerKeys(), Plugins: []PluginStatus{}}
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
			health, message := r.reportedHealth(i, dead[instanceScope(i.cfg)])
			bs.Plugins = append(bs.Plugins, PluginStatus{Instance: i.cfg.Name, Plugin: i.cfg.Plugin.Manifest.Metadata.Name,
				Version: i.cfg.Plugin.Manifest.Metadata.Version, Server: i.cfg.Server, Health: health, Message: message})
			if health == HealthDegraded && bs.Health == HealthHealthy {
				bs.Health, bs.Message = HealthDegraded, fmt.Sprintf("plugin instance %s on %s is degraded", i.cfg.Name, i.cfg.Server)
			}
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

// ───────────────────────────────────────────────────────────── instance restarts

// Restart backoff for crashed plugin instances (memory limit, hard stop, engine trap): the
// delay doubles after each crash or failed start, and goes back to the minimum once an
// instance stayed up for restartStable.
var (
	restartMin    = time.Second
	restartMax    = 5 * time.Minute
	restartStable = 5 * time.Minute
)

type restartPolicy struct{ min, max, stable time.Duration }

// retire stops restarts for irt (its revision is replaced) and returns the instance to
// stop.
func (i *instanceRT) retire() *plugin.Instance {
	i.swapMu.Lock()
	defer i.swapMu.Unlock()
	if !i.retired {
		i.retired = true
		close(i.quit)
	}
	return i.inst()
}

// install makes inst the running instance unless irt was retired meanwhile.
func (i *instanceRT) install(inst *plugin.Instance) bool {
	i.swapMu.Lock()
	defer i.swapMu.Unlock()
	if i.retired {
		return false
	}
	i.current.Store(inst)
	return true
}

func (i *instanceRT) degraded(msg string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.health, i.message = HealthDegraded, msg
}

// supervise restarts irt's instance whenever it crashes, until the revision is retired.
// While it waits, the crashed instance stays installed: its calls fail at once with
// plugin.ErrCrashed (interactions get the generic error reply).
func (r *Runtime) supervise(host *botHost, srt *serverRT, irt *instanceRT) {
	p := r.restart
	delay := p.min
	upSince := time.Now()
	for {
		inst := irt.inst()
		select {
		case <-irt.quit:
			return
		case <-inst.Done():
		}
		cause := inst.Err()
		if !errors.Is(cause, plugin.ErrCrashed) {
			return // stopped
		}
		labels := instanceLabelValues(irt.cfg)
		r.metrics.crashes.Inc(labels...)
		_ = inst.Stop(context.Background())
		if time.Since(upSince) >= p.stable {
			delay = p.min
		}
		for {
			irt.degraded(fmt.Sprintf("%v (restarting in %s)", cause, delay))
			r.notify(ChangePlugins, srt.key)
			r.log.Error("plugin instance crashed, restarting", "instance", irt.id(), "err", cause, "in", delay)
			select {
			case <-irt.quit:
				return
			case <-time.After(delay):
			}
			delay = min(delay*2, p.max)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			next, err := r.startInstance(ctx, host, srt, irt)
			cancel()
			if err != nil {
				r.metrics.restarts.Inc(append(labels, "failed")...)
				cause = fmt.Errorf("restart failed: %w", err)
				continue
			}
			if !irt.install(next) {
				_ = next.Stop(context.Background())
				return
			}
			r.metrics.restarts.Inc(append(labels, "succeeded")...)
			break
		}
		upSince = time.Now()
		irt.setHealth(nil)
		r.notify(ChangePlugins, srt.key)
		r.log.Info("plugin instance restarted", "instance", irt.id())
	}
}
