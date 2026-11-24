# Production install on a VPS

The files of the hardened single-server install of Cicord, in the order they are used:

- `scripts/`: the numbered steps (`01-base.sh`, `02-tailscale.sh`, …): base system and
  automatic updates, Tailscale, SSH and firewall, Docker, daily backups, automatic runtime updates
  (idempotent, run with `sudo`);
- `compose.yaml`: the Cicord runtime and Caddy;
- `Caddyfile`: the public community portal;
- `.env.example`: the image and the addresses for `compose.yaml` (copy it to `.env`; no
  secrets).

The guide is [Production on a VPS](https://cicord.dev/docs/self-hosting/production-vps/).
