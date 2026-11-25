#!/bin/sh
# 06-auto-update.sh — automatic updates of the Cicord runtime to new releases (idempotent).
# Run as root: sudo sh 06-auto-update.sh
#
# Installs /usr/local/sbin/cicord-update (bin/cicord-update, with lib/cicord-ops.sh as
# /usr/local/lib/cicord/ops.sh) and a systemd timer (daily, around 05:30 UTC: after the 03:30
# backup and the 04:00 unattended-upgrades reboot window). Each run:
#   1. reads the running image from /opt/cicord/.env (CICORD_IMAGE); an image that is not a
#      release of ghcr.io/cicord/cicord (a build from source) is left alone;
#   2. asks GitHub for the latest release of cicord/cicord and applies CICORD_AUTO_UPDATE:
#        patch (default)  0.1.x → 0.1.y only (0.x minor versions may change formats)
#        minor            also 0.1.x → 0.2.0: read the release notes and Upgrading first
#        off              never; the run only logs what is available
#   3. accepts the release only if the release workflow created it (author
#      github-actions[bot]) and its notes give the image digest, then verifies the image's
#      build provenance (CICORD_UPDATE_VERIFY):
#        attestation (default)  gh attestation verify: the digest was built by
#                               .github/workflows/release.yml from the tag of that release.
#                               Needs a GitHub token in /etc/cicord/github-token (0600): a
#                               fine-grained token with read-only access to public repositories
#                               and no permissions. Without it, updates are refused.
#        digest                 the release notes' digest only (weaker: trusts the release
#                               text, not a signature)
#   4. checks that the running version passes the service check (below): if it does not
#      (a degraded plugin, a paused server…), the update waits, so that a new version is
#      never rolled back for a problem it did not cause;
#   5. pulls the image BY THAT DIGEST, backs up (cicord-backup.service: a failed backup
#      stops the update), stops the runtime and copies cicord.db, switches CICORD_IMAGE to
#      <image>:<version>@<digest> and starts it;
#   6. keeps it only if the Docker health check passes within 3 minutes AND the service
#      check passes within 5 (CICORD_UPDATE_CHECK=service, the default; `health` skips it):
#      `cicord healthcheck --service` inside the container, i.e. `cicord status --wait`
#      with the runtime token: the active revision is loaded, every server is Synced (boost
#      waits are fine) and not paused, every bot and plugin instance is Healthy. A runtime
#      token is stored when there is none (a random one, used by the check only);
#   7. otherwise rolls back to the previous image digest; if the new version migrated the
#      database (schema_migrations changed), the copy of step 5 is restored first (an older
#      binary refuses a newer schema; what the new version wrote is lost), and an alert is
#      sent through the runtime's alert webhook.
# Logs: journalctl -t cicord-update. Run it now: sudo systemctl start cicord-update.service
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
apt-get -y -q install curl jq sqlite3 >/dev/null
# GitHub CLI from GitHub's apt repository (gh attestation verify).
if ! command -v gh >/dev/null; then
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg
  chmod 0644 /etc/apt/keyrings/githubcli-archive-keyring.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    > /etc/apt/sources.list.d/github-cli.list
  apt-get -q update >/dev/null && apt-get -y -q install gh >/dev/null
fi
install -d -m 0700 /etc/cicord

HERE="$(cd "$(dirname "$0")" && pwd)"
install -d -m 0755 /usr/local/lib/cicord
install -m 0644 "$HERE/lib/cicord-ops.sh" /usr/local/lib/cicord/ops.sh
install -m 0700 "$HERE/bin/cicord-update" /usr/local/sbin/cicord-update

cat > /etc/systemd/system/cicord-update.service <<'EOF'
[Unit]
Description=Update the Cicord runtime to a new release
After=docker.service network-online.target
Wants=network-online.target
Requires=docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/cicord-update
EOF
cat > /etc/systemd/system/cicord-update.timer <<'EOF'
[Unit]
Description=Daily check for a new Cicord release

[Timer]
OnCalendar=*-*-* 05:30:00 UTC
RandomizedDelaySec=30m
Persistent=true

[Install]
WantedBy=timers.target
EOF
grep -q '^CICORD_AUTO_UPDATE=' /opt/cicord/.env 2>/dev/null || echo 'CICORD_AUTO_UPDATE=patch' >> /opt/cicord/.env
systemctl daemon-reload
systemctl enable --now cicord-update.timer >/dev/null
systemctl list-timers cicord-update.timer --no-pager | head -3
