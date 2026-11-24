package cli

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/postgres"
	"github.com/cicord/cicord/internal/state/sqlite"
)

// databaseURLEnv selects the PostgreSQL state backend when --database-url is
// not given.
const databaseURLEnv = "CICORD_DATABASE_URL"

// stateDBFile is the SQLite state database in the data dir.
const stateDBFile = "cicord.db"

// databaseURLFlagHelp is the --database-url help text. The flag has no default
// value on purpose: a default read from the environment would be printed by
// --help, password included.
const databaseURLFlagHelp = "PostgreSQL connection URL of the state store (default: $" + databaseURLEnv +
	"; unset: SQLite in the data dir)"

// databaseURL returns the --database-url value, else $CICORD_DATABASE_URL.
func databaseURL(flag string) string {
	if u := strings.TrimSpace(flag); u != "" {
		return u
	}
	return strings.TrimSpace(os.Getenv(databaseURLEnv))
}

// openStateStore opens the runtime state store: PostgreSQL when dbURL is set,
// else SQLite at <dataDir>/cicord.db. The data dir is needed either way (secret
// and link keys, embedded repository, caches). log may be nil. The URL is never
// logged nor returned in errors (it may hold a password).
func openStateStore(ctx context.Context, log *slog.Logger, dataDir, dbURL string) (state.Store, error) {
	if dbURL != "" {
		s, err := postgres.Open(ctx, dbURL)
		if err != nil {
			return nil, err
		}
		if log != nil {
			log.Info("runtime state", "backend", "postgres", "database", s.Describe())
		}
		return s, nil
	}
	path := filepath.Join(dataDir, stateDBFile)
	s, err := sqlite.Open(path)
	if err != nil {
		return nil, err
	}
	if log != nil {
		log.Debug("runtime state", "backend", "sqlite", "path", path)
	}
	return s, nil
}

// openSimStore opens the in-memory store of `cicord dev --sim`: the
// simulator's IDs are new on every run, so its state must not outlive the
// process (a configured PostgreSQL database is not used).
func openSimStore(log *slog.Logger, f serverFlags) (state.Store, error) {
	if databaseURL(f.databaseURL) != "" && log != nil {
		log.Info("--sim keeps the runtime state in memory: the PostgreSQL database is not used")
	}
	return sqlite.Open(":memory:")
}
