package version

import (
	"testing"

	"github.com/Masterminds/semver/v3"
)

// Engine ranges follow semver 2.0 precedence, pre-releases included: a pre-release sorts
// before the release of the same x.y.z (0.2.0-rc.1 < 0.2.0), so it satisfies ">=0.1.0" and
// "<0.2.0", and not ">=0.2.0". Build metadata is ignored.
func TestEngineConstraint(t *testing.T) {
	for _, c := range []struct {
		rng, v string
		want   bool
	}{
		{">=0.1.0", "0.1.1", true},
		{">=0.1.0", "0.2.0-rc.1", true},
		{">=0.1.0 <0.2.0", "0.2.0-rc.1", true},
		{">=0.1.0 <0.2.0", "0.2.0", false},
		{">=0.2.0", "0.2.0-rc.1", false},
		{">=0.2.0-0", "0.2.0-rc.1", true},
		{">=0.2.0-0 <0.3.0", "0.2.0", true},
		{">=0.2.0-rc.2", "0.2.0-rc.10", true},
		{">=0.2.0-rc.10", "0.2.0-rc.2", false},
		{">=0.2.0-rc.1 <0.2.0", "0.2.0", false},
		{">=0.1.0 <0.2.0", "0.1.1+build.7", true},
		{"<=0.2.0", "0.2.0+build.7", true},
		{">0.2.0", "0.2.0+build.7", false},
		// Caret and tilde compare the major and minor numbers, not precedence: ^0.1.0 and
		// ~0.1.0 mean 0.1.x, so a 0.2.0 pre-release is outside them.
		{"^0.1.0", "0.2.0-rc.1", false},
		{"~0.1.0", "0.2.0-rc.1", false},
		{"^0.2.0-0", "0.2.0-rc.1", true},
		{"^0.1.0", "0.1.2-rc.1", true},
	} {
		cs, err := EngineConstraint(c.rng)
		if err != nil {
			t.Fatalf("EngineConstraint(%q): %v", c.rng, err)
		}
		v, err := semver.NewVersion(c.v)
		if err != nil {
			t.Fatalf("NewVersion(%q): %v", c.v, err)
		}
		if got := cs.Check(v); got != c.want {
			t.Errorf("%q satisfies %q = %v, want %v", c.v, c.rng, got, c.want)
		}
	}
	if _, err := EngineConstraint(">=0.1.0 <"); err == nil {
		t.Error("invalid range accepted")
	}
}

// A binary's default range for new repositories and plugins must admit the binary itself,
// pre-release included.
func TestEngineRange(t *testing.T) {
	for _, c := range []struct{ v, want string }{
		{"0.1.1", ">=0.1.0 <0.2.0"},
		{"0.2.0-rc.1", ">=0.2.0-0 <0.3.0"},
		{"1.4.2", ">=1.0.0 <2.0.0"},
		{"2.0.0-rc.3", ">=2.0.0-0 <3.0.0"},
		{"dev", ">=0.0.0 <0.1.0"},
	} {
		got := EngineRange(c.v)
		if got != c.want {
			t.Errorf("EngineRange(%q) = %q, want %q", c.v, got, c.want)
		}
		if v, err := semver.NewVersion(c.v); err == nil {
			cs, err := EngineConstraint(got)
			if err != nil || !cs.Check(v) {
				t.Errorf("%q does not satisfy its own range %q (%v)", c.v, got, err)
			}
		}
	}
}
