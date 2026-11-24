#!/bin/sh
# 06-auto-update.sh — automatic updates of the Cicord runtime to new releases (idempotent).
# Run as root: sudo sh 06-auto-update.sh
#
# Installs /usr/local/sbin/cicord-update and a systemd timer (daily, around 05:30 UTC: after
# the 03:30 backup and the 04:00 unattended-upgrades reboot window). Each run:
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
#      and pulls the image BY THAT DIGEST, so what runs is byte for byte what was verified;
#   4. backs up (cicord-backup.service), switches CICORD_IMAGE to <image>:<version>@<digest>,
#      restarts the runtime and waits for it to be healthy; otherwise it puts the previous
#      image back (a patch release does not change data formats; a backup is there anyway).
# Logs: journalctl -t cicord-update. Run it now: sudo systemctl start cicord-update.service
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
apt-get -y -q install curl jq >/dev/null
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

cat > /usr/local/sbin/cicord-update <<'EOF'
#!/bin/sh
set -eu
DIR=/opt/cicord
ENV="$DIR/.env"
REPO=cicord/cicord
IMAGE=ghcr.io/cicord/cicord
log() { logger -t cicord-update -- "$*"; echo "$*"; }
compose() { docker compose --project-directory "$DIR" -f "$DIR/compose.yaml" --env-file "$ENV" "$@"; }
setenv() { # setenv NAME VALUE: replace the line in .env, keeping its mode (0600)
  tmp="$(mktemp "$DIR/.env.XXXXXX")"
  sed "s|^$1=.*|$1=$2|" "$ENV" > "$tmp" && chmod 600 "$tmp" && mv "$tmp" "$ENV"
}
healthy() { # wait up to 3 minutes for the container's health check
  i=0
  while [ $i -lt 36 ]; do
    [ "$(docker inspect -f '{{.State.Health.Status}}' cicord 2>/dev/null)" = healthy ] && return 0
    sleep 5; i=$((i + 1))
  done
  return 1
}

policy="$(sed -n 's/^CICORD_AUTO_UPDATE=//p' "$ENV")"; policy="${policy:-patch}"
verify="$(sed -n 's/^CICORD_UPDATE_VERIFY=//p' "$ENV")"; verify="${verify:-attestation}"
current="$(sed -n 's/^CICORD_IMAGE=//p' "$ENV")"
case "$current" in
  "$IMAGE":*) ;;
  *) log "CICORD_IMAGE=$current is not a release image of $IMAGE: nothing to do"; exit 0 ;;
esac
cur="${current#"$IMAGE":}"; cur="${cur%%@*}"

json="$(curl -fsS --max-time 30 -H 'Accept: application/vnd.github+json' \
  "https://api.github.com/repos/$REPO/releases/latest")"
tag="$(printf '%s' "$json" | jq -r '.tag_name // empty')"
new="${tag#v}"
echo "$new" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { log "latest release '$tag' is not a version: skipped"; exit 0; }
[ "$new" != "$cur" ] || { log "up to date ($cur)"; exit 0; }
[ "$(printf '%s\n%s\n' "$cur" "$new" | sort -V | tail -1)" = "$new" ] || { log "running $cur, newer than the latest release $new"; exit 0; }

cmaj="${cur%%.*}"; cmin="$(echo "$cur" | cut -d. -f2)"
nmaj="${new%%.*}"; nmin="$(echo "$new" | cut -d. -f2)"
allowed=no
case "$policy" in
  patch) [ "$cmaj.$cmin" = "$nmaj.$nmin" ] && allowed=yes ;;
  minor) [ "$cmaj" = "$nmaj" ] && allowed=yes ;;
  off) ;;
  *) log "unknown CICORD_AUTO_UPDATE=$policy (patch, minor or off)"; exit 1 ;;
esac
if [ "$allowed" != yes ]; then
  log "Cicord $new is available (running $cur); CICORD_AUTO_UPDATE=$policy does not apply it: read https://github.com/$REPO/releases/tag/$tag and https://cicord.dev/docs/self-hosting/upgrading/"
  exit 0
fi

author="$(printf '%s' "$json" | jq -r '.author.login // empty')"
[ "$author" = "github-actions[bot]" ] || { log "release $tag was not created by the release workflow (author '$author'): refused"; exit 1; }
digest="$(printf '%s' "$json" | jq -r '.body // empty' | grep -oE "$IMAGE@sha256:[0-9a-f]{64}" | head -1 | cut -d@ -f2)"
[ -n "$digest" ] || { log "release $tag gives no image digest: refused"; exit 1; }
case "$verify" in
  attestation)
    if [ ! -s /etc/cicord/github-token ] || ! command -v gh >/dev/null; then
      log "Cicord $new is available, but its provenance cannot be verified: put a GitHub token in /etc/cicord/github-token (see https://cicord.dev/docs/self-hosting/production-vps/#9-updates)"
      exit 1
    fi
    if ! GH_TOKEN="$(cat /etc/cicord/github-token)" GH_PROMPT_DISABLED=1 gh attestation verify "oci://$IMAGE@$digest" \
      --repo "$REPO" --signer-workflow "$REPO/.github/workflows/release.yml" --source-ref "refs/tags/$tag" >/dev/null 2>&1; then
      log "release $tag: the build provenance of $IMAGE@$digest does not verify: refused"
      exit 1
    fi ;;
  digest) log "CICORD_UPDATE_VERIFY=digest: provenance not verified, trusting the release notes" ;;
  *) log "unknown CICORD_UPDATE_VERIFY=$verify (attestation or digest)"; exit 1 ;;
esac

log "updating Cicord $cur → $new ($IMAGE@$digest, verified: $verify)"
docker pull -q "$IMAGE@$digest" >/dev/null
systemctl start cicord-backup.service
setenv CICORD_IMAGE "$IMAGE:$new@$digest"
compose up -d cicord >/dev/null 2>&1 || true   # a failed start is caught by the health check
if healthy; then
  log "Cicord $new is running"
  docker image prune -f >/dev/null 2>&1 || true
  exit 0
fi
log "Cicord $new is not healthy: rolling back to $current"
setenv CICORD_IMAGE "$current"
compose up -d cicord >/dev/null 2>&1 || true
if healthy; then log "rolled back to $cur"; else log "rollback to $cur is not healthy either: check the server"; fi
exit 1
EOF
chmod 700 /usr/local/sbin/cicord-update

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
