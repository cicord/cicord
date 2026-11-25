#!/bin/sh
# 04-docker.sh — Docker Engine from Docker's official repository, hardened defaults
# (idempotent). Run as root: sudo sh 04-docker.sh
#
# - containers cannot gain privileges (no-new-privileges), no inter-container traffic on
#   the default bridge (compose networks are used instead), live-restore keeps containers
#   running while dockerd is upgraded, logs rotated by the local driver;
# - the login user is NOT added to the docker group (membership is root-equivalent):
#   use sudo for docker commands;
# - Docker bypasses ufw for published ports: only publish what must be public (Caddy on
#   80/443) and bind everything else to 127.0.0.1.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
# shellcheck source=/dev/null
. /etc/os-release
export DEBIAN_FRONTEND=noninteractive

if ! command -v docker >/dev/null 2>&1; then
  echo "==> Docker repository ($VERSION_CODENAME)"
  # Distribution packages that would conflict with docker-ce.
  for p in docker.io docker-doc docker-compose docker-compose-v2 podman-docker containerd runc; do
    apt-get -y -q remove "$p" >/dev/null 2>&1 || true
  done
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  cat > /etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $VERSION_CODENAME
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF
  apt-get update -q
  apt-get -y -q install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi

echo "==> daemon.json"
install -d -m 0755 /etc/docker
cat > /etc/docker/daemon.json <<'EOF'
{
  "no-new-privileges": true,
  "icc": false,
  "live-restore": true,
  "userland-proxy": false,
  "log-driver": "local",
  "log-opts": { "max-size": "20m", "max-file": "5" }
}
EOF
systemctl enable docker.service containerd.service >/dev/null
systemctl restart docker
docker version --format 'docker {{.Server.Version}}'
docker compose version
docker info --format 'security options: {{.SecurityOptions}}'
