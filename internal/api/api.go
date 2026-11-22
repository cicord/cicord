// Package api implements the CODI runtime HTTP API v1 (spec/api.md): authentication,
// admin endpoints (status, servers, plugin configuration with git write-back), the
// community portal (including plugin routes), machine endpoints (CI, webhooks), the
// Discord simulator endpoints of `codi dev --sim` and the embedded static UIs.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/npjonath/codi/internal/catalog"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/setup"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/store"
	"github.com/npjonath/codi/internal/writeback"
)

// OAuth configures the Discord OAuth2 authorization-code flow (scope identify).
type OAuth struct {
	// ClientID defaults to the application ID of the first bot (by key) of the active
	// revision when empty.
	ClientID     string
	ClientSecret string
	// SecretFunc, when set and non-empty, takes precedence over ClientSecret (resolved on
	// every login: env first, then the secret store).
	SecretFunc func() string
	// AuthorizeURL, TokenURL and APIURL default to Discord's (overridable for tests).
	AuthorizeURL string
	TokenURL     string
	APIURL       string
}

type Options struct {
	Runtime *runtime.Runtime
	// Store defaults to Runtime.Store().
	Store state.Store
	// Writer performs configuration write-back (nil disables config edits: 503).
	Writer writeback.Writer
	// Mode is "dev" or "serve" (default "serve"); dev enables /auth/dev-login.
	Mode string
	// Sim enables the simulator endpoints (spec/api.md §8).
	Sim *sim.World
	// RuntimeToken is the bearer token of machine clients ("" disables bearer auth).
	RuntimeToken string
	// WebhookSecret verifies GitHub webhooks ("" rejects every webhook).
	WebhookSecret string
	// Branch is the tracked branch for webhooks (default: codi.yaml spec.source.branch, then "main").
	Branch string
	OAuth  OAuth
	// PublicURL is the public origin of the runtime (OAuth redirect URI, Secure cookies).
	// Empty: derived from each request.
	PublicURL string
	// CORSOrigins are external portal origins allowed with credentials.
	CORSOrigins []string
	// AdminUI is served under /admin/, Portal under / (both with SPA fallback). Nil: 404.
	// The custom portal of the active revision (codi.yaml spec.portal.path) takes
	// precedence over Portal once it has been built.
	AdminUI fs.FS
	Portal  fs.FS
	Logger  *slog.Logger
	// SessionTTL (default 7 days); link sessions last at most 24 h.
	SessionTTL time.Duration
	// TokenSessionTTL bounds bearer session tokens of token mode (default 24 h).
	TokenSessionTTL time.Duration
	// MemberCacheTTL caches Discord member lookups used for roles (default 30 s; <0 disables).
	MemberCacheTTL time.Duration
	// MaxBodyBytes limits JSON bodies (default 1 MiB); MaxUploadBytes the plan upload (default 32 MiB).
	MaxBodyBytes   int64
	MaxUploadBytes int64
	// RequestTimeout bounds non-streaming handlers (default 60 s).
	RequestTimeout time.Duration
	// ActivationTimeout is how long a config PUT waits for the new revision (default 20 s).
	ActivationTimeout time.Duration
	HTTPClient        *http.Client
	// Stopping, when closed, ends long-lived streams (SSE) so that a graceful HTTP
	// shutdown does not wait for them.
	Stopping <-chan struct{}
	// Setup enables the setup wizard endpoints (/api/v1/setup/*, setup.go).
	Setup *setup.Wizard
	// Checker runs the Diagnostics checklist (nil: GET …/diagnostics answers 503).
	Checker *setup.Checker
	// Catalog is the plugin catalog (default: the official plugins bundled in the binary).
	Catalog *catalog.Catalog
	// PluginStore lists and describes Store plugins (default: Catalog + the registries of
	// the active codi.yaml, cached in registry.DefaultCacheDir()).
	PluginStore *store.Service
}

type server struct {
	opts  Options
	rt    *runtime.Runtime
	store state.Store
	log   *slog.Logger
	mux   *http.ServeMux

	members memberCache
	writeMu sync.Mutex // serializes config write-backs
}

// New returns the API handler.
func New(opts Options) http.Handler {
	s := newServer(opts)
	s.routes()
	return s.middleware(s.mux)
}

func newServer(opts Options) *server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if opts.Store == nil && opts.Runtime != nil {
		opts.Store = opts.Runtime.Store()
	}
	if opts.Mode == "" {
		opts.Mode = "serve"
	}
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 7 * 24 * time.Hour
	}
	if opts.TokenSessionTTL == 0 {
		opts.TokenSessionTTL = 24 * time.Hour
	}
	if opts.MemberCacheTTL == 0 {
		opts.MemberCacheTTL = 30 * time.Second
	}
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = 1 << 20
	}
	if opts.MaxUploadBytes == 0 {
		opts.MaxUploadBytes = 32 << 20
	}
	if opts.RequestTimeout == 0 {
		opts.RequestTimeout = 60 * time.Second
	}
	if opts.ActivationTimeout == 0 {
		opts.ActivationTimeout = 20 * time.Second
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if opts.OAuth.AuthorizeURL == "" {
		opts.OAuth.AuthorizeURL = "https://discord.com/oauth2/authorize"
	}
	if opts.OAuth.TokenURL == "" {
		opts.OAuth.TokenURL = "https://discord.com/api/v10/oauth2/token"
	}
	if opts.OAuth.APIURL == "" {
		opts.OAuth.APIURL = "https://discord.com/api/v10"
	}
	opts.PublicURL = strings.TrimRight(opts.PublicURL, "/")
	if opts.Catalog == nil {
		opts.Catalog = catalog.Official()
	}
	if opts.PluginStore == nil {
		opts.PluginStore = &store.Service{Catalog: opts.Catalog, CacheDir: registry.DefaultCacheDir(),
			PublicOnly: !registry.AllowPrivateRegistries()}
	}
	s := &server{opts: opts, rt: opts.Runtime, store: opts.Store, log: opts.Logger, mux: http.NewServeMux()}
	s.members.entries = map[string]memberEntry{}
	return s
}

func (s *server) routes() {
	m := s.mux
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request)) { m.HandleFunc(pattern, fn) }

	// §1 auth
	h("GET /auth/login", s.handleLogin)
	h("GET /auth/callback", s.handleCallback)
	h("POST /auth/link", s.handleLink)
	h("POST /auth/dev-login", s.handleDevLogin)
	h("POST /auth/token", s.handleToken)
	h("POST /auth/logout", s.handleLogout)
	h("/auth/", notFoundHandler)

	// §2–5 admin
	h("GET /api/v1/me", s.handleMe)
	h("GET /api/v1/status", s.handleStatus)
	h("GET /api/v1/history", s.handleHistory)
	h("GET /api/v1/servers/{server}", s.handleServer)
	h("GET /api/v1/servers/{server}/resources", s.handleResources)
	h("GET /api/v1/servers/{server}/plan", s.handlePlan)
	h("POST /api/v1/servers/{server}/sync", s.handleServerSync)
	h("GET /api/v1/servers/{server}/audit", s.handleAudit)
	h("GET /api/v1/servers/{server}/plugins", s.handlePlugins)
	h("PUT /api/v1/servers/{server}/plugins/{bot}/{instance}/config", s.handleConfigPut)
	h("GET /api/v1/servers/{server}/assets/{kind}", s.handleAssetGet)
	h("PUT /api/v1/servers/{server}/assets/{kind}", s.handleAssetPut)

	// §6 portal
	h("GET /api/v1/portal/servers", s.handlePortalServers)
	h("GET /api/v1/portal/{server}", s.handlePortalServer)
	h("GET /api/v1/portal/{server}/logo", s.handlePortalImage("logo"))
	h("GET /api/v1/portal/{server}/banner", s.handlePortalImage("banner"))
	h("GET /api/v1/portal/{server}/me", s.handlePortalMe)
	h("/api/v1/portal/{server}/plugins/{bot}/{instance}/{path...}", s.handlePluginRoute)

	// §7 machine
	h("POST /api/v1/sync", s.handleSync)
	h("POST /api/v1/plan", s.handlePlanUpload)
	h("POST /hooks/github", s.handleGitHubHook)
	h("/hooks/", notFoundHandler)
	h("GET /healthz", s.handleHealthz)
	h("GET /readyz", s.handleReadyz)

	// §8 simulator
	h("GET /api/v1/sim/state", s.simOnly(s.handleSimState))
	h("POST /api/v1/sim/users", s.simOnly(s.handleSimCreateUser))
	h("POST /api/v1/sim/servers/{id}/members", s.simOnly(s.handleSimJoin))
	h("POST /api/v1/sim/servers/{id}/boosts", s.simOnly(s.handleSimBoosts))
	h("DELETE /api/v1/sim/servers/{id}/members/{userId}", s.simOnly(s.handleSimLeave))
	h("GET /api/v1/sim/channels/{id}/messages", s.simOnly(s.handleSimMessages))
	h("GET /api/v1/sim/users/{id}/dms", s.simOnly(s.handleSimDMs))
	h("POST /api/v1/sim/messages", s.simOnly(s.handleSimSend))
	h("POST /api/v1/sim/commands", s.simOnly(s.handleSimCommand))
	h("GET /api/v1/sim/commands", s.simOnly(s.handleSimCommands))
	h("POST /api/v1/sim/components", s.simOnly(s.handleSimComponent))
	h("POST /api/v1/sim/modals", s.simOnly(s.handleSimModal))
	h("GET /api/v1/sim/events", s.simOnly(s.handleSimEvents))

	// setup wizard, catalog, diagnostics (setup.go)
	s.setupRoutes(h)

	h("/api/", notFoundHandler)

	// static UIs
	h("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	h("/admin/", s.staticHandler(s.opts.AdminUI, "/admin/", true))
	h("/", s.portalHandler())
}

// ───────────────────────────────────────────────────────────── middleware

type ctxKey int

const longRunningKey ctxKey = 1

func (s *server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("api panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				if !sw.wrote {
					writeError(sw, http.StatusInternalServerError, "internal", "internal error")
				}
			}
			if sw.status >= 500 {
				s.log.Warn("api request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration", time.Since(start))
			} else {
				s.log.Debug("api request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration", time.Since(start))
			}
		}()

		hdr := sw.Header()
		if !s.hostAllowed(r) {
			// DNS-rebinding protection: a dev runtime only answers to loopback hosts (or
			// the configured public URL).
			writeError(sw, http.StatusMisdirectedRequest, "forbidden", "unexpected Host header")
			return
		}
		hdr.Set("X-Content-Type-Options", "nosniff")
		// Portal URLs carry codi_token: never leak them to other origins.
		hdr.Set("Referrer-Policy", "same-origin")

		// CORS
		// Operator origins (--cors-origin) may use cookies; repository origins
		// (spec.portal.allowedOrigins) only token mode (no Allow-Credentials).
		origin := r.Header.Get("Origin")
		allowed, credentials := s.originPolicy(origin)
		hdr.Add("Vary", "Origin")
		if allowed {
			hdr.Set("Access-Control-Allow-Origin", origin)
			if credentials {
				hdr.Set("Access-Control-Allow-Credentials", "true")
			}
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			if allowed {
				hdr.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				hdr.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Codi-Request, Last-Event-ID")
				hdr.Set("Access-Control-Max-Age", "600")
			}
			sw.WriteHeader(http.StatusNoContent)
			return
		}

		// CSRF (spec/api.md §1): unsafe methods need X-Codi-Request unless they carry an
		// Authorization header; cross-origin requests must come from an allowed origin.
		if !isSafeMethod(r.Method) && !strings.HasPrefix(r.URL.Path, "/hooks/") {
			if origin != "" && !allowed && !s.sameOrigin(r, origin) {
				writeError(sw, http.StatusForbidden, "forbidden", "cross-origin request from a non-allowed origin")
				return
			}
			// Browsers never attach an Authorization header on their own, so requests
			// carrying one (valid or not: auth answers 401) cannot be forged cross-site.
			if r.Header.Get("Authorization") == "" && r.Header.Get("X-Codi-Request") == "" {
				writeError(sw, http.StatusForbidden, "forbidden", "missing X-Codi-Request header (CSRF protection)")
				return
			}
		}

		if r.URL.Path != "/api/v1/sim/events" {
			ctx, cancel := context.WithTimeout(r.Context(), s.opts.RequestTimeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(sw, r)
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (s *server) sameOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if s.opts.PublicURL != "" {
		// With a configured public URL, the Host header (attacker-influenced through DNS
		// rebinding) is never trusted for origin checks.
		p, err := url.Parse(s.opts.PublicURL)
		if err == nil && p.Scheme == u.Scheme && p.Host == u.Host {
			return true
		}
		// Dev runtimes: localhost and 127.0.0.1 are interchangeable (both loopback, and
		// hostAllowed already restricts dev to loopback hosts).
		return err == nil && s.opts.Mode == "dev" && isLoopback(u.Hostname()) && isLoopback(p.Hostname()) && u.Port() == p.Port()
	}
	return u.Host == r.Host
}

func isLoopback(h string) bool {
	h = strings.Trim(h, "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

// hostAllowed restricts dev runtimes to loopback hosts or the public URL host.
func (s *server) hostAllowed(r *http.Request) bool {
	if s.opts.Mode != "dev" {
		return true
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if strings.HasSuffix(host, ".localhost") {
		return true
	}
	if s.opts.PublicURL != "" {
		if p, err := url.Parse(s.opts.PublicURL); err == nil && strings.EqualFold(p.Hostname(), host) {
			return true
		}
	}
	return false
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status, w.wrote = http.StatusOK, true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush / SetWriteDeadline.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ───────────────────────────────────────────────────────────── JSON helpers

type apiError struct {
	Code        string             `json:"code"`
	Message     string             `json:"message"`
	Diagnostics config.Diagnostics `json:"diagnostics,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": apiError{Code: code, Message: msg}})
}

func writeDiagnostics(w http.ResponseWriter, msg string, ds config.Diagnostics) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": apiError{Code: "invalid", Message: msg, Diagnostics: ds}})
}

func notFoundHandler(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "not found")
}

// errHTTP is an error with an HTTP status and API error code.
type errHTTP struct {
	status int
	code   string
	msg    string
}

func (e *errHTTP) Error() string { return e.msg }

func httpErr(status int, code, format string, args ...any) error {
	return &errHTTP{status: status, code: code, msg: fmt.Sprintf(format, args...)}
}

func (s *server) fail(w http.ResponseWriter, err error) {
	var he *errHTTP
	if errors.As(err, &he) {
		writeError(w, he.status, he.code, he.msg)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "request timed out")
		return
	}
	s.log.Error("api error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

// decodeJSON reads a JSON body (bounded) into v.
func (s *server) decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.opts.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return httpErr(http.StatusRequestEntityTooLarge, "invalid", "request body too large")
		}
		if errors.Is(err, io.EOF) {
			return httpErr(http.StatusBadRequest, "invalid", "a JSON body is required")
		}
		return httpErr(http.StatusBadRequest, "invalid", "invalid JSON body: %v", err)
	}
	return nil
}

func (s *server) requireRuntime() error {
	if s.rt == nil || !s.rt.Ready() {
		return httpErr(http.StatusServiceUnavailable, "unavailable", "no active revision yet")
	}
	return nil
}
