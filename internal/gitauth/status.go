package gitauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Commit status states (GitHub also has "error", unused).
const (
	StatePending = "pending"
	StateSuccess = "success"
	StateFailure = "failure"
)

// StatusContext is the context of the commit statuses Cicord posts.
const StatusContext = "cicord/sync"

// maxDescription is GitHub's limit for a commit status description.
const maxDescription = 140

// CommitStatuses posts GitHub commit statuses on the revisions the runtime evaluates.
// Posting never fails the caller: errors are logged.
type CommitStatuses struct {
	Repo   string // owner/name
	Tokens TokenSource
	// TargetURL is the link of the status ("" for none): the admin UI.
	TargetURL string
	// API overrides https://api.github.com.
	API        string
	HTTPClient *http.Client
	Logger     *slog.Logger
	// Timeout bounds one post (default 10s).
	Timeout time.Duration

	// denied is set after a permission error: later ones are logged at debug level only
	// (a token without the Commit statuses permission would warn on every revision).
	denied atomic.Bool
}

// errDenied marks a permission error of the statuses API.
type errDenied struct{ msg string }

func (e errDenied) Error() string { return e.msg }

// Async returns a Report function that queues the statuses and posts them in order from
// a background goroutine, so that the caller (the runtime activating a revision) never
// waits for GitHub. When more than queue statuses are waiting, new ones are dropped and
// logged at debug level; the goroutine lives as long as the process.
func (c *CommitStatuses) Async(queue int) func(ctx context.Context, sha, state, description string) {
	if queue <= 0 {
		queue = 64
	}
	type item struct{ sha, state, description string }
	ch := make(chan item, queue)
	go func() {
		for it := range ch {
			c.Report(context.Background(), it.sha, it.state, it.description)
		}
	}()
	return func(ctx context.Context, sha, state, description string) {
		select {
		case ch <- item{sha, state, description}:
		default:
			log := c.Logger
			if log == nil {
				log = slog.Default()
			}
			log.DebugContext(ctx, "GitHub commit status dropped: queue full", "sha", short(sha), "state", state)
		}
	}
}

// Report sets the cicord/sync status of sha. It blocks for at most Timeout, so that
// consecutive states of a revision reach GitHub in order.
func (c *CommitStatuses) Report(ctx context.Context, sha, state, description string) {
	if c == nil || c.Repo == "" || c.Tokens == nil || sha == "" {
		return
	}
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	err := c.post(ctx, sha, state, description)
	if err == nil {
		return
	}
	level := slog.LevelWarn
	var denied errDenied
	if errors.As(err, &denied) && c.denied.Swap(true) {
		level = slog.LevelDebug
	}
	log.Log(ctx, level, "GitHub commit status not set", "repo", c.Repo, "sha", short(sha), "state", state, "err", err)
}

func (c *CommitStatuses) post(ctx context.Context, sha, state, description string) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	auth, err := Bearer(ctx, c.Tokens)
	if err != nil {
		return err
	}
	body := map[string]string{"state": state, "context": StatusContext, "description": Truncate(description, maxDescription)}
	if c.TargetURL != "" {
		body["target_url"] = c.TargetURL
	}
	payload, _ := json.Marshal(body)
	api := strings.TrimRight(c.API, "/")
	if api == "" {
		api = DefaultAPI
	}
	u, err := url.JoinPath(api, "repos", c.Repo, "statuses", sha)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	msg := APIError(resp.Status, data)
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return errDenied{msg + " (the token needs the “Commit statuses: Read and write” permission on " + c.Repo + ")"}
	}
	return errors.New(msg)
}

// Truncate shortens s to at most n runes (an ellipsis marks the cut), first line only.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
