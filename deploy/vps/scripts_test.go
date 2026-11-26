// Package vps tests the operations scripts of the VPS install (scripts/bin/cicord-backup,
// scripts/07-restore.sh and the update functions of scripts/lib/cicord-ops.sh) without
// Docker or root: they run under dash (or sh) with temporary directories, a real state
// database, and stub docker, logger and rsync commands on PATH.
package vps

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

const ticketScope = "plugin:cicord/tickets/showcase"

// fakeDocker stands in for docker: `compose stop/up` flip a "running" file, `compose up`
// with the image in $FAKE_MIGRATE runs a migration and writes a row (what a new version
// does), the service check exits with the content of service_exit (1 for the image in
// $FAKE_BAD), and `alert send` is recorded in alerts.
const fakeDocker = `#!/bin/sh
S="$FAKE_STATE"
echo "docker $*" >> "$S/log"
img() { sed -n 's/^CICORD_IMAGE=//p' "$CICORD_ENV" | tail -n 1; }
case "$1" in
  inspect)
    case "$*" in
      *Running*) cat "$S/running" 2>/dev/null || echo false ;;
      *Health*) if [ "$(cat "$S/running" 2>/dev/null)" = true ]; then echo healthy; else echo starting; fi ;;
    esac ;;
  volume) echo "$CICORD_DATA_DIR" ;;
  exec)
    case "$*" in
      *"healthcheck --help"*) echo "      --service   check the service" ;;
      *"healthcheck --service"*) exit "$(cat "$S/service_exit" 2>/dev/null || echo 0)" ;;
      *"alert send"*) echo "$*" >> "$S/alerts" ;;
      *"secret ls"*) printf 'NAME                  UPDATED\n'; [ -e "$S/no_token" ] || printf 'CICORD_RUNTIME_TOKEN  2026-11-25 12:00:00\n' ;;
      *"secret set CICORD_RUNTIME_TOKEN"*) cat > "$S/token_set" ;;
    esac ;;
  run) echo "$*" >> "$S/alerts" ;;
  compose)
    case "$*" in
      *" stop "*|*" stop") echo false > "$S/running" ;;
      *"up -d"*)
        echo true > "$S/running"
        if [ -n "${FAKE_SERVICE_EXIT:-}" ]; then echo "$FAKE_SERVICE_EXIT" > "$S/service_exit"
        elif [ -n "${FAKE_BAD:-}" ] && [ "$(img)" = "$FAKE_BAD" ]; then echo 1 > "$S/service_exit"; else echo 0 > "$S/service_exit"; fi
        if [ -n "${FAKE_MIGRATE:-}" ] && [ "$(img)" = "$FAKE_MIGRATE" ]; then
          sqlite3 "$CICORD_DATA_DIR/cicord.db" "INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, '9999_next', 'now'); CREATE TABLE written_by_new (x);"
        fi
        if [ -n "${FAKE_WRITE:-}" ] && [ "$(img)" = "$FAKE_WRITE" ]; then
          sqlite3 "$CICORD_DATA_DIR/cicord.db" "CREATE TABLE IF NOT EXISTS written_by_new (x);"
        fi ;;
    esac ;;
esac
exit 0
`

// fakeRsync copies its source into the local path of its host:path destination, or fails
// when $FAKE_RSYNC_FAIL is set.
const fakeRsync = `#!/bin/sh
[ -z "${FAKE_RSYNC_FAIL:-}" ] || { echo "ssh: connect to host nas: Connection refused" >&2; exit 12; }
for a in "$@"; do src="$dst"; dst="$a"; done
mkdir -p "${dst#*:}" && cp "$src" "${dst#*:}"
`

type harness struct {
	t                         *testing.T
	root, data, backups, envf string
	state, remote             string
	env                       []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	for _, tool := range []string{"sqlite3", "zstd", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	root := t.TempDir()
	h := &harness{t: t, root: root, data: filepath.Join(root, "data"), backups: filepath.Join(root, "backups"),
		envf: filepath.Join(root, "opt", ".env"), state: filepath.Join(root, "fake"), remote: filepath.Join(root, "remote")}
	bin := filepath.Join(root, "bin")
	for _, d := range []string{h.data, h.backups, filepath.Dir(h.envf), h.state, bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(bin, "docker"), fakeDocker, 0o755)
	write(t, filepath.Join(bin, "rsync"), fakeRsync, 0o755)
	write(t, filepath.Join(bin, "logger"), "#!/bin/sh\nexit 0\n", 0o755)
	write(t, filepath.Join(h.state, "running"), "true\n", 0o600)
	h.setEnv("CICORD_IMAGE=ghcr.io/cicord/cicord:0.1.6@sha256:old\n")
	scripts, _ := filepath.Abs("scripts")
	h.env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "CICORD_OPS_LIB="+filepath.Join(scripts, "lib", "cicord-ops.sh"),
		"CICORD_DIR="+filepath.Dir(h.envf), "CICORD_ENV="+h.envf, "CICORD_BACKUP_DIR="+h.backups, "CICORD_DATA_DIR="+h.data,
		"CICORD_POLL=0", "FAKE_STATE="+h.state)
	return h
}

func (h *harness) setEnv(content string) { write(h.t, h.envf, content, 0o600) }

// run runs a script under dash when available (it has no bash extensions), else sh.
func (h *harness) run(extraEnv []string, script string, args ...string) (string, error) {
	h.t.Helper()
	shell := "dash"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "sh"
	}
	cmd := exec.CommandContext(context.Background(), shell, append([]string{script}, args...)...)
	cmd.Env = append(append([]string{}, h.env...), extraEnv...)
	out, err := cmd.CombinedOutput()
	if testing.Verbose() {
		h.t.Logf("%s %s:\n%s", filepath.Base(script), strings.Join(args, " "), out)
	}
	return string(out), err
}

func (h *harness) alerts() string {
	b, _ := os.ReadFile(filepath.Join(h.state, "alerts"))
	return string(b)
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// seed creates a runtime state in dir: a real migrated cicord.db with a ticket, the
// embedded repository and both keys.
func seed(t *testing.T, dir, ticket string) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(dir, "cicord.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.KV(ticketScope).Set(context.Background(), "ticket:1", json.RawMessage(`{"subject":"`+ticket+`","status":"open"}`), 0); err != nil {
		t.Fatal(err)
	}
	// A secret encrypted with the data dir's key (generated as the runtime does when missing).
	key, _, err := secrets.LoadKey(dir, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := secrets.Open(context.Background(), st, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := sec.Set(context.Background(), "DISCORD_TOKEN_CICORD", "bot-token-"+ticket); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.MkdirAll(filepath.Join(dir, "repo.git", "refs"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "repo.git", "HEAD"), "ref: refs/heads/main\n", 0o600)
	write(t, filepath.Join(dir, "privacy.key"), "privacy-canary", 0600)
	if err := os.MkdirAll(filepath.Join(dir, "member-erasures", "scope"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "member-erasures", "scope", "old-marker"), "timestamp", 0600)
	write(t, filepath.Join(dir, "link.key"), "link", 0o600)
}

func noEnv(string) (string, bool) { return "", false }

// storedToken decrypts the bot token of a data dir with its secret.key, as the runtime does.
func storedToken(t *testing.T, dir string) string {
	t.Helper()
	key, _, err := secrets.LoadKey(dir, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(filepath.Join(dir, "cicord.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sec, err := secrets.Open(context.Background(), st, key)
	if err != nil {
		t.Fatal(err)
	}
	v, err := sec.Get(context.Background(), "DISCORD_TOKEN_CICORD")
	if err != nil {
		t.Fatalf("decrypt the restored secret: %v", err)
	}
	return v
}

func ticketIn(t *testing.T, db string) string {
	t.Helper()
	st, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, err := st.KV(ticketScope).Get(context.Background(), "ticket:1")
	if errors.Is(err, state.ErrNotFound) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var tk struct{ Subject string }
	_ = json.Unmarshal(v, &tk)
	return tk.Subject
}

func archives(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "cicord-*.tar.zst"))
	return m
}

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	h := newHarness(t)
	seed(t, h.data, "Cannot join voice")
	h.setEnv("CICORD_IMAGE=ghcr.io/cicord/cicord:0.1.6@sha256:old\nCICORD_BACKUP_REMOTE=rsync:backup@nas:" + h.remote + "\n")
	out, err := h.run(nil, "scripts/bin/cicord-backup")
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	as := archives(t, h.backups)
	if len(as) != 1 || !strings.Contains(out, "backup copied off the server: rsync:backup@nas:") {
		t.Fatalf("archives %v\n%s", as, out)
	}
	if fi, _ := os.Stat(as[0]); fi.Mode().Perm() != 0o600 {
		t.Errorf("archive mode %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(h.remote, filepath.Base(as[0]))); err != nil {
		t.Errorf("no off-host copy: %v", err)
	}
	list, err := exec.CommandContext(context.Background(), "sh", "-c", "zstd -dc '"+as[0]+"' | tar -tf -").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\./cicord\.db$`).Match(list) || !strings.Contains(string(list), "repo.git/HEAD") || !strings.Contains(string(list), "MANIFEST") {
		t.Errorf("archive content:\n%s", list)
	}
	if strings.Contains(string(list), "privacy.key") || strings.Contains(string(list), "secret.key") || strings.Contains(string(list), "link.key") {
		t.Fatalf("a key is in the archive:\n%s", list)
	}

	// Restore onto a fresh machine: an empty data volume, the key from the password manager.
	fresh := newHarness(t)
	fresh.setEnv("CICORD_IMAGE=ghcr.io/cicord/cicord:0.1.6@sha256:old\n")
	// The key comes from the password manager: the one the runtime generated.
	keyFile := filepath.Join(fresh.root, "secret.key")
	key, err := os.ReadFile(filepath.Join(h.data, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, keyFile, string(key), 0o600)
	if err := os.MkdirAll(filepath.Join(fresh.data, "member-erasures", "scope"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(fresh.data, "member-erasures", "scope", "recent-marker"), "newer timestamp", 0600)
	out, err = fresh.run(nil, "scripts/07-restore.sh", as[0], "--key-file", keyFile)
	if err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	for _, name := range []string{"old-marker", "recent-marker"} {
		if _, err := os.Stat(filepath.Join(fresh.data, "member-erasures", "scope", name)); err != nil {
			t.Fatal("journal merge", err)
		}
	}
	if got := ticketIn(t, filepath.Join(fresh.data, "cicord.db")); got != "Cannot join voice" {
		t.Errorf("restored ticket = %q", got)
	}
	if got := storedToken(t, fresh.data); got != "bot-token-Cannot join voice" {
		t.Errorf("restored secret = %q", got)
	}
	if _, err := os.Stat(filepath.Join(fresh.data, "repo.git", "HEAD")); err != nil {
		t.Errorf("repo.git not restored: %v", err)
	}
	if !regexp.MustCompile(`restore verified in \d+ s`).MatchString(out) {
		t.Errorf("restore output:\n%s", out)
	}
	log, _ := os.ReadFile(filepath.Join(fresh.state, "log"))
	if !strings.Contains(string(log), "compose") || !strings.Contains(string(log), "up -d cicord") || !strings.Contains(string(log), "healthcheck --service") {
		t.Errorf("restore did not start and verify the runtime:\n%s", log)
	}

	// Restoring over an existing state (same server, same key) moves it aside, never deletes it.
	for _, f := range []string{"cicord.db", "cicord.db-wal", "cicord.db-shm"} {
		_ = os.Remove(filepath.Join(fresh.data, f))
	}
	seed(t, fresh.data, "newer ticket")
	out, err = fresh.run(nil, "scripts/07-restore.sh", as[0], "--keep-key", "--no-start")
	if err != nil {
		t.Fatalf("restore --keep-key: %v\n%s", err, out)
	}
	aside, _ := filepath.Glob(filepath.Join(fresh.backups, "pre-restore-*", "cicord.db"))
	if len(aside) != 1 || ticketIn(t, aside[0]) != "newer ticket" || ticketIn(t, filepath.Join(fresh.data, "cicord.db")) != "Cannot join voice" {
		t.Errorf("previous state not kept aside: %v\n%s", aside, out)
	}

	// Without a runtime token the service check cannot run: the restore still succeeds.
	out, err = fresh.run([]string{"FAKE_SERVICE_EXIT=2"}, "scripts/07-restore.sh", as[0], "--keep-key")
	if err != nil || !strings.Contains(out, "service check skipped: no runtime token") || !strings.Contains(out, "restore verified in") {
		t.Fatalf("restore without a runtime token: %v\n%s", err, out)
	}
	// A failing service check fails the restore.
	if out, err := fresh.run([]string{"FAKE_SERVICE_EXIT=1"}, "scripts/07-restore.sh", as[0], "--keep-key"); err == nil || !strings.Contains(out, "does not pass the service check") {
		t.Fatalf("restore with a failing service check: %v\n%s", err, out)
	}

	// A wrong key is refused before anything changes.
	write(t, keyFile, "not-a-key\n", 0o600)
	if out, err := fresh.run(nil, "scripts/07-restore.sh", as[0], "--key-file", keyFile); err == nil || !strings.Contains(out, "is not a secret.key") {
		t.Errorf("bad key: %v\n%s", err, out)
	}
}

func TestBackupFailsLoudly(t *testing.T) {
	h := newHarness(t)
	// No database: a failed run and an alert.
	out, err := h.run(nil, "scripts/bin/cicord-backup")
	if err == nil || !strings.Contains(out, "cicord.db is missing") || !strings.Contains(h.alerts(), "--title Backup failed") {
		t.Fatalf("missing database: %v\n%s\nalerts: %s", err, out, h.alerts())
	}
	if len(archives(t, h.backups)) != 0 {
		t.Error("an archive was written without a database")
	}
	// A corrupt database.
	write(t, filepath.Join(h.data, "cicord.db"), strings.Repeat("not sqlite ", 500), 0o600)
	if out, err := h.run(nil, "scripts/bin/cicord-backup"); err == nil || !strings.Contains(out, "backup FAILED") {
		t.Fatalf("corrupt database: %v\n%s", err, out)
	}
	if err := os.Remove(filepath.Join(h.data, "cicord.db")); err != nil {
		t.Fatal(err)
	}
	// The off-host copy fails: the run fails, the local archive is kept.
	seed(t, h.data, "x")
	h.setEnv("CICORD_BACKUP_REMOTE=rsync:backup@nas:" + h.remote + "\n")
	out, err = h.run([]string{"FAKE_RSYNC_FAIL=1"}, "scripts/bin/cicord-backup")
	if err == nil || !strings.Contains(out, "the off-host copy to rsync:backup@nas:") || len(archives(t, h.backups)) != 1 {
		t.Fatalf("remote failure: %v\n%s", err, out)
	}
	// An unknown remote.
	h.setEnv("CICORD_BACKUP_REMOTE=s3://bucket\n")
	if out, err := h.run(nil, "scripts/bin/cicord-backup"); err == nil || !strings.Contains(out, "is not rclone:") {
		t.Fatalf("bad remote: %v\n%s", err, out)
	}
}

// updateApply runs update_apply OLD NEW from the library.
func (h *harness) updateApply(extra []string, oldImg, newImg string) (string, error) {
	h.t.Helper()
	script := filepath.Join(h.root, "apply.sh")
	write(h.t, script, `set -eu
. "$CICORD_OPS_LIB"
update_apply "$1" "$2"
`, 0o600)
	return h.run(extra, script, oldImg, newImg)
}

func envImage(t *testing.T, f string) string {
	t.Helper()
	b, _ := os.ReadFile(f)
	m := regexp.MustCompile(`(?m)^CICORD_IMAGE=(.*)$`).FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func hasTable(t *testing.T, db, table string) bool {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "sqlite3", db, "SELECT name FROM sqlite_master WHERE name='"+table+"'").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out)) == table
}

func TestUpdateApply(t *testing.T) {
	const oldImg, newImg = "ghcr.io/cicord/cicord:0.1.6@sha256:old", "ghcr.io/cicord/cicord:0.1.7@sha256:new"

	t.Run("accepted", func(t *testing.T) {
		h := newHarness(t)
		seed(t, h.data, "x")
		out, err := h.updateApply(nil, oldImg, newImg)
		if err != nil || envImage(t, h.envf) != newImg {
			t.Fatalf("update: %v, image %s\n%s", err, envImage(t, h.envf), out)
		}
		if snaps, _ := filepath.Glob(filepath.Join(h.backups, "pre-update-*.db")); len(snaps) != 0 {
			t.Errorf("snapshot kept after a good update: %v", snaps)
		}
	})

	t.Run("service check fails, same schema: image rollback, data kept", func(t *testing.T) {
		h := newHarness(t)
		seed(t, h.data, "x")
		out, err := h.updateApply([]string{"FAKE_BAD=" + newImg, "FAKE_WRITE=" + newImg}, oldImg, newImg)
		if err == nil || envImage(t, h.envf) != oldImg || !strings.Contains(out, "the service check failed") {
			t.Fatalf("update: %v, image %s\n%s", err, envImage(t, h.envf), out)
		}
		if !hasTable(t, filepath.Join(h.data, "cicord.db"), "written_by_new") {
			t.Error("the database was restored although the schema did not change")
		}
		if a := h.alerts(); !strings.Contains(a, "--title Update rolled back") {
			t.Errorf("alerts: %s", a)
		}
	})

	t.Run("migrating release fails: database restored", func(t *testing.T) {
		h := newHarness(t)
		seed(t, h.data, "before the update")
		out, err := h.updateApply([]string{"FAKE_BAD=" + newImg, "FAKE_MIGRATE=" + newImg}, oldImg, newImg)
		if err == nil || envImage(t, h.envf) != oldImg {
			t.Fatalf("update: %v, image %s\n%s", err, envImage(t, h.envf), out)
		}
		db := filepath.Join(h.data, "cicord.db")
		if hasTable(t, db, "written_by_new") || ticketIn(t, db) != "before the update" {
			t.Errorf("the pre-update database was not restored\n%s", out)
		}
		if a := h.alerts(); !strings.Contains(a, "had migrated the database") {
			t.Errorf("alerts: %s", a)
		}
	})

	t.Run("preflight: a runtime token is created when none is stored", func(t *testing.T) {
		h := newHarness(t)
		write(t, filepath.Join(h.state, "no_token"), "", 0o600)
		script := filepath.Join(h.root, "pre.sh")
		write(t, script, ". \"$CICORD_OPS_LIB\"\nupdate_preflight\n", 0o600)
		out, err := h.run(nil, script)
		tok, _ := os.ReadFile(filepath.Join(h.state, "token_set"))
		if err != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).Match(tok) || strings.Contains(out, string(tok)) {
			t.Fatalf("preflight: %v, token %q\n%s", err, tok, out)
		}
	})

	t.Run("preflight: a degraded running version postpones the update", func(t *testing.T) {
		h := newHarness(t)
		write(t, filepath.Join(h.state, "service_exit"), "1\n", 0o600)
		script := filepath.Join(h.root, "pre.sh")
		write(t, script, ". \"$CICORD_OPS_LIB\"\nupdate_preflight\n", 0o600)
		out, err := h.run(nil, script)
		if err == nil || !strings.Contains(out, "update postponed") || !strings.Contains(h.alerts(), "--title Update postponed") {
			t.Fatalf("preflight: %v\n%s\nalerts: %s", err, out, h.alerts())
		}
	})
}
