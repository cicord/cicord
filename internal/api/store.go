package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/store"
	"github.com/cicord/cicord/internal/writeback"
)

// Store endpoints (https://cicord.dev/docs/reference/http-api/store/).

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
// cicord.yaml changes affect the whole project.
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

// storeSourceDTO is a Source with its plugin count; Plugins is omitted with ?summary=1.
type storeSourceDTO struct {
	store.Source
	PluginCount int            `json:"pluginCount"`
	Plugins     []store.Plugin `json:"plugins,omitempty"`
}

// adminOf returns a predicate telling whether the caller is admin of a server (bearer:
// every server). Store listings only show instances on those servers: an admin of one
// server must not learn what runs on the others.
func (s *server) adminOf(ctx context.Context, id *identity) func(string) bool {
	if id.bearer {
		return func(string) bool { return true }
	}
	seen := map[string]bool{}
	return func(server string) bool {
		ok, done := seen[server]
		if !done {
			ro, _, err := s.roleIn(ctx, id, server, false)
			ok = err == nil && ro >= roleAdmin
			seen[server] = ok
		}
		return ok
	}
}

func visibleInstalled(in []store.Installation, ok func(string) bool) []store.Installation {
	out := []store.Installation{}
	for _, i := range in {
		if ok(i.Server) {
			out = append(out, i)
		}
	}
	return out
}

// visibleSources applies visibleInstalled to every plugin.
func visibleSources(srcs []store.Source, ok func(string) bool) {
	for si := range srcs {
		for pi := range srcs[si].Plugins {
			srcs[si].Plugins[pi].Installed = visibleInstalled(srcs[si].Plugins[pi].Installed, ok)
		}
	}
}

func (s *server) writeStore(w http.ResponseWriter, r *http.Request, force bool) {
	id, err := s.requireAnyAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	srcs, err := s.pluginStore().Sources(r.Context(), s.rt.Repo(), force)
	if err != nil {
		s.fail(w, err)
		return
	}
	visibleSources(srcs, s.adminOf(r.Context(), id))
	summary := r.URL.Query().Get("summary") == "1"
	out := make([]storeSourceDTO, 0, len(srcs))
	for _, src := range srcs {
		d := storeSourceDTO{Source: src, PluginCount: len(src.Plugins), Plugins: src.Plugins}
		if summary {
			d.Plugins = nil
		} else if d.Plugins == nil {
			d.Plugins = []store.Plugin{}
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out})
}

// handleStorePlugins is the Store's plugin table: every source, paginated, sortable,
// filterable (?source=&trust=&installed=true|false).
func (s *server) handleStorePlugins(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAnyAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	pq, err := parsePage(r, []string{"name", "source", "trust", "version", "publisher", "installed"}, "name")
	if err != nil {
		s.fail(w, err)
		return
	}
	if !pq.limited {
		pq.limit, pq.limited = 50, true
	}
	srcs, err := s.pluginStore().Sources(r.Context(), s.rt.Repo(), false)
	if err != nil {
		s.fail(w, err)
		return
	}
	visibleSources(srcs, s.adminOf(r.Context(), id))
	v := r.URL.Query()
	var all []store.Plugin
	for _, src := range srcs {
		if f := v.Get("source"); f != "" && f != src.Name {
			continue
		}
		for _, p := range src.Plugins {
			if f := v.Get("trust"); f != "" && f != p.Trust {
				continue
			}
			if f := v.Get("installed"); f != "" && (f == "true") != (len(p.Installed) > 0) {
				continue
			}
			if f := v.Get("updates"); f != "" && (f == "true") != (p.Update != nil) {
				continue
			}
			all = append(all, p)
		}
	}
	items, meta := page(all, pq,
		func(p store.Plugin) string {
			return strings.Join(append([]string{p.Name, p.DisplayName, p.Description, p.Source, p.Publisher}, p.Keywords...), " ")
		},
		func(p store.Plugin, k string) string {
			switch k {
			case "source":
				return p.Source
			case "trust":
				return p.Trust
			case "version":
				return p.Version
			case "publisher":
				return p.Publisher
			case "installed":
				return strconv.Itoa(len(p.Installed))
			}
			return p.DisplayName
		},
		func(p store.Plugin) string { return p.Source + "/" + p.Name })
	if items == nil {
		items = []store.Plugin{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": meta.Total, "offset": meta.Offset,
		"limit": meta.Limit, "sort": meta.Sort, "order": meta.Order})
}

func (s *server) handleStoreDetail(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAnyAdmin(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.pluginStore().Detail(r.Context(), s.rt.Repo(), r.PathValue("source"), r.PathValue("plugin"))
	if err != nil {
		s.failStore(w, err)
		return
	}
	d.Installed = visibleInstalled(d.Installed, s.adminOf(r.Context(), id))
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
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in cicord.yaml"))
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
	officialReg, remoteOfficial, ok := s.officialOrigin(w, r, req.Source, req.Plugin, req.Commit)
	if !ok {
		return
	}
	if req.Source == config.OfficialRegistryName && !remoteOfficial {
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
		if remoteOfficial {
			reg = &officialReg
		}
		for _, rg := range repo.Project.Spec.Registries {
			if reg == nil && rg.Name == req.Source {
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

func (s *server) handlePluginUninstall(w http.ResponseWriter, r *http.Request) {
	serverKey, botKey, instance := r.PathValue("server"), r.PathValue("bot"), r.PathValue("instance")
	id, _, err := s.requireRole(r, serverKey, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	repo := s.rt.Repo()
	if repo == nil || repo.Project == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "no active configuration yet"))
		return
	}
	if wb := repo.Project.Spec.WriteBack; wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in cicord.yaml"))
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	in := setup.Installer{Runtime: s.rt, Writer: s.opts.Writer, Catalog: s.opts.Catalog, Timeout: s.opts.ActivationTimeout}
	res, err := in.Uninstall(r.Context(), serverKey, botKey, instance, "admin-ui", id.actor())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: serverKey, Actor: id.auditActor(), Action: "plugin.uninstall",
		Target: botKey + "/" + instance, Details: mustJSON(map[string]any{
			"removed": res.Removed, "cleaned": res.Cleaned, "mode": res.Mode, "revision": res.Revision, "pullRequestUrl": res.PullRequestURL,
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
	var req struct {
		Name  string `json:"name"`
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	body := config.Registry{Name: strings.TrimSpace(req.Name), URL: strings.TrimSpace(req.URL)}
	token := strings.TrimSpace(req.Token)
	switch {
	case !registryNameRe.MatchString(body.Name):
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry names use 1 to 32 lowercase letters, digits and dashes"))
		return
	case body.Name == config.OfficialRegistryName:
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "the name %q is reserved for the official Cicord plugins", body.Name))
		return
	case !registry.IsRemoteURL(body.URL):
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry URLs must start with https://"))
		return
	case registry.HasUserinfo(body.URL):
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry URLs must not contain credentials: give the access token separately"))
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
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "registry URLs must point to a public host (%v); declare registries on your own network in cicord.yaml", err))
		return
	}
	if token != "" {
		if s.opts.SecretStore == nil {
			s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "this runtime has no secret store: set the token as an environment variable and declare secretRef in cicord.yaml"))
			return
		}
		body.SecretRef = registry.SecretRefFor(body.Name)
	}
	if err := s.pluginStore().CheckWithToken(r.Context(), body, token); err != nil {
		se := store.ExplainFetch(err)
		s.failSetup(w, &setup.Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: se.Message, Hint: se.Hint, Details: se.Details})
		return
	}
	reg := body
	if token != "" {
		if err := s.opts.SecretStore.Set(r.Context(), reg.SecretRef, token); err != nil {
			s.fail(w, err)
			return
		}
	}
	edit := func(cur []byte) ([]byte, error) {
		if cur == nil {
			return nil, errors.New("cicord.yaml does not exist")
		}
		return writeback.AppendYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("registries")}, reg)
	}
	if !s.writeProject(w, r, id, edit, fmt.Sprintf("registry: add %s\n\nURL: %s (%s)", reg.Name, reg.URL, registry.Trust(reg.URL)),
		"registry.add", reg.Name, map[string]any{"url": reg.URL, "trust": registry.Trust(reg.URL), "private": token != ""}) && token != "" {
		_ = s.opts.SecretStore.Delete(r.Context(), reg.SecretRef)
	}
}

// handleRegistryToken sets (PUT {"token"}) or removes (DELETE) the access token of a
// registry. The token goes to the secret store; cicord.yaml only gets the secretRef.
func (s *server) handleRegistryToken(w http.ResponseWriter, r *http.Request) {
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
	i := slices.IndexFunc(repo.Project.Spec.Registries, func(rg config.Registry) bool { return rg.Name == name })
	if i < 0 {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown registry %q", name))
		return
	}
	reg := repo.Project.Spec.Registries[i]
	if s.opts.SecretStore == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "this runtime has no secret store"))
		return
	}
	at := []writeback.PathElem{writeback.Key("spec"), writeback.Key("registries"), writeback.Match("name", name), writeback.Key("secretRef")}
	if r.Method == http.MethodDelete {
		if reg.SecretRef == "" {
			writeJSON(w, http.StatusOK, registryWriteResult{Mode: writeback.ModeCommit})
			return
		}
		// Only the secret the admin UI created is deleted; an operator's secret stays.
		if reg.SecretRef == registry.SecretRefFor(name) {
			_ = s.opts.SecretStore.Delete(r.Context(), reg.SecretRef)
		}
		s.writeProject(w, r, id, func(cur []byte) ([]byte, error) { return writeback.SetYAML(cur, at, nil) },
			"registry: "+name+" is public (token removed)", "registry.token", name, map[string]any{"removed": true})
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "token is required (DELETE removes it)"))
		return
	}
	// Always this registry's own secret: never another one named in cicord.yaml.
	ref := registry.SecretRefFor(name)
	check := reg
	check.SecretRef = ref
	if registry.IsRemoteURL(reg.URL) {
		if err := registry.CheckPublicHost(r.Context(), reg.URL); err != nil {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "%v", err))
			return
		}
	}
	if err := s.pluginStore().CheckWithToken(r.Context(), check, token); err != nil {
		se := store.ExplainFetch(err)
		s.failSetup(w, &setup.Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "The registry cannot be read with this token. " + se.Message, Hint: se.Hint, Details: se.Details})
		return
	}
	if err := s.opts.SecretStore.Set(r.Context(), ref, token); err != nil {
		s.fail(w, err)
		return
	}
	for _, k := range s.rt.ServerKeys() {
		s.audit(r.Context(), state.AuditEntry{Server: k, Actor: id.auditActor(), Action: "registry.token", Target: name, Details: mustJSON(map[string]any{"set": true})})
	}
	if reg.SecretRef == ref {
		// cicord.yaml already names this secret: nothing to commit.
		_, _ = s.pluginStore().Sources(r.Context(), repo, true)
		writeJSON(w, http.StatusOK, registryWriteResult{Mode: writeback.ModeCommit})
		return
	}
	s.writeProject(w, r, id, func(cur []byte) ([]byte, error) { return writeback.SetYAML(cur, at, ref) },
		"registry: "+name+" is private (token stored as "+ref+")", "registry.token", name, map[string]any{"set": true})
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
			return nil, errors.New("cicord.yaml does not exist")
		}
		return writeback.SetYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("registries"), writeback.Match("name", name)}, nil)
	}
	ref := repo.Project.Spec.Registries[slices.IndexFunc(repo.Project.Spec.Registries, func(rg config.Registry) bool { return rg.Name == name })].SecretRef
	if s.writeProject(w, r, id, edit, "registry: remove "+name, "registry.remove", name, map[string]any{}) &&
		ref == registry.SecretRefFor(name) && s.opts.SecretStore != nil {
		_ = s.opts.SecretStore.Delete(r.Context(), ref)
	}
}

// writeProject writes cicord.yaml through write-back and audits the change on every server.
func (s *server) writeProject(w http.ResponseWriter, r *http.Request, id *identity, edit writeback.EditFunc,
	message, action, target string, details map[string]any) bool {
	if s.opts.Writer == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back is not configured"))
		return false
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	repo := s.rt.Repo()
	wb := repo.Project.Spec.WriteBack
	if wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in cicord.yaml"))
		return false
	}
	rev, _ := s.rt.Revision()
	mode := writeback.ModeFor(wb, []string{"cicord.yaml"})
	prevErr := s.rt.Status().LastError
	res, err := s.opts.Writer.Write(r.Context(), writeback.Request{
		Edits:   map[string]writeback.EditFunc{"cicord.yaml": edit},
		Message: message, Actor: id.actor(), Source: "admin-ui", Mode: mode,
	})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			s.fail(w, httpErr(http.StatusConflict, "conflict", "%v", err))
			return false
		}
		s.log.Error("write-back failed", "err", err)
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back failed: %v", err))
		return false
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
	return true
}
