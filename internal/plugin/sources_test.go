package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSecretFetch(t *testing.T) {
	const marker = "fixture with+space"
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/form":
			if err := r.ParseForm(); err != nil || r.FormValue("secret") != marker {
				t.Error("form substitution")
			}
		case "/json":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["secret"] != marker {
				t.Error("json substitution")
			}
		case "/redirect":
			http.Redirect(w, r, "/forbidden", 307)
			return
		default:
			t.Error("unexpected destination")
		}
		if r.Header.Get("x-api-key") != marker {
			t.Error("header substitution")
		}
		w.Header().Set("x-reflected", marker)
		_, _ = w.Write(append(append([]byte{0, 255}, marker...), 0xfe))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	i := &Instance{grants: map[string]bool{"http.fetch": true}, limits: Limits{}.withDefaults(), egress: newEgress([]string{u.Host, "other.org"}, false, srv.Client(), 1024)}
	i.env.Secret = func(n string) (string, []string, bool) {
		if n == "HOOK" {
			return srv.URL + "/form", []string{u.Host}, true
		}
		return marker, []string{u.Host}, n == "KEY"
	}
	for _, tt := range []struct {
		url  string
		init map[string]any
		ok   bool
	}{
		{"cicord-secret:HOOK", map[string]any{"form": map[string]string{"secret": "cicord-secret:KEY"}}, true},
		{srv.URL + "/json", map[string]any{"json": map[string]string{"secret": "cicord-secret:KEY"}}, true},
		{srv.URL + "/redirect", nil, false},
		{"https://other.org", nil, false},
		{srv.URL, map[string]any{"body": "partial-cicord-secret:KEY"}, false},
		{srv.URL, map[string]any{"json": map[string]any{"nested": map[string]string{"x": "cicord-secret:KEY"}}}, false},
	} {
		init := tt.init
		if init == nil {
			init = map[string]any{}
		}
		init["method"] = "POST"
		init["headers"] = map[string]string{"x-api-key": "cicord-secret:KEY"}
		raw, _ := json.Marshal(map[string]any{"url": tt.url, "init": init})
		f, err := opHTTPFetch(i, nil, raw)
		if err == nil {
			var r any
			r, err = f(context.Background())
			if err == nil {
				data, _ := json.Marshal(r)
				var response struct {
					Body    []byte            `json:"bytes"`
					Headers map[string]string `json:"headers"`
				}
				if err := json.Unmarshal(data, &response); err != nil {
					t.Fatal(err)
				}
				want := append(append([]byte{0, 255}, "[redacted]"...), 0xfe)
				if !bytes.Equal(response.Body, want) || response.Headers["x-reflected"] != "[redacted]" {
					t.Fatal("credentialed response did not preserve bytes around the redacted body and header")
				}
			}
		}
		if (err == nil) != tt.ok {
			t.Errorf("unexpected result for %s (%v)", tt.url, err)
		}
		if err != nil && strings.Contains(err.Error(), marker) {
			t.Fatal("error exposed the credential")
		}
	}
	if requests != 3 {
		t.Fatalf("got %d requests", requests)
	}
}
