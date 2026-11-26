package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/cicord/cicord/internal/discord"
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

// The event payload may describe a private channel. Require current server-wide
// Discord read authority before returning it; the queue itself stays available
// to all Cicord admins for retry and discard.
func (s *server) handleDeadEvent(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	viewer, _, err := s.requireRole(r, key, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	info, ok := s.rt.ServerInfo(key)
	allowed := viewer.bearer || (ok && viewer.user.ID != "" && info.OwnerID == viewer.user.ID)
	if !allowed && viewer.user.ID != "" {
		m, e := s.lookupMember(r.Context(), key, viewer.user.ID, true)
		if e != nil {
			s.fail(w, e)
			return
		}
		if m != nil {
			roles, _, _, ok := s.rt.ServerResources(key)
			allowed = ok && slices.ContainsFunc(roles, func(role discord.Role) bool {
				return (role.ID == info.ID || m.HasRole(role.ID)) && role.Permissions.Has(discord.MustPermissions("Administrator"))
			})
		}
	}
	if !allowed {
		s.fail(w, httpErr(403, "forbidden", "reading raw event data requires Discord Administrator access"))
		return
	}
	data, err := s.rt.Store().Interop().DeadEvent(r.Context(), key, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(data) == 0 {
		s.fail(w, httpErr(404, "not_found", "no such event dead letter"))
		return
	}
	if gate, ok := any(s.rt).(interface {
		ErasedPayload(context.Context, string, json.RawMessage) (bool, error)
	}); ok {
		erased, err := gate.ErasedPayload(r.Context(), key, data)
		if err != nil {
			s.fail(w, err)
			return
		}
		if erased {
			s.fail(w, httpErr(404, "not_found", "event data erased"))
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, data)
}
