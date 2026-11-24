// Package plugin is Cicord's plugin host: it runs plugin bundles (built by internal/build) in
// QuickJS-ng VMs compiled to WebAssembly (internal/plugin/qjs, ADR-0018) and implements the
// @cicord/sdk contract (spec/sdk/cicord-sdk.d.ts, spec/README.md §6 and §8) on top of the
// discord and state contracts.
//
// # Lifecycle
//
//	res, _ := build.Bundle(dir, manifest.Entry)
//	def, err := plugin.Inspect(res.Code)          // static definition (commands, handlers, routes…)
//	inst, err := plugin.NewInstance(res.Code, def, env)
//	err = inst.Start(ctx)                          // evaluate module, onStart
//	err = inst.HandleEvent(ctx, ev)                // or DispatchEvent (non-blocking, ordered)
//	err = inst.HandleInteraction(ctx, it, "command", it.CommandName)
//	err = inst.Stop(ctx)                           // onStop (bounded), release the VM
//
// The bot host routes: gateway events to HandleEvent/DispatchEvent, interactions to
// HandleInteraction (component/modal custom IDs are namespaced with EncodeCustomID; use
// ParseCustomID to find the instance and local id), portal requests to HandleRoute (after
// checking Definition.MatchRoute's access level), due tasks (state.TaskQueue) to RunTask,
// cron schedules (NextRun) to RunSchedule, and events published through Env.Publish to the
// subscribers' Deliver.
//
// # Execution model
//
// One QuickJS VM (its own wasm instance and linear memory) and one event-loop goroutine
// per instance. Handlers start in call order and interleave at await points (Node-like).
// Host calls that do I/O run on other goroutines and settle their promise back on the
// loop. Every turn of the loop is entered by Go for a known invocation (a handler call,
// the settlement of one of its host calls, one of its timers), and the code that runs in
// it, microtasks included, is charged to that invocation: its context bounds every host
// call made, so when a handler times out (Limits.HandlerTimeout), its later host calls
// fail and its pending ones are cancelled. A handler that returns without awaiting some
// work keeps that work alive until its deadline. (A continuation of handler B that waits
// on a promise settled by handler A runs in A's turn and is charged to A.)
//
// Each turn is bounded by Limits.SyncBudget: a runaway turn is interrupted, its invocation
// aborted with ErrBudgetExceeded and the VM keeps serving other handlers. Microtasks still
// queued then run in a second turn charged to the same invocation; if that one overruns
// too, the instance crashes. Code that does not reach an interrupt check (a long native
// builtin) is killed hardStopGrace after the budget, which crashes the instance.
//
// # Crashes
//
// An instance crashes when its VM is discarded: Limits.Memory reached (even if plugin code
// catches the error), hard stop, engine trap. Every pending and later call fails with an
// error wrapping ErrCrashed, Done is closed and Err reports the cause; the bot host
// (internal/runtime) restarts the instance with backoff. The Go process is never affected.
//
// # Host-side quotas
//
// Independently of the engine, an instance is bounded by: Limits.QueueLength handler calls
// waiting for its loop (ErrBusy beyond), maxInFlight pending async host calls,
// maxHostArgs bytes of arguments per host call (checked before decoding), maxLogFields
// bytes of log fields, maxKVListBytes per kv.list page and maxRouteResponse bytes per
// route response.
//
// # Interactions
//
// reply()/defer()/followUp()/update()/showModal() follow the table documented on the
// interaction type (interaction.go). An interaction not answered after Limits.AutoDeferAfter
// is deferred (ephemeral when the command says `ephemeral: true`; components get a silent
// DeferredUpdate). When a handler fails before replying, the user gets GenericErrorMessage
// (ephemeral), or the deferred response is edited.
//
// # Capabilities and isolation
//
// Every host call checks Env.Grants (spec/README.md §6) and throws CapabilityError in JS
// (returned as *ScriptError wrapping *CapabilityError in Go). Channel IDs used by plugin code
// are checked to belong to the instance's server (or to a DM channel the instance opened or
// received), so a plugin cannot reach other servers the bot is in. http.fetch enforces the
// network egress rules (allow-list, public addresses only, checked address dialed, manual
// redirects, https, size and time limits).
//
// Message content is gated per instance (Env.MessageContent, the plugin's own
// MessageContent intent): gateway intents are unioned per bot, so without this a plugin
// would read content because another plugin of the same bot declared the intent.
//
// # SDK extensions
//
// Beyond cicord-sdk.d.ts the VM provides setTimeout/setInterval/clearTimeout/clearInterval,
// queueMicrotask and console.* (mapped to the instance logger). Timers belong to the handler
// that created them and stop with it. Component handler lookup falls back to the part of the
// local id before the first ':' ("vote:42" → components.vote), so ids can carry data.
package plugin
