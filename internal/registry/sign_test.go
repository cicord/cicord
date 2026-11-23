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
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/state/sqlite"
)

// useOfficial makes urls the official registry, trusting a fresh test key, with a fresh
// in-memory serial store (all restored at cleanup). Tests using it must not run in parallel.
func useOfficial(t *testing.T, urls ...string) ed25519.PrivateKey {
	t.Helper()
	t.Setenv(DevOfficialRegistryEnv, "")
	oldURLs, oldKeys, oldDev := OfficialURLs, OfficialKeys, devOfficialKey
	restore := UseSerialStore(&memorySerials{m: map[string]uint64{}})
	t.Cleanup(func() { OfficialURLs, OfficialKeys, devOfficialKey = oldURLs, oldKeys, oldDev; restore() })
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	OfficialURLs, OfficialKeys, devOfficialKey = urls, []string{EncodePublicKey(pub)}, ""
	return priv
}

func envFor(url string, serial uint64) Envelope {
	return Envelope{Registry: url, Serial: serial, Expires: time.Now().Add(24 * time.Hour)}
}

func mustSign(t *testing.T, index string, env Envelope, keys ...ed25519.PrivateKey) string {
	t.Helper()
	sig, err := SignIndex([]byte(index), env, keys...)
	if err != nil {
		t.Fatal(err)
	}
	return string(sig)
}

const regURL = "https://registry.codi.example/official"

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
	sig, err := SignIndex(index, envFor(regURL+".git/", 7), priv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sig), "registry: "+regURL+"\n") || !strings.Contains(string(sig), "serial: 7\n") {
		t.Errorf("envelope:\n%s", sig)
	}
	trusted := []string{EncodePublicKey(pub)}
	opts := VerifyOptions{Registries: []string{regURL}}
	env, id, err := VerifyIndex(index, sig, trusted, opts)
	if err != nil || id != KeyID(pub) || env.Serial != 7 || env.Registry != regURL {
		t.Fatalf("VerifyIndex = %+v %q, %v", env, id, err)
	}
	if e, err := ParseEnvelope(sig); err != nil || e.Serial != 7 {
		t.Errorf("ParseEnvelope = %+v, %v", e, err)
	}

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherPub := EncodePublicKey(other.Public().(ed25519.PublicKey))
	tampered := append([]byte{}, index...)
	tampered[len(tampered)-2] = 'X'
	expired := []byte(mustSign(t, string(index), Envelope{Registry: regURL, Serial: 8, Expires: time.Now().Add(-time.Minute)}, priv))
	reSerial := []byte(strings.Replace(string(sig), "serial: 7", "serial: 9", 1))
	reURL := []byte(strings.Replace(string(sig), "registry: "+regURL, "registry: https://evil.example/r", 1))
	unknown := []byte(strings.Replace(string(sig), "serial: 7\n", "serial: 7\nnote: hi\n", 1))
	missing := []byte(strings.Replace(string(sig), "serial: 7\n", "", 1))
	repeated := []byte(strings.Replace(string(sig), "serial: 7\n", "serial: 7\nserial: 7\n", 1))
	cases := map[string]struct {
		index, sig []byte
		trusted    []string
		opts       VerifyOptions
	}{
		"tampered index":          {tampered, sig, trusted, opts},
		"one more space":          {append(append([]byte{}, index...), ' '), sig, trusted, opts},
		"no signature":            {index, nil, trusted, opts},
		"no trusted key":          {index, sig, nil, opts},
		"signed by another key":   {index, []byte(mustSign(t, string(index), envFor(regURL, 7), other)), trusted, opts},
		"untrusted key only":      {index, sig, []string{otherPub}, opts},
		"garbage":                 {index, []byte("ed25519 zz !!!\nnot a line\n"), trusted, opts},
		"right key id, wrong sig": {index, []byte(strings.SplitAfter(string(sig), "\ned25519 ")[0] + KeyID(pub) + " " + strings.Repeat("A", 86) + "==\n"), trusted, opts},
		"bad embedded key":        {index, sig, []string{"not-a-key"}, opts},
		"wrong registry URL":      {index, sig, trusted, VerifyOptions{Registries: []string{"https://registry.codi.example/other"}}},
		"expired":                 {index, expired, trusted, opts},
		"edited serial":           {index, reSerial, trusted, opts},
		"edited registry":         {index, reURL, trusted, VerifyOptions{}},
		"unknown field":           {index, unknown, trusted, opts},
		"missing field":           {index, missing, trusted, opts},
		"repeated field":          {index, repeated, trusted, opts},
	}
	for name, c := range cases {
		if _, _, err := VerifyIndex(c.index, c.sig, c.trusted, c.opts); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := VerifyIndex(index, sig, trusted, VerifyOptions{Registries: []string{regURL}, Now: time.Now().Add(48 * time.Hour)}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expiry against Now: %v", err)
	}

	// Rotation: an index signed by the old and the new key verifies with either.
	both, _ := SignIndex(index, envFor(regURL, 10), other, priv)
	for _, k := range [][]string{trusted, {otherPub}, {"junk", otherPub}} {
		if _, _, err := VerifyIndex(index, both, k, opts); err != nil {
			t.Errorf("rotation with %v: %v", k, err)
		}
	}
	for _, e := range []Envelope{{Registry: "", Serial: 1, Expires: time.Now()}, {Registry: regURL, Serial: 0, Expires: time.Now()},
		{Registry: regURL, Serial: 1}, {Registry: "https://tok@x.example/r", Serial: 1, Expires: time.Now()}} {
		if _, err := SignIndex(index, e, priv); err == nil {
			t.Errorf("SignIndex(%+v) accepted", e)
		}
	}
}

func TestOfficialIndexMustBeSigned(t *testing.T) {
	ctx := context.Background()
	plugin := initRepo(t, map[string]string{"plugin.yaml": manifest, "src/index.ts": "export default {}"}, "v1.2.0")
	index := "plugins:\n  hello:\n    repo: " + plugin + "\n    publisher: CODI\n    verified: true\n"

	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	unsigned := initRepo(t, map[string]string{"index.yaml": index})
	key := useOfficial(t, regURL)
	good := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, envFor(regURL, 5), key)})
	OfficialURLs = []string{regURL, good, unsigned}
	idx, err := FetchIndex(ctx, good)
	if err != nil || idx.SignedBy != KeyID(key.Public().(ed25519.PublicKey)) || !idx.Plugins["hello"].Verified {
		t.Fatalf("signed official index: %+v, %v", idx, err)
	}

	// Fails closed: unsigned, unknown key, stale signature, another registry, expired.
	repos := map[string]string{
		"unsigned":    unsigned,
		"unknown key": initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, envFor(regURL, 6), stranger)}),
		"stale":       initRepo(t, map[string]string{"index.yaml": index + "  evil:\n    repo: " + plugin + "\n", "index.yaml.sig": mustSign(t, index, envFor(regURL, 6), key)}),
		"other registry": initRepo(t, map[string]string{"index.yaml": index,
			"index.yaml.sig": mustSign(t, index, envFor("https://staging.codi.example/registry", 6), key)}),
		"expired": initRepo(t, map[string]string{"index.yaml": index,
			"index.yaml.sig": mustSign(t, index, Envelope{Registry: regURL, Serial: 6, Expires: time.Now().Add(-time.Hour)}, key)}),
	}
	for name, url := range repos {
		OfficialURLs = []string{regURL, good, url}
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

	// A signature is irrelevant for unofficial registries ("verified" is dropped).
	OfficialURLs = nil
	if idx, err := FetchIndex(ctx, unsigned); err != nil || idx.SignedBy != "" {
		t.Errorf("unofficial: %+v, %v", idx, err)
	}
	res, err := ResolveLatest(ctx, []config.Registry{{Name: "acme", URL: unsigned}}, t.TempDir(), "acme/hello", "*")
	if err != nil || res.Index.Verified {
		t.Errorf("unofficial verified flag: %+v, %v", res, err)
	}
}

func TestSerialRollback(t *testing.T) {
	ctx := context.Background()
	index := "plugins: {}\n"
	key := useOfficial(t, regURL)
	newer := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, envFor(regURL, 20), key)})
	older := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, envFor(regURL, 10), key)})
	OfficialURLs = []string{regURL, newer, older} // the same registry, served from two places

	// Trust on first use: the older index is accepted while nothing newer was seen.
	db := filepath.Join(t.TempDir(), "codi.db")
	st, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	restore := UseSerialStore(KVSerialStore(st.KV(SerialKVScope)))
	if _, err := FetchIndex(ctx, older); err != nil {
		t.Fatalf("first seen: %v", err)
	}
	if _, err := FetchIndex(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchIndex(ctx, newer); err != nil {
		t.Errorf("the same serial again: %v", err)
	}
	if _, err := FetchIndex(ctx, older); !errors.Is(err, ErrBadSignature) || !strings.Contains(err.Error(), "older") {
		t.Errorf("rollback: %v", err)
	}
	restore()
	st.Close()

	// After a restart, the persisted serial still refuses the older index.
	st, err = sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defer UseSerialStore(KVSerialStore(st.KV(SerialKVScope)))()
	if _, err := FetchIndex(ctx, older); !errors.Is(err, ErrBadSignature) {
		t.Errorf("rollback after restart: %v", err)
	}
	if _, err := FetchIndex(ctx, newer); err != nil {
		t.Errorf("newer after restart: %v", err)
	}

	// Every official URL shares one serial: an older envelope signed for another official
	// URL (alias B) is refused after a higher serial was seen through alias A.
	aliasB := "https://mirror.codi.example/official"
	OfficialURLs = append(OfficialURLs, aliasB)
	viaB := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, envFor(aliasB, 15), key)})
	OfficialURLs = append(OfficialURLs, viaB)
	if _, err := FetchIndex(ctx, viaB); !errors.Is(err, ErrBadSignature) || !strings.Contains(err.Error(), "older") {
		t.Errorf("rollback through another official URL: %v", err)
	}
	newerB := initRepo(t, map[string]string{"index.yaml": index, "index.yaml.sig": mustSign(t, index, envFor(aliasB, 25), key)})
	OfficialURLs = append(OfficialURLs, newerB)
	if _, err := FetchIndex(ctx, newerB); err != nil {
		t.Errorf("newer through alias B: %v", err)
	}
	if _, err := FetchIndex(ctx, newer); !errors.Is(err, ErrBadSignature) {
		t.Errorf("serial 20 for alias A after 25 for alias B: %v", err)
	}

	// The CLI's file store behaves the same, across runs.
	file := filepath.Join(t.TempDir(), "serials.json")
	restore = UseSerialStore(FileSerialStore(file))
	if _, err := FetchIndex(ctx, newer); err != nil {
		t.Fatal(err)
	}
	restore()
	restore = UseSerialStore(FileSerialStore(file))
	defer restore()
	if _, err := FetchIndex(ctx, older); !errors.Is(err, ErrBadSignature) {
		t.Errorf("file store after restart: %v", err)
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
	key := useOfficial(t)
	url := initRepo(t, map[string]string{"index.yaml": index})
	writeSig(t, url, mustSign(t, index, envFor(url, 1), key))
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

	// A release build ignores the development override: an environment variable cannot
	// redefine what is official.
	staging := initRepo(t, map[string]string{"index.yaml": index})
	writeSig(t, staging, mustSign(t, index, envFor(staging, 1), key))
	t.Setenv(DevOfficialRegistryEnv, staging)
	if off, _ := Official(); off.URL != url || Trust(staging) == TrustOfficial {
		t.Errorf("override honoured in a release build: %+v", off)
	}
	// A development build (a dev key compiled in) honours it; the index there must still
	// be signed for that URL by a trusted key.
	_, devKey, _ := ed25519.GenerateKey(rand.Reader)
	devOfficialKey = EncodePublicKey(devKey.Public().(ed25519.PublicKey))
	if off, _ := Official(); off.URL != staging || Trust(url) == TrustOfficial {
		t.Errorf("override in a development build: %+v", off)
	}
	if _, err := ResolveLatest(ctx, nil, t.TempDir(), "codi/hello", "*"); err != nil {
		t.Errorf("signed staging registry: %v", err)
	}
	unsigned := initRepo(t, map[string]string{"index.yaml": index})
	t.Setenv(DevOfficialRegistryEnv, unsigned)
	if _, err := ResolveLatest(ctx, nil, t.TempDir(), "codi/hello", "*"); !errors.Is(err, ErrBadSignature) {
		t.Errorf("override to an unsigned registry: %v", err)
	}
}

// writeSig commits index.yaml.sig to a test registry repository.
func writeSig(t *testing.T, dir, sig string) {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SignatureFile), []byte(sig), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	if _, err := wt.Add(SignatureFile); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("sign", &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}}); err != nil {
		t.Fatal(err)
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
