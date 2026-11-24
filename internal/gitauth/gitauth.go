// Package gitauth provides the credentials of the configuration repository: a fixed
// token (CICORD_GIT_TOKEN, a personal access token) or a GitHub App, whose short-lived
// installation tokens are minted and refreshed on demand.
//
// Every git operation (fetch, clone, push) and every GitHub REST call (pull requests,
// commit statuses) asks the TokenSource for a token right before it runs, so a rotated
// installation token is picked up without a restart. Tokens and keys are never logged
// and never included in errors.
package gitauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Secret names (also environment variables: the environment takes precedence over the
// encrypted secret store, like every runtime secret).
const (
	// TokenSecret is the personal access token (or any HTTPS git token).
	TokenSecret = "CICORD_GIT_TOKEN" //nolint:gosec // G101: the name of a secret, not its value
	// AppIDSecret is the GitHub App ID (or its client ID).
	AppIDSecret = "CICORD_GITHUB_APP_ID" //nolint:gosec // G101: the name of a secret, not its value
	// AppInstallationIDSecret is the installation of the App on the repository's owner
	// (optional: discovered from the repository on github.com).
	AppInstallationIDSecret = "CICORD_GITHUB_APP_INSTALLATION_ID" //nolint:gosec // G101: the name of a secret, not its value
	// AppPrivateKeySecret is the App's private key (PEM, PKCS#1 or PKCS#8).
	AppPrivateKeySecret = "CICORD_GITHUB_APP_PRIVATE_KEY" //nolint:gosec // G101: the name of a secret, not its value
	// AppPrivateKeyFileEnv names a file holding the private key (Docker / Kubernetes
	// secrets mounted as files); the --github-app-key-file flag of `cicord serve`.
	AppPrivateKeyFileEnv = "CICORD_GITHUB_APP_PRIVATE_KEY_FILE"
)

// Authentication methods (Resolve).
const (
	MethodNone  = ""
	MethodToken = "token"
	MethodApp   = "githubApp"
)

// AppSecrets are the secret names of the GitHub App configuration.
var AppSecrets = []string{AppIDSecret, AppInstallationIDSecret, AppPrivateKeySecret}

// TokenSource returns the token of the next operation. Implementations are safe for
// concurrent use. A nil TokenSource means anonymous access.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type staticSource string

func (s staticSource) Token(context.Context) (string, error) { return string(s), nil }

// Static returns a source that always returns token (nil for an empty token).
func Static(token string) TokenSource {
	if token = strings.TrimSpace(token); token == "" {
		return nil
	}
	return staticSource(token)
}

// GitAuth returns the HTTPS basic auth of a git operation ("x-access-token" works for
// personal access tokens and installation tokens alike). It returns an untyped nil when
// ts is nil or yields an empty token.
func GitAuth(ctx context.Context, ts TokenSource) (transport.AuthMethod, error) {
	if ts == nil {
		return nil, nil
	}
	tok, err := ts.Token(ctx)
	if err != nil {
		return nil, err
	}
	if tok == "" {
		return nil, nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: tok}, nil
}

// Bearer returns the Authorization header value of a GitHub REST call ("" without a
// token).
func Bearer(ctx context.Context, ts TokenSource) (string, error) {
	if ts == nil {
		return "", nil
	}
	tok, err := ts.Token(ctx)
	if err != nil || tok == "" {
		return "", err
	}
	return "Bearer " + tok, nil
}

// Options configures Resolve.
type Options struct {
	// Lookup resolves a secret name (environment first, then the secret store).
	Lookup func(string) (string, bool)
	// TokenSecret names the token secret (default TokenSecret; the wizard records it).
	TokenSecret string
	// KeyFile is a file holding the App private key; it takes precedence over the
	// CICORD_GITHUB_APP_PRIVATE_KEY secret.
	KeyFile string
	// Repo is "owner/name" on github.com: installation discovery and token scope.
	Repo string
	// API overrides https://api.github.com (GitHub Enterprise Server, tests).
	API        string
	HTTPClient *http.Client
}

// Resolve builds the token source of the configuration repository: the GitHub App when
// CICORD_GITHUB_APP_ID and its private key are set (it takes precedence), else the
// token, else nil (anonymous). A partial App configuration is an error.
func Resolve(o Options) (TokenSource, string, error) {
	look := func(name string) string {
		if o.Lookup == nil {
			return ""
		}
		v, ok := o.Lookup(name)
		if !ok {
			return ""
		}
		return strings.TrimSpace(v)
	}
	appID, inst, key := look(AppIDSecret), look(AppInstallationIDSecret), look(AppPrivateKeySecret)
	keyFrom := AppPrivateKeySecret
	if o.KeyFile != "" {
		data, err := os.ReadFile(o.KeyFile)
		if err != nil {
			return nil, MethodNone, fmt.Errorf("GitHub App private key file: %w", err)
		}
		key, keyFrom = strings.TrimSpace(string(data)), o.KeyFile
	}
	switch {
	case appID != "" && key != "":
		app, err := NewGitHubApp(AppConfig{AppID: appID, InstallationID: inst, PrivateKey: []byte(key), Repo: o.Repo,
			API: o.API, HTTPClient: o.HTTPClient})
		if err != nil {
			return nil, MethodNone, fmt.Errorf("GitHub App (%s): %w", keyFrom, err)
		}
		return app, MethodApp, nil
	case appID != "":
		return nil, MethodNone, fmt.Errorf("GitHub App: %s is set but not its private key (%s or --github-app-key-file)", AppIDSecret, AppPrivateKeySecret)
	case key != "" || inst != "":
		return nil, MethodNone, fmt.Errorf("GitHub App: the private key or %s is set but not %s", AppInstallationIDSecret, AppIDSecret)
	}
	name := o.TokenSecret
	if name == "" {
		name = TokenSecret
	}
	if ts := Static(look(name)); ts != nil {
		return ts, MethodToken, nil
	}
	return nil, MethodNone, nil
}

// parseInstallationID accepts "" (discover) or a positive integer.
func parseInstallationID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("the installation ID must be a positive number (the last part of the installation's settings URL)")
	}
	return id, nil
}
