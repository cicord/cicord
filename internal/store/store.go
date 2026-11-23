// Package store is the plugin Store (ADR-0011, apt/Homebrew-style sources): it lists the
// plugins of every source — the official plugins bundled in the binary (source "codi")
// and the registries of codi.yaml — and describes a plugin before it is installed.
//
// Registry indexes are cached in memory (refreshed after TTL or on demand). Trust is
// computed (registry.Trust), never declared. What a plugin requests (capabilities,
// intents, network, config schema) is always read from its plugin.yaml at the resolved
// version, not from the index. Listing slash commands evaluates the plugin's code, so it
// is only done for official sources (Detail.Commands is nil otherwise).
package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/npjonath/codi/internal/catalog"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/registry"
)

// Source kinds.
const (
	KindBundled = "bundled"
	KindGit     = "git"
)

// SourceError explains why a registry could not be read.
type SourceError struct {
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details string `json:"details,omitempty"`
}

// Source is one plugin source.
type Source struct {
	Name      string       `json:"name"`
	Kind      string       `json:"kind"`
	URL       string       `json:"url,omitempty"`
	Trust     string       `json:"trust"`
	FetchedAt *time.Time   `json:"fetchedAt,omitempty"`
	Error     *SourceError `json:"error,omitempty"`
	// Private: the registry has a secretRef; Credentials is "ok" when that secret is
	// available to the runtime, "missing" otherwise. The token itself is never returned.
	Private     bool   `json:"private"`
	Credentials string `json:"credentials,omitempty"`
	// Signature is "verified" when the index is signed by an official key, "invalid" for
	// an official URL whose index is not (the source then has an error and no remote
	// plugins). Omitted for unofficial registries and the bundled-only "codi" source.
	Signature string   `json:"signature,omitempty"`
	Plugins   []Plugin `json:"plugins"`
}

// Installation is an instance using a Store plugin.
type Installation struct {
	Server   string `json:"server"`
	Bot      string `json:"bot"`
	Instance string `json:"instance"`
}

// Plugin is a plugin as listed in the Store.
type Plugin struct {
	Source      string         `json:"source"`
	Name        string         `json:"name"`
	DisplayName string         `json:"displayName"`
	Description string         `json:"description"`
	Keywords    []string       `json:"keywords"`
	Version     string         `json:"version,omitempty"`
	Publisher   string         `json:"publisher,omitempty"`
	Verified    bool           `json:"verified"`
	Repo        string         `json:"repo,omitempty"`
	Trust       string         `json:"trust"`
	Installed   []Installation `json:"installed"`
}

// Detail is a plugin with what it requests, at its latest version.
type Detail struct {
	Plugin
	Commit            string            `json:"commit,omitempty"`
	Integrity         string            `json:"integrity,omitempty"`
	Capabilities      []string          `json:"capabilities"`
	Intents           []string          `json:"intents"`
	PrivilegedIntents []string          `json:"privilegedIntents"`
	Network           []string          `json:"network"`
	Schema            map[string]any    `json:"schema"`
	Commands          []catalog.Command `json:"commands"`

	// Resolution is the resolved registry version (nil for bundled plugins).
	Resolution *registry.Resolution `json:"-"`
}

// Errors of Detail.
var (
	ErrUnknownSource = errors.New("unknown source")
	ErrUnknownPlugin = errors.New("unknown plugin")
)

// UnavailableError wraps a registry that could not be fetched or resolved.
type UnavailableError struct {
	Source string
	Err    error
}

func (e *UnavailableError) Error() string {
	return registry.Redact(fmt.Sprintf("registry %q: %v", e.Source, e.Err))
}
func (e *UnavailableError) Unwrap() error { return e.Err }

// Service is safe for concurrent use.
type Service struct {
	Catalog  *catalog.Catalog
	CacheDir string
	// TTL of a fetched index (default 30 min); Run refreshes them at this interval.
	TTL time.Duration
	// FirstRefresh delays Run's first refresh (default 10 s).
	FirstRefresh time.Duration
	// FetchTimeout bounds one index fetch (default 20 s).
	FetchTimeout time.Duration
	// Fetch reads a registry index (default registry.FetchIndex).
	Fetch func(ctx context.Context, url string) (*registry.Index, error)
	// Resolve resolves "<registry>/<plugin>" (default registry.ResolveLatest).
	Resolve func(ctx context.Context, regs []config.Registry, cacheDir, source, rng string) (*registry.Resolution, error)
	Now     func() time.Time
	// Secret resolves the secretRef of private registries (nil: anonymous fetches).
	Secret func(name string) (string, bool)
	// PublicOnly makes https fetches refuse non-public addresses (registry.WithPublicOnly):
	// set when the Store serves admin UI users, who must not reach the runtime's network.
	PublicOnly bool

	mu    sync.Mutex
	cache map[string]cached // by normalized URL
}

type cached struct {
	idx *registry.Index
	err error
	at  time.Time
}

func (s *Service) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return 30 * time.Minute
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) guard(ctx context.Context) context.Context {
	if s.PublicOnly {
		return registry.WithPublicOnly(ctx)
	}
	return ctx
}

func (s *Service) fetch(ctx context.Context, url string) (*registry.Index, error) {
	f := s.Fetch
	if f == nil {
		f = registry.FetchIndex
	}
	t := s.FetchTimeout
	if t == 0 {
		t = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(s.guard(ctx), t)
	defer cancel()
	return f(ctx, url)
}

// withToken attaches the registry's token, when it has one.
func (s *Service) withToken(ctx context.Context, r config.Registry) context.Context {
	ref := r.TokenRef()
	if ref == "" || s.Secret == nil {
		return ctx // only this registry's own token, never another secret
	}
	if tok, ok := s.Secret(ref); ok {
		return registry.WithToken(ctx, r.URL, tok)
	}
	return ctx
}

// index returns the cached index of a registry, fetching it when stale or forced.
func (s *Service) index(ctx context.Context, r config.Registry, force bool) cached {
	url := r.URL
	ctx = s.withToken(ctx, r)
	key := config.NormalizeRegistryURL(url)
	s.mu.Lock()
	c, ok := s.cache[key]
	s.mu.Unlock()
	if ok && !force && s.now().Sub(c.at) < s.ttl() {
		return c
	}
	idx, err := s.fetch(ctx, url)
	idx, err = checkSigned(url, idx, err)
	c = cached{idx: idx, err: err, at: s.now()}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[key] = c
	s.mu.Unlock()
	return c
}

// Run refreshes the registry indexes of the active configuration in the background: a
// first time shortly after start, then every TTL, so that browsing the Store rarely
// waits for git. It returns when stop is closed.
func (s *Service) Run(stop <-chan struct{}, repo func() *config.Repo) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()
	first := s.FirstRefresh
	if first == 0 {
		first = 10 * time.Second
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r := repo()
		if _, official := registry.Official(); official || len(registries(r)) > 0 {
			_, _ = s.Sources(ctx, r, true)
		}
		t.Reset(s.ttl())
	}
}

// Check fetches a registry index now (and caches it): nil when it is a valid registry.
func (s *Service) Check(ctx context.Context, r config.Registry) error {
	return s.index(ctx, r, true).err
}

// CheckWithToken is Check with a token that is not stored yet ("" = the registry's own).
func (s *Service) CheckWithToken(ctx context.Context, r config.Registry, token string) error {
	if token != "" {
		ctx = registry.WithToken(ctx, r.URL, token)
		r.SecretRef = "" // the given token wins over a stored one
	}
	return s.index(ctx, r, true).err
}

// Sources lists every source: "codi" first, then the registries in codi.yaml order.
// force refetches every registry index. repo (may be nil) fills Plugin.Installed.
func (s *Service) Sources(ctx context.Context, repo *config.Repo, force bool) ([]Source, error) {
	inst := installations(repo, s.Catalog)
	out := []Source{}
	official := Source{Name: config.OfficialRegistryName, Kind: KindBundled, Trust: registry.TrustOfficial, Plugins: []Plugin{}}
	if s.Catalog != nil {
		es, err := s.Catalog.List()
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			official.Plugins = append(official.Plugins, Plugin{Source: official.Name, Name: e.Name,
				DisplayName: e.DisplayName, Description: e.Description, Keywords: nonNil(e.Keywords),
				Version: e.Version, Publisher: "CODI", Verified: true, Trust: registry.TrustOfficial,
				Installed: nonNilInst(inst[official.Name+"/"+e.Name])})
		}
	}
	out = append(out, official)

	regs := registries(repo)
	off, hasOff := registry.Official()
	fetch := regs
	if hasOff {
		fetch = append(slices.Clone(regs), off)
	}
	results := make([]cached, len(fetch))
	var wg sync.WaitGroup
	for i, r := range fetch {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.index(ctx, r, force)
		}()
	}
	wg.Wait()
	if hasOff {
		mergeOfficial(&out[0], off, results[len(regs)], inst)
	}
	for i, r := range regs {
		c := results[i]
		src := Source{Name: r.Name, Kind: KindGit, URL: registry.Redact(r.URL), Trust: registry.Trust(r.URL), Plugins: []Plugin{}}
		at := c.at
		src.FetchedAt = &at
		src.Signature = signatureOf(r.URL, c)
		if r.SecretRef != "" {
			src.Private, src.Credentials = true, "missing"
			if ref := r.TokenRef(); s.Secret != nil && ref != "" {
				if _, ok := s.Secret(ref); ok {
					src.Credentials = "ok"
				}
			}
		}
		if c.err != nil {
			src.Error = ExplainFetch(c.err)
			out = append(out, src)
			continue
		}
		names := make([]string, 0, len(c.idx.Plugins))
		for n := range c.idx.Plugins {
			if registry.NameRe.MatchString(n) { // other keys could not be installed (cache paths)
				names = append(names, n)
			}
		}
		slices.Sort(names)
		for _, n := range names {
			e := c.idx.Plugins[n]
			src.Plugins = append(src.Plugins, fromIndex(r, n, e, inst[r.Name+"/"+n], c.idx.SignedBy != ""))
		}
		out = append(out, src)
	}
	return out, nil
}

func fromIndex(r config.Registry, name string, e registry.IndexEntry, inst []Installation, signed bool) Plugin {
	p := Plugin{Source: r.Name, Name: name, DisplayName: e.DisplayName, Description: e.Description,
		Keywords: nonNil(e.Keywords), Publisher: e.Publisher, Repo: registry.Redact(e.Repo), Trust: registry.Trust(r.URL),
		Installed: nonNilInst(inst)}
	// "verified" only means something in the official registry, from its signed index.
	p.Verified = e.Verified && p.Trust == registry.TrustOfficial && signed
	if p.DisplayName == "" {
		p.DisplayName = name
	}
	return p
}

// Detail describes a plugin at its latest version. For registries, the plugin is
// downloaded to the cache and Resolution holds what an install must lock.
func (s *Service) Detail(ctx context.Context, repo *config.Repo, source, name string) (Detail, error) {
	inst := installations(repo, s.Catalog)
	var reg *config.Registry
	if source == config.OfficialRegistryName {
		off, remote, err := s.OfficialRemote(ctx, name)
		if err != nil {
			return Detail{}, err
		}
		if remote {
			reg = &off
		}
	}
	if source == config.OfficialRegistryName && reg == nil {
		if s.Catalog == nil {
			return Detail{}, ErrUnknownPlugin
		}
		e, ok := s.Catalog.Get(name)
		if !ok {
			return Detail{}, ErrUnknownPlugin
		}
		d := Detail{Plugin: Plugin{Source: source, Name: e.Name, DisplayName: e.DisplayName, Description: e.Description,
			Keywords: nonNil(e.Keywords), Version: e.Version, Publisher: "CODI", Verified: true,
			Trust: registry.TrustOfficial, Installed: nonNilInst(inst[source+"/"+name])}}
		fill(&d, e)
		return d, nil
	}
	if !registry.NameRe.MatchString(source) || !registry.NameRe.MatchString(name) {
		return Detail{}, ErrUnknownPlugin
	}
	if reg == nil {
		for _, r := range registries(repo) {
			if r.Name == source {
				reg = &r
				break
			}
		}
	}
	if reg == nil {
		return Detail{}, ErrUnknownSource
	}
	c := s.index(ctx, *reg, false)
	if c.err != nil {
		return Detail{}, &UnavailableError{Source: source, Err: c.err}
	}
	ie, ok := c.idx.Plugins[name]
	if !ok {
		return Detail{}, ErrUnknownPlugin
	}
	resolve := s.Resolve
	if resolve == nil {
		resolve = registry.ResolveLatest
	}
	rctx, cancel := context.WithTimeout(s.withToken(s.guard(ctx), *reg), 2*time.Minute)
	defer cancel()
	res, err := resolve(rctx, []config.Registry{*reg}, s.CacheDir, source+"/"+name, "*")
	if err != nil {
		return Detail{}, &UnavailableError{Source: source, Err: err}
	}
	trust := registry.Trust(reg.URL)
	e, err := catalog.Describe(res.Dir, trust == registry.TrustOfficial)
	if err != nil {
		return Detail{}, &UnavailableError{Source: source, Err: fmt.Errorf("%s@%s: %w", name, res.Entry.Version, err)}
	}
	if e.Name != name {
		return Detail{}, &UnavailableError{Source: source, Err: fmt.Errorf("the repository of %q contains plugin %q", name, e.Name)}
	}
	d := Detail{Plugin: fromIndex(*reg, name, ie, inst[source+"/"+name], c.idx.SignedBy != ""), Commit: res.Entry.Commit,
		Integrity: res.Entry.Integrity, Resolution: res}
	d.Version = res.Entry.Version
	if d.DisplayName == name && e.DisplayName != "" {
		d.DisplayName = e.DisplayName
	}
	if d.Description == "" {
		d.Description = e.Description
	}
	fill(&d, e)
	return d, nil
}

func fill(d *Detail, e catalog.Entry) {
	d.Capabilities, d.Intents, d.PrivilegedIntents = nonNil(e.Capabilities), nonNil(e.Intents), nonNil(e.PrivilegedIntents)
	d.Network, d.Schema, d.Commands = nonNil(e.Network), e.Schema, e.Commands
	if d.Schema == nil {
		d.Schema = map[string]any{}
	}
}

func registries(repo *config.Repo) []config.Registry {
	if repo == nil || repo.Project == nil {
		return nil
	}
	return repo.Project.Spec.Registries
}

// installations maps "<source>/<plugin>" to the instances using it. A local plugin is
// counted as the bundled one when it lives in plugins/<name> and the catalog has <name>.
func installations(repo *config.Repo, cat *catalog.Catalog) map[string][]Installation {
	out := map[string][]Installation{}
	if repo == nil {
		return out
	}
	for _, i := range repo.Instances {
		if i.Plugin == nil || i.Plugin.Manifest == nil {
			continue
		}
		var key string
		name := i.Plugin.Manifest.Metadata.Name
		if strings.HasPrefix(i.Plugin.Source, ".") {
			if cat == nil {
				continue
			}
			if _, ok := cat.Get(name); !ok {
				continue
			}
			if filepath.Clean(i.Plugin.Dir) != filepath.Join(repo.Root, filepath.FromSlash(catalog.PluginDir(name))) {
				continue
			}
			key = config.OfficialRegistryName + "/" + name
		} else {
			key = i.Plugin.Source
		}
		out[key] = append(out[key], Installation{Server: i.Server, Bot: i.Bot, Instance: i.Name})
	}
	return out
}

// ExplainFetch turns a fetch error into a message for people.
func ExplainFetch(err error) *SourceError {
	msg := registry.Redact(err.Error())
	e := &SourceError{Message: "This source could not be read.", Details: msg}
	switch {
	case errors.Is(err, registry.ErrBadSignature):
		e.Message = "The official registry index has no valid, current signature (missing, invalid, expired or older than one already seen): its plugins are not listed."
		e.Hint = "This is never ignored. Try again later; if it persists, report it to the CODI maintainers."
	case errors.Is(err, context.DeadlineExceeded):
		e.Message, e.Hint = "This source did not answer in time.", "Try again later, or check the URL."
	case strings.Contains(msg, "no index.yaml"):
		e.Message, e.Hint = "This repository is not a plugin registry (no index.yaml at its root).", "Check the URL."
	case strings.Contains(msg, "index.yaml:"):
		e.Message, e.Hint = "The registry index is invalid.", "Ask the registry maintainers to fix index.yaml."
	case strings.Contains(msg, "authentication required"), strings.Contains(msg, "repository not found"):
		e.Message, e.Hint = "The repository was not found or is private.", "Check the URL; private registries are not supported yet."
	default:
		e.Hint = "Check the URL and the network access of the runtime, then refresh."
	}
	return e
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

func nonNilInst(s []Installation) []Installation {
	if s == nil {
		return []Installation{}
	}
	return s
}
