#!/bin/sh
# 05-backup.sh — daily backup of the Cicord runtime state, copied off the server (idempotent).
# Run as root: sudo sh 05-backup.sh [compose project name]
#
# Installs /usr/local/sbin/cicord-backup (bin/cicord-backup, with lib/cicord-ops.sh as
# /usr/local/lib/cicord/ops.sh) and a systemd timer (daily, 03:30 UTC). Each run writes
# /var/backups/cicord/cicord-<UTC timestamp>.tar.zst (mode 0600) with:
#   - cicord.db: an online, consistent copy (sqlite3 .backup, safe while the runtime runs),
#     checked with PRAGMA integrity_check;
#   - the embedded repository (repo.git) and the write-back clone, when present;
#   - MANIFEST: the image, the schema version and the database checksum.
# The encryption key secret.key is NEVER in an archive: the archives hold the secrets
# encrypted, the key is kept once, apart (password manager), so a stolen backup reveals
# nothing. Restoring needs both (07-restore.sh). link.key is not included either: it is
# regenerated when missing (outstanding portal links then stop working, nothing else).
#
# Off-host copy, after each backup, from /opt/cicord/.env:
#   CICORD_BACKUP_REMOTE=rclone:<remote>:<path>       e.g. rclone:b2:my-bucket/cicord
#   CICORD_BACKUP_REMOTE=rsync:[user@]host:<path>     e.g. rsync:backup@nas.example.org:/srv/cicord
#   CICORD_BACKUP_KEEP_DAYS=14                        local retention
# A run fails (non-zero exit, the unit is failed, and an alert is sent when the runtime has an
# alert webhook) when cicord.db is missing, fails its integrity check, or the copy fails.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
PROJECT="${1:-cicord}"
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=SCRIPTDIR/lib/cicord-ops.sh
. "$HERE/lib/cicord-ops.sh"
apt-get -y -q install sqlite3 zstd rsync >/dev/null
case "$(envget CICORD_BACKUP_REMOTE)" in
  rclone:*) command -v rclone >/dev/null || apt-get -y -q install rclone >/dev/null ;;
esac
install -d -m 0755 /usr/local/lib/cicord
install -m 0644 "$HERE/lib/cicord-ops.sh" /usr/local/lib/cicord/ops.sh
install -m 0700 "$HERE/bin/cicord-backup" /usr/local/sbin/cicord-backup

cat > /etc/systemd/system/cicord-backup.service <<UNIT
[Unit]
Description=Backup of the Cicord runtime state
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
Environment=CICORD_PROJECT=$PROJECT
ExecStart=/usr/local/sbin/cicord-backup
Nice=10
IOSchedulingClass=idle
UNIT
cat > /etc/systemd/system/cicord-backup.timer <<'UNIT'
[Unit]
Description=Daily backup of the Cicord runtime state

[Timer]
OnCalendar=*-*-* 03:30:00 UTC
Persistent=true
RandomizedDelaySec=5m

[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
systemctl enable --now cicord-backup.timer >/dev/null
systemctl list-timers cicord-backup.timer --no-pager | head -3
[ -n "$(envget CICORD_BACKUP_REMOTE)" ] ||
  echo "No CICORD_BACKUP_REMOTE in $CICORD_ENV: backups stay on this server. See https://cicord.dev/docs/self-hosting/backups-and-secrets/#off-host-copies"
