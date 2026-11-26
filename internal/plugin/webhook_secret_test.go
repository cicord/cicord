package plugin

import (
	"context"
	"encoding/json"
	"github.com/cicord/cicord/internal/discord"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWebhookHandleOnlySendsAndReturnsStatus(t *testing.T) {
	const discordURL = "https://discord.com/api/webhooks/123456789012345678/fixture-bearer-segment"
	const slackURL = "https://hooks.slack.com/services/TEXAMPLE/BEXAMPLE/fixture-slack-segment"
	const ref = "cicord-secret:HOOK"
	requests := 0
	status := 200
	value := discordURL
	client := &http.Client{Transport: providerTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "POST" || r.URL.String() != value || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("unexpected outgoing request")
		}
		// The credential appears as only a path segment, including in response headers.
		header := http.Header{"X-Partial": {"fixture-bearer-segment"}, "Location": {"https://discord.com/metadata"}}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(`{"token":"fixture-bearer-segment","slack":"fixture-slack-segment"}`))}, nil
	})}
	i := &Instance{grants: map[string]bool{"http.fetch": true}, limits: Limits{}.withDefaults(), egress: newEgress([]string{"discord.com", "hooks.slack.com", "other.example.org"}, false, client, 1024)}
	i.env.SecretUses = map[string]string{"HOOK": "webhook.send"}
	i.env.Secret = func(n string) (string, []string, bool) {
		return value, []string{"discord.com", "hooks.slack.com", "other.example.org"}, n == "HOOK" || n == "ALIAS"
	}
	fetch := func(raw string, init map[string]any) (*fetchResult, error) {
		data, _ := json.Marshal(map[string]any{"url": raw, "init": init})
		f, err := opHTTPFetch(i, nil, data)
		if err != nil {
			return nil, err
		}
		r, err := f(context.Background())
		if err != nil {
			return nil, err
		}
		return r.(*fetchResult), nil
	}
	discordPayload := map[string]any{"method": "POST", "json": map[string]string{"content": "fixture"}}
	for _, code := range []int{200, 302, 404} {
		status = code
		res, err := fetch(ref, discordPayload)
		if err != nil || res.Status != code || len(res.Body) != 0 || len(res.Headers) != 0 {
			t.Fatal("status-only send failed")
		}
	}
	value = slackURL
	status = 200
	if _, err := fetch(ref, map[string]any{"method": "POST", "body": `{"text":"fixture"}`}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value, raw string
		init       map[string]any
	}{
		{discordURL, ref, map[string]any{"method": "GET"}}, {discordURL, ref, map[string]any{"method": "DELETE"}},
		{discordURL + "/messages/123", ref, discordPayload}, {discordURL + "?wait=true", ref, discordPayload}, {discordURL + "/../metadata", ref, discordPayload},
		{"https://other.example.org/api/webhooks/123/fixture", ref, discordPayload}, {discordURL, "cicord-secret:ALIAS", discordPayload},
		{discordURL, "https://discord.com/other", map[string]any{"headers": map[string]string{"x-token": ref}}},
		{discordURL, "https://discord.com/other", map[string]any{"json": map[string]string{"token": "cicord-secret:ALIAS"}}},
		{discordURL, ref, map[string]any{"method": "POST", "body": `{"content":"x"}`, "headers": map[string]string{"x-http-method-override": "GET"}}},
		{slackURL, ref, map[string]any{"method": "POST", "json": map[string]string{"channel": "other", "text": "fixture"}}},
	} {
		value = tc.value
		if _, err := fetch(tc.raw, tc.init); err == nil {
			t.Fatal("accepted disallowed credential use")
		} else if strings.Contains(err.Error(), "fixture-bearer-segment") {
			t.Fatal("credential in failure")
		}
	}
	if requests != 4 {
		t.Fatalf("dispatched %d requests, want four sends", requests)
	}
}

func TestWebhookSendVMProjection(t *testing.T) {
	const source = `import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{notice:command({description:"notify",async run(ctx,it){
 const r=await ctx.http.fetch(ctx.secrets.ref("HOOK"),{method:"POST",json:{content:"hello"}});
 await it.reply(JSON.stringify([r.status,await r.text(),(await r.bytes()).length,Object.keys(r.headers)]));}})}});`
	h := start(t, source, func(e *Env) {
		e.NetworkAllow = []string{"discord.com"}
		e.SecretUses = map[string]string{"HOOK": "webhook.send"}
		e.Secret = func(name string) (string, []string, bool) {
			return "https://discord.com/api/webhooks/123/opaque-test-segment", e.NetworkAllow, name == "HOOK"
		}
		e.HTTPClient = &http.Client{Transport: providerTestTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"X-Token": {"opaque-test-segment"}}, Body: io.NopCloser(strings.NewReader(`{"token":"opaque-test-segment"}`))}, nil
		})}
	})
	if err := h.command("notice", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != `[200,"",0,[]]` {
		t.Fatal("webhook did not project to a status-only response")
	}
}
