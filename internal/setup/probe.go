package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/npjonath/cicord/internal/discord"
)

// Application flags: see the discord package (aliases kept for the setup API).
const (
	FlagGatewayPresence              = discord.FlagGatewayPresence
	FlagGatewayPresenceLimited       = discord.FlagGatewayPresenceLimited
	FlagGatewayGuildMembers          = discord.FlagGatewayGuildMembers
	FlagGatewayGuildMembersLimited   = discord.FlagGatewayGuildMembersLimited
	FlagGatewayMessageContent        = discord.FlagGatewayMessageContent
	FlagGatewayMessageContentLimited = discord.FlagGatewayMessageContentLimited
)

// IntentsFromFlags returns the privileged intents enabled for an application.
func IntentsFromFlags(flags int) discord.Intents { return discord.IntentsFromFlags(flags) }

// FlagsFromIntents is the inverse of IntentsFromFlags (non-limited flags; simulator).
func FlagsFromIntents(in discord.Intents) int { return discord.FlagsFromIntents(in) }

// Identity is what a bot token tells about its bot and application.
type Identity struct {
	BotUserID       string
	BotUsername     string
	BotAvatarURL    string
	ApplicationID   string
	ApplicationName string
	Flags           int
	// RedirectURIs are the OAuth2 redirects registered on the application; nil when
	// the probe cannot tell (the redirect check is then skipped).
	RedirectURIs []string
}

// ServerSummary is a server the bot is a member of.
type ServerSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IconURL string `json:"iconUrl,omitempty"`
}

// Probe reads what the setup wizard needs from a bot token, without opening a gateway
// connection.
//
// Server detection polls GET /users/@me/guilds instead of listening for gateway
// GUILD_CREATE events: it works before the privileged intents are enabled (a gateway
// identify with disallowed intents is closed with 4014), does not spend identify quota,
// does not leave a second gateway session behind when the runtime connects, and cannot
// miss a join that happened while Cicord was stopped. A 2-second poll is fast enough for a
// person clicking "Authorize" in another tab.
type Probe interface {
	Identify(ctx context.Context, token string) (Identity, error)
	Servers(ctx context.Context, token string) ([]ServerSummary, error)
}

// HTTPProbe talks to the Discord REST API.
type HTTPProbe struct {
	// BaseURL defaults to https://discord.com/api/v10 (tests: an httptest server).
	BaseURL string
	Client  *http.Client
	// CDNURL defaults to https://cdn.discordapp.com.
	CDNURL string
}

func (p *HTTPProbe) base() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "https://discord.com/api/v10"
}

func (p *HTTPProbe) cdn() string {
	if p.CDNURL != "" {
		return strings.TrimRight(p.CDNURL, "/")
	}
	return "https://cdn.discordapp.com"
}

func (p *HTTPProbe) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// CleanToken trims whitespace and a pasted "Bot " prefix.
func CleanToken(token string) string {
	t := strings.TrimSpace(token)
	t = strings.TrimSpace(strings.TrimPrefix(t, "Bot "))
	return strings.Trim(t, `"'`)
}

func (p *HTTPProbe) get(ctx context.Context, token, path string, out any) error {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base()+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+token)
		req.Header.Set("User-Agent", "DiscordBot (https://github.com/npjonath/cicord, 0)")
		resp, err := p.client().Do(req)
		if err != nil {
			return fmt.Errorf("discord %s: %w", path, err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return fmt.Errorf("discord %s: %w", path, discord.ErrInvalidToken)
		case resp.StatusCode == http.StatusTooManyRequests && attempt < 2:
			wait := time.Second
			var rl struct {
				RetryAfter float64 `json:"retry_after"`
			}
			if json.Unmarshal(body, &rl) == nil && rl.RetryAfter > 0 {
				wait = time.Duration(rl.RetryAfter * float64(time.Second))
			} else if s, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil {
				wait = time.Duration(s * float64(time.Second))
			}
			if wait > 5*time.Second {
				wait = 5 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		case resp.StatusCode == http.StatusForbidden:
			return fmt.Errorf("discord %s: HTTP 403: %w", path, discord.ErrForbidden)
		case resp.StatusCode >= 300:
			return fmt.Errorf("discord %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("discord %s: decode: %w", path, err)
		}
		return nil
	}
}

func (p *HTTPProbe) Identify(ctx context.Context, token string) (Identity, error) {
	token = CleanToken(token)
	if token == "" {
		return Identity{}, fmt.Errorf("empty token: %w", discord.ErrInvalidToken)
	}
	var me struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Avatar   string `json:"avatar"`
		Bot      bool   `json:"bot"`
	}
	if err := p.get(ctx, token, "/users/@me", &me); err != nil {
		return Identity{}, err
	}
	if !me.Bot {
		return Identity{}, errors.New("this token belongs to a user account, not a bot: copy the token from the Bot page of your application")
	}
	var app struct {
		ID           string    `json:"id"`
		Name         string    `json:"name"`
		Flags        int       `json:"flags"`
		RedirectURIs *[]string `json:"redirect_uris"`
	}
	if err := p.get(ctx, token, "/oauth2/applications/@me", &app); err != nil {
		return Identity{}, err
	}
	id := Identity{BotUserID: me.ID, BotUsername: me.Username, ApplicationID: app.ID, ApplicationName: app.Name, Flags: app.Flags}
	if app.RedirectURIs != nil {
		id.RedirectURIs = append([]string{}, *app.RedirectURIs...)
	}
	if me.Avatar != "" {
		id.BotAvatarURL = fmt.Sprintf("%s/avatars/%s/%s.png", p.cdn(), me.ID, me.Avatar)
	}
	return id, nil
}

func (p *HTTPProbe) Servers(ctx context.Context, token string) ([]ServerSummary, error) {
	var gs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	}
	if err := p.get(ctx, CleanToken(token), "/users/@me/guilds?limit=200", &gs); err != nil {
		return nil, err
	}
	out := make([]ServerSummary, 0, len(gs))
	for _, g := range gs {
		s := ServerSummary{ID: g.ID, Name: g.Name}
		if g.Icon != "" {
			s.IconURL = fmt.Sprintf("%s/icons/%s/%s.png", p.cdn(), g.ID, g.Icon)
		}
		out = append(out, s)
	}
	return out, nil
}

// ValidateClientSecret checks an OAuth2 client secret with the client-credentials grant
// (discord.ErrInvalidToken when Discord rejects it).
func (p *HTTPProbe) ValidateClientSecret(ctx context.Context, clientID, secret string) error {
	form := strings.NewReader("grant_type=client_credentials&scope=identify")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base()+"/oauth2/token", form)
	if err != nil {
		return err
	}
	req.SetBasicAuth(clientID, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("discord oauth2/token: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest:
		return fmt.Errorf("discord oauth2/token: HTTP %d: %w", resp.StatusCode, discord.ErrInvalidToken)
	case resp.StatusCode >= 300:
		return fmt.Errorf("discord oauth2/token: HTTP %d", resp.StatusCode)
	}
	return nil
}
