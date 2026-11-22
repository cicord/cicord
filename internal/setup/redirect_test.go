package setup

import (
	"context"
	"errors"
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
