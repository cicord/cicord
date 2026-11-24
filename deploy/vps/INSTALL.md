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
