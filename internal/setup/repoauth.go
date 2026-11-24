package setup

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cicord/cicord/internal/gitauth"
)

// repoCreds are the credentials of the github provider being chosen, validated but
// not stored yet.
type repoCreds struct {
	method string
	tokens gitauth.TokenSource
	// values to store (only what the form provided, never what came from the
	// environment): secret name → value.
	store map[string]string
	// clearApp removes stored GitHub App secrets (a token was chosen: stale App
	// secrets would take precedence on the next start).
	clearApp bool
}

// repoCredentials validates the credentials of a Repository step form. For a GitHub
// App, an installation token is minted (discovering the installation when needed).
func (w *Wizard) repoCredentials(ctx context.Context, req RepoRequest, remote string, local bool) (repoCreds, error) {
	switch req.Auth {
	case gitauth.MethodApp:
		return w.appCredentials(ctx, req, remote)
	case "", gitauth.MethodToken:
	default:
		return repoCreds{}, errorf(http.StatusBadRequest, "invalid", "", "unknown authentication %q (token or githubApp)", req.Auth)
	}
	if from := w.appFromDeployment(); from != "" {
		return repoCreds{}, errorf(http.StatusConflict, "conflict",
			"Choose “GitHub App”, or remove "+from+" from the runtime's configuration.",
			"A GitHub App is configured for this runtime (%s): it takes precedence over a token.", from)
	}
	c := repoCreds{method: gitauth.MethodToken, store: map[string]string{}, clearApp: true}
	token := strings.TrimSpace(req.Token)
	if token != "" {
		c.store[GitTokenSecret] = token
	} else {
		token, _ = w.secret(GitTokenSecret)
	}
	if token == "" && !local {
		return repoCreds{}, errorf(http.StatusUnprocessableEntity, "invalid",
			"Create a fine-grained personal access token on GitHub (Settings → Developer settings → Personal access tokens) with “Contents: Read and write” on this repository "+
				"(and “Pull requests” and “Commit statuses: Read and write” for pull requests and statuses).",
			"A GitHub token is required.")
	}
	c.tokens = gitauth.Static(token)
	return c, nil
}

// appFromDeployment names the GitHub App setting the deployment provides (environment
// or --github-app-key-file), "" when none: a token chosen in the wizard would not be used.
func (w *Wizard) appFromDeployment() string {
	for _, name := range gitauth.AppSecrets {
		if v, ok := w.opts.Env(name); ok && strings.TrimSpace(v) != "" {
			return name
		}
	}
	if w.opts.GitHubAppKeyFile != "" {
		return "--github-app-key-file"
	}
	return ""
}

func (w *Wizard) appCredentials(ctx context.Context, req RepoRequest, remote string) (repoCreds, error) {
	c := repoCreds{method: gitauth.MethodApp, store: map[string]string{}}
	appID, key, inst := strings.TrimSpace(req.AppID), strings.TrimSpace(req.PrivateKey), strings.TrimSpace(req.InstallationID)
	fresh := appID != "" || key != "" // new credentials: a stored installation may belong to another App
	if appID != "" {
		c.store[gitauth.AppIDSecret] = appID
	} else {
		appID, _ = w.secret(gitauth.AppIDSecret)
	}
	switch {
	case key != "":
		c.store[gitauth.AppPrivateKeySecret] = key
	case w.opts.GitHubAppKeyFile != "":
		data, err := os.ReadFile(w.opts.GitHubAppKeyFile)
		if err != nil {
			return repoCreds{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Message: "The GitHub App private key file cannot be read.",
				Hint: "Check --github-app-key-file, or paste the key here.", Details: err.Error()}
		}
		key = string(data)
	default:
		key, _ = w.secret(gitauth.AppPrivateKeySecret)
	}
	if inst == "" && !fresh {
		inst, _ = w.secret(gitauth.AppInstallationIDSecret)
	}
	if appID == "" || strings.TrimSpace(key) == "" {
		return repoCreds{}, errorf(http.StatusUnprocessableEntity, "invalid",
			"Create a GitHub App (Settings → Developer settings → GitHub Apps), install it on the repository, then enter its App ID and paste the private key (.pem) it generated.",
			"The GitHub App ID and private key are required.")
	}
	// Installation tokens are scoped to the repository and only sent to github.com:
	// an App is refused for a repository hosted anywhere else.
	repo := githubRepoOf(remote)
	if repo == "" {
		return repoCreds{}, errorf(http.StatusUnprocessableEntity, "invalid",
			"Use a repository URL of the form https://github.com/owner/name, or choose a personal access token for another git host.",
			"A GitHub App can only be used with a repository on github.com.")
	}
	if err := w.deploymentAppMatches(appID, inst); err != nil {
		return repoCreds{}, err
	}
	app, err := gitauth.NewGitHubApp(gitauth.AppConfig{AppID: appID, InstallationID: inst, PrivateKey: []byte(key),
		Repo: repo, API: w.opts.GitHubAPI, HTTPClient: w.opts.HTTPClient})
	if err != nil {
		return repoCreds{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Message: "The GitHub App settings are not valid: " + err.Error()}
	}
	if _, err := app.Token(ctx); err != nil {
		e := &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Details: err.Error()}
		switch {
		case errors.Is(err, gitauth.ErrJWTRejected):
			e.Message = "GitHub rejected the App ID or the private key."
			e.Hint = "Copy the App ID from the App's settings page (General → About) and paste the private key it generated (Private keys → Generate a private key). Check the system clock too."
		case errors.Is(err, gitauth.ErrNotInstalled):
			e.Message = "The GitHub App is not installed on this repository."
			e.Hint = "On the App's settings page, open “Install App”, install it on the repository's owner and give it access to this repository."
		default:
			e.Status, e.Code = http.StatusServiceUnavailable, "unavailable"
			e.Message = "Could not get a token from GitHub."
			e.Hint = "Check the runtime's network connection to api.github.com, then try again."
		}
		return repoCreds{}, e
	}
	id, err := app.InstallationID(ctx)
	if err != nil {
		return repoCreds{}, err
	}
	if v, ok := w.opts.Env(gitauth.AppInstallationIDSecret); !ok || strings.TrimSpace(v) == "" {
		c.store[gitauth.AppInstallationIDSecret] = strconv.FormatInt(id, 10) // no rediscovery after a restart
	}
	c.tokens = app
	return c, nil
}

// githubRepoOf is the owner/name of a github.com remote ("" otherwise). Tests replace it
// to let a local repository stand for a GitHub one.
var githubRepoOf = GitHubRepo

// deploymentAppMatches refuses an App ID or installation ID typed in the form that
// differs from the one the deployment provides (environment): the runtime would use the
// deployment's after a restart, so a different value could never be the one in effect.
func (w *Wizard) deploymentAppMatches(appID, inst string) error {
	for _, c := range []struct{ name, form string }{{gitauth.AppIDSecret, appID}, {gitauth.AppInstallationIDSecret, inst}} {
		if c.form == "" {
			continue
		}
		if v, ok := w.opts.Env(c.name); ok && strings.TrimSpace(v) != "" && strings.TrimSpace(v) != c.form {
			return errorf(http.StatusConflict, "conflict",
				"Leave the field empty to use the runtime's value, or change "+c.name+" in the runtime's configuration.",
				"%s differs from the value the runtime is configured with.", c.name)
		}
	}
	return nil
}

// storeCredentials writes the validated credentials to the secret store.
func (w *Wizard) storeCredentials(ctx context.Context, c repoCreds) error {
	for _, name := range []string{GitTokenSecret, gitauth.AppIDSecret, gitauth.AppPrivateKeySecret, gitauth.AppInstallationIDSecret} {
		if v, ok := c.store[name]; ok {
			if err := w.opts.Secrets.Set(ctx, name, v); err != nil {
				return err
			}
		}
	}
	if c.clearApp {
		for _, name := range gitauth.AppSecrets {
			if err := w.opts.Secrets.Delete(ctx, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// repoTokens returns the credentials of a chosen repository (nil: anonymous).
func (w *Wizard) repoTokens(repo RepoConfig) (gitauth.TokenSource, error) {
	ts, _, err := RepoTokens(repo, w.secret, gitauth.Options{KeyFile: w.opts.GitHubAppKeyFile, API: w.opts.GitHubAPI, HTTPClient: w.opts.HTTPClient})
	return ts, err
}

// RepoTokens returns the credentials of a repository chosen in the wizard, with the
// runtime's precedence (the GitHub App when configured, else the token secret the
// wizard recorded) and the method used. Only the github provider has credentials.
// o carries the key file and the API overrides; Lookup, TokenSecret and Repo are set
// from the arguments.
func RepoTokens(repo RepoConfig, lookup func(string) (string, bool), o gitauth.Options) (gitauth.TokenSource, string, error) {
	if repo.Provider != ProviderGitHub {
		return nil, gitauth.MethodNone, nil
	}
	o.Lookup, o.TokenSecret, o.Repo = lookup, repo.TokenRef, githubRepoOf(repo.URL)
	ts, method, err := gitauth.Resolve(o)
	if err == nil && repo.Auth == gitauth.MethodApp && method != gitauth.MethodApp {
		err = errors.New("the repository was set up with a GitHub App, but its secrets are missing (" + strings.Join(gitauth.AppSecrets, ", ") + ")")
	}
	return ts, method, err
}

func (w *Wizard) repoView(ctx context.Context, r RepoConfig) *RepoView {
	v := &RepoView{Provider: r.Provider, URL: r.URL, Branch: r.Branch}
	switch {
	case r.Auth == gitauth.MethodApp:
		v.Auth = gitauth.MethodApp
		v.AppID, _ = w.secret(gitauth.AppIDSecret)
		_, hasKey := w.secret(gitauth.AppPrivateKeySecret)
		v.TokenStored = hasKey || w.opts.GitHubAppKeyFile != ""
	case r.Provider == ProviderGitHub:
		v.Auth = gitauth.MethodToken
		v.TokenStored = r.TokenRef != "" && w.opts.Secrets.Has(ctx, r.TokenRef)
	}
	return v
}

// explainRemoteErr turns a CheckRemote error into a wizard error.
func explainRemoteErr(err error, method string) *Error {
	msg := err.Error()
	e := &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Details: msg}
	switch {
	case strings.Contains(msg, "authentication") || strings.Contains(msg, "authorization"):
		if method == gitauth.MethodApp {
			e.Message = "GitHub refused the App's token for this repository."
			e.Hint = "Check that the App has the “Contents: Read and write” permission and access to this repository (App settings → Install App → Configure)."
		} else {
			e.Message = "GitHub refused the token."
			e.Hint = "Check that the token is not expired and has “Contents: Read and write” access to this repository."
		}
	case strings.Contains(msg, "not found"):
		e.Message = "The repository was not found."
		e.Hint = "Check the URL. For a private repository, the token (or the GitHub App) must have access to it. Create the repository on GitHub first (it can be empty)."
	default:
		e.Message = "Could not reach the repository."
		e.Hint = "Check the URL and the runtime's network connection."
	}
	return e
}
