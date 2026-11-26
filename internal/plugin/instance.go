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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/locales"
	"github.com/cicord/cicord/internal/media"
	"github.com/cicord/cicord/internal/plugin/qjs"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/state"
)

// Env is everything an instance may touch. It is provided by the bot host.
type Env struct {
	Locales         locales.Catalog
	OverlayApply    func(context.Context, reconcile.Overlay) error
	OverlayRemove   func(context.Context, string) error
	OverlayList     func(context.Context) ([]reconcile.Overlay, error)
	RequestSync     func(context.Context, string) error
	Incident        func(context.Context, discord.IncidentActions) error
	UploadLimit     func() int // current server maximum bytes per file
	ManagedResource func(string) (bool, bool)
	AttributeAudit  func(context.Context, discord.AuditLogEntryEvent) discord.AuditLogEntryEvent
	Operations      *discord.OperationScope
	BotKey          string
	InstanceName    string // instance key, unique within the bot (namespaces custom IDs)
	PluginName      string
	PluginVersion   string
	Bot             discord.User
	ApplicationID   string

	ServerKey             string
	ServerID              string
	ServerPreferredLocale string
	ServerName            string
	ServerIconURL         string

	// Config is the effective config (defaults applied, overrides merged). It must be
	// JSON-serializable; plugins see it deep-frozen.
	Config map[string]any
	// Grants are the capabilities granted in bot.yaml.
	Grants []string
	// NetworkAllow is plugin.yaml spec.network.allow.
	Index        state.FullTextIndex
	ReportStatus func(key, message string)
	NetworkAllow []string
	Secret       func(string) (string, []string, bool)
	Files        map[string][]byte
	// AllowPrivateNetworks disables the egress address checks and allows plain http
	// (tests and `cicord dev` only).
	AllowPrivateNetworks bool
	// MessageContent reports that the plugin declares the MessageContent intent in
	// plugin.yaml. Without it, message content and attachments are blanked in every
	// Message the plugin sees (events and channel.messages()), like Discord does for bots
	// without the intent, except for the bot's own messages, messages that mention the bot
	// and DMs. Gateway intents are unioned per bot, so this keeps the gating per plugin.
	MessageContent bool
	// ServerMembers reports that the plugin declares the ServerMembers intent in plugin.yaml,
	// which role.members() requires (it lists the server's members).
	ServerModeration bool
	ServerMembers    bool
	// Membership of these role keys is declared in the active Git revision.
	DeclaredRolesByUser map[string][]string
	DeclaredMemberRoles []string
	// ServerVoiceStates reports that the plugin declares the ServerVoiceStates intent in
	// plugin.yaml, which server.voiceMembers() requires.
	ServerVoiceStates bool

	Discord discord.Client
	// Members lists every member of the server for role.members(), usually from a short-lived
	// cache shared by the server's instances. nil: Discord is asked on every call (it must
	// implement discord.MemberLister).
	Members func(ctx context.Context) ([]discord.Member, error)
	// VoiceStates returns the voice states of the server's connected members (the
	// runtime's cache, kept current by gateway events); known is false until the gateway
	// has sent them (server.voiceMembers() then fails rather than answer "nobody"). nil:
	// never known.
	VoiceStates func() (states []discord.VoiceState, known bool)
	Resolver    Resolver // may be nil (nothing resolves)
	// OwnerID returns the server owner's user id, "" when unknown (nil: unknown). It is
	// called on every use, so an ownership transfer is seen at once.
	AFKChannelID func() string
	OwnerID      func() string
	// Access is the server's spec.access (Member.access).
	Access Access

	DMReceipts state.KV          // host-owned retractable DM receipts, never exposed through ctx.kv
	KV         state.KV          // already scoped to the instance; nil: kv calls fail
	Tasks      state.TaskWriter  // nil: ctx.schedule fails
	TaskScope  string            // Task.Scope for tasks created by this instance
	Resources  state.ResourceMap // records plugin-owned channels; nil: not recorded
	Portal     PortalLinker      // nil: ctx.portal.link fails
	// Publish sends a cross-plugin event. topic is namespaced: "<plugin>.<topic>".
	Publish func(topic string, payload json.RawMessage) error

	Logger     *slog.Logger // nil: discarded
	Limits     Limits
	HTTPClient *http.Client // optional override for http.fetch (tests); egress name checks still apply
	// OnHandler, when set, receives the outcome of every handler call (metrics). It is
	// called from the goroutine delivering the result and must not block.
	OnHandler func(HandlerResult)
}

// Access lists who holds the moderator and admin access levels of the server
// (servers/<server>.yaml spec.access): user ids and role keys. The owner is always admin.
type Access struct {
	AdminUsers     []string
	AdminRoles     []string
	ModeratorUsers []string
	ModeratorRoles []string
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

// DefaultHandlerTimeout is also used to size durable task leases.
const DefaultHandlerTimeout = 10 * time.Second

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
	// AutocompleteTimeout bounds autocomplete handlers: Discord drops the response after 3
	// seconds and an autocomplete cannot be deferred. Default 2.5s.
	AutocompleteTimeout time.Duration
	// StopTimeout bounds onStop. Default 3s.
	StopTimeout time.Duration
	// FetchTimeout is the default http.fetch timeout (init.timeoutMs can raise it up to
	// HandlerTimeout). Default 10s.
	FetchTimeout time.Duration
	// MaxResponseBytes caps http.fetch response bodies. Default 5 MiB.
	MaxResponseBytes int64
	// Memory caps the instance's JS heap in bytes (the VM's linear memory is capped a fixed
	// margin above it). Reaching it discards the instance (ErrOutOfMemory, ErrCrashed); the
	// bot host restarts it. Default 64 MiB, at most 1 GiB.
	Memory int64
	// QueueLength bounds the handler calls waiting for the instance's event loop; beyond
	// it calls fail with ErrBusy. Default 1024.
	QueueLength int
}

func (l Limits) withDefaults() Limits {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&l.SyncBudget, 50*time.Millisecond)
	def(&l.StartupBudget, 2*time.Second)
	def(&l.HandlerTimeout, DefaultHandlerTimeout)
	def(&l.AutoDeferAfter, 2*time.Second)
	def(&l.AutocompleteTimeout, 2500*time.Millisecond)
	def(&l.StopTimeout, 3*time.Second)
	def(&l.FetchTimeout, 10*time.Second)
	if l.MaxResponseBytes <= 0 {
		l.MaxResponseBytes = 5 << 20
	}
	if l.Memory <= 0 {
		l.Memory = qjs.DefaultMemoryLimit
	}
	if l.QueueLength <= 0 {
		l.QueueLength = 1024
	}
	return l
}

// RouteRequest is a portal request routed to a plugin route.
type RouteRequest struct {
	Grant          *auth.ScopedGrant
	ExpectedAccess string
	Query          map[string]string
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
// (https://cicord.dev/docs/project/architecture/). *Instance is the QuickJS (internal/plugin/qjs) implementation.
type Runtime interface {
	Definition() *Definition
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// Done is closed when the instance stops serving: after Stop, or when it crashed
	// (Err wraps ErrCrashed); the bot host then replaces it.
	Done() <-chan struct{}
	Err() error
	HandleEvent(ctx context.Context, ev discord.Event) error
	HandleInteraction(ctx context.Context, it discord.Interaction, kind, localID string) error
	HandleRoute(ctx context.Context, method, path string, req RouteRequest) (RouteResponse, error)
	RunSchedule(ctx context.Context, name string) error
	RunTask(ctx context.Context, name string, payload json.RawMessage, delivery TaskDelivery) error
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

// Instance is one plugin running for one (bot, server): a QuickJS VM driven by a single
// event-loop goroutine. All methods are safe for concurrent use.
type Instance struct {
	assets        map[string][]byte
	uploads       uploadStore
	mediaSlots    chan struct{}
	binaryResults chan struct{}
	env           Env
	def           *Definition
	code          string
	limits        Limits
	logger        *slog.Logger
	grants        map[string]bool
	egress        *egress

	ctx    context.Context // instance lifetime; its cause tells Stop from a crash
	cancel context.CancelCauseFunc

	stateMu sync.Mutex
	state   int

	// event loop
	qmu      sync.Mutex
	queue    []func()
	closed   bool
	wake     chan struct{}
	loopDone chan struct{}
	queued   atomic.Int64 // handler calls posted and not started yet

	// loop-only state
	vm        *vm
	current   *invocation
	bg        *invocation
	timers    map[int64]*jsTimer
	nextTimer int64
	nextCall  int64
	inflight  int // async host calls not settled yet
	budget    budgetTimer
	paused    pausedBudget

	nextInv atomic.Uint64
	invMu   sync.Mutex
	invs    map[uint64]*invocation // running handlers, by id (settled through host done)

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
	code, assets, err := unpackAssets(code)
	if err != nil {
		return nil, err
	}
	limits := env.Limits.withDefaults()
	i := &Instance{
		env: env, assets: assets, mediaSlots: make(chan struct{}, 2), binaryResults: make(chan struct{}, 4),
		def:            def,
		code:           code,
		limits:         limits,
		grants:         map[string]bool{},
		wake:           make(chan struct{}, 1),
		loopDone:       make(chan struct{}),
		timers:         map[int64]*jsTimer{},
		invs:           map[uint64]*invocation{},
		interactions:   map[string]*interaction{},
		serverChannels: map[string]bool{},
		dmChannels:     map[string]bool{},
	}
	for _, g := range env.Grants {
		i.grants[g] = true
	}
	if i.grants["render"] {
		if err := media.Prepare(); err != nil {
			return nil, err
		}
	}
	i.logger = env.Logger.With("bot", env.BotKey, "plugin", env.PluginName, "instance", env.InstanceName, "server", env.ServerKey)
	i.egress = newEgress(env.NetworkAllow, env.AllowPrivateNetworks, env.HTTPClient, limits.MaxResponseBytes)
	i.ctx, i.cancel = context.WithCancelCause(context.Background())
	i.bg = &invocation{kind: "background", ctx: i.ctx, cancel: func(error) {}, done: make(chan invResult, 1)}

	v, err := newVM(context.Background(), i, limits.Memory)
	if err != nil {
		i.cancel(ErrStopped)
		return nil, err
	}
	i.vm = v
	i.budget.interrupt = v.q.Interrupt
	go i.loop()
	return i, nil
}

// Definition returns the static definition (nil before Start when NewInstance got nil).
func (i *Instance) Definition() *Definition {
	i.stateMu.Lock()
	defer i.stateMu.Unlock()
	return i.def
}

// Done is closed when the instance stops serving: Stop was called, or the instance crashed
// (Err wraps ErrCrashed: memory limit, hard stop, engine trap).
func (i *Instance) Done() <-chan struct{} { return i.ctx.Done() }

// Err is nil while the instance serves, ErrStopped after Stop, and wraps ErrCrashed after a
// crash.
func (i *Instance) Err() error {
	if i.ctx.Err() == nil {
		return nil
	}
	return context.Cause(i.ctx)
}

// endedErr is the error for calls that cannot run anymore.
func (i *Instance) endedErr() error {
	if err := i.Err(); err != nil && errors.Is(err, ErrCrashed) {
		return err
	}
	return ErrStopped
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
		return i.endedErr()
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
		return i.endedErr()
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

// loadModule runs on the loop: evaluate the bundle, extract the definition if needed and
// build ctx, in one turn bounded by StartupBudget.
func (i *Instance) loadModule() error {
	ctxJSON, err := json.Marshal(i.contextDTO())
	if err != nil {
		return fmt.Errorf("plugin: config is not JSON-serializable: %w", err)
	}
	err = i.enter(i.bg, i.limits.StartupBudget, func(ctx context.Context) error {
		if err := i.vm.loadPlugin(ctx, i.code); err != nil {
			return err
		}
		if i.def == nil {
			d, err := i.vm.definition(ctx)
			if err != nil {
				return err
			}
			i.stateMu.Lock()
			i.def = d
			i.stateMu.Unlock()
		}
		if _, err := i.vm.callAPI(ctx, "setup", string(ctxJSON)); err != nil {
			return fmt.Errorf("plugin: setup: %w", err)
		}
		return nil
	})
	if isInterrupt(err) {
		return fmt.Errorf("plugin: module evaluation exceeded %s: %w", i.limits.StartupBudget, ErrBudgetExceeded)
	}
	return err
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
		i.vm.q.Interrupt()
		select {
		case <-i.loopDone:
		case <-time.After(250 * time.Millisecond):
			i.budget.killNow(ErrStopped)
			<-i.loopDone
		}
	}
	// The loop has exited: loop-only state is ours now.
	for _, t := range i.timers {
		t.timer.Stop()
	}
	i.timers = nil
	i.vm.close()
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

// currentInv is the invocation whose code is running (loop only): the one the current turn
// was entered for. Turns are always started by Go for a known invocation (handler call,
// host call settlement, timer), so continuations after an await are attributed to the
// invocation that owns the host call being settled.
func (i *Instance) currentInv() *invocation {
	if i.current != nil {
		return i.current
	}
	return i.bg
}

func isInterrupt(err error) bool { return errors.Is(err, qjs.ErrInterrupted) }

// budgetTimer bounds one turn: at the budget the VM is interrupted (soft stop, the VM keeps
// serving); if the code is still running hardStopGrace later (a native builtin that does
// not check for interrupts) the turn's context is cancelled, which kills the VM.
type budgetTimer struct {
	mu        sync.Mutex
	gen       uint64
	active    bool
	d         time.Duration
	soft      *time.Timer
	hard      *time.Timer
	kill      context.CancelCauseFunc
	interrupt func()
}

func (b *budgetTimer) arm(d time.Duration, kill context.CancelCauseFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gen++
	gen := b.gen
	b.active, b.d, b.kill = true, d, kill
	b.soft = time.AfterFunc(d, func() {
		b.mu.Lock()
		if b.active && b.gen == gen {
			b.interrupt()
		}
		b.mu.Unlock()
	})
	b.hard = time.AfterFunc(d+hardStopGrace(d), func() {
		b.mu.Lock()
		if b.active && b.gen == gen {
			kill(ErrBudgetExceeded)
		}
		b.mu.Unlock()
	})
}

func (b *budgetTimer) disarm() {
	b.mu.Lock()
	b.active = false
	soft, hard := b.soft, b.hard
	b.mu.Unlock()
	if soft != nil {
		soft.Stop()
		hard.Stop()
	}
}

// pause suspends the budget (bounded host-side work inside a turn, e.g. parsing a large
// host result); resume gives the turn a fresh budget.
func (b *budgetTimer) pause() (time.Duration, context.CancelCauseFunc, bool) {
	b.mu.Lock()
	active, d, kill := b.active, b.d, b.kill
	b.mu.Unlock()
	if active {
		b.disarm()
	}
	return d, kill, active
}

// killNow kills the running turn, if any (Stop of a VM stuck in a native builtin).
func (b *budgetTimer) killNow(cause error) {
	b.mu.Lock()
	kill := b.kill
	b.mu.Unlock()
	if kill != nil {
		kill(cause)
	}
}

func (i *Instance) pauseBudget() {
	d, kill, active := i.budget.pause()
	i.paused = pausedBudget{d: d, kill: kill, active: active}
}

func (i *Instance) resumeBudget() {
	p := i.paused
	i.paused = pausedBudget{}
	if p.active {
		i.budget.arm(p.d, p.kill)
	}
}

type pausedBudget struct {
	d      time.Duration
	kill   context.CancelCauseFunc
	active bool
}

// enter runs one turn of JS for inv: fn plus every microtask it queues, under a sync
// budget. When the budget is exceeded the invocation is aborted with ErrBudgetExceeded and
// the VM keeps serving; the microtasks still queued then run in a second turn charged to
// the same invocation, and if that one exceeds its budget too, the instance is crashed
// (code that keeps running after its handler was aborted). When the VM dies (memory
// limit, hard stop, trap) the instance is crashed. enter returns fn's error (interrupts
// included; budget aborts are already handled), or the crash error.
func (i *Instance) enter(inv *invocation, budget time.Duration, fn func(ctx context.Context) error) error {
	err := i.turn(inv, budget, fn)
	if err != nil && isInterrupt(err) && i.vm.q.Dead() == nil {
		if inv != i.bg {
			e := fmt.Errorf("%s %q: %w (%s)", inv.kind, inv.name, ErrBudgetExceeded, budget)
			inv.cancel(e)
			inv.settle(invResult{err: e})
			i.forget(inv)
		}
		i.logger.Error("plugin code exceeded its synchronous execution budget and was interrupted",
			"handler", inv.kind, "name", inv.name, "budget", budget)
		if i.vm.q.JobsPending() {
			jerr := i.turn(inv, budget, func(context.Context) error { return nil })
			if isInterrupt(jerr) && i.vm.q.Dead() == nil {
				i.crash(fmt.Errorf("%w: code of %s %q kept running after it was aborted", ErrBudgetExceeded, inv.kind, inv.name))
			}
		}
	}
	if dead := i.vm.q.Dead(); dead != nil {
		i.crash(i.deathCause(dead, budget))
		return i.endedErr()
	}
	return err
}

// turn runs fn then drains microtasks under one budget, and flushes unhandled rejections.
func (i *Instance) turn(inv *invocation, budget time.Duration, fn func(ctx context.Context) error) error {
	q := i.vm.q
	if q.Dead() != nil {
		return q.Dead()
	}
	ctx, kill := context.WithCancelCause(context.Background())
	defer kill(nil)
	q.ClearInterrupt()
	i.current = inv
	i.budget.arm(budget, kill)
	err := fn(ctx)
	if q.Dead() == nil && !isInterrupt(err) {
		if jerr := q.RunJobs(ctx); jerr != nil {
			if isInterrupt(jerr) || q.Dead() != nil {
				err = jerr
			} else {
				i.logger.Error("uncaught error in plugin code", "error", i.vm.asScriptError(jerr).Error())
			}
		}
	}
	if jerr := q.TakeJobError(); jerr != nil {
		i.logger.Error("uncaught error in plugin code", "error", i.vm.asScriptError(jerr).Error())
	}
	if q.Dead() == nil && !isInterrupt(err) && q.Rejections() {
		i.flushRejections(ctx)
	}
	i.budget.disarm()
	i.current = nil
	q.ClearInterrupt()
	return err
}

func (i *Instance) deathCause(dead error, budget time.Duration) error {
	switch {
	case errors.Is(dead, qjs.ErrOutOfMemory):
		return fmt.Errorf("%w (%d MiB)", ErrOutOfMemory, i.limits.Memory>>20)
	case errors.Is(dead, qjs.ErrKilled):
		return fmt.Errorf("%w: plugin code ignored the interrupt for %s and was killed", ErrBudgetExceeded, budget+hardStopGrace(budget))
	}
	return dead
}

// crash discards the VM (loop only): every pending and later call fails with the crash
// error and Done is closed, so the bot host can replace the instance.
func (i *Instance) crash(cause error) {
	if i.ctx.Err() != nil {
		return // stopping (or already crashed)
	}
	e := fmt.Errorf("%w: %w", ErrCrashed, cause)
	i.logger.Error("plugin instance crashed", "error", cause.Error())
	i.cancel(e)
	i.qmu.Lock()
	i.closed = true
	i.queue = nil
	i.qmu.Unlock()
	i.vm.close()
}

func (i *Instance) flushRejections(ctx context.Context) {
	s, err := i.vm.callAPI(ctx, "takeRejections")
	if err != nil {
		return
	}
	var infos []string
	if json.Unmarshal([]byte(s), &infos) != nil {
		return
	}
	for _, info := range infos {
		i.logger.Warn("unhandled promise rejection in plugin code", "error", i.vm.scriptError(info).Error())
	}
}

// ───────────────────────────────────────────────────────────── invocations

type invResult struct {
	err  error
	data string
}

// invocation is one handler run. Its context bounds every host call made by its code
// (attributed per turn, see currentInv).
type invocation struct {
	locale string
	// Only member identities supplied by the host for this handler may be used to
	// check channel access. Plugin arguments cannot invent role memberships.
	audienceMembers sync.Map // user ID -> discord.Member
	id              uint64
	kind            string
	name            string
	ctx             context.Context
	cancel          context.CancelCauseFunc
	release         context.CancelFunc
	done            chan invResult
	once            sync.Once
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
	tctx, release := context.WithTimeoutCause(discord.WithOperations(i.ctx, i.env.Operations), timeout, ErrHandlerTimeout)
	ctx, cancel := context.WithCancelCause(tctx)
	inv := &invocation{
		id: i.nextInv.Add(1), kind: kind, name: name, ctx: ctx, cancel: cancel, release: release,
		done: make(chan invResult, 1),
	}
	stop := context.AfterFunc(caller, func() { cancel(context.Cause(caller)) })
	return inv, stop
}

// forget drops a settled invocation from the registry.
func (i *Instance) forget(inv *invocation) {
	i.invMu.Lock()
	delete(i.invs, inv.id)
	i.invMu.Unlock()
}

// done is the prelude reporting a handler's outcome (loop only).
func (i *Instance) done(id uint64, ok bool, data string) {
	i.invMu.Lock()
	inv := i.invs[id]
	delete(i.invs, id)
	i.invMu.Unlock()
	if inv == nil {
		return // already settled (timeout, budget)
	}
	switch {
	case !ok:
		inv.settle(invResult{err: i.vm.scriptError(data)})
	case len(data) > maxHandlerResult:
		inv.settle(invResult{err: fmt.Errorf("%s %q returned more than %d bytes", inv.kind, inv.name, maxHandlerResult)})
	default:
		inv.settle(invResult{data: data})
	}
}

func (i *Instance) checkState(kind string) error {
	if err := i.Err(); err != nil && errors.Is(err, ErrCrashed) {
		return err
	}
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

// HandlerResult is the outcome of one handler call (Env.OnHandler).
type HandlerResult struct {
	// Kind is the handler kind: start, stop, event, command, autocomplete, component, modal,
	// route, schedule, task or subscription.
	Kind string
	// Name is the handler's local name, from the plugin definition: the command, the
	// component or modal id, the event, the route key, the schedule, task or topic ("" for
	// start and stop). It never holds user input.
	Name string
	// Duration runs from the call to its result, including the wait in the instance queue.
	Duration time.Duration
	// Queued is false when the call was refused before reaching the queue (ErrBusy, a
	// crashed instance): Duration then measures nothing.
	Queued bool
	Err    error
}

// observe reports a handler result to Env.OnHandler. Calls that ran no plugin code because
// there is no handler or the instance is not serving (stopped, not started) are not
// reported.
func (i *Instance) observe(kind, name string, start time.Time, queued bool, err error) {
	if i.env.OnHandler == nil || errors.Is(err, ErrNoHandler) || errors.Is(err, ErrStopped) || errors.Is(err, ErrNotStarted) {
		return
	}
	i.env.OnHandler(HandlerResult{Kind: kind, Name: name, Duration: time.Since(start), Queued: queued, Err: err})
}

// dispatch queues a handler call. The returned channel receives exactly one result: when
// the handler's promise settles, its budget is exceeded, it times out, ctx is cancelled or
// the instance stops. Calls are queued in call order.
func (i *Instance) dispatch(caller context.Context, kind, name string, data any, inter *interaction) <-chan invResult {
	out := make(chan invResult, 1)
	start, queued := time.Now(), false
	finish := func(r invResult) {
		if inter != nil {
			inter.finish(r.err)
		}
		i.observe(kind, name, start, queued, r.err)
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
	switch kind {
	case "stop":
		timeout = i.limits.StopTimeout
	case "autocomplete":
		timeout = i.limits.AutocompleteTimeout
	}
	if i.queued.Load() >= int64(i.limits.QueueLength) {
		finish(invResult{err: fmt.Errorf("%w (%d waiting)", ErrBusy, i.limits.QueueLength)})
		return out
	}
	inv, stopCaller := i.newInvocation(caller, kind, name, timeout)
	switch v := data.(type) {
	case interactionDTO:
		inv.locale = v.Locale
	case autocompleteDTO:
		inv.locale = v.Locale
	}
	inv.rememberAudience(data)
	queued = true

	i.queued.Add(1)
	posted := i.post(func() {
		i.queued.Add(-1)
		if err := inv.alive(); err != nil {
			inv.settle(invResult{err: err})
			return
		}
		i.invMu.Lock()
		i.invs[inv.id] = inv
		i.invMu.Unlock()
		err := i.enter(inv, i.limits.SyncBudget, func(ctx context.Context) error {
			handled, err := i.vm.runAPI(ctx, "dispatch", strconv.FormatUint(inv.id, 10), kind, name, dataJSON)
			if err == nil && handled != "1" {
				i.forget(inv)
				inv.settle(invResult{err: fmt.Errorf("%w for %s %q", ErrNoHandler, kind, name)})
			}
			return err
		})
		if err != nil && !isInterrupt(err) {
			i.forget(inv)
			inv.settle(invResult{err: err})
		}
	})
	if !posted {
		i.queued.Add(-1)
		inv.settle(invResult{err: i.endedErr()})
	}

	go func() {
		ended := func() invResult {
			cause := context.Cause(inv.ctx)
			if errors.Is(cause, ErrHandlerTimeout) {
				cause = fmt.Errorf("%s %q: %w after %s", kind, name, ErrHandlerTimeout, timeout)
			}
			return invResult{err: cause}
		}
		var r invResult
		select {
		case r = <-inv.done:
		case <-inv.ctx.Done():
			select {
			case r = <-inv.done:
			default:
				r = ended()
			}
		}
		// A handler that fails once its invocation ended (timeout, cancellation) usually
		// fails because of it: its host calls return "no longer running". Report why it
		// ended, not the plugin's error; a handler that succeeded at the deadline keeps its
		// result.
		if r.err != nil && inv.ctx.Err() != nil {
			r = ended()
		}
		stopCaller()
		i.forget(inv)
		if r.err != nil && !errors.Is(r.err, ErrNoHandler) && !errors.Is(r.err, ErrStopped) && !errors.Is(r.err, ErrBudgetExceeded) && !errors.Is(r.err, ErrCrashed) {
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
	if _, ok := ev.(discord.AuditLogEntryEvent); ok && !i.grants[capAuditRead] {
		return errChan(&CapabilityError{Capability: capAuditRead})
	}
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
	if it.ServerID != "" && it.ServerID != i.env.ServerID {
		return errChan(fmt.Errorf("plugin: interaction for server %s routed to the instance of server %s", it.ServerID, i.env.ServerID))
	}
	var ephemeral bool
	switch kind {
	case "contextMenu":
		if it.CommandType == discord.MessageMenu && it.TargetMessage == nil || it.CommandType == discord.UserMenu && it.TargetMember == nil {
			return errChan(errors.New("context menu target is unavailable"))
		}
		if def := i.Definition(); def != nil {
			for _, m := range def.ContextMenus {
				if m.Name == localID && m.Type == it.CommandType {
					ephemeral = m.Ephemeral
					break
				}
			}
		}
	case "command":
		// The handler is the invoked leaf: "poll", "polls close", "polls admin purge".
		path, _ := it.CommandPath()
		path[0] = localID
		localID = strings.Join(path, " ")
		if def := i.Definition(); def != nil {
			if c, ok := def.Command(path[0]); ok {
				ephemeral = c.Ephemeral
			}
		}
	case "autocomplete":
		return i.dispatchAutocomplete(ctx, it, localID)
	case "component", "modal":
	default:
		return errChan(fmt.Errorf("plugin: unknown interaction kind %q", kind))
	}
	x := i.newInteraction(it, kind, ephemeral)
	return resultErr(i.dispatch(ctx, kind, localID, i.interactionDTO(x, localID), x))
}

// dispatchAutocomplete runs the autocomplete handler of the focused option and answers
// with its choices (ResponseAutocomplete). There is no deferral: when the handler fails,
// times out (Limits.AutocompleteTimeout) or returns invalid choices, the user gets an
// empty list and the error is returned.
func (i *Instance) dispatchAutocomplete(ctx context.Context, it discord.Interaction, command string) <-chan error {
	path, opts := it.CommandPath()
	path[0] = command
	focused, ok := it.FocusedOption()
	if !ok {
		return i.answerAutocomplete(ctx, it, nil, errors.New("plugin: autocomplete interaction without a focused option"))
	}
	var optDef discord.CommandOption
	if def := i.Definition(); def != nil {
		if c, ok := def.Command(command); ok {
			if leaf, ok := c.Leaf(path); ok {
				for _, o := range leaf {
					if o.Name == focused.Name {
						optDef = o
					}
				}
			}
		}
	}
	if !optDef.Autocomplete {
		// A stale command set on Discord's side: answer anyway ("no options", not "failed").
		return i.answerAutocomplete(ctx, it, nil, fmt.Errorf("%w for autocomplete of /%s option %q", ErrNoHandler, strings.Join(path, " "), focused.Name))
	}
	dto := i.autocompleteDTO(it, path, opts, focused)
	res := i.dispatch(ctx, "autocomplete", strings.Join(path, " ")+"/"+focused.Name, dto, nil)
	out := make(chan error, 1)
	go func() {
		r := <-res
		err := r.err
		var choices []discord.CommandChoice
		if err == nil {
			choices, err = parseChoices(r.data, optDef)
		}
		out <- <-i.answerAutocomplete(ctx, it, choices, err)
	}()
	return out
}

// answerAutocomplete sends the choices (an empty list when nil) and returns err, or the
// response error.
func (i *Instance) answerAutocomplete(ctx context.Context, it discord.Interaction, choices []discord.CommandChoice, err error) <-chan error {
	if choices == nil || err != nil {
		choices = []discord.CommandChoice{}
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	rerr := i.env.Discord.RespondInteraction(rctx, it.ID, it.Token, discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: choices})
	if rerr != nil {
		i.logger.Warn("autocomplete response failed", "interaction", it.ID, "error", rerr)
		if err == nil {
			err = rerr
		}
	}
	return errChan(err)
}

// parseChoices validates what an autocomplete handler returned: at most 25 {name, value}
// with names of 1-100 characters and values of the option's type (strings of at most 100
// characters, integers for integer options).
func parseChoices(data string, opt discord.CommandOption) ([]discord.CommandChoice, error) {
	if data == "" || data == "null" {
		return nil, nil
	}
	var raw []struct {
		Name  any `json:"name"`
		Value any `json:"value"`
	}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, fmt.Errorf("autocomplete must return an array of {name, value} choices")
	}
	if len(raw) > discord.MaxAutocompleteChoices {
		return nil, fmt.Errorf("autocomplete returned %d choices (at most %d)", len(raw), discord.MaxAutocompleteChoices)
	}
	out := make([]discord.CommandChoice, 0, len(raw))
	for k, c := range raw {
		name, ok := c.Name.(string)
		if !ok || name == "" || utf8.RuneCountInString(name) > 100 {
			return nil, fmt.Errorf("autocomplete choice %d: name must be 1-100 characters", k)
		}
		switch v := c.Value.(type) {
		case string:
			if opt.Type != discord.OptionString || utf8.RuneCountInString(v) > 100 {
				return nil, fmt.Errorf("autocomplete choice %q: value must be a %s", name, choiceKind(opt.Type))
			}
		case float64:
			if opt.Type == discord.OptionString || (opt.Type == discord.OptionInteger && v != float64(int64(v))) {
				return nil, fmt.Errorf("autocomplete choice %q: value must be a %s", name, choiceKind(opt.Type))
			}
		default:
			return nil, fmt.Errorf("autocomplete choice %q: value must be a %s", name, choiceKind(opt.Type))
		}
		out = append(out, discord.CommandChoice{Name: name, Value: c.Value})
	}
	return out, nil
}

func choiceKind(t discord.CommandOptionType) string {
	switch t {
	case discord.OptionInteger:
		return "integer"
	case discord.OptionNumber:
		return "number"
	}
	return "string of at most 100 characters"
}

// HandleInteraction runs the handler of a command (localID = command name; the invoked
// subcommand is read from the interaction), an autocomplete (localID = command name),
// a component or a modal (localID = local id from ParseCustomID) and waits for it.
// Interaction responses follow cicord-sdk.d.ts: auto-defer after Limits.AutoDeferAfter,
// generic ephemeral error reply when the handler fails before answering; autocompletes
// are answered with the handler's choices (never deferred).
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
	if req.ExpectedAccess != "" && req.ExpectedAccess != route.Access {
		return RouteResponse{}, ErrNoHandler
	}
	if route.Access == "scoped" {
		g := req.Grant
		if g == nil || g.Bot != i.env.BotKey || g.Instance != i.env.InstanceName || g.Plugin != i.env.PluginName || !g.Valid() || req.Viewer == nil || (params["id"] != "" && params["id"] != g.ID) {
			return RouteResponse{Status: 403}, nil
		}
		dto.Scope = &g.ResourceScope
	} else if req.Grant != nil {
		return RouteResponse{Status: 403}, nil
	}
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
	if len(r.data) > maxRouteResponse {
		return RouteResponse{}, fmt.Errorf("plugin: route %s returned more than %d bytes", route.Key(), maxRouteResponse)
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
func (i *Instance) RunTask(ctx context.Context, name string, payload json.RawMessage, delivery TaskDelivery) error {
	if err := i.require(capScheduler); err != nil {
		return err
	}
	return (<-i.dispatch(ctx, "task", name, payloadDTO{Payload: rawOrNil(payload), Task: &delivery}, nil)).err
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
	inv    *invocation
	repeat bool
	d      time.Duration
	timer  *time.Timer
}

func (i *Instance) setTimer(ms float64, repeat bool) (int64, error) {
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
	t := &jsTimer{inv: inv, repeat: repeat, d: d}
	i.timers[id] = t
	t.timer = time.AfterFunc(d, func() { i.post(func() { i.fireTimer(id) }) })
	return id, nil
}

func (i *Instance) fireTimer(id int64) {
	t, ok := i.timers[id]
	if !ok {
		return
	}
	sid := strconv.FormatInt(id, 10)
	if t.inv.alive() != nil {
		delete(i.timers, id)
		_ = i.enter(t.inv, i.limits.SyncBudget, func(ctx context.Context) error {
			_, err := i.vm.runAPI(ctx, "dropTimer", sid)
			return err
		})
		return
	}
	if !t.repeat {
		delete(i.timers, id)
	}
	keep := "0"
	if t.repeat {
		keep = "1"
	}
	err := i.enter(t.inv, i.limits.SyncBudget, func(ctx context.Context) error {
		_, err := i.vm.runAPI(ctx, "fireTimer", sid, keep)
		return err
	})
	if err != nil && !isInterrupt(err) && !errors.Is(err, ErrCrashed) {
		i.logger.Error("uncaught error in timer callback", "error", err.Error())
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
