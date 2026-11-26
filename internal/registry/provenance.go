package registry

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	var proof indexProof
	var idx *Index
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if data, err := readIndexProof(cached); err == nil && json.Unmarshal(data, &proof) == nil {
		if err := checkSerial(ctx, func() (Envelope, error) {
			e, _, err := VerifyIndex([]byte(proof.Index), []byte(proof.Signature), officialKeys(), VerifyOptions{Registries: officialURLs()})
			return e, err
		}); err == nil {
			idx, _ = ParseIndex([]byte(proof.Index))
		}
	}
	if idx == nil {
		idx, err = FetchIndex(ctx, official.URL)
		if err != nil || idx.SignedBy == "" {
			return "", false
		}
		saveIndexProof(cached, idx.proof)
	}
	e, ok := idx.Plugins[name]
	if !ok || e.Commit == "" || e.Commit != le.Commit || e.Path != le.Path || e.Version != le.Version || e.Repo != le.Repo {
		return "", false
	}
	return reg, e.Verified
}

type indexProof struct{ Index, Signature string }

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
