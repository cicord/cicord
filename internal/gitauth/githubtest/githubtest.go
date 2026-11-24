// Package githubtest is a fake GitHub REST API for tests: GitHub App installation
// tokens (the JWT signature is verified), pull requests and commit statuses. The App's
// private key is generated at test time (never commit key files).
package githubtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	keyOnce sync.Once
	key     *rsa.PrivateKey
	keyErr  error
)

// Server is a fake GitHub API with one App installed on one repository.
type Server struct {
	URL            string
	AppID          string
	Repo           string // owner/name the App is installed on
	InstallationID int64
	// KeyPEM is the App's private key (PKCS#1 PEM).
	KeyPEM []byte

	mu        sync.Mutex
	tokens    map[string]bool // issued installation tokens, plus accepted static tokens
	exchanges int
	pulls     []Request
	statuses  []Request
}

// Request is a recorded pull request or commit status call.
type Request struct {
	Path          string
	Authorization string
	Body          map[string]any
}

// New starts a fake GitHub API (closed at the end of the test).
func New(t testing.TB) *Server {
	t.Helper()
	keyOnce.Do(func() { key, keyErr = rsa.GenerateKey(rand.Reader, 2048) })
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	s := &Server{AppID: "4242", Repo: "acme/community", InstallationID: 77, tokens: map[string]bool{},
		KeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA " + "PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkJWT(w, r) {
			return
		}
		if r.PathValue("owner")+"/"+r.PathValue("repo") != s.Repo {
			writeError(w, http.StatusNotFound, "Not Found")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": s.InstallationID})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkJWT(w, r) {
			return
		}
		if r.PathValue("id") != strconv.FormatInt(s.InstallationID, 10) {
			writeError(w, http.StatusNotFound, "Not Found")
			return
		}
		s.mu.Lock()
		s.exchanges++
		tok := fmt.Sprintf("inst-token-%d", s.exchanges)
		s.tokens[tok] = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	})
	record := func(list *[]Request) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			tok, _ := strings.CutPrefix(auth, "Bearer ")
			s.mu.Lock()
			ok := s.tokens[tok]
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*list = append(*list, Request{Path: r.URL.Path, Authorization: auth, Body: body})
			s.mu.Unlock()
			if !ok {
				writeError(w, http.StatusUnauthorized, "Bad credentials")
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/" + s.Repo + "/pull/1"})
		}
	}
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls", record(&s.pulls))
	mux.HandleFunc("POST /repos/{owner}/{repo}/statuses/{sha}", record(&s.statuses))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// AcceptToken makes a static token (a personal access token) valid for the REST calls.
func (s *Server) AcceptToken(tok string) {
	s.mu.Lock()
	s.tokens[tok] = true
	s.mu.Unlock()
}

// Exchanges is the number of installation tokens issued.
func (s *Server) Exchanges() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exchanges
}

// Pulls returns the recorded pull request calls.
func (s *Server) Pulls() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.pulls...)
}

// Statuses returns the recorded commit status calls.
func (s *Server) Statuses() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.statuses...)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// checkJWT verifies the App JWT: RS256 signature with the App's public key, iss, and a
// lifetime of at most 10 minutes around now.
func (s *Server) checkJWT(w http.ResponseWriter, r *http.Request) bool {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(tok, ".")
	ok := len(parts) == 3
	if ok {
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		ok = err == nil && rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) == nil
	}
	if ok {
		raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c struct {
			Iat, Exp int64
			Iss      string
		}
		now := time.Now().Unix()
		ok = json.Unmarshal(raw, &c) == nil && c.Iss == s.AppID && c.Iat <= now && c.Exp > now && c.Exp-c.Iat <= 600
	}
	if !ok {
		_, _ = io.Copy(io.Discard, r.Body)
		writeError(w, http.StatusUnauthorized, "A JSON web token could not be decoded")
	}
	return ok
}
