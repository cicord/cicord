package api

import (
	"errors"
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
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "expected a Discord member ID (a snowflake of 17 to 20 digits)"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	target := s.rt.Pseudonym(key, body.UserID)
	report, err := s.rt.MemberData(r.Context(), key, body.UserID, action)
	if err != nil {
		// An erase that failed part-way already ran some hooks: it must leave a
		// trace (P10). The marker was stored first, so a retry is safe.
		s.log.Warn("member data operation failed", "server", key, "action", action, "subject", target, "err", err)
		if action == "erase" {
			s.audit(r.Context(), state.AuditEntry{Server: key, Actor: actor.auditActor(), Action: "privacy." + action, Target: target,
				Details: mustJSON(map[string]any{"complete": false, "failed": true, "plugins": statuses(report), "tasksErased": report.Tasks, "interopErased": report.Interop})})
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":  apiError{Code: "unavailable", Message: "member data operation could not be completed; retry it"},
			"report": report,
		})
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: key, Actor: actor.auditActor(), Action: "privacy." + action, Target: target, Details: mustJSON(map[string]bool{"complete": report.Complete})})
	writeJSON(w, http.StatusOK, report)
}

// statuses summarises a report for the audit log, without any member data.
func statuses(rep runtime.PrivacyReport) map[string]string {
	out := map[string]string{}
	for _, p := range rep.Plugins {
		out[p.Instance] = p.Status
	}
	return out
}

// GET /servers/{server}/orphaned-data lists uninstalled installations that still
// hold data on the server.
func (s *server) handleOrphanedData(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleAdmin); err != nil {
		s.fail(w, err)
		return
	}
	items, err := s.rt.OrphanedData(r.Context(), key)
	if err != nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "storage inventory unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// POST /servers/{server}/orphaned-data/purge deletes the data an uninstalled
// installation left. The body repeats the installation name as confirmation.
func (s *server) handlePurgeOrphanedData(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	actor, _, err := s.requireRole(r, key, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	var body struct {
		Instance string `json:"instance"`
		Confirm  string `json:"confirm"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Instance == "" || body.Confirm != body.Instance {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", `"confirm" must repeat the installation name "<bot>/<instance>"`))
		return
	}
	usage, err := s.rt.PurgeOrphanedData(r.Context(), key, body.Instance)
	switch {
	case errors.Is(err, runtime.ErrInstalled):
		s.fail(w, httpErr(http.StatusConflict, "conflict", "%s", err.Error()))
		return
	case err != nil:
		s.log.Warn("purge orphaned data", "server", key, "instance", body.Instance, "err", err)
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "the purge could not be completed"))
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: key, Actor: actor.auditActor(), Action: "plugin.data.purge", Target: body.Instance, Details: mustJSON(usage)})
	writeJSON(w, http.StatusOK, usage)
}
