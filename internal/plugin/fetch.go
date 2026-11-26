package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Egress policy for http.fetch (https://cicord.dev/docs/plugins/writing/ "Network egress"):
//  1. the hostname must match spec.network.allow ("*.example.com" matches subdomains only;
//     an entry without a port allows the scheme's default port only);
//  2. every address the name resolves to must be public;
//  3. the connection goes to the checked address (the dialer re-checks the exact IP it
//     connects to in net.Dialer.Control, which also defeats DNS rebinding);
//  4. redirects are followed manually, each hop re-checked against 1-3;
//  5. https only (http too with AllowPrivateNetworks), bounded body size and timeout.
// Proxies from the environment are never used.

const (
	maxRedirects   = 5
	maxRequestBody = 1 << 20
)

// ErrEgressDenied is wrapped by every http.fetch policy refusal.
var ErrEgressDenied = errors.New("network egress denied")

type egress struct {
	allow        []allowEntry
	allowPrivate bool
	client       *http.Client
	maxBytes     int64
}

type allowEntry struct {
	host     string // lowercase; leading "*." for wildcards
	port     string // "" = default port of the scheme
	wildcard bool
}

var (
	strictClient  = newEgressClient(false)
	privateClient = newEgressClient(true)
)

func newEgress(allow []string, allowPrivate bool, override *http.Client, maxBytes int64) *egress {
	e := &egress{allowPrivate: allowPrivate, maxBytes: maxBytes}
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		ent := allowEntry{host: a}
		if h, p, err := net.SplitHostPort(a); err == nil {
			ent.host, ent.port = h, p
		}
		if rest, ok := strings.CutPrefix(ent.host, "*."); ok {
			ent.host, ent.wildcard = rest, true
		}
		e.allow = append(e.allow, ent)
	}
	switch {
	case override != nil:
		c := *override
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		e.client = &c
	case allowPrivate:
		e.client = privateClient
	default:
		e.client = strictClient
	}
	return e
}

func newEgressClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrEgressDenied, err)
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("%w: unexpected dial address %q", ErrEgressDenied, address)
			}
			return checkPublic(ip)
		},
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid port in dial address %q: %w", addr, err)
		}
		var ips []netip.Addr
		if ip, err := netip.ParseAddr(host); err == nil {
			ips = []netip.Addr{ip}
		} else {
			ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%s: no addresses", host)
		}
		if !allowPrivate {
			for _, ip := range ips {
				if err := checkPublic(ip); err != nil {
					return nil, fmt.Errorf("%s resolves to a non-public address: %w", host, err)
				}
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, netip.AddrPortFrom(ip.Unmap(), uint16(port)).String())
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dial,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          64,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var nonPublicPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "this network"
		"100.64.0.0/10",   // CGNAT
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // reserved, broadcast
		"64:ff9b::/96",    // NAT64 (embeds IPv4)
		"64:ff9b:1::/48",  // local NAT64
		"100::/64",        // discard
		"2001::/32",       // Teredo (embeds IPv4)
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4 (embeds IPv4)
		"fec0::/10",       // site-local (deprecated)
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// checkPublic refuses loopback, private (RFC 1918, fc00::/7), link-local (incl.
// 169.254.169.254), CGNAT, multicast, unspecified, reserved and IPv4-embedding ranges.
// IPv4-mapped IPv6 addresses are checked as IPv4.
func checkPublic(ip netip.Addr) error {
	ip = ip.Unmap()
	bad := !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast()
	if !bad {
		for _, p := range nonPublicPrefixes {
			if p.Contains(ip) {
				bad = true
				break
			}
		}
	}
	if bad {
		return fmt.Errorf("%w: refusing to connect to non-public address %s", ErrEgressDenied, ip)
	}
	return nil
}

func defaultPort(scheme string) string {
	if scheme == "http" {
		return "80"
	}
	return "443"
}

// checkURL applies the scheme and allow-list rules.
func (e *egress) checkURL(u *url.URL) error {
	switch u.Scheme {
	case "https":
	case "http":
		if !e.allowPrivate {
			return fmt.Errorf("%w: only https URLs are allowed (got %s)", ErrEgressDenied, u.Redacted())
		}
	default:
		return fmt.Errorf("%w: unsupported URL scheme %q", ErrEgressDenied, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in URLs are not allowed", ErrEgressDenied)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrEgressDenied)
	}
	port := u.Port()
	if port == "" {
		port = defaultPort(u.Scheme)
	}
	for _, a := range e.allow {
		wantPort := a.port
		if wantPort == "" {
			wantPort = defaultPort(u.Scheme)
		}
		if port != wantPort {
			continue
		}
		if a.wildcard {
			if strings.HasSuffix(host, "."+a.host) {
				return nil
			}
		} else if host == a.host {
			return nil
		}
	}
	return fmt.Errorf("%w: host %s is not in spec.network.allow of plugin.yaml", ErrEgressDenied, u.Host)
}

type fetchInit struct {
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Body      *string           `json:"body"`
	Form      map[string]string `json:"form"`
	JSON      map[string]any    `json:"json"`
	TimeoutMs float64           `json:"timeoutMs"`
}

type fetchResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"bytes"`
}

var fetchMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true}

func opHTTPFetch(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capHTTPFetch); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		URL  string    `json:"url"`
		Init fetchInit `json:"init"`
	}](args)
	if err != nil {
		return nil, err
	}
	price, budget, err := i.providerCharge(a.URL, a.Init)
	if err != nil {
		return nil, err
	}
	materials, rawURL, err := i.prepareFetch(a.URL, &a.Init)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() {
		return nil, argErr("http.fetch: invalid URL")
	}
	sendOnly, err := i.checkWebhookSend(a.URL, u, &a.Init, materials)
	if err != nil {
		return nil, err
	}
	for _, s := range materials {
		if i.env.BudgetedSecrets[s.name] && (budget != s.name || len(materials) != 1) {
			return nil, errors.New("metered credential requires its approved request header")
		}
		if u.Scheme != "https" || newEgress(s.hosts, false, nil, 0).checkURL(u) != nil {
			return nil, fmt.Errorf("%w: destination outside secret host grant", ErrEgressDenied)
		}
	}
	if err := i.egress.checkURL(u); err != nil {
		return nil, err
	}
	method := strings.ToUpper(a.Init.Method)
	if method == "" {
		method = "GET"
	}
	if !fetchMethods[method] {
		return nil, argErr("http.fetch: unsupported method")
	}
	if a.Init.Body != nil && len(*a.Init.Body) > maxRequestBody {
		return nil, argErr("http.fetch: request body larger than %d bytes", maxRequestBody)
	}
	timeout := i.limits.FetchTimeout
	if a.Init.TimeoutMs > 0 {
		timeout = time.Duration(a.Init.TimeoutMs * float64(time.Millisecond))
	}
	if timeout > i.limits.HandlerTimeout {
		timeout = i.limits.HandlerTimeout
	}
	return func(ctx context.Context) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var settle func(context.Context, int) error
		if budget != "" {
			if i.env.ReserveBudget == nil {
				return nil, errors.New("provider budget unavailable")
			}
			var err error
			settle, err = i.env.ReserveBudget(ctx, budget, price)
			if errors.Is(err, ErrBudgetExhausted) {
				return &fetchResult{Status: 429, Headers: map[string]string{"x-cicord-budget": "exhausted"}}, nil
			}
			if err != nil {
				return nil, errors.New("provider budget unavailable")
			}
		}
		res, err := i.egress.do(ctx, method, u, a.Init.Headers, a.Init.Body, len(materials) > 0, sendOnly)
		if err != nil {
			if len(materials) > 0 {
				return nil, errors.New("http.fetch: credentialed request failed")
			}
			if ctx.Err() != nil && errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("http.fetch %s %s: timed out after %s", method, u.Redacted(), timeout)
			}
			return nil, fmt.Errorf("http.fetch %s %s: %w", method, u.Redacted(), err)
		}
		if settle != nil {
			cost := providerUsage(res, price)
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			if err := settle(cctx, cost); err != nil {
				return nil, errors.New("provider budget reconciliation unavailable")
			}
		}
		if err := redactResponse(res, materials, i.egress.maxBytes); err != nil {
			return nil, err
		}
		return res, nil
	}, nil
}

var strippedOnCrossHost = []string{"authorization", "cookie", "proxy-authorization"}

func (e *egress) do(ctx context.Context, method string, u *url.URL, headers map[string]string, body *string, sensitive, sendOnly bool) (*fetchResult, error) {
	hdr := http.Header{}
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "host", "content-length", "connection", "transfer-encoding", "te", "upgrade", "trailer", "keep-alive", "proxy-connection":
			continue
		}
		hdr.Set(k, v)
	}
	if hdr.Get("User-Agent") == "" {
		hdr.Set("User-Agent", "cicord-plugin/1")
	}
	for hop := 0; ; hop++ {
		if err := e.checkURL(u); err != nil {
			return nil, err
		}
		var rd io.Reader
		if body != nil {
			rd = strings.NewReader(*body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
		if err != nil {
			return nil, err
		}
		req.Header = hdr.Clone()
		resp, err := e.client.Do(req)
		if err != nil {
			return nil, err
		}
		if sendOnly {
			resp.Body.Close()
			return &fetchResult{Status: resp.StatusCode, Headers: map[string]string{}, Body: []byte{}}, nil
		}
		if loc := resp.Header.Get("Location"); isRedirect(resp.StatusCode) && loc != "" {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if sensitive {
				return nil, errors.New("credentialed requests cannot follow redirects")
			}
			if hop >= maxRedirects {
				return nil, fmt.Errorf("too many redirects (max %d)", maxRedirects)
			}
			next, err := u.Parse(loc)
			if err != nil {
				return nil, fmt.Errorf("invalid redirect location %q", loc)
			}
			if !strings.EqualFold(next.Host, u.Host) || next.Scheme != u.Scheme {
				for _, h := range strippedOnCrossHost {
					hdr.Del(h)
				}
			}
			switch {
			case resp.StatusCode == http.StatusSeeOther && method != "HEAD":
				method, body = "GET", nil
			case (resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound) && method == "POST":
				method, body = "GET", nil
			}
			if body == nil {
				hdr.Del("Content-Type")
			}
			u = next
			continue
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, e.maxBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > e.maxBytes {
			return nil, fmt.Errorf("response body larger than %d bytes", e.maxBytes)
		}
		out := &fetchResult{Status: resp.StatusCode, Headers: map[string]string{}, Body: data}
		keys := make([]string, 0, len(resp.Header))
		for k := range resp.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Headers[strings.ToLower(k)] = strings.Join(resp.Header[k], ", ")
		}
		return out, nil
	}
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}
