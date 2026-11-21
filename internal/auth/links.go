// Package auth signs portal links and API session tokens.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrExpiredToken = errors.New("auth: expired token")
)

// LinkClaims are carried by a signed portal link (ctx.portal.link in the SDK).
type LinkClaims struct {
	Server  string `json:"s"`           // server key
	UserID  string `json:"u,omitempty"` // pre-authenticated member ("" = anonymous link)
	Path    string `json:"p"`
	Expires int64  `json:"e"` // unix seconds
	Nonce   string `json:"n"`
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
	m.Write([]byte("codi-link-v1:"))
	m.Write([]byte(p))
	return m.Sum(nil)
}

func (s *Signer) Verify(token string) (LinkClaims, error) {
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
	if s.now().Unix() > c.Expires {
		return LinkClaims{}, ErrExpiredToken
	}
	return c, nil
}

// Linker builds portal URLs: <base><path>?server=<key>&codi_token=<token>.
type Linker struct {
	Signer  *Signer
	BaseURL string // portal origin, e.g. https://codi.example.com
	Server  string // server key the links are for
	MaxTTL  time.Duration
}

// Link implements the plugin host's PortalLinker.
func (l *Linker) Link(path, userID string, ttl time.Duration) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("portal path must start with a single '/': %q", path)
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if l.MaxTTL > 0 && ttl > l.MaxTTL {
		ttl = l.MaxTTL
	}
	tok := l.Signer.Sign(LinkClaims{Server: l.Server, UserID: userID, Path: path, Expires: l.Signer.now().Add(ttl).Unix()})
	u, err := url.Parse(strings.TrimRight(l.BaseURL, "/") + path)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("server", l.Server)
	q.Set("codi_token", tok)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
