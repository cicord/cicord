package auth

import (
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestLinks(t *testing.T) {
	s := NewSigner([]byte("secret"))
	now := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return now }
	l := &Linker{Signer: s, BaseURL: "https://cicord.example.com/", Server: "my-community", MaxTTL: time.Hour}
	raw, err := l.Link("/onboarding", "42", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	if u.Host != "cicord.example.com" || u.Path != "/onboarding" || u.Query().Get("server") != "my-community" {
		t.Fatalf("url = %s", raw)
	}
	c, err := s.Verify(u.Query().Get("cicord_token"))
	if err != nil || c.UserID != "42" || c.Server != "my-community" || c.Path != "/onboarding" {
		t.Fatalf("claims = %+v, %v", c, err)
	}
	// tampering
	tok := u.Query().Get("cicord_token")
	if _, err := s.Verify(tok[:len(tok)-2] + "xx"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("tampered token: %v", err)
	}
	if _, err := NewSigner([]byte("other")).Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("wrong key: %v", err)
	}
	// expiry
	now = now.Add(6 * time.Minute)
	if _, err := s.Verify(tok); !errors.Is(err, ErrExpiredToken) {
		t.Errorf("expired: %v", err)
	}
	if _, err := l.Link("//evil.com", "", 0); err == nil {
		t.Errorf("protocol-relative path must be rejected")
	}
}
