package registry

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"

	"github.com/npjonath/codi/internal/config"
)

// useOfficial makes url the official registry, trusting a fresh test key (restored at
// cleanup). Tests using it must not run in parallel.
func useOfficial(t *testing.T, url string) ed25519.PrivateKey {
	t.Helper()
	t.Setenv(DevOfficialRegistryEnv, "")
	oldURLs, oldKeys := OfficialURLs, OfficialKeys
	t.Cleanup(func() { OfficialURLs, OfficialKeys = oldURLs, oldKeys })
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	OfficialURLs, OfficialKeys = []string{url}, []string{EncodePublicKey(pub)}
	return priv
}

func mustSign(t *testing.T, index string, keys ...ed25519.PrivateKey) string {
	t.Helper()
	sig, err := SignIndex([]byte(index), keys...)
	if err != nil {
		t.Fatal(err)
	}
	return string(sig)
}

func TestSignAndVerify(t *testing.T) {
	pub, privFile, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivateKey(privFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePrivateKey([]byte(strings.Split(string(privFile), "\n")[1])); err != nil {
		t.Errorf("a bare base64 seed (env var) is accepted: %v", err)
	}
	for _, bad := range []string{"", "nope", "# only a comment\n", "AAAA\nBBBB\n"} {
		if _, err := ParsePrivateKey([]byte(bad)); err == nil {
			t.Errorf("ParsePrivateKey(%q) accepted", bad)
		}
	}
	index := []byte("plugins:\n  xp:\n    repo: https://example.org/xp.git\n    verified: true\n")
	sig, _ := SignIndex(index, priv)
	trusted := []string{EncodePublicKey(pub)}
	id, err := VerifyIndex(index, sig, trusted)
	if err != nil || id != KeyID(pub) {
		t.Fatalf("VerifyIndex = %q, %v", id, err)
	}

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherPub := EncodePublicKey(other.Public().(ed25519.PublicKey))
	tampered := append([]byte{}, index...)
	tampered[len(tampered)-2] = 'X'
	cases := map[string]struct {
		index, sig []byte
		trusted    []string
	}{
		"tampered index":          {tampered, sig, trusted},
		"one more space":          {append(append([]byte{}, index...), ' '), sig, trusted},
		"no signature":            {index, nil, trusted},
		"no trusted key":          {index, sig, nil},
		"signed by another key":   {index, mustSignB(t, index, other), trusted},
		"untrusted key only":      {index, sig, []string{otherPub}},
		"garbage":                 {index, []byte("ed25519 zz !!!\nnot a line\n"), trusted},
		"right key id, wrong sig": {index, []byte("ed25519 " + KeyID(pub) + " " + strings.Repeat("A", 86) + "==\n"), trusted},
		"bad embedded key":        {index, sig, []string{"not-a-key"}},
	}
	for name, c := range cases {
		if _, err := VerifyIndex(c.index, c.sig, c.trusted); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Rotation: an index signed by the old and the new key verifies with either.
	both, _ := SignIndex(index, other, priv)
	for _, k := range [][]string{trusted, {otherPub}, {"junk", otherPub}} {
		if _, err := VerifyIndex(index, both, k); err != nil {
			t.Errorf("rotation with %v: %v", k, err)
		}
	}
}

func mustSignB(t *testing.T, index []byte, k ed25519.PrivateKey) []byte {
	t.Helper()
	b, err := SignIndex(index, k)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOfficialIndexMustBeSigned(t *testing.T) {
	ctx := context.Background()
	plugin := initRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": "export default {}"}, "v1.2.0")
	index := "plugins:\n  hello:\n    repo: " + plugin + "\n    publisher: CODI\n    verified: true\n"

	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	unsigned := initRepo(t, map[string]string{"index.yaml": index})
	key := useOfficial(t, unsigned)
	// The same files, signed with the official key, under the official URL.
	good := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, key)})
	OfficialURLs = []string{good, unsigned}
	if Trust(good) != TrustOfficial || Trust(unsigned) != TrustOfficial {
		t.Fatal("test setup: official URLs")
	}
	idx, err := FetchIndex(ctx, good)
	if err != nil || idx.SignedBy != KeyID(key.Public().(ed25519.PublicKey)) || !idx.Plugins["hello"].Verified {
		t.Fatalf("signed official index: %+v, %v", idx, err)
	}

	// Fails closed: unsigned, signed by an unknown key, or signature of other bytes.
	unknown := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, stranger)})
	stale := initRepo(t, map[string]string{"index.yaml": index + "  evil:\n    repo: " + plugin + "\n", "index.yaml.sig": mustSign(t, index, key)})
	for name, url := range map[string]string{"unsigned": unsigned, "unknown key": unknown, "stale signature": stale} {
		OfficialURLs = []string{good, url}
		if idx, err := FetchIndex(ctx, url); !errors.Is(err, ErrBadSignature) || idx != nil {
			t.Errorf("%s: %+v, %v", name, idx, err)
		}
		// ResolveLatest (codi plugin add) goes through the same check.
		if _, err := ResolveLatest(ctx, []config.Registry{{Name: "mirror", URL: url}}, t.TempDir(), "mirror/hello", "*"); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: ResolveLatest = %v", name, err)
		}
	}
	// No official key in the build: nothing verifies.
	OfficialKeys = nil
	if _, err := FetchIndex(ctx, good); !errors.Is(err, ErrBadSignature) {
		t.Errorf("no official key: %v", err)
	}

	// A signature is irrelevant for unofficial registries ("verified" is dropped later).
	OfficialURLs = nil
	if idx, err := FetchIndex(ctx, unsigned); err != nil || idx.SignedBy != "" {
		t.Errorf("unofficial: %+v, %v", idx, err)
	}
	res, err := ResolveLatest(ctx, []config.Registry{{Name: "acme", URL: unsigned}}, t.TempDir(), "acme/hello", "*")
	if err != nil || res.Index.Verified {
		t.Errorf("unofficial verified flag: %+v, %v", res, err)
	}
}

func TestImplicitOfficialRegistry(t *testing.T) {
	ctx := context.Background()
	t.Setenv(DevOfficialRegistryEnv, "")
	if _, ok := Official(); ok {
		t.Fatal("no official registry is configured in this build yet")
	}
	if got := WithOfficial(nil); len(got) != 0 {
		t.Errorf("WithOfficial = %v", got)
	}

	plugin := initRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": "export default {}"}, "v1.2.0")
	index := "plugins:\n  hello:\n    repo: " + plugin + "\n    verified: true\n"
	placeholder := t.TempDir()
	key := useOfficial(t, placeholder)
	url := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, key)})
	OfficialURLs = []string{url, "https://old.example/registry.git"}
	off, ok := Official()
	if !ok || off.Name != config.OfficialRegistryName || off.URL != url {
		t.Fatalf("Official = %+v %v", off, ok)
	}
	if Trust("https://old.example/registry") != TrustOfficial {
		t.Error("the other official URLs are aliases")
	}
	regs := WithOfficial([]config.Registry{{Name: "acme", URL: "https://acme.example/r.git"}})
	if len(regs) != 2 || regs[1] != off {
		t.Errorf("WithOfficial = %+v", regs)
	}

	// `codi plugin add codi/hello` resolves through the official registry, with no codi.yaml entry.
	res, err := ResolveLatest(ctx, nil, t.TempDir(), "codi/hello", "*")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Index.Verified || res.Entry.Version != "1.2.0" {
		t.Errorf("resolution = %+v", res)
	}
	// The runtime resolves codi/<name> from codi.lock (no registry lookup).
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "codi.yaml"), []byte("apiVersion: codi.dev/v1alpha1\nkind: Project\nmetadata:\n  name: p\nspec: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteLock(root, &Lock{Plugins: map[string]LockEntry{"codi/hello": res.Entry}}); err != nil {
		t.Fatal(err)
	}
	if _, key, err := NewResolver(root, t.TempDir()).Resolve("codi/hello", "^1.0.0"); err != nil || key != "codi/hello@1.2.0" {
		t.Errorf("Resolve = %q, %v", key, err)
	}

	// The development override replaces the URL, never the keys: an unsigned registry
	// there is still refused.
	unsigned := initRepo(t, map[string]string{"index.yaml": index})
	t.Setenv(DevOfficialRegistryEnv, unsigned)
	if off, _ := Official(); off.URL != unsigned || Trust(url) == TrustOfficial {
		t.Errorf("override: %+v", off)
	}
	if _, err := ResolveLatest(ctx, nil, t.TempDir(), "codi/hello", "*"); !errors.Is(err, ErrBadSignature) {
		t.Errorf("override to an unsigned registry: %v", err)
	}
}

func TestTagMovedSincePublished(t *testing.T) {
	ctx := context.Background()
	plugin := initRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": "export default {}"}, "v1.2.0")
	r, err := git.PlainOpen(plugin)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := r.Head()
	good := head.Hash().String()
	other := strings.Repeat("a", 40)
	for _, c := range []struct {
		version, commit string
		ok              bool
	}{{"1.2.0", good, true}, {"1.2.0", other, false}, {"1.1.0", other, true}, {"", "", true}} {
		reg := initRepo(t, map[string]string{"index.yaml": "plugins:\n  hello:\n    repo: " + plugin +
			"\n    version: \"" + c.version + "\"\n    commit: \"" + c.commit + "\"\n"})
		_, err := ResolveLatest(ctx, []config.Registry{{Name: "acme", URL: reg}}, t.TempDir(), "acme/hello", "*")
		if (err == nil) != c.ok || (err != nil && !strings.Contains(err.Error(), "the tag moved")) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}
