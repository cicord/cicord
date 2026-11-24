package setup

import (
	"context"
	"errors"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"

	"github.com/cicord/cicord/internal/gitauth"
	"github.com/cicord/cicord/internal/gitauth/githubtest"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/state/sqlite"
)

func repoWizard(t *testing.T, env map[string]string, api string) (*Wizard, *secrets.Store) {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	sec, err := secrets.Open(t.Context(), store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	w := New(Options{Mode: "serve", Store: store, Secrets: sec, GitHubAPI: api,
		Env: func(n string) (string, bool) { v, ok := env[n]; return v, ok }})
	return w, sec
}

func wizardErr(t *testing.T, err error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	return e
}

func TestChooseRepoGitHubApp(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t)
	remote := t.TempDir()
	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	w, sec := repoWizard(t, nil, gh.URL)
	req := RepoRequest{Provider: ProviderGitHub, URL: remote, Branch: "main", Auth: gitauth.MethodApp, AppID: gh.AppID, PrivateKey: string(gh.KeyPEM)}

	// Not on github.com: an App is refused (its tokens only go to github.com), even with
	// an installation ID.
	req.InstallationID = "77"
	_, err := w.ChooseRepo(ctx, req)
	if e := wizardErr(t, err); !strings.Contains(e.Message, "only be used with a repository on github.com") {
		t.Fatalf("not github.com: %+v", e)
	}
	// From here on, the local repository stands for github.com/acme/community.
	githubRepoOf = func(string) string { return gh.Repo }
	t.Cleanup(func() { githubRepoOf = GitHubRepo })
	// Wrong installation.
	req.InstallationID = "78"
	_, err = w.ChooseRepo(ctx, req)
	if e := wizardErr(t, err); e.Message != "The GitHub App is not installed on this repository." {
		t.Fatalf("wrong installation: %+v", e)
	}
	// Wrong App ID: GitHub rejects the JWT.
	bad := req
	bad.AppID, bad.InstallationID = "1", "77"
	_, err = w.ChooseRepo(ctx, bad)
	if e := wizardErr(t, err); !strings.Contains(e.Message, "rejected") {
		t.Fatalf("bad app id: %+v", e)
	}
	// Missing private key.
	nokey := req
	nokey.PrivateKey, nokey.AppID = "", ""
	_, err = w.ChooseRepo(ctx, nokey)
	if e := wizardErr(t, err); !strings.Contains(e.Message, "required") {
		t.Fatalf("no key: %+v", e)
	}

	// Valid: an installation token is minted, the remote checked, the secrets stored.
	req.InstallationID = "77"
	v, err := w.ChooseRepo(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if v.Repo == nil || v.Repo.Auth != gitauth.MethodApp || v.Repo.AppID != gh.AppID || !v.Repo.TokenStored {
		t.Fatalf("view = %+v", v.Repo)
	}
	for name, want := range map[string]string{gitauth.AppIDSecret: gh.AppID, gitauth.AppInstallationIDSecret: "77"} {
		if got, _ := sec.Get(ctx, name); got != want {
			t.Errorf("%s = %q", name, got)
		}
	}
	if got, _ := sec.Get(ctx, gitauth.AppPrivateKeySecret); got != strings.TrimSpace(string(gh.KeyPEM)) {
		t.Error("private key not stored")
	}
	if gh.Exchanges() == 0 {
		t.Fatal("no installation token was minted")
	}
	// The recorded repository resolves to the App.
	st, _, _ := LoadState(ctx, w.opts.Store)
	ts, method, err := RepoTokens(*st.Repo, w.secret, gitauth.Options{API: gh.URL})
	if err != nil || method != gitauth.MethodApp {
		t.Fatalf("RepoTokens = %q %v", method, err)
	}
	if tok, err := ts.Token(ctx); err != nil || !strings.HasPrefix(tok, "inst-token-") {
		t.Fatalf("token = %q %v", tok, err)
	}
	// Resubmitting without new values reuses the stored secrets.
	if _, err := w.ChooseRepo(ctx, RepoRequest{Provider: ProviderGitHub, URL: remote, Auth: gitauth.MethodApp}); err != nil {
		t.Fatalf("stored secrets: %v", err)
	}

	// Switching to a token removes the stored App secrets (they would take precedence).
	v, err = w.ChooseRepo(ctx, RepoRequest{Provider: ProviderGitHub, URL: remote, Branch: "main", Token: "a-personal-token"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Repo.Auth != gitauth.MethodToken || !v.Repo.TokenStored {
		t.Fatalf("token view = %+v", v.Repo)
	}
	for _, name := range gitauth.AppSecrets {
		if sec.Has(ctx, name) {
			t.Errorf("%s still stored", name)
		}
	}
	// A repository set up with the App but without its secrets is an error, not a
	// silent fallback to the token.
	if _, _, err := RepoTokens(RepoConfig{Provider: ProviderGitHub, URL: remote, Auth: gitauth.MethodApp, TokenRef: GitTokenSecret}, w.secret, gitauth.Options{}); err == nil {
		t.Fatal("missing App secrets accepted")
	}
}

func TestChooseRepoTokenWithAppInEnvironment(t *testing.T) {
	remote := t.TempDir()
	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{{gitauth.AppIDSecret: "4242"}, {gitauth.AppPrivateKeySecret: "a key"}} {
		w, _ := repoWizard(t, env, "")
		_, err := w.ChooseRepo(context.Background(), RepoRequest{Provider: ProviderGitHub, URL: remote, Token: "a-personal-token"})
		if e := wizardErr(t, err); e.Code != "conflict" || !strings.Contains(e.Hint, "GitHub App") {
			t.Fatalf("%v: err = %+v", env, e)
		}
	}
	// The key file of `cicord serve --github-app-key-file` counts too.
	w, _ := repoWizard(t, nil, "")
	w.opts.GitHubAppKeyFile = "/run/secrets/app.pem"
	_, err := w.ChooseRepo(context.Background(), RepoRequest{Provider: ProviderGitHub, URL: remote, Token: "a-personal-token"})
	if e := wizardErr(t, err); e.Code != "conflict" || !strings.Contains(e.Message, "--github-app-key-file") {
		t.Fatalf("key file: err = %+v", e)
	}
}
