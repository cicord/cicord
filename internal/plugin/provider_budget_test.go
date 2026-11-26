package plugin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type providerTestTransport func(*http.Request) (*http.Response, error)

func (f providerTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMeteredHTTPNeverDispatchesWithoutReservation(t *testing.T) {
	const addr = "https://api.anthropic.com/v1/messages"
	requests := 0
	status := 200
	spent := -1
	deny := false
	client := &http.Client{Transport: providerTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":9,"output_tokens":4}}`))}, nil
	})}
	i := &Instance{grants: map[string]bool{"http.fetch": true}, limits: Limits{}.withDefaults(), egress: newEgress([]string{"api.anthropic.com"}, false, client, 4096)}
	i.env.BudgetedSecrets = map[string]bool{"KEY": true}
	i.env.Secret = func(n string) (string, []string, bool) { return "fixture", []string{"api.anthropic.com"}, n == "KEY" }
	i.env.ReserveBudget = func(_ context.Context, n string, units int) (func(context.Context, int) error, error) {
		if n != "KEY" || units <= 100 {
			t.Fatal("invalid reservation")
		}
		if deny {
			return nil, ErrBudgetExhausted
		}
		return func(_ context.Context, n int) error { spent = n; return nil }, nil
	}
	post := func(dest string, h map[string]string, doc map[string]any) (*fetchResult, error) {
		raw, _ := json.Marshal(map[string]any{"url": dest, "init": map[string]any{"method": "POST", "headers": h, "json": doc}})
		f, err := opHTTPFetch(i, nil, raw)
		if err != nil {
			return nil, err
		}
		out, err := f(t.Context())
		if err != nil {
			return nil, err
		}
		return out.(*fetchResult), nil
	}
	headers := map[string]string{"x-api-key": "cicord-secret:KEY", "anthropic-version": "2023-06-01"}
	doc := map[string]any{"model": "test", "system": "reply", "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "question"}}}
	if _, err := post(addr, headers, doc); err != nil || spent != 13 {
		t.Fatalf("success: %v, spent %d", err, spent)
	}
	status = 429
	if _, err := post(addr, headers, doc); err != nil || spent != 0 {
		t.Fatalf("rate limited: %v, spent %d", err, spent)
	}
	deny = true
	got, err := post(addr, headers, doc)
	if err != nil || got.Headers["x-cicord-budget"] != "exhausted" || requests != 2 {
		t.Fatal("provider called without budget")
	}
	for _, u := range []string{"https://api.anthropic.com/v1/other", addr + "?beta=1"} {
		if _, err := post(u, headers, doc); err == nil {
			t.Fatal("unapproved metered endpoint")
		}
	}
	if _, err := post(addr, map[string]string{"x-other": "cicord-secret:KEY"}, doc); err == nil {
		t.Fatal("metered handle moved")
	}
	doc["tools"] = []any{}
	if _, err := post(addr, headers, doc); err == nil {
		t.Fatal("unmetered request field")
	}
	if requests != 2 {
		t.Fatal("unexpected provider dispatch")
	}
}
