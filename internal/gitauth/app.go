package gitauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is the GitHub REST API of github.com.
const DefaultAPI = "https://api.github.com"

const (
	// refreshBefore renews an installation token this long before it expires.
	refreshBefore = 5 * time.Minute
	// jwtLifetime stays below GitHub's 10-minute maximum; iat is backdated for clock skew.
	jwtLifetime = 9 * time.Minute
	jwtBackdate = 60 * time.Second
)

var (
	// ErrJWTRejected means GitHub refused the App's JSON Web Token: wrong App ID, wrong
	// private key, or a clock that is off by more than a minute.
	ErrJWTRejected = errors.New("GitHub rejected the App credentials (check the App ID, the private key and the system clock)")
	// ErrNotInstalled means the App has no installation on the repository (or the
	// configured installation does not exist).
	ErrNotInstalled = errors.New("the GitHub App is not installed on the repository")
)

// AppConfig configures a GitHub App token source.
type AppConfig struct {
	// AppID is the App ID (or the client ID) shown on the App's settings page.
	AppID string
	// InstallationID is optional when Repo is set: it is then discovered.
	InstallationID string
	// PrivateKey is the PEM private key generated on the App's settings page (PKCS#1
	// "RSA PRIVATE KEY" or PKCS#8 "PRIVATE KEY"). Literal "\n" sequences are accepted
	// (keys passed through single-line environment files).
	PrivateKey []byte
	// Repo is "owner/name": installation discovery, and the installation tokens are
	// restricted to this repository.
	Repo string
	// API overrides https://api.github.com.
	API        string
	HTTPClient *http.Client
	// Now overrides the clock (tests).
	Now func() time.Time
}

// GitHubApp mints installation access tokens for a GitHub App. It caches the token and
// renews it 5 minutes before it expires; concurrent callers share one exchange.
type GitHubApp struct {
	appID  string
	repo   string
	key    *rsa.PrivateKey
	api    string
	client *http.Client
	now    func() time.Time

	// sem serializes refreshes (a channel so that waiting callers honor their context);
	// it guards installation, token and expires.
	sem          chan struct{}
	installation int64
	token        string
	expires      time.Time
}

// NewGitHubApp validates the configuration and parses the private key. It does not call
// GitHub.
func NewGitHubApp(cfg AppConfig) (*GitHubApp, error) {
	appID := strings.TrimSpace(cfg.AppID)
	if appID == "" {
		return nil, errors.New("the App ID is required")
	}
	if strings.ContainsAny(appID, " \t\r\n\"") {
		return nil, errors.New("the App ID must be the number (or the client ID) shown on the App's settings page")
	}
	inst, err := parseInstallationID(cfg.InstallationID)
	if err != nil {
		return nil, err
	}
	repo := strings.Trim(strings.TrimSpace(cfg.Repo), "/")
	if repo != "" && strings.Count(repo, "/") != 1 {
		return nil, fmt.Errorf("repository %q is not owner/name", repo)
	}
	if inst == 0 && repo == "" {
		return nil, fmt.Errorf("%s is required when the repository is not on github.com", AppInstallationIDSecret)
	}
	key, err := ParsePrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	a := &GitHubApp{appID: appID, repo: repo, key: key, api: strings.TrimRight(cfg.API, "/"), client: cfg.HTTPClient,
		now: cfg.Now, sem: make(chan struct{}, 1), installation: inst}
	if a.api == "" {
		a.api = DefaultAPI
	}
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	if a.now == nil {
		a.now = time.Now
	}
	return a, nil
}

// ParsePrivateKey decodes a PEM RSA private key (PKCS#1 or PKCS#8).
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	s := strings.TrimSpace(string(data))
	if !strings.Contains(s, "\n") && strings.Contains(s, `\n`) {
		s = strings.ReplaceAll(s, `\n`, "\n")
	}
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, errors.New("the private key is not PEM (paste the whole .pem file downloaded from the App's settings page, BEGIN and END lines included)")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("the private key could not be parsed (expected the RSA key of a GitHub App, PKCS#1 or PKCS#8)")
	}
	k, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the private key is not an RSA key (GitHub Apps use RSA keys)")
	}
	return k, nil
}

// AppID returns the configured App ID.
func (a *GitHubApp) AppID() string { return a.appID }

func (a *GitHubApp) lock(ctx context.Context) error {
	select {
	case a.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *GitHubApp) unlock() { <-a.sem }

// Token returns a valid installation token, minting a new one when the cached token
// expires within 5 minutes.
func (a *GitHubApp) Token(ctx context.Context) (string, error) {
	if err := a.lock(ctx); err != nil {
		return "", err
	}
	defer a.unlock()
	if a.token != "" && a.now().Add(refreshBefore).Before(a.expires) {
		return a.token, nil
	}
	if err := a.discover(ctx); err != nil {
		return "", err
	}
	var body io.Reader
	if a.repo != "" {
		_, name, _ := strings.Cut(a.repo, "/")
		payload, _ := json.Marshal(map[string][]string{"repositories": {name}})
		body = bytes.NewReader(payload)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := "/app/installations/" + strconv.FormatInt(a.installation, 10) + "/access_tokens"
	status, err := a.call(ctx, http.MethodPost, path, body, &out)
	switch {
	case err != nil && status == http.StatusNotFound:
		return "", fmt.Errorf("%w: installation %d not found (check %s)", ErrNotInstalled, a.installation, AppInstallationIDSecret)
	case err != nil && status == http.StatusUnprocessableEntity && a.repo != "":
		return "", fmt.Errorf("%w: installation %d has no access to %s: %w", ErrNotInstalled, a.installation, a.repo, err)
	case err != nil:
		return "", fmt.Errorf("GitHub App installation token: %w", err)
	case out.Token == "":
		return "", errors.New("GitHub App installation token: empty token in the response")
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = a.now().Add(30 * time.Minute)
	}
	a.token, a.expires = out.Token, out.ExpiresAt
	return a.token, nil
}

// InstallationID returns the configured or discovered installation.
func (a *GitHubApp) InstallationID(ctx context.Context) (int64, error) {
	if err := a.lock(ctx); err != nil {
		return 0, err
	}
	defer a.unlock()
	if err := a.discover(ctx); err != nil {
		return 0, err
	}
	return a.installation, nil
}

// discover looks the installation of the repository up (sem held).
func (a *GitHubApp) discover(ctx context.Context) error {
	if a.installation != 0 {
		return nil
	}
	var out struct {
		ID int64 `json:"id"`
	}
	status, err := a.call(ctx, http.MethodGet, "/repos/"+a.repo+"/installation", nil, &out)
	switch {
	case err != nil && status == http.StatusNotFound:
		return fmt.Errorf("%w: install it on %s (App settings → Install App)", ErrNotInstalled, a.repo)
	case err != nil:
		return fmt.Errorf("GitHub App installation of %s: %w", a.repo, err)
	case out.ID <= 0:
		return fmt.Errorf("GitHub App installation of %s: no installation ID in the response", a.repo)
	}
	a.installation = out.ID
	return nil
}

// call performs an App-authenticated (JWT) request and decodes a 2xx JSON response.
func (a *GitHubApp) call(ctx context.Context, method, path string, body io.Reader, out any) (int, error) {
	jwt, err := a.jwt()
	if err != nil {
		return 0, err
	}
	u, err := url.JoinPath(a.api, path)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+jwt)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return resp.StatusCode, ErrJWTRejected
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s %s: %s", method, path, APIError(resp.Status, data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: invalid response: %w", method, path, err)
	}
	return resp.StatusCode, nil
}

// APIError formats a GitHub error response: the status and the API "message" field.
func APIError(status string, body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return status + ": " + e.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return status
	}
	return status + ": " + s
}

// jwt signs the App's JSON Web Token (RS256): iat backdated by 60s, exp in 9 minutes,
// iss the App ID (GitHub accepts the App ID or the client ID).
func (a *GitHubApp) jwt() (string, error) {
	now := a.now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": a.appID,
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign the GitHub App JWT: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
