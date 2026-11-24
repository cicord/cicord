package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The GitHub webhook secret and the runtime token are read from the encrypted secret store
// when the environment does not set them (cicord secret set, as the setup wizard does).
func TestServeMachineSecretsFromStore(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	data := filepath.Join(dir, ".cicord", "data")
	t.Setenv("CICORD_SECRET_KEY", "")
	t.Setenv("CICORD_WEBHOOK_SECRET", "")
	t.Setenv("CICORD_RUNTIME_TOKEN", "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	base := "http://" + addr
	serve := func() *cicordProc {
		return startCicord(t, "serve", "--local", "--listen", addr, "--public-url", base, "--data-dir", data, "--log-level", "error", dir)
	}
	listening := regexp.MustCompile(`listening on ` + regexp.QuoteMeta(base))
	// A first start creates the runtime state (and its secret store); the secrets are set
	// while it is stopped, then read by the next start.
	p := serve()
	p.wait(t, listening)
	p.stop(t)
	for name, value := range map[string]string{"CICORD_WEBHOOK_SECRET": "whsec-store", "CICORD_RUNTIME_TOKEN": "rt-store"} {
		var o, e bytes.Buffer
		cmd := newRootCmd()
		cmd.SetArgs([]string{"secret", "set", name, "--data-dir", data})
		cmd.SetIn(strings.NewReader(value + "\n"))
		cmd.SetOut(&o)
		cmd.SetErr(&e)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("secret set %s: %v %s", name, err, e.String())
		}
	}

	p = serve()
	p.wait(t, listening)
	defer p.stop(t)

	post := func(path string, body []byte, headers ...string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	sign := func(body []byte, secret string) string {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(body)
		return "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	push := []byte(`{"ref":"refs/heads/main"}`)
	if code := post("/hooks/github", push, "X-GitHub-Event", "push", "X-Hub-Signature-256", sign(push, "wrong")); code != http.StatusUnauthorized {
		t.Errorf("webhook with a wrong signature = %d", code)
	}
	if code := post("/hooks/github", push, "X-GitHub-Event", "push", "X-Hub-Signature-256", sign(push, "whsec-store")); code == http.StatusUnauthorized || code >= 500 {
		t.Errorf("webhook signed with the stored secret = %d", code)
	}
	if code := post("/api/v1/sync", nil, "Authorization", "Bearer wrong"); code != http.StatusUnauthorized {
		t.Errorf("sync with a wrong token = %d", code)
	}
	if code := post("/api/v1/sync", nil, "Authorization", "Bearer rt-store"); code != http.StatusAccepted {
		t.Errorf("sync with the stored runtime token = %d", code)
	}
}
