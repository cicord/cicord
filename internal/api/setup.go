package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/setup"
)

// Setup wizard endpoints (spec/api.md §10), the plugin catalog and Diagnostics.
//
// Access: the one-time setup token printed in the logs is exchanged (POST
// /api/v1/setup/session) for a setup session cookie (HttpOnly, SameSite=Strict). The
// setup session is an admin principal on the rest of the API until the wizard
// completes, so the wizard can use the normal endpoints (resources for the schema form
// pickers, plan…) before any Discord login exists. After completion every setup
// endpoint except status answers 410 and the setup sessions are revoked.

const (
	setupCookie        = "cicord_setup"
	setupSessionMethod = setup.SessionMethod
)

func (s *server) setupRoutes(h func(string, func(http.ResponseWriter, *http.Request))) {
	// Always available (normal mode too).
	h("GET /api/v1/catalog", s.handleCatalog)
	h("GET /api/v1/store", s.handleStore)
	h("POST /api/v1/store/refresh", s.handleStoreRefresh)
	h("GET /api/v1/store/plugins", s.handleStorePlugins)
	h("GET /api/v1/store/{source}/{plugin}", s.handleStoreDetail)
	h("GET /api/v1/store/{source}/{plugin}/update", s.handleStoreUpdateReview)
	h("POST /api/v1/store/{source}/{plugin}/update", s.handleStoreUpdate)
	h("POST /api/v1/servers/{server}/store/install", s.handleStoreInstall)
	h("DELETE /api/v1/servers/{server}/plugins/{bot}/{instance}", s.handlePluginUninstall)
	h("POST /api/v1/registries", s.handleRegistryAdd)
	h("DELETE /api/v1/registries/{name}", s.handleRegistryRemove)
	h("PUT /api/v1/registries/{name}/token", s.handleRegistryToken)
	h("DELETE /api/v1/registries/{name}/token", s.handleRegistryToken)
	h("GET /api/v1/servers/{server}/diagnostics", s.handleDiagnostics)

	h("GET /api/v1/setup/status", s.handleSetupStatus)
	h("POST /api/v1/setup/session", s.handleSetupSession)
	w := func(fn func(http.ResponseWriter, *http.Request, *setup.Wizard)) func(http.ResponseWriter, *http.Request) {
		return s.setupOnly(fn)
	}
	h("POST /api/v1/setup/intent", w(s.handleSetupIntent))
	h("POST /api/v1/setup/step", w(s.handleSetupStep))
	h("POST /api/v1/setup/discord/token", w(s.handleSetupToken))
	h("POST /api/v1/setup/discord/recheck", w(s.handleSetupRecheck))
	h("GET /api/v1/setup/discord/invite", w(s.handleSetupInvite))
	h("GET /api/v1/setup/discord/servers", w(s.handleSetupServers))
	h("POST /api/v1/setup/server", w(s.handleSetupServer))
	h("POST /api/v1/setup/server/check", w(s.handleSetupServerCheck))
	h("POST /api/v1/setup/repo", w(s.handleSetupRepo))
	h("POST /api/v1/setup/import", w(s.handleSetupImport))
	h("POST /api/v1/setup/oauth", w(s.handleSetupOAuth))
	h("POST /api/v1/setup/oauth/skip", w(s.handleSetupOAuthSkip))
	h("GET /api/v1/setup/oauth/test", w(s.handleSetupOAuthTest))
	h("GET /api/v1/setup/verify", w(s.handleSetupVerify))
	h("GET /api/v1/setup/plugins", w(s.handleSetupCatalog))
	h("POST /api/v1/setup/plugins/install", w(s.handleSetupInstall))
	h("POST /api/v1/setup/finish", w(s.handleSetupFinish))
	h("GET /api/v1/setup/sim", w(s.handleSetupSim))
	h("POST /api/v1/setup/sim/bot", w(s.handleSetupSimBot))
	h("POST /api/v1/setup/sim/intents", w(s.handleSetupSimIntents))
	h("POST /api/v1/setup/sim/invite", w(s.handleSetupSimInvite))
	h("POST /api/v1/setup/sim/raise-role", w(s.handleSetupSimRaise))
	h("POST /api/v1/setup/sim/message", w(s.handleSetupSimMessage))
	h("/api/v1/setup/", notFoundHandler)
}

// NewSetup returns the handler of setup mode (no configured repository): the admin UI,
// the setup endpoints and the health endpoints. Every other API path answers 503
// setup_required, and the portal root redirects to the wizard.
func NewSetup(opts Options) http.Handler {
	s := newServer(opts)
	m := s.mux
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request)) { m.HandleFunc(pattern, fn) }
	s.setupRoutes(h)
	h("GET /healthz", s.handleHealthz)
	h("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("setup mode\n"))
	})
	setupRequired := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{
			"code": "setup_required", "message": "Cicord is not set up yet.",
			"hint": "Open the setup link printed in the Cicord logs (/admin/setup?token=…).",
		}})
	}
	h("/api/", setupRequired)
	h("/auth/", setupRequired)
	h("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/setup", http.StatusFound)
	})
	h("/admin/", s.staticHandler(s.opts.AdminUI, "/admin/", true))
	h("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/admin/setup", http.StatusFound)
			return
		}
		notFoundHandler(w, r)
	})
	return s.middleware(s.mux)
}

// failSetup answers a wizard error (explained) or falls back to fail.
func (s *server) failSetup(w http.ResponseWriter, err error) {
	var se *setup.Error
	if errors.As(err, &se) {
		status := se.Status
		if status == 0 {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"error": se})
		return
	}
	s.fail(w, err)
}

func (s *server) setupSessionID(r *http.Request) string {
	c, err := r.Cookie(setupCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// identifySetup returns the setup principal when the request carries a live setup
// session.
func (s *server) identifySetup(r *http.Request) *identity {
	wz := s.opts.Setup
	if wz == nil {
		return nil
	}
	id := s.setupSessionID(r)
	if !wz.ValidSession(r.Context(), id) {
		return nil
	}
	return &identity{setup: true, sessionID: id, user: setupUser}
}

var setupUser = discord.User{ID: setup.SessionUserID, Username: "setup", GlobalName: "Setup"}

func (s *server) setupOnly(fn func(http.ResponseWriter, *http.Request, *setup.Wizard)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		wz := s.opts.Setup
		if wz == nil {
			notFoundHandler(w, r)
			return
		}
		if wz.Completed(r.Context()) {
			writeError(w, http.StatusGone, "gone", "setup is already complete")
			return
		}
		if !wz.ValidSession(r.Context(), s.setupSessionID(r)) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{
				"code": "unauthenticated", "message": "Your setup session expired or is missing.",
				"hint": "Open the setup link printed in the Cicord logs (restart Cicord to print a new one).",
			}})
			return
		}
		fn(w, r, wz)
	}
}

func (s *server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	wz := s.opts.Setup
	if wz == nil {
		writeJSON(w, http.StatusOK, map[string]any{"active": false, "completed": true, "authenticated": false})
		return
	}
	completed := wz.Completed(r.Context())
	authed := !completed && wz.ValidSession(r.Context(), s.setupSessionID(r))
	out := map[string]any{"active": !completed, "completed": completed, "authenticated": authed,
		"simulator": wz.Simulator()}
	if authed {
		v, err := wz.Status(r.Context())
		if err != nil {
			s.failSetup(w, err)
			return
		}
		out["view"] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleSetupSession(w http.ResponseWriter, r *http.Request) {
	wz := s.opts.Setup
	if wz == nil {
		notFoundHandler(w, r)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	sess, err := wz.Exchange(r.Context(), body.Token)
	if err != nil {
		s.failSetup(w, err)
		return
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is s.secureCookies(r): set whenever the request or the public URL is HTTPS
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: sess.ID, Path: "/", Expires: sess.ExpiresAt, //nolint:gosec // G124: Secure is s.secureCookies(r), set whenever the request or the public URL is HTTPS
		MaxAge: int(time.Until(sess.ExpiresAt).Seconds()), HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secureCookies(r)})
	v, err := wz.Status(r.Context())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "completed": false, "authenticated": true, "simulator": wz.Simulator(), "view": v})
}

func (s *server) viewResult(w http.ResponseWriter, v setup.View, err error) {
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) handleSetupIntent(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body struct {
		Intent string `json:"intent"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := wz.SetIntent(r.Context(), body.Intent)
	s.viewResult(w, v, err)
}

func (s *server) handleSetupStep(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body struct {
		Step string `json:"step"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := wz.Goto(r.Context(), body.Step)
	s.viewResult(w, v, err)
}

func (s *server) handleSetupToken(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body struct {
		Token string `json:"token"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := wz.SubmitToken(r.Context(), body.Token)
	s.viewResult(w, v, err)
}

func (s *server) handleSetupRecheck(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	v, err := wz.RecheckDiscord(r.Context())
	s.viewResult(w, v, err)
}

func (s *server) handleSetupInvite(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	v, err := wz.Status(r.Context())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	if v.Bot == nil {
		s.failSetup(w, &setup.Error{Status: http.StatusConflict, Code: "conflict", Message: "No Discord application is configured yet.", Hint: "Paste the bot token first."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": v.InviteURL, "scopes": []string{"bot", "applications.commands"}, "permissions": "Administrator"})
}

func (s *server) handleSetupServers(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	out, err := wz.Servers(r.Context())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

func (s *server) handleSetupServer(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body struct {
		ServerID string `json:"serverId"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := wz.SelectServer(r.Context(), strings.TrimSpace(body.ServerID))
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleSetupServerCheck(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	res, err := wz.CheckServer(r.Context())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleSetupRepo(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body setup.RepoRequest
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := wz.ChooseRepo(r.Context(), body)
	s.viewResult(w, v, err)
}

func (s *server) handleSetupImport(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &body); err != nil {
			s.fail(w, err)
			return
		}
	}
	res, err := wz.Import(r.Context(), body.Confirm)
	if err != nil {
		s.failSetup(w, err)
		return
	}
	if res.Plan != nil {
		p := humanPlan(*res.Plan)
		writeJSON(w, http.StatusOK, map[string]any{"view": res.View, "plan": p, "needsConfirm": res.NeedsConfirm, "files": res.Files, "warnings": nonNilStrings(res.Warnings)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"view": res.View, "plan": nil, "needsConfirm": res.NeedsConfirm, "files": res.Files, "warnings": nonNilStrings(res.Warnings)})
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *server) handleSetupOAuth(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body struct {
		ClientSecret string `json:"clientSecret"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := wz.SaveOAuth(r.Context(), body.ClientSecret)
	s.viewResult(w, v, err)
}

func (s *server) handleSetupOAuthSkip(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	v, err := wz.SkipLogin(r.Context())
	s.viewResult(w, v, err)
}

// handleSetupOAuthTest reports whether this browser has a Discord login session (after
// "Test login" went through /auth/login and back).
func (s *server) handleSetupOAuthTest(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	out := map[string]any{"ok": false}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if sess, err := s.store.Sessions().Get(r.Context(), c.Value); err == nil {
			var d sessionData
			_ = json.Unmarshal(sess.Data, &d)
			if !d.Token && d.Method != setupSessionMethod {
				id := &identity{sessionID: sess.ID, data: d, user: discord.User{
					ID: sess.UserID, Username: d.Username, GlobalName: d.DisplayName, AvatarURL: d.AvatarURL}}
				out["ok"] = true
				out["user"] = toUser(id.user)
				if s.rt != nil {
					out["servers"] = s.me(r.Context(), id).Servers
				}
				_, _ = wz.LoginTested(r.Context(), id.user.DisplayName())
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleSetupVerify(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	rep, err := wz.Verify(r.Context())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reportDTO(rep))
}

func reportDTO(rep setup.Report) map[string]any {
	out := map[string]any{"server": rep.Server, "bot": rep.Bot, "checks": rep.Checks, "ranAt": rep.RanAt, "ok": rep.OK}
	if rep.Plan != nil {
		out["plan"] = humanPlan(*rep.Plan)
	}
	return out
}

func (s *server) handleSetupCatalog(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	s.writeCatalog(w)
}

func (s *server) handleSetupInstall(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	var body setup.InstallRequest
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, v, err := wz.InstallPlugin(r.Context(), body)
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "view": v})
}

func (s *server) handleSetupFinish(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	v, err := wz.Finish(r.Context())
	if err != nil {
		s.failSetup(w, err)
		return
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is s.secureCookies(r): set whenever the request or the public URL is HTTPS
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: "", Path: "/", MaxAge: -1, //nolint:gosec // G124: Secure is s.secureCookies(r), set whenever the request or the public URL is HTTPS
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secureCookies(r)})
	writeJSON(w, http.StatusOK, v)
}

// ───────────────────────────────────────────────────────────── simulator helpers

func (s *server) simEnv(w http.ResponseWriter, wz *setup.Wizard) *setup.SimEnv {
	if e := wz.SimEnv(); e != nil {
		return e
	}
	writeError(w, http.StatusNotFound, "not_found", "the setup simulator is only available with cicord dev --sim --setup")
	return nil
}

func (s *server) handleSetupSim(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	e := s.simEnv(w, wz)
	if e == nil {
		return
	}
	type srv struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := []srv{}
	for _, x := range e.Servers() {
		out = append(out, srv{x.ID, x.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out, "owner": toUser(e.Owner)})
}

func (s *server) handleSetupSimBot(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	if s.simEnv(w, wz) == nil {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &body); err != nil {
			s.fail(w, err)
			return
		}
	}
	// The simulated token is not a secret (it only works against this process' simulator).
	tok, err := wz.SimCreateBot(strings.TrimSpace(body.Name))
	if err != nil {
		s.failSetup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

func (s *server) simAction(w http.ResponseWriter, err error) {
	if err != nil {
		s.failSetup(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSetupSimIntents(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	if s.simEnv(w, wz) == nil {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	s.simAction(w, wz.SimSetIntents(r.Context(), body.Enabled))
}

func (s *server) handleSetupSimInvite(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	if s.simEnv(w, wz) == nil {
		return
	}
	var body struct {
		ServerID string `json:"serverId"`
		Low      bool   `json:"low"`
	}
	if r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &body); err != nil {
			s.fail(w, err)
			return
		}
	}
	err := wz.SimInvite(r.Context(), body.ServerID, body.Low)
	if err != nil && strings.Contains(err.Error(), "already in server") {
		err = &setup.Error{Status: http.StatusConflict, Code: "conflict", Message: "The bot is already in this server."}
	}
	s.simAction(w, err)
}

func (s *server) handleSetupSimRaise(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	if s.simEnv(w, wz) == nil {
		return
	}
	s.simAction(w, wz.SimRaiseRole(r.Context()))
}

func (s *server) handleSetupSimMessage(w http.ResponseWriter, r *http.Request, wz *setup.Wizard) {
	if s.simEnv(w, wz) == nil {
		return
	}
	s.simAction(w, wz.SimSendMessage(r.Context()))
}

// ───────────────────────────────────────────────────────────── catalog & diagnostics

func (s *server) writeCatalog(w http.ResponseWriter) {
	es, err := s.opts.Catalog.List()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": es})
}

// handleCatalog lists the installable plugins (admins of any server, setup, bearer).
func (s *server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAuth(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !id.bearer && !id.setup {
		admin := false
		if s.rt != nil {
			for _, k := range s.rt.ServerKeys() {
				if ro, _, err := s.roleIn(r.Context(), id, k, false); err == nil && ro >= roleAdmin {
					admin = true
					break
				}
			}
		}
		if !admin {
			s.fail(w, httpErr(http.StatusForbidden, "forbidden", "requires the admin role on a server"))
			return
		}
	}
	s.writeCatalog(w)
}

// handleDiagnostics runs the setup checklist on a server (admin Diagnostics page).
func (s *server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleAdmin); err != nil {
		s.fail(w, err)
		return
	}
	if s.opts.Checker == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "diagnostics are not available on this runtime"))
		return
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			s.fail(w, httpErr(http.StatusBadRequest, "invalid", "since must be an RFC 3339 time"))
			return
		}
		since = t
	}
	writeJSON(w, http.StatusOK, reportDTO(s.opts.Checker.Run(r.Context(), key, since)))
}
