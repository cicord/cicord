package api

import (
	"fmt"
	"net/http"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/setup"
)

// officialOrigin tells an install of source "cicord" whether the plugin comes from the
// official remote registry (store.Service.OfficialOrigin, like the plugin's Store page) or
// from the binary. The install must match what was reviewed: a reviewed commit means the
// remote version, none the bundled one; when the origin changed in between, the install
// is refused (409 changed).
// ok is false when the response was written.
func (s *server) officialOrigin(w http.ResponseWriter, r *http.Request, source, plugin, reviewedCommit string) (reg config.Registry, remote, ok bool) {
	if source != config.OfficialRegistryName {
		return config.Registry{}, false, true
	}
	reg, remote, err := s.pluginStore().OfficialOrigin(r.Context(), s.rt.Repo(), plugin)
	if err != nil {
		s.failStore(w, err)
		return config.Registry{}, false, false
	}
	if _, configured := registry.Official(); configured && remote != (reviewedCommit != "") {
		s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "changed",
			Message: fmt.Sprintf("%s changed since you reviewed it.", plugin),
			Hint:    "Review it again, then install."})
		return config.Registry{}, false, false
	}
	return reg, remote, true
}
