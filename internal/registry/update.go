package registry

import (
	"context"
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Plugin updates (ADR-0011 "Update" sections): a registry plugin is up to date when its
// codi.lock version is the highest release tag of its repository, any major version.

// LatestVersion returns the highest release tag of a plugin repository listed by the
// registry at registryURL, without cloning it (tags only). Prereleases are ignored, like
// ResolveLatest with the range "*", so the version it reports is the one an update
// resolves. The repository is checked like ResolveLatest does: an https registry only
// lists https repositories, and public-only contexts refuse non-public hosts.
func LatestVersion(ctx context.Context, registryURL, repoURL string) (string, error) {
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
	v := HighestVersion(tags, "*")
	if v == "" {
		return "", fmt.Errorf("%s has no release tag", Redact(repoURL))
	}
	return v, nil
}

// HighestVersion is the highest semver tag satisfying rng ("" when none).
func HighestVersion(tags []string, rng string) string {
	c, err := semver.NewConstraint(rng)
	if err != nil {
		return ""
	}
	var best *semver.Version
	for _, t := range tags {
		v, err := semver.NewVersion(t)
		if err != nil || !c.Check(v) {
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
