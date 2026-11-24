package setup

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type redirectProbe struct{ uris []string }

func (p redirectProbe) Identify(context.Context, string) (Identity, error) {
	return Identity{ApplicationID: "999", RedirectURIs: p.uris}, nil
}

func (redirectProbe) Servers(context.Context, string) ([]ServerSummary, error) { return nil, nil }

// Seen live: the application only listed the redirect of another port, and Discord
// answered "invalid redirect_uri" at the first admin login. The wizard must say so
// when the client secret is saved.
func TestCheckRedirect(t *testing.T) {
	bot := &BotState{SecretRef: "DISCORD_TOKEN_GARDIEN", ApplicationID: "999"}
	env := func(name string) (string, bool) { return "tok", name == bot.SecretRef }
	cases := []struct {
		name string
		uris []string
		ok   bool
	}{
		{"registered", []string{"http://127.0.0.1:7420/auth/callback", "http://127.0.0.1:7421/auth/callback"}, true},
		{"other port only", []string{"http://127.0.0.1:7420/auth/callback"}, false},
		{"none registered", []string{}, false},
		{"unknown (simulator)", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := New(Options{Probe: redirectProbe{c.uris}, Env: env, PublicURL: "http://127.0.0.1:7421/"})
			err := w.checkRedirect(context.Background(), bot)
			var e *Error
			switch {
			case c.ok && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case !c.ok && (!errors.As(err, &e) || e.Code != "redirect_missing"):
				t.Fatalf("err = %v, want redirect_missing", err)
			}
		})
	}
}

func TestPruneEmptyDropsBlankListRows(t *testing.T) {
	got := pruneEmpty(map[string]any{"roles": []any{"modo", "", nil}, "empty": []any{""}, "name": ""})
	if r, ok := got["roles"].([]any); !ok || len(r) != 1 || r[0] != "modo" || len(got) != 1 {
		t.Errorf("pruneEmpty = %v", got)
	}
}

// With a separate admin URL (serve --admin-url), Discord must know both callbacks: the
// portal login uses the public one, the admin console (and the wizard) the admin one.
func TestCheckRedirectAdminURL(t *testing.T) {
	const (
		public = "https://community.example.org/auth/callback"
		admin  = "https://cicord.example.ts.net/auth/callback"
	)
	bot := &BotState{SecretRef: "DISCORD_TOKEN_GARDIEN", ApplicationID: "999"}
	env := func(name string) (string, bool) { return "tok", name == bot.SecretRef }
	cases := []struct {
		name    string
		uris    []string
		missing []string
	}{
		{"both", []string{public, admin}, nil},
		{"public only", []string{public}, []string{admin}},
		{"admin only", []string{admin}, []string{public}},
		{"none", []string{}, []string{public, admin}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := New(Options{Probe: redirectProbe{c.uris}, Env: env, PublicURL: "https://community.example.org/",
				AdminURL: "https://cicord.example.ts.net"})
			err := w.checkRedirect(context.Background(), bot)
			if c.missing == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) || e.Code != "redirect_missing" {
				t.Fatalf("err = %v, want redirect_missing", err)
			}
			for _, u := range []string{public, admin} {
				want := slices.Contains(c.missing, u)
				if got := strings.Contains(e.Hint, u) && strings.Contains(e.Details, u); got != want {
					t.Errorf("%s named as missing = %v, want %v (hint %q, details %q)", u, got, want, e.Hint, e.Details)
				}
			}
		})
	}
	w := New(Options{PublicURL: "https://community.example.org", AdminURL: "https://cicord.example.ts.net"})
	if w.redirectURL() != admin || !slices.Equal(w.redirectURLs(), []string{public, admin}) {
		t.Fatalf("redirectURL = %q, redirectURLs = %v", w.redirectURL(), w.redirectURLs())
	}
	w = New(Options{PublicURL: "https://community.example.org"})
	if w.redirectURL() != public || !slices.Equal(w.redirectURLs(), []string{public}) {
		t.Fatalf("without admin URL: redirectURL = %q, redirectURLs = %v", w.redirectURL(), w.redirectURLs())
	}
}
