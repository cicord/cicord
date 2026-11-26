package api

import (
	"net/http"

	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
)

type pluginSecretView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Hosts       []string `json:"hosts"`
	Required    bool     `json:"required"`
	Bound       bool     `json:"bound"`
	External    bool     `json:"external"`
}

func (s *server) handlePluginSecret(w http.ResponseWriter, r *http.Request) {
	server := r.PathValue("server")
	actor, _, err := s.requireRole(r, server, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.opts.SecretStore == nil {
		s.fail(w, httpErr(503, "unavailable", "encrypted secret storage is unavailable"))
		return
	}
	for _, in := range s.rt.Instances(server) {
		c := in.Config
		if c.Bot != r.PathValue("bot") || c.Name != r.PathValue("instance") {
			continue
		}
		name := r.PathValue("name")
		for _, spec := range c.Plugin.Manifest.Spec.Secrets {
			binding, ok := c.SecretRefs[name]
			if spec.Name != name || !ok {
				continue
			}
			if s.opts.Secret != nil {
				if _, ok := s.opts.Secret(binding.SecretRef); ok {
					s.fail(w, httpErr(409, "conflict", "this binding is managed by the operator"))
					return
				}
			}
			var body struct {
				Value string `json:"value"`
			}
			if err := s.decodeJSON(w, r, &body); err != nil {
				s.fail(w, httpErr(400, "invalid", "invalid secret request"))
				return
			}
			if len(body.Value) == 0 || len(body.Value) > 8192 {
				s.fail(w, httpErr(400, "invalid", "enter a value of 1 to 8192 bytes"))
				return
			}
			if err := s.opts.SecretStore.Set(r.Context(), runtime.PluginSecretSlot(c, name), body.Value); err != nil {
				s.fail(w, httpErr(503, "unavailable", "secret storage failed"))
				return
			}
			s.audit(r.Context(), state.AuditEntry{Server: server, Actor: actor.auditActor(), Action: "plugin.secret.set", Target: c.ID() + "/" + name})
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	s.fail(w, httpErr(404, "not_found", "unknown instance secret binding"))
}
