// Cicord's QuickJS-ng shim, compiled with QuickJS-ng to wasm32-wasi (reactor) by
// scripts/build-quickjs-wasm.sh and driven by internal/plugin/qjs (wazero).
//
// The guest is a single JS runtime + context with the standard intrinsics only: no
// quickjs-libc (no std/os modules, no files, no stdout). Everything the plugin can reach
// goes through one native function, host.raw(kind, a, b, n), which forwards to the Go
// import cicord.host_call. Go never re-enters the guest from an import: results are
// copied with cicord.host_take into a buffer the guest allocated.
//
// Go -> guest calls (cq_*) all return a status word: the low byte is the result code
// (CQ_*), the upper bits are flags (CQ_F_*). Their string arguments are pulled with
// cicord.args_take (one wasm call per turn: wazero starts a watcher goroutine per call).
// Output strings are described by the `out` struct, whose address Go reads once
// (cq_state).
//
// CPU: QuickJS's interrupt handler asks Go (cicord.interrupt) whether to stop: 1 stops the
// JS code with an uncatchable error (soft stop, the VM stays usable), 2 kills the VM (the
// guest traps). Native builtins do not all reach the interrupt handler, so the whole
// engine is also built with -finstrument-function-entry-bare: every C function entry
// counts down to a kill check, and a builtin stuck in a long loop traps within a few
// thousand calls of the kill (see scripts/build-quickjs-wasm.sh).
//
// Memory: JS allocations go through malloc functions that enforce the limit given to
// cq_init and set a sticky out-of-memory flag when it is hit (plugin code can catch the
// RangeError/InternalError QuickJS throws, the flag cannot be cleared). Any other
// allocation failure sets the same flag. The interrupt handler stops the code as soon as
// the flag is set.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <malloc.h>

#include "quickjs.h"

#define IMPORT(name) __attribute__((import_module("cicord"), import_name(name)))
#define EXPORT(name) __attribute__((export_name(name)))

// host_call returns: >= 0 a result of that many bytes (take it with host_take);
// -1 undefined; <= -2 an error, JSON of (-r - 2) bytes (take it with host_take).
IMPORT("host_call") int32_t host_call(int32_t kind, const char *a, int32_t alen, const char *b, int32_t blen, double n);
IMPORT("host_take") void host_take(char *dst);
IMPORT("interrupt") int32_t host_interrupt(void);
// args_take copies the pending call arguments (as many bytes as the call announced).
IMPORT("args_take") void args_take(char *dst);

enum {
	CQ_OK = 0,          // success, result is undefined
	CQ_STRING = 1,      // success, out holds the result string
	CQ_EXCEPTION = 2,   // a JS exception, out holds its errInfo JSON
	CQ_INTERRUPTED = 3, // interrupted (uncatchable); out may hold details
	CQ_FAILED = 4,      // the shim itself failed (bad call, allocation failure)
};
enum {
	CQ_F_OOM = 1 << 8,         // the memory limit was hit (sticky)
	CQ_F_JOBS = 1 << 9,        // jobs (microtasks) are pending
	CQ_F_REJECTIONS = 1 << 10, // unhandled rejections are waiting in the prelude
	CQ_F_JOBERR = 1 << 11,     // an exception escaped a job (out.jptr holds its errInfo)
};

// Host call kinds (keep in sync with internal/plugin/qjs).
enum {
	K_PAUSE = 7,  // suspend the sync budget (host-produced JSON parsing)
	K_RESUME = 8, // resume it
};

static struct {
	char *ptr;
	int32_t len;
	char *jptr; // last exception that escaped a job
	int32_t jlen;
} out;

// Answers of cicord.interrupt.
enum { I_RUN = 0, I_STOP = 1, I_KILL = 2 };

// Function entries between two kill checks.
#define KILL_CHECK_EVERY 4096

static int32_t kill_countdown = KILL_CHECK_EVERY;

__attribute__((no_instrument_function)) void __cyg_profile_func_enter_bare(void) {
	if (--kill_countdown > 0)
		return;
	kill_countdown = KILL_CHECK_EVERY;
	if (host_interrupt() == I_KILL)
		__builtin_trap();
}

static JSRuntime *rt;
static JSContext *ctx;
static JSValue api = JS_UNDEFINED; // object returned by the prelude
static JSValue on_rejection = JS_UNDEFINED;

static size_t mem_limit, mem_used;
static int oom, rejections;
// fired is set when the interrupt handler stopped the code during the current entry
// (QuickJS-ng makes every InternalError uncatchable, not only interrupts).
static int fired;

// ───────────────────────────────────────────────────────────── memory

static void *m_malloc(void *opaque, size_t size) {
	(void)opaque;
	if (oom || (mem_limit && mem_used + size > mem_limit)) {
		oom = 1;
		return NULL;
	}
	void *p = malloc(size);
	if (!p) {
		oom = 1;
		return NULL;
	}
	mem_used += malloc_usable_size(p);
	return p;
}

static void *m_calloc(void *opaque, size_t count, size_t size) {
	if (size && count > SIZE_MAX / size) {
		oom = 1;
		return NULL;
	}
	void *p = m_malloc(opaque, count * size);
	if (p)
		memset(p, 0, count * size);
	return p;
}

static void m_free(void *opaque, void *ptr) {
	(void)opaque;
	if (!ptr)
		return;
	mem_used -= malloc_usable_size(ptr);
	free(ptr);
}

static void *m_realloc(void *opaque, void *ptr, size_t size) {
	if (!ptr)
		return m_malloc(opaque, size);
	if (size == 0) {
		m_free(opaque, ptr);
		return NULL;
	}
	size_t old = malloc_usable_size(ptr);
	if (oom || (mem_limit && size > old && mem_used + (size - old) > mem_limit)) {
		oom = 1;
		return NULL;
	}
	void *p = realloc(ptr, size);
	if (!p) {
		oom = 1;
		return NULL;
	}
	mem_used = mem_used - old + malloc_usable_size(p);
	return p;
}

static size_t m_usable(const void *ptr) { return malloc_usable_size((void *)ptr); }

static const JSMallocFunctions mfuncs = {m_calloc, m_malloc, m_free, m_realloc, m_usable};

// buf is the shared Go -> guest argument buffer (Go writes into it, then calls).
static char *buf;
static size_t buf_cap;

// take_args pulls n bytes of call arguments into buf (plus room for a terminator).
static bool take_args(size_t n) {
	if (n + 1 > buf_cap) {
		char *b = realloc(buf, n + 1);
		if (!b) {
			oom = 1;
			return false;
		}
		buf = b;
		buf_cap = n + 1;
	}
	args_take(buf);
	buf[n] = 0;
	return true;
}

EXPORT("cq_state") void *cq_state(void) { return &out; }

// ───────────────────────────────────────────────────────────── status

static void set_out(char *p, int32_t len) {
	free(out.ptr);
	out.ptr = p;
	out.len = len;
}

static int32_t flags(int32_t code) {
	int32_t f = code;
	if (oom)
		f |= CQ_F_OOM;
	if (rt && JS_IsJobPending(rt))
		f |= CQ_F_JOBS;
	if (rejections)
		f |= CQ_F_REJECTIONS;
	return f;
}

// copy_out stores a JS string in out (CQ_STRING) or reports a failure.
static int32_t copy_out(JSValueConst v) {
	size_t len;
	const char *s = JS_ToCStringLen(ctx, &len, v);
	if (!s) {
		JSValue e = JS_GetException(ctx);
		JS_FreeValue(ctx, e);
		return CQ_FAILED;
	}
	char *p = malloc(len + 1);
	if (!p) {
		JS_FreeCString(ctx, s);
		oom = 1;
		return CQ_FAILED;
	}
	memcpy(p, s, len);
	p[len] = 0;
	JS_FreeCString(ctx, s);
	set_out(p, (int32_t)len);
	return CQ_STRING;
}

static void out_literal(const char *s) {
	size_t len = strlen(s);
	char *p = malloc(len + 1);
	if (!p) {
		set_out(NULL, 0);
		return;
	}
	memcpy(p, s, len + 1);
	set_out(p, (int32_t)len);
}

// exception turns the pending exception into a status (out: errInfo JSON).
static int32_t exception(void) {
	JSValue e = JS_GetException(ctx);
	if (fired && JS_IsUncatchableError(e)) {
		JS_FreeValue(ctx, e);
		out_literal("{\"name\":\"InternalError\",\"message\":\"interrupted\"}");
		return CQ_INTERRUPTED;
	}
	int32_t code = CQ_EXCEPTION;
	JSValue fn = JS_IsUndefined(api) ? JS_UNDEFINED : JS_GetPropertyStr(ctx, api, "errInfo");
	if (JS_IsFunction(ctx, fn)) {
		JSValue info = JS_Call(ctx, fn, JS_UNDEFINED, 1, (JSValueConst *)&e);
		if (JS_IsException(info)) {
			JSValue e2 = JS_GetException(ctx);
			JS_FreeValue(ctx, e2);
			out_literal("{\"name\":\"Error\",\"message\":\"unserializable error\"}");
			code = fired ? CQ_INTERRUPTED : CQ_EXCEPTION;
		} else if (copy_out(info) != CQ_STRING) {
			out_literal("{\"name\":\"Error\",\"message\":\"unserializable error\"}");
		}
		JS_FreeValue(ctx, info);
	} else {
		// Before the prelude is up: the message is all we have.
		JSValue s = JS_ToString(ctx, e);
		if (JS_IsException(s) || copy_out(s) != CQ_STRING) {
			JSValue e2 = JS_GetException(ctx);
			JS_FreeValue(ctx, e2);
			out_literal("error");
		}
		JS_FreeValue(ctx, s);
	}
	JS_FreeValue(ctx, fn);
	JS_FreeValue(ctx, e);
	return code;
}

// result turns a call result into a status (consumes v).
static int32_t result(JSValue v) {
	if (JS_IsException(v))
		return exception();
	int32_t code = CQ_OK;
	if (JS_IsString(v))
		code = copy_out(v);
	else if (!JS_IsUndefined(v) && !JS_IsNull(v)) {
		JSValue s = JS_ToString(ctx, v);
		code = JS_IsException(s) ? exception() : copy_out(s);
		JS_FreeValue(ctx, s);
	}
	JS_FreeValue(ctx, v);
	return code;
}

// ───────────────────────────────────────────────────────────── host object

static int interrupt_handler(JSContext *c, void *opaque) {
	(void)c;
	(void)opaque;
	int32_t r = host_interrupt();
	if (r == I_KILL)
		__builtin_trap();
	if (oom || r) {
		fired = 1;
		return 1;
	}
	return 0;
}

static void rejection_tracker(JSContext *c, JSValueConst promise, JSValueConst reason, bool is_handled, void *opaque) {
	(void)opaque;
	if (!JS_IsFunction(c, on_rejection))
		return;
	JSValueConst args[3] = {promise, reason, JS_NewBool(c, is_handled)};
	JSValue r = JS_Call(c, on_rejection, JS_UNDEFINED, 3, args);
	if (JS_IsException(r)) {
		JSValue e = JS_GetException(c);
		if (fired)
			JS_Throw(c, e); // keep the interrupt going
		else
			JS_FreeValue(c, e);
	} else {
		if (JS_ToBool(c, r))
			rejections = 1;
		JS_FreeValue(c, r);
	}
}

// take reads a pending host result of n bytes into a new JS string.
static JSValue take_string(JSContext *c, int32_t n) {
	char *p = malloc((size_t)n + 1);
	if (!p) {
		oom = 1;
		return JS_ThrowOutOfMemory(c);
	}
	host_take(p);
	JSValue s = JS_NewStringLen(c, p, (size_t)n);
	free(p);
	return s;
}

static const char *arg_string(JSContext *c, JSValueConst v, size_t *len) {
	*len = 0;
	if (JS_IsUndefined(v) || JS_IsNull(v))
		return NULL;
	return JS_ToCStringLen(c, len, v);
}

// host.raw(kind, a, b, n): string | undefined | {e: errorJSON}
static JSValue js_host_raw(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv) {
	(void)this_val;
	if (oom)
		return JS_ThrowOutOfMemory(c); // the VM is doomed: nothing reaches the host anymore
	int32_t kind;
	double n = 0;
	if (argc < 4 || JS_ToInt32(c, &kind, argv[0]) || JS_ToFloat64(c, &n, argv[3]))
		return JS_EXCEPTION;
	size_t alen, blen;
	const char *a = arg_string(c, argv[1], &alen);
	if (!a && !JS_IsUndefined(argv[1]) && !JS_IsNull(argv[1]))
		return JS_EXCEPTION;
	const char *b = arg_string(c, argv[2], &blen);
	if (!b && !JS_IsUndefined(argv[2]) && !JS_IsNull(argv[2])) {
		JS_FreeCString(c, a);
		return JS_EXCEPTION;
	}
	int32_t r = host_call(kind, a, (int32_t)alen, b, (int32_t)blen, n);
	if (a)
		JS_FreeCString(c, a);
	if (b)
		JS_FreeCString(c, b);
	if (r == -1)
		return JS_UNDEFINED;
	if (r >= 0)
		return take_string(c, r);
	JSValue e = take_string(c, -r - 2);
	if (JS_IsException(e))
		return e;
	JSValue o = JS_NewObject(c);
	if (JS_IsException(o)) {
		JS_FreeValue(c, e);
		return o;
	}
	if (JS_SetPropertyStr(c, o, "e", e) < 0) {
		JS_FreeValue(c, o);
		return JS_EXCEPTION;
	}
	return o;
}

// host.parseJSON(s): JSON.parse without reviver, outside the sync budget (the host
// produced the JSON; no plugin code runs).
static JSValue js_host_parse(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv) {
	(void)this_val;
	size_t len;
	if (argc < 1)
		return JS_UNDEFINED;
	const char *s = JS_ToCStringLen(c, &len, argv[0]);
	if (!s)
		return JS_EXCEPTION;
	host_call(K_PAUSE, NULL, 0, NULL, 0, 0);
	JSValue v = JS_ParseJSON(c, s, len, "<host>");
	host_call(K_RESUME, NULL, 0, NULL, 0, 0);
	JS_FreeCString(c, s);
	return v;
}

// host.onRejection(fn): fn(promise, reason, handled) returns true when unhandled
// rejections are waiting.
static JSValue js_host_on_rejection(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv) {
	(void)this_val;
	if (argc < 1 || !JS_IsFunction(c, argv[0]))
		return JS_ThrowTypeError(c, "onRejection: expected a function");
	JS_FreeValue(c, on_rejection);
	on_rejection = JS_DupValue(c, argv[0]);
	return JS_UNDEFINED;
}

// ───────────────────────────────────────────────────────────── exports

// cq_init creates the runtime. mem: JS heap limit in bytes (0: none); stack: JS stack
// limit in bytes (below the linker's stack size).
EXPORT("cq_init") int32_t cq_init(int32_t mem, int32_t stack) {
	if (rt)
		return CQ_FAILED;
	mem_limit = (size_t)(uint32_t)mem;
	rt = JS_NewRuntime2(&mfuncs, NULL);
	if (!rt)
		return flags(CQ_FAILED);
	JS_SetMaxStackSize(rt, (size_t)(uint32_t)stack);
	JS_SetInterruptHandler(rt, interrupt_handler, NULL);
	JS_SetHostPromiseRejectionTracker(rt, rejection_tracker, NULL);
	JS_SetCanBlock(rt, false);
	ctx = JS_NewContext(rt);
	if (!ctx)
		return flags(CQ_FAILED);
	return flags(CQ_OK);
}

// script_name copies the script name stored after the source (buf[len+1:len+1+name_len])
// and NUL-terminates the source. It returns false when the layout does not fit buf.
static bool script_name(int32_t len, int32_t name_len, char *name, size_t size) {
	if (!ctx || len < 0 || name_len < 0 || !take_args((size_t)len + 1 + (size_t)name_len))
		return false;
	size_t nl = (size_t)name_len < size - 1 ? (size_t)name_len : size - 1;
	memcpy(name, buf + len + 1, nl);
	name[nl] = 0;
	buf[len] = 0;
	return true;
}

// install_prelude calls the prelude function fn (consumed) with the host object and keeps
// the returned API object.
static int32_t install_prelude(JSValue fn) {
	JSValue host = JS_NewObject(ctx);
	JS_SetPropertyStr(ctx, host, "raw", JS_NewCFunction(ctx, js_host_raw, "raw", 4));
	JS_SetPropertyStr(ctx, host, "parseJSON", JS_NewCFunction(ctx, js_host_parse, "parseJSON", 1));
	JS_SetPropertyStr(ctx, host, "onRejection", JS_NewCFunction(ctx, js_host_on_rejection, "onRejection", 1));
	JSValue v = JS_Call(ctx, fn, JS_UNDEFINED, 1, (JSValueConst *)&host);
	JS_FreeValue(ctx, fn);
	JS_FreeValue(ctx, host);
	if (JS_IsException(v))
		return flags(exception());
	if (!JS_IsObject(v)) {
		JS_FreeValue(ctx, v);
		out_literal("the prelude did not return an object");
		return flags(CQ_FAILED);
	}
	JS_FreeValue(ctx, api);
	api = v;
	return flags(CQ_OK);
}

// cq_prelude evaluates the prelude (an expression evaluating to a function; layout as for
// cq_eval), calls it with the host object and keeps the returned API object.
EXPORT("cq_prelude") int32_t cq_prelude(int32_t len, int32_t name_len) {
	fired = 0;
	char name[128];
	if (!script_name(len, name_len, name, sizeof(name)))
		return CQ_FAILED;
	JSValue fn = JS_Eval(ctx, buf, (size_t)len, name, JS_EVAL_TYPE_GLOBAL | JS_EVAL_FLAG_STRICT);
	if (JS_IsException(fn))
		return flags(exception());
	return install_prelude(fn);
}

// run_bytecode evaluates bytecode produced by cq_compile (buf[0:len]). The bytecode
// always comes from cq_compile in a VM of the same quickjs.wasm (never from plugins).
static JSValue run_bytecode(int32_t len) {
	if (len <= 0 || !take_args((size_t)len))
		return JS_ThrowTypeError(ctx, "bad bytecode buffer");
	JSValue fn = JS_ReadObject(ctx, (const uint8_t *)buf, (size_t)len, JS_READ_OBJ_BYTECODE);
	if (JS_IsException(fn))
		return fn;
	return JS_EvalFunction(ctx, fn);
}

// cq_prelude_bc is cq_prelude for bytecode.
EXPORT("cq_prelude_bc") int32_t cq_prelude_bc(int32_t len) {
	fired = 0;
	if (!ctx)
		return CQ_FAILED;
	JSValue fn = run_bytecode(len);
	if (JS_IsException(fn))
		return flags(exception());
	return install_prelude(fn);
}

// cq_eval_bc evaluates a script compiled by cq_compile.
EXPORT("cq_eval_bc") int32_t cq_eval_bc(int32_t len) {
	fired = 0;
	if (!ctx)
		return CQ_FAILED;
	JSValue v = run_bytecode(len);
	if (JS_IsException(v))
		return flags(exception());
	JS_FreeValue(ctx, v);
	return flags(CQ_OK);
}

// cq_compile compiles a global script (layout as for cq_eval; strict mode when strict is
// set) without running it; out holds the bytecode.
EXPORT("cq_compile") int32_t cq_compile(int32_t len, int32_t name_len, int32_t strict) {
	fired = 0;
	char name[128];
	if (!script_name(len, name_len, name, sizeof(name)))
		return CQ_FAILED;
	int fl = JS_EVAL_TYPE_GLOBAL | JS_EVAL_FLAG_COMPILE_ONLY | (strict ? JS_EVAL_FLAG_STRICT : 0);
	JSValue fn = JS_Eval(ctx, buf, (size_t)len, name, fl);
	if (JS_IsException(fn))
		return flags(exception());
	size_t size;
	uint8_t *bc = JS_WriteObject(ctx, &size, fn, JS_WRITE_OBJ_BYTECODE);
	JS_FreeValue(ctx, fn);
	if (!bc)
		return flags(exception());
	char *p = malloc(size + 1);
	if (!p) {
		js_free(ctx, bc);
		oom = 1;
		return flags(CQ_FAILED);
	}
	memcpy(p, bc, size);
	js_free(ctx, bc);
	set_out(p, (int32_t)size);
	return flags(CQ_STRING);
}

// cq_eval evaluates a global script: buf[0:len] is the source, buf[len+1:len+1+name_len]
// the script name.
EXPORT("cq_eval") int32_t cq_eval(int32_t len, int32_t name_len) {
	fired = 0;
	char name[128];
	if (!script_name(len, name_len, name, sizeof(name)))
		return CQ_FAILED;
	JSValue v = JS_Eval(ctx, buf, (size_t)len, name, JS_EVAL_TYPE_GLOBAL);
	if (JS_IsException(v))
		return flags(exception());
	JS_FreeValue(ctx, v);
	return flags(CQ_OK);
}

// drain_jobs runs pending jobs (microtasks) until none is left, an interrupt or the
// memory limit. An exception escaping a job (an uncatchable error, which QuickJS-ng does
// not turn into a rejection) does not stop the drain; the last one is kept in out.jptr
// (CQ_EXCEPTION is returned).
static int32_t drain_jobs(void) {
	int32_t status = CQ_OK;
	for (;;) {
		JSContext *c;
		int r = JS_ExecutePendingJob(rt, &c);
		if (r == 0)
			break;
		if (r < 0) {
			char *sp = out.ptr;
			int32_t sl = out.len;
			out.ptr = NULL;
			out.len = 0;
			int32_t code = exception();
			free(out.jptr);
			out.jptr = out.ptr;
			out.jlen = out.len;
			out.ptr = sp;
			out.len = sl;
			if (code == CQ_INTERRUPTED || oom)
				return CQ_INTERRUPTED;
			status = CQ_EXCEPTION;
		}
		if (oom)
			return CQ_INTERRUPTED;
	}
	return status;
}

// cq_call calls api[name](...args): the arguments (name first, then argc strings) are
// pulled back to back, lens are their byte lengths. Up to 5 arguments. With drain set,
// the jobs queued by the call run in the same wasm call (CQ_F_JOBERR reports an exception
// that escaped one).
EXPORT("cq_call") int32_t cq_call(int32_t argc, int32_t l0, int32_t l1, int32_t l2, int32_t l3, int32_t l4, int32_t l5, int32_t drain) {
	fired = 0;
	int32_t lens[6] = {l0, l1, l2, l3, l4, l5};
	if (!ctx || JS_IsUndefined(api) || argc < 0 || argc > 5)
		return CQ_FAILED;
	size_t total = 0;
	for (int i = 0; i <= argc; i++) {
		if (lens[i] < 0)
			return CQ_FAILED;
		total += (size_t)lens[i];
	}
	if (!take_args(total))
		return flags(CQ_FAILED);
	JSAtom fname = JS_NewAtomLen(ctx, buf, (size_t)l0);
	if (fname == JS_ATOM_NULL)
		return flags(exception());
	JSValue fn = JS_GetProperty(ctx, api, fname);
	JS_FreeAtom(ctx, fname);
	if (!JS_IsFunction(ctx, fn)) {
		JS_FreeValue(ctx, fn);
		out_literal("unknown prelude function");
		return flags(CQ_FAILED);
	}
	JSValue args[5];
	size_t off = (size_t)l0;
	int built = 0;
	for (int i = 0; i < argc; i++) {
		args[i] = JS_NewStringLen(ctx, buf + off, (size_t)lens[i + 1]);
		off += (size_t)lens[i + 1];
		if (JS_IsException(args[i])) {
			for (int j = 0; j < built; j++)
				JS_FreeValue(ctx, args[j]);
			JS_FreeValue(ctx, fn);
			return flags(exception());
		}
		built++;
	}
	JSValue v = JS_Call(ctx, fn, JS_UNDEFINED, argc, (JSValueConst *)args);
	for (int i = 0; i < built; i++)
		JS_FreeValue(ctx, args[i]);
	JS_FreeValue(ctx, fn);
	int32_t code = result(v);
	if (!drain || (code != CQ_OK && code != CQ_STRING) || oom)
		return flags(code);
	int32_t jc = drain_jobs();
	if (jc == CQ_INTERRUPTED)
		return flags(CQ_INTERRUPTED);
	return flags(code) | (jc == CQ_EXCEPTION ? CQ_F_JOBERR : 0);
}

// cq_jobs runs pending jobs (see drain_jobs); an escaped exception is reported as
// CQ_EXCEPTION with its errInfo in out.jptr.
EXPORT("cq_jobs") int32_t cq_jobs(void) {
	fired = 0;
	if (!ctx)
		return CQ_FAILED;
	return flags(drain_jobs());
}

// cq_take_rejections clears the rejection flag (the prelude keeps the list).
EXPORT("cq_take_rejections") void cq_take_rejections(void) { rejections = 0; }

// cq_mem reports the JS heap in use, in bytes.
EXPORT("cq_mem") int32_t cq_mem(void) { return (int32_t)mem_used; }

// cq_gc runs the garbage collector.
EXPORT("cq_gc") void cq_gc(void) {
	if (rt)
		JS_RunGC(rt);
}
