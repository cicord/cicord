// Package plugin is Cicord's plugin host: it runs plugin bundles (built by internal/build) in
// Sobek VMs and implements the @cicord/sdk contract (spec/sdk/cicord-sdk.d.ts, spec/README.md §6
// and §8) on top of the discord and state contracts.
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
// One Sobek runtime and one event-loop goroutine per instance. Handlers start in call order
// and interleave at await points (Node-like). Host calls that do I/O run on other goroutines
// and settle their promise back on the loop. The handler that issued a host call is tracked
// across awaits with Sobek's AsyncContextTracker, so every call is bounded by its own
// handler's context: when a handler times out (Limits.HandlerTimeout), its later host calls
// fail and its pending ones are cancelled. A handler that returns without awaiting some work
// keeps that work alive until its deadline.
//
// Each turn of the loop (a handler's synchronous part, or the continuations run when a
// host call settles) is bounded by Limits.SyncBudget: a runaway turn is interrupted, the
// running handler is aborted with ErrBudgetExceeded and the VM keeps serving other
// handlers. Sobek drops the microtasks queued during an interrupted turn; handlers that were
// waiting on them end with ErrHandlerTimeout.
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
