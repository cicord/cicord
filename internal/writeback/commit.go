package writeback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/npjonath/codi/internal/config"
)

const (
	ModeCommit      = "commit"
	ModePullRequest = "pullRequest"
)

// EditFunc computes the new content of a file from its current content (nil when the
// file does not exist). It is called on the latest version of the branch, so edits are
// rebased automatically; returning an error aborts the change.
type EditFunc func(current []byte) ([]byte, error)

type Request struct {
	Edits   map[string]EditFunc // repo-relative path (slash separated) → edit
	Message string              // first line + optional body
	Actor   string              // "discord:<id> (<name>)"
	Source  string              // "admin-ui" | "api" | "cli"
	Mode    string              // ModeCommit | ModePullRequest
}

type Result struct {
	Mode           string `json:"mode"`
	Revision       string `json:"revision"` // short SHA, empty for PRs and non-git writes
	SHA            string `json:"-"`
	PullRequestURL string `json:"pullRequestUrl"`
	Branch         string `json:"-"`
}

// Writer applies requests.
type Writer interface {
	Write(ctx context.Context, req Request) (Result, error)
}

// ErrConflict is returned when an edit cannot be applied on the latest branch head.
var ErrConflict = errors.New("writeback: conflict")

// ModeFor picks the write mode for a set of changed paths from codi.yaml policies:
// first matching policy per path, strictest mode across paths, defaultMode otherwise.
func ModeFor(wb *config.WriteBack, paths []string) string {
	def := ModePullRequest
	if wb != nil && wb.DefaultMode != "" {
		def = wb.DefaultMode
	}
	mode := ModeCommit
	for _, p := range paths {
		m := def
		if wb != nil {
		policies:
			for _, pol := range wb.Policies {
				for _, g := range pol.Paths {
					if globMatch(g, p) {
						m = pol.Mode
						break policies
					}
				}
			}
		}
		if m == ModePullRequest {
			mode = ModePullRequest
		}
	}
	return mode
}

// globMatch supports `**` (any path, including separators), `*` and `?` (within a segment).
func globMatch(glob, p string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			b.WriteString(".*")
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString("/?")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(p)
}

func trailers(req Request) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(req.Message))
	b.WriteString("\n\n")
	if req.Actor != "" {
		fmt.Fprintf(&b, "Codi-Actor: %s\n", req.Actor)
	}
	src := req.Source
	if src == "" {
		src = "api"
	}
	fmt.Fprintf(&b, "Codi-Source: %s\n", src)
	return b.String()
}

// ───────────────────────────────────────────────────────────── local directory (dev)

// DirWriter writes files straight into a working directory (`codi dev`). There is no
// commit; the local source picks the change up like any edit.
type DirWriter struct {
	Root string
	mu   sync.Mutex
}

func (w *DirWriter) Write(ctx context.Context, req Request) (Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string][]byte{}
	for rel, edit := range req.Edits {
		p, err := safeJoin(w.Root, rel)
		if err != nil {
			return Result{}, err
		}
		cur, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		next, err := edit(cur)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %s: %v", ErrConflict, rel, err)
		}
		out[p] = next
	}
	for p, data := range out {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return Result{}, err
		}
	}
	return Result{Mode: ModeCommit}, nil
}

func safeJoin(root, rel string) (string, error) {
	p := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the repository", rel)
	}
	return p, nil
}

// ───────────────────────────────────────────────────────────── git

type GitOptions struct {
	URL     string // remote URL
	Branch  string // target branch (default main)
	Path    string // sub-directory containing codi.yaml
	WorkDir string // working clone location
	Token   string // HTTPS token (also used for the GitHub API)
	Author  string // default "codi[bot]"
	Email   string // default "codi[bot]@users.noreply.github.com"
	// GitHubRepo "owner/name" enables pull requests through the GitHub API.
	GitHubRepo string
	// GitHubAPI overrides https://api.github.com (tests).
	GitHubAPI  string
	HTTPClient *http.Client
}

// GitWriter commits on a working clone and pushes to the remote.
type GitWriter struct {
	opts GitOptions
	mu   sync.Mutex
}

func NewGitWriter(opts GitOptions) *GitWriter {
	if opts.Branch == "" {
		opts.Branch = "main"
	}
	if opts.Path == "" {
		opts.Path = "."
	}
	if opts.Author == "" {
		opts.Author = "codi[bot]"
	}
	if opts.Email == "" {
		opts.Email = "codi[bot]@users.noreply.github.com"
	}
	if opts.GitHubAPI == "" {
		opts.GitHubAPI = "https://api.github.com"
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitWriter{opts: opts}
}

func (w *GitWriter) auth() *githttp.BasicAuth {
	if w.opts.Token == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: w.opts.Token}
}

func (w *GitWriter) clone(ctx context.Context) (*git.Repository, error) {
	r, err := git.PlainOpen(w.opts.WorkDir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		var a *githttp.BasicAuth = w.auth()
		co := &git.CloneOptions{URL: w.opts.URL, ReferenceName: plumbing.NewBranchReferenceName(w.opts.Branch), SingleBranch: true}
		if a != nil {
			co.Auth = a
		}
		return git.PlainCloneContext(ctx, w.opts.WorkDir, false, co)
	}
	return r, err
}

func (w *GitWriter) Write(ctx context.Context, req Request) (Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	r, err := w.clone(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("writeback clone: %w", err)
	}
	fo := &git.FetchOptions{RemoteName: "origin", Force: true,
		RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", w.opts.Branch, w.opts.Branch))}}
	if a := w.auth(); a != nil {
		fo.Auth = a
	}
	if err := r.FetchContext(ctx, fo); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return Result{}, fmt.Errorf("writeback fetch: %w", err)
	}
	head, err := r.Reference(plumbing.NewRemoteReferenceName("origin", w.opts.Branch), true)
	if err != nil {
		return Result{}, err
	}
	wt, err := r.Worktree()
	if err != nil {
		return Result{}, err
	}
	branch := w.opts.Branch
	if req.Mode == ModePullRequest {
		branch = fmt.Sprintf("codi/change-%d", time.Now().UnixNano())
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: head.Hash(), Branch: plumbing.NewBranchReferenceName(branch), Force: true, Create: !w.branchExists(r, branch)}); err != nil {
		// existing local branch: reset it to the remote head
		if err := wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch), Force: true}); err != nil {
			return Result{}, err
		}
		if err := wt.Reset(&git.ResetOptions{Commit: head.Hash(), Mode: git.HardReset}); err != nil {
			return Result{}, err
		}
	}
	base := filepath.Join(w.opts.WorkDir, filepath.FromSlash(w.opts.Path))
	for rel, edit := range req.Edits {
		p, err := safeJoin(base, rel)
		if err != nil {
			return Result{}, err
		}
		cur, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		next, err := edit(cur)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %s: %v", ErrConflict, rel, err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(p, next, 0o644); err != nil {
			return Result{}, err
		}
		inRepo, _ := filepath.Rel(w.opts.WorkDir, p)
		if _, err := wt.Add(filepath.ToSlash(inRepo)); err != nil {
			return Result{}, err
		}
	}
	st, err := wt.Status()
	if err != nil {
		return Result{}, err
	}
	if st.IsClean() {
		return Result{Mode: req.Mode, Revision: head.Hash().String()[:7], SHA: head.Hash().String()}, nil
	}
	sig := &object.Signature{Name: w.opts.Author, Email: w.opts.Email, When: time.Now()}
	h, err := wt.Commit(trailers(req), &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		return Result{}, err
	}
	po := &git.PushOptions{RemoteName: "origin",
		RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))}}
	if a := w.auth(); a != nil {
		po.Auth = a
	}
	if err := r.PushContext(ctx, po); err != nil {
		if strings.Contains(err.Error(), "non-fast-forward") {
			return Result{}, fmt.Errorf("%w: branch moved during the write, retry", ErrConflict)
		}
		return Result{}, fmt.Errorf("writeback push: %w", err)
	}
	res := Result{Mode: req.Mode, SHA: h.String(), Branch: branch}
	if req.Mode == ModeCommit {
		res.Revision = h.String()[:7]
		return res, nil
	}
	if w.opts.GitHubRepo != "" {
		u, err := w.openPR(ctx, branch, req)
		if err != nil {
			return res, err
		}
		res.PullRequestURL = u
	}
	return res, nil
}

func (w *GitWriter) branchExists(r *git.Repository, branch string) bool {
	_, err := r.Reference(plumbing.NewBranchReferenceName(branch), false)
	return err == nil
}

func (w *GitWriter) openPR(ctx context.Context, branch string, req Request) (string, error) {
	title := strings.SplitN(strings.TrimSpace(req.Message), "\n", 2)[0]
	body := "Change proposed from the CODI admin UI.\n\n" + strings.TrimSpace(strings.TrimPrefix(trailers(req), strings.TrimSpace(req.Message)))
	payload, _ := json.Marshal(map[string]string{"title": title, "head": branch, "base": w.opts.Branch, "body": body})
	u := fmt.Sprintf("%s/repos/%s/pulls", strings.TrimRight(w.opts.GitHubAPI, "/"), url.PathEscape(w.opts.GitHubRepo))
	u = strings.Replace(u, "%2F", "/", 1)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	hreq.Header.Set("Accept", "application/vnd.github+json")
	hreq.Header.Set("Authorization", "Bearer "+w.opts.Token)
	resp, err := w.opts.HTTPClient.Do(hreq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("github: create pull request: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal(data, &out)
	return out.HTMLURL, nil
}
