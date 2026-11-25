package runtime

import (
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/plugin"
)

// onHandler returns the plugin.Env.OnHandler of an instance: the metrics, then
// Options.OnHandler (the alert notifier names the failing handler with it).
func (r *Runtime) onHandler(in config.Instance) func(plugin.HandlerResult) {
	m := r.metrics.onHandler(in)
	hook := r.opts.OnHandler
	if hook == nil {
		return m
	}
	return func(res plugin.HandlerResult) {
		m(res)
		hook(in, res)
	}
}
