package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/statetest"
)

// The backend-neutral conformance suite, against an in-memory database and a
// file database (WAL, separate reader and writer pools, several handles).
func TestConformance(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		statetest.Run(t, statetest.Backend{
			NewDatabase: func(*testing.T) statetest.Opener {
				return func(t *testing.T, now func() time.Time) state.Store {
					return mustOpen(t, ":memory:", WithClock(now))
				}
			},
		})
	})
	t.Run("file", func(t *testing.T) {
		statetest.Run(t, statetest.Backend{
			NewDatabase: func(t *testing.T) statetest.Opener {
				path := filepath.Join(t.TempDir(), "state.db")
				return func(t *testing.T, now func() time.Time) state.Store {
					return mustOpen(t, path, WithClock(now))
				}
			},
			SharedHandles: true,
		})
	})
}
