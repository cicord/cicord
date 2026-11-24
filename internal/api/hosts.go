package api

import (
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Admin host (Options.AdminURL): a deployment can serve the admin console on a private
// hostname (a VPN such as a Tailscale tailnet) and the community portal on a public one,
// both reaching the same listener. The request Host picks the URL used for the OAuth
// redirect URI and the Secure cookie flag, and admin routes only answer on the admin
// host. The Host header is chosen by the client: this is defense in depth, the network
// (a listener that is not reachable from the internet, a VPN) is the actual boundary.

// initAdminHost precomputes the normalized admin host (newServer). An admin URL that
// does not parse makes the admin routes unreachable on every host (fail closed).
func (s *server) initAdminHost() {
	s.opts.AdminURL = strings.TrimRight(s.opts.AdminURL, "/")
	if s.opts.AdminURL == "" {
		return
	}
	u, err := url.Parse(s.opts.AdminURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		s.log.Error("invalid admin URL: the admin routes are disabled", "url", s.opts.AdminURL)
		s.adminHost = "\x00invalid"
		return
	}
	s.adminPort = defaultPort(u.Scheme)
	s.adminHost = hostKey(u.Host, s.adminPort)
	s.adminScheme = u.Scheme
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// hostKey normalizes host[:port] for comparisons: lower case, without IPv6 brackets or a
// trailing dot, with defPort when the port is absent.
func hostKey(hostport, defPort string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, ""
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if port == "" {
		port = defPort
	}
	return net.JoinHostPort(host, port)
}

// onAdminHost reports whether the request was sent to the admin URL host (false without
// an admin URL). A Host without a port matches the default port of the admin URL scheme.
func (s *server) onAdminHost(r *http.Request) bool {
	return s.adminHost != "" && hostKey(r.Host, s.adminPort) == s.adminHost
}

// isAdminOrigin reports whether origin (an Origin header) is the admin URL origin.
func (s *server) isAdminOrigin(u *url.URL) bool {
	return s.adminHost != "" && u.Scheme == s.adminScheme && hostKey(u.Host, defaultPort(u.Scheme)) == s.adminHost
}

// hostRouteAllowed is the admin host restriction: with an admin URL, a request on any
// other host only reaches the routes shared with the portal (sharedPath); setup mode only
// answers the health endpoints there. Without an admin URL every route is allowed.
//
// The request path and the pattern the mux would match are both classified, so that a
// path the mux routes differently once cleaned or unescaped (%2F in a wildcard) cannot
// reach an admin handler.
func (s *server) hostRouteAllowed(r *http.Request) bool {
	if s.adminHost == "" || s.onAdminHost(r) {
		return true
	}
	p := cleanRoutePath(r.URL.Path)
	if s.setupMode {
		return p == "/healthz" || p == "/readyz"
	}
	if !sharedPath(p) {
		return false
	}
	if _, pattern := s.mux.Handler(r); pattern != "" {
		if _, pp, ok := strings.Cut(pattern, " "); ok {
			pattern = pp // "GET /api/v1/me" → "/api/v1/me"
		}
		return sharedPath(pattern)
	}
	return true
}

// cleanRoutePath cleans a request path, keeping a trailing slash.
func cleanRoutePath(p string) string {
	c := path.Clean("/" + p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c
}

// sharedPath reports whether a route answers on every host: the portal (SPA and
// /api/v1/portal/**), login (/auth/*), /api/v1/me, the machine endpoints (/api/v1/sync,
// /api/v1/plan, /hooks/*), health (/healthz, /readyz) and the simulator. The admin UI
// (/admin, /admin/*) and every other /api/ path (status, history, servers, catalog,
// store, registries, setup…) are admin routes: new endpoints are admin-only by default.
func sharedPath(p string) bool {
	switch {
	case p == "/admin" || strings.HasPrefix(p, "/admin/"):
		return false
	case p == "/api" || strings.HasPrefix(p, "/api/"):
		switch p {
		case "/api/v1/me", "/api/v1/sync", "/api/v1/plan":
			return true
		}
		return strings.HasPrefix(p, "/api/v1/portal/") || strings.HasPrefix(p, "/api/v1/sim/")
	}
	return true
}
