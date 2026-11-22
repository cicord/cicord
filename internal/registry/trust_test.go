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
