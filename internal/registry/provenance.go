package registry

import (
	"context"
	"encoding/json"
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
	if data, err := os.ReadFile(cached); err == nil && json.Unmarshal(data, &proof) == nil {
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

func saveIndexProof(path string, proof indexProof) {
	if proof.Signature == "" {
		return
	}
	b, err := json.Marshal(proof)
	if err == nil {
		_ = os.WriteFile(path, b, 0600)
	}
}
