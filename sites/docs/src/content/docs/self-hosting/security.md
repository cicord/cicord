---
title: Security checklist
description: What to check before exposing a Cicord runtime, and what the runtime enforces on its own.
---

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
- [ ] Keep bot tokens and secrets in an env file or the encrypted secret store, **never in the
      repository**.
- [ ] Decide on the **bot's permissions**. Administrator is simplest, but a leaked token then
      controls the whole server. A [least-privilege bot role](../bot-permissions/) holds only
      what the configuration and plugins use. Private channels then need a `"@bot"` overwrite.
- [ ] Only list trusted origins in **`--cors-origin`**: they can make credentialed calls.
- [ ] **Protect the data directory** (`0700`, owned by the runtime user) and back it up: it
      holds sessions, plugin data, the secret store and its key.
- [ ] Review **`spec.access`** (admins and moderators) of each server, and use `writeBack`
      policies with `pullRequest` for structural changes.
- [ ] Review what each plugin is **granted** in `bot.yaml`, and treat plugins from unofficial
      registries as third-party code.

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
- With a bot that is not Administrator, every plan and sync first checks the permissions the
  changes need and that the bot keeps access to the channels it manages. If a check fails,
  nothing is applied ([bot permissions](../bot-permissions/#how-cicord-checks)).

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

:::note
A security review of the plugin sandbox (escapes), YAML fuzzing and link token reuse is on the
roadmap. KV storage has per-value limits but no total quota per instance yet.
:::
