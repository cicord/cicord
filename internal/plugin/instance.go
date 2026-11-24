package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/grafana/sobek"

	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/state"
)

// Env is everything an instance may touch. It is provided by the bot host.
type Env struct {
	BotKey        string
	InstanceName  string // instance key, unique within the bot (namespaces custom IDs)
	PluginName    string
	PluginVersion string
	Bot           discord.User
	ApplicationID string

	ServerKey     string
	ServerID      string
	ServerName    string
	ServerIconURL string

	// Config is the effective config (defaults applied, overrides merged). It must be
	// JSON-serializable; plugins see it deep-frozen.
	Config map[string]any
	// Grants are the capabilities granted in bot.yaml.
	Grants []string
	// NetworkAllow is plugin.yaml spec.network.allow.
	NetworkAllow []string
	// AllowPrivateNetworks disables the egress address checks and allows plain http
	// (tests and `cicord dev` only).
	AllowPrivateNetworks bool
	// MessageContent reports that the plugin declares the MessageContent intent in
	// plugin.yaml. Without it, message content and attachments are blanked in every
	// Message the plugin sees (events and channel.messages()), like Discord does for bots
	// without the intent, except for the bot's own messages, messages that mention the bot
	// and DMs. Gateway intents are unioned per bot, so this keeps the gating per plugin.
	MessageContent bool

	Discord  discord.Client
	Resolver Resolver // may be nil (nothing resolves)

	KV        state.KV          // already scoped to the instance; nil: kv calls fail
	Tasks     state.TaskQueue   // nil: ctx.schedule fails
	TaskScope string            // Task.Scope for tasks created by this instance
	Resources state.ResourceMap // records plugin-owned channels; nil: not recorded
	Portal    PortalLinker      // nil: ctx.portal.link fails
	// Publish sends a cross-plugin event. topic is namespaced: "<plugin>.<topic>".
	Publish func(topic string, payload json.RawMessage) error

	Logger     *slog.Logger // nil: discarded
	Limits     Limits
	HTTPClient *http.Client // optional override for http.fetch (tests); egress name checks still apply
}

// Resolver maps Cicord keys to Discord IDs for the instance's server and exposes the cached
// channels and roles. Implementations must be safe for concurrent use.
type Resolver interface {
	ID(kind state.ResourceKind, key string) (string, bool)
	Key(id string) (kind state.ResourceKind, key string, ok bool)
	Channel(id string) (discord.Channel, bool)
	Role(id string) (discord.Role, bool)
}

// PortalLinker signs portal URLs. ttl 0 means the linker's default.
type PortalLinker interface {
	Link(path, userID string, ttl time.Duration) (string, error)
}

// Limits bounds plugin execution. Zero fields take the defaults.
type Limits struct {
	// SyncBudget is the longest continuous synchronous JS execution (one turn of the event
	// loop) before the VM is interrupted and the handler aborted. Default 50ms.
	SyncBudget time.Duration
	// StartupBudget replaces SyncBudget while the module is evaluated. Default 2s.
	StartupBudget time.Duration
	// HandlerTimeout bounds each handler (wall clock, including awaits). Async work started
	// by a handler is cut when it elapses, even if the handler already returned. Default 10s.
	HandlerTimeout time.Duration
	// AutoDeferAfter: interactions not answered after this delay are deferred. Default 2s.
	AutoDeferAfter time.Duration
	// StopTimeout bounds onStop. Default 3s.
	StopTimeout time.Duration
	// FetchTimeout is the default http.fetch timeout (init.timeoutMs can raise it up to
	// HandlerTimeout). Default 10s.
	FetchTimeout time.Duration
	// MaxResponseBytes caps http.fetch response bodies. Default 5 MiB.
	MaxResponseBytes int64
}

func (l Limits) withDefaults() Limits {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&l.SyncBudget, 50*time.Millisecond)
	def(&l.StartupBudget, 2*time.Second)
	def(&l.HandlerTimeout, 10*time.Second)
	def(&l.AutoDeferAfter, 2*time.Second)
	def(&l.StopTimeout, 3*time.Second)
	def(&l.FetchTimeout, 10*time.Second)
	if l.MaxResponseBytes <= 0 {
		l.MaxResponseBytes = 5 << 20
	}
	return l
}

// RouteRequest is a portal request routed to a plugin route.
type RouteRequest struct {
	Query map[string]string
	// Body is the JSON request body (nil when absent).
	Body   json.RawMessage
	Viewer *RouteViewer // nil for anonymous viewers
}

// RouteViewer is the authenticated portal user.
type RouteViewer struct {
	User   discord.User
	Member *discord.Member
}

// RouteResponse is what a route handler returned.
type RouteResponse struct {
	Status  int
	Headers map[string]string
	Body    json.RawMessage // JSON; nil when the handler returned no body
}

// Runtime is the language-agnostic surface of a plugin instance consumed by the bot host
// (ARCHITECTURE.md §4.6). *Instance is the Sobek implementation.
type Runtime interface {
	Definition() *Definition
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	HandleEvent(ctx context.Context, ev discord.Event) error
	HandleInteraction(ctx context.Context, it discord.Interaction, kind, localID string) error
	HandleRoute(ctx context.Context, method, path string, req RouteRequest) (RouteResponse, error)
	RunSchedule(ctx context.Context, name string) error
	RunTask(ctx context.Context, name string, payload json.RawMessage) error
	Deliver(ctx context.Context, topic string, payload json.RawMessage) error
}

var _ Runtime = (*Instance)(nil)

const (
	stateNew = iota
	stateStarting
	stateStarted
	stateStopping
	stateStopped
)

// Instance is one plugin running for one (bot, server): a Sobek VM driven by a single
// event-loop goroutine. All methods are safe for concurrent use.
type Instance struct {
	env    Env
	def    *Definition
	code   string
	limits Limits
	logger *slog.Logger
	grants map[string]bool
	egress *egress

	ctx    context.Context // instance lifetime
	cancel context.CancelCauseFunc

	stateMu sync.Mutex
	state   int

	// event loop
	qmu      sync.Mutex
	queue    []func()
	closed   bool
	wake     chan struct{}
	loopDone chan struct{}

	// loop-only state
	vm         *vm
	current    *invocation
	bg         *invocation
	timers     map[int64]*jsTimer
	nextTimer  int64
	rejections map[*sobek.Promise]struct{}
	budget     budgetTimer

	nextInv atomic.Uint64

	imu          sync.Mutex
	interactions map[string]*interaction

	chMu           sync.Mutex
	serverChannels map[string]bool
	dmChannels     map[string]bool
}

var instanceNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// NewInstance creates an instance (VM ready, module not evaluated yet: call Start). def is
// the definition obtained from Inspect(code); when nil it is extracted during Start.
func NewInstance(code string, def *Definition, env Env) (*Instance, error) {
	if !instanceNameRe.MatchString(env.InstanceName) {
		return nil, fmt.Errorf("plugin: invalid instance name %q", env.InstanceName)
	}
	if env.Discord == nil {
		return nil, errors.New("plugin: Env.Discord is required")
	}
	if env.ServerID == "" {
		return nil, errors.New("plugin: Env.ServerID is required")
	}
	if env.Logger == nil {
		env.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	limits := env.Limits.withDefaults()
	i := &Instance{
		env:            env,
		def:            def,
		code:           code,
		limits:         limits,
		grants:         map[string]bool{},
		wake:           make(chan struct{}, 1),
		loopDone:       make(chan struct{}),
		timers:         map[int64]*jsTimer{},
		rejections:     map[*sobek.Promise]struct{}{},
		interactions:   map[string]*interaction{},
		serverChannels: map[string]bool{},
		dmChannels:     map[string]bool{},
	}
	for _, g := range env.Grants {
		i.grants[g] = true
	}
	i.logger = env.Logger.With("bot", env.BotKey, "plugin", env.PluginName, "instance", env.InstanceName, "server", env.ServerKey)
	i.egress = newEgress(env.NetworkAllow, env.AllowPrivateNetworks, env.HTTPClient, limits.MaxResponseBytes)
	i.ctx, i.cancel = context.WithCancelCause(context.Background())
	i.bg = &invocation{kind: "background", ctx: i.ctx, cancel: func(error) {}, done: make(chan invResult, 1)}

	v, err := newVM(i)
	if err != nil {
		i.cancel(ErrStopped)
		return nil, err
	}
	i.vm = v
	i.budget.rt = v.rt
	v.rt.SetAsyncContextTracker(tracker{i})
	v.rt.SetPromiseRejectionTracker(func(p *sobek.Promise, op sobek.PromiseRejectionOperation) {
		if op == sobek.PromiseRejectionReject {
			i.rejections[p] = struct{}{}
		} else {
			delete(i.rejections, p)
		}
	})
	go i.loop()
	return i, nil
}

// Definition returns the static definition (nil before Start when NewInstance got nil).
func (i *Instance) Definition() *Definition {
	i.stateMu.Lock()
	defer i.stateMu.Unlock()
	return i.def
}

// Start evaluates the module (bounded by Limits.StartupBudget), then runs onStart (bounded by
// HandlerTimeout). After an error, call Stop to release the instance.
func (i *Instance) Start(ctx context.Context) error {
	i.stateMu.Lock()
	if i.state != stateNew {
		i.stateMu.Unlock()
		return errors.New("plugin: instance already started")
	}
	i.state = stateStarting
	i.stateMu.Unlock()

	errc := make(chan error, 1)
	if !i.post(func() { errc <- i.loadModule() }) {
		return ErrStopped
	}
	var err error
	select {
	case err = <-errc:
	case <-ctx.Done():
		return ctx.Err()
	case <-i.ctx.Done():
		return context.Cause(i.ctx)
	}
	if err != nil {
		return err
	}
	i.stateMu.Lock()
	if i.state != stateStarting {
		i.stateMu.Unlock()
		return ErrStopped
	}
	i.state = stateStarted
	hasStart := i.def.HasOnStart
	i.stateMu.Unlock()
	if hasStart {
		if r := <-i.dispatch(ctx, "start", "", nil, nil); r.err != nil {
			return fmt.Errorf("plugin: onStart: %w", r.err)
		}
	}
	return nil
}

// loadModule runs on the loop.
func (i *Instance) loadModule() error {
	var defObj *sobek.Object
	err := i.enter(i.bg, i.limits.StartupBudget, func() error {
		d, err := i.vm.loadPlugin(i.code)
		defObj = d
		return err
	})
	if err != nil {
		if isInterrupt(err) {
			return fmt.Errorf("plugin: module evaluation exceeded %s: %w", i.limits.StartupBudget, ErrBudgetExceeded)
		}
		return err
	}
	if i.def == nil {
		d, err := i.vm.definition(defObj)
		if err != nil {
			return err
		}
		i.stateMu.Lock()
		i.def = d
		i.stateMu.Unlock()
	}
	ctxJSON, err := json.Marshal(i.contextDTO())
	if err != nil {
		return fmt.Errorf("plugin: config is not JSON-serializable: %w", err)
	}
	if _, err := i.vm.setup(sobek.Undefined(), defObj, i.vm.rt.ToValue(string(ctxJSON))); err != nil {
		return fmt.Errorf("plugin: setup: %w", i.vm.asScriptError(err))
	}
	return nil
}

// Stop runs onStop (bounded by Limits.StopTimeout and ctx), then releases the VM. Pending and
// later calls fail with ErrStopped. Stop is idempotent.
func (i *Instance) Stop(ctx context.Context) error {
	i.stateMu.Lock()
	prev := i.state
	if prev == stateStopping || prev == stateStopped {
		i.stateMu.Unlock()
		return nil
	}
	i.state = stateStopping
	hasStop := i.def != nil && i.def.HasOnStop
	i.stateMu.Unlock()

	var stopErr error
	if prev == stateStarted && hasStop {
		sctx, cancel := context.WithTimeout(ctx, i.limits.StopTimeout)
		if r := <-i.dispatch(sctx, "stop", "", nil, nil); r.err != nil {
			stopErr = fmt.Errorf("plugin: onStop: %w", r.err)
		}
		cancel()
	}
	i.shutdown()
	i.stateMu.Lock()
	i.state = stateStopped
	i.stateMu.Unlock()
	return stopErr
}

func (i *Instance) shutdown() {
	i.cancel(ErrStopped)
	i.qmu.Lock()
	i.closed = true
	i.queue = nil
	i.qmu.Unlock()
	select {
	case i.wake <- struct{}{}:
	default:
	}
	select {
	case <-i.loopDone:
	case <-time.After(i.limits.SyncBudget + 250*time.Millisecond):
		i.vm.rt.Interrupt(ErrStopped)
		<-i.loopDone
	}
	// The loop has exited: loop-only state is ours now.
	for _, t := range i.timers {
		t.timer.Stop()
	}
	i.timers = nil
	i.imu.Lock()
	for _, x := range i.interactions {
		x.stopAutoDefer()
	}
	i.interactions = map[string]*interaction{}
	i.imu.Unlock()
}

// ───────────────────────────────────────────────────────────── event loop

// post queues f on the loop. It returns false once the instance is stopped.
func (i *Instance) post(f func()) bool {
	i.qmu.Lock()
	if i.closed {
		i.qmu.Unlock()
		return false
	}
	i.queue = append(i.queue, f)
	i.qmu.Unlock()
	select {
	case i.wake <- struct{}{}:
	default:
	}
	return true
}

func (i *Instance) loop() {
	defer close(i.loopDone)
	for {
		i.qmu.Lock()
		for len(i.queue) == 0 {
			if i.closed {
				i.qmu.Unlock()
				return
			}
			i.qmu.Unlock()
			<-i.wake
			i.qmu.Lock()
		}
		f := i.queue[0]
		i.queue[0] = nil
		i.queue = i.queue[1:]
		i.qmu.Unlock()
		i.runTask(f)
	}
}

func (i *Instance) runTask(f func()) {
	defer func() {
		if r := recover(); r != nil {
			i.logger.Error("plugin host: panic in event loop", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	f()
}

type tracker struct{ i *Instance }

func (t tracker) Grab() any { return t.i.current }
func (t tracker) Resumed(o any) {
	inv, _ := o.(*invocation)
	t.i.current = inv
}
func (t tracker) Exited() { t.i.current = nil }

// currentInv is the invocation whose code is running (loop only).
func (i *Instance) currentInv() *invocation {
	if i.current != nil {
		return i.current
	}
	return i.bg
}

// budgetTimer interrupts the VM when a turn runs longer than its budget.
type budgetTimer struct {
	mu     sync.Mutex
	gen    uint64
	active bool
	d      time.Duration
	timer  *time.Timer
	rt     *sobek.Runtime
}

func (b *budgetTimer) arm(d time.Duration) {
	b.mu.Lock()
	b.gen++
	gen := b.gen
	b.active, b.d = true, d
	b.timer = time.AfterFunc(d, func() {
		b.mu.Lock()
		if b.active && b.gen == gen {
			b.rt.Interrupt(ErrBudgetExceeded)
		}
		b.mu.Unlock()
	})
	b.mu.Unlock()
}

func (b *budgetTimer) disarm() {
	b.mu.Lock()
	b.active = false
	t := b.timer
	b.mu.Unlock()
	if t != nil {
		t.Stop()
	}
}

// unbudgeted runs f (host-side work inside a turn, e.g. parsing a large host result) with
// the budget suspended; the turn gets a fresh budget afterwards. f must not run plugin code.
func (i *Instance) unbudgeted(f func()) {
	b := &i.budget
	b.mu.Lock()
	wasActive, d := b.active, b.d
	b.mu.Unlock()
	if !wasActive {
		f()
		return
	}
	b.disarm()
	defer b.arm(d)
	f()
}

// enter runs one turn of JS (fn plus the microtasks it queues) for inv under a sync budget.
// When the budget is exceeded, the invocation whose code was running is aborted. The
// returned error is fn's error (interrupts included); budget aborts are already handled.
func (i *Instance) enter(inv *invocation, budget time.Duration, fn func() error) error {
	rt := i.vm.rt
	rt.ClearInterrupt()
	i.current = inv
	i.budget.arm(budget)
	err := fn()
	i.budget.disarm()
	culprit := i.current
	i.current = nil
	rt.ClearInterrupt() // no Interrupt can happen after disarm
	if err != nil && isInterrupt(err) {
		if culprit == nil {
			culprit = inv
		}
		if culprit != i.bg {
			e := fmt.Errorf("%s %q: %w (%s); pending microtasks of this turn were dropped", culprit.kind, culprit.name, ErrBudgetExceeded, budget)
			culprit.cancel(e)
			culprit.settle(invResult{err: e})
		}
		i.logger.Error("plugin code exceeded its synchronous execution budget and was interrupted",
			"handler", culprit.kind, "name", culprit.name, "budget", budget)
	}
	i.flushRejections()
	return err
}

func (i *Instance) flushRejections() {
	if len(i.rejections) == 0 {
		return
	}
	for p := range i.rejections {
		se := i.vm.scriptErrorFromValue(p.Result())
		i.logger.Warn("unhandled promise rejection in plugin code", "error", se.Error())
	}
	clear(i.rejections)
}

// ───────────────────────────────────────────────────────────── invocations

type invResult struct {
	err  error
	data string
}

// invocation is one handler run. Its context bounds every host call made by its code
// (tracked across awaits with Sobek's AsyncContextTracker).
type invocation struct {
	id      uint64
	kind    string
	name    string
	ctx     context.Context
	cancel  context.CancelCauseFunc
	release context.CancelFunc
	done    chan invResult
	once    sync.Once
}

func (inv *invocation) settle(r invResult) {
	inv.once.Do(func() { inv.done <- r })
}

func (inv *invocation) alive() error {
	if inv.ctx.Err() != nil {
		return fmt.Errorf("%s %q is no longer running: %w", inv.kind, inv.name, context.Cause(inv.ctx))
	}
	return nil
}

func (i *Instance) newInvocation(caller context.Context, kind, name string, timeout time.Duration) (*invocation, func() bool) {
	tctx, release := context.WithTimeoutCause(i.ctx, timeout, ErrHandlerTimeout)
	ctx, cancel := context.WithCancelCause(tctx)
	inv := &invocation{
		id: i.nextInv.Add(1), kind: kind, name: name, ctx: ctx, cancel: cancel, release: release,
		done: make(chan invResult, 1),
	}
	stop := context.AfterFunc(caller, func() { cancel(context.Cause(caller)) })
	return inv, stop
}

func (i *Instance) checkState(kind string) error {
	i.stateMu.Lock()
	defer i.stateMu.Unlock()
	switch i.state {
	case stateStarted:
		return nil
	case stateStarting:
		if kind == "start" {
			return nil
		}
		return ErrNotStarted
	case stateStopping:
		if kind == "stop" {
			return nil
		}
		return ErrStopped
	case stateStopped:
		return ErrStopped
	}
	return ErrNotStarted
}

// dispatch queues a handler call. The returned channel receives exactly one result: when
// the handler's promise settles, its budget is exceeded, it times out, ctx is cancelled or
// the instance stops. Calls are queued in call order.
func (i *Instance) dispatch(caller context.Context, kind, name string, data any, inter *interaction) <-chan invResult {
	out := make(chan invResult, 1)
	finish := func(r invResult) {
		if inter != nil {
			inter.finish(r.err)
		}
		out <- r
	}
	if err := i.checkState(kind); err != nil {
		finish(invResult{err: err})
		return out
	}
	var dataJSON string
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			finish(invResult{err: fmt.Errorf("plugin: encode %s arguments: %w", kind, err)})
			return out
		}
		dataJSON = string(b)
	}
	timeout := i.limits.HandlerTimeout
	if kind == "stop" {
		timeout = i.limits.StopTimeout
	}
	inv, stopCaller := i.newInvocation(caller, kind, name, timeout)

	posted := i.post(func() {
		if err := inv.alive(); err != nil {
			inv.settle(invResult{err: err})
			return
		}
		rt := i.vm.rt
		ok := rt.ToValue(func(c sobek.FunctionCall) sobek.Value {
			r := invResult{}
			if a := c.Argument(0); !sobek.IsUndefined(a) {
				r.data = a.String()
			}
			inv.settle(r)
			return sobek.Undefined()
		})
		fail := rt.ToValue(func(c sobek.FunctionCall) sobek.Value {
			inv.settle(invResult{err: parseScriptError(c.Argument(0).String())})
			return sobek.Undefined()
		})
		err := i.enter(inv, i.limits.SyncBudget, func() error {
			handled, err := i.vm.dispatchF(sobek.Undefined(), rt.ToValue(kind), rt.ToValue(name), rt.ToValue(dataJSON), ok, fail)
			if err == nil && !handled.ToBoolean() {
				inv.settle(invResult{err: fmt.Errorf("%w for %s %q", ErrNoHandler, kind, name)})
			}
			return err
		})
		if err != nil && !isInterrupt(err) {
			inv.settle(invResult{err: i.vm.asScriptError(err)})
		}
	})
	if !posted {
		inv.settle(invResult{err: ErrStopped})
	}

	go func() {
		var r invResult
		select {
		case r = <-inv.done:
		case <-inv.ctx.Done():
			select {
			case r = <-inv.done:
			default:
				cause := context.Cause(inv.ctx)
				if errors.Is(cause, ErrHandlerTimeout) {
					cause = fmt.Errorf("%s %q: %w after %s", kind, name, ErrHandlerTimeout, timeout)
				}
				r = invResult{err: cause}
			}
		}
		stopCaller()
		if r.err != nil && !errors.Is(r.err, ErrNoHandler) && !errors.Is(r.err, ErrStopped) && !errors.Is(r.err, ErrBudgetExceeded) {
			i.logger.Error("plugin handler failed", "handler", kind, "name", name, "error", r.err.Error())
		}
		finish(r)
	}()
	return out
}

// ───────────────────────────────────────────────────────────── public dispatch

// DispatchEvent queues a gateway event and returns immediately; the channel receives the
// handler's result. Use it from the gateway goroutine to keep arrival order without
// blocking. Events without a handler complete immediately with nil.
func (i *Instance) DispatchEvent(ctx context.Context, ev discord.Event) <-chan error {
	name := EventName(ev)
	if name == "" {
		return errChan(fmt.Errorf("plugin: %T is not a plugin event", ev))
	}
	if sid := discord.ServerIDOf(ev); sid != "" && sid != i.env.ServerID {
		return errChan(fmt.Errorf("plugin: event for server %s routed to the instance of server %s", sid, i.env.ServerID))
	}
	def := i.Definition()
	if def == nil || !def.HasEvent(name) {
		if err := i.checkState("event"); err != nil {
			return errChan(err)
		}
		return errChan(nil)
	}
	return resultErr(i.dispatch(ctx, "event", name, i.eventDTO(ev), nil))
}

// HandleEvent runs the handler for a gateway event and waits for it.
func (i *Instance) HandleEvent(ctx context.Context, ev discord.Event) error {
	return <-i.DispatchEvent(ctx, ev)
}

// DispatchInteraction queues an interaction (see HandleInteraction) and returns immediately.
func (i *Instance) DispatchInteraction(ctx context.Context, it discord.Interaction, kind, localID string) <-chan error {
	var ephemeral bool
	switch kind {
	case "command":
		if def := i.Definition(); def != nil {
			if c, ok := def.Command(localID); ok {
				ephemeral = c.Ephemeral
			}
		}
	case "component", "modal":
	default:
		return errChan(fmt.Errorf("plugin: unknown interaction kind %q", kind))
	}
	if it.ServerID != "" && it.ServerID != i.env.ServerID {
		return errChan(fmt.Errorf("plugin: interaction for server %s routed to the instance of server %s", it.ServerID, i.env.ServerID))
	}
	x := i.newInteraction(it, kind, ephemeral)
	return resultErr(i.dispatch(ctx, kind, localID, i.interactionDTO(x, localID), x))
}

// HandleInteraction runs the handler of a command (localID = command name), component or
// modal (localID = local id from ParseCustomID) and waits for it. Interaction responses
// follow cicord-sdk.d.ts: auto-defer after Limits.AutoDeferAfter, generic ephemeral error
// reply when the handler fails before answering.
func (i *Instance) HandleInteraction(ctx context.Context, it discord.Interaction, kind, localID string) error {
	return <-i.DispatchInteraction(ctx, it, kind, localID)
}

// HandleRoute runs the portal route matching method and path (requires portal.routes).
// Access control (RouteDef.Access, see Definition.MatchRoute) is the caller's job.
func (i *Instance) HandleRoute(ctx context.Context, method, path string, req RouteRequest) (RouteResponse, error) {
	if err := i.require(capPortalRoutes); err != nil {
		return RouteResponse{}, err
	}
	def := i.Definition()
	if def == nil {
		return RouteResponse{}, ErrNotStarted
	}
	route, params, ok := def.MatchRoute(method, path)
	if !ok {
		return RouteResponse{}, fmt.Errorf("%w for route %s %s", ErrNoHandler, method, path)
	}
	dto := routeDTO{Params: params, Query: req.Query, Body: req.Body}
	if dto.Query == nil {
		dto.Query = map[string]string{}
	}
	if len(dto.Body) == 0 {
		dto.Body = nil
	}
	if req.Viewer != nil {
		v := &viewerDTO{User: i.userDTO(req.Viewer.User)}
		if req.Viewer.Member != nil {
			m := i.memberDTO(*req.Viewer.Member)
			v.Member = &m
		}
		dto.Viewer = v
	}
	r := <-i.dispatch(ctx, "route", route.Key(), dto, nil)
	if r.err != nil {
		return RouteResponse{}, r.err
	}
	var out struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
	}
	if err := json.Unmarshal([]byte(r.data), &out); err != nil {
		return RouteResponse{}, fmt.Errorf("plugin: route response: %w", err)
	}
	if out.Status < 100 || out.Status > 599 {
		return RouteResponse{}, fmt.Errorf("plugin: route %s returned invalid status %d", route.Key(), out.Status)
	}
	return RouteResponse{Status: out.Status, Headers: out.Headers, Body: out.Body}, nil
}

// RunSchedule runs a `schedules` entry (requires scheduler). Computing when is the bot
// host's job (see NextRun).
func (i *Instance) RunSchedule(ctx context.Context, name string) error {
	if err := i.require(capScheduler); err != nil {
		return err
	}
	return (<-i.dispatch(ctx, "schedule", name, nil, nil)).err
}

// RunTask runs a `tasks` entry with the payload given to ctx.schedule.after (requires
// scheduler).
func (i *Instance) RunTask(ctx context.Context, name string, payload json.RawMessage) error {
	if err := i.require(capScheduler); err != nil {
		return err
	}
	return (<-i.dispatch(ctx, "task", name, payloadDTO{Payload: rawOrNil(payload)}, nil)).err
}

// Deliver runs the subscription for topic ("<plugin>.<topic>"; requires events.subscribe).
func (i *Instance) Deliver(ctx context.Context, topic string, payload json.RawMessage) error {
	if err := i.require(capEventsSubscribe); err != nil {
		return err
	}
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	return (<-i.dispatch(ctx, "subscription", topic, payloadDTO{Payload: payload}, nil)).err
}

func rawOrNil(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return nil
	}
	return r
}

func errChan(err error) <-chan error {
	c := make(chan error, 1)
	c <- err
	return c
}

func resultErr(in <-chan invResult) <-chan error {
	out := make(chan error, 1)
	go func() { out <- (<-in).err }()
	return out
}

// ───────────────────────────────────────────────────────────── timers

const maxTimers = 10000

type jsTimer struct {
	fn     sobek.Callable
	inv    *invocation
	repeat bool
	d      time.Duration
	timer  *time.Timer
}

func (i *Instance) setTimer(fn sobek.Callable, ms float64, repeat bool) (int64, error) {
	inv := i.currentInv()
	if err := inv.alive(); err != nil {
		return 0, err
	}
	if len(i.timers) >= maxTimers {
		return 0, fmt.Errorf("too many active timers (max %d)", maxTimers)
	}
	if ms != ms || ms < 0 { // NaN
		ms = 0
	}
	const maxMs = float64(24 * 24 * time.Hour / time.Millisecond)
	if ms > maxMs {
		ms = maxMs
	}
	d := time.Duration(ms * float64(time.Millisecond))
	if repeat && d < time.Millisecond {
		d = time.Millisecond
	}
	i.nextTimer++
	id := i.nextTimer
	t := &jsTimer{fn: fn, inv: inv, repeat: repeat, d: d}
	i.timers[id] = t
	t.timer = time.AfterFunc(d, func() { i.post(func() { i.fireTimer(id) }) })
	return id, nil
}

func (i *Instance) fireTimer(id int64) {
	t, ok := i.timers[id]
	if !ok {
		return
	}
	if t.inv.alive() != nil {
		delete(i.timers, id)
		return
	}
	if !t.repeat {
		delete(i.timers, id)
	}
	err := i.enter(t.inv, i.limits.SyncBudget, func() error {
		_, err := t.fn(sobek.Undefined())
		return err
	})
	if err != nil && !isInterrupt(err) {
		i.logger.Error("uncaught error in timer callback", "error", i.vm.asScriptError(err).Error())
	}
	if t.repeat {
		if _, still := i.timers[id]; still {
			t.timer.Reset(t.d)
		}
	}
}

func (i *Instance) clearTimer(id int64) {
	if t, ok := i.timers[id]; ok {
		t.timer.Stop()
		delete(i.timers, id)
	}
}
