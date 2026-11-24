#!/bin/sh
# 05-backup.sh — daily local backup of the Cicord runtime state (idempotent).
# Run as root: sudo sh 05-backup.sh [compose project name]
#
# Installs /usr/local/sbin/cicord-backup and a systemd timer (daily, 03:30 UTC) that
# writes /var/backups/cicord/cicord-<UTC timestamp>.tar.zst (mode 0600) with:
#   - cicord.db: an online, consistent copy (sqlite3 .backup, safe while the runtime runs)
#   - secret.key and link.key: WITHOUT secret.key the encrypted secrets are unreadable
#   - the embedded repository (repo.git) and write-back clone, when present
# Backups older than 14 days are deleted. Copy them off the server too (provider backup
# storage, another machine): a local backup does not survive the loss of the server.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
PROJECT="${1:-cicord}"
apt-get -y -q install sqlite3 zstd >/dev/null

cat > /usr/local/sbin/cicord-backup <<EOF
#!/bin/sh
set -eu
umask 077
DATA="\$(docker volume inspect -f '{{.Mountpoint}}' ${PROJECT}_cicord-data)"
OUT=/var/backups/cicord
STAMP="\$(date -u +%Y%m%dT%H%M%SZ)"
WORK="\$(mktemp -d)"
trap 'rm -rf "\$WORK"' EXIT
mkdir -p "\$OUT"
if [ -f "\$DATA/cicord.db" ]; then
  sqlite3 "\$DATA/cicord.db" ".backup '\$WORK/cicord.db'"
  sqlite3 "\$WORK/cicord.db" "PRAGMA integrity_check;" | grep -qx ok
fi
for f in secret.key link.key; do [ -f "\$DATA/\$f" ] && cp -p "\$DATA/\$f" "\$WORK/"; done
for d in repo.git writeback; do [ -d "\$DATA/\$d" ] && cp -a "\$DATA/\$d" "\$WORK/"; done
tar -C "\$WORK" -cf - . | zstd -q -19 -o "\$OUT/cicord-\$STAMP.tar.zst"
chmod 600 "\$OUT/cicord-\$STAMP.tar.zst"
find "\$OUT" -name 'cicord-*.tar.zst' -mtime +14 -delete
echo "backup: \$OUT/cicord-\$STAMP.tar.zst"
EOF
chmod 700 /usr/local/sbin/cicord-backup

cat > /etc/systemd/system/cicord-backup.service <<'EOF'
[Unit]
Description=Backup of the Cicord runtime state
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/cicord-backup
Nice=10
IOSchedulingClass=idle
EOF
cat > /etc/systemd/system/cicord-backup.timer <<'EOF'
[Unit]
Description=Daily backup of the Cicord runtime state

[Timer]
OnCalendar=*-*-* 03:30:00 UTC
Persistent=true
RandomizedDelaySec=5m

[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload
systemctl enable --now cicord-backup.timer >/dev/null
systemctl list-timers cicord-backup.timer --no-pager | head -3
