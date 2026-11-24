package sqlite

import (
	"context"
	"testing"
	"time"
)

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
