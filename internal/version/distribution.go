package version

import (
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ReleaseTag is stamped on official release binaries and images. Go also records the
// exact module version for a go install of a tag.
var ReleaseTag string

// SourceRef identifies a source build made without VCS metadata (for example Docker).
// Source checkouts normally receive the full commit from the Go toolchain.
var SourceRef string

// Go run and source archives can omit the VCS stamp. This published source baseline
// implements the branch SDK. Snapshot CI explicitly supplies the engine version when
// installing it. An ordinary source binary prefers its own embedded VCS commit.
const snapshotBaseline = "25e50db5a14c082966877fc39b6ebde329be7b27"

var commitRef = regexp.MustCompile("^[0-9a-f]{40}$")
var pseudoRef = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-[0-9A-Za-z.-]*[0-9]{14}-[0-9a-f]{12}$`)

func buildInfo() *debug.BuildInfo {
	info, _ := debug.ReadBuildInfo()
	return info
}

// Published reports whether the binary identifies a tagged distribution of v. A
// semantic prerelease number alone says nothing about release archives or npm.
func Published(v string) bool {
	return published(v, ReleaseTag, buildInfo())
}

func published(v, tag string, info *debug.BuildInfo) bool {
	sv, err := semver.StrictNewVersion(v)
	if err != nil || strings.HasPrefix(sv.Prerelease(), "dev") || sv.Metadata() != "" {
		return false
	}
	want := "v" + v
	return tag == want || (info != nil && info.Main.Path == "github.com/cicord/cicord" && info.Main.Version == want)
}

// Revision returns an immutable go-install ref for the exact source. A versioned
// module installation can have a Go pseudo-version but no local Git metadata.
func Revision() string {
	if ref := revision(SourceRef, buildInfo()); ref != "" {
		return ref
	}
	return snapshotBaseline
}

// ValidSourceRef accepts only immutable commit or Go pseudo-version references.
func ValidSourceRef(ref string) bool { return commitRef.MatchString(ref) || pseudoRef.MatchString(ref) }

func revision(override string, info *debug.BuildInfo) string {
	if ValidSourceRef(override) {
		return override
	}
	if info == nil {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && commitRef.MatchString(s.Value) {
			return s.Value
		}
	}
	if info.Main.Path == "github.com/cicord/cicord" && pseudoRef.MatchString(info.Main.Version) {
		return info.Main.Version
	}
	return ""
}
