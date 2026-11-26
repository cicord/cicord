package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

// Latest versions are looked up once per TTL (tags only), like indexes, and only for
// installed registry plugins; CacheOnly never fetches.
func TestUpdatesCacheLatestVersions(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	lookups := map[string]int{}
	latest := "1.1.0"
	s := &Service{
		Now: func() time.Time { return now },
		Fetch: func(context.Context, string) (*registry.Index, error) {
			return &registry.Index{Plugins: map[string]registry.IndexEntry{
				"xp":    {Repo: "https://acme.example/xp.git"},
				"other": {Repo: "https://acme.example/other.git"},
			}}, nil
		},
		Latest: func(_ context.Context, _, _ string, ie registry.IndexEntry, _ bool) (string, error) {
			repoURL := ie.Repo
			mu.Lock()
			defer mu.Unlock()
			lookups[repoURL]++
			return latest, nil
		},
	}
	root := t.TempDir()
	if err := registry.WriteLock(root, &registry.Lock{Plugins: map[string]registry.LockEntry{
		"acme/xp": {Version: "1.0.0", Repo: "https://acme.example/xp.git", Commit: "c"},
	}}); err != nil {
		t.Fatal(err)
	}
	repo := repoWith(config.Registry{Name: "acme", URL: "https://acme.example/r.git"})
	repo.Root = root
	repo.Instances = []config.Instance{{Bot: "b", Name: "xp", Server: "s",
		Plugin: &config.LoadedPlugin{Source: "acme/xp", Manifest: &config.Plugin{Metadata: config.PluginMetadata{Name: "xp", Version: "1.0.0"}}}}}

	// Nothing cached yet: CacheOnly finds nothing and fetches nothing.
	if ups := s.Updates(context.Background(), repo, CacheOnly); len(ups) != 0 || len(lookups) != 0 {
		t.Fatalf("cache only = %v, lookups %v", ups, lookups)
	}
	srcs, err := s.Sources(context.Background(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	var xp Plugin
	for _, p := range srcs[1].Plugins {
		if p.Name == "xp" {
			xp = p
		} else if p.Update != nil {
			t.Errorf("a plugin that is not installed has an update: %+v", p)
		}
	}
	if xp.Update == nil || xp.Update.Current != "1.0.0" || xp.Update.Latest != "1.1.0" || xp.Update.Level != "minor" || xp.Update.Kind != UpdateRegistry {
		t.Fatalf("xp = %+v", xp)
	}
	_, _ = s.Sources(context.Background(), repo, false)
	if ups := s.Updates(context.Background(), repo, CacheOnly); ups["acme/xp"].Latest != "1.1.0" {
		t.Errorf("cache only = %v", ups)
	}
	if lookups["https://acme.example/xp.git"] != 1 || lookups["https://acme.example/other.git"] != 0 {
		t.Fatalf("lookups = %v", lookups)
	}
	// Refetched when forced or stale.
	latest = "2.0.0"
	_, _ = s.Sources(context.Background(), repo, true)
	if ups := s.Updates(context.Background(), repo, CacheOnly); ups["acme/xp"].Level != "major" {
		t.Errorf("after refresh = %v", ups)
	}
	now = now.Add(31 * time.Minute)
	_, _ = s.Sources(context.Background(), repo, false)
	if lookups["https://acme.example/xp.git"] != 3 {
		t.Errorf("lookups = %v", lookups)
	}
}
