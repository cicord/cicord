package api

import (
	"net/http"
	"time"

	"github.com/cicord/cicord/internal/state"
)

func (s *server) handleDeadLetters(w http.ResponseWriter, r *http.Request) {
	serverKey := r.PathValue("server")
	if _, _, err := s.requireRole(r, serverKey, roleAdmin); err != nil {
		s.fail(w, err)
		return
	}
	items, err := s.rt.Store().Tasks().DeadLetters(r.Context(), serverKey, 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) handleDeadLetterDecision(w http.ResponseWriter, r *http.Request) {
	serverKey := r.PathValue("server")
	actor, _, err := s.requireRole(r, serverKey, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	decision := r.PathValue("decision")
	if decision != "retry" && decision != "discard" {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown task decision"))
		return
	}
	id := r.PathValue("id")
	changed, err := s.rt.Store().Tasks().ResolveDead(r.Context(), serverKey, id, decision == "retry", time.Now())
	if err != nil {
		s.fail(w, err)
		return
	}
	if !changed {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "no such dead letter"))
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: serverKey, Actor: actor.auditActor(), Action: "task." + decision, Target: id})
	w.WriteHeader(http.StatusNoContent)
}
