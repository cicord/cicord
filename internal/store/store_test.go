package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

func repoWith(regs ...config.Registry) *config.Repo {
	return &config.Repo{Project: &config.Project{Spec: config.ProjectSpec{Registries: regs}}}
}

func TestSourcesCacheAndErrors(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex // Sources fetches every registry concurrently
	fetches := map[string]int{}
	s := &Service{
		Now: func() time.Time { return now },
		Fetch: func(_ context.Context, url string) (*registry.Index, error) {
			mu.Lock()
			fetches[url]++
			mu.Unlock()
			if url == "https://down.example/r.git" {
				return nil, errors.New("clone https://down.example/r.git: repository not found")
			}
			return &registry.Index{Plugins: map[string]registry.IndexEntry{
				"b": {Repo: "https://x/b.git", Verified: true},
				"a": {Repo: "https://x/a.git", DisplayName: "Alpha", Keywords: []string{"k"}},
			}}, nil
		},
	}
	repo := repoWith(config.Registry{Name: "acme", URL: "https://acme.example/r.git"}, config.Registry{Name: "down", URL: "https://down.example/r.git"})
	srcs, err := s.Sources(context.Background(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 3 || srcs[0].Name != "cicord" || srcs[1].Name != "acme" || srcs[2].Name != "down" {
		t.Fatalf("sources = %+v", srcs)
	}
	acme := srcs[1]
	if acme.Trust != registry.TrustUnofficial || len(acme.Plugins) != 2 || acme.Plugins[0].Name != "a" || acme.Plugins[0].DisplayName != "Alpha" {
		t.Fatalf("acme = %+v", acme)
	}
	if acme.Plugins[1].Verified {
		t.Error("verified is only honoured for official registries")
	}
	if srcs[2].Error == nil || srcs[2].Error.Message != "The repository was not found or is private." || len(srcs[2].Plugins) != 0 {
		t.Errorf("down = %+v", srcs[2])
	}

	// Cached until the TTL, refetched when forced or stale.
	_, _ = s.Sources(context.Background(), repo, false)
	if fetches["https://acme.example/r.git"] != 1 {
		t.Errorf("fetches = %v", fetches)
	}
	_, _ = s.Sources(context.Background(), repo, true)
	now = now.Add(31 * time.Minute)
	_, _ = s.Sources(context.Background(), repo, false)
	if fetches["https://acme.example/r.git"] != 3 {
		t.Errorf("fetches = %v", fetches)
	}

	if _, err := s.Detail(context.Background(), repo, "nope", "a"); !errors.Is(err, ErrUnknownSource) {
		t.Errorf("unknown source: %v", err)
	}
	if _, err := s.Detail(context.Background(), repo, "acme", "zzz"); !errors.Is(err, ErrUnknownPlugin) {
		t.Errorf("unknown plugin: %v", err)
	}
	var ue *UnavailableError
	if _, err := s.Detail(context.Background(), repo, "down", "a"); !errors.As(err, &ue) {
		t.Errorf("down: %v", err)
	}
}

func TestRunRefreshesInBackground(t *testing.T) {
	var mu sync.Mutex
	n := 0
	s := &Service{TTL: 20 * time.Millisecond, FirstRefresh: time.Millisecond,
		Fetch: func(context.Context, string) (*registry.Index, error) {
			mu.Lock()
			n++
			mu.Unlock()
			return &registry.Index{}, nil
		}}
	repo := repoWith(config.Registry{Name: "acme", URL: "https://acme.example/r.git"})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { s.Run(stop, func() *config.Repo { return repo }); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := n
		mu.Unlock()
		if got >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d background refreshes", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}
