package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/npjonath/codi/internal/catalog"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/setup"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/store"
)

// Plugin updates (spec/api.md §11): review the latest version against what is installed,
// then update every instance of the plugin in one write-back.

// updateReviewDTO is GET /store/{source}/{plugin}/update.
type updateReviewDTO struct {
	// Available is false when the installed version is the latest.
	Available bool              `json:"available"`
	Update    *store.UpdateInfo `json:"update"`
	// Detail is the latest version (what the update installs), as GET /store/{source}/{plugin}.
	Detail store.Detail `json:"detail"`
	*setup.UpdatePlan
	// ConfigValid is false when an instance's config is invalid for the new version.
	ConfigValid bool `json:"configValid"`
	// MissingAdmin lists the servers running the plugin where the caller is not admin
	// (the update is refused while it is not empty).
	MissingAdmin []string `json:"missingAdmin"`
	// Restricted: for each server of MissingAdmin, the number of instances there (their
	// details are not returned: they belong to another server's admins).
	Restricted []restrictedServer `json:"restricted"`
}

// updateSpec resolves the latest version of an installed Store plugin.
func (s *server) updateSpec(ctx context.Context, repo *config.Repo, source, name string) (store.Detail, setup.UpdateSpec, error) {
	d, err := s.pluginStore().Detail(ctx, repo, source, name)
	if err != nil {
		return store.Detail{}, setup.UpdateSpec{}, err
	}
	spec := setup.UpdateSpec{Source: source + "/" + name, Name: d.Name, DisplayName: d.DisplayName, Version: d.Version,
		Capabilities: d.Capabilities, Intents: d.Intents, PrivilegedIntents: d.PrivilegedIntents, Network: d.Network, Schema: d.Schema}
	if source == config.OfficialRegistryName {
		files, err := s.catalogOf().PluginFiles(name)
		if err != nil {
			return store.Detail{}, setup.UpdateSpec{}, store.ErrUnknownPlugin
		}
		spec.Files = files
	} else {
		lock := d.Resolution.Entry
		spec.Lock = &lock
	}
	return d, spec, nil
}

func (s *server) catalogOf() *catalog.Catalog {
	if c := s.pluginStore().Catalog; c != nil {
		return c
	}
	return s.opts.Catalog
}

// missingAdmin lists the servers where id is not admin (none for bearer clients).
func (s *server) missingAdmin(ctx context.Context, id *identity, servers []string) []string {
	out := []string{}
	for _, k := range servers {
		if ro, _, err := s.roleIn(ctx, id, k, false); err != nil || ro < roleAdmin {
			out = append(out, k)
		}
	}
	return out
}

// copyStatus checks the bundled copy of plugin name in the active revision.
func (s *server) copyStatus(repo *config.Repo, name string) (catalog.CopyStatus, error) {
	return s.catalogOf().CheckCopy(name, filepath.Join(repo.Root, filepath.FromSlash(catalog.PluginDir(name))))
}

func (s *server) handleStoreUpdateReview(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAnyAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "no active configuration yet"))
		return
	}
	source, name := r.PathValue("source"), r.PathValue("plugin")
	if len(setup.PluginServers(repo, source+"/"+name)) == 0 {
		s.fail(w, httpErr(http.StatusNotFound, "not_installed", "%s/%s is not installed", source, name))
		return
	}
	d, spec, err := s.updateSpec(r.Context(), repo, source, name)
	if err != nil {
		s.failStore(w, err)
		return
	}
	plan, err := setup.PlanUpdate(repo, spec)
	if err != nil {
		s.failPlan(w, source, name, err)
		return
	}
	if spec.Bundled() {
		st, err := s.copyStatus(repo, name)
		if err != nil {
			s.fail(w, err)
			return
		}
		plan.Modified = st.Modified
	}
	// Instances (their config diagnostics, grants and ranges) are only shown for servers
	// where the caller is admin: for the others, only how many instances block the update.
	missing := s.missingAdmin(r.Context(), id, plan.Servers)
	restricted := []restrictedServer{}
	if len(missing) > 0 {
		visible := []setup.UpdateInstance{}
		for _, ui := range plan.Instances {
			if !slices.Contains(missing, ui.Server) {
				visible = append(visible, ui)
				continue
			}
			if i := slices.IndexFunc(restricted, func(r restrictedServer) bool { return r.Server == ui.Server }); i >= 0 {
				restricted[i].Instances++
			} else {
				restricted = append(restricted, restrictedServer{Server: ui.Server, Instances: 1})
			}
		}
		plan.Instances = visible
		d.Installed = slices.DeleteFunc(d.Installed, func(i store.Installation) bool { return slices.Contains(missing, i.Server) })
	}
	writeJSON(w, http.StatusOK, updateReviewDTO{Available: d.Update != nil, Update: d.Update, Detail: d, UpdatePlan: plan,
		ConfigValid: plan.ConfigValid(), MissingAdmin: missing, Restricted: restricted})
}

// restrictedServer is a server where the caller is not admin: its instances are not shown.
type restrictedServer struct {
	Server    string `json:"server"`
	Instances int    `json:"instances"`
}

func (s *server) failPlan(w http.ResponseWriter, source, name string, err error) {
	if errors.Is(err, setup.ErrNotInstalled) {
		s.fail(w, httpErr(http.StatusNotFound, "not_installed", "%s/%s is not installed", source, name))
		return
	}
	s.failSetup(w, &setup.Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
		Message: "The update cannot be computed.", Hint: "See the details.", Details: registry.Redact(err.Error())})
}

type storeUpdateRequest struct {
	// Version, Commit and Integrity of the reviewed version (commit: registry plugins).
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	Integrity         string `json:"integrity"`
	ConfirmUnofficial bool   `json:"confirmUnofficial"`
	// Force overwrites a locally modified copy of an official plugin.
	Force bool `json:"force"`
}

func (s *server) handleStoreUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAnyAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req storeUpdateRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "no active configuration yet"))
		return
	}
	source, name := r.PathValue("source"), r.PathValue("plugin")
	ref := source + "/" + name

	// The version is shared by every instance: admin on every server where one runs.
	servers := setup.PluginServers(repo, ref)
	if len(servers) == 0 {
		s.fail(w, httpErr(http.StatusNotFound, "not_installed", "%s is not installed", ref))
		return
	}
	if missing := s.missingAdmin(r.Context(), id, servers); len(missing) > 0 {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden",
			"updating %s changes every server where it runs: it requires the admin role on each of them (missing on %s)", ref, strings.Join(missing, ", ")))
		return
	}
	if wb := repo.Project.Spec.WriteBack; wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in codi.yaml"))
		return
	}

	trust := registry.TrustOfficial
	if source != config.OfficialRegistryName {
		var reg *config.Registry
		for _, rg := range repo.Project.Spec.Registries {
			if rg.Name == source {
				reg = &rg
				break
			}
		}
		if reg == nil {
			s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown plugin source"))
			return
		}
		trust = registry.Trust(reg.URL)
		if trust != registry.TrustOfficial && !req.ConfirmUnofficial {
			s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "confirmation_required",
				Message: fmt.Sprintf("%s comes from an unofficial source.", name),
				Hint:    "Review what the new version requests and confirm that you trust this source."})
			return
		}
		if req.Version == "" || req.Commit == "" || req.Integrity == "" {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "version, commit and integrity of the reviewed version are required"))
			return
		}
	} else if req.Version == "" {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "version of the reviewed version is required"))
		return
	}

	d, spec, err := s.updateSpec(r.Context(), repo, source, name)
	if err != nil {
		s.failStore(w, err)
		return
	}
	if d.Version != req.Version || (spec.Lock != nil && (d.Commit != req.Commit || d.Integrity != req.Integrity)) ||
		(spec.Lock == nil && req.Integrity != "" && req.Integrity != d.Integrity) {
		s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "changed",
			Message: fmt.Sprintf("%s changed since you reviewed it (now %s).", d.DisplayName, d.Version),
			Hint:    "Review the update again."})
		return
	}
	if d.Update == nil {
		s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "up_to_date",
			Message: fmt.Sprintf("%s %s is already installed.", d.DisplayName, d.Version)})
		return
	}
	plan, err := setup.PlanUpdate(repo, spec)
	if err != nil {
		s.failPlan(w, source, name, err)
		return
	}
	if spec.Bundled() {
		st, err := s.copyStatus(repo, name)
		if err != nil {
			s.fail(w, err)
			return
		}
		if st.Modified && !req.Force {
			var lines []string
			for _, p := range st.Changed {
				lines = append(lines, "changed: "+p)
			}
			for _, p := range st.Removed {
				lines = append(lines, "deleted by the update: "+p)
			}
			s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "modified",
				Message: fmt.Sprintf("The copy of %s in %s/ was modified in the repository.", d.DisplayName, catalog.PluginDir(name)),
				Hint:    "Updating overwrites the local changes: keep them elsewhere, then force the update.", Details: strings.Join(lines, "\n")})
			return
		}
		plan.Modified = st.Modified
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	in := setup.Installer{Runtime: s.rt, Writer: s.opts.Writer, Catalog: s.opts.Catalog, Timeout: s.opts.ActivationTimeout}
	if c := s.opts.Checker; c != nil {
		in.Probe, in.Secrets = c.Probe, c.Secrets
	}
	res, err := in.Update(r.Context(), plan, "admin-ui", id.actor())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	details := mustJSON(map[string]any{
		"source": source, "plugin": name, "from": plan.Current.Version, "to": res.Version, "commit": d.Commit, "trust": trust,
		"capabilitiesAdded": plan.Diff.Capabilities.Added, "capabilitiesRemoved": plan.Diff.Capabilities.Removed,
		"forced": req.Force && plan.Modified, "mode": res.Mode, "revision": res.Revision, "pullRequestUrl": res.PullRequestURL,
	})
	for _, k := range plan.Servers {
		s.audit(r.Context(), state.AuditEntry{Server: k, Actor: id.auditActor(), Action: "plugin.update", Target: ref, Details: details})
	}
	writeJSON(w, http.StatusOK, res)
}
