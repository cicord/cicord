package registry

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"gopkg.in/yaml.v3"

	"github.com/npjonath/codi/internal/config"
)

// Private registries: a registry of codi.yaml may name a secret (secretRef) holding an
// access token. The token is attached to the context for the registry's host only, so it
// is sent to that host over https and never to the other hosts an index may list.

type credsKey struct{}

// WithToken returns ctx carrying token for the host of url (https only).
func WithToken(ctx context.Context, url, token string) context.Context {
	host := strings.ToLower(hostOf(url))
	if host == "" || token == "" || !IsRemoteURL(url) {
		return ctx
	}
	prev, _ := ctx.Value(credsKey{}).(map[string]string)
	next := make(map[string]string, len(prev)+1)
	for k, v := range prev {
		next[k] = v
	}
	next[host] = token
	return context.WithValue(ctx, credsKey{}, next)
}

// AuthFor is the git auth for url from ctx (nil: anonymous).
func AuthFor(ctx context.Context, url string) transport.AuthMethod {
	if !IsRemoteURL(url) {
		return nil
	}
	m, _ := ctx.Value(credsKey{}).(map[string]string)
	tok := m[strings.ToLower(hostOf(url))]
	if tok == "" {
		return nil
	}
	// GitHub, GitLab and Gitea accept a token as the password of any user name.
	return &githttp.BasicAuth{Username: "x-access-token", Password: tok}
}

// WithRegistryTokens attaches the tokens of every registry that has a resolvable secretRef.
func WithRegistryTokens(ctx context.Context, regs []config.Registry, secret func(string) (string, bool)) context.Context {
	if secret == nil {
		return ctx
	}
	for _, r := range regs {
		ref := r.TokenRef()
		if ref == "" {
			continue // only this registry's own token, never another secret
		}
		if tok, ok := secret(ref); ok {
			ctx = WithToken(ctx, r.URL, tok)
		}
	}
	return ctx
}

// SecretRefFor is the secret name of a registry's token (config.RegistryTokenRef).
func SecretRefFor(name string) string { return config.RegistryTokenRef(name) }

// projectRegistries reads spec.registries of codi.yaml in root (nil on any error).
func projectRegistries(root string) []config.Registry {
	data, err := os.ReadFile(filepath.Join(root, "codi.yaml"))
	if err != nil {
		return nil
	}
	var p struct {
		Spec struct {
			Registries []config.Registry `yaml:"registries"`
		} `yaml:"spec"`
	}
	if yaml.Unmarshal(data, &p) != nil {
		return nil
	}
	return p.Spec.Registries
}
