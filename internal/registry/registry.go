// Package registry resolves registry plugins (ADR-0011): a registry is a git repository
// with an index.yaml mapping plugin names to git repositories; plugin versions are semver
// tags; codi.lock pins the resolved commit and a content hash.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"gopkg.in/yaml.v3"

	"github.com/npjonath/codi/internal/config"
)

const LockFile = "codi.lock"

// Index is the content of a registry's index.yaml.
type Index struct {
	Plugins map[string]IndexEntry `yaml:"plugins"`
	// SignedBy is the ID of the official key whose signature verified (FetchIndex sets it
	// for official URLs only; "" otherwise). Never read from the file.
	SignedBy string `yaml:"-"`
}

type IndexEntry struct {
	Repo        string   `yaml:"repo"`
	DisplayName string   `yaml:"displayName,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Keywords    []string `yaml:"keywords,omitempty"`
	Publisher   string   `yaml:"publisher,omitempty"`
	Verified    bool     `yaml:"verified,omitempty"`
	// Version is the latest published version (written by `codi plugin publish`), and
	// Commit the commit of its tag: resolving that version fails if the tag moved.
	Version string `yaml:"version,omitempty"`
	Commit  string `yaml:"commit,omitempty"`
}

// NameRe is the shape of registry and plugin names. Both become cache directories, so
// anything else (dots, slashes) is refused.
var NameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// CheckSource validates "<registry>/<plugin>".
func CheckSource(source string) (registryName, plugin string, err error) {
	reg, name, ok := strings.Cut(source, "/")
	if !ok || !NameRe.MatchString(reg) || !NameRe.MatchString(name) {
		return "", "", fmt.Errorf("invalid plugin reference %q: use <registry>/<plugin> with lowercase letters, digits and dashes", source)
	}
	return reg, name, nil
}

// userinfoRe matches the credentials of a URL ("https://user:token@host").
// The userinfo runs to the last "@" of the authority (a password may contain "@").
var userinfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/?#\s]*@`)

// Redact removes URL credentials from s (a URL or an error message).
func Redact(s string) string { return userinfoRe.ReplaceAllString(s, "${1}***@") }

// HasUserinfo reports whether a URL embeds credentials.
func HasUserinfo(raw string) bool { return userinfoRe.MatchString(raw) }

// Trust levels of a plugin source (ADR-0011).
const (
	TrustOfficial   = "official"
	TrustUnofficial = "unofficial"
)

// OfficialURLs are the URLs of the official CODI registry, the first being its address
// (Official) and the others aliases. None exists yet: the official plugins are bundled in
// the binary (internal/catalog). An index read from one of these URLs must be signed by
// an official key (sign.go). Tests set it; DevOfficialRegistryEnv overrides it in
// development.
//
// TODO(owner): set it once the official registry repository exists.
var OfficialURLs = []string{}

// Trust is computed from the registry URL, never declared in codi.yaml. "official" here
// means "an official URL": its index is only used when its signature verifies (FetchIndex).
func Trust(url string) string {
	u := config.NormalizeRegistryURL(url)
	for _, o := range officialURLs() {
		if config.NormalizeRegistryURL(o) == u {
			return TrustOfficial
		}
	}
	return TrustUnofficial
}

// IsRemoteURL reports whether a git URL is a network https URL (what the admin UI may
// add). file://, local paths, ssh and http are operator-only (CLI).
func IsRemoteURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "https://")
}

func hostOf(raw string) string {
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// CheckRepoURL refuses plugin repositories that a registry may not point to: an https
// registry only lists https plugin repositories (no file:// or local paths on the host).
func CheckRepoURL(registryURL, repoURL string) error {
	if IsRemoteURL(registryURL) && !IsRemoteURL(repoURL) {
		return fmt.Errorf("plugin repository %q must be an https:// URL like its registry", repoURL)
	}
	return nil
}

// Lock is codi.lock (spec/README.md §7).
type Lock struct {
	LockVersion int                  `yaml:"lockVersion"`
	Plugins     map[string]LockEntry `yaml:"plugins"`
}

type LockEntry struct {
	Version      string   `yaml:"version"`
	Repo         string   `yaml:"repo"`
	Commit       string   `yaml:"commit"`
	Integrity    string   `yaml:"integrity"`
	Capabilities []string `yaml:"capabilities,omitempty"`
}

func ReadLock(repoRoot string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, LockFile))
	if errors.Is(err, fs.ErrNotExist) {
		data = nil
	} else if err != nil {
		return nil, err
	}
	return ParseLock(data)
}

// ParseLock decodes codi.lock (nil or empty: an empty lock).
func ParseLock(data []byte) (*Lock, error) {
	l := Lock{LockVersion: 1}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &l); err != nil {
			return nil, fmt.Errorf("%s: %w", LockFile, err)
		}
	}
	if l.Plugins == nil {
		l.Plugins = map[string]LockEntry{}
	}
	return &l, nil
}

// MarshalLock encodes codi.lock with its header.
func MarshalLock(l *Lock) ([]byte, error) {
	l.LockVersion = 1
	data, err := yaml.Marshal(l)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by codi — do not edit by hand.\n"), data...), nil
}

func WriteLock(repoRoot string, l *Lock) error {
	data, err := MarshalLock(l)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repoRoot, LockFile), data, 0o644)
}

// SetLockEntry returns codi.lock content (cur may be nil) with source pinned to e.
func SetLockEntry(cur []byte, source string, e LockEntry) ([]byte, error) {
	l, err := ParseLock(cur)
	if err != nil {
		return nil, err
	}
	l.Plugins[source] = e
	return MarshalLock(l)
}

// Resolver implements config.PluginResolver from codi.lock + a local cache.
type Resolver struct {
	RepoRoot string
	CacheDir string
	// PublicOnly downloads locked plugins only from public addresses (see WithPublicOnly).
	PublicOnly bool
	// Secret resolves the secretRef of private registries (nil: anonymous downloads).
	Secret func(name string) (string, bool)
	mu     sync.Mutex
	lock   *Lock
}

var _ config.PluginResolver = (*Resolver)(nil)

func NewResolver(repoRoot, cacheDir string) *Resolver {
	return &Resolver{RepoRoot: repoRoot, CacheDir: cacheDir}
}

// NewRuntimeResolver is the resolver of a running runtime: downloads of locked plugins
// only reach public addresses unless CODI_ALLOW_PRIVATE_REGISTRIES=true;
// secret resolves the secretRef of private registries.
func NewRuntimeResolver(repoRoot, cacheDir string, secret func(string) (string, bool)) *Resolver {
	r := NewResolver(repoRoot, cacheDir)
	r.PublicOnly = !AllowPrivateRegistries()
	r.Secret = secret
	return r
}

// DefaultCacheDir is ~/.cache/codi/plugins (or $CODI_CACHE_DIR/plugins).
func DefaultCacheDir() string {
	if d := os.Getenv("CODI_CACHE_DIR"); d != "" {
		return filepath.Join(d, "plugins")
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "codi", "plugins")
	}
	return filepath.Join(os.TempDir(), "codi-plugins")
}

func (r *Resolver) Resolve(source, versionRange string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lock == nil {
		l, err := ReadLock(r.RepoRoot)
		if err != nil {
			return "", "", err
		}
		r.lock = l
	}
	e, ok := r.lock.Plugins[source]
	if !ok {
		return "", "", fmt.Errorf("registry plugin %q is not in %s (run `codi plugin add %s`)", source, LockFile, source)
	}
	c, err := semver.NewConstraint(versionRange)
	if err != nil {
		return "", "", fmt.Errorf("invalid version range %q: %w", versionRange, err)
	}
	v, err := semver.NewVersion(e.Version)
	if err != nil || !c.Check(v) {
		return "", "", fmt.Errorf("locked version %s of %q does not satisfy %q (run `codi plugin update`)", e.Version, source, versionRange)
	}
	ctx := WithRegistryTokens(context.Background(), projectRegistries(r.RepoRoot), r.Secret)
	if r.PublicOnly {
		ctx = WithPublicOnly(ctx)
	}
	dir, err := Materialize(ctx, r.CacheDir, source, e)
	if err != nil {
		return "", "", err
	}
	return dir, source + "@" + e.Version, nil
}

// Materialize ensures the locked plugin is in the cache and its integrity matches.
func Materialize(ctx context.Context, cacheDir, source string, e LockEntry) (string, error) {
	dir, err := cachePath(cacheDir, source, e)
	if err != nil {
		return "", err
	}
	marker := filepath.Join(dir, ".codi-integrity")
	if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) == e.Integrity {
		return dir, nil
	}
	repo, err := cloneMem(ctx, e.Repo)
	if err != nil {
		return "", err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(e.Commit))
	if err != nil {
		return "", fmt.Errorf("%s: commit %s not found: %w", e.Repo, e.Commit, err)
	}
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	sum, err := exportTree(commit, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if e.Integrity != "" && sum != e.Integrity {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("integrity mismatch for %s@%s: lock has %s, content is %s", source, e.Version, e.Integrity, sum)
	}
	os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	if err := os.WriteFile(marker, []byte(sum+"\n"), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// cachePath is <cacheDir>/<registry>/<plugin>/<version>-<sha>, refusing anything that
// would leave cacheDir (the names come from codi.lock and registry indexes).
func cachePath(cacheDir, source string, e LockEntry) (string, error) {
	reg, name, err := CheckSource(source)
	if err != nil {
		return "", err
	}
	v, err := semver.NewVersion(e.Version)
	if err != nil {
		return "", fmt.Errorf("%s: invalid version %q", source, e.Version)
	}
	if !shaRe.MatchString(e.Commit) {
		return "", fmt.Errorf("%s: invalid commit %q", source, e.Commit)
	}
	dir := filepath.Join(cacheDir, reg, name, v.String()+"-"+shortSHA(e.Commit))
	if rel, err := filepath.Rel(cacheDir, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: cache path escapes the cache directory", source)
	}
	return dir, nil
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func cloneMem(ctx context.Context, url string) (*git.Repository, error) {
	r, err := git.CloneContext(ctx, memory.NewStorage(), nil, &git.CloneOptions{URL: url, Tags: git.AllTags, NoCheckout: true,
		Auth: AuthFor(ctx, url)})
	if err != nil {
		return nil, fmt.Errorf("clone %s: %w", Redact(url), err)
	}
	return r, nil
}

// exportTree writes the commit's files into dir and returns the integrity hash
// ("sha256-<base64>") over sorted (path, content) pairs.
func exportTree(c *object.Commit, dir string) (string, error) {
	tree, err := c.Tree()
	if err != nil {
		return "", err
	}
	type entry struct {
		name string
		data []byte
	}
	var entries []entry
	err = tree.Files().ForEach(func(f *object.File) error {
		if f.Mode == filemode.Symlink || f.Mode == filemode.Submodule {
			return nil
		}
		clean := path.Clean(f.Name)
		if strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return fmt.Errorf("unsafe path %q", f.Name)
		}
		rd, err := f.Reader()
		if err != nil {
			return err
		}
		defer rd.Close()
		data, err := io.ReadAll(rd)
		if err != nil {
			return err
		}
		entries = append(entries, entry{clean, data})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%d\x00", e.name, len(e.data))
		h.Write(e.data)
		p := filepath.Join(dir, filepath.FromSlash(e.name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, e.data, 0o644); err != nil {
			return "", err
		}
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// ───────────────────────────────────────────────────────────── add / update

// FetchIndex reads index.yaml from a registry repository. For an official URL, the index
// must be signed by an official key (index.yaml.sig in the same commit, over the exact
// bytes): otherwise it fails with ErrBadSignature and returns no index.
func FetchIndex(ctx context.Context, url string) (*Index, error) {
	r, err := cloneMem(ctx, url)
	if err != nil {
		return nil, err
	}
	head, err := r.Head()
	if err != nil {
		return nil, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}
	f, err := c.File(IndexFile)
	if err != nil {
		return nil, fmt.Errorf("%s: no index.yaml: %w", Redact(url), err)
	}
	content, err := f.Contents()
	if err != nil {
		return nil, err
	}
	signedBy := ""
	if Trust(url) == TrustOfficial {
		var sig []byte
		if sf, err := c.File(SignatureFile); err == nil && sf.Size <= maxSignatureFile {
			s, err := sf.Contents()
			if err != nil {
				return nil, err
			}
			sig = []byte(s)
		}
		err := checkSerial(ctx, func() (Envelope, error) {
			env, id, err := VerifyIndex([]byte(content), sig, officialKeys(), VerifyOptions{Registries: officialURLs()})
			signedBy = id
			return env, err
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", Redact(url), err)
		}
	}
	idx, err := ParseIndex([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", Redact(url), err)
	}
	idx.SignedBy = signedBy
	return idx, nil
}

// ParseIndex decodes index.yaml.
func ParseIndex(data []byte) (*Index, error) {
	var idx Index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("index.yaml: %w", err)
	}
	return &idx, nil
}

// Resolution is the outcome of resolving a registry reference to a concrete version.
type Resolution struct {
	Source string
	Entry  LockEntry
	Dir    string
	Index  IndexEntry
	// Manifest is the plugin.yaml of the resolved version.
	Manifest *config.Plugin
}

// ResolveLatest resolves "<registry>/<name>" with a semver range against the registry
// index and the plugin repository tags, downloads it and computes its lock entry.
func ResolveLatest(ctx context.Context, registries []config.Registry, cacheDir, source, versionRange string) (*Resolution, error) {
	regName, name, err := CheckSource(source)
	if err != nil {
		return nil, err
	}
	var regURL string
	for _, r := range WithOfficial(registries) {
		if r.Name == regName {
			regURL = r.URL
		}
	}
	if regURL == "" {
		return nil, fmt.Errorf("unknown registry %q (declare it in codi.yaml spec.registries)", regName)
	}
	idx, err := FetchIndex(ctx, regURL)
	if err != nil {
		return nil, err
	}
	ie, ok := idx.Plugins[name]
	if !ok {
		return nil, fmt.Errorf("plugin %q not found in registry %q", name, regName)
	}
	if idx.SignedBy == "" {
		ie.Verified = false // only a verified official index vouches for its publishers
	}
	if err := CheckRepoURL(regURL, ie.Repo); err != nil {
		return nil, err
	}
	if publicOnly(ctx) && IsRemoteURL(ie.Repo) {
		if err := CheckPublicHost(ctx, ie.Repo); err != nil {
			return nil, fmt.Errorf("plugin repository %q: %w", Redact(ie.Repo), err)
		}
	}
	if versionRange == "" {
		versionRange = "*"
	}
	c, err := semver.NewConstraint(versionRange)
	if err != nil {
		return nil, err
	}
	repo, err := cloneMem(ctx, ie.Repo)
	if err != nil {
		return nil, err
	}
	tags, err := repo.Tags()
	if err != nil {
		return nil, err
	}
	var best *semver.Version
	var bestHash plumbing.Hash
	_ = tags.ForEach(func(ref *plumbing.Reference) error {
		v, err := semver.NewVersion(ref.Name().Short())
		if err != nil || !c.Check(v) {
			return nil
		}
		if best == nil || v.GreaterThan(best) {
			h := ref.Hash()
			if tag, err := repo.TagObject(h); err == nil { // annotated tag
				h = tag.Target
			}
			best, bestHash = v, h
		}
		return nil
	})
	if best == nil {
		return nil, fmt.Errorf("no version of %s matches %q", source, versionRange)
	}
	if iv, err := semver.NewVersion(ie.Version); err == nil && ie.Commit != "" && iv.Equal(best) && bestHash.String() != ie.Commit {
		return nil, fmt.Errorf("%s: tag v%s points to %s, but the registry index published it at %s (the tag moved)",
			source, best, shortSHA(bestHash.String()), shortSHA(ie.Commit))
	}
	commit, err := repo.CommitObject(bestHash)
	if err != nil {
		return nil, err
	}
	entry := LockEntry{Version: best.String(), Repo: ie.Repo, Commit: bestHash.String()}
	dir, err := cachePath(cacheDir, source, entry)
	if err != nil {
		return nil, err
	}
	os.RemoveAll(dir)
	sum, err := exportTree(commit, dir)
	if err != nil {
		return nil, err
	}
	entry.Integrity = sum
	if err := os.WriteFile(filepath.Join(dir, ".codi-integrity"), []byte(sum+"\n"), 0o644); err != nil {
		return nil, err
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return nil, fmt.Errorf("%s@%s has no plugin.yaml", source, entry.Version)
	}
	var m config.Plugin
	if err := yaml.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("%s@%s: plugin.yaml: %w", source, entry.Version, err)
	}
	entry.Capabilities = m.Spec.Capabilities
	return &Resolution{Source: source, Entry: entry, Dir: dir, Index: ie, Manifest: &m}, nil
}

// remoteTags is kept for callers that only need versions without cloning objects.
func remoteTags(ctx context.Context, url string) ([]string, error) {
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}})
	refs, err := rem.ListContext(ctx, &git.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range refs {
		if r.Name().IsTag() {
			out = append(out, r.Name().Short())
		}
	}
	return out, nil
}
