---
title: Security checklist
description: What to check before exposing a Cicord runtime, and what the runtime enforces on its own.
---

For a complete install that applies this page (firewall, SSH and the admin console on a
Tailscale tailnet, Caddy for the public portal, backups), follow
[Production on a VPS](../production-vps/).

## Checklist

- [ ] **Never expose `cicord dev`** (dev login, simulator, working-tree edits). In dev mode the
      runtime only answers loopback host names (`localhost`, `127.0.0.1`, `::1`,
      `*.localhost`) or the `--public-url` host, and `/auth/dev-login` requires `--sim` or the
      runtime token, but it is still not meant for a network.
- [ ] **Serve over HTTPS** with an `https://` `--public-url`: `Secure` cookies, a correct OAuth
      redirect and a working CSRF origin check depend on it.
- [ ] **Keep port 7420 private**; only the reverse proxy reaches it.
- [ ] To keep the admin console off the internet, serve it on a
      [private hostname](#admin-on-a-private-network-portal-public) with `--admin-url`.
- [ ] Use a long random **`CICORD_RUNTIME_TOKEN`** (for example `openssl rand -hex 32`). It is
      admin everywhere: store it as a CI secret only.
- [ ] Set **`CICORD_WEBHOOK_SECRET`** if you use webhooks (signatures are verified with
      HMAC-SHA256).
- [ ] Scope **`CICORD_GIT_TOKEN`** to the configuration repository (a fine-grained token), or
      use a **GitHub App** installed on that repository only, and keep its private key out of
      the repository.
- [ ] Keep bot tokens and secrets in the encrypted secret store (setup wizard,
      `cicord secret set` on stdin) or in an env file readable by the runtime only, **never in
      the repository**.
- [ ] Keep a copy of the secret store's key (`secret.key` or `CICORD_SECRET_KEY`) in a
      password manager, **apart from the backups** of the database
      ([Backups and secrets](../backups-and-secrets/#what-to-back-up)).
- [ ] Only list trusted origins in **`--cors-origin`**: they can make credentialed calls.
- [ ] **Protect the data directory** (`0700`, owned by the runtime user; the container image
      creates `/data` that way) and back it up: it holds sessions, plugin data, the secret
      store and its key.
- [ ] Review **`spec.access`** (admins and moderators) of each server, and use `writeBack`
      policies with `pullRequest` for structural changes.
- [ ] Review what each plugin is **granted** in `bot.yaml`, and treat plugins from unofficial
      registries as third-party code.
- [ ] Harden the **Discord side** too: a private bot, 2FA for moderation, least-privilege
      roles ([below](#discord-server-hardening)).

## What the runtime enforces

**HTTP API**

- Every unsafe request (`POST`, `PUT`, `PATCH`, `DELETE`) needs the `X-Cicord-Request` header or
  an `Authorization` header, and an `Origin` that is the public URL, the admin URL or an allowed
  CORS origin. `/hooks/*` is exempt and HMAC-authenticated.
- Session cookies are HttpOnly and `SameSite=Lax` (`Secure` over HTTPS). Sessions last 7 days.
- Portal link sessions are limited to one server and to the member role outside the portal
  endpoints; token-mode sessions (embedded portals) work on every endpoint but are capped at
  the member role outside the portal endpoints, so they never get admin or moderator rights.
- Origins declared in `cicord.yaml` (`spec.portal.allowedOrigins`) get CORS **without**
  credentials, so a merged pull request can never open cookie sessions to a new site.
- Plugin routes cannot set cookies, CORS or security headers.
- The admin console is served with `X-Frame-Options: DENY`; every response has
  `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`.
- The OAuth `state` is random, single-use and bound to the browser.

- With `--admin-url`, the admin console and the admin API answer `404` on every host but the
  admin one ([below](#admin-on-a-private-network-portal-public)).

**Plugins**

- Each plugin instance runs in its own VM (QuickJS-ng compiled to WebAssembly) with no file
  system, network or Node.js API; every host call is checked against the capabilities
  granted in `bot.yaml`.
- An instance only reaches its own server's channels, and storage is scoped to the instance.
- `http.fetch` only reaches the manifest's allow-listed public hosts over `https`; resolved
  addresses are checked (no loopback, private, link-local or metadata addresses), the
  connection goes to the checked address and every redirect is re-checked.
- CPU and wall-clock budgets stop runaway handlers; code that ignores the CPU budget (a long
  native call) is killed shortly after it.
- Each instance has a hard memory limit (64 MiB of JavaScript heap, in its own WebAssembly
  memory). Reaching it, or being killed, discards that instance only: it is reported
  `Degraded` and restarted with backoff, the process and the other plugins keep running.
- Host-side quotas bound what an instance can push into the runtime: pending SDK calls,
  queued handler calls, SDK call arguments, log fields, KV pages and route responses.
- A plugin update that asks for a new capability fails validation until it is granted, and
  `cicord.lock` pins registry plugins by commit and content hash.
- Before installation, the Store only reads an unofficial plugin's `plugin.yaml`; its code
  runs only once you install it.

**Secrets**

- Secrets never appear in YAML, only their names (`secretRef`).
- The secret store is encrypted with AES-256-GCM; the API never returns a value.
- A private registry's token is only sent to that registry's host.

**Discord**

- Only resources declared in YAML are ever modified; deletions need `prune: true` and are
  blocked by `deletionProtection` unless it is turned off.

## Admin on a private network, portal public

The community portal has to be public, the admin console does not. `cicord serve` can answer
on two hostnames from the same listener: the **public URL** for the portal and the **admin
URL** for the admin console, for example a hostname that only exists inside a VPN such as a
[Tailscale](https://tailscale.com/) tailnet.

```text
internet ── Caddy (https://community.example.org) ──────────┐
                                                            ├─► 127.0.0.1:7420  cicord serve
tailnet  ── tailscale serve (https://cicord.example.ts.net) ┘
```

```sh
# Binary on the host. In Docker, keep --listen :7420 and publish 127.0.0.1:7420:7420.
cicord serve /srv/cicord --data-dir /var/lib/cicord --listen 127.0.0.1:7420 \
  --public-url https://community.example.org \
  --admin-url https://cicord.example.ts.net      # or CICORD_ADMIN_URL
tailscale serve --bg 7420                        # HTTPS on the tailnet name → 127.0.0.1:7420
```

```text title="Caddyfile"
community.example.org {
	reverse_proxy 127.0.0.1:7420 {
		header_up Host community.example.org
	}
}
```

What changes with `--admin-url`:

- **Two redirect URIs.** In the Discord developer portal, add both `https://community.example.org/auth/callback`
  and `https://cicord.example.ts.net/auth/callback` as OAuth2 redirects of the login
  application. The setup wizard checks both and names the missing ones.
- **Logins stay on their host.** A login started on the admin hostname uses the admin
  callback, one started anywhere else the public callback. Cookies have no `Domain`, so a
  portal session is never sent to the admin hostname and the reverse: sign in on each.
- **Admin routes only answer on the admin host.** On any other `Host`, the admin console
  (`/admin`, `/admin/*`) and every API endpoint except `/api/v1/me`, `/api/v1/portal/**`,
  `/api/v1/sync` and `/api/v1/plan` answer `404`, like an unknown route: status, history,
  servers, plugin configuration, catalog, Store, registries and the setup wizard. The
  portal, `/auth/*`, `/api/v1/me`, `/hooks/*`, `/healthz` and `/readyz` answer on both. In
  setup mode, the setup link is printed with the admin URL and the public hostname only
  answers the health endpoints.
- **Machine clients.** GitHub webhooks, `cicord sync` (without `--wait`) and
  `cicord plan --remote` work through the public URL. `cicord status` and `--wait` read
  `/api/v1/status` and `/api/v1/history`, admin endpoints: run them against the admin URL
  (from a CI runner on the VPN).
- The startup banner and the GitHub commit status link point at the admin URL; links sent
  by the bots (`ctx.portal.link()`) keep the public URL.

Requirements:

- **Both proxies must pass the original `Host`** (the runtime never reads
  `X-Forwarded-Host`). Caddy does by default; pinning `header_up Host` to the public name, as
  above, also stops a forged `Host` from reaching the admin routes through the public proxy.
  If the admin hostname answers `404` on `/admin/`, your proxy rewrites `Host`.
- **Bind the listener to loopback** (`--listen 127.0.0.1:7420`; the default `:7420` listens on
  every interface). In Docker, publish `127.0.0.1:7420:7420` only.
- The `Host` check is **defense in depth**: the header is chosen by the client. The actual
  boundary is the network: the admin hostname only resolves and routes inside the VPN, and
  the listener is not reachable from the internet.

## Discord server hardening

Cicord secures what it runs; the Discord server and application need a few settings of their
own. They are made in Discord, not in YAML, except where noted.

**The bot's permissions.** The setup wizard invites the bot with the **Administrator**
permission (`permissions=8`), the configuration Cicord is tested with: the reconciler manages
roles, channels, permission overwrites and server settings, and the bot must see every channel
it manages. What it implies:

- the bot bypasses every channel overwrite and can do anything on the server except what only
  the owner can (deleting the server, transferring it). Plugins are still limited by their
  grants, but **a leaked bot token is full control of the server**: keep it in the secret
  store, and [rotate it](../backups-and-secrets/#rotating-a-leaked-credential) at once if it
  may have leaked;
- its role must stay **above every role Cicord manages** (the wizard and the Diagnostics page
  check it). Members whose highest role is below the bot's cannot edit the bot's role.

The wizard's permission check also accepts a role without Administrator that has View
Channels, Manage Roles, Manage Channels, Manage Server, Send Messages, Embed Links, Read Message
History, Add Reactions, Manage Messages and Moderate Members. Some plugins need more (the
moderation plugin's `/kick` and `/ban` need Kick Members and Ban Members), and channels the bot
cannot see cannot be managed. Running without Administrator is possible, but it is not the
tested default.

**A private bot.** In the [Developer Portal](https://discord.com/developers/applications), so
that nobody else can add your bot to a server: **Installation → Install Link: None** first,
then **Bot → Public Bot** off (Discord refuses a private bot that still has an install link).
As its owner, you can still invite it with the wizard's link.

**Privileged intents.** Turn on only the ones your plugins declare (Server Members, Message
Content); Cicord never uses the Presence intent.

**2FA for moderation.** In Server Settings → **Safety Setup**, turn on the requirement of
two-factor authentication for moderation actions, so that a stolen password alone cannot ban
members or delete channels. Discord applies it to bots through their owner: the account that
owns the application (or the team owner) must have 2FA enabled, or the bot's moderation calls
fail. Cicord does not manage this setting.

**Least privilege for roles.** Give human roles only the permissions they use:

- no Administrator on staff roles; keep Manage Roles, Manage Server, Manage Webhooks and Ban
  Members to the few people who need them;
- in Cicord, `spec.access` decides who is a Cicord admin or moderator
  ([Access and roles](../../concepts/access-and-roles/)): map it to small roles, and protect
  `servers/**` with a `pullRequest` [write-back policy](../../concepts/write-back/) so that
  permission changes are reviewed;
- never offer a role that carries permissions in a self-assignable panel (reaction-roles,
  onboarding);
- `profile.verificationLevel` and `profile.explicitContentFilter` are declared in YAML
  ([Server](../../configuration/server/#profile)); AutoMod rules too.

:::note
A security review of the plugin sandbox (escapes), YAML fuzzing and link token reuse is on the
roadmap. KV storage has per-value limits but no total quota per instance yet.
:::
