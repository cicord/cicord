package api

import (
	"net/http"
	"slices"
)

func (s *server) handleInterop(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleAdmin); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.rt.InteropGraph(key))
}
func (s *server) confirmServerGrants(w http.ResponseWriter, caps []string, confirmed bool) bool {
	if confirmed {
		return true
	}
	for _, c := range []string{"server.overlays", "server.incident", "server.sync"} {
		if slices.Contains(caps, c) {
			s.fail(w, httpErr(http.StatusConflict, "confirmation_required", "confirm the privileged server capability %s", c))
			return false
		}
	}
	return true
}
