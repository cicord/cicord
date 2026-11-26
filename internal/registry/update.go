package registry

import (
	"context"
	"fmt"
	"path"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// Plugin updates (ADR-0011 "Update" sections): a registry plugin is up to date when its
// cicord.lock version is the highest release tag of its repository, any major version.

// LatestVersion returns the highest release tag of a plugin repository listed by the
// registry at registryURL, without cloning it (tags only). tagPrefix is the plugin's
// IndexEntry.TagPrefix ("<name>/" in a monorepo). Prereleases are ignored, like
// ResolveLatest with the range "*". It reads tags only, so it does not know the engine a
// release requires nor the signed index ceiling: LatestFor reports the version an update
// resolves. The repository is checked like ResolveLatest does: an https registry only
// lists https repositories, and public-only contexts refuse non-public hosts.
func LatestVersion(ctx context.Context, registryURL, repoURL, tagPrefix string) (string, error) {
	if err := CheckRepoURL(registryURL, repoURL); err != nil {
		return "", err
	}
	if publicOnly(ctx) && IsRemoteURL(repoURL) {
		if err := CheckPublicHost(ctx, repoURL); err != nil {
			return "", fmt.Errorf("plugin repository %q: %w", Redact(repoURL), err)
		}
	}
	tags, err := ListTags(ctx, repoURL)
	if err != nil {
		return "", err
	}
	v := HighestVersion(tags, tagPrefix, "*")
	if v == "" {
		return "", fmt.Errorf("%s has no release tag %s", Redact(repoURL), ReleaseTag(tagPrefix, "<version>"))
	}
	return v, nil
}

// LatestFor is the version `cicord plugin update` would resolve for plugin name of a
// registry index entry (signed: the index is the verified official one), without
// downloading the plugin: the highest release tag whose plugin.yaml spec.engine admits
// the running engine (EngineAccepts), and for a signed index never above the version it
// publishes. Like ResolveLatest, prereleases are ignored and at most maxEngineProbes
// manifests are read. Unlike LatestVersion (tags only), it reads plugin.yaml at the
// candidate tags, so its result is never a version the loader would refuse.
func LatestFor(ctx context.Context, registryURL, name string, ie IndexEntry, signed bool) (string, error) {
	if err := CheckRepoURL(registryURL, ie.Repo); err != nil {
		return "", err
	}
	if err := CheckPluginPath(ie.Path); err != nil {
		return "", err
	}
	if publicOnly(ctx) && IsRemoteURL(ie.Repo) {
		if err := CheckPublicHost(ctx, ie.Repo); err != nil {
			return "", fmt.Errorf("plugin repository %q: %w", Redact(ie.Repo), err)
		}
	}
	var ceiling *semver.Version
	if signed {
		v, err := semver.NewVersion(ie.Version)
		if err != nil {
			return "", fmt.Errorf("%s: the signed official index publishes no version of this plugin", name)
		}
		ceiling = v
	}
	refs, err := listRefs(ctx, ie.Repo)
	if err != nil {
		return "", fmt.Errorf("list %s: %w", Redact(ie.Repo), err)
	}
	all, _ := semver.NewConstraint("*")
	prefix := ie.TagPrefix(name)
	cands := releaseCandidates(refs, prefix, all, ceiling)
	if len(cands) == 0 {
		return "", fmt.Errorf("%s has no release tag %s", Redact(ie.Repo), ReleaseTag(prefix, "<version>"))
	}
	var refused []refusedRelease
	for n, cand := range cands {
		if n == maxEngineProbes {
			break
		}
		engine, ok := cachedEngine(ie.Repo, cand.ref, ie.Path)
		if !ok {
			if engine, err = releaseEngine(ctx, ie, cand); err != nil {
				return "", err
			}
			rememberEngine(ie.Repo, cand.ref, ie.Path, engine)
		}
		if EngineAccepts(engine) {
			return cand.v.String(), nil
		}
		refused = append(refused, refusedRelease{cand.v.String(), engine})
	}
	return "", noCompatibleRelease(name, "*", refused, len(cands) > maxEngineProbes)
}

// releaseEngine reads spec.engine from plugin.yaml at a release tag (a shallow clone,
// no package files).
func releaseEngine(ctx context.Context, ie IndexEntry, cand candidate) (string, error) {
	repo, err := cloneRef(ctx, ie.Repo, cand.ref.Name())
	if err != nil {
		return "", err
	}
	fetched, err := repo.Reference(cand.ref.Name(), true)
	if err != nil {
		return "", err
	}
	hash := fetched.Hash()
	if tag, err := repo.TagObject(hash); err == nil {
		hash = tag.Target
	}
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return "", err
	}
	f, err := commit.File(path.Join(ie.Path, "plugin.yaml"))
	if err != nil {
		return "", fmt.Errorf("%s at %s: no plugin.yaml: %w", Redact(ie.Repo), cand.ref.Name().Short(), err)
	}
	if f.Size < 0 || f.Size > maxManifestSize {
		return "", ErrRegistryLimit
	}
	raw, err := f.Contents()
	if err != nil {
		return "", err
	}
	var m struct {
		Spec struct {
			Engine string `yaml:"engine"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(raw), &m); err != nil {
		return "", fmt.Errorf("%s at %s: plugin.yaml: %w", Redact(ie.Repo), cand.ref.Name().Short(), err)
	}
	return m.Spec.Engine, nil
}

const maxManifestSize = 1 << 20

// HighestVersion is the highest release tag with prefix (TagPrefix) satisfying rng, as a
// version ("" when none).
func HighestVersion(tags []string, prefix, rng string) string {
	c, err := semver.NewConstraint(rng)
	if err != nil {
		return ""
	}
	var best *semver.Version
	for _, t := range tags {
		v, ok := tagVersion(t, prefix)
		if !ok || !c.Check(v) {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
		}
	}
	if best == nil {
		return ""
	}
	return best.String()
}

// Update levels.
const (
	LevelMajor = "major"
	LevelMinor = "minor"
	LevelPatch = "patch"
)

// UpdateLevel compares two versions: "" when latest is not newer than current (or either
// is not semver), otherwise the highest part that changed (major, minor or patch).
func UpdateLevel(current, latest string) string {
	cur, err1 := semver.NewVersion(current)
	lat, err2 := semver.NewVersion(latest)
	if err1 != nil || err2 != nil || !lat.GreaterThan(cur) {
		return ""
	}
	switch {
	case lat.Major() != cur.Major():
		return LevelMajor
	case lat.Minor() != cur.Minor():
		return LevelMinor
	default:
		return LevelPatch
	}
}

// RangeAllows reports whether the version range of a bot.yaml instance admits version
// (an invalid range admits nothing).
func RangeAllows(rng, version string) bool {
	if rng == "" {
		rng = "*"
	}
	c, err := semver.NewConstraint(rng)
	if err != nil {
		return false
	}
	v, err := semver.NewVersion(version)
	return err == nil && c.Check(v)
}
