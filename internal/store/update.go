package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

// Update kinds.
const (
	// UpdateRegistry: the cicord.lock version of a registry plugin is not the highest
	// release tag of its repository (any major version).
	UpdateRegistry = "registry"
	// UpdateBundled: a copy of an official plugin (plugins/<name>/) is older than the
	// version bundled in this binary.
	UpdateBundled = "bundled"
)

// UpdateInfo describes an available update of an installed plugin.
type UpdateInfo struct {
	Kind    string `json:"kind"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
	// Level is the highest part of the version that changes: major, minor or patch.
	Level string `json:"level"`
	// Modified (bundled copies): the copy was changed locally, or cannot be told apart from
	// a local change; updating overwrites it and must be forced.
	Modified bool `json:"modified,omitempty"`
}

// FetchMode says how much network an update check may use.
type FetchMode int

const (
	// CacheOnly never fetches: registry plugins are only checked when their index and
	// latest version are cached (the background refresh fills them).
	CacheOnly FetchMode = iota
	// FetchMissing fetches what is not cached or stale (TTL), like browsing the Store.
	FetchMissing
	// FetchAll refetches every latest version (refresh).
	FetchAll
)

type cachedLatest struct {
	version string
	err     error
	at      time.Time
}

// Updates returns the available updates of the installed Store plugins, by
// "<source>/<plugin>". Registry lookups only list the tags of plugin repositories (no
// clone) and are cached with the index TTL; failures are left out (never an error).
func (s *Service) Updates(ctx context.Context, repo *config.Repo, mode FetchMode) map[string]UpdateInfo {
	return s.updates(ctx, repo, mode)
}

func (s *Service) updates(ctx context.Context, repo *config.Repo, mode FetchMode) map[string]UpdateInfo {
	out := map[string]UpdateInfo{}
	if repo == nil {
		return out
	}
	// Copies of official plugins (plugins/<name>/) and registry sources (cicord.lock),
	// including "cicord/<name>" locked from the implicit official registry.
	copies, locked := map[string]bool{}, map[string]bool{}
	for _, i := range repo.Instances {
		if i.Plugin == nil || i.Plugin.Manifest == nil {
			continue
		}
		if !strings.HasPrefix(i.Plugin.Source, ".") {
			locked[i.Plugin.Source] = true
			continue
		}
		name := i.Plugin.Manifest.Metadata.Name
		if filepath.Clean(i.Plugin.Dir) == filepath.Join(repo.Root, filepath.FromSlash(catalog.PluginDir(name))) {
			copies[name] = true
		}
	}
	for name := range copies {
		if u, ok := s.BundledUpdate(repo, name); ok {
			out[config.OfficialRegistryName+"/"+name] = u
		}
	}

	regs := registry.WithOfficial(registries(repo))
	if len(regs) == 0 || len(locked) == 0 {
		return out
	}
	lock, err := registry.ReadLock(repo.Root)
	if err != nil {
		return out
	}
	type job struct {
		key, repoURL, prefix, current string
		reg                           config.Registry
	}
	var jobs []job
	for _, r := range regs {
		var c cached
		if mode == CacheOnly {
			s.mu.Lock()
			c = s.cache[config.NormalizeRegistryURL(r.URL)]
			s.mu.Unlock()
		} else {
			c = s.index(ctx, r, false)
		}
		if c.err != nil || c.idx == nil {
			continue
		}
		for key := range locked {
			src, name, _ := strings.Cut(key, "/")
			if src != r.Name {
				continue
			}
			le, ok := lock.Plugins[key]
			ie, ok2 := c.idx.Plugins[name]
			if !ok || !ok2 {
				continue
			}
			jobs = append(jobs, job{key: key, repoURL: ie.Repo, prefix: ie.TagPrefix(name), current: le.Version, reg: r})
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			latest := s.latest(ctx, j.reg, j.repoURL, j.prefix, mode)
			if level := registry.UpdateLevel(j.current, latest); level != "" {
				mu.Lock()
				out[j.key] = UpdateInfo{Kind: UpdateRegistry, Current: j.current, Latest: latest, Level: level}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// latestKey is the cache key of the latest version of a plugin: its repository and tag
// prefix (the plugins of a monorepo share the repository, not the tags).
func latestKey(repoURL, tagPrefix string) string {
	return config.NormalizeRegistryURL(repoURL) + "#" + tagPrefix
}

// latest returns the highest release of a plugin repository ("" when unknown).
func (s *Service) latest(ctx context.Context, reg config.Registry, repoURL, tagPrefix string, mode FetchMode) string {
	key := latestKey(repoURL, tagPrefix)
	s.mu.Lock()
	c, ok := s.tags[key]
	s.mu.Unlock()
	switch {
	case mode == CacheOnly:
		return c.version
	case ok && mode == FetchMissing && s.now().Sub(c.at) < s.ttl():
		return c.version
	}
	f := s.Latest
	if f == nil {
		f = registry.LatestVersion
	}
	t := s.FetchTimeout
	if t == 0 {
		t = 20 * time.Second
	}
	fctx, cancel := context.WithTimeout(s.withToken(s.guard(ctx), reg), t)
	defer cancel()
	v, err := f(fctx, reg.URL, repoURL, tagPrefix)
	c = cachedLatest{version: v, err: err, at: s.now()}
	s.mu.Lock()
	if s.tags == nil {
		s.tags = map[string]cachedLatest{}
	}
	s.tags[key] = c
	s.mu.Unlock()
	return v
}

// BundledUpdate reports whether the copy of official plugin name in the repository
// (plugins/<name>/) is older than the version bundled in this binary.
func (s *Service) BundledUpdate(repo *config.Repo, name string) (UpdateInfo, bool) {
	if s.Catalog == nil || repo == nil {
		return UpdateInfo{}, false
	}
	e, ok := s.Catalog.Get(name)
	if !ok {
		return UpdateInfo{}, false
	}
	st, err := s.Catalog.CheckCopy(name, filepath.Join(repo.Root, filepath.FromSlash(catalog.PluginDir(name))))
	if err != nil {
		return UpdateInfo{}, false
	}
	level := registry.UpdateLevel(st.Version, e.Version)
	if level == "" {
		return UpdateInfo{}, false
	}
	return UpdateInfo{Kind: UpdateBundled, Current: st.Version, Latest: e.Version, Level: level, Modified: st.Modified}, true
}
