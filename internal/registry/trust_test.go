package registry

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/npjonath/codi/internal/config"
)

func TestTrustAndURLs(t *testing.T) {
	if got := Trust("https://github.com/acme/plugins.git"); got != TrustUnofficial {
		t.Errorf("Trust = %q", got)
	}
	old := OfficialURLs
	t.Cleanup(func() { OfficialURLs = old })
	OfficialURLs = []string{"https://github.com/codi-dev/registry"}
	for _, u := range []string{"https://github.com/codi-dev/registry", "HTTPS://GitHub.com/codi-dev/registry.git", "https://github.com/codi-dev/registry/"} {
		if Trust(u) != TrustOfficial {
			t.Errorf("Trust(%q) is not official", u)
		}
	}
	if Trust("https://github.com/codi-dev/registry-fork") != TrustUnofficial {
		t.Error("a similar URL must not be official")
	}
	if config.NormalizeRegistryURL("https://Example.org/A/b.git/") != "https://example.org/A/b" {
		t.Errorf("normalize = %q", config.NormalizeRegistryURL("https://Example.org/A/b.git/"))
	}
	for _, c := range []struct {
		reg, repo string
		ok        bool
	}{
		{"https://example.org/r.git", "https://example.org/p.git", true},
		{"https://example.org/r.git", "file:///etc", false},
		{"https://example.org/r.git", "/srv/plugin", false},
		{"https://example.org/r.git", "http://169.254.169.254/x", false},
		{"/srv/registry", "/srv/plugin", true},
	} {
		if err := CheckRepoURL(c.reg, c.repo); (err == nil) != c.ok {
			t.Errorf("CheckRepoURL(%q, %q) = %v", c.reg, c.repo, err)
		}
	}
}

func TestCheckPublicHost(t *testing.T) {
	ctx := t.Context()
	for _, u := range []string{"https://127.0.0.1/r.git", "https://10.1.2.3/r.git", "https://169.254.169.254/latest",
		"https://[::1]/r", "https://localhost/r", "https://git.localhost/r", "https://0.0.0.0/r", "https://192.168.1.10:8443/r", "not a url"} {
		if err := CheckPublicHost(ctx, u); err == nil {
			t.Errorf("CheckPublicHost(%q) accepted", u)
		}
	}
	if err := CheckPublicHost(ctx, "https://8.8.8.8/r.git"); err != nil {
		t.Errorf("public IP refused: %v", err)
	}
	for _, a := range []string{"100.64.1.1", "198.18.0.1", "0.1.2.3", "64:ff9b::a00:1", "2002:a00:1::1", "fd00::1", "::ffff:127.0.0.1", "fe80::1", "224.0.0.1"} {
		if IsPublicAddr(netip.MustParseAddr(a)) {
			t.Errorf("IsPublicAddr(%s) = true", a)
		}
	}
	for _, a := range []string{"8.8.8.8", "2606:4700::1111"} {
		if !IsPublicAddr(netip.MustParseAddr(a)) {
			t.Errorf("IsPublicAddr(%s) = false", a)
		}
	}
	// The dialer guard refuses a loopback git server even when the URL looks fine.
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := FetchIndex(WithPublicOnly(ctx), srv.URL+"/r.git"); !errors.Is(err, ErrNotPublic) {
		t.Errorf("guarded fetch of a loopback server: %v", err)
	}
}

func TestNamesAndCachePaths(t *testing.T) {
	for _, s := range []string{"acme/../x", "../x/y", "acme/..", "acme/a/b", "acme/A", "acme", ".x/y"} {
		if _, _, err := CheckSource(s); err == nil {
			t.Errorf("CheckSource(%q) accepted", s)
		}
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	if _, err := cachePath(t.TempDir(), "acme/hello", LockEntry{Version: "1.0.0", Commit: sha}); err != nil {
		t.Errorf("valid cache path: %v", err)
	}
	for _, e := range []LockEntry{{Version: "../../x", Commit: sha}, {Version: "1.0.0", Commit: "../x"}} {
		if _, err := cachePath(t.TempDir(), "acme/hello", e); err == nil {
			t.Errorf("cachePath accepted %+v", e)
		}
	}
	if _, err := Materialize(t.Context(), t.TempDir(), "acme/../../etc", LockEntry{Version: "1.0.0", Commit: sha}); err == nil {
		t.Error("Materialize accepted a traversal source")
	}
	if got := Redact("clone https://bob:s3cret@git.example/r.git: auth failed"); got != "clone https://***@git.example/r.git: auth failed" {
		t.Errorf("Redact = %q", got)
	}
	if !HasUserinfo("https://tok@github.com/a/b") || HasUserinfo("https://github.com/a/b") {
		t.Error("HasUserinfo")
	}
}
