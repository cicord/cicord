package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/npjonath/cicord/internal/discord"
)

const fetchPlugin = `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({
  commands: {
    fetch: command({
      description: "x",
      options: { url: { type: "string", description: "url", required: true }, method: { type: "string", description: "m" } },
      async run(ctx, it) {
        let out: unknown;
        try {
          const r = await ctx.http.fetch(it.options.url, {
            method: it.options.method, headers: { "X-Test": "1", Authorization: "secret" }, body: it.options.method ? "payload" : undefined,
          });
          const text = await r.text();
          let json: unknown = null;
          try { json = await r.json(); } catch {}
          out = { status: r.status, type: r.headers["content-type"], text, json };
        } catch (e) {
          out = { error: (e as Error).message };
        }
        await it.reply(JSON.stringify(out));
      },
    }),
  },
});`

func fetchVia(t *testing.T, h *harness, rawURL, method string) map[string]any {
	t.Helper()
	resetCalls(h)
	opts := []discord.InteractionOption{{Name: "url", Type: discord.OptionString, Value: rawURL}}
	if method != "" {
		opts = append(opts, discord.InteractionOption{Name: "method", Type: discord.OptionString, Value: method})
	}
	if err := h.command("fetch", opts, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(contentOf(h.responses()[0])), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHTTPFetchAllowList(t *testing.T) {
	var lastHeaders http.Header
	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastHeaders = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"method":%q}`, r.Method)
		case "/redirect-ok":
			http.Redirect(w, r, "/json", http.StatusFound)
		case "/redirect-out":
			http.Redirect(w, r, "http://evil.example.net/steal", http.StatusFound)
		case "/redirect-loop":
			http.Redirect(w, r, "/redirect-loop", http.StatusFound)
		case "/big":
			w.Write([]byte(strings.Repeat("x", 2048)))
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL) // http://127.0.0.1:port

	h := start(t, fetchPlugin, func(e *Env) {
		e.NetworkAllow = []string{u.Host}
		e.AllowPrivateNetworks = true // dev mode: httptest listens on loopback, plain http
		e.Limits.MaxResponseBytes = 1024
	})

	got := fetchVia(t, h, srv.URL+"/json", "")
	if got["status"] != float64(200) || got["type"] != "application/json" || !jsonEq(got["json"], map[string]any{"method": "GET"}) {
		t.Fatalf("GET: %v", got)
	}
	if lastHeaders.Get("X-Test") != "1" || lastHeaders.Get("User-Agent") != "cicord-plugin/1" {
		t.Fatalf("headers: %v", lastHeaders)
	}
	got = fetchVia(t, h, srv.URL+"/json", "post")
	if !jsonEq(got["json"], map[string]any{"method": "POST"}) || lastBody != "payload" {
		t.Fatalf("POST: %v body %q", got, lastBody)
	}
	got = fetchVia(t, h, srv.URL+"/redirect-ok", "")
	if !jsonEq(got["json"], map[string]any{"method": "GET"}) {
		t.Fatalf("same-host redirect: %v", got)
	}

	for path, want := range map[string]string{
		"/redirect-out":  "evil.example.net is not in spec.network.allow",
		"/redirect-loop": "too many redirects",
		"/big":           "response body larger than 1024 bytes",
	} {
		if got := fetchVia(t, h, srv.URL+path, ""); !strings.Contains(fmt.Sprint(got["error"]), want) {
			t.Errorf("%s: %v, want %q", path, got, want)
		}
	}
	for _, bad := range []string{"http://example.org/", "ftp://" + u.Host + "/", "http://user:pw@" + u.Host + "/json", "not a url"} {
		if got := fetchVia(t, h, bad, ""); got["error"] == nil {
			t.Errorf("%s accepted: %v", bad, got)
		}
	}
}

func jsonEq(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestHTTPFetchRejectsPrivateAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL) // https://127.0.0.1:port
	_, port, _ := strings.Cut(u.Host, ":")

	h := start(t, fetchPlugin, func(e *Env) {
		// The allow-list matches: only the address check can refuse these.
		e.NetworkAllow = []string{u.Host, "localhost:" + port, "169.254.169.254", "metadata.test.invalid"}
	})
	for _, target := range []string{
		srv.URL + "/",                     // loopback IP literal
		"https://localhost:" + port + "/", // name resolving to loopback
		"https://169.254.169.254/latest/", // cloud metadata
	} {
		got := fetchVia(t, h, target, "")
		if msg := fmt.Sprint(got["error"]); !strings.Contains(msg, "non-public address") {
			t.Errorf("%s: %v", target, got)
		}
	}
	// Plain http is refused outside dev mode, before any connection.
	if got := fetchVia(t, h, "http://"+u.Host+"/", ""); !strings.Contains(fmt.Sprint(got["error"]), "only https") {
		t.Errorf("http: %v", got)
	}
}

func TestCheckPublic(t *testing.T) {
	private := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"224.0.0.1", "255.255.255.255", "::1", "::", "fc00::1", "fd12::1", "fe80::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::a00:1", "2002:a00:1::", "2001:db8::1", "198.18.0.1",
	}
	for _, s := range private {
		if err := checkPublic(netip.MustParseAddr(s)); !errors.Is(err, ErrEgressDenied) {
			t.Errorf("%s should be refused", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111", "::ffff:1.1.1.1"} {
		if err := checkPublic(netip.MustParseAddr(s)); err != nil {
			t.Errorf("%s should be allowed: %v", s, err)
		}
	}
}

func TestAllowListMatching(t *testing.T) {
	e := newEgress([]string{"api.example.com", "*.cdn.example.org", "hooks.example.net:8443"}, false, nil, 1)
	cases := map[string]bool{
		"https://api.example.com/x":       true,
		"https://API.example.com./x":      true,
		"https://api.example.com:443/x":   true,
		"https://api.example.com:8443/x":  false, // no port in the entry: default port only
		"https://evil-api.example.com/":   false,
		"https://example.com/":            false,
		"https://a.cdn.example.org/":      true,
		"https://a.b.cdn.example.org/":    true,
		"https://cdn.example.org/":        false, // wildcard matches subdomains only
		"https://hooks.example.net:8443/": true,
		"https://hooks.example.net/":      false,
		"http://api.example.com/":         false, // https only
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		if err := e.checkURL(u); (err == nil) != want {
			t.Errorf("%s: allowed=%v, want %v (%v)", raw, err == nil, want, err)
		}
	}
}

func TestHTTPFetchTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	u, _ := url.Parse(srv.URL)
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({ commands: { f: command({ description: "x", options: { url: { type: "string", description: "u", required: true } },
  async run(ctx, it) {
    try { await ctx.http.fetch(it.options.url, { timeoutMs: 50 }); } catch (e) { await it.reply((e as Error).message); }
  } }) } });`, func(e *Env) { e.NetworkAllow = []string{u.Host}; e.AllowPrivateNetworks = true })
	if err := h.command("f", []discord.InteractionOption{{Name: "url", Type: discord.OptionString, Value: srv.URL}}, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); !strings.Contains(got, "timed out after 50ms") {
		t.Fatalf("got %q", got)
	}
}

// TestHTTPFetchLargeJSONWithinDefaultBudget: a response near the default size cap must be
// usable with the default sync budget (host-side JSON parsing is not budgeted).
func TestHTTPFetchLargeJSONWithinDefaultBudget(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	n := 0
	for sb.Len() < 4<<20 {
		if n > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%d,"name":"item","ok":true}`, n)
		n++
	}
	sb.WriteString("]")
	body := sb.String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := start(t, `
import { command, definePlugin } from "@cicord/sdk";
export default definePlugin({ commands: { f: command({ description: "x", options: { url: { type: "string", description: "u", required: true } },
  async run(ctx, it) {
    const r = await ctx.http.fetch(it.options.url);
    const items = await r.json() as unknown[];
    await it.reply(String(items.length));
  } }) } });`, func(e *Env) { e.NetworkAllow = []string{u.Host}; e.AllowPrivateNetworks = true })
	if err := h.command("f", []discord.InteractionOption{{Name: "url", Type: discord.OptionString, Value: srv.URL}}, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != fmt.Sprint(n) {
		t.Fatalf("got %q, want %d items", got, n)
	}
}
