package writeback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/gitauth"
	"github.com/cicord/cicord/internal/gitauth/githubtest"
)

// bareRepo returns a bare repository with bots/moderator/bot.yaml on main.
func bareRepo(t *testing.T) string {
	t.Helper()
	upstream := t.TempDir()
	repo, err := git.PlainInitWithOptions(upstream, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	p := filepath.Join(upstream, "bots/moderator/bot.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(botYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("bots/moderator/bot.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "dev", Email: "d@x", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	bare := t.TempDir()
	if _, err := git.PlainClone(bare, true, &git.CloneOptions{URL: upstream}); err != nil {
		t.Fatal(err)
	}
	return bare
}

// TestPullRequestWithGitHubApp opens a pull request with an installation token of a
// GitHub App: the git operations and the REST call ask the token source each time.
func TestPullRequestWithGitHubApp(t *testing.T) {
	gh := githubtest.New(t)
	app, err := gitauth.NewGitHubApp(gitauth.AppConfig{AppID: gh.AppID, PrivateKey: gh.KeyPEM, Repo: gh.Repo, API: gh.URL})
	if err != nil {
		t.Fatal(err)
	}
	w := NewGitWriter(GitOptions{URL: bareRepo(t), Branch: "main", WorkDir: filepath.Join(t.TempDir(), "wc"), Tokens: app,
		GitHubRepo: gh.Repo, GitHubAPI: gh.URL})
	res, err := w.Write(context.Background(), Request{
		Edits:   map[string]EditFunc{"bots/moderator/bot.yaml": PluginConfigEdit("welcome", "", map[string]any{"channel": "general"})},
		Message: "config: via PR", Mode: ModePullRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.PullRequestURL != "https://github.com/acme/community/pull/1" {
		t.Fatalf("result = %+v", res)
	}
	pulls := gh.Pulls()
	if len(pulls) != 1 || pulls[0].Authorization != "Bearer inst-token-1" || pulls[0].Path != "/repos/acme/community/pulls" ||
		!strings.HasPrefix(pulls[0].Body["head"].(string), "cicord/change-") {
		t.Fatalf("pulls = %+v", pulls)
	}
	if gh.Exchanges() != 1 {
		t.Fatalf("exchanges = %d (the installation token is cached)", gh.Exchanges())
	}
}

// TestGitWriterCredentialError: a failing token source aborts the write before any push.
func TestGitWriterCredentialError(t *testing.T) {
	gh := githubtest.New(t)
	gh.Repo = "acme/other" // the App is not installed on acme/community
	app, err := gitauth.NewGitHubApp(gitauth.AppConfig{AppID: gh.AppID, PrivateKey: gh.KeyPEM, Repo: "acme/community", API: gh.URL})
	if err != nil {
		t.Fatal(err)
	}
	w := NewGitWriter(GitOptions{URL: bareRepo(t), Branch: "main", WorkDir: filepath.Join(t.TempDir(), "wc"), Tokens: app})
	_, err = w.Write(context.Background(), Request{
		Edits: map[string]EditFunc{"x.txt": func([]byte) ([]byte, error) { return []byte("x"), nil }}, Message: "x", Mode: ModeCommit,
	})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
}
