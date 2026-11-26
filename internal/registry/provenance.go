package registry

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Provenance returns the reserved cicord source only when the signed registry entry
// binds this installation's repository, path, version and commit. An editable lockfile,
// registry alias, or manifest with an official-looking name cannot confer that identity.
func (r *Resolver) Provenance(source string) (string, bool) {
	reg, name, err := CheckSource(source)
	if err != nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	official, ok := Official()
	if !ok || r.lock == nil {
		return "", false
	}
	url := ""
	for _, v := range WithOfficial(projectRegistries(r.RepoRoot)) {
		if reg == v.Name {
			url = v.URL
			break
		}
	}
	if Trust(url) != TrustOfficial {
		return reg, false
	}
	le, ok := r.lock.Plugins[source]
	if !ok {
		return "", false
	}
	cached := filepath.Join(r.CacheDir, "official-index-proof.json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	identity := func(idx *Index) (string, bool, bool) {
		e, ok := idx.Plugins[name]
		if !ok || e.Commit == "" || e.Commit != le.Commit || e.Path != le.Path || e.Version != le.Version || e.Repo != le.Repo {
			return "", false, false
		}
		return reg, e.Verified, true
	}
	// The cached proof is used as is only while it is recent and binds this lock entry. A
	// lock bumped to a newer release, or a proof older than maxProofAge (so that a
	// publisher revoked with verified: false loses its identity promptly), refetches the
	// signed index. The cached proof stays the offline fallback: it is still verified
	// (signature, expiry and serial) on every read.
	cachedIdx, fetchedAt := cachedIndexProof(ctx, cached)
	if cachedIdx != nil && time.Since(fetchedAt) < maxProofAge && !fetchedAt.After(time.Now().Add(time.Minute)) {
		if src, verified, ok := identity(cachedIdx); ok {
			return src, verified
		}
	}
	if proofFetchDue(cached) {
		if idx, err := FetchIndex(ctx, official.URL); err == nil && idx.SignedBy != "" {
			saveIndexProof(cached, idx.proof)
			src, verified, _ := identity(idx)
			return src, verified
		}
	}
	if cachedIdx != nil {
		src, verified, _ := identity(cachedIdx)
		return src, verified
	}
	return "", false
}

// maxProofAge bounds how long a cached official index proof is trusted without a refetch.
const maxProofAge = time.Hour

// proofRetry spaces the refetches of one cache's index proof (a lock entry the signed index
// does not bind, or an offline runtime, would otherwise fetch on every load).
const proofRetry = 5 * time.Minute

var (
	proofFetchMu sync.Mutex
	proofFetches = map[string]time.Time{}
)

func proofFetchDue(path string) bool {
	proofFetchMu.Lock()
	defer proofFetchMu.Unlock()
	if last, ok := proofFetches[path]; ok && time.Since(last) < proofRetry {
		return false
	}
	proofFetches[path] = time.Now()
	return true
}

// cachedIndexProof reads and verifies the cached official index proof (nil when absent or
// invalid), with the time it was fetched (zero for proofs written before it was recorded).
func cachedIndexProof(ctx context.Context, path string) (*Index, time.Time) {
	data, err := readIndexProof(path)
	if err != nil {
		return nil, time.Time{}
	}
	var proof indexProof
	if json.Unmarshal(data, &proof) != nil {
		return nil, time.Time{}
	}
	if err := checkSerial(ctx, func() (Envelope, error) {
		e, _, err := VerifyIndex([]byte(proof.Index), []byte(proof.Signature), officialKeys(), VerifyOptions{Registries: officialURLs()})
		return e, err
	}); err != nil {
		return nil, time.Time{}
	}
	idx, err := ParseIndex([]byte(proof.Index))
	if err != nil {
		return nil, time.Time{}
	}
	return idx, proof.Fetched
}

type indexProof struct {
	Index, Signature string
	// Fetched is when the index was read from the registry (FetchIndex).
	Fetched time.Time `json:",omitzero"`
}

// proofSerial is the serial of a proof's signature envelope (0 when it does not verify).
func proofSerial(p indexProof) uint64 {
	env, _, err := VerifyIndex([]byte(p.Index), []byte(p.Signature), officialKeys(), VerifyOptions{Registries: officialURLs()})
	if err != nil {
		return 0
	}
	return env.Serial
}

// Index proofs are cache metadata outside the package hash. Read only a bounded
// regular file and publish by rename, even if a previous entry was redirected.
func readIndexProof(path string) ([]byte, error) {
	const limit = 8 << 20
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	st, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid cached index proof")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(b) > limit {
		return nil, errors.New("cached proof too large")
	}
	return b, err
}
func saveIndexProof(path string, proof indexProof) {
	if proof.Signature == "" {
		return
	}
	// Never replace a newer signed index with an older one (two processes sharing the
	// cache, or a resolution racing a refresh): the serial only moves forward.
	if data, err := readIndexProof(path); err == nil {
		var cur indexProof
		if json.Unmarshal(data, &cur) == nil && proofSerial(cur) > proofSerial(proof) {
			return
		}
	}
	b, err := json.Marshal(proof)
	if err != nil {
		return
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return
	}
	defer root.Close()
	tmp := ".proof-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return
	}
	defer func() { _ = root.Remove(tmp) }()
	_, err = f.Write(b)
	closeErr := f.Close()
	if err == nil && closeErr == nil {
		_ = root.Rename(tmp, filepath.Base(path))
	}
}
