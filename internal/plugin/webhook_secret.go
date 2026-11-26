package plugin

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	discordSendPath = regexp.MustCompile(`^/api/(v10/)?webhooks/[0-9]{1,20}/[A-Za-z0-9_-]{1,256}$`)
	slackSendPath   = regexp.MustCompile(`^/services/[A-Za-z0-9_-]{1,80}/[A-Za-z0-9_-]{1,80}/[A-Za-z0-9_-]{1,256}$`)
	errWebhookUse   = errors.New("webhook credential requires an approved incoming webhook POST")
)

func isURLCredential(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://")
}

// Incoming webhook URLs are bearer capabilities. A response can reveal the
// bearer separately from the full URL, so send returns only the HTTP status.
func (i *Instance) checkWebhookSend(raw string, u *url.URL, init *fetchInit, used []material) (bool, error) {
	logical, whole := strings.CutPrefix(raw, secretPrefix)
	if whole && i.env.SecretUses[logical] != "webhook.send" {
		return false, errWebhookUse
	}
	for _, m := range used {
		if i.env.SecretUses[m.name] != "webhook.send" && !isURLCredential(m.value) {
			continue
		}
		if !whole || logical != m.name || i.env.SecretUses[m.name] != "webhook.send" || len(used) != 1 || !strings.EqualFold(init.Method, "POST") {
			return false, errWebhookUse
		}
		if u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" || strings.ContainsAny(m.value, "?#%") {
			return false, errWebhookUse
		}
		switch u.Hostname() {
		case "discord.com":
			if !discordSendPath.MatchString(u.Path) {
				return false, errWebhookUse
			}
		case "hooks.slack.com":
			if !slackSendPath.MatchString(u.Path) {
				return false, errWebhookUse
			}
		default:
			return false, errWebhookUse
		}
		if init.Form != nil || init.Body == nil {
			return false, errWebhookUse
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(*init.Body), &fields) != nil || len(fields) == 0 {
			return false, errWebhookUse
		}
		for k := range fields {
			switch u.Hostname() {
			case "discord.com":
				switch k {
				case "content", "embeds", "allowed_mentions", "components", "flags":
				default:
					return false, errWebhookUse
				}
			case "hooks.slack.com":
				switch k {
				case "text", "blocks", "attachments", "mrkdwn", "unfurl_links", "unfurl_media":
				default:
					return false, errWebhookUse
				}
			}
		}
		for k, v := range init.Headers {
			if !strings.EqualFold(k, "content-type") || !strings.EqualFold(v, "application/json") {
				return false, errWebhookUse
			}
		}
		init.Headers = map[string]string{"Content-Type": "application/json"}
		return true, nil
	}
	return false, nil
}
