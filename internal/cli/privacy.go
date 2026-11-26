package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"os"
	"path/filepath"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/state"
)

// A dedicated key survives portal and encrypted-secret key rotation. Keep it
// in the operator's key escrow, separately from the HMAC-only journal.
func privacySigner(ctx context.Context, dir string, store state.Store) (*auth.Signer, error) {
	keyfile := filepath.Join(dir, "privacy.key")
	key, err := os.ReadFile(keyfile)
	if override := os.Getenv("CICORD_PRIVACY_KEY"); override != "" {
		key = []byte(override)
		err = nil
	}
	if errors.Is(err, os.ErrNotExist) {
		if entries, e := os.ReadDir(filepath.Join(dir, "member-erasures")); e == nil && len(entries) > 0 {
			return nil, errors.New("restore privacy.key or CICORD_PRIVACY_KEY before opening the erasure journal")
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		var b [32]byte
		rand.Read(b[:])
		key = []byte(base64.StdEncoding.EncodeToString(b[:]))
		// Link an already-flushed temporary file to publish a complete key atomically.
		tmp, e := os.CreateTemp(dir, ".privacy-key-")
		if e != nil {
			return nil, e
		}
		defer os.Remove(tmp.Name())
		if _, e = tmp.Write(key); e == nil {
			e = tmp.Sync()
		}
		ce := tmp.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
		if e = os.Link(tmp.Name(), keyfile); errors.Is(e, os.ErrExist) {
			key, e = os.ReadFile(keyfile)
		}
		if e != nil {
			return nil, e
		}
	} else if err != nil {
		return nil, err
	}
	raw, err := secrets.ParseKey(string(key))
	if err != nil {
		return nil, errors.New("privacy key must encode 32 random bytes")
	}
	signer := auth.NewSigner(raw)
	check, _ := json.Marshal(signer.PrivacyDigest("key-check", "", ""))
	// Check the external journal independently when an older SQL backup predates privacy.
	identity := filepath.Join(dir, "member-erasures", "identity")
	got, journalErr := os.ReadFile(identity)
	if journalErr == nil && string(got) != string(check) {
		return nil, errors.New("privacy key differs from the erasure journal identity")
	} else if journalErr != nil && !errors.Is(journalErr, os.ErrNotExist) {
		return nil, journalErr
	}
	kv := store.KV("privacy:identity")
	k := kv.(state.AdvancedKV)
	res, err := k.Atomic(ctx, []state.KVCheck{{Key: "key"}}, []state.KVMutation{{Op: "set", Key: "key", Value: check}})
	if err != nil {
		return nil, err
	}
	if !res.OK {
		prior, err := kv.Get(ctx, "key")
		if err != nil {
			return nil, err
		}
		if string(prior) != string(check) {
			return nil, errors.New("privacy key differs from the database identity; restore the deployment's privacy.key")
		}
	}
	if journalErr == nil {
		return signer, nil
	}
	if err := os.MkdirAll(filepath.Dir(identity), 0700); err != nil {
		return nil, err
	}
	journal, err := os.OpenRoot(filepath.Join(dir, "member-erasures"))
	if err != nil {
		return nil, err
	}
	defer journal.Close()
	if err := journal.WriteFile("identity", check, 0600); err != nil {
		return nil, err
	}
	return signer, nil
}
