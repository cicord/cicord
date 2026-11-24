---
title: "ADR-0018 — Plugins run on QuickJS-ng compiled to WebAssembly"
description: "ADR-0018: Plugins run on QuickJS-ng compiled to WebAssembly (wazero): hard memory cap, killable CPU."
sidebar:
  label: "0018 · Plugins run on QuickJS-ng compiled to WebAssem…"
  order: 18
---

**Status:** Accepted (2026-11-24). Supersedes the engine and bundle target of
[ADR-0005](../0005-typescript-plugins/); the rest of ADR-0005 (TypeScript, esbuild in Go,
`@cicord/sdk`, inline source maps) stands.

## Context
Plugins ran on Sobek, a pure-Go JavaScript engine. Its limits made one plugin able to take
down every bot of a runtime:

- no memory accounting: `"x".repeat(1e9)`, `new ArrayBuffer(n)` or a growing array allocate
  on the Go heap, and a Go out-of-memory is fatal (it cannot be recovered);
- `Interrupt` is only checked between bytecode instructions, so it cannot stop a long native
  builtin;
- the runtime could not tell a crashed instance from a slow one, and nothing restarted it.

The replacement had to keep `CGO_ENABLED=0` (static binary, no C toolchain to build Cicord)
and the engine-neutral surface of the plugin host (`plugin.Runtime`, the op table, the
interaction state machine, the `sdk.js` prelude).

| | Sobek | fastschema/qjs | **Own binding: QuickJS-ng → wasm on wazero** |
|---|---|---|---|
| Hard memory limit | no | yes (`MemoryLimit`) | **yes (JS heap and linear memory)** |
| Stop native builtins | no | no (fixed `MaxExecutionTime`) | **yes (soft interrupt + kill)** |
| A crash stays in the plugin | no | yes | **yes** |
| CGO | none | none | **none** |
| Host functions, promises, microtask control | yes | partial (its eval runs the std event loop) | **yes, sized for the prelude** |
| Attack surface | Go only | quickjs-libc, host CWD mounted as `/` | **no libc modules, no WASI capability but clocks and random** |

**Spike: fastschema/qjs v0.0.6** (QuickJS-ng on wazero). Rejected:
- its interrupt handler is commented out (`helpers.c`); only a fixed execution timeout
  exists, nothing a budget timer can drive;
- `eval.c` runs `js_std_loop`/`js_std_await` inside eval, so the host does not control
  microtasks;
- it links quickjs-libc (`std`/`os` helpers) and mounts the host's working directory as the
  guest's `/`;
- its compilation state is a global, mutable cache.

## Decision
**Engine.** `internal/plugin/qjs` runs QuickJS-ng (0.18.0) compiled with a small shim
(`csrc/cicord.c`) to `wasm32-wasip1` (reactor), on wazero (pure Go, compiler engine).
- **Reproducible build:** `scripts/build-quickjs-wasm.sh` downloads the pinned QuickJS-ng
  tarball (sha256 checked) and compiles it in the `wasi-sdk-34` image pinned by digest
  (`linux/amd64`, `--network none`, `-ffile-prefix-map`). The `.wasm` is committed;
  `quickjs.wasm.sha256` is checked by a Go test and `--check` rebuilds and compares.
- **One compile per process:** quickjs.wasm is compiled once per process (an in-memory
  wazero compilation cache, one wazero runtime per memory cap), then each plugin instance
  instantiates it. The SDK prelude is compiled to QuickJS bytecode once per process and
  bundles once per content hash (bounded cache). Bytecode only ever comes from our own
  `Compile` in the same quickjs.wasm, never from plugins: QuickJS does not validate
  bytecode.
- **Guest surface:** the standard intrinsics, no quickjs-libc (no `std`/`os`, files,
  stdout), WASI with real clocks and `crypto/rand` and nothing else. Plugin code reaches Go
  only through `host.raw(kind, a, b, n)`, a native function handed to the prelude only.
  Go never re-enters the guest from an import: results are copied with `host_take` into a
  buffer the guest allocated, and call arguments are pulled the same way, so each event
  loop turn is a single wasm call (wazero starts a watcher goroutine per call).

**Memory.** `Limits.Memory` (default 64 MiB, at most 1 GiB) caps the JS heap through the
shim's malloc functions, which set a sticky out-of-memory flag: plugin code can catch the
error QuickJS throws, but the VM is discarded anyway, and no host call is served after the
flag is set. The wasm linear memory is capped 24 MiB above the heap (shadow stack, static
data, shim buffers); it is backed by a mapping outside the Go heap (`mmap` on unix, a Go
slice elsewhere) whose address never moves. The shadow stack comes first in linear memory
(1 MiB, `--stack-first`) so an overflow traps instead of corrupting the heap; the JS stack
limit (768 KiB) makes QuickJS throw a `RangeError` before that.

**CPU.** The existing budget timer drives two stops (`SyncBudget`, `StartupBudget`,
`InspectBudget`, `HandlerTimeout` keep their semantics):
- **soft stop** at the budget: QuickJS's interrupt handler asks Go (an atomic read) and
  throws an uncatchable error; the invocation is aborted, the VM keeps serving. QuickJS-ng
  polls it in loops, calls, regular expressions, `sort` and most array builtins;
- **hard stop** `max(budget, 500 ms)` later: Go stores a kill flag into the guest's linear
  memory. The engine is compiled with `-finstrument-function-entry-bare` (every C function
  entry checks the flag: a load and a branch), and the build script adds the check to the
  loops that can run long without a call (`string_cmp`, used per position by the naive
  O(n·m) string searches, and the BigInt row helpers). The guest traps, the instance
  crashes. wazero's `WithCloseOnContextDone` was measured and rejected: it leaves the guest
  for Go at every wasm loop iteration, which made CPU-bound plugin code 3.7 times slower
  than on Sobek.
- After a soft stop, microtasks still queued run in a second turn charged to the same
  invocation; if that turn overruns too, the instance crashes (code that keeps running after
  its handler was aborted).

**Attribution.** Sobek's `AsyncContextTracker` followed a handler across `await`. QuickJS-ng
0.18's promise hooks do not fire for promise reaction jobs (only for thenable jobs), so they
cannot follow `await`. Instead every turn is entered by Go for a known invocation: a
handler call, the settlement of one of its host calls (the invocation id is kept with the
call), or one of its timers; the code of that turn, microtasks included, is charged to it.

**Crashes and restarts.** Out-of-memory, hard stop and engine traps discard the VM: every
pending and later call of the instance fails with `ErrCrashed`, `Instance.Done()` is closed
and `Err()` gives the cause (both on `plugin.Runtime`). `internal/runtime` supervises each
instance: a crash marks it `Degraded` at once with the cause, the crashed instance stays
installed (its calls fail fast, interactions get the generic error reply), and it is
restarted after 1 s, doubling up to 5 min, reset after 5 min up. A replaced revision retires
its supervisors.

**Host-side quotas** (independent of the engine): handler calls waiting for an instance
(`Limits.QueueLength`, 1024, `ErrBusy` beyond), pending async host calls (256), host call
arguments (2 MiB, checked before `json.Unmarshal`), log fields (16 KiB), `kv.list` page
(4 MiB: the page is cut short and its cursor continues), route response (4 MiB). Strings
are also bounded where they leave the VM, so a plugin cannot move its heap onto the Go
heap: a host call argument longer than 4.2 MiB is refused before it is copied, and error
messages, stacks (16 KiB each) and log lines (4 KiB) are cut by the prelude and again by
the host.

**Bundles** target ES2023 syntax (QuickJS-ng runs all of it, async generators included).
Stack traces are mapped to the TypeScript sources by a small source map decoder in the
host (QuickJS does not read source maps).

## Benchmarks
`go test -bench . ./internal/plugin/` (bench_test.go, engine-neutral), Apple M5 Max,
Go 1.27.4. Sobek is the branch base (f6b76e1) with the same benchmark file; the middle
column is the first port (prelude parsed per VM, hard stop through wazero's
`WithCloseOnContextDone`).

| | Sobek | First port | **QuickJS-ng wasm (chosen)** |
|---|---|---|---|
| Instance start (NewInstance + Start + Stop) | 0.21 ms | 6.8 ms | **0.50 ms** |
| Event handler with two KV calls | 32 µs | 108 µs | **54 µs** |
| CPU-bound command (string/Map/sort/JSON work) | 11.5 ms | 42.0 ms | **12.8 ms** |
| Idle instance | 197 KiB Go heap | 2 MiB Go heap | **388 KiB Go heap + ≤ 1.44 MiB wasm memory** (outside the Go heap; the 1 MiB stack is mostly untouched) |

## Consequences
- A plugin can no longer crash the process or the other plugins through memory or CPU; a
  crash costs that instance a restart. The binary grows by about 1.1 MB (quickjs.wasm).
- Each instance reserves its linear memory cap as virtual memory (88 MiB by default, on
  unix committed only when touched).
- Behaviour differences visible to plugins: QuickJS-ng makes every `InternalError`
  uncatchable (out of memory, "string too long", interrupts); stack traces have QuickJS's
  format (positions still mapped to TypeScript); dates are in UTC (WASI has no time zone);
  there is no `Intl` (as before).
- Remaining limits, in the security review backlog: a native loop that neither calls a
  function nor goes through a patched helper can delay the kill (bounded by the instance's
  memory: it only walks its own data); a VM stuck that way keeps its event-loop goroutine
  until it returns; the per-instance KV total quota needs a size accounting in
  `state.KV` (not available yet); a continuation waiting on a promise settled by another
  handler is charged to that handler.
- The memory backing uses wazero's `experimental.MemoryAllocator` API; a wazero upgrade must
  keep it (the qjs tests cover kill, memory cap and restarts).
- Upgrading QuickJS-ng: change the version and tarball sha256 in the build script, run it
  (it fails if a kill-check patch no longer applies), run `go test ./internal/plugin/...`,
  commit the three files.
