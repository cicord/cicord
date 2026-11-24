package cli

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strings"

	"github.com/cicord/cicord/internal/gitauth"
)

// logRepoAuth logs how the configuration repository authenticates (never the secret).
func logRepoAuth(log *slog.Logger, method, remote string) {
	switch method {
	case gitauth.MethodApp:
		log.Info("configuration repository: GitHub App authentication", "repo", remote)
	case gitauth.MethodToken:
		log.Info("configuration repository: token authentication ("+gitauth.TokenSecret+")", "repo", remote)
	}
}

// commitStatusHook returns the runtime hook that sets the cicord/sync commit status
// on the revisions of a GitHub repository (nil without a repository or credentials).
func commitStatusHook(log *slog.Logger, ghRepo string, tokens gitauth.TokenSource, publicURL string) func(context.Context, string, string, string) {
	if ghRepo == "" || tokens == nil {
		return nil
	}
	cs := &gitauth.CommitStatuses{Repo: ghRepo, Tokens: tokens, TargetURL: adminURL(publicURL), Logger: log}
	// Posted from a background queue: activating a revision never waits for GitHub.
	return cs.Async(64)
}

// adminURL is the admin UI of a public URL, or "" when the URL is a loopback address
// (a link GitHub users could not open).
func adminURL(public string) string {
	u, err := url.Parse(strings.TrimRight(public, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") || strings.HasSuffix(strings.ToLower(h), ".localhost") {
		return ""
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	return u.String() + "/admin/"
}
