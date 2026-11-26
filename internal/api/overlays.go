package api

import "net/http"

func (s *server) handleOverlays(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleAdmin); err != nil {
		s.fail(w, err)
		return
	}
	rows, err := s.rt.Overlays(r.Context(), key, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}
func (s *server) handleRemoveOverlay(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	id, _, err := s.requireRole(r, key, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err = s.rt.RemoveOverlay(r.Context(), key, r.PathValue("bot")+"/"+r.PathValue("instance"), r.PathValue("id"), id.auditActor()); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
