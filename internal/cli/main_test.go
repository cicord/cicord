package cli

import (
	"os"
	"testing"

	"github.com/cicord/cicord/internal/registry"
)

// testSourceCommit is the source commit of the unpublished cicord the tests run as.
const testSourceCommit = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"

// TestMain isolates the CLI tests from the developer's environment:
//   - none may fetch the real official registry (tests that need one set
//     registry.OfficialURLs themselves);
//   - they open runtime state stores, and a developer's CICORD_DATABASE_URL must never
//     point them at a real database;
//   - a test binary carries no VCS stamp, so unpublished-build scaffolds pin a fixed
//     test commit instead of refusing (tests of the refusal override it again).
func TestMain(m *testing.M) {
	registry.OfficialURLs = nil
	sourceRevision = func() string { return testSourceCommit }
	_ = os.Unsetenv(databaseURLEnv)
	os.Exit(m.Run())
}
