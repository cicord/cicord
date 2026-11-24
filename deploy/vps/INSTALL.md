# Production install on a VPS — log

The steps used to install the official Cicord community runtime, in order. Each step is a
script in [`scripts/`](scripts) (idempotent, run with `sudo`), so the install can be
replayed on a fresh server. This log is the source of the docs tutorial
"Production install on a VPS".

Target: OVH VPS, fresh **Ubuntu 26.04 LTS** (x86_64, 6 vCPU, 11 GiB RAM, 96 GiB disk), user
`ubuntu` with passwordless sudo, SSH key only.

Topology:

| What | Where | Reachable from |
|---|---|---|
| Community portal, Discord login for members, GitHub webhook | `https://community.cicord.dev` → Caddy (80/443) → `127.0.0.1:7420` | internet |
| Admin console and admin API, setup wizard | `https://cicord.<tailnet>.ts.net` → `tailscale serve` → `127.0.0.1:7420` | tailnet only |
| SSH | port 22 on `tailscale0` | tailnet only |
| Cicord runtime | Docker, `127.0.0.1:7420` | the host only |
| Fallback | provider KVM console / rescue mode | provider account |

## 0. Audit of the fresh server

Only `sshd` listens (22, public). Key-only auth, root has no key. No firewall.
`unattended-upgrades` is installed (security updates only). Time synced by chrony, UTC.

Local workstation: a `Host *` block with `ForwardAgent yes` forwards the SSH agent (here the
1Password agent) to every server: anyone root on the server could use the keys while you are
connected. Add a dedicated block **before** `Host *` (the first value found wins):

```sshconfig
Host cicord
  HostName cicord.<tailnet>.ts.net
  User ubuntu
  ForwardAgent no
```

## 1. Base system — `scripts/01-base.sh`

```sh
scp deploy/vps/scripts/01-base.sh ubuntu@<server>:/tmp/ && ssh ubuntu@<server> 'sudo sh /tmp/01-base.sh cicord'
ssh ubuntu@<server> 'sudo systemctl reboot'   # a new kernel was installed
```

- full upgrade; packages `ufw`, `unattended-upgrades`, `needrestart`, `sqlite3`…
- hostname `cicord`;
- automatic updates: security **and** regular updates from Ubuntu, Docker and Tailscale,
  daily; automatic reboot at 04:00 UTC when required; `needrestart` restarts services after
  library updates. Check with `sudo unattended-upgrade --dry-run --debug`;
- sysctl hardening (no ICMP redirects or source routing, rp_filter, SYN cookies,
  kptr/dmesg restrictions, ptrace scope);
- journald capped at 1 GiB / one month.

## 2. Tailscale — `scripts/02-tailscale.sh`

```sh
ssh ubuntu@<server> 'sudo sh /tmp/02-tailscale.sh cicord'   # prints a login URL: open it
```

- official Tailscale apt repository, `tailscale up --hostname=cicord --ssh=false`;
- in the Tailscale admin console: **disable key expiry** for the machine (otherwise it
  leaves the tailnet when the key expires, with SSH and the admin console), and enable
  **HTTPS certificates** (DNS settings) for `tailscale serve`;
- if the machine registered under another name: `sudo tailscale set --hostname=cicord`.
- check from the workstation: `tailscale ping cicord`, `ssh ubuntu@<tailscale IP>`.

## 3. SSH and firewall — `scripts/03-ssh-firewall.sh`

Run it **over the tailnet** (after it, SSH is not reachable from the internet):

```sh
ssh ubuntu@<tailscale IP> 'sudo sh /tmp/03-ssh-firewall.sh ubuntu'
```

- `sshd_config.d/10-cicord.conf`: no root, no passwords, public key only, `AllowUsers ubuntu`,
  no agent/TCP/X11 forwarding, `MaxAuthTries 3`, `LoginGraceTime 30`;
- ufw: deny incoming by default; allow `22/tcp` **on `tailscale0` only**, `80/tcp` and
  `443/tcp` (Caddy), `41641/udp` (Tailscale direct connections). sshd keeps listening on all
  addresses so a late Tailscale start never locks it out; the firewall does the filtering.
- checks: `sudo sshd -T`, `sudo ufw status verbose`; from the workstation, SSH to the public
  IP must time out.

## 4. Docker — `scripts/04-docker.sh`

```sh
ssh cicord 'sudo sh /tmp/04-docker.sh'
```

- Docker Engine and the compose plugin from Docker's official repository (distribution
  packages removed first);
- `daemon.json`: `no-new-privileges`, `icc: false`, `live-restore`, no userland proxy, `local`
  log driver with rotation;
- the login user is **not** in the `docker` group (root-equivalent): use `sudo docker …`;
- Docker bypasses ufw for published ports: only Caddy publishes 80/443, the runtime binds
  `127.0.0.1:7420`.

## 5. DNS, Discord application, GitHub App (in the providers' consoles)

### DNS

At the DNS provider of the domain, for the public portal host (here `community.cicord.dev`):

| Type | Name | Value |
|---|---|---|
| `A` | `community` | the server's public IPv4 |
| `AAAA` | `community` | the server's public IPv6 |

Check: `dig +short A community.cicord.dev` and `dig +short AAAA community.cicord.dev`.
Caddy obtains the TLS certificate on its first start (ports 80 and 443 must reach it).

### Discord server and application

1. Create the Discord server (you are its owner).
2. [Discord Developer Portal](https://discord.com/developers/applications) → **New Application**
   (here "Cicord").
3. **Bot** tab:
   - the bot exists with the application; **Reset Token** shows its token once — do not paste
     it anywhere but the setup wizard (step 7), it is stored encrypted on the server;
   - **Privileged Gateway Intents**: enable **Server Members Intent** (member events,
     onboarding, moderation) and **Message Content Intent** (levels, moderation of message
     content). Presence Intent is not used;
   - leave **Public Bot** off if only you should be able to invite it.
4. **OAuth2** tab:
   - **Client ID**: shown; **Client Secret**: **Reset Secret** shows it once — it goes into the
     setup wizard (Discord login step), never into a file;
   - **Redirects**: add both callback URLs, exactly:
     - `https://community.cicord.dev/auth/callback` (members, public portal)
     - `https://cicord.<tailnet>.ts.net/auth/callback` (admin console, tailnet)
5. The bot is invited from the setup wizard (it prints the invite URL with the permissions
   Cicord needs).

### GitHub repository and GitHub App

1. Create the configuration repository, empty (here the public `cicord/cicord-community`):
   the setup wizard writes the first commit.
2. Organization **Settings → Developer settings → GitHub Apps → New GitHub App**:
   - **GitHub App name**: e.g. "Cicord Community"; **Homepage URL**: `https://cicord.dev`;
   - **Webhook**: active; **Webhook URL** `https://community.cicord.dev/hooks/github`;
     **Webhook secret**: a long random value (`openssl rand -hex 32`), kept in the password
     manager — it becomes the runtime secret `CICORD_WEBHOOK_SECRET`;
   - **Repository permissions**:

     | Permission | Access | Used for |
     |---|---|---|
     | Contents | Read and write | fetching revisions, write-back commits |
     | Pull requests | Read and write | the `pullRequest` write-back mode |
     | Commit statuses | Read and write | the `cicord/sync` status on each revision |
     | Metadata | Read-only | mandatory |

     No organization or account permission;
   - **Subscribe to events**: **Push** (it appears once Contents is selected). Without it the
     runtime still notices new commits, by polling every minute;
   - **Where can this GitHub App be installed?** Only on this account.
3. After creating it: note the **App ID**; **Private keys → Generate a private key** downloads
   a `.pem` — keep it in the password manager, it goes into the setup wizard.
4. **Install App** → the organization → **Only select repositories** → the configuration
   repository only.

Check: `gh api orgs/<org>/installations --jq '.installations[] | {app_slug, permissions, events}'`
shows the four permissions and `events: ["push"]`.

## 6. Deploy the runtime and Caddy — `compose.yaml`, `Caddyfile`

On the workstation, from the repository (macOS: `COPYFILE_DISABLE=1` keeps `._*` metadata
files out of the archives):

```sh
ssh cicord 'sudo install -d -m 0750 /opt/cicord'
COPYFILE_DISABLE=1 tar -C deploy/vps -cf - compose.yaml Caddyfile .env.example scripts | ssh cicord 'sudo tar -C /opt/cicord -xf -'
ssh cicord 'sudo cp /opt/cicord/.env.example /opt/cicord/.env && sudo chmod 600 /opt/cicord/.env && sudoedit /opt/cicord/.env'
```

`.env`: the image tag, `CICORD_PUBLIC_URL`/`CICORD_PUBLIC_HOST` (public portal),
`CICORD_ADMIN_URL` (`https://cicord.<tailnet>.ts.net`), `PUBLIC_IPV4`/`PUBLIC_IPV6` (the
server's public addresses, see below). No secrets.

Image: until release images are published, build it on the server from a source archive
(native architecture, no git credentials on the server):

```sh
COPYFILE_DISABLE=1 git archive --format=tar HEAD | ssh cicord "sudo docker build --build-arg VERSION=0.1.0 --build-arg COMMIT=$(git rev-parse --short HEAD) -t cicord:$(git rev-parse --short HEAD) -"
```

Start (the `/opt/cicord` directory is root-only, so pass its paths to compose):

```sh
ssh cicord 'sudo docker compose --project-directory /opt/cicord -f /opt/cicord/compose.yaml --env-file /opt/cicord/.env up -d'
```

- Caddy is bound to the **public** addresses only (`PUBLIC_IPV4`, `PUBLIC_IPV6`): published on
  every interface, Docker's NAT rules would also capture port 443 of `tailscale0` and hide the
  admin console;
- the compose network has IPv6 enabled: with the userland proxy off, Docker can only forward
  published IPv6 ports to containers with an IPv6 address;
- Caddy obtains the certificate of the public host on its first start (logs:
  `certificate obtained successfully`).

Admin console on the tailnet (TLS certificate from Tailscale):

```sh
ssh cicord 'sudo tailscale serve --bg --https=443 http://127.0.0.1:7420'
```

Checks from the workstation (on the tailnet):

| URL | Expected |
|---|---|
| `https://community.cicord.dev/healthz` | 200, with HSTS / nosniff / referrer-policy headers |
| `https://community.cicord.dev/admin/` | 404 (never served publicly) |
| `https://cicord.<tailnet>.ts.net/admin/` | 200 |

## 7. First start: the setup wizard

With no configuration yet, the runtime starts in setup mode and prints a one-time link:

```sh
ssh cicord 'sudo docker logs cicord 2>&1 | grep "setup wizard"'
```

Open it from a device of the tailnet. On the public host, setup mode answers 404 except the
health endpoints.

## 8. Secrets and backups

The wizard stores the bot token, the Discord OAuth client secret and the GitHub App ID, key and
installation ID in the runtime's **encrypted secret store**: AES-256-GCM, inside `cicord.db`
in the `cicord_cicord-data` volume; the key is `secret.key` (mode 0600) in the same volume,
generated on the server. The browser reaches the wizard over the tailnet (WireGuard, then
`tailscale serve` HTTPS) only. `cicord secret ls` shows names, never values.

The GitHub webhook secret (the value typed in the GitHub App settings, step 5) is set from the
workstation, the value being pasted into the prompt (stdin), never on a command line:

```sh
ssh cicord 'sudo docker exec -i cicord cicord secret set CICORD_WEBHOOK_SECRET --data-dir /data'
```

What the encryption protects: a copy of the database alone (a backup, a dump) reveals no
secret. What it does not: root on the server can read the key and the database — the runtime
has to decrypt the tokens to work. The protection there is the server's exposure (only Caddy
public, SSH and admin on the tailnet).

Hardening applied:
- the data directory is `0700` (the image creates `/data` that way; an existing volume:
  `sudo chmod 0700 "$(sudo docker volume inspect -f '{{.Mountpoint}}' cicord_cicord-data)"`);
- **the key is kept apart from the backups**: once, in the password manager, copied without
  being displayed:

  ```sh
  ssh cicord 'sudo cat /var/lib/docker/volumes/cicord_cicord-data/_data/secret.key' | pbcopy
  # paste into the password manager, then: pbcopy < /dev/null
  ```

- daily backups — `scripts/05-backup.sh` installs a systemd timer (03:30 UTC) writing
  `/var/backups/cicord/cicord-<timestamp>.tar.zst` (root, 0600): an online, integrity-checked
  copy of `cicord.db` plus the embedded repository / write-back clone, **without**
  `secret.key` or `link.key`; 14 days kept. Check: `sudo systemctl list-timers cicord-backup.timer`,
  `sudo systemctl start cicord-backup.service`. Copy the archives off the server as well.

Rotation of a leaked credential (bot token, OAuth secret, App key, webhook secret, store key):
see the docs, *Self-hosting → Backups and secrets → Rotating a leaked credential*.
