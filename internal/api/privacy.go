package api

import (
	"net/http"

	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
)

func (s *server) handleMemberData(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	actor, _, err := s.requireRole(r, key, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	action := r.PathValue("action")
	if action != "export" && action != "erase" {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown member data operation"))
		return
	}
	var body struct {
		UserID string `json:"userId"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if !runtime.ValidPrivacyMember(body.UserID) {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "expected a Discord member ID"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	report, err := s.rt.MemberData(r.Context(), key, body.UserID, action)
	if err != nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "member data operation could not be completed"))
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: key, Actor: actor.auditActor(), Action: "privacy." + action, Target: s.rt.Pseudonym(key, body.UserID), Details: mustJSON(map[string]bool{"complete": report.Complete})})
	writeJSON(w, http.StatusOK, report)
}
