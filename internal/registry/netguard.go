package registry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// SSRF guard for registry fetches made on behalf of admin UI users (ADR-0011).
//
// A context marked with WithPublicOnly makes every git-over-https connection of that
// fetch refuse non-public addresses. The check runs in the dialer, after DNS resolution
// and for every connection (redirects included), so DNS rebinding cannot bypass it.
// Unmarked contexts (operator CLI, configured sources, write-back) are unaffected.

type publicOnlyKey struct{}

// WithPublicOnly marks ctx so that git fetches only connect to public addresses.
func WithPublicOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, publicOnlyKey{}, true)
}

func publicOnly(ctx context.Context) bool {
	v, _ := ctx.Value(publicOnlyKey{}).(bool)
	return v
}

// ErrNotPublic is returned when a guarded fetch would reach a non-public address.
var ErrNotPublic = errors.New("not a public address")

// deniedPrefixes are special-purpose ranges that IsGlobalUnicast/IsPrivate miss.
var deniedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2001:db8::/32", "2002::/16", "fc00::/7",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// IsPublicAddr reports whether an address is globally routable unicast.
func IsPublicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range deniedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func guardedControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	a, err := netip.ParseAddr(host)
	if err != nil || !IsPublicAddr(a) {
		return fmt.Errorf("%w: %s", ErrNotPublic, host)
	}
	return nil
}

func init() {
	base := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	guarded := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: guardedControl}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if publicOnly(ctx) {
			return guarded.DialContext(ctx, network, addr)
		}
		return base.DialContext(ctx, network, addr)
	}
	tr.Proxy = func(r *http.Request) (*url.URL, error) {
		if publicOnly(r.Context()) {
			return nil, nil // a proxy would hide the real destination from the guard
		}
		return http.ProxyFromEnvironment(r)
	}
	client.InstallProtocol("https", githttp.NewClient(&http.Client{Transport: tr}))
}

// CheckPublicHost refuses URLs whose host is, or resolves to, a non-public address. It
// gives an early, clear error; the dialer guard (WithPublicOnly) is what enforces it.
func CheckPublicHost(ctx context.Context, raw string) error {
	host := hostOf(raw)
	if host == "" {
		return fmt.Errorf("invalid URL %q", raw)
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return fmt.Errorf("%s is not a public host", host)
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return fmt.Errorf("cannot resolve %s: %w", host, err)
		}
		addrs = ips
	}
	for _, a := range addrs {
		if !IsPublicAddr(a) {
			return fmt.Errorf("%s is not a public host", host)
		}
	}
	return nil
}
