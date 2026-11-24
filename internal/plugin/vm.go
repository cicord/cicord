package plugin

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/plugin/qjs"
)

//go:embed sdk.js
var preludeSource string

// bundleName is the script name used for plugin code; positions are mapped back to the
// TypeScript sources through the bundle's inline source map.
const bundleName = "plugin.bundle.js"

// Host call kinds (sdk.js hraw; 7 and 8 are qjs.KindPauseBudget/KindResumeBudget).
const (
	kindCall       = 1
	kindCallAsync  = 2
	kindLog        = 3
	kindSetTimer   = 4
	kindClearTimer = 5
	kindDone       = 6
)

// Host-side caps on data crossing the VM boundary.
const (
	// maxHostArgs caps the JSON arguments of one host call (checked before decoding).
	maxHostArgs = 2 << 20
	// maxLogFields caps the JSON fields of one log call.
	maxLogFields = 16 << 10
	// maxRouteResponse caps the JSON a route handler returns.
	maxRouteResponse = 4 << 20
	// maxHandlerResult caps what a handler settles with (routes are the only users).
	maxHandlerResult = maxRouteResponse + 1024
)

// host is what the prelude calls into. Inspect uses a host that refuses everything.
type host interface {
	call(op, args string) (string, bool, error)
	callAsync(op, args string) (int64, error)
	log(level, msg, fields string)
	setTimer(ms float64, repeat bool) (int64, error)
	clearTimer(id int64)
	// done receives a handler's result (dispatch).
	done(inv uint64, ok bool, data string)
	// pauseBudget/resumeBudget bracket bounded host-side work done inside a turn (parsing
	// host-produced JSON) so it does not count as runaway plugin code.
	pauseBudget()
	resumeBudget()
}

// vm is a QuickJS VM with the SDK prelude loaded.
type vm struct {
	q    *qjs.VM
	code string // the bundle (for source maps), set by loadPlugin
	smap *sourceMap
}

var logLevels = [...]string{"debug", "info", "warn", "error"}

func newVM(ctx context.Context, h host, memory int64) (*vm, error) {
	v := &vm{}
	hostFn := func(kind int32, a, b string, n float64) qjs.Reply {
		switch kind {
		case kindCall:
			res, ok, err := h.call(a, b)
			if err != nil {
				return qjs.Reply{Error: errorJSON(err)}
			}
			return qjs.Reply{Value: res, Undefined: !ok}
		case kindCallAsync:
			id, err := h.callAsync(a, b)
			if err != nil {
				return qjs.Reply{Error: errorJSON(err)}
			}
			return qjs.Reply{Value: strconv.FormatInt(id, 10)}
		case kindLog:
			lvl := "info"
			if n >= 0 && int(n) < len(logLevels) {
				lvl = logLevels[int(n)]
			}
			h.log(lvl, a, b)
		case kindSetTimer:
			id, err := h.setTimer(n, a == "1")
			if err != nil {
				return qjs.Reply{Error: errorJSON(err)}
			}
			return qjs.Reply{Value: strconv.FormatInt(id, 10)}
		case kindClearTimer:
			h.clearTimer(int64(n))
		case kindDone:
			h.done(uint64(n), a == "ok", b)
		case qjs.KindPauseBudget:
			h.pauseBudget()
		case qjs.KindResumeBudget:
			h.resumeBudget()
		}
		return qjs.Reply{Undefined: true}
	}
	bc, err := preludeBytecode()
	if err != nil {
		return nil, err
	}
	q, err := qjs.New(ctx, qjs.Config{MemoryLimit: memory}, hostFn)
	if err != nil {
		return nil, fmt.Errorf("plugin: create VM: %w", err)
	}
	v.q = q
	if err := q.PreludeBytecode(ctx, bc); err != nil {
		q.Close()
		return nil, fmt.Errorf("plugin: load SDK prelude: %w", err)
	}
	return v, nil
}

var (
	preludeOnce sync.Once
	preludeBC   *qjs.Bytecode
	preludeErr  error
)

// preludeBytecode compiles the SDK prelude once per process.
func preludeBytecode() (*qjs.Bytecode, error) {
	preludeOnce.Do(func() {
		ctx := context.Background()
		q, err := qjs.New(ctx, qjs.Config{}, func(int32, string, string, float64) qjs.Reply { return qjs.Reply{Undefined: true} })
		if err != nil {
			preludeErr = fmt.Errorf("plugin: create VM: %w", err)
			return
		}
		defer q.Close()
		preludeBC, preludeErr = q.Compile(ctx, "cicord:sdk.js", preludeSource, true)
		if preludeErr != nil {
			preludeErr = fmt.Errorf("plugin: compile SDK prelude: %w", preludeErr)
		}
	})
	return preludeBC, preludeErr
}

// bundleCache keeps the bytecode of recently loaded bundles (Inspect then every instance
// of the plugin compile a bundle once). Bounded: `cicord dev` rebuilds on every change.
var bundleCache = struct {
	sync.Mutex
	m     map[[32]byte]*qjs.Bytecode
	order [][32]byte
}{m: map[[32]byte]*qjs.Bytecode{}}

const bundleCacheSize = 64

func cachedBundle(key [32]byte) *qjs.Bytecode {
	bundleCache.Lock()
	defer bundleCache.Unlock()
	return bundleCache.m[key]
}

func cacheBundle(key [32]byte, bc *qjs.Bytecode) {
	bundleCache.Lock()
	defer bundleCache.Unlock()
	if _, ok := bundleCache.m[key]; ok {
		return
	}
	if len(bundleCache.order) >= bundleCacheSize {
		delete(bundleCache.m, bundleCache.order[0])
		bundleCache.order = bundleCache.order[1:]
	}
	bundleCache.m[key] = bc
	bundleCache.order = append(bundleCache.order, key)
}

func (v *vm) close() { v.q.Close() }

// errorJSON describes a Go error for the prelude's mkErrorJSON (CapabilityError and
// TypeError when applicable).
func errorJSON(err error) string {
	var ce *CapabilityError
	info := struct {
		Name       string `json:"name"`
		Message    string `json:"message"`
		Capability string `json:"capability,omitempty"`
	}{Name: "Error", Message: err.Error()}
	if errors.As(err, &ce) {
		info.Capability, info.Message = ce.Capability, ce.Error()
	} else if errors.Is(err, errInvalidArgument) {
		info.Name = "TypeError"
	}
	b, _ := json.Marshal(info)
	return string(b)
}

func parseScriptError(js string) *ScriptError {
	var info struct {
		Name, Message, Stack, Capability string
	}
	if err := json.Unmarshal([]byte(js), &info); err != nil {
		return &ScriptError{Name: "Error", Message: js}
	}
	return &ScriptError{Name: info.Name, Message: info.Message, Stack: info.Stack, Capability: info.Capability}
}

// scriptError converts an errInfo JSON into a *ScriptError with TypeScript positions.
func (v *vm) scriptError(info string) *ScriptError {
	se := parseScriptError(info)
	se.Stack = v.mapStack(se.Stack)
	return se
}

// mapStack rewrites bundle positions in a stack trace to source positions.
func (v *vm) mapStack(stack string) string {
	if stack == "" || v.code == "" || !strings.Contains(stack, bundleName) {
		return stack
	}
	if v.smap == nil {
		v.smap = parseInlineSourceMap(v.code)
	}
	return v.smap.rewrite(stack, bundleName)
}

// asScriptError converts an exception returned by a VM call; other errors pass through.
func (v *vm) asScriptError(err error) error {
	var ex *qjs.Exception
	if errors.As(err, &ex) {
		return v.scriptError(ex.Info)
	}
	return err
}

// callAPI calls a prelude API function; exceptions become *ScriptError.
func (v *vm) callAPI(ctx context.Context, fn string, args ...string) (string, error) {
	r, _, err := v.q.Call(ctx, fn, args...)
	if err != nil {
		return "", v.asScriptError(err)
	}
	return r, nil
}

// runAPI is callAPI, and the microtasks it queues run before it returns (one wasm call).
func (v *vm) runAPI(ctx context.Context, fn string, args ...string) (string, error) {
	r, _, err := v.q.Run(ctx, fn, args...)
	if err != nil {
		return "", v.asScriptError(err)
	}
	return r, nil
}

// loadPlugin evaluates the bundle and checks its default export (kept by the prelude).
func (v *vm) loadPlugin(ctx context.Context, code string) error {
	v.code, v.smap = code, nil
	key := sha256.Sum256([]byte(code))
	bc := cachedBundle(key)
	if bc == nil {
		var err error
		if bc, err = v.q.Compile(ctx, bundleName, code, false); err != nil {
			return fmt.Errorf("plugin: evaluate module: %w", v.asScriptError(err))
		}
		cacheBundle(key, bc)
	}
	if err := v.q.EvalBytecode(ctx, bc); err != nil {
		return fmt.Errorf("plugin: evaluate module: %w", v.asScriptError(err))
	}
	st, err := v.callAPI(ctx, "loadPlugin", build.GlobalName)
	if err != nil {
		return fmt.Errorf("plugin: %w", err)
	}
	switch st {
	case "ok":
		return nil
	case "missing":
		return fmt.Errorf("plugin: the bundle does not define %s (was it built with internal/build?)", build.GlobalName)
	default:
		return fmt.Errorf("plugin: %w", errNoDefault)
	}
}

func (v *vm) definition(ctx context.Context) (*Definition, error) {
	s, err := v.callAPI(ctx, "describe")
	if err != nil {
		return nil, fmt.Errorf("plugin: describe definition: %w", err)
	}
	var dp describedPlugin
	if err := json.Unmarshal([]byte(s), &dp); err != nil {
		return nil, fmt.Errorf("plugin: describe definition: %w", err)
	}
	return buildDefinition(dp)
}

// ───────────────────────────────────────────────────────────── Inspect

// InspectBudget bounds the synchronous execution of a module during Inspect.
var InspectBudget = 5 * time.Second

type inspectHost struct{}

var errInspecting = errors.New("SDK calls are not available while the module is evaluated (use them inside handlers)")

func (inspectHost) call(string, string) (string, bool, error) { return "", false, errInspecting }
func (inspectHost) callAsync(string, string) (int64, error)   { return 0, errInspecting }
func (inspectHost) log(string, string, string)                {}
func (inspectHost) setTimer(float64, bool) (int64, error)     { return 0, errInspecting }
func (inspectHost) clearTimer(int64)                          {}
func (inspectHost) done(uint64, bool, string)                 {}
func (inspectHost) pauseBudget()                              {}
func (inspectHost) resumeBudget()                             {}

// Inspect loads the bundle (see internal/build) in a throwaway VM, evaluates
// `definePlugin(...)` and extracts the static definition. It validates command names,
// descriptions and options (Discord rules; required options first), event names, cron
// expressions, route keys and access levels, and reports every problem at once
// (*DefinitionError).
func Inspect(code string) (*Definition, error) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	v, err := newVM(ctx, inspectHost{}, 0)
	if err != nil {
		return nil, err
	}
	defer v.close()
	// Soft stop at the budget, hard stop (the VM is discarded anyway) a bit later.
	soft := time.AfterFunc(InspectBudget, v.q.Interrupt)
	defer soft.Stop()
	hard := time.AfterFunc(InspectBudget+hardStopGrace(InspectBudget), func() { cancel(ErrBudgetExceeded) })
	defer hard.Stop()
	if err := v.loadPlugin(ctx, code); err != nil {
		if errors.Is(err, qjs.ErrInterrupted) || errors.Is(err, qjs.ErrKilled) {
			return nil, fmt.Errorf("plugin: module evaluation exceeded %s: %w", InspectBudget, ErrBudgetExceeded)
		}
		if errors.Is(err, qjs.ErrOutOfMemory) {
			return nil, fmt.Errorf("plugin: module evaluation: %w", ErrOutOfMemory)
		}
		return nil, err
	}
	return v.definition(ctx)
}

// hardStopGrace is how long code may keep running past its budget (in a native builtin
// that does not check for interrupts) before the VM is killed.
func hardStopGrace(budget time.Duration) time.Duration {
	return max(budget, 500*time.Millisecond)
}

// ───────────────────────────────────────────────────────────── errors

// ScriptError is an error thrown by plugin code. Stack positions are mapped to the
// plugin's TypeScript sources.
type ScriptError struct {
	Name    string
	Message string
	Stack   string
	// Capability is set when the error is a CapabilityError.
	Capability string
}

func (e *ScriptError) Error() string {
	if e.Stack != "" {
		if strings.HasPrefix(e.Stack, e.Name) {
			return e.Stack
		}
		return e.Name + ": " + e.Message + "\n" + e.Stack
	}
	return e.Name + ": " + e.Message
}

// Unwrap exposes the CapabilityError, so errors.As(err, *CapabilityError) works.
func (e *ScriptError) Unwrap() error {
	if e.Capability != "" {
		return &CapabilityError{Capability: e.Capability}
	}
	return nil
}

// CapabilityError is returned (and thrown in JS as CapabilityError) when a call needs a
// capability that is not granted.
type CapabilityError struct{ Capability string }

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("missing capability %q: grant it to this plugin instance in bot.yaml", e.Capability)
}

// Sentinel errors.
var (
	ErrHandlerTimeout = errors.New("plugin: handler timed out")
	ErrBudgetExceeded = errors.New("plugin: synchronous execution budget exceeded")
	// ErrOutOfMemory: the instance reached Limits.Memory. The instance is discarded
	// (ErrCrashed).
	ErrOutOfMemory = errors.New("plugin: memory limit exceeded")
	// ErrCrashed: the instance's VM was discarded (memory limit, hard stop of code that
	// ignored its budget, engine trap). Every pending and later call fails; the bot host
	// restarts the instance (see Instance.Done).
	ErrCrashed         = errors.New("plugin: instance crashed")
	ErrStopped         = errors.New("plugin: instance stopped")
	ErrNotStarted      = errors.New("plugin: instance not started")
	ErrNoHandler       = errors.New("plugin: no handler")
	ErrBusy            = errors.New("plugin: too many queued calls")
	errInvalidArgument = errors.New("invalid argument")
)
