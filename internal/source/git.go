package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

type GitOptions struct {
	URL    string // remote URL (https://…, file://…, or a local path)
	Branch string // default "main"
	// Path is the sub-directory containing cicord.yaml (monorepos). Default ".".
	Path string
	// CacheDir holds the mirror clone and the revision snapshots.
	CacheDir string
	// Token for HTTPS auth (GitHub token / app installation token). Optional.
	Token string
	// PollInterval triggers periodic fetches (0 disables polling).
	PollInterval time.Duration
}

// Git tracks a branch of a git repository. Revisions are commits.
type Git struct {
	opts    GitOptions
	changes chan struct{}
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	repo    *git.Repository
}

func NewGit(opts GitOptions) (*Git, error) {
	if opts.URL == "" {
		return nil, errors.New("git source: URL is required")
	}
	if opts.Branch == "" {
		opts.Branch = "main"
	}
	if opts.Path == "" {
		opts.Path = "."
	}
	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return nil, err
	}
	g := &Git{opts: opts, changes: make(chan struct{}, 1), done: make(chan struct{})}
	if opts.PollInterval > 0 {
		go func() {
			t := time.NewTicker(opts.PollInterval)
			defer t.Stop()
			for {
				select {
				case <-g.done:
					return
				case <-t.C:
					notify(g.changes)
				}
			}
		}()
	}
	return g, nil
}

func (g *Git) Changes() <-chan struct{} { return g.changes }
func (g *Git) Trigger()                 { notify(g.changes) }
func (g *Git) Close() error {
	g.once.Do(func() { close(g.done) })
	return nil
}

func (g *Git) auth() transport.AuthMethod {
	if g.opts.Token == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: g.opts.Token}
}

func (g *Git) mirrorDir() string { return filepath.Join(g.opts.CacheDir, "mirror.git") }

func (g *Git) open() (*git.Repository, error) {
	if g.repo != nil {
		return g.repo, nil
	}
	r, err := git.PlainOpen(g.mirrorDir())
	if errors.Is(err, git.ErrRepositoryNotExists) {
		r, err = git.PlainInit(g.mirrorDir(), true)
		if err == nil {
			_, err = r.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{g.opts.URL}})
		}
	}
	if err != nil {
		return nil, err
	}
	g.repo = r
	return r, nil
}

// Fetch fetches the tracked branch and snapshots its head commit.
func (g *Git) Fetch(ctx context.Context) (Revision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, err := g.open()
	if err != nil {
		return Revision{}, err
	}
	refspec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", g.opts.Branch, g.opts.Branch))
	err = r.FetchContext(ctx, &git.FetchOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{refspec}, Auth: g.auth(), Force: true, Tags: git.NoTags})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return Revision{}, fmt.Errorf("git fetch %s: %w", g.opts.URL, err)
	}
	ref, err := r.Reference(plumbing.NewRemoteReferenceName("origin", g.opts.Branch), true)
	if err != nil {
		return Revision{}, fmt.Errorf("branch %q not found on %s: %w", g.opts.Branch, g.opts.URL, err)
	}
	return g.snapshot(r, ref.Hash())
}

// Snapshot exports an arbitrary commit (used for plans of other revisions).
func (g *Git) Snapshot(sha string) (Revision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, err := g.open()
	if err != nil {
		return Revision{}, err
	}
	return g.snapshot(r, plumbing.NewHash(sha))
}

func (g *Git) snapshot(r *git.Repository, h plumbing.Hash) (Revision, error) {
	c, err := r.CommitObject(h)
	if err != nil {
		return Revision{}, err
	}
	sha := h.String()
	rev := Revision{
		ID:      sha[:7],
		SHA:     sha,
		Message: strings.TrimSpace(firstLine(c.Message)),
		Author:  c.Author.Name,
		Time:    c.Committer.When.UTC(),
		Source:  "git",
	}
	snapRoot := filepath.Join(g.opts.CacheDir, "revisions", sha)
	rev.Dir = filepath.Join(snapRoot, filepath.FromSlash(path.Clean(g.opts.Path)))
	if _, err := os.Stat(filepath.Join(snapRoot, ".complete")); err == nil {
		return rev, nil
	}
	tree, err := c.Tree()
	if err != nil {
		return Revision{}, err
	}
	tmp, err := os.MkdirTemp(g.opts.CacheDir, ".tmp-")
	if err != nil {
		return Revision{}, err
	}
	err = tree.Files().ForEach(func(f *object.File) error {
		if f.Mode == filemode.Symlink || f.Mode == filemode.Submodule {
			return nil
		}
		clean := path.Clean(f.Name)
		if strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return fmt.Errorf("unsafe path in repository: %q", f.Name)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rd, err := f.Reader()
		if err != nil {
			return err
		}
		defer rd.Close()
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rd); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err == nil {
		err = os.WriteFile(filepath.Join(tmp, ".complete"), nil, 0o644)
	}
	if err != nil {
		os.RemoveAll(tmp)
		return Revision{}, err
	}
	if err := os.MkdirAll(filepath.Dir(snapRoot), 0o755); err != nil {
		os.RemoveAll(tmp)
		return Revision{}, err
	}
	if err := os.Rename(tmp, snapRoot); err != nil {
		os.RemoveAll(tmp)
		if _, statErr := os.Stat(filepath.Join(snapRoot, ".complete")); statErr != nil {
			return Revision{}, err
		}
	}
	if _, err := os.Stat(filepath.Join(rev.Dir, "cicord.yaml")); err != nil {
		return rev, fmt.Errorf("commit %s has no cicord.yaml in %q", rev.ID, g.opts.Path)
	}
	return rev, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
