// Package source produces immutable revisions of a CODI repository for the runtime.
//
// A Revision is a read-only snapshot directory of the repository at one point in time
// (a git commit, or a content hash of a local working directory in dev mode). The
// runtime loads, builds and activates revisions; it never reads the live working tree
// directly, so edits made while a sync runs cannot produce a torn state.
package source

import (
	"context"
	"time"
)

type Revision struct {
	ID      string    `json:"id"`  // short identifier shown to users (7-char SHA or content hash)
	SHA     string    `json:"sha"` // full git SHA, or content hash for local sources
	Dir     string    `json:"-"`   // snapshot directory (repo root, i.e. where codi.yaml lives)
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
	Source  string    `json:"source"` // "local" | "git"
}

// Source yields revisions.
type Source interface {
	// Fetch returns the newest revision, fetching from the remote if needed. The same
	// SHA returns the same snapshot directory.
	Fetch(ctx context.Context) (Revision, error)
	// Changes receives a value when a new revision may be available (file change,
	// poll tick, webhook trigger). Never closed before Close.
	Changes() <-chan struct{}
	// Trigger requests an immediate check (webhook, API).
	Trigger()
	Close() error
}

func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
