package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/plugin"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/state"
)

type themeDTO struct {
	Primary string `json:"primary"`
	Accent  string `json:"accent"`
	Logo    string `json:"logo"`
	Mode    string `json:"mode"`
}

type portalPluginDTO struct {
	Bot      string   `json:"bot"`
	Instance string   `json:"instance"`
	Plugin   string   `json:"plugin"`
	Routes   []string `json:"routes"`
}

type portalServerDTO struct {
	Key     string            `json:"key"`
	Name    string            `json:"name"`
	IconURL string            `json:"iconUrl"`
	Theme   themeDTO          `json:"theme"`
	Plugins []portalPluginDTO `json:"plugins"`
}

func (s *server) portalServer(key string) (portalServerDTO, bool) {
	srv, ok := s.rt.ServerConfig(key)
	if !ok {
		return portalServerDTO{}, false
	}
	out := portalServerDTO{Key: key, Name: s.serverName(key), Plugins: []portalPluginDTO{}}
	if info, ok := s.rt.ServerInfo(key); ok {
		out.IconURL = info.IconURL
	}
	if p := srv.Spec.Portal; p != nil && p.Theme != nil {
		t := p.Theme
		out.Theme = themeDTO{Primary: t.Primary, Accent: t.Accent, Mode: t.Mode}
		if t.Logo != "" {
			if strings.HasPrefix(t.Logo, "https://") || strings.HasPrefix(t.Logo, "http://") {
				out.Theme.Logo = t.Logo
			} else {
				out.Theme.Logo = "/api/v1/portal/" + key + "/logo"
			}
		}
	}
	for _, in := range s.rt.Instances(key) {
		if in.Definition == nil || len(in.Definition.Routes) == 0 {
			continue
		}
		pp := portalPluginDTO{Bot: in.Config.Bot, Instance: in.Config.Name, Plugin: in.Config.Plugin.Manifest.Metadata.Name, Routes: []string{}}
		for _, rd := range in.Definition.Routes {
			pp.Routes = append(pp.Routes, rd.Key())
		}
		out.Plugins = append(out.Plugins, pp)
	}
	return out, true
}

// hasPortal: the server declares spec.portal or runs a plugin with portal routes.
func (s *server) hasPortal(p portalServerDTO) bool {
	srv, _ := s.rt.ServerConfig(p.Key)
	return (srv != nil && srv.Spec.Portal != nil) || len(p.Plugins) > 0
}

func (s *server) handlePortalServers(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	items := []portalServerDTO{}
	for _, key := range s.rt.ServerKeys() {
		if p, ok := s.portalServer(key); ok && s.hasPortal(p) {
			items = append(items, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) handlePortalServer(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	p, ok := s.portalServer(r.PathValue("server"))
	if !ok || !s.hasPortal(p) {
		notFoundHandler(w, r)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handlePortalLogo serves theme.logo when it is a file of the repository (relative to
// the server YAML).
func (s *server) handlePortalLogo(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	srv, ok := s.rt.ServerConfig(r.PathValue("server"))
	if !ok || srv.Spec.Portal == nil || srv.Spec.Portal.Theme == nil || srv.Spec.Portal.Theme.Logo == "" {
		notFoundHandler(w, r)
		return
	}
	logo := srv.Spec.Portal.Theme.Logo
	if strings.Contains(logo, "://") {
		http.Redirect(w, r, logo, http.StatusFound)
		return
	}
	rev, _ := s.rt.Revision()
	rel := path.Clean(path.Join(path.Dir(srv.File), logo))
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		notFoundHandler(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(rev.Dir, filepath.FromSlash(rel)))
	if err != nil {
		notFoundHandler(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(rel))
	if !strings.HasPrefix(ct, "image/") {
		notFoundHandler(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, path.Base(rel), time.Time{}, bytes.NewReader(data))
}

type memberRoleDTO struct {
	Key   string `json:"key"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type portalMemberDTO struct {
	Nick     string          `json:"nick"`
	JoinedAt time.Time       `json:"joinedAt"`
	Roles    []memberRoleDTO `json:"roles"`
}

func (s *server) memberDTO(serverKey string, m *discord.Member) portalMemberDTO {
	out := portalMemberDTO{Nick: m.Nick, JoinedAt: m.JoinedAt, Roles: []memberRoleDTO{}}
	roles, _, keyOf, _ := s.rt.ServerResources(serverKey)
	byID := map[string]discord.Role{}
	for _, ro := range roles {
		byID[ro.ID] = ro
	}
	for _, rid := range m.RoleIDs {
		ro, ok := byID[rid]
		if !ok {
			continue
		}
		key := ""
		if keyOf != nil {
			if kind, k, ok := keyOf(rid); ok && kind == state.KindRole {
				key = k
			}
		}
		out.Roles = append(out.Roles, memberRoleDTO{Key: key, ID: rid, Name: ro.Name, Color: colorHex(ro.Color)})
	}
	return out
}

func (s *server) handlePortalMe(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	if _, ok := s.rt.ServerConfig(key); !ok {
		notFoundHandler(w, r)
		return
	}
	id, err := s.requireAuth(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	ro, m, err := s.roleIn(r.Context(), id, key, true)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ro < roleMember || m == nil {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "you are not a member of this server"))
		return
	}
	u := id.user
	if m.User.ID != "" {
		u = m.User
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUser(u), "member": s.memberDTO(key, m), "role": ro.String()})
}

// Headers a plugin route may not set.
var blockedRouteHeaders = map[string]bool{
	"set-cookie": true, "content-length": true, "transfer-encoding": true, "connection": true,
	"content-type": true, "location": true, "content-security-policy": true, "strict-transport-security": true,
}

func (s *server) handlePluginRoute(w http.ResponseWriter, r *http.Request) {
	serverKey, botKey, inst := r.PathValue("server"), r.PathValue("bot"), r.PathValue("instance")
	routePath := "/" + r.PathValue("path")
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	route, ok := s.rt.MatchRoute(serverKey, botKey, inst, r.Method, routePath)
	if !ok {
		notFoundHandler(w, r)
		return
	}
	id, err := s.identify(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	need := parseRole(route.Access)
	if route.Access == "" {
		need = roleMember
	}
	var viewer *plugin.RouteViewer
	if id != nil && !id.bearer {
		ro, m, err := s.roleIn(r.Context(), id, serverKey, true)
		if err != nil {
			s.fail(w, err)
			return
		}
		if ro < need {
			s.fail(w, httpErr(http.StatusForbidden, "forbidden", "this route requires the %s role", need))
			return
		}
		u := id.user
		if m != nil && m.User.ID != "" {
			u = m.User
		}
		viewer = &plugin.RouteViewer{User: u, Member: m}
	} else if id == nil && need > roleNone {
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "authentication required"))
		return
	}

	req := plugin.RouteRequest{Query: map[string]string{}, Viewer: viewer}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			req.Query[k] = v[0]
		}
	}
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opts.MaxBodyBytes))
		if err != nil {
			s.fail(w, httpErr(http.StatusRequestEntityTooLarge, "invalid", "request body too large"))
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			if !json.Valid(body) {
				s.fail(w, httpErr(http.StatusBadRequest, "invalid", "the body must be JSON"))
				return
			}
			req.Body = body
		}
	}
	resp, _, err := s.rt.HandleRoute(r.Context(), serverKey, botKey, inst, r.Method, routePath, req)
	if errors.Is(err, runtime.ErrNotFound) {
		notFoundHandler(w, r)
		return
	}
	if err != nil {
		s.log.Warn("plugin route failed", "server", serverKey, "bot", botKey, "instance", inst, "route", route.Key(), "err", err)
		s.fail(w, httpErr(http.StatusInternalServerError, "internal", "the plugin route failed"))
		return
	}
	for k, v := range resp.Headers {
		lk := strings.ToLower(k)
		if blockedRouteHeaders[lk] || strings.HasPrefix(lk, "access-control-") {
			continue
		}
		w.Header().Set(k, v)
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	if resp.Body == nil {
		if status == http.StatusOK {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}
