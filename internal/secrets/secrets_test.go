package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/state/sqlite"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSetGetRotateDelete(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := Open(ctx, st, newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "DISCORD_TOKEN_CICORD", "tok-1"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(ctx, "DISCORD_TOKEN_CICORD"); err != nil || v != "tok-1" {
		t.Fatalf("get = %q, %v", v, err)
	}
	// Ciphertext at rest: the value never appears in the KV.
	raw, _ := st.KV(Scope).Get(ctx, "DISCORD_TOKEN_CICORD")
	if bytes.Contains(raw, []byte("tok-1")) {
		t.Fatal("plaintext stored")
	}
	if err := s.Set(ctx, "DISCORD_TOKEN_CICORD", "tok-2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get(ctx, "DISCORD_TOKEN_CICORD"); v != "tok-2" {
		t.Fatalf("rotated value = %q", v)
	}
	ms, err := s.List(ctx)
	if err != nil || len(ms) != 1 || ms[0].Name != "DISCORD_TOKEN_CICORD" || ms[0].UpdatedAt.Before(ms[0].CreatedAt) {
		t.Fatalf("list = %+v, %v", ms, err)
	}
	if err := s.Delete(ctx, "DISCORD_TOKEN_CICORD"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "DISCORD_TOKEN_CICORD"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := s.Set(ctx, "bad name", "x"); err == nil {
		t.Fatal("invalid name accepted")
	}
	if err := s.Set(ctx, "EMPTY", ""); err == nil {
		t.Fatal("empty value accepted")
	}
}

func TestWrongKeyAndKeyRotation(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(":memory:")
	defer st.Close()
	k1 := newKey(t)
	s, err := Open(ctx, st, k1)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Set(ctx, "A", "alpha")
	if _, err := Open(ctx, st, newKey(t)); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
	k2 := newKey(t)
	if err := s.RotateKey(ctx, k2); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, st, k1); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("old key still works: %v", err)
	}
	s2, err := Open(ctx, st, k2)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s2.Get(ctx, "A"); v != "alpha" {
		t.Fatalf("after rotation = %q", v)
	}
}

func TestCiphertextBoundToName(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(":memory:")
	defer st.Close()
	s, _ := Open(ctx, st, newKey(t))
	_ = s.Set(ctx, "A", "alpha")
	raw, _ := st.KV(Scope).Get(ctx, "A")
	_ = st.KV(Scope).Set(ctx, "B", raw, 0) // moved ciphertext
	if _, err := s.Get(ctx, "B"); err == nil {
		t.Fatal("ciphertext accepted under another name")
	}
}

func TestLoadKey(t *testing.T) {
	dir := t.TempDir()
	none := func(string) (string, bool) { return "", false }
	k, src, err := LoadKey(dir, none)
	if err != nil || len(k) != 32 || src != filepath.Join(dir, KeyFile) {
		t.Fatalf("generate: %v %q", err, src)
	}
	fi, _ := os.Stat(filepath.Join(dir, KeyFile))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", fi.Mode().Perm())
	}
	k2, _, _ := LoadKey(dir, none)
	if !bytes.Equal(k, k2) {
		t.Fatal("key not persisted")
	}
	raw := newKey(t)
	for _, v := range []string{base64.StdEncoding.EncodeToString(raw), hex.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(raw)} {
		got, src, err := LoadKey(dir, func(string) (string, bool) { return v, true })
		if err != nil || !bytes.Equal(got, raw) || src != KeyEnv {
			t.Fatalf("env %q: %v", v, err)
		}
	}
	if _, _, err := LoadKey(dir, func(string) (string, bool) { return base64.StdEncoding.EncodeToString([]byte("short")), true }); err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("short key: %v", err)
	}
}

func TestResolverEnvFirst(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(":memory:")
	defer st.Close()
	s, _ := Open(ctx, st, newKey(t))
	_ = s.Set(ctx, "TOK", "stored")
	_ = s.Set(ctx, "ONLY", "stored-only")
	env := map[string]string{"TOK": "from-env"}
	r := Resolver(s, func(n string) (string, bool) { v, ok := env[n]; return v, ok })
	if v, _ := r("TOK"); v != "from-env" {
		t.Fatalf("env precedence: %q", v)
	}
	if v, _ := r("ONLY"); v != "stored-only" {
		t.Fatalf("store fallback: %q", v)
	}
	if _, ok := r("MISSING"); ok {
		t.Fatal("missing resolved")
	}
}
