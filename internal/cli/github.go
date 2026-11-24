package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// prComment posts or updates the Cicord comment of a GitHub pull request.
type prComment struct {
	api    string // GitHub API base (GITHUB_API_URL, default https://api.github.com)
	token  string
	repo   string // owner/name
	number int
}

// errNotPullRequest means the run has no pull request to comment on.
var errNotPullRequest = errors.New("not running for a pull request")

// githubFromEnv reads the GitHub Actions environment. pr > 0 overrides the pull request
// number of GITHUB_EVENT_PATH.
func githubFromEnv(pr int) (*prComment, error) {
	c := &prComment{
		api:   strings.TrimRight(os.Getenv("GITHUB_API_URL"), "/"),
		token: os.Getenv("GITHUB_TOKEN"),
		repo:  os.Getenv("GITHUB_REPOSITORY"),
	}
	if c.api == "" {
		c.api = "https://api.github.com"
	}
	c.number = pr
	if c.number == 0 {
		p := os.Getenv("GITHUB_EVENT_PATH")
		if p == "" {
			return nil, errNotPullRequest
		}
		data, err := os.ReadFile(p) //nolint:gosec // G703: GITHUB_EVENT_PATH is set by the GitHub Actions runner
		if err != nil {
			return nil, fmt.Errorf("read GITHUB_EVENT_PATH: %w", err)
		}
		var ev struct {
			Number      int `json:"number"`
			PullRequest *struct {
				Number int `json:"number"`
			} `json:"pull_request"`
			Issue *struct {
				Number      int             `json:"number"`
				PullRequest json.RawMessage `json:"pull_request"`
			} `json:"issue"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil, fmt.Errorf("parse GITHUB_EVENT_PATH: %w", err)
		}
		switch {
		case ev.PullRequest != nil && ev.PullRequest.Number > 0:
			c.number = ev.PullRequest.Number
		case ev.Issue != nil && len(ev.Issue.PullRequest) > 0 && ev.Issue.Number > 0:
			c.number = ev.Issue.Number
		case ev.PullRequest != nil && ev.Number > 0:
			c.number = ev.Number
		default:
			return nil, errNotPullRequest
		}
	}
	if c.repo == "" || !strings.Contains(c.repo, "/") {
		return nil, errors.New("GITHUB_REPOSITORY (owner/name) is not set")
	}
	if c.token == "" {
		return nil, errors.New("GITHUB_TOKEN is not set (the workflow needs `permissions: pull-requests: write`)")
	}
	return c, nil
}

type ghComment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Type string `json:"type"` // "Bot" for github-actions[bot] / GitHub Apps
	} `json:"user"`
}

// ghError is an error answer of the GitHub API.
type ghError struct {
	Status int
	Msg    string
}

func (e *ghError) Error() string { return e.Msg }

func (c *prComment) request(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(data))
		}
		return &ghError{Status: resp.StatusCode, Msg: fmt.Sprintf("GitHub %s %s: HTTP %d: %s", method, path, resp.StatusCode, e.Message)}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// upsert updates the bot comment containing marker, or creates one. Comments written by
// people (quoting the marker) are never edited. It returns the comment URL and whether
// it was created.
func (c *prComment) upsert(ctx context.Context, marker, body string) (string, bool, error) {
	issue := "/repos/" + c.repo + "/issues/" + strconv.Itoa(c.number)
	var existing *ghComment
	for page := 1; page <= 50 && existing == nil; page++ {
		var list []ghComment
		if err := c.request(ctx, http.MethodGet, fmt.Sprintf("%s/comments?per_page=100&page=%d", issue, page), nil, &list); err != nil {
			return "", false, err
		}
		for i := range list {
			if strings.Contains(list[i].Body, marker) && (list[i].User.Type == "" || list[i].User.Type == "Bot") {
				existing = &list[i]
				break
			}
		}
		if len(list) < 100 {
			break
		}
	}
	var out ghComment
	if existing != nil {
		err := c.request(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", c.repo, existing.ID), map[string]string{"body": body}, &out)
		var ge *ghError
		if !errors.As(err, &ge) || (ge.Status != http.StatusForbidden && ge.Status != http.StatusNotFound) {
			return out.HTMLURL, false, err
		}
		// not ours to edit (another bot / app): post a new comment instead
	}
	err := c.request(ctx, http.MethodPost, issue+"/comments", map[string]string{"body": body}, &out)
	return out.HTMLURL, true, err
}
