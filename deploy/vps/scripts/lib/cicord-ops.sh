# shellcheck shell=sh
# cicord-ops.sh — functions shared by cicord-backup, cicord-update (installed by
# 05-backup.sh and 06-auto-update.sh as /usr/local/lib/cicord/ops.sh) and 07-restore.sh.
#
# Every path can be overridden from the environment, which is how the tests run them
# without Docker or root (deploy/vps/scripts_test.go):
#   CICORD_DIR         /opt/cicord (compose.yaml, .env)
#   CICORD_ENV         $CICORD_DIR/.env
#   CICORD_BACKUP_DIR  /var/backups/cicord
#   CICORD_PROJECT     cicord (the compose project: its volume is <project>_cicord-data)
#   CICORD_DATA_DIR    the data directory (default: the volume's mount point)
#   CICORD_CONTAINER   cicord
#
# The runtime state is SQLite (cicord.db) on this install; a CICORD_DATABASE_URL in .env
# (PostgreSQL) is backed up with pg_dump instead (docs: Backups and secrets).

CICORD_DIR="${CICORD_DIR:-/opt/cicord}"
CICORD_ENV="${CICORD_ENV:-$CICORD_DIR/.env}"
CICORD_BACKUP_DIR="${CICORD_BACKUP_DIR:-/var/backups/cicord}"
CICORD_PROJECT="${CICORD_PROJECT:-cicord}"
CICORD_CONTAINER="${CICORD_CONTAINER:-cicord}"
CICORD_UID=65532

log() { logger -t "${CICORD_LOG_TAG:-cicord}" -- "$*" 2>/dev/null || true; echo "$*"; }

envget() { # envget NAME: the value of NAME in .env, read like compose does: the last line
  # wins, surrounding spaces are trimmed, "…" or '…' quotes are removed, and an unquoted
  # value ends at a " #" comment (patch  # comment → patch)
  [ -f "$CICORD_ENV" ] || return 0
  v="$(tr -d '\r' < "$CICORD_ENV" | sed -n "s/^[[:space:]]*\(export[[:space:]]\{1,\}\)\{0,1\}$1[[:space:]]*=//p" | tail -n 1)"
  v="$(printf '%s' "$v" | sed 's/^[[:space:]]*//')"
  case "$v" in
    \"*) v="${v#\"}"; v="${v%%\"*}" ;;
    \'*) v="${v#\'}"; v="${v%%\'*}" ;;
    *) v="$(printf '%s' "$v" | sed -e 's/[[:space:]]#.*$//' -e 's/[[:space:]]*$//')" ;;
  esac
  printf '%s' "$v"
}

setenv() { # setenv NAME VALUE: replace the line(s) of NAME in .env (as envget reads them),
  # keeping its mode (0600), and check that compose will read VALUE
  tmp="$(mktemp "$CICORD_ENV.XXXXXX")"
  sed "s|^[[:space:]]*\(export[[:space:]]\{1,\}\)\{0,1\}$1[[:space:]]*=.*|$1=$2|" "$CICORD_ENV" > "$tmp" && chmod 600 "$tmp" && mv "$tmp" "$CICORD_ENV"
  [ "$(envget "$1")" = "$2" ] || { log "could not set $1 in $CICORD_ENV: check the file"; exit 1; }
}

compose() { docker compose --project-directory "$CICORD_DIR" -f "$CICORD_DIR/compose.yaml" --env-file "$CICORD_ENV" "$@"; }

data_dir() { # the runtime's data directory (the cicord-data volume)
  if [ -n "${CICORD_DATA_DIR:-}" ]; then printf '%s' "$CICORD_DATA_DIR"; return 0; fi
  docker volume inspect -f '{{.Mountpoint}}' "${CICORD_PROJECT}_cicord-data" 2>/dev/null
}

running() { [ "$(docker inspect -f '{{.State.Running}}' "$CICORD_CONTAINER" 2>/dev/null)" = true ]; }

healthy() { # healthy [SECONDS]: wait for the container's Docker health check (default 180 s)
  i=0; n=$(( ${1:-180} / 5 ))
  while [ "$i" -lt "$n" ]; do
    [ "$(docker inspect -f '{{.State.Health.Status}}' "$CICORD_CONTAINER" 2>/dev/null)" = healthy ] && return 0
    sleep "${CICORD_POLL:-5}"; i=$((i + 1))
  done
  return 1
}

supports_service_check() { # the running binary has `cicord healthcheck --service`
  docker exec "$CICORD_CONTAINER" cicord healthcheck --help 2>/dev/null | grep -q -- '--service'
}

service_check() { # service_check [TIMEOUT]: the service-level check inside the container:
  # the active revision is loaded, every server is Synced (boost waits are fine) and not
  # paused, every bot and plugin instance is Healthy (cicord status --wait). Exit status: 0
  # healthy, 1 not, 2 no runtime token.
  docker exec "$CICORD_CONTAINER" cicord healthcheck --service --data-dir /data --timeout "${1:-5m}"
}

alert() { # alert SEVERITY TITLE DETAIL [WORKFLOW] [STATE]: through the runtime's alert
  # webhook (CICORD_ALERT_WEBHOOK in its secret store), best effort: the running
  # container, else a one-off container of CICORD_IMAGE on the data volume
  set -- "$1" "$2" "$3" "${4:-}" "${5:-firing}"
  if running && docker exec "$CICORD_CONTAINER" cicord alert send --data-dir /data \
      --severity "$1" --title "$2" --detail "$3" --workflow "$4" --state "$5" >/dev/null 2>&1; then
    log "alert sent: $2"; return 0
  fi
  img="$(envget CICORD_IMAGE)"
  if [ -n "$img" ] && ! running && docker run --rm --user "$CICORD_UID:$CICORD_UID" -v "${CICORD_PROJECT}_cicord-data:/data" \
      -e CICORD_PUBLIC_URL="$(envget CICORD_PUBLIC_URL)" -e CICORD_ADMIN_URL="$(envget CICORD_ADMIN_URL)" \
      "$img" alert send --data-dir /data --severity "$1" --title "$2" --detail "$3" --workflow "$4" --state "$5" >/dev/null 2>&1; then
    log "alert sent: $2"; return 0
  fi
  log "alert not sent (no alert webhook configured, or it failed): $2"
}

own_data() { # own_data DIR: give the runtime's user its files back (sqlite3 runs as root)
  [ "$(id -u)" -eq 0 ] || return 0
  chown -R "$CICORD_UID:$CICORD_UID" "$1"
}

db_ok() { # db_ok FILE: SQLite's integrity check passes
  [ "$(sqlite3 "$1" 'PRAGMA integrity_check;' 2>&1 | head -5)" = ok ]
}

schema_version() { # schema_version FILE: the highest applied migration of a state database
  sqlite3 "$1" 'SELECT COALESCE(MAX(version), 0) FROM schema_migrations;' 2>/dev/null
}

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

# ───────────────────────────────────────────────────────────── backup

backup_fail() { # backup_fail MESSAGE: log, alert and exit 1
  log "backup FAILED: $1"
  alert critical "Backup failed" "$1" backup
  exit 1
}

# backup_run: an archive of the runtime state in $CICORD_BACKUP_DIR, then the off-host copy.
# The archive holds cicord.db (an online, consistent, integrity-checked copy), repo.git and
# writeback/ when present, and MANIFEST. Never secret.key (it decrypts the stored secrets:
# it lives apart, in a password manager) nor link.key (regenerated when missing).
backup_run() {
  umask 077
  data="$(data_dir)" || true
  [ -n "$data" ] && [ -d "$data" ] || backup_fail "the data directory (volume ${CICORD_PROJECT}_cicord-data) was not found"
  keep="$(envget CICORD_BACKUP_KEEP_DAYS)"; keep="${keep:-14}"
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  BACKUP_WORK="$(mktemp -d)"
  trap 'rm -rf "$BACKUP_WORK"' EXIT
  mkdir -p "$CICORD_BACKUP_DIR" "$BACKUP_WORK/cicord"
  db=sqlite; schema=""
  if [ -n "$(envget CICORD_DATABASE_URL)" ]; then
    db=postgres
    log "CICORD_DATABASE_URL is set: the state is in PostgreSQL, back it up with pg_dump; this archive holds the data directory only"
  else
    [ -f "$data/cicord.db" ] || backup_fail "$data/cicord.db is missing: there is no runtime state to back up (wrong volume, or the runtime never started)"
    sqlite3 "$data/cicord.db" ".backup '$BACKUP_WORK/cicord/cicord.db'" || { own_data "$data"; backup_fail "sqlite3 could not copy $data/cicord.db"; }
    own_data "$data"
    db_ok "$BACKUP_WORK/cicord/cicord.db" || backup_fail "the copy of cicord.db fails SQLite's integrity check: $(sqlite3 "$BACKUP_WORK/cicord/cicord.db" 'PRAGMA integrity_check;' 2>&1 | head -1)"
    schema="$(schema_version "$BACKUP_WORK/cicord/cicord.db")"
  fi
  for d in repo.git writeback; do
    if [ -d "$data/$d" ]; then cp -a "$data/$d" "$BACKUP_WORK/cicord/"; fi
  done
  {
    echo "created=$stamp"
    echo "image=$(envget CICORD_IMAGE)"
    echo "database=$db"
    echo "schema=$schema"
    if [ "$db" = sqlite ]; then echo "cicord.db.sha256=$(sha256 "$BACKUP_WORK/cicord/cicord.db")"; fi
    echo "excluded=secret.key link.key"
  } > "$BACKUP_WORK/cicord/MANIFEST"
  archive="$CICORD_BACKUP_DIR/cicord-$stamp.tar.zst"
  tar -C "$BACKUP_WORK/cicord" -cf "$BACKUP_WORK/backup.tar" . || backup_fail "tar failed"
  zstd -q -19 -f -o "$archive.part" "$BACKUP_WORK/backup.tar" || backup_fail "zstd failed (disk full?)"
  zstd -q -t "$archive.part" || backup_fail "the archive does not decompress"
  list="$(zstd -dc "$archive.part" | tar -tf -)" || backup_fail "the archive cannot be listed"
  if printf '%s\n' "$list" | grep -Eq '(^|/)(secret|link)\.key$'; then rm -f "$archive.part"; backup_fail "a key file got into the archive: refused"; fi
  if [ "$db" = sqlite ] && ! printf '%s\n' "$list" | grep -Eq '^(\./)?cicord\.db$'; then backup_fail "cicord.db is not in the archive"; fi
  mv "$archive.part" "$archive"
  chmod 600 "$archive"
  log "backup: $archive ($(du -h "$archive" | cut -f1), schema ${schema:-n/a})"
  find "$CICORD_BACKUP_DIR" -maxdepth 1 -name 'cicord-*.tar.zst' -mtime +"$keep" -exec rm -f {} +
  offhost_copy "$archive"
}

# offhost_copy ARCHIVE: copy the archive to CICORD_BACKUP_REMOTE (.env), if set:
#   rclone:<remote>:<path>      rclone copyto (S3, B2, SFTP… configured with `rclone config`)
#   rsync:[<user>@]<host>:<path>  rsync over ssh with root's key (BatchMode: no prompt)
# The remote keeps its own retention (bucket lifecycle rules, a cron on the target).
offhost_copy() {
  remote="$(envget CICORD_BACKUP_REMOTE)"
  name="$(basename "$1")"
  if [ -z "$remote" ]; then
    log "no CICORD_BACKUP_REMOTE in $CICORD_ENV: the backup stays on this server only"
    return 0
  fi
  case "$remote" in
    rclone:*:*)
      dest="${remote#rclone:}"
      command -v rclone >/dev/null || backup_fail "CICORD_BACKUP_REMOTE uses rclone, which is not installed (apt-get install rclone)"
      rclone copyto "$1" "$dest/$name" ||
        backup_fail "the off-host copy to $remote failed (the local backup $1 is kept)"
      rclone lsf "$dest/$name" 2>/dev/null | grep -qx "$name" ||
        backup_fail "the off-host copy is not listed on $remote (the local backup $1 is kept)" ;;
    rsync:*:*)
      dest="${remote#rsync:}"
      rsync -t --chmod=F600 -e "ssh -o BatchMode=yes -o ConnectTimeout=30" "$1" "$dest/" ||
        backup_fail "the off-host copy to $remote failed (the local backup $1 is kept)" ;;
    *) backup_fail "CICORD_BACKUP_REMOTE=$remote is not rclone:<remote>:<path> nor rsync:[user@]host:<path>" ;;
  esac
  log "backup copied off the server: $remote/$name"
}

# ───────────────────────────────────────────────────────────── update

# update_apply CURRENT_IMAGE NEW_IMAGE: switch the runtime to NEW_IMAGE (already pulled) and
# keep it only if the service check passes; otherwise roll back to CURRENT_IMAGE. A version
# that migrated the database cannot be rolled back by image alone (the older binary refuses
# a newer schema): the database copied right before the switch, with the runtime stopped, is
# put back, and what the new version wrote meanwhile is lost (a few minutes at most).
update_apply() {
  cur_img="$1"; new_img="$2"
  data="$(data_dir)" || true
  [ -n "$data" ] && [ -f "$data/cicord.db" ] || { log "no $data/cicord.db: refused (this updater handles the SQLite state of the VPS install)"; return 1; }
  if [ -n "$(envget CICORD_DATABASE_URL)" ]; then
    log "CICORD_DATABASE_URL is set: automatic updates cannot roll a PostgreSQL schema back: update by hand (Upgrading)"; return 1
  fi
  check="$(envget CICORD_UPDATE_CHECK)"; check="${check:-service}"
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  snap="$CICORD_BACKUP_DIR/pre-update-$stamp.db"
  mkdir -p "$CICORD_BACKUP_DIR"
  before="$(schema_version "$data/cicord.db")"
  compose stop cicord >/dev/null 2>&1 || { log "could not stop the runtime: update aborted"; return 1; }
  if ! sqlite3 "$data/cicord.db" ".backup '$snap'" || ! db_ok "$snap"; then
    own_data "$data"; rm -f "$snap"
    log "could not copy the database before the update: aborted, restarting $cur_img"
    compose up -d cicord >/dev/null 2>&1 || true
    return 1
  fi
  own_data "$data"; chmod 600 "$snap"
  setenv CICORD_IMAGE "$new_img"
  compose up -d cicord >/dev/null 2>&1 || true   # a failed start is caught by the checks
  why=""
  if ! healthy 180; then
    why="its health check did not pass within 3 minutes"
  elif [ "$check" = service ]; then
    if supports_service_check; then
      service_check 5m || why="the service check failed (a revision not loaded, a server not Synced or paused, or a bot or plugin not Healthy)"
    else
      log "the new version has no service check: accepted on its health check"
    fi
  fi
  if [ -z "$why" ]; then
    rm -f "$snap"
    return 0
  fi
  log "the new version is not healthy: $why; rolling back to $cur_img"
  after="$(schema_version "$data/cicord.db")"
  compose stop cicord >/dev/null 2>&1 || true
  restored=""
  if [ "$after" != "$before" ]; then
    for f in cicord.db-wal cicord.db-shm; do rm -f "$data/$f"; done
    cp "$snap" "$data/cicord.db" && chmod 600 "$data/cicord.db"
    own_data "$data"
    restored=" The new version had migrated the database (schema $before → $after): the copy taken before the update was restored, changes made in between are lost."
    log "database restored from $snap (schema $after → $before)"
  fi
  setenv CICORD_IMAGE "$cur_img"
  compose up -d cicord >/dev/null 2>&1 || true
  if healthy 180; then
    log "rolled back to $cur_img"
    alert critical "Update rolled back" "The update to $new_img was rolled back: $why.$restored Read the logs: journalctl -t cicord-update." update
  else
    log "the rollback to $cur_img is not healthy either: check the server (the pre-update database is $snap)"
    alert critical "Update rollback failed" "The update to $new_img failed ($why) and the rollback to $cur_img is not healthy either. The database before the update is $snap.$restored" update
  fi
  return 1
}

# update_preflight: the running version must pass the service check before an update, so
# that a new version is not rolled back for a problem it did not cause. Creates a runtime
# token when none is stored (the check authenticates with it; the next start reads it).
update_preflight() {
  check="$(envget CICORD_UPDATE_CHECK)"; check="${check:-service}"
  [ "$check" = service ] || { log "CICORD_UPDATE_CHECK=$check: updates are accepted on the Docker health check only"; return 0; }
  running || { log "the runtime is not running: update postponed"; return 1; }
  if ! docker exec "$CICORD_CONTAINER" cicord secret ls --data-dir /data 2>/dev/null | cut -d' ' -f1 | grep -qx CICORD_RUNTIME_TOKEN; then
    log "no CICORD_RUNTIME_TOKEN in the secret store: storing a random one for the service check (it takes effect at the next start)"
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n' | docker exec -i "$CICORD_CONTAINER" cicord secret set CICORD_RUNTIME_TOKEN --data-dir /data >/dev/null ||
      { log "could not store a runtime token: update postponed"; return 1; }
    return 0 # the running version does not have it yet: its check is skipped this time
  fi
  supports_service_check || { log "the running version has no service check: the pre-update check is skipped"; return 0; }
  if ! service_check 2m; then
    log "the running version does not pass the service check: update postponed until it does (fix it, or resume a paused server)"
    alert warning "Update postponed" "A new Cicord version is available, but the running one does not pass the service check (a server not Synced or paused, a bot or plugin not Healthy). Updates wait until it does." update
    return 1
  fi
}
