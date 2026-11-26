package registry

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestCallIdentityRequiresSignedLockedArtifact(t *testing.T) {
	key := useOfficial(t, regURL)
	commit := strings.Repeat("a", 40)
	index := "plugins:\n  guard:\n    repo: https://github.com/cicord/plugins\n    path: plugins/guard\n    version: 1.0.0\n    commit: " + commit + "\n    verified: true\n"
	r := NewResolver(t.TempDir(), t.TempDir())
	entry := LockEntry{Version: "1.0.0", Repo: "https://github.com/cicord/plugins", Path: "plugins/guard", Commit: commit}
	r.lock = &Lock{Plugins: map[string]LockEntry{"cicord/guard": entry}}
	saveIndexProof(filepath.Join(r.CacheDir, "official-index-proof.json"), indexProof{Index: index, Signature: mustSign(t, index, envFor(regURL, 1), key), Fetched: time.Now()})
	// The only fetch a mismatch may trigger fails (no network in tests): no identity.
	proofFetchMu.Lock()
	proofFetches[filepath.Join(r.CacheDir, "official-index-proof.json")] = time.Now()
	proofFetchMu.Unlock()
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

// officialRegistry is a local git official registry whose index.yaml can be republished.
type officialRegistry struct {
	t   *testing.T
	url string
	key ed25519.PrivateKey
}

func newOfficialRegistry(t *testing.T) *officialRegistry {
	t.Helper()
	key := useOfficial(t)
	url := initRepo(t, map[string]string{"README.md": "official"})
	OfficialURLs = []string{url}
	return &officialRegistry{t, url, key}
}

// publish commits index and its signature at serial.
func (o *officialRegistry) publish(index string, serial uint64) indexProof {
	o.t.Helper()
	sig := mustSign(o.t, index, envFor(o.url, serial), o.key)
	r, err := git.PlainOpen(o.url)
	if err != nil {
		o.t.Fatal(err)
	}
	wt, _ := r.Worktree()
	for name, content := range map[string]string{IndexFile: index, SignatureFile: sig} {
		if err := os.WriteFile(filepath.Join(o.url, name), []byte(content), 0o644); err != nil {
			o.t.Fatal(err)
		}
		if _, err := wt.Add(name); err != nil {
			o.t.Fatal(err)
		}
	}
	if _, err := wt.Commit("publish", &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}}); err != nil {
		o.t.Fatal(err)
	}
	return indexProof{Index: index, Signature: sig}
}

func guardIndex(version, commit string, verified bool) string {
	v := ""
	if verified {
		v = "    verified: true\n"
	}
	return "plugins:\n  guard:\n    repo: https://github.com/cicord/cicord-plugins\n    path: plugins/guard\n    version: " + version + "\n    commit: " + commit + "\n" + v
}

func guardResolver(t *testing.T, version, commit string) *Resolver {
	r := NewResolver(t.TempDir(), t.TempDir())
	r.lock = &Lock{Plugins: map[string]LockEntry{"cicord/guard": {Version: version, Repo: "https://github.com/cicord/cicord-plugins", Path: "plugins/guard", Commit: commit}}}
	return r
}

func cachedSerial(t *testing.T, r *Resolver) uint64 {
	t.Helper()
	data, err := readIndexProof(filepath.Join(r.CacheDir, "official-index-proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p indexProof
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return proofSerial(p)
}

// SEC-B-01: a cached proof that does not bind the lock entry (the lock was bumped to a
// newer release) refetches the signed index instead of dropping the official identity.
func TestProvenanceRefetchesWhenCachedProofMissesTheLock(t *testing.T) {
	o := newOfficialRegistry(t)
	c1, c2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	old := o.publish(guardIndex("1.0.0", c1, true), 1)
	o.publish(guardIndex("1.1.0", c2, true), 2)
	r := guardResolver(t, "1.1.0", c2)
	old.Fetched = time.Now()
	saveIndexProof(filepath.Join(r.CacheDir, "official-index-proof.json"), old)
	if src, verified := r.Provenance("cicord/guard"); src != "cicord" || !verified {
		t.Fatalf("after a lock bump: src=%q verified=%v", src, verified)
	}
	if s := cachedSerial(t, r); s != 2 {
		t.Fatalf("cached serial = %d, want the refetched 2", s)
	}
	// The older proof can no longer replace the newer one in the cache.
	saveIndexProof(filepath.Join(r.CacheDir, "official-index-proof.json"), old)
	if s := cachedSerial(t, r); s != 2 {
		t.Fatalf("older proof overwrote the newer: serial %d", s)
	}
}

// SEC-B-01: a revocation (verified: false in a newer signed index) applies once the cached
// proof is older than maxProofAge, without waiting for its signature to expire.
func TestProvenanceAppliesRevocationAfterMaxAge(t *testing.T) {
	o := newOfficialRegistry(t)
	c := strings.Repeat("3", 40)
	trusted := o.publish(guardIndex("1.0.0", c, true), 1)
	o.publish(guardIndex("1.0.0", c, false), 2)
	r := guardResolver(t, "1.0.0", c)
	trusted.Fetched = time.Now().Add(-2 * maxProofAge)
	saveIndexProof(filepath.Join(r.CacheDir, "official-index-proof.json"), trusted)
	if src, verified := r.Provenance("cicord/guard"); src != "cicord" || verified {
		t.Fatalf("revoked publisher kept its identity: src=%q verified=%v", src, verified)
	}
}

// The cached proof remains the offline fallback: when the registry cannot be read, a
// stale but valid proof that binds the lock still gives the identity.
func TestProvenanceOfflineFallback(t *testing.T) {
	o := newOfficialRegistry(t)
	c := strings.Repeat("4", 40)
	proof := o.publish(guardIndex("1.0.0", c, true), 1)
	r := guardResolver(t, "1.0.0", c)
	proof.Fetched = time.Now().Add(-2 * maxProofAge)
	saveIndexProof(filepath.Join(r.CacheDir, "official-index-proof.json"), proof)
	if err := os.RemoveAll(filepath.Join(o.url, ".git")); err != nil { // offline: not a repository any more
		t.Fatal(err)
	}
	if src, verified := r.Provenance("cicord/guard"); src != "cicord" || !verified {
		t.Fatalf("offline: src=%q verified=%v", src, verified)
	}
}
