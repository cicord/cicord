package cli

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// normalizeAdminURL validates --admin-url: an absolute https URL (http only for a
// loopback host) without path, query or credentials. It returns the URL without a
// trailing slash ("" stays "").
func normalizeAdminURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", fmt.Errorf("--admin-url %q: not an absolute URL (e.g. https://cicord.example.ts.net)", raw)
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || strings.HasSuffix(raw, "#") {
		return "", fmt.Errorf("--admin-url %q: only scheme://host[:port] is allowed (no path, query or credentials)", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(strings.ToLower(u.Hostname())) {
			return "", fmt.Errorf("--admin-url %q: use https (http is only allowed for a loopback host)", raw)
		}
	default:
		return "", fmt.Errorf("--admin-url %q: the scheme must be https", raw)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// checkAdminDistinct fails when the admin URL has the host (and port) of the public URL:
// the admin host restriction would then do nothing.
func checkAdminDistinct(admin, public string) error {
	if admin == "" {
		return nil
	}
	a, err := url.Parse(admin)
	if err != nil {
		return fmt.Errorf("--admin-url %q: %w", admin, err)
	}
	p, err := url.Parse(public)
	if err != nil {
		return fmt.Errorf("public URL %q: %w", public, err)
	}
	if hostPortKey(a) == hostPortKey(p) {
		return fmt.Errorf("--admin-url %s has the host of the public URL %s: use a separate hostname (or omit --admin-url)", admin, public)
	}
	return nil
}

func hostPortKey(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.ToLower(u.Hostname()), "."), port)
}

// consoleURL is the base URL of the admin console: the admin URL when set, the public URL
// otherwise (setup link, startup banner, commit status target).
func consoleURL(admin, public string) string {
	if admin != "" {
		return admin
	}
	return public
}
