package plugin

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/grafana/sobek"

	"github.com/cicord/cicord/internal/build"
)

//go:embed sdk.js
var preludeSource string

var (
	preludeOnce sync.Once
	preludeProg *sobek.Program
	preludeErr  error
)

func preludeProgram() (*sobek.Program, error) {
	preludeOnce.Do(func() {
		preludeProg, preludeErr = sobek.Compile("cicord:sdk.js", preludeSource, true)
	})
	return preludeProg, preludeErr
}

// bundleName is the script name used for plugin code; positions are mapped back to the
// TypeScript sources through the bundle's inline source map.
const bundleName = "plugin.bundle.js"

// host is what the prelude calls into. Inspect uses a host that refuses everything.
type host interface {
	call(op, args string) (sobek.Value, error)
	callAsync(op, args string) (sobek.Value, error)
	log(level, msg, fields string)
	setTimer(fn sobek.Callable, ms float64, repeat bool) (int64, error)
	clearTimer(id int64)
	// unbudgeted runs bounded host-side work outside the sync budget.
	unbudgeted(f func())
}

// vm is a Sobek runtime with the SDK prelude loaded.
type vm struct {
	rt *sobek.Runtime

	mkError   sobek.Callable
	errInfo   sobek.Callable
	describe  sobek.Callable
	load      sobek.Callable
	setup     sobek.Callable
	dispatchF sobek.Callable
}

func newVM(h host) (*vm, error) {
	prg, err := preludeProgram()
	if err != nil {
		return nil, fmt.Errorf("plugin: compile SDK prelude: %w", err)
	}
	rt := sobek.New()
	rt.SetMaxCallStackSize(4096)
	v := &vm{rt: rt}

	fnV, err := rt.RunProgram(prg)
	if err != nil {
		return nil, fmt.Errorf("plugin: run SDK prelude: %w", err)
	}
	fn, ok := sobek.AssertFunction(fnV)
	if !ok {
		return nil, errors.New("plugin: SDK prelude is not a function")
	}

	jsonParse, ok := sobek.AssertFunction(rt.Get("JSON").ToObject(rt).Get("parse"))
	if !ok {
		return nil, errors.New("plugin: JSON.parse missing")
	}
	hostObj := rt.NewObject()
	throw := func(err error) { panic(v.jsError(err)) }
	// parseJSON parses host-produced JSON (no reviver: no plugin code runs) outside the sync
	// budget, so large but bounded payloads (fetch bodies up to MaxResponseBytes, KV values)
	// do not count as runaway code.
	_ = hostObj.Set("parseJSON", func(c sobek.FunctionCall) sobek.Value {
		var (
			res sobek.Value
			err error
		)
		h.unbudgeted(func() { res, err = jsonParse(sobek.Undefined(), c.Argument(0)) })
		if err != nil {
			panic(err)
		}
		return res
	})
	_ = hostObj.Set("call", func(c sobek.FunctionCall) sobek.Value {
		res, err := h.call(c.Argument(0).String(), c.Argument(1).String())
		if err != nil {
			throw(err)
		}
		return res
	})
	_ = hostObj.Set("callAsync", func(c sobek.FunctionCall) sobek.Value {
		res, err := h.callAsync(c.Argument(0).String(), c.Argument(1).String())
		if err != nil {
			throw(err)
		}
		return res
	})
	_ = hostObj.Set("log", func(c sobek.FunctionCall) sobek.Value {
		h.log(c.Argument(0).String(), c.Argument(1).String(), c.Argument(2).String())
		return sobek.Undefined()
	})
	_ = hostObj.Set("setTimer", func(c sobek.FunctionCall) sobek.Value {
		cb, ok := sobek.AssertFunction(c.Argument(0))
		if !ok {
			panic(rt.NewTypeError("timer callback must be a function"))
		}
		id, err := h.setTimer(cb, c.Argument(1).ToFloat(), c.Argument(2).ToBoolean())
		if err != nil {
			throw(err)
		}
		return rt.ToValue(id)
	})
	_ = hostObj.Set("clearTimer", func(c sobek.FunctionCall) sobek.Value {
		h.clearTimer(c.Argument(0).ToInteger())
		return sobek.Undefined()
	})

	apiV, err := fn(sobek.Undefined(), hostObj)
	if err != nil {
		return nil, fmt.Errorf("plugin: init SDK prelude: %w", err)
	}
	api := apiV.ToObject(rt)
	get := func(name string) sobek.Callable {
		f, ok := sobek.AssertFunction(api.Get(name))
		if !ok {
			panic("plugin: SDK prelude does not export " + name)
		}
		return f
	}
	v.mkError = get("mkError")
	v.errInfo = get("errInfo")
	v.describe = get("describe")
	v.load = get("loadPlugin")
	v.setup = get("setup")
	v.dispatchF = get("dispatch")
	return v, nil
}

// jsError converts a Go error into a JS error value (CapabilityError when applicable).
func (v *vm) jsError(err error) sobek.Value {
	var ce *CapabilityError
	name, capability := "Error", ""
	if errors.As(err, &ce) {
		capability = ce.Capability
	} else if errors.Is(err, errInvalidArgument) {
		name = "TypeError"
	}
	msg := err.Error()
	if ce != nil {
		msg = ce.Error()
	}
	val, cerr := v.mkError(sobek.Undefined(), v.rt.ToValue(name), v.rt.ToValue(msg), v.rt.ToValue(capability))
	if cerr != nil {
		return v.rt.NewGoError(err)
	}
	return val
}

// scriptError converts a JS error (errInfo JSON or an exception) to a *ScriptError.
func (v *vm) scriptErrorFromValue(val sobek.Value) *ScriptError {
	s, err := v.errInfo(sobek.Undefined(), val)
	if err != nil {
		return &ScriptError{Name: "Error", Message: val.String()}
	}
	return parseScriptError(s.String())
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

// asScriptError converts an error returned by a Sobek call.
func (v *vm) asScriptError(err error) error {
	var ex *sobek.Exception
	if errors.As(err, &ex) {
		se := v.scriptErrorFromValue(ex.Value())
		if se.Stack == "" {
			se.Stack = ex.String()
		}
		return se
	}
	return err
}

// loadPlugin evaluates the bundle and returns the definePlugin() object.
func (v *vm) loadPlugin(code string) (*sobek.Object, error) {
	prg, err := sobek.Compile(bundleName, code, false)
	if err != nil {
		return nil, fmt.Errorf("plugin: compile bundle: %w", err)
	}
	if _, err := v.rt.RunProgram(prg); err != nil {
		return nil, fmt.Errorf("plugin: evaluate module: %w", v.asScriptError(err))
	}
	exp := v.rt.Get(build.GlobalName)
	if exp == nil || sobek.IsUndefined(exp) || sobek.IsNull(exp) {
		return nil, fmt.Errorf("plugin: the bundle does not define %s (was it built with internal/build?)", build.GlobalName)
	}
	defV, err := v.load(sobek.Undefined(), exp)
	if err != nil {
		return nil, fmt.Errorf("plugin: %w", v.asScriptError(err))
	}
	if sobek.IsUndefined(defV) {
		return nil, fmt.Errorf("plugin: %w", errNoDefault)
	}
	return defV.ToObject(v.rt), nil
}

func (v *vm) definition(def *sobek.Object) (*Definition, error) {
	s, err := v.describe(sobek.Undefined(), def)
	if err != nil {
		return nil, fmt.Errorf("plugin: describe definition: %w", v.asScriptError(err))
	}
	var dp describedPlugin
	if err := json.Unmarshal([]byte(s.String()), &dp); err != nil {
		return nil, fmt.Errorf("plugin: describe definition: %w", err)
	}
	return buildDefinition(dp)
}

// ───────────────────────────────────────────────────────────── Inspect

// InspectBudget bounds the synchronous execution of a module during Inspect.
var InspectBudget = 5 * time.Second

type inspectHost struct{}

var errInspecting = errors.New("SDK calls are not available while the module is evaluated (use them inside handlers)")

func (inspectHost) call(string, string) (sobek.Value, error)      { return nil, errInspecting }
func (inspectHost) callAsync(string, string) (sobek.Value, error) { return nil, errInspecting }
func (inspectHost) log(string, string, string)                    {}
func (inspectHost) setTimer(sobek.Callable, float64, bool) (int64, error) {
	return 0, errInspecting
}
func (inspectHost) clearTimer(int64)    {}
func (inspectHost) unbudgeted(f func()) { f() }

// Inspect loads the bundle (see internal/build) in a throwaway VM, evaluates
// `definePlugin(...)` and extracts the static definition. It validates command names,
// descriptions and options (Discord rules; required options first), event names, cron
// expressions, route keys and access levels, and reports every problem at once
// (*DefinitionError).
func Inspect(code string) (*Definition, error) {
	v, err := newVM(inspectHost{})
	if err != nil {
		return nil, err
	}
	t := time.AfterFunc(InspectBudget, func() { v.rt.Interrupt(ErrBudgetExceeded) })
	defer t.Stop()
	def, err := v.loadPlugin(code)
	if err != nil {
		if isInterrupt(err) {
			return nil, fmt.Errorf("plugin: module evaluation exceeded %s: %w", InspectBudget, ErrBudgetExceeded)
		}
		return nil, err
	}
	return v.definition(def)
}

func isInterrupt(err error) bool {
	var ie *sobek.InterruptedError
	return errors.As(err, &ie)
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
	ErrHandlerTimeout  = errors.New("plugin: handler timed out")
	ErrBudgetExceeded  = errors.New("plugin: synchronous execution budget exceeded")
	ErrStopped         = errors.New("plugin: instance stopped")
	ErrNotStarted      = errors.New("plugin: instance not started")
	ErrNoHandler       = errors.New("plugin: no handler")
	errInvalidArgument = errors.New("invalid argument")
)
