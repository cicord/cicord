package sqlite

import (
	"testing"

	"github.com/cicord/cicord/internal/state/statetest"
)

func TestPurgeScope(t *testing.T) { statetest.PurgeScope(t, mustOpen(t, ":memory:")) }
