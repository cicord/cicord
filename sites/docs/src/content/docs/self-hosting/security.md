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
- [ ] Use a long random **`CICORD_RUNTIME_TOKEN`** (for example `openssl rand -hex 32`). It is
      admin everywhere: store it as a CI secret only.
- [ ] Set **`CICORD_WEBHOOK_SECRET`** if you use webhooks (signatures are verified with
      HMAC-SHA256).
- [ ] Scope **`CICORD_GIT_TOKEN`** to the configuration repository (a fine-grained token).
- [ ] Keep bot tokens and secrets in an env file or the encrypted secret store, **never in the
      repository**.
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
  an `Authorization` header, and an `Origin` that is the public URL or an allowed CORS origin.
  `/hooks/*` is exempt and HMAC-authenticated.
- Session cookies are HttpOnly and `SameSite=Lax` (`Secure` over HTTPS). Sessions last 7 days.
- Portal link sessions are limited to one server and to the member role outside the portal;
  token-mode sessions are limited to the portal endpoints.
- Origins declared in `cicord.yaml` (`spec.portal.allowedOrigins`) get CORS **without**
  credentials, so a merged pull request can never open cookie sessions to a new site.
- Plugin routes cannot set cookies, CORS or security headers.
- The admin console is served with `X-Frame-Options: DENY`; every response has
  `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`.
- The OAuth `state` is random, single-use and bound to the browser.

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

:::note
A security review of the plugin sandbox (escapes), YAML fuzzing and link token reuse is on the
roadmap. KV storage has per-value limits but no total quota per instance yet.
:::
