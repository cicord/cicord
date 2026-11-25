#!/bin/sh
# 03-ssh-firewall.sh — SSH hardening and firewall (idempotent).
# Run as root over the TAILNET (ssh ubuntu@<tailscale IP>), never over the public IP:
# once the firewall is on, SSH is only reachable through tailscale0.
#   sudo sh 03-ssh-firewall.sh [ssh-user]
#
# Exposure afterwards:
#   public:   80/tcp, 443/tcp (the community portal, served by Caddy), 41641/udp (Tailscale)
#   tailnet:  22/tcp (SSH) — the admin console is published by `tailscale serve`
#   nothing else: the Cicord runtime only listens on 127.0.0.1.
# Fallback if the tailnet is unreachable: the provider's KVM console / rescue mode.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
SSH_USER="${1:-ubuntu}"
if ! { command -v tailscale >/dev/null && tailscale ip -4 >/dev/null 2>&1; }; then
  echo "Tailscale is not up: run 02-tailscale.sh first" >&2; exit 1
fi

echo "==> sshd"
cat > /etc/ssh/sshd_config.d/10-cicord.conf <<EOF
# Cicord host: key-only logins for $SSH_USER, no root, no forwarding.
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
AuthenticationMethods publickey
AllowUsers $SSH_USER
X11Forwarding no
AllowAgentForwarding no
AllowTcpForwarding no
PermitTunnel no
MaxAuthTries 3
MaxSessions 4
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
EOF
sshd -t
systemctl reload ssh

echo "==> firewall (ufw)"
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw default deny routed
ufw allow in on tailscale0 to any port 22 proto tcp comment 'SSH, tailnet only'
ufw allow 80/tcp comment 'HTTP (ACME, redirect to HTTPS)'
ufw allow 443/tcp comment 'HTTPS: community portal'
ufw allow 41641/udp comment 'Tailscale direct connections'
ufw logging low
ufw --force enable
ufw status verbose
