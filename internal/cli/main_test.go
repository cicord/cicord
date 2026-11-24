package cli

import (
	"os"
	"testing"

	"github.com/cicord/cicord/internal/registry"
)

// TestMain isolates the CLI tests from the developer's environment:
//   - none may fetch the real official registry (tests that need one set
//     registry.OfficialURLs themselves);
//   - they open runtime state stores, and a developer's CICORD_DATABASE_URL must never
//     point them at a real database.
func TestMain(m *testing.M) {
	registry.OfficialURLs = nil
	_ = os.Unsetenv(databaseURLEnv)
	os.Exit(m.Run())
}
