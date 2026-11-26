package version

import (
	"runtime/debug"
	"testing"
)

func TestDistributionProvenance(t *testing.T) {
	const candidate = "0.2.0-rc.1"
	main := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/cicord/cicord", Version: v}}
	}
	for _, c := range []struct {
		v, tag string
		info   *debug.BuildInfo
		want   bool
	}{
		{candidate, "", nil, false}, {candidate, "", main("(devel)"), false},
		{candidate, "v" + candidate, nil, true}, {candidate, "", main("v" + candidate), true},
		{candidate, "v0.1.11", nil, false}, {"0.2.0", "v0.2.0", nil, true},
		{"0.2.0-dev", "v0.2.0-dev", nil, false}, {"0.2.0-snapshot+abcdef", "v0.2.0-rc.1", nil, false},
	} {
		if got := published(c.v, c.tag, c.info); got != c.want {
			t.Errorf("published(%s,%s,%+v)=%t", c.v, c.tag, c.info, got)
		}
	}

	const sha = "0123456789abcdef0123456789abcdef01234567"
	const pseudo = "v0.1.12-0.20261126061048-cdaf13a2ec56"
	info := main(pseudo)
	if got := revision("", info); got != pseudo {
		t.Fatal(got)
	}
	info.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: sha}}
	if got := revision("", info); got != sha {
		t.Fatal(got)
	}
	if got := revision(pseudo, info); got != pseudo {
		t.Fatal(got)
	}
	for _, mutable := range []string{"main", "feature/0.2.0", "latest", "v0.1.11", "dev", "123abc", "x; false"} {
		if ValidSourceRef(mutable) || revision(mutable, nil) != "" {
			t.Errorf("accepted mutable or invalid ref %q", mutable)
		}
	}
}
