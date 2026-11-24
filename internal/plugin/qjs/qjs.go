// Package qjs runs QuickJS-ng, compiled to WebAssembly (quickjs.wasm, built by
// scripts/build-quickjs-wasm.sh from csrc/cicord.c), on wazero. It is the JavaScript engine
// of internal/plugin and is deliberately narrow: one VM evaluates scripts, calls the
// functions of the object returned by a prelude with string arguments, drains microtasks,
// and forwards the prelude's host.raw(kind, a, b, n) calls to a Go HostFunc.
//
// Isolation and limits, per VM:
//   - the guest has no WASI capabilities beyond clocks and random bytes (no files,
//     arguments, environment or output) and only reaches Go through HostFunc;
//   - the JS heap is capped (Config.MemoryLimit, enforced by the shim's allocator) and so
//     is the wasm linear memory (the limit plus a fixed margin): exceeding it puts the VM
//     in a dead state (ErrOutOfMemory) even if plugin code catches the JS error;
//   - Interrupt stops running JS at the next interrupt check (loops, calls and the long
//     native builtins of QuickJS-ng poll it); the call returns ErrInterrupted and the VM
//     stays usable;
//   - Kill, or cancelling the context of a call, stops it unconditionally: the guest traps
//     at its next kill check and the VM is dead afterwards (ErrKilled). This is the hard
//     stop for code that does not reach an interrupt check (a long native builtin): the
//     engine is built so that every C function entry counts down to a kill check (see
//     csrc/cicord.c). wazero's WithCloseOnContextDone would do the same at every wasm loop
//     iteration, at about 4 times the CPU cost;
//   - any wasm trap (stack exhaustion, assertion) kills the VM (ErrTrap). The Go process
//     is never affected.
//
// A VM is not safe for concurrent use: all calls, including from HostFunc, happen on the
// caller's goroutine. Interrupt may be called from any goroutine.
package qjs

import (
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed quickjs.wasm
var wasmBytes []byte

// Host call kinds handled by the VM itself; the others are the prelude's business.
const (
	KindPauseBudget  = 7
	KindResumeBudget = 8
)

// Result codes and flags of the shim (csrc/cicord.c).
const (
	cqOK          = 0
	cqString      = 1
	cqException   = 2
	cqInterrupted = 3
	cqFailed      = 4

	fOOM        = 1 << 8
	fJobs       = 1 << 9
	fRejections = 1 << 10
	fJobErr     = 1 << 11
)

// Defaults and fixed sizes.
const (
	DefaultMemoryLimit = 64 << 20
	// linkStack is the shadow stack size the module is linked with (build script).
	linkStack = 1 << 20
	// DefaultStackLimit is the JS stack limit, below linkStack so that QuickJS throws a
	// RangeError before the shadow stack overflows (which would trap).
	DefaultStackLimit = 768 << 10
	// memoryMargin is linear memory beyond the JS heap: shadow stack, static data and the
	// shim's own buffers (call arguments, host results).
	memoryMargin = 24 << 20
	pageSize     = 64 << 10
	maxCallArgs  = 5
)

// Errors.
var (
	// ErrInterrupted: the call was stopped by Interrupt. The VM is still usable.
	ErrInterrupted = errors.New("qjs: interrupted")
	// ErrOutOfMemory: the memory limit was reached. The VM is dead.
	ErrOutOfMemory = errors.New("qjs: out of memory")
	// ErrKilled: the call's context ended while JS was running (hard stop). The VM is dead.
	ErrKilled = errors.New("qjs: killed")
	// ErrTrap: the guest trapped (for example on stack exhaustion). The VM is dead.
	ErrTrap = errors.New("qjs: trap")
	// ErrClosed: the VM was closed.
	ErrClosed = errors.New("qjs: closed")
)

// Exception is a JS exception that escaped a call. Info is the JSON produced by the
// prelude's errInfo (or the exception's string form before the prelude is loaded).
type Exception struct{ Info string }

func (e *Exception) Error() string { return "qjs: exception: " + e.Info }

// Reply is what a HostFunc returns to host.raw: a string, undefined, or an error (the
// prelude receives {e: Error} and throws).
type Reply struct {
	Value     string
	Undefined bool
	Error     string // JSON, see the prelude
}

// HostFunc handles host.raw(kind, a, b, n) calls. a and b are "" when undefined.
type HostFunc func(kind int32, a, b string, n float64) Reply

// Config configures a VM. Zero fields take the defaults.
type Config struct {
	// MemoryLimit caps the JS heap in bytes. Default DefaultMemoryLimit.
	MemoryLimit int64
	// StackLimit caps the JS stack in bytes (at most 1 MiB minus a margin). Default
	// DefaultStackLimit.
	StackLimit int
}

// Answers of the interrupt import (csrc/cicord.c).
const (
	stateRun  = 0
	stateStop = 1
	stateKill = 2
)

// shared is one wazero runtime per linear memory limit (the page cap is a runtime
// setting); the compilation cache makes every runtime after the first cheap.
type shared struct {
	rt  wazero.Runtime
	mod wazero.CompiledModule
}

var (
	sharedMu sync.Mutex
	runtimes = map[uint32]*shared{}
	cache    = wazero.NewCompilationCache()
)

type vmKey struct{}

func sharedFor(ctx context.Context, pages uint32) (*shared, error) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if s := runtimes[pages]; s != nil {
		return s, nil
	}
	cfg := wazero.NewRuntimeConfig().
		WithCompilationCache(cache).

		WithMemoryLimitPages(pages).
		WithCoreFeatures(api.CoreFeaturesV2)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("qjs: instantiate WASI: %w", err)
	}
	i32, f64 := api.ValueTypeI32, api.ValueTypeF64
	_, err := rt.NewHostModuleBuilder("cicord").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostCall), []api.ValueType{i32, i32, i32, i32, i32, f64}, []api.ValueType{i32}).Export("host_call").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostTake), []api.ValueType{i32}, nil).Export("host_take").
		NewFunctionBuilder().WithGoFunction(api.GoFunc(hostInterrupt), nil, []api.ValueType{i32}).Export("interrupt").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(argsTake), []api.ValueType{i32}, nil).Export("args_take").
		Instantiate(ctx)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("qjs: host module: %w", err)
	}
	mod, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("qjs: compile quickjs.wasm: %w", err)
	}
	s := &shared{rt: rt, mod: mod}
	runtimes[pages] = s
	return s, nil
}

// Precompile compiles quickjs.wasm for the default limits, so the first VM does not pay
// for it. It is optional.
func Precompile(ctx context.Context) error {
	_, err := sharedFor(ctx, pagesFor(DefaultMemoryLimit))
	return err
}

func pagesFor(limit int64) uint32 {
	return uint32((limit + memoryMargin + pageSize - 1) / pageSize)
}

// VM is one QuickJS runtime and context in its own wasm instance.
type VM struct {
	mod  api.Module
	host HostFunc

	fnInit, fnPrelude, fnEval, fnCall, fnJobs, fnTakeRej, fnMem, fnGC api.Function
	fnCompile, fnPreludeBC, fnEvalBC                                 api.Function
	outAddr                                                          uint32

	state       atomic.Int32 // stateRun, stateStop (Interrupt) or stateKill
	pending     []byte // host result waiting for host_take
	args        []byte // call arguments waiting for args_take
	flags       uint32
	jobErr      error
	dead        error
}

// New instantiates a VM.
func New(ctx context.Context, cfg Config, host HostFunc) (*VM, error) {
	if cfg.MemoryLimit <= 0 {
		cfg.MemoryLimit = DefaultMemoryLimit
	}
	if cfg.MemoryLimit > 1<<30 {
		return nil, errors.New("qjs: MemoryLimit above 1 GiB")
	}
	if cfg.StackLimit <= 0 {
		cfg.StackLimit = DefaultStackLimit
	}
	if cfg.StackLimit > linkStack-128<<10 {
		cfg.StackLimit = linkStack - 128<<10
	}
	s, err := sharedFor(ctx, pagesFor(cfg.MemoryLimit))
	if err != nil {
		return nil, err
	}
	v := &VM{host: host}
	mcfg := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithSysWalltime().
		WithSysNanotime().
		WithRandSource(rand.Reader)
	mod, err := s.rt.InstantiateModule(v.ctx(ctx), s.mod, mcfg)
	if err != nil {
		return nil, fmt.Errorf("qjs: instantiate: %w", err)
	}
	v.mod = mod
	get := func(name string) api.Function {
		f := mod.ExportedFunction(name)
		if f == nil && err == nil {
			err = fmt.Errorf("qjs: quickjs.wasm does not export %s", name)
		}
		return f
	}
	v.fnInit, v.fnPrelude, v.fnEval = get("cq_init"), get("cq_prelude"), get("cq_eval")
	v.fnCall, v.fnJobs, v.fnTakeRej, v.fnMem, v.fnGC = get("cq_call"), get("cq_jobs"), get("cq_take_rejections"), get("cq_mem"), get("cq_gc")
	v.fnCompile, v.fnPreludeBC, v.fnEvalBC = get("cq_compile"), get("cq_prelude_bc"), get("cq_eval_bc")
	state := get("cq_state")
	if err != nil {
		v.Close()
		return nil, err
	}
	r, err := state.Call(v.ctx(ctx))
	if err != nil {
		v.Close()
		return nil, fmt.Errorf("qjs: cq_state: %w", err)
	}
	v.outAddr = uint32(r[0])
	code, err := v.call(ctx, v.fnInit, uint64(cfg.MemoryLimit), uint64(cfg.StackLimit))
	if err == nil && code != cqOK {
		err = errors.New("qjs: cannot create the JS runtime")
	}
	if err != nil {
		v.Close()
		return nil, err
	}
	return v, nil
}

func (v *VM) ctx(ctx context.Context) context.Context { return context.WithValue(ctx, vmKey{}, v) }

// Close releases the VM. It is idempotent.
func (v *VM) Close() {
	if v.mod != nil {
		_ = v.mod.Close(context.Background())
		v.mod = nil
	}
	if v.dead == nil {
		v.dead = ErrClosed
	}
}

// Dead returns why the VM can no longer be used (nil while it can).
func (v *VM) Dead() error { return v.dead }

// Interrupt asks the running (or next) call to stop at its next interrupt check. Safe
// for concurrent use.
func (v *VM) Interrupt() { v.state.CompareAndSwap(stateRun, stateStop) }

// ClearInterrupt cancels a pending Interrupt (not a Kill).
func (v *VM) ClearInterrupt() { v.state.CompareAndSwap(stateStop, stateRun) }

// Kill stops the running (or next) call unconditionally; the VM is dead afterwards. Safe
// for concurrent use.
func (v *VM) Kill() { v.state.Store(stateKill) }

// JobsPending reports that microtasks were pending after the last call.
func (v *VM) JobsPending() bool { return v.flags&fJobs != 0 }

// Rejections reports that the prelude recorded unhandled rejections; it clears the flag.
func (v *VM) Rejections() bool {
	if v.flags&fRejections == 0 {
		return false
	}
	v.flags &^= fRejections
	if v.dead == nil {
		_, _ = v.fnTakeRej.Call(context.Background())
	}
	return true
}

// MemoryUsed is the JS heap in use, in bytes.
func (v *VM) MemoryUsed() int64 {
	if v.dead != nil {
		return 0
	}
	r, err := v.fnMem.Call(context.Background())
	if err != nil {
		return 0
	}
	return int64(uint32(r[0]))
}

// GC runs the garbage collector.
func (v *VM) GC(ctx context.Context) error {
	_, err := v.call(ctx, v.fnGC)
	return err
}

// call invokes a shim export and decodes its status word.
func (v *VM) call(ctx context.Context, fn api.Function, params ...uint64) (int, error) {
	if v.dead != nil {
		return 0, v.dead
	}
	if ctx.Done() != nil {
		stop := context.AfterFunc(ctx, v.Kill)
		defer stop()
	}
	r, err := fn.Call(v.ctx(ctx), params...)
	if err != nil {
		if v.state.Load() == stateKill {
			cause := context.Cause(ctx)
			if cause == nil {
				cause = errors.New("killed")
			}
			v.dead = fmt.Errorf("%w: %w", ErrKilled, cause)
		} else {
			v.dead = fmt.Errorf("%w: %w", ErrTrap, err)
		}
		v.closeModule()
		return 0, v.dead
	}
	if len(r) == 0 {
		return cqOK, nil
	}
	st := uint32(r[0])
	v.flags = st
	if st&fOOM != 0 {
		v.dead = ErrOutOfMemory
		v.closeModule()
		return 0, v.dead
	}
	return int(st & 0xff), nil
}

func (v *VM) closeModule() {
	if v.mod != nil {
		_ = v.mod.Close(context.Background())
		v.mod = nil
	}
}

// out reads the shim's output string.
func (v *VM) out() string {
	mem := v.mod.Memory()
	ptr, ok1 := mem.ReadUint32Le(v.outAddr)
	n, ok2 := mem.ReadUint32Le(v.outAddr + 4)
	if !ok1 || !ok2 || ptr == 0 {
		return ""
	}
	b, ok := mem.Read(ptr, n)
	if !ok {
		return ""
	}
	return string(b)
}

// jobOut reads the errInfo of the last exception that escaped a job.
func (v *VM) jobOut() string {
	mem := v.mod.Memory()
	ptr, ok1 := mem.ReadUint32Le(v.outAddr + 8)
	n, ok2 := mem.ReadUint32Le(v.outAddr + 12)
	if !ok1 || !ok2 || ptr == 0 {
		return ""
	}
	b, ok := mem.Read(ptr, n)
	if !ok {
		return ""
	}
	return string(b)
}

// TakeJobError returns (and clears) the last exception that escaped a microtask during
// Run or RunJobs; the drain went on after it.
func (v *VM) TakeJobError() error {
	err := v.jobErr
	v.jobErr = nil
	return err
}

// status converts a result code into (string result, defined, error).
func (v *VM) status(code int) (string, bool, error) {
	switch code {
	case cqOK:
		return "", false, nil
	case cqString:
		return v.out(), true, nil
	case cqException:
		return "", false, &Exception{Info: v.out()}
	case cqInterrupted:
		return "", false, ErrInterrupted
	default:
		msg := v.out()
		if msg == "" {
			msg = "shim call failed"
		}
		return "", false, errors.New("qjs: " + msg)
	}
}

// setArgs stages parts back to back for the next call (pulled by args_take) and returns
// their lengths.
func (v *VM) setArgs(parts ...string) ([]uint64, error) {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	if total > 1<<30 {
		return nil, errors.New("qjs: argument too large")
	}
	if v.dead != nil {
		return nil, v.dead
	}
	buf := make([]byte, 0, total)
	lens := make([]uint64, len(parts))
	for i, p := range parts {
		buf = append(buf, p...)
		lens[i] = uint64(len(p))
	}
	v.args = buf
	return lens, nil
}

// Prelude evaluates src, an expression evaluating to function(host), calls it with the
// host object and keeps the object it returns: Call invokes its functions.
func (v *VM) Prelude(ctx context.Context, name, src string) error {
	return v.script(ctx, v.fnPrelude, name, src)
}

// Eval evaluates a global (sloppy mode) script.
func (v *VM) Eval(ctx context.Context, name, src string) error {
	return v.script(ctx, v.fnEval, name, src)
}

// Bytecode is a script compiled by Compile. It can only be run by VMs of the same build
// (the embedded quickjs.wasm) and must never come from anywhere else: QuickJS does not
// validate bytecode.
type Bytecode struct{ b string }

// Compile compiles a global script without running it (strict: as with Prelude, else as
// with Eval). Compiling once and running the bytecode in many VMs is much cheaper than
// evaluating the source in each.
func (v *VM) Compile(ctx context.Context, name, src string, strict bool) (*Bytecode, error) {
	lens, err := v.setArgs(src, "\x00", name)
	if err != nil {
		return nil, err
	}
	s := uint64(0)
	if strict {
		s = 1
	}
	code, err := v.call(ctx, v.fnCompile, lens[0], lens[2], s)
	if err != nil {
		return nil, err
	}
	out, _, err := v.status(code)
	if err != nil {
		return nil, err
	}
	return &Bytecode{b: out}, nil
}

// PreludeBytecode is Prelude for a compiled prelude (Compile with strict set).
func (v *VM) PreludeBytecode(ctx context.Context, bc *Bytecode) error {
	return v.bytecode(ctx, v.fnPreludeBC, bc)
}

// EvalBytecode is Eval for a compiled script.
func (v *VM) EvalBytecode(ctx context.Context, bc *Bytecode) error {
	return v.bytecode(ctx, v.fnEvalBC, bc)
}

func (v *VM) bytecode(ctx context.Context, fn api.Function, bc *Bytecode) error {
	lens, err := v.setArgs(bc.b)
	if err != nil {
		return err
	}
	code, err := v.call(ctx, fn, lens[0])
	if err != nil {
		return err
	}
	_, _, err = v.status(code)
	return err
}

func (v *VM) script(ctx context.Context, fn api.Function, name, src string) error {
	lens, err := v.setArgs(src, "\x00", name)
	if err != nil {
		return err
	}
	code, err := v.call(ctx, fn, lens[0], lens[2])
	if err != nil {
		return err
	}
	_, _, err = v.status(code)
	return err
}

// Call calls fn (a function of the prelude's object) with string arguments (at most 5)
// and returns its result converted to a string (defined is false for undefined/null).
// Microtasks queued by the call are not run: see RunJobs.
func (v *VM) Call(ctx context.Context, fn string, args ...string) (result string, defined bool, err error) {
	return v.invoke(ctx, false, fn, args)
}

// Run is Call followed by RunJobs, in a single wasm call (cheaper): the microtasks queued
// by the call run before it returns. ErrInterrupted can come from either part; an
// exception escaping a microtask is kept for TakeJobError.
func (v *VM) Run(ctx context.Context, fn string, args ...string) (result string, defined bool, err error) {
	return v.invoke(ctx, true, fn, args)
}

func (v *VM) invoke(ctx context.Context, drain bool, fn string, args []string) (string, bool, error) {
	if len(args) > maxCallArgs {
		return "", false, fmt.Errorf("qjs: at most %d arguments", maxCallArgs)
	}
	lens, err := v.setArgs(append([]string{fn}, args...)...)
	if err != nil {
		return "", false, err
	}
	params := make([]uint64, 8)
	params[0] = uint64(len(args))
	copy(params[1:], lens)
	if drain {
		params[7] = 1
	}
	code, err := v.call(ctx, v.fnCall, params...)
	if err != nil {
		return "", false, err
	}
	if v.flags&fJobErr != 0 {
		v.jobErr = &Exception{Info: v.jobOut()}
	}
	return v.status(code)
}

// RunJobs runs pending microtasks until none is left. It returns ErrInterrupted when the
// drain was interrupted (jobs may remain), or an *Exception when an error escaped a job
// (QuickJS-ng does not turn uncatchable errors into rejections); the drain went on.
func (v *VM) RunJobs(ctx context.Context) error {
	if v.dead == nil && v.flags&fJobs == 0 {
		return nil // the last call reported no pending job
	}
	code, err := v.call(ctx, v.fnJobs)
	if err != nil {
		return err
	}
	if code == cqException {
		return &Exception{Info: v.jobOut()}
	}
	_, _, err = v.status(code)
	return err
}

// ───────────────────────────────────────────────────────────── host imports

func vmOf(ctx context.Context) *VM {
	v, _ := ctx.Value(vmKey{}).(*VM)
	return v
}

func readString(m api.Module, ptr, n uint32) string {
	if n == 0 {
		return ""
	}
	b, ok := m.Memory().Read(ptr, n)
	if !ok {
		return ""
	}
	return string(b) // copies
}

func hostCall(ctx context.Context, m api.Module, stack []uint64) {
	v := vmOf(ctx)
	kind := api.DecodeI32(stack[0])
	a := readString(m, api.DecodeU32(stack[1]), api.DecodeU32(stack[2]))
	b := readString(m, api.DecodeU32(stack[3]), api.DecodeU32(stack[4]))
	n := api.DecodeF64(stack[5])
	if v == nil {
		stack[0] = api.EncodeI32(-1)
		return
	}
	r := v.host(kind, a, b, n)
	switch {
	case r.Error != "":
		v.pending = []byte(r.Error)
		stack[0] = api.EncodeI32(int32(-len(v.pending) - 2)) //nolint:gosec // host results are bounded far below 2 GiB
	case r.Undefined:
		v.pending = nil
		stack[0] = api.EncodeI32(-1)
	default:
		v.pending = []byte(r.Value)
		stack[0] = api.EncodeI32(int32(len(v.pending))) //nolint:gosec // host results are bounded far below 2 GiB
	}
}

func hostTake(ctx context.Context, m api.Module, stack []uint64) {
	v := vmOf(ctx)
	if v == nil {
		return
	}
	m.Memory().Write(api.DecodeU32(stack[0]), v.pending)
	v.pending = nil
}

func argsTake(ctx context.Context, m api.Module, stack []uint64) {
	v := vmOf(ctx)
	if v == nil {
		return
	}
	m.Memory().Write(api.DecodeU32(stack[0]), v.args)
	v.args = nil
}

func hostInterrupt(ctx context.Context, stack []uint64) {
	v := vmOf(ctx)
	if v == nil {
		stack[0] = 0
		return
	}
	stack[0] = api.EncodeI32(v.state.Load())
}
