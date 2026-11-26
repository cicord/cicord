// Package auth signs portal links and API session tokens.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cicord/cicord/internal/state"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrExpiredToken = errors.New("auth: expired token")
)

// LinkClaims are carried by a signed portal link (ctx.portal.link in the SDK).
type ResourceScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

var scopeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (v ResourceScope) Valid() bool { return v.Kind == "case" && scopeID.MatchString(v.ID) }

// ScopedGrant binds a case to the installation that issued it and the case's revocation generation.
type ScopedGrant struct {
	RenewalOnly bool `json:"renewalOnly,omitempty"`
	ResourceScope
	Bot        string `json:"bot"`
	Instance   string `json:"instance"`
	Plugin     string `json:"plugin"`
	Generation int64  `json:"generation"`
}
type LinkClaims struct {
	Scope   *ScopedGrant `json:"scope,omitempty"`
	Server  string       `json:"s"`           // server key
	UserID  string       `json:"u,omitempty"` // pre-authenticated member ("" = anonymous link)
	Path    string       `json:"p"`
	Expires int64        `json:"e"` // unix seconds
	Nonce   string       `json:"n"`
}

// Signer signs and verifies link tokens with HMAC-SHA256.
type Signer struct {
	key []byte
	now func() time.Time
}

func NewSigner(key []byte) *Signer {
	return &Signer{key: key, now: time.Now}
}

// RandomKey returns a fresh 32-byte key.
func RandomKey() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}

func RandomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Signer) Sign(c LinkClaims) string {
	if c.Nonce == "" {
		c.Nonce = RandomID(9)
	}
	payload, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + base64.RawURLEncoding.EncodeToString(s.mac(p))
}

func (s *Signer) mac(p string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("cicord-link-v1:"))
	m.Write([]byte(p))
	return m.Sum(nil)
}

func (s *Signer) Verify(token string) (LinkClaims, error) {
	return s.verify(token, 0)
}

// Renewal accepts an authentic, recently expired case credential for a new OAuth
// challenge. Its resulting session may only request a replacement from the issuer.
func (s *Signer) VerifyRenewal(token string) (LinkClaims, error) {
	c, err := s.verify(token, 30*24*time.Hour)
	if err != nil {
		return c, err
	}
	if c.Scope == nil || c.Expires > s.now().Unix() {
		return LinkClaims{}, ErrInvalidToken
	}
	c.Scope.RenewalOnly = true
	c.Expires = s.now().Add(10 * time.Minute).Unix()
	return c, nil
}

func (s *Signer) verify(token string, grace time.Duration) (LinkClaims, error) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return LinkClaims{}, ErrInvalidToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(p)) {
		return LinkClaims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return LinkClaims{}, ErrInvalidToken
	}
	var c LinkClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return LinkClaims{}, ErrInvalidToken
	}
	if s.now().Add(-grace).Unix() >= c.Expires {
		return LinkClaims{}, ErrExpiredToken
	}
	return c, nil
}

// Linker builds portal URLs: <base><path>?server=<key>&cicord_token=<token>.
type Linker struct {
	Signer                *Signer
	BaseURL               string // portal origin, e.g. https://cicord.example.com
	Server                string // server key the links are for
	MaxTTL                time.Duration
	Bot, Instance, Plugin string
	Store                 state.Store
}

// Link implements the plugin host's PortalLinker.
func (l *Linker) Link(path, userID string, ttl time.Duration) (string, error) {
	return l.url(path, userID, ttl, nil)
}

func (l *Linker) url(path, userID string, ttl time.Duration, grant *ScopedGrant) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") {
		return "", fmt.Errorf("portal path must start with a single '/': %q", path)
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if grant == nil && l.MaxTTL > 0 && ttl > l.MaxTTL {
		ttl = l.MaxTTL
	}
	tok := l.Signer.Sign(LinkClaims{Scope: grant, Server: l.Server, UserID: userID, Path: path, Expires: l.Signer.now().Add(ttl).Unix()})
	u, err := url.Parse(strings.TrimRight(l.BaseURL, "/") + path)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("server", l.Server)
	if grant == nil {
		q.Set("cicord_token", tok)
	} else {
		q.Set("cicord_scope", tok)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func scopeGeneration(ctx context.Context, store state.Store, server string, g ScopedGrant) (int64, error) {
	raw, err := scopeKV(store, server, g).Get(ctx, g.Kind+"/"+g.ID)
	if errors.Is(err, state.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var version int64
	err = json.Unmarshal(raw, &version)
	return version, err
}
func scopeKV(store state.Store, server string, g ScopedGrant) state.KV {
	return store.KV("portal:scopes/" + server + "/" + g.Bot + "/" + g.Instance + "/" + g.Plugin)
}
func (c LinkClaims) Current(ctx context.Context, store state.Store) (bool, error) {
	if c.Scope == nil || !c.Scope.Valid() || c.UserID == "" || c.Expires <= time.Now().Unix() {
		return false, nil
	}
	version, err := scopeGeneration(ctx, store, c.Server, *c.Scope)
	return version == c.Scope.Generation, err
}
func (l *Linker) ScopedLink(ctx context.Context, path, userID string, ttl time.Duration, scope ResourceScope) (string, error) {
	if !scope.Valid() || userID == "" || ttl < 0 || ttl > 7*24*time.Hour {
		return "", fmt.Errorf("invalid scoped link: case, user and lifetime up to seven days required")
	}
	g := ScopedGrant{ResourceScope: scope, Bot: l.Bot, Instance: l.Instance, Plugin: l.Plugin}
	v, err := scopeGeneration(ctx, l.Store, l.Server, g)
	if err != nil {
		return "", err
	}
	g.Generation = v
	return l.url(path, userID, ttl, &g)
}
func (l *Linker) Revoke(ctx context.Context, scope ResourceScope) error {
	if !scope.Valid() {
		return fmt.Errorf("invalid case scope")
	}
	_, err := scopeKV(l.Store, l.Server, ScopedGrant{Bot: l.Bot, Instance: l.Instance, Plugin: l.Plugin}).Increment(ctx, scope.Kind+"/"+scope.ID, 1)
	return err
}

// PrivacyDigest uses a distinct HMAC domain for erasures and pseudonyms.
func (s *Signer) PrivacyDigest(purpose, server, user string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("cicord-privacy-v1\x00" + purpose + "\x00" + server + "\x00" + user))
	return hex.EncodeToString(m.Sum(nil))
}
