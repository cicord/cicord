package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/state"
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

type portalLinkDTO struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Icon  string `json:"icon"`
}

type portalPagesDTO struct {
	Onboarding  bool `json:"onboarding"`
	Profile     bool `json:"profile"`
	Leaderboard bool `json:"leaderboard"`
	Tickets     bool `json:"tickets"`
}

type portalServerDTO struct {
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	IconURL     string            `json:"iconUrl"`
	Theme       themeDTO          `json:"theme"`
	Title       string            `json:"title"`
	Tagline     string            `json:"tagline"`
	Description string            `json:"description"`
	Banner      string            `json:"banner"`
	Links       []portalLinkDTO   `json:"links"`
	Pages       portalPagesDTO    `json:"pages"`
	Locale      string            `json:"locale"`
	Plugins     []portalPluginDTO `json:"plugins"`
}

func enabled(b *bool) bool { return b == nil || *b }

// portalImageURL is the URL of a portal image: absolute URLs as is, repository files
// through the image endpoint (versioned by revision so that caches follow new commits).
func (s *server) portalImageURL(key, kind, value string) string {
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://"):
		return value
	}
	u := "/api/v1/portal/" + url.PathEscape(key) + "/" + kind
	if rev, ok := s.rt.Revision(); ok && rev.ID != "" {
		u += "?v=" + url.QueryEscape(rev.ID)
	}
	return u
}

func (s *server) portalServer(key string) (portalServerDTO, bool) {
	srv, ok := s.rt.ServerConfig(key)
	if !ok {
		return portalServerDTO{}, false
	}
	out := portalServerDTO{
		Key: key, Name: s.serverName(key), Plugins: []portalPluginDTO{}, Links: []portalLinkDTO{},
		Pages: portalPagesDTO{Onboarding: true, Profile: true, Leaderboard: true, Tickets: true},
	}
	out.Title = out.Name
	if info, ok := s.rt.ServerInfo(key); ok {
		out.IconURL = info.IconURL
	}
	if p := srv.Spec.Portal; p != nil {
		if t := p.Theme; t != nil {
			out.Theme = themeDTO{Primary: t.Primary, Accent: t.Accent, Mode: t.Mode, Logo: s.portalImageURL(key, "logo", t.Logo)}
		}
		if p.Title != "" {
			out.Title = p.Title
		}
		out.Tagline, out.Description, out.Locale = p.Tagline, p.Description, p.Locale
		out.Banner = s.portalImageURL(key, "banner", p.Banner)
		for _, l := range p.Links {
			icon := l.Icon
			if icon == "" {
				icon = "link"
			}
			out.Links = append(out.Links, portalLinkDTO{Label: l.Label, URL: l.URL, Icon: icon})
		}
		if pg := p.Pages; pg != nil {
			out.Pages = portalPagesDTO{Onboarding: enabled(pg.Onboarding), Profile: enabled(pg.Profile),
				Leaderboard: enabled(pg.Leaderboard), Tickets: enabled(pg.Tickets)}
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

// handlePortalImage serves theme.logo ("logo") or banner ("banner") when it is a file of
// the repository (relative to the server YAML); an absolute URL redirects.
func (s *server) handlePortalImage(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.requireRuntime(); err != nil {
			s.fail(w, err)
			return
		}
		srv, ok := s.rt.ServerConfig(r.PathValue("server"))
		repo := s.rt.Repo()
		if !ok || repo == nil || srv.Spec.Portal == nil {
			notFoundHandler(w, r)
			return
		}
		var value string
		switch kind {
		case "logo":
			if srv.Spec.Portal.Theme != nil {
				value = srv.Spec.Portal.Theme.Logo
			}
		case "banner":
			value = srv.Spec.Portal.Banner
		}
		if value == "" {
			notFoundHandler(w, r)
			return
		}
		if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
			http.Redirect(w, r, value, http.StatusFound)
			return
		}
		rel, ok := config.ResolveRepoPath(srv.File, value)
		ct := config.PortalImageType(rel)
		if !ok || ct == "" || config.LocalPathError(repo.Root, rel) != nil {
			notFoundHandler(w, r)
			return
		}
		root, err := os.OpenRoot(repo.Root)
		if err != nil {
			notFoundHandler(w, r)
			return
		}
		defer root.Close()
		f, err := root.Open(filepath.FromSlash(rel))
		if err != nil {
			notFoundHandler(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() || st.Size() > maxPortalImage {
			notFoundHandler(w, r)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=300")
		// A repository SVG must never run script on the runtime origin.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		// Embedding sites on other origins may display the image.
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		http.ServeContent(w, r, path.Base(rel), st.ModTime(), f)
	}
}

// maxPortalImage bounds the logo / banner files served (10 MiB).
const maxPortalImage = 10 << 20

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

// routeHeader reports whether a plugin route may set a response header. It is an
// allowlist: a route cannot set cookies, redirects (Location, Refresh), CORS, security
// or hop-by-hop headers (Connection, Upgrade…), nor clear site data. Plugin headers are
// x-* (except the runtime's own x-cicord-*) and a few content headers.
func routeHeader(name string, public bool) bool {
	lk := strings.ToLower(name)
	switch lk {
	case "content-disposition", "content-language", "etag", "last-modified", "expires", "retry-after":
		return true
	case "cache-control":
		// Non-public routes are always no-store; a public route may let caches keep it.
		return public
	}
	if !strings.HasPrefix(lk, "x-") {
		return false
	}
	for _, p := range reservedRouteHeaders {
		if strings.HasPrefix(lk, p) {
			return false
		}
	}
	return true
}

// reservedRouteHeaders are x-* prefixes a plugin route cannot set: the runtime's own,
// browser security headers, and the headers reverse proxies act on (nginx X-Accel-*
// internal redirects, X-Sendfile and its lighttpd and LiteSpeed forms, forwarding and
// rewrite headers). A proxy in front of Cicord would otherwise serve its own files or
// internal locations on a plugin's behalf.
var reservedRouteHeaders = []string{
	"x-cicord-",
	"x-content-type-options", "x-frame-options", "x-xss-protection",
	"x-permitted-cross-domain-policies", "x-dns-prefetch-control",
	"x-accel-", "x-sendfile", "x-lighttpd-", "x-litespeed-", "x-reproxy-",
	"x-forwarded-", "x-real-ip", "x-original-", "x-rewrite-", "x-http-method",
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
	var grant *auth.ScopedGrant
	switch {
	case route.Access == "scoped":
		if id == nil {
			s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "case sign-in required"))
			return
		}
		claim := id.data.Case
		if claim == nil || claim.Scope == nil || claim.UserID != id.user.ID || claim.Server != serverKey || claim.Scope.Bot != botKey || claim.Scope.Instance != inst {
			s.fail(w, httpErr(http.StatusForbidden, "forbidden", "a matching case link is required"))
			return
		}
		ok, err := claim.Current(r.Context(), s.store)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !ok {
			s.fail(w, httpErr(http.StatusForbidden, "forbidden", "expired or revoked case link"))
			return
		}
		grant = claim.Scope
		viewer = &plugin.RouteViewer{User: id.user}
	case id != nil && id.data.Case != nil:
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "this session is restricted to its case"))
		return
	case id != nil && !id.bearer:
		// The viewer's member decides what the plugin shows (member.canView): read it now.
		ro, m, err := s.resolveRole(r.Context(), id, serverKey, true, true)
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
	case id == nil && need > roleNone:
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "authentication required"))
		return
	}

	req := plugin.RouteRequest{Query: map[string]string{}, Viewer: viewer, Grant: grant, ExpectedAccess: route.Access}
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
	if viewer != nil && !isSafeMethod(r.Method) {
		// Plugins often change the viewer's roles (onboarding): do not serve the cached
		// member to the portal's next /me.
		s.dropMember(serverKey, viewer.User.ID)
	}
	if errors.Is(err, runtime.ErrNotFound) {
		notFoundHandler(w, r)
		return
	}
	if err != nil {
		s.log.Warn("plugin route failed", "server", serverKey, "bot", botKey, "instance", inst, "route", route.Key(), "err", err)
		s.fail(w, httpErr(http.StatusInternalServerError, "internal", "the plugin route failed"))
		return
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	if status < 200 || status > 599 {
		// 1xx would let a plugin answer "101 Switching Protocols" on the connection.
		s.log.Warn("plugin route returned an invalid status", "server", serverKey, "bot", botKey, "instance", inst, "route", route.Key(), "status", status)
		s.fail(w, httpErr(http.StatusInternalServerError, "internal", "the plugin route failed"))
		return
	}
	public := route.Access == "public"
	for k, v := range resp.Headers {
		if routeHeader(k, public) {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A public route may answer differently to a signed-in viewer: never let a shared
	// cache keep that answer, and key the anonymous one on the credentials.
	w.Header().Add("Vary", "Cookie, Authorization")
	if !public || viewer != nil {
		w.Header().Set("Cache-Control", "no-store")
	}
	if resp.Raw != nil {
		w.Header().Set("Content-Type", resp.ContentType+"; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, *resp.Raw)
		return
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
