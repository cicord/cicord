#!/bin/sh
# 07-restore.sh — restore a backup of 05-backup.sh onto this server (a new one, or the same).
# Run as root: sudo sh 07-restore.sh ARCHIVE [--key-file FILE] [--keep-key] [--no-start]
#
#   ARCHIVE          a cicord-<timestamp>.tar.zst of cicord-backup
#   --key-file FILE  the secret store key (secret.key, from the password manager); without
#                    it, the key is read from stdin (paste it, Enter, Ctrl-D)
#   --keep-key       keep the secret.key already in the data volume (restoring on the same
#                    server)
#   --no-start       restore only; do not start and verify the runtime
#
# Prerequisites on a new server: steps 1 to 4 of the VPS guide, /opt/cicord with compose.yaml
# and .env (the same CICORD_IMAGE as the backup, or a newer one: MANIFEST says which), and
# 05-backup.sh run. Steps: check the archive (decompression, SQLite integrity, no key in it),
# stop the runtime, move the current state aside to /var/backups/cicord/pre-restore-<time>/,
# put cicord.db, repo.git and writeback/ in place, write secret.key (0600), give the files to
# the runtime's user, start it, then verify: the Docker health check and the service check
# (`cicord healthcheck --service`: revision loaded, servers Synced, bots and plugins
# Healthy). It prints how long the restore took, for the recovery drill.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
CICORD_LOG_TAG=cicord-restore
# shellcheck source=SCRIPTDIR/lib/cicord-ops.sh
. "${CICORD_OPS_LIB:-$HERE/lib/cicord-ops.sh}"

die() { log "restore FAILED: $*"; exit 1; }
usage() { echo "usage: sudo sh 07-restore.sh ARCHIVE [--key-file FILE] [--keep-key] [--no-start]" >&2; exit 2; }

archive=""; keyfile=""; keepkey=no; start=yes
while [ $# -gt 0 ]; do
  case "$1" in
    --key-file) [ $# -ge 2 ] || usage; keyfile="$2"; shift 2 ;;
    --keep-key) keepkey=yes; shift ;;
    --no-start) start=no; shift ;;
    -h|--help) usage ;;
    -*) usage ;;
    *) [ -z "$archive" ] || usage; archive="$1"; shift ;;
  esac
done
[ -n "$archive" ] || usage
[ "$(id -u)" -eq 0 ] || [ -n "${CICORD_DATA_DIR:-}" ] || die "run as root (sudo)"
[ -f "$archive" ] || die "$archive: no such file"
t0="$(date +%s)"
umask 077

# 1. The key, before anything changes.
key=""
if [ -n "$keyfile" ]; then
  [ -f "$keyfile" ] || die "$keyfile: no such file"
  key="$(tr -d ' \r\n' < "$keyfile")"
elif [ "$keepkey" = no ]; then
  [ -t 0 ] && echo "Paste the secret store key (secret.key), then Enter and Ctrl-D:" >&2
  key="$(tr -d ' \r\n')"
fi
if [ "$keepkey" = no ]; then
  # secret.key: 32 bytes, base64 (standard or URL, padded or not) or hex, as the runtime reads it.
  printf '%s' "$key" | grep -Eq '^([A-Za-z0-9+/_-]{43}=?|[0-9A-Fa-f]{64})$' ||
    die "the key is not a secret.key (32 bytes in base64 or hex); pass --key-file, or --keep-key to keep the volume's key"
fi

# 2. The archive.
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
zstd -q -t "$archive" || die "$archive does not decompress"
if ! { zstd -dc "$archive" > "$WORK/backup.tar" && tar -C "$WORK" -xf "$WORK/backup.tar"; }; then
  die "$archive cannot be extracted"
fi
rm -f "$WORK/backup.tar"
for k in secret.key link.key; do [ ! -e "$WORK/$k" ] || rm -f "$WORK/$k"; done
db="$(sed -n 's/^database=//p' "$WORK/MANIFEST" 2>/dev/null)"; db="${db:-sqlite}"
if [ "$db" = sqlite ]; then
  [ -f "$WORK/cicord.db" ] || die "$archive has no cicord.db"
  db_ok "$WORK/cicord.db" || die "the cicord.db of $archive fails SQLite's integrity check"
  want="$(sed -n 's/^cicord.db.sha256=//p' "$WORK/MANIFEST" 2>/dev/null)"
  [ -z "$want" ] || [ "$want" = "$(sha256 "$WORK/cicord.db")" ] || die "the cicord.db of $archive does not match its MANIFEST checksum"
fi
log "archive $archive: $(tr '\n' ' ' < "$WORK/MANIFEST" 2>/dev/null)"
img="$(sed -n 's/^image=//p' "$WORK/MANIFEST" 2>/dev/null)"
if [ -n "$img" ] && [ "$img" != "$(envget CICORD_IMAGE)" ]; then
  log "note: the backup was taken with $img; $CICORD_ENV has CICORD_IMAGE=$(envget CICORD_IMAGE) (use the same version or a newer one: an older binary refuses a newer database)"
fi

# 3. Stop the runtime; the volume exists once the container was created.
if running; then compose stop cicord >/dev/null || die "could not stop the runtime"; fi
if [ -z "${CICORD_DATA_DIR:-}" ]; then compose up --no-start cicord >/dev/null 2>&1 || true; fi
data="$(data_dir)" || true
[ -n "$data" ] && [ -d "$data" ] || die "the data volume ${CICORD_PROJECT}_cicord-data was not found: run 'docker compose up --no-start cicord' in $CICORD_DIR"

# 4. The current state goes aside (never deleted).
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
aside="$CICORD_BACKUP_DIR/pre-restore-$stamp"
[ ! -e "$aside" ] || aside="$aside-$$"
moved=no
for f in cicord.db cicord.db-wal cicord.db-shm repo.git writeback secret.key; do
  if [ -e "$data/$f" ]; then
    if [ "$f" = secret.key ] && [ "$keepkey" = yes ]; then continue; fi
    mkdir -p "$aside"; mv "$data/$f" "$aside/"; moved=yes
  fi
done
[ "$moved" = no ] || log "the previous state is in $aside (delete it once the restore is verified)"

# 5. Restore.
if [ "$db" = sqlite ]; then cp "$WORK/cicord.db" "$data/cicord.db"; chmod 600 "$data/cicord.db"; fi
for d in repo.git writeback; do
  if [ -d "$WORK/$d" ]; then cp -a "$WORK/$d" "$data/"; fi
done
if [ "$keepkey" = no ]; then
  printf '%s\n' "$key" > "$data/secret.key"
elif [ ! -f "$data/secret.key" ]; then
  die "--keep-key, but $data has no secret.key"
fi
chmod 600 "$data/secret.key"
own_data "$data"
chmod 700 "$data"
log "restored $archive into $data"

[ "$start" = yes ] || { log "not started (--no-start). Start it: docker compose up -d cicord"; exit 0; }

# 6. Start and verify.
compose up -d cicord >/dev/null || die "the runtime did not start: docker compose logs cicord"
healthy "${CICORD_RESTORE_WAIT:-180}" || die "the runtime is not healthy: docker logs cicord (a wrong key fails at startup)"
if supports_service_check; then
  rc=0; service_check 5m || rc=$?
  case "$rc" in
    0) ;;
    2) log "service check skipped: no runtime token is stored (cicord secret set CICORD_RUNTIME_TOKEN); check the admin console overview" ;;
    *) die "the runtime started but does not pass the service check (see above): check the admin console" ;;
  esac
else
  log "this version has no service check: check the admin console overview"
fi
log "restore verified in $(( $(date +%s) - t0 )) s. Now check a ticket (or other plugin data) in the admin console, and that the next plan is empty: cicord plan --remote"
