#!/bin/sh
# 01-base.sh — base hardening of a fresh Ubuntu server for Cicord (idempotent).
# Run as root: sudo sh 01-base.sh [hostname]
#
# - full upgrade and the packages the next steps need
# - hostname
# - automatic updates: security AND regular updates, daily, with an automatic reboot at
#   04:00 UTC when a kernel or core library update requires it
# - needrestart restarts services automatically after library updates
# - kernel and network hardening (sysctl), journald size cap
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
HOSTNAME_NEW="${1:-cicord}"
export DEBIAN_FRONTEND=noninteractive

echo "==> packages"
apt-get update -q
apt-get -y -q -o Dpkg::Options::=--force-confold full-upgrade
apt-get -y -q install ca-certificates curl gnupg ufw unattended-upgrades apt-listchanges needrestart sqlite3

echo "==> hostname: $HOSTNAME_NEW"
hostnamectl set-hostname "$HOSTNAME_NEW"
if ! grep -q "[[:space:]]$HOSTNAME_NEW\$" /etc/hosts; then
  printf '127.0.1.1\t%s\n' "$HOSTNAME_NEW" >> /etc/hosts
fi

echo "==> automatic updates"
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Download-Upgradeable-Packages "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
cat > /etc/apt/apt.conf.d/52cicord-unattended-upgrades <<'EOF'
// Cicord host: install security and regular updates, from Ubuntu and from the
// Docker and Tailscale repositories, and reboot at 04:00 UTC when required.
Unattended-Upgrade::Allowed-Origins {
        "${distro_id}:${distro_codename}";
        "${distro_id}:${distro_codename}-security";
        "${distro_id}ESMApps:${distro_codename}-apps-security";
        "${distro_id}ESM:${distro_codename}-infra-security";
        "${distro_id}:${distro_codename}-updates";
};
// The Docker and Tailscale repositories (Tailscale's Release file has no Suite field,
// so match on origin and codename).
Unattended-Upgrade::Origins-Pattern {
        "origin=Docker,codename=${distro_codename}";
        "origin=Tailscale,codename=${distro_codename}";
};
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
Unattended-Upgrade::Remove-New-Unused-Dependencies "true";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-WithUsers "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:00";
Unattended-Upgrade::SyslogEnable "true";
EOF
# needrestart: restart services automatically after library upgrades (no prompt).
mkdir -p /etc/needrestart/conf.d
cat > /etc/needrestart/conf.d/50-cicord.conf <<'EOF'
$nrconf{restart} = 'a';
EOF
systemctl enable --now unattended-upgrades.service >/dev/null

echo "==> sysctl hardening"
cat > /etc/sysctl.d/60-cicord-hardening.conf <<'EOF'
# Network: no redirects or source routing, reverse-path filtering, SYN cookies.
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv6.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0
net.ipv4.conf.all.accept_source_route = 0
net.ipv4.conf.default.accept_source_route = 0
net.ipv6.conf.all.accept_source_route = 0
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
net.ipv4.tcp_syncookies = 1
net.ipv4.icmp_echo_ignore_broadcasts = 1
net.ipv4.conf.all.log_martians = 1
# Kernel: hide kernel pointers and dmesg from unprivileged users, restrict ptrace.
kernel.kptr_restrict = 2
kernel.dmesg_restrict = 1
kernel.yama.ptrace_scope = 1
fs.protected_symlinks = 1
fs.protected_hardlinks = 1
fs.suid_dumpable = 0
EOF
sysctl --system >/dev/null

echo "==> journald size cap"
mkdir -p /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/50-cicord.conf <<'EOF'
[Journal]
SystemMaxUse=1G
MaxRetentionSec=1month
EOF
systemctl restart systemd-journald

echo "==> done (reboot if /var/run/reboot-required exists)"
[ -f /var/run/reboot-required ] && echo "reboot required" || true
