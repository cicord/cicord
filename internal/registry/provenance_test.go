package registry

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCallIdentityRequiresSignedLockedArtifact(t *testing.T) {
	key := useOfficial(t, regURL)
	commit := strings.Repeat("a", 40)
	index := "plugins:\n  guard:\n    repo: https://github.com/cicord/plugins\n    path: plugins/guard\n    version: 1.0.0\n    commit: " + commit + "\n    verified: true\n"
	r := NewResolver(t.TempDir(), t.TempDir())
	entry := LockEntry{Version: "1.0.0", Repo: "https://github.com/cicord/plugins", Path: "plugins/guard", Commit: commit}
	r.lock = &Lock{Plugins: map[string]LockEntry{"cicord/guard": entry}}
	saveIndexProof(filepath.Join(r.CacheDir, "official-index-proof.json"), indexProof{index, mustSign(t, index, envFor(regURL, 1), key)})
	if src, trusted := r.Provenance("cicord/guard"); src != "cicord" || !trusted {
		t.Fatal(src, trusted)
	}
	for _, modify := range []func(*LockEntry){func(e *LockEntry) { e.Commit = strings.Repeat("b", 40) }, func(e *LockEntry) { e.Repo = "https://github.com/other/guard" }, func(e *LockEntry) { e.Path = "plugins/other" }} {
		copy := entry
		modify(&copy)
		r.lock.Plugins["cicord/guard"] = copy
		if src, v := r.Provenance("cicord/guard"); src == "cicord" || v {
			t.Fatal("unsigned artifact claimed signed identity")
		}
	}
}
