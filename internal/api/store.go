package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/setup"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/store"
	"github.com/npjonath/codi/internal/writeback"
)

// Store endpoints (spec/api.md §11).

var registryNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (s *server) pluginStore() *store.Service {
	return s.opts.PluginStore
}

// requireAnyAdmin accepts bearer clients and admins of at least one server.
func (s *server) requireAnyAdmin(r *http.Request) (*identity, error) {
	id, err := s.requireAuth(r)
	if err != nil {
		return nil, err
	}
	if id.bearer {
		return id, nil
	}
	if err := s.requireRuntime(); err != nil {
		return nil, err
	}
	for _, k := range s.rt.ServerKeys() {
		if ro, _, err := s.roleIn(r.Context(), id, k, false); err == nil && ro >= roleAdmin {
			return id, nil
		}
	}
	return nil, httpErr(http.StatusForbidden, "forbidden", "requires the admin role on a server")
}

// requireProjectAdmin accepts bearer clients and admins of every managed server:
// codi.yaml changes affect the whole project.
func (s *server) requireProjectAdmin(r *http.Request) (*identity, error) {
	id, err := s.requireAuth(r)
	if err != nil {
		return nil, err
	}
	if id.bearer {
		return id, nil
	}
	if err := s.requireRuntime(); err != nil {
		return nil, err
	}
	keys := s.rt.ServerKeys()
	if len(keys) == 0 {
		return nil, httpErr(http.StatusForbidden, "forbidden", "requires the admin role on every server of the project")
	}
	for _, k := range keys {
		ro, _, err := s.roleIn(r.Context(), id, k, false)
		if err != nil {
			return nil, err
		}
		if ro < roleAdmin {
			return nil, httpErr(http.StatusForbidden, "forbidden", "changing plugin sources requires the admin role on every server of the project (missing on %s)", k)
		}
	}
	return id, nil
}

func (s *server) handleStore(w http.ResponseWriter, r *http.Request) {
	s.writeStore(w, r, false)
}

func (s *server) handleStoreRefresh(w http.ResponseWriter, r *http.Request) {
	s.writeStore(w, r, true)
}

func (s *server) writeStore(w http.ResponseWriter, r *http.Request, force bool) {
	if _, err := s.requireAnyAdmin(r); err != nil {
		s.fail(w, err)
		return
	}
	srcs, err := s.pluginStore().Sources(r.Context(), s.rt.Repo(), force)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": srcs})
}

func (s *server) handleStoreDetail(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAnyAdmin(r); err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.pluginStore().Detail(r.Context(), s.rt.Repo(), r.PathValue("source"), r.PathValue("plugin"))
	if err != nil {
		s.failStore(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// failStore maps store errors to API errors.
func (s *server) failStore(w http.ResponseWriter, err error) {
	var ue *store.UnavailableError
	switch {
	case errors.Is(err, store.ErrUnknownSource):
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown plugin source"))
	case errors.Is(err, store.ErrUnknownPlugin):
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown plugin"))
	case errors.As(err, &ue):
		s.failSetup(w, &setup.Error{Status: http.StatusBadGateway, Code: "registry_unavailable",
			Message: fmt.Sprintf("The %s source could not be read.", ue.Source),
			Hint:    "Refresh the sources, check the registry URL, or try again later.", Details: registry.Redact(ue.Err.Error())})
	default:
		s.fail(w, err)
	}
}

type storeInstallRequest struct {
	Source            string         `json:"source"`
	Plugin            string         `json:"plugin"`
	Bot               string         `json:"bot"`
	Instance          string         `json:"instance"`
	Config            map[string]any `json:"config"`
	Version           string         `json:"version"`
	Commit            string         `json:"commit"`
	Integrity         string         `json:"integrity"`
	ConfirmUnofficial bool           `json:"confirmUnofficial"`
}

func (s *server) handleStoreInstall(w http.ResponseWriter, r *http.Request) {
	serverKey := r.PathValue("server")
	id, _, err := s.requireRole(r, serverKey, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req storeInstallRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "no active configuration yet"))
		return
	}
	if req.Instance != "" && !registryNameRe.MatchString(req.Instance) {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "instance names use lowercase letters, digits and dashes"))
		return
	}
	if wb := repo.Project.Spec.WriteBack; wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in codi.yaml"))
		return
	}
	// Bot: the only bot managing the server, unless given.
	if req.Bot == "" {
		var bots []string
		for k, b := range repo.Bots {
			if slices.Contains(b.Spec.Servers, serverKey) {
				bots = append(bots, k)
			}
		}
		switch len(bots) {
		case 0:
			s.fail(w, httpErr(http.StatusConflict, "conflict", "no bot manages server %s", serverKey))
			return
		case 1:
			req.Bot = bots[0]
		default:
			slices.Sort(bots)
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "several bots manage %s (%s): choose one", serverKey, strings.Join(bots, ", ")))
			return
		}
	}

	var pkg setup.Package
	trust := registry.TrustOfficial
	if req.Source == config.OfficialRegistryName {
		e, ok := s.opts.Catalog.Get(req.Plugin)
		if !ok {
			s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown plugin"))
			return
		}
		if pkg, err = setup.CatalogPackage(s.opts.Catalog, e); err != nil {
			s.fail(w, err)
			return
		}
	} else {
		var reg *config.Registry
		for _, rg := range repo.Project.Spec.Registries {
			if rg.Name == req.Source {
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
				Message: fmt.Sprintf("%s comes from an unofficial source.", req.Plugin),
				Hint:    "Review what it requests and confirm that you trust this source."})
			return
		}
		if req.Version == "" || req.Commit == "" || req.Integrity == "" {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "version, commit and integrity of the reviewed plugin are required"))
			return
		}
		d, err := s.pluginStore().Detail(r.Context(), repo, req.Source, req.Plugin)
		if err != nil {
			s.failStore(w, err)
			return
		}
		if d.Version != req.Version || d.Commit != req.Commit || d.Integrity != req.Integrity {
			s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "changed",
				Message: fmt.Sprintf("%s changed since you reviewed it (now %s).", d.DisplayName, d.Version),
				Hint:    "Review it again, then install."})
			return
		}
		lock := d.Resolution.Entry
		pkg = setup.Package{Name: d.Name, DisplayName: d.DisplayName, Version: d.Version,
			From:         fmt.Sprintf("the %s registry (%s, %s)", req.Source, reg.URL, trust),
			Capabilities: d.Capabilities, PrivilegedIntents: d.PrivilegedIntents,
			Source: req.Source + "/" + req.Plugin, VersionRange: "^" + d.Version, Lock: &lock}
		if len(d.Commands) > 0 {
			pkg.FirstCommand = d.Commands[0].Name
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	in := setup.Installer{Runtime: s.rt, Writer: s.opts.Writer, Catalog: s.opts.Catalog, Timeout: s.opts.ActivationTimeout}
	if c := s.opts.Checker; c != nil {
		in.Probe, in.Secrets = c.Probe, c.Secrets
	}
	res, err := in.InstallPackage(r.Context(), setup.Target{Server: serverKey, Bot: req.Bot, Instance: req.Instance, Config: req.Config},
		pkg, "admin-ui", id.actor())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: serverKey, Actor: id.auditActor(), Action: "store.install",
		Target: req.Bot + "/" + res.Instance, Details: mustJSON(map[string]any{
			"source": req.Source, "plugin": req.Plugin, "version": pkg.Version, "commit": req.Commit,
			"trust": trust, "mode": res.Mode, "pullRequestUrl": res.PullRequestURL,
		})})
	writeJSON(w, http.StatusOK, res)
}

type registryWriteResult struct {
	Mode           string `json:"mode"`
	Revision       string `json:"revision,omitempty"`
	PullRequestURL string `json:"pullRequestUrl,omitempty"`
}

func (s *server) handleRegistryAdd(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireProjectAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var body config.Registry
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	body.Name, body.URL = strings.TrimSpace(body.Name), strings.TrimSpace(body.URL)
	switch {
	case !registryNameRe.MatchString(body.Name):
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry names use 1 to 32 lowercase letters, digits and dashes"))
		return
	case body.Name == config.OfficialRegistryName:
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "the name %q is reserved for the official CODI plugins", body.Name))
		return
	case !registry.IsRemoteURL(body.URL):
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry URLs must start with https://"))
		return
	case registry.HasUserinfo(body.URL):
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry URLs must not contain credentials (private registries are not supported yet)"))
		return
	}
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "no active configuration yet"))
		return
	}
	for _, rg := range repo.Project.Spec.Registries {
		if rg.Name == body.Name {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "a registry named %q already exists", body.Name))
			return
		}
		if config.NormalizeRegistryURL(rg.URL) == config.NormalizeRegistryURL(body.URL) {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "this URL is already the registry %q", rg.Name))
			return
		}
	}
	if err := registry.CheckPublicHost(r.Context(), body.URL); err != nil {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry URLs must point to a public host (%v); declare registries on your own network in codi.yaml", err))
		return
	}
	if err := s.pluginStore().Check(r.Context(), body.URL); err != nil {
		se := store.ExplainFetch(err)
		s.failSetup(w, &setup.Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: se.Message, Hint: se.Hint, Details: se.Details})
		return
	}
	reg := body
	edit := func(cur []byte) ([]byte, error) {
		if cur == nil {
			return nil, errors.New("codi.yaml does not exist")
		}
		return writeback.AppendYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("registries")}, reg)
	}
	s.writeProject(w, r, id, edit, fmt.Sprintf("registry: add %s\n\nURL: %s (%s)", reg.Name, reg.URL, registry.Trust(reg.URL)),
		"registry.add", reg.Name, map[string]any{"url": reg.URL, "trust": registry.Trust(reg.URL)})
}

func (s *server) handleRegistryRemove(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireProjectAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	name := r.PathValue("name")
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "no active configuration yet"))
		return
	}
	if !slices.ContainsFunc(repo.Project.Spec.Registries, func(rg config.Registry) bool { return rg.Name == name }) {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown registry %q", name))
		return
	}
	var used []string
	for bk, b := range repo.Bots {
		for _, p := range b.Spec.Plugins {
			if strings.HasPrefix(p.Source, name+"/") {
				used = append(used, bk+"/"+p.Name)
			}
		}
	}
	if lock, err := registry.ReadLock(repo.Root); err == nil {
		for src := range lock.Plugins {
			if strings.HasPrefix(src, name+"/") {
				used = append(used, registry.LockFile+": "+src)
			}
		}
	}
	if len(used) > 0 {
		slices.Sort(used)
		s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "in_use",
			Message: fmt.Sprintf("The %s source is still used.", name),
			Hint:    "Uninstall its plugins first.", Details: strings.Join(used, "\n")})
		return
	}
	edit := func(cur []byte) ([]byte, error) {
		if cur == nil {
			return nil, errors.New("codi.yaml does not exist")
		}
		return writeback.SetYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("registries"), writeback.Match("name", name)}, nil)
	}
	s.writeProject(w, r, id, edit, "registry: remove "+name, "registry.remove", name, map[string]any{})
}

// writeProject writes codi.yaml through write-back and audits the change on every server.
func (s *server) writeProject(w http.ResponseWriter, r *http.Request, id *identity, edit writeback.EditFunc,
	message, action, target string, details map[string]any) {
	if s.opts.Writer == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back is not configured"))
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	repo := s.rt.Repo()
	wb := repo.Project.Spec.WriteBack
	if wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in codi.yaml"))
		return
	}
	rev, _ := s.rt.Revision()
	mode := writeback.ModeFor(wb, []string{"codi.yaml"})
	prevErr := s.rt.Status().LastError
	res, err := s.opts.Writer.Write(r.Context(), writeback.Request{
		Edits:   map[string]writeback.EditFunc{"codi.yaml": edit},
		Message: message, Actor: id.actor(), Source: "admin-ui", Mode: mode,
	})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			s.fail(w, httpErr(http.StatusConflict, "conflict", "%v", err))
			return
		}
		s.log.Error("write-back failed", "err", err)
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back failed: %v", err))
		return
	}
	if res.Mode == "" {
		res.Mode = mode
	}
	details["mode"], details["revision"], details["pullRequestUrl"] = res.Mode, res.Revision, res.PullRequestURL
	for _, k := range s.rt.ServerKeys() {
		s.audit(r.Context(), state.AuditEntry{Server: k, Actor: id.auditActor(), Action: action, Target: target, Details: mustJSON(details)})
	}
	out := registryWriteResult{Mode: res.Mode, Revision: res.Revision, PullRequestURL: res.PullRequestURL}
	if res.Mode == writeback.ModeCommit {
		s.rt.Trigger()
		if nr, ok := s.waitActivation(r.Context(), rev.SHA, res.SHA, prevErr); ok {
			out.Revision = nr.ID
		}
	} else {
		out.Revision = ""
	}
	writeJSON(w, http.StatusOK, out)
}
