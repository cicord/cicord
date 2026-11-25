package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

// TestMain keeps the tests off the network: none may fetch the real official registry
// (tests that need an official registry set registry.OfficialURLs themselves).
func TestMain(m *testing.M) {
	registry.OfficialURLs = nil
	os.Exit(m.Run())
}

const officialURL = "https://registry.cicord.example/official.git"

// withOfficial makes officialURL the official registry for the test (not parallel), with
// an official key (the fake Fetch functions decide what is signed).
func withOfficial(t *testing.T) {
	t.Helper()
	t.Setenv(registry.DevOfficialRegistryEnv, "")
	oldURLs, oldKeys := registry.OfficialURLs, registry.OfficialKeys
	t.Cleanup(func() { registry.OfficialURLs, registry.OfficialKeys = oldURLs, oldKeys })
	pub, _, err := registry.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	registry.OfficialURLs, registry.OfficialKeys = []string{officialURL}, []string{registry.EncodePublicKey(pub)}
}

// A build without an official key never fetches the official index: source "cicord" lists
// the bundled plugins with an explanation, no "invalid signature" badge.
func TestOfficialWithoutKey(t *testing.T) {
	withOfficial(t)
	registry.OfficialKeys = nil
	if registry.HasOfficialKeys() {
		t.Skip("this test binary embeds a development official key (-ldflags -X …devOfficialKey)")
	}
	s := &Service{Catalog: testCatalog(), Fetch: func(context.Context, string) (*registry.Index, error) {
		t.Error("the official index is fetched although nothing could verify it")
		return nil, errors.New("unexpected fetch")
	}}
	srcs, err := s.Sources(context.Background(), nil, true)
	if err != nil || len(srcs) != 1 {
		t.Fatalf("sources = %+v, %v", srcs, err)
	}
	c := srcs[0]
	if c.URL != officialURL || c.Signature != "" || c.Error == nil || !strings.Contains(c.Error.Message, "no official registry key") || len(c.Plugins) != 2 {
		t.Errorf("cicord = %+v (error %+v)", c, c.Error)
	}
	// Bundled plugins still install; a plugin only the remote registry has is unavailable.
	if _, remote, err := s.OfficialRemote(context.Background(), "welcome"); remote || err != nil {
		t.Errorf("OfficialRemote(welcome) = %v, %v", remote, err)
	}
	var unavailable *UnavailableError
	if _, _, err := s.OfficialRemote(context.Background(), "xp"); !errors.As(err, &unavailable) || !errors.Is(err, registry.ErrNoOfficialKey) {
		t.Errorf("OfficialRemote(xp) = %v", err)
	}
	if d, err := s.Detail(context.Background(), nil, config.OfficialRegistryName, "welcome"); err != nil || d.Resolution != nil {
		t.Errorf("bundled detail = %+v, %v", d, err)
	}
}

func testCatalog() *catalog.Catalog {
	return catalog.New(os.DirFS("../../examples"), []string{"basic/plugins/welcome", "basic/plugins/onboarding"})
}

func officialIndex(signed bool) *registry.Index {
	idx := &registry.Index{Plugins: map[string]registry.IndexEntry{
		"welcome":    {Repo: "https://git.example/welcome.git", Version: "9.0.0", Publisher: "Cicord", Verified: true},
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
	// Without an official URL, "cicord" is the bundled catalog only (today's behaviour).
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
	cicord := srcs[0]
	if cicord.Name != "cicord" || cicord.Kind != KindBundled || cicord.URL != officialURL || cicord.Signature != SignatureVerified || cicord.Error != nil {
		t.Fatalf("cicord = %+v", cicord)
	}
	got := map[string]Plugin{}
	var names []string
	for _, p := range cicord.Plugins {
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
	if p := got["xp"]; !p.Verified || p.Source != "cicord" || p.Publisher != "acme" {
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
	cicord = srcs[0]
	if cicord.Error == nil || cicord.Signature != SignatureInvalid || len(cicord.Plugins) != 2 || !strings.Contains(cicord.Error.Message, "signature") {
		t.Errorf("unsigned cicord = %+v", cicord)
	}
	bundled := map[string]string{"welcome": "0.1.1", "onboarding": "0.1.0"}
	for _, p := range cicord.Plugins {
		if p.Repo != "" || p.Version != bundled[p.Name] {
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
	if _, err := s.Detail(ctx, nil, "cicord", "xp"); !errors.As(err, &ue) {
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
			if len(regs) != 1 || regs[0].Name != "cicord" || regs[0].URL != officialURL {
				t.Errorf("regs = %+v", regs)
			}
			resolved = append(resolved, source)
			return &registry.Resolution{Source: source, Dir: "../../examples/basic/plugins/welcome",
				Entry: registry.LockEntry{Version: "9.0.0", Repo: "https://git.example/welcome.git", Commit: sha, Integrity: "sha256-x"}}, nil
		}}
	d, err := s.Detail(context.Background(), nil, "cicord", "welcome")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(resolved, ",") != "cicord/welcome" || d.Resolution == nil || d.Commit != sha || d.Version != "9.0.0" ||
		d.Trust != registry.TrustOfficial || !d.Verified || d.Commands == nil {
		t.Errorf("detail = %+v", d)
	}
	// The bundled one still wins when older remotely: no resolution, no commit.
	d, err = s.Detail(context.Background(), nil, "cicord", "onboarding")
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
		{"0.2.0-rc.1", "0.2.0", true, true}, {"0.2.0", "0.2.0-rc.2", true, false},
		{"0.2.0-rc.2", "0.2.0-rc.10", true, true}, {"0.2.0", "0.2.0+build.1", true, false},
	} {
		if got := remoteWins(c.bundled, c.has, registry.IndexEntry{Version: c.remote}); got != c.want {
			t.Errorf("remoteWins(%+v) = %v", c, got)
		}
	}
}

// An official plugin locked from the official registry (cicord.lock "cicord/<name>", like
// cicord/tickets on the community repository) whose index entry is not newer than the
// bundled copy: its Store page is the registry's latest version, so the page shows the
// update the table shows (the table reads the release tags, the index may lag behind).
// A page that resolves a newer version than the table's cached one tells the console.
func TestLockedOfficialDetailMatchesTable(t *testing.T) {
	withOfficial(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := registry.WriteLock(root, &registry.Lock{Plugins: map[string]registry.LockEntry{
		"cicord/onboarding": {Version: "0.1.0", Repo: "https://git.example/onboarding.git", Commit: "c1"},
	}}); err != nil {
		t.Fatal(err)
	}
	repo := &config.Repo{Root: root, Project: &config.Project{}}
	repo.Instances = []config.Instance{{Bot: "b", Name: "onboarding", Server: "s",
		Plugin: &config.LoadedPlugin{Source: "cicord/onboarding", Manifest: &config.Plugin{Metadata: config.PluginMetadata{Name: "onboarding", Version: "0.1.0"}}}}}
	latest, resolved := "0.2.0", "0.2.0"
	changes := 0
	s := &Service{Catalog: testCatalog(),
		Fetch: func(context.Context, string) (*registry.Index, error) {
			idx := officialIndex(true) // onboarding 0.0.1: the bundled 0.1.0 wins in the listing
			return idx, nil
		},
		Latest: func(context.Context, string, string, string) (string, error) { return latest, nil },
		Resolve: func(context.Context, []config.Registry, string, string, string) (*registry.Resolution, error) {
			return &registry.Resolution{Entry: registry.LockEntry{Version: resolved, Commit: "c2", Integrity: "sha256-x"},
				Dir: "../../examples/basic/plugins/onboarding"}, nil
		},
		Changed: func() { changes++ },
	}
	srcs, err := s.Sources(ctx, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	var row Plugin
	for _, p := range srcs[0].Plugins {
		if p.Name == "onboarding" {
			row = p
		}
	}
	if row.Update == nil || row.Update.Current != "0.1.0" || row.Update.Latest != "0.2.0" {
		t.Fatalf("table row = %+v", row)
	}
	d, err := s.Detail(ctx, repo, config.OfficialRegistryName, "onboarding")
	if err != nil {
		t.Fatal(err)
	}
	if d.Resolution == nil || d.Version != "0.2.0" || d.Update == nil || *d.Update != *row.Update || len(d.Installed) != 1 {
		t.Fatalf("detail = %+v (update %+v), table %+v", d, d.Update, row.Update)
	}
	if changes != 0 {
		t.Errorf("Changed called %d times although the table was up to date", changes)
	}
	// Installs of that plugin come from the registry too (what the page reviewed).
	if _, remote, err := s.OfficialOrigin(ctx, repo, "onboarding"); !remote || err != nil {
		t.Errorf("OfficialOrigin = %v, %v", remote, err)
	}
	if _, remote, _ := s.OfficialOrigin(ctx, nil, "onboarding"); remote {
		t.Error("not locked: the bundled copy wins")
	}

	// A newer release than the table's cached latest: the console is told to refetch.
	resolved = "0.3.0"
	if d, err = s.Detail(ctx, repo, config.OfficialRegistryName, "onboarding"); err != nil || d.Update == nil || d.Update.Latest != "0.3.0" {
		t.Fatalf("detail = %+v, %v", d.Update, err)
	}
	if changes != 1 {
		t.Errorf("Changed called %d times, want 1", changes)
	}
}
