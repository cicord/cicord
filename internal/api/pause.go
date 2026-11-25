package api

import (
	"errors"
	"net/http"

	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
)

// Emergency switch (https://cicord.dev/docs/reference/http-api/servers/): pausing
// enforcement stops every apply to a server until it is resumed. Moderators and admins of
// the server may pause and resume: a moderator handling a raid must not wait for an
// admin, and resuming only brings back the configuration reviewed in git.

type pauseRequest struct {
	Reason string `json:"reason"`
}

// pauseStateDTO is GET …/pause: the pause, and the plan that resuming lets apply.
type pauseStateDTO struct {
	Paused *runtime.PauseInfo `json:"paused"`
	// Plan is the pending plan of the server: for moderators only while it is paused (the
	// resume preview; the plan endpoint is admin-only), always for admins. Null when it is
	// not shown or cannot be computed (PlanError then says why).
	Plan      *planDTO `json:"plan"`
	PlanError string   `json:"planError,omitempty"`
}

func pauseInfoOf(id *identity, reason string) runtime.PauseInfo {
	p := runtime.PauseInfo{By: id.auditActor(), Reason: reason}
	if !id.bearer && !id.setup {
		p.ByName = id.user.DisplayName()
	}
	return p
}

func (s *server) handlePauseGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	_, ro, err := s.requireRole(r, key, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := pauseStateDTO{}
	if p, ok := s.rt.Paused(key); ok {
		out.Paused = &p
	}
	if out.Paused == nil && ro < roleAdmin {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if p, err := s.rt.PlanServer(r.Context(), key); err != nil {
		out.PlanError = err.Error()
	} else {
		dto := humanPlan(p)
		out.Plan = &dto
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handlePause(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	id, _, err := s.requireRole(r, key, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req pauseRequest
	if r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &req); err != nil {
			s.fail(w, err)
			return
		}
	}
	if len([]rune(req.Reason)) > runtime.MaxPauseReason {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "the reason is longer than %d characters", runtime.MaxPauseReason))
		return
	}
	p, changed, err := s.rt.Pause(r.Context(), key, pauseInfoOf(id, req.Reason))
	if err != nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "pause: %v", err))
		return
	}
	if changed {
		s.audit(r.Context(), state.AuditEntry{Server: key, Actor: id.auditActor(), Action: "enforcement.pause", Target: key,
			Details: mustJSON(map[string]any{"reason": p.Reason})})
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": p, "changed": changed})
}

func (s *server) handleResume(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	id, _, err := s.requireRole(r, key, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	prev, changed, err := s.resume(r, id, key)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"resumed": changed}
	if changed {
		out["pause"] = prev
	}
	writeJSON(w, http.StatusOK, out)
}

// resume ends the pause of a server, with its audit entry.
func (s *server) resume(r *http.Request, id *identity, key string) (runtime.PauseInfo, bool, error) {
	name := ""
	if !id.bearer && !id.setup {
		name = id.user.DisplayName()
	}
	prev, changed, err := s.rt.Resume(r.Context(), key, id.auditActor(), name)
	if err != nil {
		return prev, false, httpErr(http.StatusServiceUnavailable, "unavailable", "resume: %v", err)
	}
	if changed {
		s.audit(r.Context(), state.AuditEntry{Server: key, Actor: id.auditActor(), Action: "enforcement.resume", Target: key,
			Details: mustJSON(map[string]any{"pausedBy": prev.By, "pausedAt": prev.At, "reason": prev.Reason})})
	}
	return prev, changed, nil
}

// pausedError answers a sync refused because the server is paused.
func pausedError(w http.ResponseWriter, err error) bool {
	var pe *runtime.PausedError
	if !errors.As(err, &pe) {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]any{"error": humanAPIError{
		Code: "paused", Message: pe.Error(),
		Hint: `Resume enforcement first (POST /api/v1/servers/` + pe.Server + `/resume, cicord resume ` + pe.Server + `), or send {"resume": true} with this sync.`,
	}})
	return true
}
