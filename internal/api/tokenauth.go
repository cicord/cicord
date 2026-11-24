package api

// Embedded (token) authentication, spec/api.md §1 "Token mode": a third-party site that
// embeds the portal SDK on another origin cannot rely on the SameSite=Lax session cookie.
// It gets a bearer session token instead:
//
//   - OAuth: /auth/login?mode=token&redirect=<allowed origin URL> → Discord → /auth/callback
//     redirects to <redirect>?cicord_code=<one-time code> → POST /auth/token {code} from that
//     origin → {token, expiresAt, me}.
//   - Portal links: POST /auth/link {token, mode: "token"} → {token, expiresAt, me}.
//   - Dev: POST /auth/dev-login {userId, mode: "token"} → {token, expiresAt, me}.
//
// Token sessions are sent as `Authorization: Bearer cicord_st_…`. They have the full role of
// the user on portal endpoints and at most `member` everywhere else (like link sessions),
// so a token stored by a third-party site never reaches the admin endpoints.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

const (
	// tokenPrefix distinguishes session tokens from the runtime token.
	tokenPrefix = "cicord_st_" //nolint:gosec // G101: a public token prefix, not a credential
	// authCodeTTL bounds the one-time code of the OAuth token flow.
	authCodeTTL = 60 * time.Second
)

// originPolicy says whether an origin may call the runtime cross-origin and whether it may
// do so with credentials (cookies). Operator origins (--cors-origin) get credentials;
// origins declared in the repository (cicord.yaml spec.portal.allowedOrigins) are token-mode
// only, so that a merged pull request can never open cookie sessions to a new site.
func (s *server) originPolicy(origin string) (allowed, credentials bool) {
	if origin == "" {
		return false, false
	}
	if slices.Contains(s.opts.CORSOrigins, origin) {
		return true, true
	}
	if s.rt != nil {
		if repo := s.rt.Repo(); repo != nil && repo.Project != nil && repo.Project.Spec.Portal != nil &&
			slices.Contains(repo.Project.Spec.Portal.AllowedOrigins, origin) {
			return true, false
		}
	}
	return false, false
}

// tokenRedirect validates the redirect of a token-mode login: a local path, or an absolute
// URL on an allowed origin (either list). It returns the target and its origin ("" for a
// local path).
func (s *server) tokenRedirect(target string) (string, string, bool) {
	if target == "" {
		return "/", "", true
	}
	if isLocalRedirect(target) {
		return target, "", true
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return "", "", false
	}
	origin := u.Scheme + "://" + u.Host
	if ok, _ := s.originPolicy(origin); !ok {
		return "", "", false
	}
	return target, origin, true
}

// identifyToken resolves a session token. Unknown or expired tokens are 401s (the client
// then forgets the token).
func (s *server) identifyToken(ctx context.Context, tok string) (*identity, error) {
	sess, err := s.store.Sessions().Get(ctx, tok)
	if errors.Is(err, state.ErrNotFound) {
		return nil, httpErr(http.StatusUnauthorized, "unauthenticated", "invalid or expired session token")
	}
	if err != nil {
		return nil, err
	}
	var d sessionData
	_ = json.Unmarshal(sess.Data, &d)
	if !d.Token || subtle.ConstantTimeCompare([]byte(sess.ID), []byte(tok)) != 1 {
		return nil, httpErr(http.StatusUnauthorized, "unauthenticated", "invalid or expired session token")
	}
	return &identity{sessionID: sess.ID, data: d, user: discord.User{
		ID: sess.UserID, Username: d.Username, GlobalName: d.DisplayName, AvatarURL: d.AvatarURL,
	}}, nil
}

type tokenSessionDTO struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	Me        meDTO     `json:"me"`
}

// createTokenSession stores a token session (no cookie) and returns its DTO.
func (s *server) createTokenSession(ctx context.Context, u discord.User, d sessionData, ttl time.Duration) (tokenSessionDTO, error) {
	if ttl <= 0 || ttl > s.opts.TokenSessionTTL {
		ttl = s.opts.TokenSessionTTL
	}
	d.Token = true
	d.Username, d.DisplayName, d.AvatarURL = u.Username, u.GlobalName, u.AvatarURL
	data, _ := json.Marshal(d)
	sess := state.Session{ID: tokenPrefix + auth.RandomID(32), UserID: u.ID, Data: data, ExpiresAt: time.Now().Add(ttl).UTC()}
	if err := s.store.Sessions().Put(ctx, sess); err != nil {
		return tokenSessionDTO{}, err
	}
	id := &identity{sessionID: sess.ID, user: u, data: d}
	return tokenSessionDTO{Token: sess.ID, ExpiresAt: sess.ExpiresAt, Me: s.me(ctx, id)}, nil
}

// authCode is the one-time code of the OAuth token flow (KV "api:authcode").
type authCode struct {
	User   discord.User `json:"user"`
	Origin string       `json:"origin"` // origin allowed to redeem it ("" = the runtime's own)
}

// issueAuthCode stores a one-time code for u and returns the redirect URL carrying it.
func (s *server) issueAuthCode(ctx context.Context, u discord.User, redirect string) (string, error) {
	target, origin, ok := s.tokenRedirect(redirect)
	if !ok {
		target, origin = "/", ""
	}
	code := auth.RandomID(24)
	data, _ := json.Marshal(authCode{User: u, Origin: origin})
	if err := s.store.KV("api:authcode").Set(ctx, code, data, authCodeTTL); err != nil {
		return "", err
	}
	tu, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	q := tu.Query()
	q.Set("cicord_code", code)
	tu.RawQuery = q.Encode()
	return tu.String(), nil
}

// handleToken redeems a one-time code: POST /auth/token {"code"} → TokenSession. The
// request must come from the origin the login redirected to.
func (s *server) handleToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Code == "" {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "code is required"))
		return
	}
	kv := s.store.KV("api:authcode")
	raw, err := kv.Get(r.Context(), body.Code)
	if err != nil {
		s.fail(w, httpErr(http.StatusUnauthorized, "unauthenticated", "invalid or expired login code"))
		return
	}
	_ = kv.Delete(r.Context(), body.Code) // single use, even when the origin check fails
	var c authCode
	if err := json.Unmarshal(raw, &c); err != nil {
		s.fail(w, err)
		return
	}
	origin := r.Header.Get("Origin")
	if c.Origin != "" && origin != c.Origin || c.Origin == "" && origin != "" && !s.sameOrigin(r, origin) {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "this login code belongs to another origin"))
		return
	}
	out, err := s.createTokenSession(r.Context(), c.User, sessionData{Method: "oauth", Origin: c.Origin}, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// bearerSessionToken returns the session token of an Authorization header ("" when the
// header carries none).
func bearerSessionToken(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	tok = strings.TrimSpace(tok)
	if !ok || !strings.HasPrefix(tok, tokenPrefix) {
		return ""
	}
	return tok
}
