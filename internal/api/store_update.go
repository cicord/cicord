package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/store"
)

// Plugin updates (https://cicord.dev/docs/reference/http-api/store/): review the latest version against what is installed,
// then update every instance of the plugin in one write-back.

// updateReviewDTO is GET /store/{source}/{plugin}/update.
type updateReviewDTO struct {
	// Source and Plugin name the installed plugin ("<source>/<plugin>").
	Source string `json:"source"`
	Plugin string `json:"plugin"`
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

// updateRegistry is the registry an installed plugin updates from: a registry of cicord.yaml,
// the implicit official registry for "cicord/<plugin>" locked in cicord.lock, or nil for a
// copy of a bundled official plugin (plugins/<plugin>/, updated from the binary).
func updateRegistry(repo *config.Repo, source, name string) (*config.Registry, error) {
	if source == config.OfficialRegistryName {
		if !setup.LockedSource(repo, source+"/"+name) {
			return nil, nil
		}
		// Official only through the official URL, whose index must carry a valid
		// signature (registry.FetchIndex refuses it otherwise).
		off, ok := registry.Official()
		if !ok {
			return nil, store.ErrUnknownSource
		}
		return &off, nil
	}
	for _, rg := range repo.Project.Spec.Registries {
		if rg.Name == source {
			return &rg, nil
		}
	}
	return nil, store.ErrUnknownSource
}

// updateSpec resolves the latest version of an installed Store plugin (reg: see
// updateRegistry).
func (s *server) updateSpec(ctx context.Context, repo *config.Repo, reg *config.Registry, source, name string) (store.Detail, setup.UpdateSpec, error) {
	var d store.Detail
	var err error
	if reg == nil {
		d, err = s.pluginStore().BundledDetail(repo, name)
	} else {
		d, err = s.pluginStore().RegistryDetail(ctx, repo, *reg, name)
	}
	if err != nil {
		return store.Detail{}, setup.UpdateSpec{}, err
	}
	spec := setup.UpdateSpec{SecretDeclarations: d.SecretDeclarations, Source: source + "/" + name, Name: d.Name, DisplayName: d.DisplayName, Version: d.Version,
		Capabilities: d.Capabilities, Intents: d.Intents, PrivilegedIntents: d.PrivilegedIntents, Network: d.Network, Optional: d.Optional, Schema: d.Schema}
	if reg == nil {
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
	dto, err := s.updateReview(r.Context(), id, repo, r.PathValue("source"), r.PathValue("plugin"))
	if err != nil {
		s.failUpdate(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// updatesReviewDTO is GET /store/updates: the review of every available update, all
// computed against the same configuration (POST /store/updates applies them together).
type updatesReviewDTO struct {
	Items []updateReviewDTO `json:"items"`
	// Unavailable: plugins with an update whose review cannot be computed (an unreadable
	// source, for example).
	Unavailable []updateUnavailable `json:"unavailable"`
}

type updateUnavailable struct {
	Source  string `json:"source"`
	Plugin  string `json:"plugin"`
	Message string `json:"message"`
}

func (s *server) handleStoreUpdatesReview(w http.ResponseWriter, r *http.Request) {
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
	out := updatesReviewDTO{Items: []updateReviewDTO{}, Unavailable: []updateUnavailable{}}
	ups := s.pluginStore().Updates(r.Context(), repo, store.FetchMissing)
	for _, ref := range slices.Sorted(maps.Keys(ups)) {
		source, name, ok := strings.Cut(ref, "/")
		if !ok {
			continue
		}
		dto, err := s.updateReview(r.Context(), id, repo, source, name)
		if err != nil {
			out.Unavailable = append(out.Unavailable, updateUnavailable{Source: source, Plugin: name, Message: updateErrMessage(err)})
			continue
		}
		if dto.Available {
			out.Items = append(out.Items, dto)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// updateErrMessage is the message of an update error, as the API would answer it.
func updateErrMessage(err error) string {
	var se *setup.Error
	var he *errHTTP
	var ue *store.UnavailableError
	switch {
	case errors.As(err, &se):
		return se.Message
	case errors.As(err, &he):
		return he.msg
	case errors.As(err, &ue):
		return fmt.Sprintf("The %s source could not be read.", ue.Source)
	case errors.Is(err, store.ErrUnknownSource):
		return "unknown plugin source"
	case errors.Is(err, store.ErrUnknownPlugin):
		return "unknown plugin"
	}
	return "internal error"
}

// updateReview computes the review of the update of an installed plugin against repo.
func (s *server) updateReview(ctx context.Context, id *identity, repo *config.Repo, source, name string) (updateReviewDTO, error) {
	if len(setup.PluginServers(repo, source+"/"+name)) == 0 {
		return updateReviewDTO{}, httpErr(http.StatusNotFound, "not_installed", "%s/%s is not installed", source, name)
	}
	reg, err := updateRegistry(repo, source, name)
	if err != nil {
		return updateReviewDTO{}, err
	}
	d, spec, err := s.updateSpec(ctx, repo, reg, source, name)
	if err != nil {
		return updateReviewDTO{}, err
	}
	plan, err := setup.PlanUpdate(repo, spec)
	if err != nil {
		return updateReviewDTO{}, planErr(source, name, err)
	}
	if spec.Bundled() {
		st, err := s.copyStatus(repo, name)
		if err != nil {
			return updateReviewDTO{}, err
		}
		plan.Modified = st.Modified
	}
	// Instances (their config diagnostics, grants and ranges) are only shown for servers
	// where the caller is admin: for the others, only how many instances block the update.
	missing := s.missingAdmin(ctx, id, plan.Servers)
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
		plan.SecretReview = ""
		plan.Instances = visible
		d.Installed = slices.DeleteFunc(d.Installed, func(i store.Installation) bool { return slices.Contains(missing, i.Server) })
	}
	return updateReviewDTO{Source: source, Plugin: name, Available: d.Update != nil, Update: d.Update, Detail: d, UpdatePlan: plan,
		ConfigValid: plan.ConfigValid(), MissingAdmin: missing, Restricted: restricted}, nil
}

// restrictedServer is a server where the caller is not admin: its instances are not shown.
type restrictedServer struct {
	Server    string `json:"server"`
	Instances int    `json:"instances"`
}

// planErr is the error of a plan that cannot be computed.
func planErr(source, name string, err error) error {
	if errors.Is(err, setup.ErrNotInstalled) {
		return httpErr(http.StatusNotFound, "not_installed", "%s/%s is not installed", source, name)
	}
	return &setup.Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
		Message: "The update cannot be computed.", Hint: "See the details.", Details: registry.Redact(err.Error())}
}

// failUpdate answers the error of an update: a setup error, a Store error or an API error.
func (s *server) failUpdate(w http.ResponseWriter, err error) {
	var se *setup.Error
	if errors.As(err, &se) {
		s.failSetup(w, err)
		return
	}
	s.failStore(w, err)
}

type storeUpdateRequest struct {
	ConfirmSecretChanges string `json:"confirmSecretChanges"`
	// Version, Commit and Integrity of the reviewed version (commit: registry plugins).
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	Integrity         string `json:"integrity"`
	ConfirmPrivileged bool   `json:"confirmPrivileged"`
	ConfirmUnofficial bool   `json:"confirmUnofficial"`
	// Force overwrites a locally modified copy of an official plugin.
	Force bool `json:"force"`
}

// storeUpdateItem is one plugin of POST /store/updates: what its review showed.
type storeUpdateItem struct {
	Source               string `json:"source"`
	Plugin               string `json:"plugin"`
	ConfirmSecretChanges string `json:"confirmSecretChanges"`
	Version              string `json:"version"`
	Commit               string `json:"commit"`
	Integrity            string `json:"integrity"`
	Force                bool   `json:"force"`
}

// storeUpdatesRequest is POST /store/updates: several plugins updated in one change.
type storeUpdatesRequest struct {
	Updates           []storeUpdateItem `json:"updates"`
	ConfirmPrivileged bool              `json:"confirmPrivileged"`
	ConfirmUnofficial bool              `json:"confirmUnofficial"`
}

// maxBatchUpdates bounds POST /store/updates.
const maxBatchUpdates = 64

// preparedUpdate is an update whose review was checked against the latest version.
type preparedUpdate struct {
	ref    string
	plan   *setup.UpdatePlan
	detail store.Detail
	trust  string
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
	s.applyUpdates(w, r, id, []storeUpdateItem{{Source: r.PathValue("source"), Plugin: r.PathValue("plugin"), ConfirmSecretChanges: req.ConfirmSecretChanges,
		Version: req.Version, Commit: req.Commit, Integrity: req.Integrity, Force: req.Force}}, req.ConfirmPrivileged, req.ConfirmUnofficial)
}

func (s *server) handleStoreUpdates(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAnyAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req storeUpdatesRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	switch {
	case len(req.Updates) == 0:
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "updates is empty"))
		return
	case len(req.Updates) > maxBatchUpdates:
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "at most %d updates at once", maxBatchUpdates))
		return
	}
	seen := map[string]bool{}
	for _, it := range req.Updates {
		ref := it.Source + "/" + it.Plugin
		if it.Source == "" || it.Plugin == "" || strings.Contains(it.Plugin, "/") {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "source and plugin are required"))
			return
		}
		if seen[ref] {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "%s is listed twice", ref))
			return
		}
		seen[ref] = true
	}
	s.applyUpdates(w, r, id, req.Updates, req.ConfirmPrivileged, req.ConfirmUnofficial)
}

// applyUpdates checks each reviewed update against the latest version (every check of a
// single update, per plugin) and applies them all in one change, or none.
func (s *server) applyUpdates(w http.ResponseWriter, r *http.Request, id *identity, items []storeUpdateItem, confirmPrivileged, confirmUnofficial bool) {
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "no active configuration yet"))
		return
	}
	if wb := repo.Project.Spec.WriteBack; wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in cicord.yaml"))
		return
	}
	prepared := make([]preparedUpdate, 0, len(items))
	var added []string
	for _, it := range items {
		p, err := s.prepareUpdate(r.Context(), id, repo, it, confirmUnofficial)
		if err != nil {
			s.failUpdate(w, err)
			return
		}
		prepared = append(prepared, p)
		for _, c := range p.plan.Diff.Capabilities.Added {
			if !slices.Contains(added, c) {
				added = append(added, c)
			}
		}
	}
	if !s.confirmServerGrants(w, added, confirmPrivileged) {
		return
	}
	plans := make([]*setup.UpdatePlan, 0, len(prepared))
	refs := make([]string, 0, len(prepared))
	for _, p := range prepared {
		plans = append(plans, p.plan)
		refs = append(refs, p.ref)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	in := setup.Installer{Runtime: s.rt, Writer: s.opts.Writer, Catalog: s.opts.Catalog, Timeout: s.opts.ActivationTimeout}
	if c := s.opts.Checker; c != nil {
		in.Probe, in.Secrets = c.Probe, c.Secrets
	}
	res, err := in.UpdateAll(r.Context(), plans, "admin-ui", id.actor())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	for _, p := range prepared {
		source, name, _ := strings.Cut(p.ref, "/")
		d := map[string]any{
			"source": source, "plugin": name, "from": p.plan.Current.Version, "to": p.detail.Version, "commit": p.detail.Commit, "trust": p.trust,
			"capabilitiesAdded": p.plan.Diff.Capabilities.Added, "capabilitiesRemoved": p.plan.Diff.Capabilities.Removed,
			"approvedSecretReview": p.plan.SecretReview,
			"forced":               p.plan.Modified, "mode": res.Mode, "revision": res.Revision, "pullRequestUrl": res.PullRequestURL,
		}
		if len(prepared) > 1 {
			d["batch"] = refs
		}
		details := mustJSON(d)
		for _, k := range p.plan.Servers {
			s.audit(r.Context(), state.AuditEntry{Server: k, Actor: id.auditActor(), Action: "plugin.update", Target: p.ref, Details: details})
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// prepareUpdate checks one reviewed update: admin on every server running the plugin, a
// trusted (or confirmed) source, the reviewed version still the latest, a modified copy
// only when forced, and the credential review confirmed.
func (s *server) prepareUpdate(ctx context.Context, id *identity, repo *config.Repo, it storeUpdateItem, confirmUnofficial bool) (preparedUpdate, error) {
	source, name := it.Source, it.Plugin
	ref := source + "/" + name

	// The version is shared by every instance: admin on every server where one runs.
	servers := setup.PluginServers(repo, ref)
	if len(servers) == 0 {
		return preparedUpdate{}, httpErr(http.StatusNotFound, "not_installed", "%s is not installed", ref)
	}
	if missing := s.missingAdmin(ctx, id, servers); len(missing) > 0 {
		return preparedUpdate{}, httpErr(http.StatusForbidden, "forbidden",
			"updating %s changes every server where it runs: it requires the admin role on each of them (missing on %s)", ref, strings.Join(missing, ", "))
	}

	trust := registry.TrustOfficial
	reg, err := updateRegistry(repo, source, name)
	if err != nil {
		return preparedUpdate{}, err
	}
	if reg != nil {
		trust = registry.Trust(reg.URL)
		if trust != registry.TrustOfficial && !confirmUnofficial {
			return preparedUpdate{}, &setup.Error{Status: http.StatusConflict, Code: "confirmation_required",
				Message: fmt.Sprintf("%s comes from an unofficial source.", name),
				Hint:    "Review what the new version requests and confirm that you trust this source."}
		}
		if it.Version == "" || it.Commit == "" || it.Integrity == "" {
			return preparedUpdate{}, httpErr(http.StatusBadRequest, "invalid", "%s: version, commit and integrity of the reviewed version are required", ref)
		}
	} else if it.Version == "" {
		return preparedUpdate{}, httpErr(http.StatusBadRequest, "invalid", "%s: version of the reviewed version is required", ref)
	}

	d, spec, err := s.updateSpec(ctx, repo, reg, source, name)
	if err != nil {
		return preparedUpdate{}, err
	}
	if d.Version != it.Version || (spec.Lock != nil && (d.Commit != it.Commit || d.Integrity != it.Integrity)) ||
		(spec.Lock == nil && it.Integrity != "" && it.Integrity != d.Integrity) {
		return preparedUpdate{}, &setup.Error{Status: http.StatusConflict, Code: "changed",
			Message: fmt.Sprintf("%s changed since you reviewed it (now %s).", d.DisplayName, d.Version),
			Hint:    "Review the update again."}
	}
	if d.Update == nil {
		return preparedUpdate{}, &setup.Error{Status: http.StatusConflict, Code: "up_to_date",
			Message: fmt.Sprintf("%s %s is already installed.", d.DisplayName, d.Version)}
	}
	plan, err := setup.PlanUpdate(repo, spec)
	if err != nil {
		return preparedUpdate{}, planErr(source, name, err)
	}
	if spec.Bundled() {
		st, err := s.copyStatus(repo, name)
		if err != nil {
			return preparedUpdate{}, err
		}
		if st.Modified && !it.Force {
			var lines []string
			for _, p := range st.Changed {
				lines = append(lines, "changed: "+p)
			}
			for _, p := range st.Removed {
				lines = append(lines, "deleted by the update: "+p)
			}
			return preparedUpdate{}, &setup.Error{Status: http.StatusConflict, Code: "modified",
				Message: fmt.Sprintf("The copy of %s in %s/ was modified in the repository.", d.DisplayName, catalog.PluginDir(name)),
				Hint:    "Updating overwrites the local changes: keep them elsewhere, then force the update.", Details: strings.Join(lines, "\n")}
		}
		plan.Modified = st.Modified
	}
	if !plan.AcceptsSecretReview(it.ConfirmSecretChanges) {
		return preparedUpdate{}, &setup.Error{Status: http.StatusConflict, Code: "confirmation_required",
			Message: fmt.Sprintf("The update of %s expands access to an existing plugin credential.", d.DisplayName),
			Hint:    "Review the changed credential hosts and provider limits for every installation, then confirm that review."}
	}
	return preparedUpdate{ref: ref, plan: plan, detail: d, trust: trust}, nil
}
