package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sender delivers alert messages.
type Sender interface {
	Send(ctx context.Context, msgs []Message) error
}

// maxEmbeds is Discord's limit of embeds per webhook message.
const maxEmbeds = 10

// Webhook posts alerts to a webhook URL: a Discord webhook (discord.com/api/webhooks/…)
// gets embeds, any other URL a JSON document (see JSONPayload). The URL is a secret
// (whoever has it can post): it never appears in errors or logs.
type Webhook struct {
	url     string
	discord bool
	client  *http.Client
	retry   time.Duration // first retry delay (doubles)
}

// NewWebhook validates raw: https, or http on a loopback host (tests, a local relay).
func NewWebhook(raw string, client *http.Client) (*Webhook, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid alert webhook URL (expected https://…)")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return nil, errors.New("the alert webhook URL must use https (http is only allowed for localhost)")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Webhook{url: u.String(), discord: IsDiscordWebhook(u), client: client, retry: 2 * time.Second}, nil
}

// Describe names the target without its secret part (logs, `cicord alert test`).
func (w *Webhook) Describe() string {
	u, _ := url.Parse(w.url)
	if w.discord {
		return "Discord webhook on " + u.Host
	}
	return "JSON webhook on " + u.Host
}

// IsDiscordWebhook reports whether u is a Discord webhook execution URL.
func IsDiscordWebhook(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	switch h {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
	default:
		return false
	}
	return strings.HasPrefix(u.Path, "/api/webhooks/") || strings.HasPrefix(u.Path, "/api/v10/webhooks/")
}

func isLoopback(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Send posts msgs, in chunks of maxEmbeds (one webhook call per chunk).
func (w *Webhook) Send(ctx context.Context, msgs []Message) error {
	for len(msgs) > 0 {
		n := min(len(msgs), maxEmbeds)
		var body any
		if w.discord {
			body = DiscordPayload(msgs[:n])
		} else {
			body = JSONPayload(msgs[:n])
		}
		if err := w.post(ctx, body); err != nil {
			return err
		}
		msgs = msgs[n:]
	}
	return nil
}

// post sends one payload; a 429 is retried after its Retry-After (at most 30 s), a 5xx or
// a network error up to twice more.
func (w *Webhook) post(ctx context.Context, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var last error
	for attempt := range 3 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(data))
		if err != nil {
			return errors.New("alert webhook: invalid request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "cicord-alerts")
		res, err := w.client.Do(req)
		wait := w.retry << attempt
		switch {
		case err != nil:
			last = fmt.Errorf("alert webhook: %w", stripURL(err))
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			if res.StatusCode < 300 {
				return nil
			}
			last = fmt.Errorf("alert webhook answered %s", res.Status)
			if res.StatusCode == http.StatusTooManyRequests {
				if s, err := strconv.ParseFloat(res.Header.Get("Retry-After"), 64); err == nil && s > 0 {
					wait = min(time.Duration(s*float64(time.Second)), 30*time.Second)
				}
			} else if res.StatusCode < 500 {
				return last // the payload or the URL is wrong: retrying does not help
			}
		}
		if attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(wait):
		}
	}
	return last
}

// stripURL removes the request URL from an HTTP client error: *url.Error prints it, and
// the webhook URL holds its token.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return errors.New("timed out")
		}
		return ue.Err
	}
	return err
}

// ───────────────────────────────────────────────────────────── payloads

// Embed colors.
const (
	colorCritical = 0xE5484D
	colorWarning  = 0xF5A524
	colorInfo     = 0x3E63DD
	colorResolved = 0x30A46C
)

type discordPayload struct {
	Username        string          `json:"username"`
	Content         string          `json:"content,omitempty"`
	Embeds          []discordEmbed  `json:"embeds"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

type allowedMentions struct {
	Parse []string `json:"parse"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	URL         string         `json:"url,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Footer      *discordFooter `json:"footer,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordFooter struct {
	Text string `json:"text"`
}

// DiscordPayload is the body of a Discord webhook execution: one embed per message, no
// mention is ever resolved (allowed_mentions.parse is empty).
func DiscordPayload(msgs []Message) any {
	p := discordPayload{Username: "Cicord alerts", AllowedMentions: allowedMentions{Parse: []string{}}}
	for _, m := range msgs {
		e := discordEmbed{Title: clip(m.Heading(), 256), Description: clip(m.Detail, 2000), Color: m.color(), Timestamp: m.Time.UTC().Format(time.RFC3339)}
		if m.Link != "" {
			e.URL = m.Link
		}
		for _, f := range m.Fields() {
			e.Fields = append(e.Fields, discordField{Name: f.Name, Value: clip(f.Value, 1024), Inline: f.Inline})
		}
		if m.Link != "" {
			e.Fields = append(e.Fields, discordField{Name: "Admin console", Value: clip(m.Link, 1024)})
		}
		e.Footer = &discordFooter{Text: "Cicord · " + m.Runtime}
		p.Embeds = append(p.Embeds, e)
	}
	return p
}

// jsonPayload is the body sent to a non-Discord webhook. text is a one-line summary per
// alert, which Slack, Mattermost and similar incoming webhooks display as is.
type jsonPayload struct {
	Source string    `json:"source"`
	Text   string    `json:"text"`
	Alerts []Message `json:"alerts"`
}

// JSONPayload is the body of a generic webhook.
func JSONPayload(msgs []Message) any {
	lines := make([]string, len(msgs))
	for i, m := range msgs {
		lines[i] = m.Summary()
	}
	return jsonPayload{Source: "cicord", Text: strings.Join(lines, "\n"), Alerts: msgs}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
