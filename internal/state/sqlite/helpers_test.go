package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for WithClock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 3, 14, 15, 9, 26, 535897932, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type backend struct {
	name string
	open func(t *testing.T, opts ...Option) *Store
}

var backends = []backend{
	{"memory", func(t *testing.T, opts ...Option) *Store { return mustOpen(t, ":memory:", opts...) }},
	{"file", func(t *testing.T, opts ...Option) *Store {
		return mustOpen(t, filepath.Join(t.TempDir(), "state.db"), opts...)
	}},
}

// eachBackend runs fn as a subtest against an in-memory and a file-backed store.
func eachBackend(t *testing.T, fn func(t *testing.T, s *Store, clk *fakeClock)) {
	t.Helper()
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			clk := newFakeClock()
			s := b.open(t, WithClock(clk.Now))
			fn(t, s, clk)
		})
	}
}

func mustOpen(t *testing.T, path string, opts ...Option) *Store {
	t.Helper()
	s, err := Open(path, opts...)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func ctx(t *testing.T) context.Context { return t.Context() }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// assertTime checks exact instant equality and UTC location.
func assertTime(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("%s location = %v, want UTC", what, got.Location())
	}
}
