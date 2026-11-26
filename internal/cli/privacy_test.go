package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/cicord/cicord/internal/state/sqlite"
)

func TestPrivacyIdentitySurvivesRestartAndRefusesWrongKey(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	signer, err := privacySigner(ctx, dir, store)
	if err != nil {
		t.Fatal(err)
	}
	digest := signer.PrivacyDigest("erasure", "server", "1234")
	if fi, err := os.Stat(filepath.Join(dir, "privacy.key")); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatal("key permissions", err)
	}
	// Portal signing keys rotate independently.
	if err := os.WriteFile(filepath.Join(dir, "link.key"), []byte("another secret"), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err = privacySigner(ctx, dir, store)
	if err != nil || signer.PrivacyDigest("erasure", "server", "1234") != digest {
		t.Fatal(err)
	}
	t.Setenv("CICORD_PRIVACY_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := privacySigner(ctx, dir, store); err == nil {
		t.Fatal("accepted mismatched journal key")
	}
	if _, err := privacySigner(ctx, t.TempDir(), store); err == nil {
		t.Fatal("accepted mismatched database key")
	}
}
