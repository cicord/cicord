// Package registry resolves registry plugins (ADR-0011): a registry is a git repository
// with an index.yaml mapping plugin names to git repositories; plugin versions are semver
// tags; cicord.lock pins the resolved commit and a content hash.
//
// A plugin repository holds one plugin (plugin.yaml at its root, tags v<semver>) or several
// (a monorepo: the index entry's path is the plugin directory, tags <name>/v<semver>, and
// the content hash covers that directory only).
package registry

import (
	"context"
	"crypto/sha256"
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
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/version"
)

const LockFile = "cicord.lock"

// Index is the content of a registry's index.yaml.
type Index struct {
	proof   indexProof
	Plugins map[string]IndexEntry `yaml:"plugins"`
	// SignedBy is the ID of the official key whose signature verified (FetchIndex sets it
	// for official URLs only; "" otherwise). Never read from the file.
	SignedBy string `yaml:"-"`
}

type IndexEntry struct {
	Repo string `yaml:"repo"`
	// Path is the plugin directory inside Repo (slash-separated, e.g. plugins/polls) when
	// the repository holds several plugins; "" when plugin.yaml is at its root. A plugin
	// with a path is released with the tags <name>/v<semver> (TagPrefix), and only that
	// directory is downloaded and hashed.
	Path        string   `yaml:"path,omitempty"`
	DisplayName string   `yaml:"displayName,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Keywords    []string `yaml:"keywords,omitempty"`
	Publisher   string   `yaml:"publisher,omitempty"`
	Verified    bool     `yaml:"verified,omitempty"`
	// Version is the latest published version (written by `cicord plugin publish`), and
	// Commit the commit of its tag: resolving that version fails if the tag moved.
	Version string `yaml:"version,omitempty"`
	Commit  string `yaml:"commit,omitempty"`
}

// TagPrefix is the prefix of the release tags of plugin name: "<name>/" for a plugin in a
// subdirectory of its repository (several plugins share the tags of one repository), ""
// for a plugin at the root of its own repository (tags v<semver>).
func (e IndexEntry) TagPrefix(name string) string {
	if e.Path == "" {
		return ""
	}
	return name + "/"
}

// ReleaseTag is the tag of version (a semver without "v") with a TagPrefix.
func ReleaseTag(prefix, version string) string { return prefix + "v" + version }

// pluginPathRe is a relative directory: slash-separated segments that do not start with a
// dot (no ".", "..", hidden directories), without empty segments.
var pluginPathRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*(/[A-Za-z0-9_][A-Za-z0-9._-]*)*$`)

// CheckPluginPath validates IndexEntry.Path (it comes from registry indexes, which may be
// hostile): "" or a relative directory without "..", ".", backslashes or a leading slash.
func CheckPluginPath(p string) error {
	if p == "" || (pluginPathRe.MatchString(p) && path.Clean(p) == p && len(p) <= 255) {
		return nil
	}
	return fmt.Errorf("invalid plugin path %q: use a relative directory like plugins/<name> (no \"..\", no leading slash)", p)
}

// tagVersion parses a release tag with prefix: "<prefix>v1.2.0" (or "<prefix>1.2.0").
// Without prefix, tags containing a slash are not releases (they belong to the plugins of
// a monorepo).
func tagVersion(tag, prefix string) (*semver.Version, bool) {
	if prefix != "" {
		rest, ok := strings.CutPrefix(tag, prefix)
		if !ok {
			return nil, false
		}
		tag = rest
	}
	if strings.Contains(tag, "/") {
		return nil, false
	}
	v, err := semver.NewVersion(tag)
	return v, err == nil
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

// OfficialURLs are the URLs of the official Cicord registry, the first being its address
// (Official) and the others aliases: the monorepo github.com/cicord/cicord-plugins
// (plugins/<name>/, tags <name>/v<semver>, signed index.yaml at its root). The official
// plugins are also bundled in the binary (internal/catalog) for offline and first
// installs. An index read from one of these URLs must be signed by an official key
// (sign.go, OfficialKeys). Tests reset it (TestMain) so that they never reach GitHub;
// DevOfficialRegistryEnv overrides it in development builds.
var OfficialURLs = []string{"https://github.com/cicord/cicord-plugins"}

// Trust is computed from the registry URL, never declared in cicord.yaml. "official" here
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

// Lock is cicord.lock (https://cicord.dev/docs/plugins/publishing/).
type Lock struct {
	LockVersion int                  `yaml:"lockVersion"`
	Plugins     map[string]LockEntry `yaml:"plugins"`
}

type LockEntry struct {
	Version string `yaml:"version"`
	Repo    string `yaml:"repo"`
	// Path is the plugin directory inside Repo (IndexEntry.Path); Integrity covers it only.
	Path         string   `yaml:"path,omitempty"`
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

// ParseLock decodes cicord.lock (nil or empty: an empty lock).
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

// MarshalLock encodes cicord.lock with its header.
func MarshalLock(l *Lock) ([]byte, error) {
	l.LockVersion = 1
	data, err := yaml.Marshal(l)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by cicord — do not edit by hand.\n"), data...), nil
}

func WriteLock(repoRoot string, l *Lock) error {
	data, err := MarshalLock(l)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repoRoot, LockFile), data, 0o644)
}

// SetLockEntry returns cicord.lock content (cur may be nil) with source pinned to e.
func SetLockEntry(cur []byte, source string, e LockEntry) ([]byte, error) {
	l, err := ParseLock(cur)
	if err != nil {
		return nil, err
	}
	l.Plugins[source] = e
	return MarshalLock(l)
}

// Resolver implements config.PluginResolver from cicord.lock + a local cache.
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
// only reach public addresses unless CICORD_ALLOW_PRIVATE_REGISTRIES=true;
// secret resolves the secretRef of private registries.
func NewRuntimeResolver(repoRoot, cacheDir string, secret func(string) (string, bool)) *Resolver {
	r := NewResolver(repoRoot, cacheDir)
	r.PublicOnly = !AllowPrivateRegistries()
	r.Secret = secret
	return r
}

// DefaultCacheDir is ~/.cache/cicord/plugins (or $CICORD_CACHE_DIR/plugins).
func DefaultCacheDir() string {
	if d := os.Getenv("CICORD_CACHE_DIR"); d != "" {
		return filepath.Join(d, "plugins")
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "cicord", "plugins")
	}
	return filepath.Join(os.TempDir(), "cicord-plugins")
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
		return "", "", fmt.Errorf("registry plugin %q is not in %s (run `cicord plugin add %s`)", source, LockFile, source)
	}
	c, err := semver.NewConstraint(versionRange)
	if err != nil {
		return "", "", fmt.Errorf("invalid version range %q: %w", versionRange, err)
	}
	v, err := semver.NewVersion(e.Version)
	if err != nil || !c.Check(v) {
		return "", "", fmt.Errorf("locked version %s of %q does not satisfy %q (run `cicord plugin update`)", e.Version, source, versionRange)
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
	parent, err := openCacheParent(cacheDir, dir)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	name := filepath.Base(dir)
	if verifyCache(ctx, parent, name, e.Integrity, false) {
		return dir, nil
	}
	// Pre-0.2 caches used a shortened commit and a marker. Recompute the tree
	// before using them offline; all future writes use the full, content-specific path.
	legacy := legacyCacheName(e)
	if verifyCache(ctx, parent, legacy, e.Integrity, true) {
		return filepath.Join(filepath.Dir(dir), legacy), nil
	}
	_, pluginName, _ := CheckSource(source)
	tag := ReleaseTag((IndexEntry{Path: e.Path}).TagPrefix(pluginName), e.Version)
	repo, err := pinnedRepo(ctx, e.Repo, plumbing.NewHash(e.Commit), plumbing.NewTagReferenceName(tag))
	if err != nil {
		return "", err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(e.Commit))
	if err != nil {
		return "", fmt.Errorf("%s: commit %s not found: %w", Redact(e.Repo), e.Commit, err)
	}
	files, err := commitFiles(commit, e.Path)
	if err != nil {
		return "", err
	}
	sum := Integrity(files)
	if e.Integrity != "" && sum != e.Integrity {
		return "", fmt.Errorf("integrity mismatch for %s@%s: lock has %s, content is %s", source, e.Version, e.Integrity, sum)
	}
	if err = installCache(ctx, parent, name, sum, files); err != nil {
		return "", err
	}
	return dir, nil
}

// cachePath is <cacheDir>/<registry>/<plugin>/<version>-<sha>, refusing anything that
// would leave cacheDir (the names come from cicord.lock and registry indexes).
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
	if err := CheckPluginPath(e.Path); err != nil {
		return "", fmt.Errorf("%s: %w", source, err)
	}
	suffix := sha256.Sum256([]byte(e.Path + "\x00" + e.Integrity))
	dir := filepath.Join(cacheDir, reg, name, fmt.Sprintf("%s-%s-%x", v.String(), e.Commit, suffix[:12]))
	if rel, err := filepath.Rel(cacheDir, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: cache path escapes the cache directory", source)
	}
	return dir, nil
}

func legacyCacheName(e LockEntry) string {
	v, _ := semver.NewVersion(e.Version)
	return v.String() + "-" + shortSHA(e.Commit)
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// commitFiles reads precisely the regular Git files covered by the lock.
func commitFiles(c *object.Commit, sub string) (map[string][]byte, error) {
	if err := CheckPluginPath(sub); err != nil {
		return nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	if sub != "" {
		tree, err = tree.Tree(sub)
		if err != nil {
			return nil, fmt.Errorf("directory %s not found in commit %s: %w", sub, shortSHA(c.Hash.String()), err)
		}
	}
	return packageFiles(tree, maxPackageFiles, maxPackageSize, maxObjectSize)
}

// Count all visited entries so directories and ignored symbolic links also have
// bounded traversal cost. Only ordinary file contents contribute to the lock hash.
func packageFiles(tree *object.Tree, maxFiles int, maxTotal, maxFile int64) (map[string][]byte, error) {
	files := map[string][]byte{}
	folded := map[string]string{}
	var total int64
	walk := object.NewTreeWalker(tree, true, nil)
	defer walk.Close()
	for visited := 0; ; visited++ {
		name, entry, err := walk.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if visited >= 2*maxFiles {
			return nil, ErrRegistryLimit
		}
		if entry.Mode == filemode.Dir || entry.Mode == filemode.Symlink || entry.Mode == filemode.Submodule {
			continue
		}
		if !fs.ValidPath(name) || !filepath.IsLocal(filepath.FromSlash(name)) || strings.ContainsAny(name, "\\:") {
			return nil, errors.New("unsafe package filename")
		}
		if len(files) >= maxFiles {
			return nil, ErrRegistryLimit
		}
		if err := checkFoldCollision(folded, name); err != nil {
			return nil, err
		}
		f, err := tree.TreeEntryFile(&entry)
		if err != nil {
			return nil, err
		}
		if f.Size < 0 || f.Size > maxFile || f.Size > maxTotal-total {
			return nil, ErrRegistryLimit
		}
		rd, err := f.Reader()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rd, f.Size+1))
		_ = rd.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != f.Size {
			return nil, errors.New("invalid package file size")
		}
		files[name] = data
		total += f.Size
	}

}

// checkFoldCollision refuses a package path that names the same file or directory as
// another one on a case-insensitive or normalization-insensitive filesystem (macOS,
// Windows): "src/index.ts" and "src/Index.ts", or "src/x.ts" and "SRC/y.ts". Such a tree
// would be cached with one file overwriting the other, so what is built would depend on
// the filesystem and on write order, not on the locked tree.
func checkFoldCollision(seen map[string]string, name string) error {
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		key := cases.Fold().String(norm.NFC.String(p))
		if prev, ok := seen[key]; ok && prev != p {
			return fmt.Errorf("unsafe package filename: %q and %q are the same path on case-insensitive filesystems", prev, p)
		}
		seen[key] = p
	}
	return nil
}

// Integrity is the content hash of a plugin ("sha256-<base64>") over its files sorted by
// path (slash-separated, relative to the plugin directory): what cicord.lock pins.
func Integrity(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		integrityHeader(h, n, int64(len(files[n])))
		h.Write(files[n])
	}
	return integrityDigest(h)
}

// ───────────────────────────────────────────────────────────── add / update

// FetchIndex reads index.yaml from a registry repository. For an official URL, the index
// must be signed by an official key (index.yaml.sig in the same commit, over the exact
// bytes): otherwise it fails with ErrBadSignature and returns no index. A build without
// official keys fails with ErrNoOfficialKey without any network access.
func FetchIndex(ctx context.Context, url string) (*Index, error) {
	if Trust(url) == TrustOfficial && !HasOfficialKeys() {
		return nil, fmt.Errorf("%s: %w", Redact(url), ErrNoOfficialKey)
	}
	r, err := cloneRef(ctx, url, plumbing.HEAD)
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
	if f.Size < 0 || f.Size > maxRefsBytes {
		return nil, fmt.Errorf("index.yaml: %w", ErrRegistryLimit)
	}
	content, err := f.Contents()
	if err != nil {
		return nil, err
	}
	var proof indexProof
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
		proof = indexProof{Index: content, Signature: string(sig), Fetched: time.Now()}
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
	idx.proof = proof
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
		return nil, fmt.Errorf("unknown registry %q (declare it in cicord.yaml spec.registries)", regName)
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
	if err := CheckPluginPath(ie.Path); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	prefix := ie.TagPrefix(name)
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
	ceiling, err := indexCeiling(idx, ie, source)
	if err != nil {
		return nil, err
	}
	refs, err := listRefs(ctx, ie.Repo)
	if err != nil {
		return nil, fmt.Errorf("list release tags of %s: %w", Redact(ie.Repo), err)
	}
	cands := releaseCandidates(refs, prefix, c, ceiling)
	if len(cands) == 0 {
		return nil, fmt.Errorf("no version of %s matches %q", source, versionRange)
	}
	var refused []refusedRelease
	for n, cand := range cands {
		if n == maxEngineProbes {
			break
		}
		// A release already known to require another engine is skipped without a fetch.
		if engine, ok := cachedEngine(ie.Repo, cand.ref, ie.Path); ok && !EngineAccepts(engine) {
			refused = append(refused, refusedRelease{cand.v.String(), engine})
			continue
		}
		rel, err := fetchRelease(ctx, source, ie, prefix, cand)
		if err != nil {
			return nil, err
		}
		rememberEngine(ie.Repo, cand.ref, ie.Path, rel.manifest.Spec.Engine)
		if !EngineAccepts(rel.manifest.Spec.Engine) {
			refused = append(refused, refusedRelease{cand.v.String(), rel.manifest.Spec.Engine})
			continue
		}
		dir, err := installRelease(ctx, cacheDir, source, rel)
		if err != nil {
			return nil, err
		}
		saveIndexProof(filepath.Join(cacheDir, "official-index-proof.json"), idx.proof)
		rel.entry.Capabilities = rel.manifest.Spec.Capabilities
		return &Resolution{Source: source, Entry: rel.entry, Dir: dir, Index: ie, Manifest: rel.manifest}, nil
	}
	return nil, noCompatibleRelease(source, versionRange, refused, len(cands) > maxEngineProbes)
}

// maxEngineProbes bounds the releases (newest first) whose plugin.yaml is fetched to find
// one that supports the running engine: each probe is a shallow clone.
const maxEngineProbes = 8

// engineVersion is the running engine, checked against the spec.engine of plugin
// manifests (a variable for tests).
var engineVersion = func() string { return version.Version }

// EngineAccepts reports whether a plugin manifest's spec.engine admits the running
// engine, exactly like the loader (config.LoadOptions.EngineVersion): pre-release engines
// match by semver precedence (version.EngineConstraint), a development build whose version
// is not semver ("dev") admits every range, and an empty or invalid range admits none.
func EngineAccepts(rng string) bool {
	v, err := semver.NewVersion(engineVersion())
	if err != nil {
		return true
	}
	c, err := version.EngineConstraint(rng)
	return err == nil && c.Check(v)
}

type candidate struct {
	v   *semver.Version
	ref *plumbing.Reference
}

// releaseCandidates lists the release tags with prefix that satisfy c, newest first,
// without those above ceiling (nil: no ceiling).
func releaseCandidates(refs []*plumbing.Reference, prefix string, c *semver.Constraints, ceiling *semver.Version) []candidate {
	var out []candidate
	for _, ref := range refs {
		if !ref.Name().IsTag() {
			continue
		}
		v, ok := tagVersion(ref.Name().Short(), prefix)
		if !ok || !c.Check(v) || (ceiling != nil && v.GreaterThan(ceiling)) {
			continue
		}
		out = append(out, candidate{v, ref})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].v.GreaterThan(out[b].v) })
	return out
}

// indexCeiling is the highest version a resolution may pick: for the official registry
// (a verified, signed index) the version its entry publishes, so that a tag pushed to the
// repository but not yet released through the signed index is never installed. Other
// registries have no ceiling: their index is not authoritative.
func indexCeiling(idx *Index, ie IndexEntry, source string) (*semver.Version, error) {
	if idx.SignedBy == "" {
		return nil, nil
	}
	v, err := semver.NewVersion(ie.Version)
	if err != nil {
		return nil, fmt.Errorf("%s: the signed official index publishes no version of this plugin", source)
	}
	return v, nil
}

type refusedRelease struct{ version, engine string }

func noCompatibleRelease(source, versionRange string, refused []refusedRelease, more bool) error {
	running := engineVersion()
	if len(refused) == 0 {
		return fmt.Errorf("no version of %s matches %q", source, versionRange)
	}
	checked := ""
	if more {
		checked = fmt.Sprintf(" (checked the %d newest matching releases)", len(refused))
	}
	r := refused[0]
	return fmt.Errorf("no version of %s matching %q supports Cicord %s%s: the newest, %s, requires Cicord %q; upgrade Cicord or pin an older range (%s@<range>)",
		source, versionRange, running, checked, r.version, r.engine, source)
}

type release struct {
	entry    LockEntry
	files    map[string][]byte
	manifest *config.Plugin
}

// fetchRelease downloads one candidate release tag and reads its files and manifest.
func fetchRelease(ctx context.Context, source string, ie IndexEntry, prefix string, cand candidate) (*release, error) {
	repo, err := cloneRef(ctx, ie.Repo, cand.ref.Name())
	if err != nil {
		return nil, err
	}
	fetched, err := repo.Reference(cand.ref.Name(), true)
	if err != nil {
		return nil, err
	}
	if fetched.Hash() != cand.ref.Hash() {
		return nil, errors.New("selected release tag changed during fetch")
	}
	hash := fetched.Hash()
	if tag, err := repo.TagObject(hash); err == nil {
		hash = tag.Target
	}
	if iv, err := semver.NewVersion(ie.Version); err == nil && ie.Commit != "" && iv.Equal(cand.v) && hash.String() != ie.Commit {
		return nil, fmt.Errorf("%s: tag %s points to %s, but the registry index published it at %s (the tag moved)",
			source, ReleaseTag(prefix, cand.v.String()), shortSHA(hash.String()), shortSHA(ie.Commit))
	}
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return nil, err
	}
	entry := LockEntry{Version: cand.v.String(), Repo: ie.Repo, Path: ie.Path, Commit: hash.String()}
	files, err := commitFiles(commit, ie.Path)
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", source, entry.Version, err)
	}
	entry.Integrity = Integrity(files)
	raw, ok := files["plugin.yaml"]
	if !ok {
		where := "the repository root"
		if ie.Path != "" {
			where = ie.Path
		}
		return nil, fmt.Errorf("%s@%s has no plugin.yaml in %s", source, entry.Version, where)
	}
	var m config.Plugin
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s@%s: plugin.yaml: %w", source, entry.Version, err)
	}
	return &release{entry: entry, files: files, manifest: &m}, nil
}

func installRelease(ctx context.Context, cacheDir, source string, rel *release) (string, error) {
	dir, err := cachePath(cacheDir, source, rel.entry)
	if err != nil {
		return "", err
	}
	parent, err := openCacheParent(cacheDir, dir)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	return dir, installCache(ctx, parent, filepath.Base(dir), rel.entry.Integrity, rel.files)
}

// The spec.engine of release tags already read, by repository, tag object and plugin
// path: a tag that moves gets a new hash, so an entry never goes stale.
var (
	enginesMu sync.Mutex
	engines   = map[string]string{}
)

const maxCachedEngines = 4096

func engineKey(repo string, ref *plumbing.Reference, sub string) string {
	return config.NormalizeRegistryURL(repo) + "\x00" + ref.Hash().String() + "\x00" + sub
}

func cachedEngine(repo string, ref *plumbing.Reference, sub string) (string, bool) {
	enginesMu.Lock()
	defer enginesMu.Unlock()
	e, ok := engines[engineKey(repo, ref, sub)]
	return e, ok
}

func rememberEngine(repo string, ref *plumbing.Reference, sub, engine string) {
	enginesMu.Lock()
	defer enginesMu.Unlock()
	if len(engines) >= maxCachedEngines {
		clear(engines)
	}
	engines[engineKey(repo, ref, sub)] = engine
}

// ListTags lists the tag names of a git repository without cloning it (ls-remote).
func ListTags(ctx context.Context, url string) ([]string, error) {
	refs, err := listRefs(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", Redact(url), err)
	}
	var out []string
	for _, r := range refs {
		if r.Name().IsTag() {
			out = append(out, r.Name().Short())
		}
	}
	return out, nil
}
