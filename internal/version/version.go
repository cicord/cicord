// Package version holds the Cicord build version (overridden with -ldflags at release).
package version

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Version is the engine version, checked against cicord.yaml spec.engine and plugin manifests.
var Version = "0.1.11"

// Commit is the git commit the binary was built from.
var Commit = "dev"

// EngineConstraint parses an engine range (cicord.yaml spec.engine, plugin manifest
// spec.engine) to check an engine version against.
//
// Pre-release engines are matched by plain semver 2.0 precedence: 0.2.0-rc.1 sorts after
// every 0.1.x and before 0.2.0, so it satisfies ">=0.1.0" and ">=0.1.0 <0.2.0" but not
// ">=0.2.0" (">=0.2.0-0" admits the 0.2.0 pre-releases). Masterminds' default, like npm's,
// is to reject a pre-release unless the range itself names one; that would make every
// release candidate refuse every repository and plugin. Plugin version ranges keep that
// default: "^1.0.0" never resolves to a plugin's release candidate.
//
// Caret and tilde ranges compare the major and minor numbers instead: "^0.1.0" is 0.1.x, so
// it does not admit 0.2.0-rc.1.
func EngineConstraint(rng string) (*semver.Constraints, error) {
	c, err := semver.NewConstraint(rng)
	if err != nil {
		return nil, err
	}
	c.IncludePrerelease = true
	return c, nil
}

// EngineRange is the default spec.engine of new repositories and plugins created by the
// engine version v: its minor (its major from 1.0). The range of a pre-release starts at
// the first pre-release of that minor (">=0.2.0-0 <0.3.0"), so that it admits v itself and
// stays valid for the release. A version that does not start with "major.minor" ("dev")
// gives the 0.0 range.
func EngineRange(v string) string {
	var major, minor uint64
	pre := ""
	if sv, err := semver.NewVersion(v); err == nil {
		major, minor = sv.Major(), sv.Minor()
		if sv.Prerelease() != "" {
			pre = "-0"
		}
	} else {
		_, _ = fmt.Sscanf(v, "%d.%d", &major, &minor)
	}
	if major == 0 {
		return fmt.Sprintf(">=0.%d.0%s <0.%d.0", minor, pre, minor+1)
	}
	return fmt.Sprintf(">=%d.0.0%s <%d.0.0", major, pre, major+1)
}
