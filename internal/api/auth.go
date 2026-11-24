package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

const (
	sessionCookie = "cicord_session"
	oauthCookie   = "cicord_oauth_state"
	maxLinkTTL    = 24 * time.Hour
)

// sessionData is stored as state.Session.Data.
type sessionData struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	// Method: "oauth" | "link" | "dev".
	Method string `json:"method"`
	// LinkServer restricts a session created from a portal link to that server.
	LinkServer string `json:"linkServer,omitempty"`
	// Token marks a bearer session token (token mode): only accepted in an
	// Authorization header, never as a cookie; capped at member outside the portal.
	Token bool `json:"token,omitempty"`
	// Origin is the embedding site a token session was issued to (informational).
	Origin string `json:"origin,omitempty"`
}

// identity is the caller of a request.
type identity struct {
	bearer bool
	// setup is the setup-wizard principal (setup cookie): admin until setup completes.
	setup     bool
	sessionID string
	user      discord.User
	data      sessionData
}

func (id *identity) actor() string {
	if id.bearer {
		return "runtime-token"
	}
	if id.setup {
		return "setup"
	}
	return fmt.Sprintf("discord:%s (%s)", id.user.ID, id.user.DisplayName())
}

func (s *server) isBearer(r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.opts.RuntimeToken != "" &&
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(s.opts.RuntimeToken)) == 1
}

// identify returns the caller, or nil when unauthenticated. An invalid bearer token is
// an error (401), an unknown/expired cookie is simply anonymous.
func (s *server) identify(r *http.Request) (*identity, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		if s.isBearer(r) {
			return &identity{bearer: true, user: discord.User{ID: "runtime", Username: "runtime"}}, nil
		}
		if tok := bearerSessionToken(r); tok != "" {
			return s.identifyToken(r.Context(), tok)
		}
		return nil, httpErr(http.StatusUnauthorized, "unauthenticated", "invalid bearer token")
	}
	// While the setup wizard runs, its session wins over a Discord login made from the same
	// browser ("Test login" with a non-owner account must not lock the wizard out).
	if id := s.identifySetup(r); id != nil {
		return id, nil
	}
	c, err := r.Cookie(sessionCookie)
	if errors.Is(err, http.ErrNoCookie) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.Value == "" {
		return nil, nil
	}
	sess, err := s.store.Sessions().Get(r.Context(), c.Value)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d sessionData
	_ = json.Unmarshal(sess.Data, &d)
	if d.Token || d.Method == setupSessionMethod {
		return nil, nil // token / setup sessions are never accepted as cicord_session cookies
	}
	return &identity{sessionID: sess.ID, data: d, user: discord.User{
		ID: sess.UserID, Username: d.Username, GlobalName: d.DisplayName, AvatarURL: d.AvatarURL,
	}}, nil
}

// requireAuth returns the caller or a 401.
func (s *server) requireAuth(r *http.Request) (*identity, error) {
	id, err := s.identify(r)
	if err != nil {
		return nil, err
	}
	if id == nil {
		return nil, httpErr(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return id, nil
}

// secureCookies: the request or the URL of its host (publicURL) is HTTPS.
func (s *server) secureCookies(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(s.publicURL(r), "https://")
}

func (s *server) createSession(w http.ResponseWriter, r *http.Request, u discord.User, d sessionData, ttl time.Duration) (*identity, error) {
	if ttl <= 0 || ttl > s.opts.SessionTTL {
		ttl = s.opts.SessionTTL
	}
	d.Username, d.DisplayName, d.AvatarURL = u.Username, u.GlobalName, u.AvatarURL
	data, _ := json.Marshal(d)
	sess := state.Session{ID: auth.RandomID(32), UserID: u.ID, Data: data, ExpiresAt: time.Now().Add(ttl)}
	if err := s.store.Sessions().Put(r.Context(), sess); err != nil {
		return nil, err
	}
	// Replace a previous session of this browser.
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.store.Sessions().Delete(r.Context(), c.Value)
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is s.secureCookies(r): set whenever the request or the public URL is HTTPS
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is s.secureCookies(r), set whenever the request or the public URL is HTTPS
		Name: sessionCookie, Value: sess.ID, Path: "/", Expires: sess.ExpiresAt, MaxAge: int(ttl.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookies(r),
	})
	return &identity{sessionID: sess.ID, user: u, data: d}, nil
}

// ───────────────────────────────────────────────────────────── roles

type role int

const (
	roleNone role = iota
	roleMember
	roleModerator
	roleAdmin
)

func (r role) String() string {
	switch r {
	case roleMember:
		return "member"
	case roleModerator:
		return "moderator"
	case roleAdmin:
		return "admin"
	}
	return ""
}

func parseRole(s string) role {
	switch s {
	case "member":
		return roleMember
	case "moderator":
		return roleModerator
	case "admin":
		return roleAdmin
	}
	return roleNone
}

type memberEntry struct {
	member  *discord.Member // nil = not a member
	expires time.Time
}

type memberCache struct {
	mu      sync.Mutex
	entries map[string]memberEntry
}

// member fetches (with a short cache) the Discord member of a user in a server; nil
// when the user is not in the server.
func (s *server) member(ctx context.Context, serverKey, userID string) (*discord.Member, error) {
	key := serverKey + "/" + userID
	now := time.Now()
	if s.opts.MemberCacheTTL > 0 {
		s.members.mu.Lock()
		e, ok := s.members.entries[key]
		s.members.mu.Unlock()
		if ok && now.Before(e.expires) {
			return e.member, nil
		}
	}
	srv, ok := s.rt.ServerConfig(serverKey)
	if !ok {
		return nil, nil
	}
	cl, ok := s.rt.Client(serverKey)
	if !ok {
		return nil, httpErr(http.StatusServiceUnavailable, "unavailable", "no bot is connected to server %s", serverKey)
	}
	m, err := cl.GetMember(ctx, srv.Spec.DiscordID, userID)
	var mp *discord.Member
	switch {
	case err == nil:
		mp = &m
	case errors.Is(err, discord.ErrNotFound):
	default:
		return nil, fmt.Errorf("get member: %w", err)
	}
	if s.opts.MemberCacheTTL > 0 {
		s.members.mu.Lock()
		if len(s.members.entries) > 10000 {
			s.members.entries = map[string]memberEntry{}
		}
		s.members.entries[key] = memberEntry{member: mp, expires: now.Add(s.opts.MemberCacheTTL)}
		s.members.mu.Unlock()
	}
	return mp, nil
}

// roleIn resolves the caller's role in a server (https://cicord.dev/docs/reference/http-api/authentication/). portal is true for
// portal endpoints; sessions created from a portal link are limited to that server and,
// outside the portal, to the member role.
func (s *server) roleIn(ctx context.Context, id *identity, serverKey string, portal bool) (role, *discord.Member, error) {
	if id == nil {
		return roleNone, nil, nil
	}
	srv, ok := s.rt.ServerConfig(serverKey)
	if !ok {
		return roleNone, nil, nil
	}
	if id.bearer || id.setup {
		return roleAdmin, nil, nil
	}
	if id.data.LinkServer != "" && id.data.LinkServer != serverKey {
		return roleNone, nil, nil
	}
	m, err := s.member(ctx, serverKey, id.user.ID)
	if err != nil {
		return roleNone, nil, err
	}
	if m == nil {
		return roleNone, nil, nil
	}
	r := roleMember
	if info, ok := s.rt.ServerInfo(serverKey); ok && info.OwnerID == id.user.ID {
		r = roleAdmin
	} else if a := srv.Spec.Access; a != nil {
		matches := func(p *config.Principals) bool {
			if p == nil {
				return false
			}
			if slices.Contains(p.Users, id.user.ID) {
				return true
			}
			for _, rk := range p.Roles {
				if rid, ok := s.rt.ResourceID(serverKey, state.KindRole, rk); ok && m.HasRole(rid) {
					return true
				}
			}
			return false
		}
		if matches(a.Admins) {
			r = roleAdmin
		} else if matches(a.Moderators) {
			r = roleModerator
		}
	}
	// Link and token sessions only get their full role on the portal endpoints.
	if (id.data.LinkServer != "" || id.data.Token) && !portal && r > roleMember {
		r = roleMember
	}
	return r, m, nil
}

// requireRole authenticates the caller and checks its role in a server.
func (s *server) requireRole(r *http.Request, serverKey string, min role) (*identity, role, error) {
	if err := s.requireRuntime(); err != nil {
		return nil, roleNone, err
	}
	id, err := s.requireAuth(r)
	if err != nil {
		return nil, roleNone, err
	}
	if _, ok := s.rt.ServerConfig(serverKey); !ok {
		return nil, roleNone, httpErr(http.StatusNotFound, "not_found", "unknown server %q", serverKey)
	}
	got, _, err := s.roleIn(r.Context(), id, serverKey, false)
	if err != nil {
		return nil, roleNone, err
	}
	if got < min {
		return nil, got, httpErr(http.StatusForbidden, "forbidden", "requires the %s role on server %s", min, serverKey)
	}
	return id, got, nil
}

// ───────────────────────────────────────────────────────────── /me

type userDTO struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl"`
	Bot         bool   `json:"bot,omitempty"`
}

func toUser(u discord.User) userDTO {
	return userDTO{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName(), AvatarURL: u.AvatarURL, Bot: u.Bot}
}

type meServer struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type meDTO struct {
	User    userDTO    `json:"user"`
	Servers []meServer `json:"servers"`
}

func (s *server) me(ctx context.Context, id *identity) meDTO {
	out := meDTO{User: toUser(id.user), Servers: []meServer{}}
	if s.rt == nil {
		return out
	}
	for _, key := range s.rt.ServerKeys() {
		r, _, err := s.roleIn(ctx, id, key, false)
		if err != nil {
			s.log.Warn("resolve role", "server", key, "err", err)
			continue
		}
		if r == roleNone {
			continue
		}
		out.Servers = append(out.Servers, meServer{Key: key, Name: s.serverName(key), Role: r.String()})
	}
	return out
}

func (s *server) serverName(key string) string {
	if info, ok := s.rt.ServerInfo(key); ok && info.Name != "" {
		return info.Name
	}
	if srv, ok := s.rt.ServerConfig(key); ok && srv.Spec.Profile != nil && srv.Spec.Profile.Name != "" {
		return srv.Spec.Profile.Name
	}
	return key
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAuth(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.me(r.Context(), id))
}

// ───────────────────────────────────────────────────────────── link / dev / logout

func (s *server) handleLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Mode  string `json:"mode"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if err := checkMode(body.Mode); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	claims, err := s.rt.Signer().Verify(strings.TrimSpace(body.Token))
	switch {
	case errors.Is(err, auth.ErrExpiredToken):
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "this link has expired, ask for a new one"))
		return
	case err != nil:
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "invalid link token"))
		return
	}
	if claims.UserID == "" {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "this link is anonymous and cannot sign you in"))
		return
	}
	if _, ok := s.rt.ServerConfig(claims.Server); !ok {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown server %q", claims.Server))
		return
	}
	u := discord.User{ID: claims.UserID, Username: claims.UserID}
	if m, err := s.member(r.Context(), claims.Server, claims.UserID); err == nil && m != nil {
		u = m.User
	} else if cl, ok := s.rt.Client(claims.Server); ok {
		if got, err := cl.GetUser(r.Context(), claims.UserID); err == nil {
			u = got
		}
	}
	ttl := time.Until(time.Unix(claims.Expires, 0))
	if ttl < time.Hour {
		ttl = time.Hour // the session outlives the short link a little
	}
	if ttl > maxLinkTTL {
		ttl = maxLinkTTL
	}
	if body.Mode == "token" {
		out, err := s.createTokenSession(r.Context(), u, sessionData{Method: "link", LinkServer: claims.Server, Origin: r.Header.Get("Origin")}, ttl)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	id, err := s.createSession(w, r, u, sessionData{Method: "link", LinkServer: claims.Server}, ttl)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.me(r.Context(), id))
}

func (s *server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if s.opts.Mode != "dev" {
		notFoundHandler(w, r)
		return
	}
	// Impersonating arbitrary users is only acceptable against the simulator; against
	// real Discord it requires the runtime token.
	if s.opts.Sim == nil && !s.isBearer(r) {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "dev login requires --sim or the runtime token"))
		return
	}
	var body struct {
		UserID string `json:"userId"`
		Mode   string `json:"mode"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if err := checkMode(body.Mode); err != nil {
		s.fail(w, err)
		return
	}
	if body.UserID == "" {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "userId is required"))
		return
	}
	u := discord.User{ID: body.UserID, Username: body.UserID}
	if s.opts.Sim != nil {
		got, ok := s.opts.Sim.User(body.UserID)
		if !ok {
			s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown simulated user %s", body.UserID))
			return
		}
		u = got
	} else if s.rt != nil {
		if cl, ok := s.rt.AnyClient(); ok {
			if got, err := cl.GetUser(r.Context(), body.UserID); err == nil {
				u = got
			}
		}
	}
	if body.Mode == "token" {
		out, err := s.createTokenSession(r.Context(), u, sessionData{Method: "dev", Origin: r.Header.Get("Origin")}, 0)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	id, err := s.createSession(w, r, u, sessionData{Method: "dev"}, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.me(r.Context(), id))
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := bearerSessionToken(r); tok != "" {
		// Token mode: revoke the token; never touch the browser's cookie session.
		if id, err := s.identifyToken(r.Context(), tok); err == nil {
			if err := s.store.Sessions().Delete(r.Context(), id.sessionID); err != nil && !errors.Is(err, state.ErrNotFound) {
				s.fail(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.store.Sessions().Delete(r.Context(), c.Value); err != nil && !errors.Is(err, state.ErrNotFound) {
			s.fail(w, err)
			return
		}
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is s.secureCookies(r): set whenever the request or the public URL is HTTPS
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, //nolint:gosec // G124: Secure is s.secureCookies(r), set whenever the request or the public URL is HTTPS
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookies(r)})
	w.WriteHeader(http.StatusNoContent)
}

// ───────────────────────────────────────────────────────────── Discord OAuth2

type oauthState struct {
	Redirect string `json:"redirect"`
	// Mode "token": the callback redirects with a one-time code instead of a cookie.
	Mode string `json:"mode,omitempty"`
}

// checkMode validates the auth mode of a login request ("" = cookie).
func checkMode(mode string) error {
	if mode != "" && mode != "cookie" && mode != "token" {
		return httpErr(http.StatusBadRequest, "invalid", "mode must be cookie or token")
	}
	return nil
}

// safeRedirect accepts a local path ("/x", not "//x" nor "/\x") or an absolute URL on an
// allowed CORS origin (external portal).
func (s *server) safeRedirect(target string) string {
	if isLocalRedirect(target) {
		return target
	}
	// Anything else must be an absolute URL on an allowed origin ("//x" and "/\\x" have no
	// scheme, so they end here as "/").
	if u, err := url.Parse(target); err == nil && u.Scheme != "" && u.Host != "" {
		if slices.Contains(s.opts.CORSOrigins, u.Scheme+"://"+u.Host) {
			return target
		}
	}
	return "/"
}

// isLocalRedirect reports whether target is a path on this host: it starts with a single
// "/" (not "//" nor "/\\", which browsers read as another host) and has no scheme or host.
func isLocalRedirect(target string) bool {
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") {
		return false
	}
	u, err := url.Parse(target)
	return err == nil && u.Host == "" && u.Scheme == ""
}

// clientSecret returns the OAuth2 client secret (SecretFunc first: the setup wizard
// stores it after the handler was built).
func (s *server) clientSecret() string {
	if f := s.opts.OAuth.SecretFunc; f != nil {
		if v := f(); v != "" {
			return v
		}
	}
	return s.opts.OAuth.ClientSecret
}

func (s *server) clientID() string {
	if s.opts.OAuth.ClientID != "" {
		return s.opts.OAuth.ClientID
	}
	if s.rt == nil {
		return ""
	}
	repo := s.rt.Repo()
	if repo == nil {
		return ""
	}
	keys := make([]string, 0, len(repo.Bots))
	for k := range repo.Bots {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return ""
	}
	return repo.Bots[keys[0]].Spec.ApplicationID
}

// publicURL is the origin the request was sent to: the admin URL on the admin host
// (hosts.go), otherwise the public URL (derived from the request when unset). The OAuth
// redirect URI uses it, so a login stays on the host it started from.
func (s *server) publicURL(r *http.Request) string {
	if s.onAdminHost(r) {
		return s.opts.AdminURL
	}
	if s.opts.PublicURL != "" {
		return s.opts.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	cid := s.clientID()
	if cid == "" || s.clientSecret() == "" {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "Discord login is not configured (CICORD_DISCORD_CLIENT_SECRET)"))
		return
	}
	mode := r.URL.Query().Get("mode")
	if err := checkMode(mode); err != nil {
		s.fail(w, err)
		return
	}
	stv := oauthState{Redirect: s.safeRedirect(r.URL.Query().Get("redirect"))}
	if mode == "token" {
		target, _, ok := s.tokenRedirect(r.URL.Query().Get("redirect"))
		if !ok {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "redirect must be a local path or a URL on an allowed origin"))
			return
		}
		stv = oauthState{Redirect: target, Mode: "token"}
	}
	st := auth.RandomID(24)
	data, _ := json.Marshal(stv)
	if err := s.store.KV("api:oauth").Set(r.Context(), st, data, 10*time.Minute); err != nil {
		s.fail(w, err)
		return
	}
	// The state is bound to this browser: the callback requires the same value in a
	// cookie (SameSite=Lax so that it survives the top-level redirect back from Discord).
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is s.secureCookies(r): set whenever the request or the public URL is HTTPS
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: st, Path: "/auth/", MaxAge: 600, //nolint:gosec // G124: Secure is s.secureCookies(r), set whenever the request or the public URL is HTTPS
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookies(r)})
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", cid)
	q.Set("scope", "identify")
	q.Set("state", st)
	q.Set("redirect_uri", s.publicURL(r)+"/auth/callback")
	q.Set("prompt", "none")
	http.Redirect(w, r, s.opts.OAuth.AuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

func (s *server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st := q.Get("state")
	c, err := r.Cookie(oauthCookie)
	if st == "" || err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(st)) != 1 {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "invalid OAuth state"))
		return
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is s.secureCookies(r): set whenever the request or the public URL is HTTPS
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: "", Path: "/auth/", MaxAge: -1, //nolint:gosec // G124: Secure is s.secureCookies(r), set whenever the request or the public URL is HTTPS
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookies(r)})
	kv := s.store.KV("api:oauth")
	raw, err := kv.Get(r.Context(), st)
	if err != nil {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "OAuth state expired, please log in again"))
		return
	}
	_ = kv.Delete(r.Context(), st)
	var os oauthState
	_ = json.Unmarshal(raw, &os)
	if e := q.Get("error"); e != "" {
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "Discord login failed: %s", e))
		return
	}
	code := q.Get("code")
	if code == "" {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "missing code"))
		return
	}
	u, err := s.exchangeCode(r.Context(), code, s.publicURL(r)+"/auth/callback")
	if err != nil {
		s.log.Warn("oauth exchange failed", "err", err)
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "Discord login failed"))
		return
	}
	if os.Mode == "token" {
		target, err := s.issueAuthCode(r.Context(), u, os.Redirect)
		if err != nil {
			s.fail(w, err)
			return
		}
		// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- issueAuthCode only returns a target allowed by s.tokenRedirect (else "/")
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if _, err := s.createSession(w, r, u, sessionData{Method: "oauth"}, 0); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, s.safeRedirect(os.Redirect), http.StatusFound)
}

func (s *server) exchangeCode(ctx context.Context, code, redirectURI string) (discord.User, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", s.clientID())
	form.Set("client_secret", s.clientSecret())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return discord.User{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := s.doJSON(req, &tok); err != nil {
		return discord.User{}, fmt.Errorf("token: %w", err)
	}
	if tok.AccessToken == "" {
		return discord.User{}, errors.New("token: no access_token")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.opts.OAuth.APIURL, "/")+"/users/@me", nil)
	if err != nil {
		return discord.User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var me struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
	}
	if err := s.doJSON(req, &me); err != nil {
		return discord.User{}, fmt.Errorf("users/@me: %w", err)
	}
	if me.ID == "" {
		return discord.User{}, errors.New("users/@me: no id")
	}
	u := discord.User{ID: me.ID, Username: me.Username, GlobalName: me.GlobalName}
	if me.Avatar != "" {
		u.AvatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", me.ID, me.Avatar)
	}
	return u, nil
}

func (s *server) doJSON(req *http.Request, out any) error {
	resp, err := s.opts.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s", resp.Status)
	}
	return json.Unmarshal(data, out)
}
