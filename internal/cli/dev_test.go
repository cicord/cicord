package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a goroutine-safe bytes.Buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := copyRepoTree(src, dst); err != nil {
		t.Fatal(err)
	}
}

func TestDevSimSmoke(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "../../examples/basic", dir)
	t.Setenv("CICORD_RUNTIME_TOKEN", "smoke-token")

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- ExecuteContext(ctx, []string{"dev", "--sim", "--listen", "127.0.0.1:0", "--data-dir", t.TempDir(), "--log-level", "warn", dir}, &stdout, &stderr)
	}()
	defer func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("exit code %d; stderr:\n%s", code, stderr.String())
			}
		case <-time.After(30 * time.Second):
			t.Error("cicord dev did not stop")
		}
	}()

	re := regexp.MustCompile(`listening on (http://127\.0\.0\.1:\d+)`)
	var base string
	deadline := time.Now().Add(30 * time.Second)
	for base == "" {
		if m := re.FindStringSubmatch(stdout.String()); m != nil {
			base = m[1]
			break
		}
		select {
		case code := <-done:
			t.Fatalf("cicord dev exited with %d:\n%s\n%s", code, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no URL printed:\n%s\n%s", stdout.String(), stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(stdout.String(), "Simulator panel") || !strings.Contains(stdout.String(), base+"/admin/") {
		t.Errorf("stdout = %s", stdout.String())
	}

	get := func(path string) (int, []byte) {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Header.Set("Authorization", "Bearer smoke-token")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, body
	}
	if code, body := get("/healthz"); code != 200 || strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("healthz = %d %s", code, body)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		if code, _ := get("/readyz"); code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not ready:\n%s", stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	code, body := get("/api/v1/status")
	var st struct {
		Mode      string
		Simulator bool
		Servers   []struct{ Key, Health string }
	}
	if err := json.Unmarshal(body, &st); err != nil || code != 200 {
		t.Fatalf("status = %d %s", code, body)
	}
	if st.Mode != "dev" || !st.Simulator || len(st.Servers) != 1 || st.Servers[0].Health != "Healthy" {
		t.Fatalf("status = %s", body)
	}
	if code, body := get("/admin/"); code != 200 || !strings.Contains(string(body), "<html") {
		t.Fatalf("admin = %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "cicord.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestCheckLocalDataDir(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	cases := []struct {
		dir      string
		explicit bool
		want     string
		err      bool
	}{
		{outside, true, outside, false},
		{filepath.Join(repo, ".cicord", "x"), true, filepath.Join(repo, ".cicord", "x"), false},
		{filepath.Join(repo, ".cicord-data"), false, filepath.Join(repo, ".cicord", "data"), false},
		{filepath.Join(repo, "state"), true, "", true},
	}
	for _, c := range cases {
		got, err := checkLocalDataDir(repo, c.dir, c.explicit)
		if (err != nil) != c.err || (!c.err && got != c.want) {
			t.Errorf("checkLocalDataDir(%s, %v) = %q, %v; want %q (err %v)", c.dir, c.explicit, got, err, c.want, c.err)
		}
	}
}
