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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/npjonath/cicord/internal/config"
)

// Signed official index (ADR-0011, "Update (2026-11-23)").
//
// The official registry's index.yaml comes with index.yaml.sig: a signature envelope that
// binds the registry URL, a monotonically increasing serial, an expiry and the SHA-256 of
// the exact bytes of index.yaml, followed by one ed25519 signature of that envelope per
// signing key:
//
//	# Signatures of index.yaml (cicord registry sign). Do not edit.
//	format: cicord-registry-signature-v1
//	registry: https://github.com/cicord-dev/registry
//	serial: 1764000000
//	expires: 2026-12-23T00:00:00Z
//	index-sha256: 9f86d081…
//	ed25519 <key-id> <base64 signature>
//
// The signed message is the canonical envelope (signedMessage). The runtime embeds the
// official public keys (OfficialKeys, a list for key rotation). An index read from an
// official URL is only used when a signature by an official key verifies, the envelope
// names an official registry URL, has not expired, and its serial is not lower than the
// highest serial already seen for that registry (SerialStore: no replay of an older
// index). Anything else is ErrBadSignature, never a fallback to an unofficial reading.

const (
	// IndexFile is the registry index at the root of a registry repository.
	IndexFile = "index.yaml"
	// SignatureFile holds the signature envelope of IndexFile.
	SignatureFile = "index.yaml.sig"
	// SignatureFormat is the envelope version.
	SignatureFormat = "cicord-registry-signature-v1"
	sigAlgo         = "ed25519"
	// maxSignatureFile bounds what is read from a registry.
	maxSignatureFile = 64 << 10
)

// OfficialKeys are the public keys (base64 of the 32-byte ed25519 key, as printed by
// `cicord registry keygen`) whose signatures make an index official.
//
// TODO(owner): embed the production key once the official registry repository and its
// signing key exist. While empty, no index can verify, so an official URL (if any) is
// an error source.
var OfficialKeys = []string{}

// devOfficialKey is an extra trusted key for development builds only, set at build time:
//
//	go build -ldflags "-X github.com/npjonath/cicord/internal/registry.devOfficialKey=<base64>"
//
// Release builds never set it. It is also what makes a build a development build for
// DevOfficialRegistryEnv. It cannot be changed by cicord.yaml, the API or the environment.
var devOfficialKey = ""

// DevOfficialRegistryEnv overrides the official registry URL in development builds
// (devOfficialKey set) only; a release build ignores it, so an environment variable can
// never redefine what is official in production. Even then, the index there must be
// signed (for that URL) by a trusted key.
const DevOfficialRegistryEnv = "CICORD_DEV_OFFICIAL_REGISTRY"

// ErrBadSignature: an official index without a valid, current signature by an official key.
var ErrBadSignature = errors.New("the official registry index is not validly signed by an official key")

var devWarnOnce sync.Once

// officialURLs is OfficialURLs, or the development override.
func officialURLs() []string {
	if devOfficialKey == "" {
		return OfficialURLs // release build: the environment cannot change it
	}
	if u := strings.TrimSpace(os.Getenv(DevOfficialRegistryEnv)); u != "" {
		devWarnOnce.Do(func() {
			slog.Warn("development build: official plugin registry overridden", "env", DevOfficialRegistryEnv, "url", Redact(u))
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

// Official is the implicit official remote registry, named "cicord" (config.OfficialRegistryName):
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
// cicord.yaml cannot declare a registry named "cicord" (config validation), so there is no
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

// ───────────────────────────────────────────────────────────── keys

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

const privateKeyHeader = "# cicord registry signing key (ed25519 seed, base64). Keep it secret."

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
		return nil, errors.New("invalid signing key (expected the base64 ed25519 seed written by `cicord registry keygen`)")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// ───────────────────────────────────────────────────────────── envelope

// Envelope is what a signature covers, besides the index bytes (through their hash).
type Envelope struct {
	// Registry is the normalized URL of the registry the index is for.
	Registry string
	// Serial increases with every signature; a runtime refuses a lower serial than the
	// highest it has seen for that registry.
	Serial uint64
	// Expires bounds how long this signature is valid (re-sign before).
	Expires time.Time
	// IndexSHA256 is the hex SHA-256 of index.yaml.
	IndexSHA256 string
}

func indexHash(index []byte) string {
	sum := sha256.Sum256(index)
	return hex.EncodeToString(sum[:])
}

// signedMessage is the canonical form of an envelope: the exact bytes that are signed.
func signedMessage(e Envelope) []byte {
	return fmt.Appendf(nil, "%s\nregistry: %s\nserial: %d\nexpires: %s\nindex-sha256: %s\n",
		SignatureFormat, e.Registry, e.Serial, e.Expires.UTC().Format(time.RFC3339), e.IndexSHA256)
}

// SignIndex returns the content of index.yaml.sig for index, signed by every key. env's
// Registry is normalized, IndexSHA256 computed.
func SignIndex(index []byte, env Envelope, keys ...ed25519.PrivateKey) ([]byte, error) {
	if len(keys) == 0 {
		return nil, errors.New("no signing key")
	}
	env.Registry = config.NormalizeRegistryURL(env.Registry)
	if env.Registry == "" || strings.ContainsAny(env.Registry, " \n\r\t") {
		return nil, errors.New("the registry URL to sign for is required (no spaces)")
	}
	if HasUserinfo(env.Registry) {
		return nil, errors.New("the registry URL must not contain credentials")
	}
	if env.Serial == 0 {
		return nil, errors.New("the serial must be positive")
	}
	if env.Expires.IsZero() {
		return nil, errors.New("an expiry is required")
	}
	env.Expires = env.Expires.UTC().Truncate(time.Second)
	env.IndexSHA256 = indexHash(index)
	msg := signedMessage(env)
	var b bytes.Buffer
	b.WriteString("# Signatures of index.yaml (cicord registry sign). Do not edit.\n")
	fmt.Fprintf(&b, "format: %s\nregistry: %s\nserial: %d\nexpires: %s\nindex-sha256: %s\n",
		SignatureFormat, env.Registry, env.Serial, env.Expires.Format(time.RFC3339), env.IndexSHA256)
	for _, k := range keys {
		pub := k.Public().(ed25519.PublicKey)
		fmt.Fprintf(&b, "%s %s %s\n", sigAlgo, KeyID(pub), base64.StdEncoding.EncodeToString(ed25519.Sign(k, msg)))
	}
	return b.Bytes(), nil
}

type sigLine struct{ keyID, sig string }

// ParseEnvelope reads the envelope of a signature file without verifying it (e.g. to
// pick the next serial). Unknown or repeated header fields are refused.
func ParseEnvelope(sig []byte) (Envelope, error) {
	e, _, err := parseSignatureFile(sig)
	return e, err
}

func parseSignatureFile(sig []byte) (Envelope, []sigLine, error) {
	var e Envelope
	var lines []sigLine
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(sig))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if f := strings.Fields(line); len(f) == 3 && f[0] == sigAlgo {
			lines = append(lines, sigLine{f[1], f[2]})
			continue
		}
		k, v, ok := strings.Cut(line, ": ")
		if !ok || seen[k] {
			return e, nil, fmt.Errorf("malformed or repeated line %q", line)
		}
		seen[k] = true
		v = strings.TrimSpace(v)
		switch k {
		case "format":
			if v != SignatureFormat {
				return e, nil, fmt.Errorf("unsupported signature format %q", v)
			}
		case "registry":
			e.Registry = v
		case "serial":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil || n == 0 {
				return e, nil, fmt.Errorf("invalid serial %q", v)
			}
			e.Serial = n
		case "expires":
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return e, nil, fmt.Errorf("invalid expiry %q", v)
			}
			e.Expires = t
		case "index-sha256":
			e.IndexSHA256 = v
		default:
			return e, nil, fmt.Errorf("unknown field %q", k)
		}
	}
	for _, k := range []string{"format", "registry", "serial", "expires", "index-sha256"} {
		if !seen[k] {
			return e, nil, fmt.Errorf("missing field %q", k)
		}
	}
	return e, lines, nil
}

// VerifyOptions are the checks of VerifyIndex besides the signature itself.
type VerifyOptions struct {
	// Registries are the URLs the envelope may name (normalized before comparison);
	// empty: any (only for `cicord registry verify` on a checkout).
	Registries []string
	// Now is the time the expiry is checked against (zero: time.Now()).
	Now time.Time
}

// VerifyIndex checks that sig is a current signature envelope of index by one of the
// trusted keys and returns the envelope and the key's ID. It fails closed: no trusted
// key, no or malformed envelope, another index hash, a registry that is not one of
// opts.Registries, an expired envelope, or only unknown/invalid signatures are all
// ErrBadSignature. The serial (anti-rollback) is checked by the caller (checkSerial).
func VerifyIndex(index, sig []byte, trusted []string, opts VerifyOptions) (Envelope, string, error) {
	bad := func(format string, a ...any) (Envelope, string, error) {
		return Envelope{}, "", fmt.Errorf("%w (%s)", ErrBadSignature, fmt.Sprintf(format, a...))
	}
	if len(trusted) == 0 {
		return bad("this build embeds no official public key")
	}
	if len(sig) == 0 {
		return bad("no %s", SignatureFile)
	}
	if len(sig) > maxSignatureFile {
		return bad("%s is too large", SignatureFile)
	}
	env, lines, err := parseSignatureFile(sig)
	if err != nil {
		return bad("%s: %v", SignatureFile, err)
	}
	keys := map[string]ed25519.PublicKey{}
	for _, s := range trusted {
		pub, err := ParsePublicKey(s)
		if err != nil {
			continue // a bad embedded key is a build error caught by tests; never trust it
		}
		keys[KeyID(pub)] = pub
	}
	msg := signedMessage(env)
	keyID := ""
	for _, l := range lines {
		pub, ok := keys[l.keyID]
		if !ok {
			continue // signed by a key this build does not trust (rotation)
		}
		s, err := base64.StdEncoding.DecodeString(l.sig)
		if err != nil || len(s) != ed25519.SignatureSize {
			continue
		}
		if ed25519.Verify(pub, msg, s) {
			keyID = l.keyID
			break
		}
	}
	// The envelope is only trusted once a signature over it verified.
	switch {
	case keyID == "":
		return bad("no valid signature by a trusted key in %s", SignatureFile)
	case env.IndexSHA256 != indexHash(index):
		return bad("the signature is for another version of index.yaml")
	case len(opts.Registries) > 0 && !containsURL(opts.Registries, env.Registry):
		return bad("the signature is for the registry %s, not this one", Redact(env.Registry))
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !now.Before(env.Expires) {
		return bad("the signature expired on %s", env.Expires.UTC().Format(time.RFC3339))
	}
	return env, keyID, nil
}

func containsURL(urls []string, u string) bool {
	n := config.NormalizeRegistryURL(u)
	for _, x := range urls {
		if config.NormalizeRegistryURL(x) == n {
			return true
		}
	}
	return false
}
