package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/npjonath/codi/internal/catalog"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
)

const officialURL = "https://registry.codi.example/official.git"

// withOfficial makes officialURL the official registry for the test (not parallel).
func withOfficial(t *testing.T) {
	t.Helper()
	t.Setenv(registry.DevOfficialRegistryEnv, "")
	old := registry.OfficialURLs
	t.Cleanup(func() { registry.OfficialURLs = old })
	registry.OfficialURLs = []string{officialURL}
}

func testCatalog() *catalog.Catalog {
	return catalog.New(os.DirFS("../../examples"), []string{"basic/plugins/welcome", "basic/plugins/onboarding"})
}

func officialIndex(signed bool) *registry.Index {
	idx := &registry.Index{Plugins: map[string]registry.IndexEntry{
		"welcome":    {Repo: "https://git.example/welcome.git", Version: "9.0.0", Publisher: "CODI", Verified: true},
		"onboarding": {Repo: "https://git.example/onboarding.git", Version: "0.0.1"},
		"xp":         {Repo: "https://git.example/xp.git", Version: "1.0.0", Publisher: "acme", Verified: true},
		"Bad.Name":   {Repo: "https://git.example/bad.git"},
	}}
	if signed {
		idx.SignedBy = "0123456789abcdef"
	}
	return idx
}

func TestImplicitOfficialSource(t *testing.T) {
	ctx := context.Background()
	// Without an official URL, "codi" is the bundled catalog only (today's behaviour).
	s := &Service{Catalog: testCatalog(), Fetch: func(context.Context, string) (*registry.Index, error) {
		t.Fatal("nothing to fetch")
		return nil, nil
	}}
	t.Setenv(registry.DevOfficialRegistryEnv, "")
	srcs, err := s.Sources(ctx, nil, false)
	if err != nil || len(srcs) != 1 || srcs[0].URL != "" || srcs[0].Signature != "" || len(srcs[0].Plugins) != 2 {
		t.Fatalf("bundled only: %+v, %v", srcs, err)
	}

	withOfficial(t)
	signed := true
	s = &Service{Catalog: testCatalog(), Fetch: func(_ context.Context, url string) (*registry.Index, error) {
		// What an official URL serves; the store caches by normalized URL.
		return officialIndex(signed && registry.Trust(url) == registry.TrustOfficial), nil
	}}
	srcs, err = s.Sources(ctx, repoWith(config.Registry{Name: "mirror", URL: officialURL + "/"}), true)
	if err != nil {
		t.Fatal(err)
	}
	codi := srcs[0]
	if codi.Name != "codi" || codi.Kind != KindBundled || codi.URL != officialURL || codi.Signature != SignatureVerified || codi.Error != nil {
		t.Fatalf("codi = %+v", codi)
	}
	got := map[string]Plugin{}
	var names []string
	for _, p := range codi.Plugins {
		got[p.Name] = p
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "onboarding,welcome,xp" {
		t.Errorf("plugins = %v", names)
	}
	if p := got["welcome"]; p.Version != "9.0.0" || p.Repo == "" || !p.Verified || p.Trust != registry.TrustOfficial {
		t.Errorf("a newer remote version wins: %+v", p)
	}
	if p := got["onboarding"]; p.Version != "0.1.0" || p.Repo != "" {
		t.Errorf("an older remote version loses to the bundled one: %+v", p)
	}
	if p := got["xp"]; !p.Verified || p.Source != "codi" || p.Publisher != "acme" {
		t.Errorf("remote-only plugin: %+v", p)
	}
	// The official URL declared under another name is official too (same signed index).
	if m := srcs[1]; m.Trust != registry.TrustOfficial || m.Signature != SignatureVerified || len(m.Plugins) != 3 {
		t.Errorf("mirror = %+v", m)
	}

	for name, want := range map[string]bool{"welcome": true, "onboarding": false, "xp": true} {
		reg, remote, err := s.OfficialRemote(ctx, name)
		if err != nil || remote != want || (remote && reg.URL != officialURL) {
			t.Errorf("OfficialRemote(%s) = %+v %v %v", name, reg, remote, err)
		}
	}
	if _, _, err := s.OfficialRemote(ctx, "nope"); !errors.Is(err, ErrUnknownPlugin) {
		t.Errorf("unknown: %v", err)
	}

	// Fails closed: an unsigned official index is an error source with the bundled plugins
	// only, even when Fetch (replaceable) forgets to check.
	signed = false
	srcs, _ = s.Sources(ctx, repoWith(config.Registry{Name: "mirror", URL: officialURL}), true)
	codi = srcs[0]
	if codi.Error == nil || codi.Signature != SignatureInvalid || len(codi.Plugins) != 2 || !strings.Contains(codi.Error.Message, "signature") {
		t.Errorf("unsigned codi = %+v", codi)
	}
	for _, p := range codi.Plugins {
		if p.Repo != "" || p.Version != "0.1.0" {
			t.Errorf("a remote plugin leaked from an unsigned index: %+v", p)
		}
	}
	if m := srcs[1]; m.Error == nil || len(m.Plugins) != 0 || m.Signature != SignatureInvalid {
		t.Errorf("unsigned mirror = %+v", m)
	}
	if _, remote, err := s.OfficialRemote(ctx, "welcome"); remote || err != nil {
		t.Errorf("welcome falls back to the bundled copy: %v %v", remote, err)
	}
	var ue *UnavailableError
	if _, _, err := s.OfficialRemote(ctx, "xp"); !errors.As(err, &ue) || !errors.Is(err, registry.ErrBadSignature) {
		t.Errorf("xp needs the remote registry: %v", err)
	}
	if _, err := s.Detail(ctx, nil, "codi", "xp"); !errors.As(err, &ue) {
		t.Errorf("Detail(xp) = %v", err)
	}
	if err := s.Check(ctx, config.Registry{Name: "mirror", URL: officialURL}); !errors.Is(err, registry.ErrBadSignature) {
		t.Errorf("Check = %v", err)
	}
}

func TestOfficialRemoteDetail(t *testing.T) {
	withOfficial(t)
	sha := strings.Repeat("b", 40)
	var resolved []string
	s := &Service{Catalog: testCatalog(), CacheDir: t.TempDir(),
		Fetch: func(context.Context, string) (*registry.Index, error) { return officialIndex(true), nil },
		Resolve: func(_ context.Context, regs []config.Registry, _, source, _ string) (*registry.Resolution, error) {
			if len(regs) != 1 || regs[0].Name != "codi" || regs[0].URL != officialURL {
				t.Errorf("regs = %+v", regs)
			}
			resolved = append(resolved, source)
			return &registry.Resolution{Source: source, Dir: "../../examples/basic/plugins/welcome",
				Entry: registry.LockEntry{Version: "9.0.0", Repo: "https://git.example/welcome.git", Commit: sha, Integrity: "sha256-x"}}, nil
		}}
	d, err := s.Detail(context.Background(), nil, "codi", "welcome")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(resolved, ",") != "codi/welcome" || d.Resolution == nil || d.Commit != sha || d.Version != "9.0.0" ||
		d.Trust != registry.TrustOfficial || !d.Verified || d.Commands == nil {
		t.Errorf("detail = %+v", d)
	}
	// The bundled one still wins when older remotely: no resolution, no commit.
	d, err = s.Detail(context.Background(), nil, "codi", "onboarding")
	if err != nil || d.Resolution != nil || d.Commit != "" || d.Version != "0.1.0" {
		t.Errorf("bundled detail = %+v, %v", d, err)
	}
}

func TestRemoteWins(t *testing.T) {
	for _, c := range []struct {
		bundled, remote string
		has, want       bool
	}{
		{"0.1.0", "0.2.0", true, true}, {"0.1.0", "0.1.0", true, false}, {"0.1.0", "", true, false},
		{"0.1.0", "junk", true, false}, {"", "", false, true}, {"0.2.0", "0.1.9", true, false},
	} {
		if got := remoteWins(c.bundled, c.has, registry.IndexEntry{Version: c.remote}); got != c.want {
			t.Errorf("remoteWins(%+v) = %v", c, got)
		}
	}
}
