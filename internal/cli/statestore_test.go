package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/state/postgres"
	"github.com/cicord/cicord/internal/state/sqlite"
)

func TestMain(m *testing.M) {
	// The CLI tests open runtime state stores: a developer's CICORD_DATABASE_URL
	// must never point them at a real database.
	_ = os.Unsetenv(databaseURLEnv)
	os.Exit(m.Run())
}

func TestDatabaseURLPrecedence(t *testing.T) {
	t.Setenv(databaseURLEnv, " postgres://env@h/db ")
	if got := databaseURL(""); got != "postgres://env@h/db" {
		t.Errorf("env fallback = %q", got)
	}
	if got := databaseURL("postgres://flag@h/db"); got != "postgres://flag@h/db" {
		t.Errorf("flag = %q", got)
	}
	t.Setenv(databaseURLEnv, "")
	if got := databaseURL(""); got != "" {
		t.Errorf("none = %q", got)
	}
}

// The flag has no default taken from the environment: --help would print it.
func TestDatabaseURLNeverInHelp(t *testing.T) {
	const pw = "hunter2-Help-Leak"
	t.Setenv(databaseURLEnv, "postgres://cicord:"+pw+"@db/cicord")
	for _, args := range [][]string{{"serve", "--help"}, {"dev", "--help"}, {"secret", "--help"}, {"secret", "ls", "--help"}} {
		code, out, errOut := run(t, args...)
		if code != 0 || !strings.Contains(out, "--database-url") {
			t.Errorf("%v: exit %d, flag missing:\n%s", args, code, out)
		}
		if strings.Contains(out+errOut, pw) {
			t.Errorf("%v prints the database password", args)
		}
	}
}

func TestOpenStateStoreDefaultsToSQLite(t *testing.T) {
	dir := t.TempDir()
	st, err := openStateStore(t.Context(), nil, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok := st.(*sqlite.Store); !ok {
		t.Errorf("store is %T, want *sqlite.Store", st)
	}
	if _, err := os.Stat(filepath.Join(dir, stateDBFile)); err != nil {
		t.Errorf("no %s in the data dir: %v", stateDBFile, err)
	}
}

func TestOpenStateStoreErrorHidesPassword(t *testing.T) {
	const pw = "hunter2-Open-Leak"
	for _, u := range []string{
		"postgres://cicord:" + pw + "@127.0.0.1:1/cicord?connect_timeout=2",
		"postgres://cicord:" + pw + "@127.0.0.1:bad/cicord",
	} {
		_, err := openStateStore(t.Context(), nil, t.TempDir(), u)
		if err == nil || strings.Contains(err.Error(), pw) {
			t.Errorf("openStateStore error = %v", err)
		}
	}
}

// pgTestURL returns a connection string to an empty schema of the test server
// (CICORD_TEST_POSTGRES_URL), skipping the test without one.
func pgTestURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv("CICORD_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("CICORD_TEST_POSTGRES_URL is not set: no PostgreSQL server to test against")
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	name := "cicord_cli_test_" + hex.EncodeToString(b)
	exec := func(sql string) {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(c, base)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer conn.Close(c)
		if _, err := conn.Exec(c, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	ident := pgx.Identifier{name}.Sanitize()
	exec("CREATE SCHEMA " + ident)
	t.Cleanup(func() { exec("DROP SCHEMA " + ident + " CASCADE") })
	if !strings.HasPrefix(base, "postgres://") && !strings.HasPrefix(base, "postgresql://") {
		return base + " search_path=" + name
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal("CICORD_TEST_POSTGRES_URL is not a valid URL")
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return u.String()
}

func TestOpenStateStorePostgresLogsNoPassword(t *testing.T) {
	dbURL := pgTestURL(t)
	var logs bytes.Buffer
	st, err := openStateStore(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)), t.TempDir(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok := st.(*postgres.Store); !ok {
		t.Errorf("store is %T, want *postgres.Store", st)
	}
	cfg, _ := pgx.ParseConfig(dbURL)
	if !strings.Contains(logs.String(), "backend=postgres") || (cfg.Password != "" && strings.Contains(logs.String(), cfg.Password)) {
		t.Errorf("startup log = %s", logs.String())
	}
}

// cicord secret works against PostgreSQL: the secrets live in the database, the
// key in the data dir, and no SQLite database is created.
func TestSecretCommandPostgres(t *testing.T) {
	dbURL := pgTestURL(t)
	data := t.TempDir()
	t.Setenv(secrets.KeyEnv, "")
	run := func(stdin string, args ...string) (int, string) {
		var o, e syncBuffer
		cmd := newRootCmd()
		cmd.SetArgs(append(args, "--data-dir", data))
		cmd.SetIn(strings.NewReader(stdin))
		cmd.SetOut(&o)
		cmd.SetErr(&e)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			return 1, err.Error() + e.String()
		}
		return 0, o.String()
	}

	// No key yet: refused instead of generating one that would not match.
	if code, o := run("tok\n", "secret", "set", "DISCORD_TOKEN_X", "--database-url", dbURL); code == 0 || !strings.Contains(o, secrets.KeyFile) {
		t.Fatalf("set without a key = %d %s", code, o)
	}
	if _, _, err := secrets.LoadKey(data, nil); err != nil { // what the runtime does on first start
		t.Fatal(err)
	}
	if code, o := run("tok-123\n", "secret", "set", "DISCORD_TOKEN_X", "--database-url", dbURL); code != 0 {
		t.Fatalf("set = %d %s", code, o)
	}
	t.Setenv(databaseURLEnv, dbURL) // the environment works like the flag
	if code, o := run("", "secret", "rotate-key"); code != 0 {
		t.Fatalf("rotate = %s", o)
	}
	if code, o := run("", "secret", "ls"); code != 0 || !strings.Contains(o, "DISCORD_TOKEN_X") || strings.Contains(o, "tok-123") {
		t.Fatalf("ls = %d %s", code, o)
	}
	st, err := postgres.Open(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := openSecrets(t.Context(), st, data, false)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := sec.Get(t.Context(), "DISCORD_TOKEN_X"); v != "tok-123" {
		t.Fatalf("value after rotation = %q", v)
	}
	st.Close()
	if code, o := run("", "secret", "rm", "DISCORD_TOKEN_X"); code != 0 {
		t.Fatal(o)
	}
	if _, err := os.Stat(filepath.Join(data, stateDBFile)); err == nil {
		t.Errorf("a SQLite %s was created in the data dir", stateDBFile)
	}
}

// `cicord serve --database-url`: the setup wizard's progress lives in
// PostgreSQL (it resumes after a restart) and the data dir has no SQLite file.
func TestServeSetupWithPostgres(t *testing.T) {
	dbURL := pgTestURL(t)
	world := sim.NewWorld()
	env, err := setup.NewSimEnv(world)
	if err != nil {
		t.Fatal(err)
	}
	old := serveDiscord
	serveDiscord = func(*slog.Logger) (discord.Connector, setup.Probe) { return world, env.Probe() }
	defer func() { serveDiscord = old }()
	t.Setenv("CICORD_SECRET_KEY", "")
	t.Setenv("CICORD_RUNTIME_TOKEN", "")

	dir, data := t.TempDir(), t.TempDir()
	args := []string{"serve", "--listen", "127.0.0.1:0", "--data-dir", data, "--database-url", dbURL, dir}

	p := startCicord(t, args...)
	m := p.wait(t, setupURLRe)
	c := newSetupClient(t, m[1])
	c.ok("POST", "/api/v1/setup/session", map[string]any{"token": m[2]})
	c.ok("POST", "/api/v1/setup/intent", map[string]any{"intent": "existing"})
	tok, appID := env.CreateBot("Cicord")
	if err := env.SetIntents(appID, discord.PrivilegedIntents); err != nil {
		t.Fatal(err)
	}
	c.ok("POST", "/api/v1/setup/discord/token", map[string]any{"token": tok})
	p.stop(t)
	if logs := p.stderr.String(); !strings.Contains(logs, "backend=postgres") {
		t.Errorf("no postgres startup log:\n%s", logs)
	}
	if cfg, _ := pgx.ParseConfig(dbURL); cfg != nil && cfg.Password != "" && strings.Contains(p.stdout.String()+p.stderr.String(), cfg.Password) {
		t.Error("the database password was printed")
	}

	p = startCicord(t, args...)
	m = p.wait(t, setupURLRe)
	c = newSetupClient(t, m[1])
	st := c.ok("POST", "/api/v1/setup/session", map[string]any{"token": m[2]})
	if get(st, "view", "bot", "username") != "Cicord" {
		t.Fatalf("wizard did not resume from PostgreSQL: view = %v", st["view"])
	}
	p.stop(t)
	if _, err := os.Stat(filepath.Join(data, stateDBFile)); err == nil {
		t.Errorf("a SQLite %s was created in the data dir", stateDBFile)
	}
	if _, err := os.Stat(filepath.Join(data, secrets.KeyFile)); err != nil {
		t.Errorf("secret.key not in the data dir: %v", err)
	}
}
