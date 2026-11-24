// Package secrets is the runtime secret store: bot tokens, the OAuth client secret and
// git tokens entered in the setup wizard, encrypted at rest in the state store.
//
// Design:
//   - values are sealed with AES-256-GCM (random 96-bit nonce per write); the secret
//     name is the additional authenticated data, so a ciphertext cannot be moved to
//     another name;
//   - the 32-byte key comes from CICORD_SECRET_KEY (base64 or hex of exactly 32 bytes) or
//     from a key file generated in the data dir with mode 0600 (LoadKey);
//   - a sentinel ciphertext detects a wrong key at startup (Open fails instead of
//     returning garbage later);
//   - ciphertexts live in the state KV scope "cicord:secrets" (JSON objects), so no schema
//     migration is needed;
//   - nothing in this package returns a value except Get and the Resolver used by the
//     runtime; List returns names and timestamps only.
//
// Resolution order for the runtime (Resolver): environment first, then the store, so a
// deployment can always override a stored secret with an environment variable.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/npjonath/cicord/internal/state"
)

const (
	// Scope is the state KV scope holding the ciphertexts.
	Scope = "cicord:secrets"
	// KeyEnv is the environment variable holding the encryption key.
	KeyEnv = "CICORD_SECRET_KEY"
	// KeyFile is the generated key file name in the data dir.
	KeyFile  = "secret.key"
	sentinel = "\x00sentinel"
	keySize  = 32
)

// ErrNotFound is returned by Get for an unknown secret.
var ErrNotFound = errors.New("secrets: not found")

// ErrWrongKey means the store was encrypted with another key.
var ErrWrongKey = errors.New("secrets: the encryption key does not match the stored secrets (CICORD_SECRET_KEY or " + KeyFile + " changed)")

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidName reports whether name can be used as a secret name (an environment-variable
// style identifier, like token.secretRef).
func ValidName(name string) bool { return namePattern.MatchString(name) }

// ParseKey decodes a CICORD_SECRET_KEY value: base64 (std or URL, padded or not) or hex of
// exactly 32 bytes.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == 2*keySize {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != keySize {
				return nil, fmt.Errorf("%s must encode exactly %d bytes (got %d): generate one with `openssl rand -base64 32`", KeyEnv, keySize, len(b))
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("%s must be the base64 or hex encoding of %d random bytes (e.g. `openssl rand -base64 32`)", KeyEnv, keySize)
}

// LoadKey returns the key from CICORD_SECRET_KEY (lookup) or from <dataDir>/secret.key,
// generating the file (0600) on first use. source describes where the key came from.
func LoadKey(dataDir string, lookup func(string) (string, bool)) (key []byte, source string, err error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if v, ok := lookup(KeyEnv); ok && strings.TrimSpace(v) != "" {
		k, err := ParseKey(v)
		return k, KeyEnv, err
	}
	p := filepath.Join(dataDir, KeyFile)
	if data, err := os.ReadFile(p); err == nil {
		k, err := ParseKey(string(data))
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", p, err)
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(p, 0o600)
		}
		return k, p, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	k := make([]byte, keySize)
	if _, err := rand.Read(k); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(k) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, "", werr
	}
	return k, p, nil
}

// Meta describes a stored secret (never its value).
type Meta struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type record struct {
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Store is an encrypted secret store. It is safe for concurrent use.
type Store struct {
	kv  state.KV
	mu  sync.RWMutex
	gcm cipher.AEAD
	now func() time.Time
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secrets: key must be %d bytes", keySize)
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Open opens the store kept in st with key. It fails with ErrWrongKey when existing
// secrets were sealed with another key.
func Open(ctx context.Context, st state.Store, key []byte) (*Store, error) {
	gcm, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	s := &Store{kv: st.KV(Scope), gcm: gcm, now: time.Now}
	raw, err := s.kv.Get(ctx, sentinel)
	switch {
	case errors.Is(err, state.ErrNotFound):
		if err := s.put(ctx, sentinel, "cicord", time.Time{}); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		var rec record
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("secrets: corrupt sentinel: %w", err)
		}
		if _, err := s.open(sentinel, rec); err != nil {
			return nil, ErrWrongKey
		}
	}
	return s, nil
}

func (s *Store) seal(name, value string) (nonce, ct []byte, err error) {
	nonce = make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, s.gcm.Seal(nil, nonce, []byte(value), []byte(name)), nil
}

func (s *Store) open(name string, rec record) (string, error) {
	nonce, err := base64.StdEncoding.DecodeString(rec.Nonce)
	if err != nil {
		return "", err
	}
	ct, err := base64.StdEncoding.DecodeString(rec.Ciphertext)
	if err != nil {
		return "", err
	}
	pt, err := s.gcm.Open(nil, nonce, ct, []byte(name))
	if err != nil {
		return "", fmt.Errorf("secrets: cannot decrypt %s: %w", name, ErrWrongKey)
	}
	return string(pt), nil
}

func (s *Store) put(ctx context.Context, name, value string, created time.Time) error {
	nonce, ct, err := s.seal(name, value)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if created.IsZero() {
		created = now
	}
	data, _ := json.Marshal(record{Nonce: base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct), CreatedAt: created, UpdatedAt: now})
	return s.kv.Set(ctx, name, data, 0)
}

func (s *Store) get(ctx context.Context, name string) (record, error) {
	raw, err := s.kv.Get(ctx, name)
	if errors.Is(err, state.ErrNotFound) {
		return record{}, ErrNotFound
	}
	if err != nil {
		return record{}, err
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return record{}, fmt.Errorf("secrets: corrupt record %s: %w", name, err)
	}
	return rec, nil
}

// Set stores (or rotates: replaces the value of) a secret.
func (s *Store) Set(ctx context.Context, name, value string) error {
	if !ValidName(name) {
		return fmt.Errorf("secrets: invalid name %q (letters, digits and _ only)", name)
	}
	if value == "" {
		return fmt.Errorf("secrets: empty value for %s", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var created time.Time
	if rec, err := s.get(ctx, name); err == nil {
		created = rec.CreatedAt
	}
	return s.put(ctx, name, value, created)
}

// Get returns the value of a secret (ErrNotFound when missing).
func (s *Store) Get(ctx context.Context, name string) (string, error) {
	if !ValidName(name) {
		return "", ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, err := s.get(ctx, name)
	if err != nil {
		return "", err
	}
	return s.open(name, rec)
}

// Has reports whether a secret is stored.
func (s *Store) Has(ctx context.Context, name string) bool {
	if !ValidName(name) {
		return false
	}
	_, err := s.get(ctx, name)
	return err == nil
}

// Delete removes a secret (missing secrets are not an error).
func (s *Store) Delete(ctx context.Context, name string) error {
	if !ValidName(name) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kv.Delete(ctx, name)
}

// List returns the stored secrets' metadata, sorted by name. Values are never returned.
func (s *Store) List(ctx context.Context) ([]Meta, error) {
	var out []Meta
	cursor := ""
	for {
		items, next, err := s.kv.List(ctx, "", 1000, cursor)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.Key == sentinel {
				continue
			}
			var rec record
			if json.Unmarshal(it.Value, &rec) == nil {
				out = append(out, Meta{Name: it.Key, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt})
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RotateKey re-encrypts every secret (and the sentinel) with newKey. On success the
// store uses newKey; the caller must persist it (CICORD_SECRET_KEY / key file) before the
// next start.
func (s *Store) RotateKey(ctx context.Context, newKey []byte) error {
	gcm, err := newAEAD(newKey)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type plain struct {
		name, value string
		created     time.Time
	}
	var all []plain
	cursor := ""
	for {
		items, next, err := s.kv.List(ctx, "", 1000, cursor)
		if err != nil {
			return err
		}
		for _, it := range items {
			var rec record
			if err := json.Unmarshal(it.Value, &rec); err != nil {
				return fmt.Errorf("secrets: corrupt record %s: %w", it.Key, err)
			}
			v, err := s.open(it.Key, rec)
			if err != nil {
				return err
			}
			all = append(all, plain{it.Key, v, rec.CreatedAt})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	old := s.gcm
	s.gcm = gcm
	for _, p := range all {
		if err := s.put(ctx, p.name, p.value, p.created); err != nil {
			s.gcm = old // partially rotated: the caller must retry with the new key
			return fmt.Errorf("secrets: rotate %s: %w", p.name, err)
		}
	}
	return nil
}

// Resolver returns the runtime secret lookup: env first (lookup, default os.LookupEnv),
// then the store. A nil store resolves from the environment only.
func Resolver(s *Store, lookup func(string) (string, bool)) func(string) (string, bool) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return func(name string) (string, bool) {
		if v, ok := lookup(name); ok && v != "" {
			return v, true
		}
		if s == nil {
			return "", false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v, err := s.Get(ctx, name)
		if err != nil {
			return "", false
		}
		return v, true
	}
}
