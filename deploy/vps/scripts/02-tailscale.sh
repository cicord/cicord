#!/bin/sh
# 02-tailscale.sh — install Tailscale from its official repository and join the tailnet.
# Run as root: sudo sh 02-tailscale.sh [hostname]
# `tailscale up` prints a login URL: open it with an account of the tailnet.
# The admin console of Cicord will only be reachable through this tailnet.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
NAME="${1:-cicord}"
. /etc/os-release
export DEBIAN_FRONTEND=noninteractive

if ! command -v tailscale >/dev/null 2>&1; then
  echo "==> Tailscale repository ($VERSION_CODENAME)"
  install -d -m 0755 /usr/share/keyrings
  curl -fsSL "https://pkgs.tailscale.com/stable/ubuntu/$VERSION_CODENAME.noarmor.gpg" -o /usr/share/keyrings/tailscale-archive-keyring.gpg
  curl -fsSL "https://pkgs.tailscale.com/stable/ubuntu/$VERSION_CODENAME.tailscale-keyring.list" -o /etc/apt/sources.list.d/tailscale.list
  apt-get update -q
  apt-get -y -q install tailscale
fi
systemctl enable --now tailscaled >/dev/null

if ! tailscale status >/dev/null 2>&1; then
  echo "==> joining the tailnet as $NAME (open the URL below)"
  tailscale up --hostname="$NAME" --ssh=false
fi
tailscale status --self --peers=false
echo "tailscale IPv4: $(tailscale ip -4)"
