package registry

import (
	"context"
	"testing"
)

func TestLatestVersion(t *testing.T) {
	repo := initRepo(t, map[string]string{"plugin.yaml": "metadata:\n  name: x\n"}, "v1.0.0", "v1.2.0", "v2.0.0-rc.1", "latest")
	v, err := LatestVersion(context.Background(), "/tmp/registry", repo)
	if err != nil || v != "1.2.0" {
		t.Fatalf("latest = %q, %v (prereleases and non-semver tags are ignored)", v, err)
	}
	// An https registry may not point to a local repository.
	if _, err := LatestVersion(context.Background(), "https://example.org/r.git", repo); err == nil {
		t.Error("https registry listing a local repository")
	}
	// Public-only lookups refuse private hosts.
	if _, err := LatestVersion(WithPublicOnly(context.Background()), "https://example.org/r.git", "https://127.0.0.1/x.git"); err == nil {
		t.Error("public-only lookup of a private host")
	}
}

func TestUpdateLevelAndRanges(t *testing.T) {
	for _, c := range []struct{ cur, lat, want string }{
		{"1.0.0", "1.0.1", LevelPatch}, {"1.0.0", "1.1.0", LevelMinor}, {"1.4.2", "2.0.0", LevelMajor},
		{"1.0.0", "1.0.0", ""}, {"2.0.0", "1.9.0", ""}, {"x", "1.0.0", ""},
	} {
		if got := UpdateLevel(c.cur, c.lat); got != c.want {
			t.Errorf("UpdateLevel(%s, %s) = %q, want %q", c.cur, c.lat, got, c.want)
		}
	}
	if !RangeAllows("^1.0.0", "1.4.0") || RangeAllows("^1.0.0", "2.0.0") || RangeAllows("not a range", "1.0.0") {
		t.Error("RangeAllows")
	}
}
