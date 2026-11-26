package plugin

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrBudgetExhausted = errors.New("daily provider budget exhausted")

// A metered credential is usable only on the text-only Anthropic Messages API.
// The VM cannot select a ledger, report usage, refund a request or read another
// installation's balance.
func (i *Instance) providerCharge(raw string, init fetchInit) (int, string, error) {
	name := ""
	for k, v := range init.Headers {
		if n, ok := strings.CutPrefix(v, secretPrefix); ok && i.env.BudgetedSecrets[n] && strings.EqualFold(k, "x-api-key") {
			name = n
			break
		}
	}
	if name == "" {
		return 0, "", nil
	}
	invalid := errors.New("metered credential requires a text-only Anthropic Messages POST")
	if raw != "https://api.anthropic.com/v1/messages" || !strings.EqualFold(init.Method, "POST") || init.Form != nil || init.Body != nil || len(init.JSON) != 4 {
		return 0, "", invalid
	}
	for k, v := range init.Headers {
		switch strings.ToLower(k) {
		case "x-api-key":
			if v != secretPrefix+name {
				return 0, "", invalid
			}
		case "anthropic-version":
			if v != "2023-06-01" {
				return 0, "", invalid
			}
		default:
			return 0, "", invalid
		}
	}
	rawBody, err := json.Marshal(init.JSON)
	if err != nil {
		return 0, "", invalid
	}
	var body struct {
		Model    string                           `json:"model"`
		System   string                           `json:"system"`
		Max      int                              `json:"max_tokens"`
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(rawBody)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&body); err != nil || body.Model == "" || body.Max < 1 || body.Max > 8192 || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content == "" {
		return 0, "", invalid
	}
	// UTF-8 bytes conservatively bound text BPE tokens. Also reserve framing
	// overhead and every requested output token.
	return len(rawBody) + 256 + body.Max, name, nil
}
func providerUsage(result *fetchResult, held int) int {
	if result.Status < 200 || result.Status >= 300 {
		return 0
	}
	var body struct {
		Usage *struct {
			Input  *int `json:"input_tokens"`
			Output *int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(result.Body, &body) != nil || body.Usage == nil || body.Usage.Input == nil || body.Usage.Output == nil {
		return held
	}
	i, o := *body.Usage.Input, *body.Usage.Output
	if i < 0 || o < 0 || i > 10000000 || o > 10000000 {
		return held
	}
	return i + o
}
