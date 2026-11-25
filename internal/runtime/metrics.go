package runtime

import (
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/metrics"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/reconcile"
)

// Prometheus metrics of the runtime (https://cicord.dev/docs/self-hosting/monitoring/).
// Labels are configuration keys (server, bot, plugin instance), the plugin name and fixed
// enumerations: their cardinality is bounded by the repository, never by Discord users.

// instanceLabels are the labels of a plugin instance. "instance" is avoided: Prometheus
// sets it on every scraped series (the target).
var instanceLabels = []string{"bot", "server", "plugin", "plugin_instance"}

func instanceLabelValues(in config.Instance) []string {
	return []string{in.Bot, in.Server, in.Plugin.Manifest.Metadata.Name, in.Name}
}

// serverHealths are the values of the health label (state set: one series per value).
var serverHealths = []string{HealthHealthy, HealthProgressing, HealthDegraded, HealthMissing}

type runtimeMetrics struct {
	activations   *metrics.CounterVec   // result
	fetchErrors   *metrics.CounterVec   //
	syncs         *metrics.CounterVec   // server, result
	applied       *metrics.CounterVec   // server, trigger
	failed        *metrics.CounterVec   // server, trigger
	crashes       *metrics.CounterVec   // instanceLabels
	restarts      *metrics.CounterVec   // instanceLabels, result
	handlerErrors *metrics.CounterVec   // instanceLabels, kind
	handlerTime   *metrics.HistogramVec // instanceLabels, kind
}

func withLabels(extra ...string) []string {
	return append(append([]string{}, instanceLabels...), extra...)
}

func newRuntimeMetrics(reg *metrics.Registry, r *Runtime) *runtimeMetrics {
	m := &runtimeMetrics{
		activations: reg.NewCounter("cicord_revision_activations_total",
			"Revisions evaluated, by result: activated, or rejected (invalid configuration, plugin build failure).", "result"),
		fetchErrors: reg.NewCounter("cicord_source_fetch_errors_total",
			"Failed fetches of the configuration source (git fetch, local snapshot)."),
		syncs: reg.NewCounter("cicord_reconcile_syncs_total",
			"Full reconciliations of a server (activation, periodic resync with selfHeal, sync button), by result: succeeded or failed.", "server", "result"),
		applied: reg.NewCounter("cicord_reconcile_changes_applied_total",
			"Changes applied to Discord, by trigger: sync (full reconciliation) or self_heal (drift correction).", "server", "trigger"),
		failed: reg.NewCounter("cicord_reconcile_changes_failed_total",
			"Changes that Discord refused, by trigger: sync or self_heal.", "server", "trigger"),
		crashes: reg.NewCounter("cicord_plugin_instance_crashes_total",
			"Plugin instance crashes (memory limit, hard stop, engine trap).", instanceLabels...),
		restarts: reg.NewCounter("cicord_plugin_instance_restarts_total",
			"Restarts of crashed plugin instances, by result: succeeded or failed.", withLabels("result")...),
		handlerErrors: reg.NewCounter("cicord_plugin_handler_errors_total",
			"Plugin handler calls that failed (exception, timeout, budget exceeded, crash, full queue), by handler kind.", withLabels("kind")...),
		handlerTime: reg.NewHistogram("cicord_plugin_handler_duration_seconds",
			"Duration of plugin handler calls, from dispatch (including the queue wait) to the result, by handler kind.",
			metrics.DefBuckets, withLabels("kind")...),
	}
	r.registerGauges(reg)
	return m
}

// onHandler returns the plugin.Env.OnHandler of an instance.
func (m *runtimeMetrics) onHandler(in config.Instance) func(plugin.HandlerResult) {
	base := instanceLabelValues(in)
	return func(res plugin.HandlerResult) {
		labels := append(append([]string{}, base...), res.Kind)
		if res.Err != nil {
			m.handlerErrors.Inc(labels...)
		}
		if res.Queued {
			m.handlerTime.Observe(res.Duration.Seconds(), labels...)
		}
	}
}

func (m *runtimeMetrics) reconciled(server, trigger string, res reconcile.Result) {
	m.applied.Add(float64(len(res.Applied)), server, trigger)
	m.failed.Add(float64(len(res.Failed)), server, trigger)
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// registerGauges registers the gauges computed from the runtime state at every scrape.
// Every configured server, bot and plugin instance has a series (0 when it is missing,
// disconnected or stopped), so that its absence can be alerted on.
func (r *Runtime) registerGauges(reg *metrics.Registry) {
	reg.NewFunc(metrics.Gauge, "cicord_revision_active", "1 when a revision is active (the runtime serves a configuration).", nil,
		func(emit metrics.Emit) { emit(boolValue(r.Active() != nil)) })
	reg.NewFunc(metrics.Gauge, "cicord_revision_info", "The active revision (always 1): short revision id and source (git, local).",
		[]string{"revision", "source"}, func(emit metrics.Emit) {
			if a := r.Active(); a != nil {
				emit(1, a.rev.ID, a.rev.Source)
			}
		})
	reg.NewFunc(metrics.Gauge, "cicord_revision_activated_timestamp_seconds", "When the active revision was activated, in seconds since the Unix epoch.", nil,
		func(emit metrics.Emit) {
			if a := r.Active(); a != nil {
				emit(float64(a.activatedAt.UnixNano()) / 1e9)
			}
		})

	type serverSnap struct {
		key, health string
		lastSync    float64
		ok, drift   bool
	}
	servers := func(fn func(serverSnap)) {
		a := r.Active()
		if a == nil {
			return
		}
		for _, key := range sortedKeys(a.servers) {
			s := a.servers[key]
			s.mu.Lock()
			snap := serverSnap{key: key, health: s.health, ok: s.lastSync != nil && s.lastSyncOK}
			if s.lastSync != nil {
				snap.lastSync = float64(s.lastSync.UnixNano()) / 1e9
			}
			s.mu.Unlock()
			snap.drift = s.rec != nil && s.rec.DriftPending()
			fn(snap)
		}
	}
	reg.NewFunc(metrics.Gauge, "cicord_server_last_sync_timestamp_seconds",
		"When the server was last fully reconciled, in seconds since the Unix epoch (0: never).", []string{"server"},
		func(emit metrics.Emit) { servers(func(s serverSnap) { emit(s.lastSync, s.key) }) })
	reg.NewFunc(metrics.Gauge, "cicord_server_last_sync_success", "1 when the last full reconciliation of the server succeeded.", []string{"server"},
		func(emit metrics.Emit) { servers(func(s serverSnap) { emit(boolValue(s.ok), s.key) }) })
	reg.NewFunc(metrics.Gauge, "cicord_server_out_of_sync", "1 when Discord drifted from the repository and the drift is not corrected yet.", []string{"server"},
		func(emit metrics.Emit) { servers(func(s serverSnap) { emit(boolValue(s.drift), s.key) }) })
	reg.NewFunc(metrics.Gauge, "cicord_server_health", "Server health (state set: 1 for the current health, 0 for the others).", []string{"server", "health"},
		func(emit metrics.Emit) {
			servers(func(s serverSnap) {
				for _, h := range serverHealths {
					emit(boolValue(s.health == h), s.key, h)
				}
			})
		})

	reg.NewFunc(metrics.Gauge, "cicord_bot_gateway_connected", "1 when the bot's Discord gateway connection is up.", []string{"bot"},
		func(emit metrics.Emit) {
			repo := r.Repo()
			if repo == nil {
				return
			}
			for _, key := range sortedKeys(repo.Bots) {
				emit(boolValue(r.gatewayConnected(key)), key)
			}
		})
	reg.NewFunc(metrics.Gauge, "cicord_plugin_instance_running", "1 when the plugin instance is running (0: not started, crashed or restarting).", instanceLabels,
		func(emit metrics.Emit) {
			a := r.Active()
			if a == nil {
				return
			}
			for _, i := range a.instances {
				inst := i.inst()
				emit(boolValue(inst != nil && inst.Err() == nil), instanceLabelValues(i.cfg)...)
			}
		})
}

// gatewayConnected reports whether a bot's gateway is up: its session is open and, when the
// session can tell, READY.
func (r *Runtime) gatewayConnected(bot string) bool {
	h := r.bot(bot)
	if h == nil {
		return false
	}
	if gs, ok := h.session.(discord.GatewayStatusReader); ok {
		return gs.GatewayConnected()
	}
	return true
}
