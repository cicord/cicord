package writeback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cicord/cicord/internal/config"
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

// TestMixedPathsOneChange: a plugin change touching paths with different write-back
// policies (community: bots/** and plugins/** commit, the rest a pull request) is one
// change, in the strictest mode: the bot file, cicord.lock and the portal's route types
// land in the same commit of one pull request, whose body keeps the commit message body.
func TestMixedPathsOneChange(t *testing.T) {
	wb := &config.WriteBack{DefaultMode: ModePullRequest, Policies: []config.WriteBackPolicy{
		{Paths: []string{"bots/**", "plugins/**"}, Mode: ModeCommit},
	}}
	paths := []string{"bots/moderator/bot.yaml", "cicord.lock", "portal/src/cicord-routes.d.ts"}
	if got := ModeFor(wb, paths); got != ModePullRequest {
		t.Fatalf("mode of %v = %s", paths, got)
	}
	if got := ModeFor(wb, []string{"bots/moderator/bot.yaml", "plugins/welcome/plugin.yaml"}); got != ModeCommit {
		t.Fatalf("bots and plugins only = %s", got)
	}

	gh := githubtest.New(t)
	app, err := gitauth.NewGitHubApp(gitauth.AppConfig{AppID: gh.AppID, PrivateKey: gh.KeyPEM, Repo: gh.Repo, API: gh.URL})
	if err != nil {
		t.Fatal(err)
	}
	bare := bareRepo(t)
	w := NewGitWriter(GitOptions{URL: bare, Branch: "main", WorkDir: filepath.Join(t.TempDir(), "wc"), Tokens: app,
		GitHubRepo: gh.Repo, GitHubAPI: gh.URL})
	set := func(content string) EditFunc { return func([]byte) ([]byte, error) { return []byte(content), nil } }
	res, err := w.Write(context.Background(), Request{
		Edits: map[string]EditFunc{
			"bots/moderator/bot.yaml":       PluginConfigEdit("welcome", "", map[string]any{"channel": "general"}),
			"cicord.lock":                   set("plugins: {}\n"),
			"portal/src/cicord-routes.d.ts": set("// generated\n"),
		},
		Message: "plugin: update cicord/tickets 0.2.0 → 0.2.1\n\nRegenerated portal/src/cicord-routes.d.ts (cicord portal typegen).",
		Mode:    ModeFor(wb, paths),
	})
	if err != nil {
		t.Fatal(err)
	}
	pulls := gh.Pulls()
	if res.Mode != ModePullRequest || len(pulls) != 1 {
		t.Fatalf("result = %+v, pulls = %+v", res, pulls)
	}
	body, _ := pulls[0].Body["body"].(string)
	if pulls[0].Body["title"] != "plugin: update cicord/tickets 0.2.0 → 0.2.1" ||
		!strings.Contains(body, "Regenerated portal/src/cicord-routes.d.ts (cicord portal typegen).") ||
		!strings.Contains(body, "Cicord-Source: api") {
		t.Fatalf("pull request = %+v", pulls[0].Body)
	}
	r, err := git.PlainOpen(bare)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Reference(plumbing.NewBranchReferenceName(res.Branch), true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.CommitObject(ref.Hash())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := c.Parent(0)
	if err != nil {
		t.Fatal(err)
	}
	if parent.NumParents() != 0 {
		t.Fatal("the change is more than one commit")
	}
	changes, err := object.DiffTree(mustTree(t, parent), mustTree(t, c))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ch := range changes {
		got = append(got, ch.To.Name)
	}
	if strings.Join(got, ",") != "bots/moderator/bot.yaml,cicord.lock,portal/src/cicord-routes.d.ts" {
		t.Fatalf("files of the commit = %v", got)
	}
}

func mustTree(t *testing.T, c *object.Commit) *object.Tree {
	t.Helper()
	tr, err := c.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return tr
}
