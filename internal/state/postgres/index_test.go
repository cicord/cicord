package postgres

import (
	"github.com/cicord/cicord/internal/state/statetest"
	"testing"
)

func TestFullText(t *testing.T) { statetest.FullText(t, mustOpen(t, newSchema(t))) }
