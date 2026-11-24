package registry

import (
	"context"
	"testing"
)

func TestLatestVersion(t *testing.T) {
	repo := initRepo(t, map[string]string{"plugin.yaml": "metadata:\n  name: x\n"}, "v1.0.0", "v1.2.0", "v2.0.0-rc.1", "latest")
	v, err := LatestVersion(context.Background(), "/tmp/registry", repo, "")
	if err != nil || v != "1.2.0" {
		t.Fatalf("latest = %q, %v (prereleases and non-semver tags are ignored)", v, err)
	}
	// An https registry may not point to a local repository.
	if _, err := LatestVersion(context.Background(), "https://example.org/r.git", repo, ""); err == nil {
		t.Error("https registry listing a local repository")
	}
	// Public-only lookups refuse private hosts.
	if _, err := LatestVersion(WithPublicOnly(context.Background()), "https://example.org/r.git", "https://127.0.0.1/x.git", ""); err == nil {
		t.Error("public-only lookup of a private host")
	}
}

func TestUpdateLevelAndRanges(t *testing.T) {
	for _, c := range []struct{ cur, lat, want string }{
		{"1.0.0", "1.0.1", LevelPatch}, {"1.0.0", "1.1.0", LevelMinor}, {"1.4.2", "2.0.0", LevelMajor},
		{"1.0.0", "1.0.0", ""}, {"2.0.0", "1.9.0", ""}, {"x", "1.0.0", ""},
		// semver 2.0 precedence: a pre-release precedes its release, numeric identifiers
		// compare as numbers, build metadata is ignored.
		{"1.0.0-rc.1", "1.0.0", LevelPatch}, {"1.0.0", "1.0.0-rc.1", ""}, {"1.0.0-rc.2", "1.0.0-rc.10", LevelPatch},
		{"1.0.0-rc.10", "1.0.0-rc.2", ""}, {"1.0.0", "1.0.0+build.2", ""}, {"0.9.0", "1.0.0-rc.1", LevelMajor},
	} {
		if got := UpdateLevel(c.cur, c.lat); got != c.want {
			t.Errorf("UpdateLevel(%s, %s) = %q, want %q", c.cur, c.lat, got, c.want)
		}
	}
	if !RangeAllows("^1.0.0", "1.4.0") || RangeAllows("^1.0.0", "2.0.0") || RangeAllows("not a range", "1.0.0") {
		t.Error("RangeAllows")
	}
	// Plugin version ranges (unlike engine ranges) only admit a pre-release when they name
	// one, like npm: "^1.0.0" never resolves to a release candidate.
	if RangeAllows("^1.0.0", "1.1.0-rc.1") || RangeAllows("*", "1.0.0-rc.1") || !RangeAllows("^1.1.0-0", "1.1.0-rc.1") || !RangeAllows("^1.0.0", "1.0.1+build.3") {
		t.Error("RangeAllows pre-release")
	}
	tags := []string{"v2.0.0-rc.1", "v2.0.0-rc.10", "v2.0.0-rc.2", "v1.9.0", "v1.9.1+build.4"}
	if got := HighestVersion(tags, "", ">=2.0.0-0"); got != "2.0.0-rc.10" {
		t.Errorf("highest pre-release = %q", got)
	}
	if got := HighestVersion(tags, "", "*"); got != "1.9.1+build.4" {
		t.Errorf("highest release = %q", got)
	}
	if got := HighestVersion(append(tags, "v2.0.0"), "", ">=2.0.0-0"); got != "2.0.0" {
		t.Errorf("highest with the release = %q", got)
	}
}
