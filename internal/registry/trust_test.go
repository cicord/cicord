package registry

import (
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
	if !sameHost("https://git.example.org/r.git", "https://GIT.example.org/p.git") || sameHost("https://a.example/r", "https://b.example/r") {
		t.Error("sameHost")
	}
}
