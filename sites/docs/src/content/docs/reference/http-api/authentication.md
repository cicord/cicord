---
title: "HTTP API: authentication"
description: How clients authenticate to the runtime API - session cookies from Discord OAuth2 and portal links, session tokens for embedded portals, the runtime bearer token, CSRF, the admin host and per-server roles.
---

Part of the [HTTP API reference](../). For the concepts, see
[Access and roles](../../../concepts/access-and-roles/).

## Clients

| Client | How |
|---|---|
| Browser, same origin | The `cicord_session` cookie (HttpOnly, `SameSite=Lax`, path `/`, `Secure` when the request is TLS or `--public-url` is `https://`), from Discord OAuth2 (`/auth/login`) or a portal link (`/auth/link`). |
| Portal on another origin | A **session token**, `Authorization: Bearer cicord_st_…` ([token mode](#token-mode-embedded-portals)). |
| CLI, CI | `Authorization: Bearer <CICORD_RUNTIME_TOKEN>`, compared in constant time. A runtime token acts as `admin` on every server. |

Any other `Authorization` value, or an unknown or expired session token, answers
`401 unauthenticated`, even when a valid cookie is present. An unknown or expired cookie is
simply anonymous.

## Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/auth/login?redirect=<path>[&mode=token]` | Redirects (`302`) to Discord OAuth2 (scope `identify`, `prompt=none`). |
| GET | `/auth/callback` | OAuth2 callback: creates the session, redirects (`302`) to `redirect`. |
| POST | `/auth/link` | Body `{"token": "<signed link token>"}`. Exchanges a token produced by `ctx.portal.link()` (query parameter `cicord_token` on the portal URL) for a session. Returns `Me`. |
| POST | `/auth/dev-login` | **Dev mode only.** Body `{"userId": "<id>"}`. Creates a session for that user. Returns `Me`. |
| POST | `/auth/token` | Token mode. Body `{"code": "<one-time code>"}`. Redeems the `cicord_code` of a token-mode login for a `TokenSession`. |
| POST | `/auth/logout` | Deletes the session and clears the cookie; with `Authorization: Bearer cicord_st_…`, revokes that token instead. `204`. |

### Discord OAuth2

- `redirect` must be a local path (`/x`, not `//x` nor `/\x`) or an absolute URL on an allowed
  CORS origin; anything else falls back to `/`.
- The redirect URI sent to Discord is `<public URL>/auth/callback`, or
  `<admin URL>/auth/callback` for a request on the [admin host](#admin-host---admin-url). It
  must be registered in the Discord application.
- `cicord serve` always has a public URL: `--public-url`, otherwise `spec.runtime.url`, the
  URL the setup wizard ran on, then `http://localhost:<port>`. Only an API handler embedded
  without a public URL derives it from the request's scheme and `Host`.
- The login, the callback, the code exchange and the cookies stay on the host the login
  started from.
- The `state` parameter is random, single-use (10 minutes, stored in the runtime database) and
  bound to the browser by the `cicord_oauth_state` cookie (HttpOnly, Lax, path `/auth/`). A
  missing or mismatching cookie, or an expired state, answers `400 invalid`. A Discord error or
  a failed code exchange answers `401 unauthenticated`.
- The OAuth2 client ID is `CICORD_DISCORD_CLIENT_ID`, by default the application ID of the
  first bot (by key). Without a client ID or `CICORD_DISCORD_CLIENT_SECRET`, `/auth/login`
  answers `503 unavailable`.

### Sessions

- Sessions last 7 days. Session IDs are random (256 bits) and stored in the runtime database.
  A new login replaces the browser's previous session.
- **Link sessions** (`/auth/link`) last until the link token's expiry, at least 1 hour and at
  most 24 hours (`ctx.portal.link()` tokens last 15 minutes by default). They are **scoped to
  the token's server**: the caller has no role on any other server, and on the token's server
  it is at most `member` outside the [portal endpoints](../portal/), so a forwarded DM link
  never grants admin console access.
  - `401 unauthenticated` for an invalid or expired token;
  - `400 invalid` for an anonymous link (`ctx.portal.link(path)` without a member), which
    cannot sign anyone in;
  - `404` when the token's server is no longer in the repository; `503` before the first
    revision.
  - Link tokens are not single-use.
- **Dev login** answers `404` outside dev mode (`cicord dev`). Impersonating arbitrary users is
  only acceptable against the simulator, so it also requires **`--sim` or the runtime token**
  (`Authorization: Bearer …`), otherwise `403 forbidden`. With the simulator the user must
  exist in the simulated world (`404` otherwise); without it the profile is looked up through
  a connected bot. `userId` is required (`400`).

## Token mode (embedded portals)

A site on another origin (a fan site with its own design) cannot use the `SameSite=Lax`
cookie. It authenticates with a bearer **session token** instead; `@cicord/client` does it when
created with `auth: "token"`. See [Embedding](../../../portal/embedding/).

- **Allowed origins**: the embedding site's origin must be in `cicord.yaml`
  `spec.portal.allowedOrigins` (token mode only) or `--cors-origin`.
- **OAuth**: `/auth/login?mode=token&redirect=<URL>`, where `redirect` is a local path or an
  absolute URL on an allowed origin (otherwise `400 invalid`: unlike cookie mode, there is no
  silent fallback to `/`). After Discord, `/auth/callback` sets no session cookie and
  redirects to `redirect` with `cicord_code=<code>` added to its query. The code is
  single-use, valid 60 seconds, and bound to the redirect's origin: `POST /auth/token {"code"}`
  must come from that origin (`Origin` header; a local redirect binds it to the runtime's own
  origin), otherwise `403 forbidden` (and the code is burnt). An unknown or expired code
  answers `401`.
- **Portal links** and **dev login**: `POST /auth/link {"token", "mode": "token"}` and
  `POST /auth/dev-login {"userId", "mode": "token"}` return a `TokenSession` instead of
  setting a cookie. `mode` is `"cookie"` (default) or `"token"` (anything else: `400`).
- `TokenSession = {"token": "cicord_st_…", "expiresAt": "<RFC 3339>", "me": Me}`. Tokens last
  24 hours (link tokens: as long as the link session, at most 24 hours) and are stored by the
  runtime like sessions (revoked by `/auth/logout`).

  ```sh
  curl -X POST http://127.0.0.1:7420/auth/dev-login -H 'X-Cicord-Request: 1' \
    -d '{"userId": "1575125353398009859", "mode": "token"}'
  ```

  ```json
  { "token": "cicord_st_…", "expiresAt": "2026-11-26T12:35:26.373234Z",
    "me": { "user": { "id": "1575125353398009859", "username": "alice", "displayName": "alice", "avatarUrl": "" },
            "servers": [ { "key": "showcase", "name": "Cicord Showcase", "role": "member" } ] } }
  ```
- **Scope**: on the portal endpoints (plugin routes included) a token session has the user's
  full role on the server; everywhere else it is **at most `member`**, like link sessions, so a
  token kept by a third-party site never reaches the admin endpoints. A token is only accepted
  in the `Authorization` header, never as the `cicord_session` cookie; a cookie session ID is
  never accepted as a bearer token.
- Requests carrying `Authorization` need no `X-Cicord-Request` header (CSRF, below), but the
  `Origin` check still applies: unsafe requests must come from an allowed origin.

## CSRF

Every request with an unsafe method (anything but `GET`, `HEAD`, `OPTIONS`), the `/auth/*`
endpoints included, must carry the header **`X-Cicord-Request: 1`** (any non-empty value)
unless it has an `Authorization` header (browsers never add one on their own).
`@cicord/client` always sends it. Otherwise:

```json
{"error": {"code": "forbidden", "message": "missing X-Cicord-Request header (CSRF protection)"}}
```

In addition, an unsafe request carrying an `Origin` header must come from the runtime's own
origin or an allowed CORS origin, otherwise `403 forbidden`. The runtime's own origin is the
origin of `--public-url` when it is set (the `Host` header is then never trusted); otherwise
`Origin` must match the request's `Host`. The origin of `--admin-url` is accepted too.

A cross-site form cannot set custom headers, and cross-origin scripts need a CORS preflight,
which only `--cors-origin` origins pass. `/hooks/*` is exempt (HMAC-authenticated).

## Admin host (`--admin-url`)

`cicord serve --admin-url https://host[:port]` (env `CICORD_ADMIN_URL`; `http` only for a
loopback host; no path) serves the admin console on a second hostname, typically reachable
only through a VPN, while the portal stays on `--public-url`. Both reach the same listener.
See [Admin on a private network](../../../self-hosting/security/#admin-on-a-private-network-portal-public).

- **Host matching**: a request is on the admin host when its `Host` header, compared
  case-insensitively without a trailing dot, has the admin URL's host and port; a `Host`
  without a port stands for the default port of the admin URL's scheme (443 for https).
  `X-Forwarded-Host` is never read: reverse proxies must pass the original `Host`.
- On the admin host, the admin URL replaces the public URL for the OAuth redirect URI and the
  `Secure` cookie flag. Cookies have no `Domain`: a session on one host is not sent to the
  other.
- **Admin routes answer `404 not_found` on every other host**, exactly like an unknown route:
  `/admin`, `/admin/*` and every `/api/*` path except `/api/v1/me`, `/api/v1/portal/**`,
  `/api/v1/sync`, `/api/v1/plan` and `/api/v1/sim/**`. The status, history, servers, catalog,
  Store, registries and setup endpoints are thus admin-only, and so is any endpoint added
  later. The portal (`/` and its SPA paths), `/auth/*`, `/api/v1/me`, `/hooks/*`, `/healthz`
  and `/readyz` answer on every host.
- The route is classified from both the cleaned request path and the pattern it matches, so
  encoded or dotted paths cannot reach an admin handler. An admin URL that does not parse
  makes the admin routes unreachable on every host (fail closed).
- In setup mode, only `/healthz` and `/readyz` answer outside the admin host.
- `Host` is chosen by the client: this restriction is defense in depth. The network (the admin
  hostname only resolving or routing inside the VPN, a listener bound to loopback) is the
  actual boundary.

Without `--admin-url`, nothing changes: every route answers on every host.

## Roles

For each server, a user is `admin` (the server owner, or `spec.access.admins`), `moderator`
(`spec.access.moderators`), `member` (a member of the Discord server) or nothing. Bearer-token
clients are `admin` everywhere.

The user's live member roles are fetched from Discord (through the server's reconciling bot,
cached 30 seconds; no connected bot: `503`) and matched with
`spec.access.<admins|moderators>.roles` (keys resolved through the resource map) and `.users`
(Discord user IDs). Admin wins over moderator. A user who is not in the server has no role.
Link sessions and session tokens are restricted as described above.

Endpoints that need a role on a server answer:

- `503` before the first revision;
- `401` without a session;
- `404` for an unknown server key;
- `403` when the role is insufficient (`"requires the <role> role on server <key>"`).
