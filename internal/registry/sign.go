package registry

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/npjonath/codi/internal/config"
)

// Signed official index (ADR-0011, "Update (2026-11-23)").
//
// The official registry's index.yaml is signed with ed25519: index.yaml.sig, next to it,
// holds one detached signature per signing key over the exact bytes of index.yaml. The
// runtime embeds the official public keys (OfficialKeys, a list for key rotation: sign
// with the old and the new key during a rotation, then drop the old one). An index fetched
// from an official URL is only used when one of its signatures verifies with an official
// key: a missing or invalid signature is an error (ErrBadSignature), never a fallback to
// an unsigned, unofficial reading.

const (
	// IndexFile is the registry index at the root of a registry repository.
	IndexFile = "index.yaml"
	// SignatureFile holds the detached signatures of IndexFile.
	SignatureFile = "index.yaml.sig"
	// sigAlgo prefixes every signature line.
	sigAlgo = "ed25519"
	// maxSignatureFile bounds what is read from a registry.
	maxSignatureFile = 64 << 10
)

// OfficialKeys are the public keys (base64 of the 32-byte ed25519 key, as printed by
// `codi registry keygen`) whose signatures make an index official.
//
// TODO(owner): embed the production key once the official registry repository and its
// signing key exist. While empty, no index can verify, so an official URL (if any) is
// an error source.
var OfficialKeys = []string{}

// devOfficialKey is an extra trusted key for development builds only, set at build time:
//
//	go build -ldflags "-X github.com/npjonath/codi/internal/registry.devOfficialKey=<base64>"
//
// Release builds never set it. It cannot be changed by codi.yaml, the API or the
// environment of a running runtime.
var devOfficialKey = ""

// DevOfficialRegistryEnv overrides the official registry URL. Development only: the
// index there must still be signed by an official key (OfficialKeys, or devOfficialKey
// in a development build), so it cannot make an arbitrary registry official.
const DevOfficialRegistryEnv = "CODI_DEV_OFFICIAL_REGISTRY"

// ErrBadSignature: an official index without a valid signature by an official key.
var ErrBadSignature = errors.New("the official registry index is not signed by an official key")

var devWarnOnce sync.Once

// officialURLs is OfficialURLs, or the development override.
func officialURLs() []string {
	if u := strings.TrimSpace(os.Getenv(DevOfficialRegistryEnv)); u != "" {
		devWarnOnce.Do(func() {
			slog.Warn("development override of the official plugin registry", "env", DevOfficialRegistryEnv, "url", Redact(u))
		})
		return []string{u}
	}
	return OfficialURLs
}

// officialKeys is OfficialKeys plus the development key.
func officialKeys() []string {
	if devOfficialKey != "" {
		return append([]string{devOfficialKey}, OfficialKeys...)
	}
	return OfficialKeys
}

// Official is the implicit official remote registry, named "codi" (config.OfficialRegistryName):
// the first official URL (the others are aliases, e.g. an old address). ok is false when
// there is none: the official plugins are then only the bundled ones.
func Official() (config.Registry, bool) {
	urls := officialURLs()
	if len(urls) == 0 {
		return config.Registry{}, false
	}
	return config.Registry{Name: config.OfficialRegistryName, URL: urls[0]}, true
}

// WithOfficial returns regs plus the implicit official registry (when there is one).
// codi.yaml cannot declare a registry named "codi" (config validation), so there is no
// conflict.
func WithOfficial(regs []config.Registry) []config.Registry {
	off, ok := Official()
	if !ok {
		return regs
	}
	out := make([]config.Registry, 0, len(regs)+1)
	for _, r := range regs {
		if r.Name != off.Name {
			out = append(out, r)
		}
	}
	return append(out, off)
}

// KeyID is the short identifier of a public key in signature files (16 hex digits of its
// SHA-256).
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// EncodePublicKey is the textual form of a public key (what OfficialKeys holds).
func EncodePublicKey(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

// ParsePublicKey decodes EncodePublicKey.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid ed25519 public key (expected base64 of 32 bytes)")
	}
	return ed25519.PublicKey(b), nil
}

const privateKeyHeader = "# codi registry signing key (ed25519 seed, base64). Keep it secret."

// GenerateSigningKey returns a new key pair: the public key and the private key file
// content (EncodePrivateKey).
func GenerateSigningKey() (ed25519.PublicKey, []byte, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return pub, EncodePrivateKey(priv), nil
}

// EncodePrivateKey is the private key file format: a comment line and the base64 seed.
func EncodePrivateKey(priv ed25519.PrivateKey) []byte {
	return []byte(privateKeyHeader + "\n" + base64.StdEncoding.EncodeToString(priv.Seed()) + "\n")
}

// ParsePrivateKey decodes EncodePrivateKey (comment lines are ignored; the bare base64
// seed is accepted, e.g. from an environment variable).
func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	var b64 string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if b64 != "" {
			return nil, errors.New("invalid signing key: more than one key line")
		}
		b64 = line
	}
	seed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid signing key (expected the base64 ed25519 seed written by `codi registry keygen`)")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// SignIndex returns the content of index.yaml.sig: one signature of index per key.
func SignIndex(index []byte, keys ...ed25519.PrivateKey) ([]byte, error) {
	if len(keys) == 0 {
		return nil, errors.New("no signing key")
	}
	var b bytes.Buffer
	b.WriteString("# Signatures of index.yaml (codi registry sign). Do not edit.\n")
	for _, k := range keys {
		pub := k.Public().(ed25519.PublicKey)
		fmt.Fprintf(&b, "%s %s %s\n", sigAlgo, KeyID(pub), base64.StdEncoding.EncodeToString(ed25519.Sign(k, index)))
	}
	return b.Bytes(), nil
}

// VerifyIndex checks that sig holds a valid signature of index by one of the trusted keys
// and returns that key's ID. It fails closed: no trusted key, no signature, a malformed
// file or only unknown/invalid signatures are all ErrBadSignature.
func VerifyIndex(index, sig []byte, trusted []string) (string, error) {
	if len(trusted) == 0 {
		return "", fmt.Errorf("%w (this build embeds no official public key)", ErrBadSignature)
	}
	if len(sig) == 0 {
		return "", fmt.Errorf("%w (no %s)", ErrBadSignature, SignatureFile)
	}
	if len(sig) > maxSignatureFile {
		return "", fmt.Errorf("%w (%s is too large)", ErrBadSignature, SignatureFile)
	}
	keys := map[string]ed25519.PublicKey{}
	for _, s := range trusted {
		pub, err := ParsePublicKey(s)
		if err != nil {
			continue // a bad embedded key is a build error caught by tests; never trust it
		}
		keys[KeyID(pub)] = pub
	}
	sc := bufio.NewScanner(bytes.NewReader(sig))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != sigAlgo {
			continue
		}
		pub, ok := keys[f[1]]
		if !ok {
			continue // signed by a key this build does not trust (rotation)
		}
		s, err := base64.StdEncoding.DecodeString(f[2])
		if err != nil || len(s) != ed25519.SignatureSize {
			continue
		}
		if ed25519.Verify(pub, index, s) {
			return f[1], nil
		}
	}
	return "", fmt.Errorf("%w (no valid signature by a trusted key in %s)", ErrBadSignature, SignatureFile)
}
