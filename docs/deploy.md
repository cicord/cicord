# Self-hosting Cicord

`cicord serve` is a single static binary: the runtime (reconciler, bot gateways, plugin
host), the HTTP API, the admin UI (`/admin/`) and the default community portal (`/`).
It needs a data directory and outbound access to Discord (and to your git host). It has no
other runtime dependencies: no database server, no Node.js, no `git` binary (git is
go-git, SQLite is pure Go).

> `cicord dev` is for local development only. It lets anyone who can reach it sign in as any
> user (`/auth/dev-login`) and edits the working tree. Never expose it on a network.

## 1. What `cicord serve` needs

```
cicord serve [dir] [--data-dir DIR] [--listen ADDR] [--public-url URL] [--cors-origin ORIGIN]... [--log-level LEVEL] [--local]
```

- `dir` (default `.`) must contain `cicord.yaml`. Its `spec.source` says what to follow:
  - `provider: github` / `git` with `repo`, `branch` (default `main`), `path`,
    `pollInterval` (default `1m`): the runtime clones and polls the repository. `dir` only
    needs `cicord.yaml`.
  - `provider: embedded`: on first start, the contents of `dir` are imported into a bare
    repository in the data dir (`repo.git`), which is then the source of truth.
  - no `spec.source`, or `--local`: `dir` is watched as-is (no git). Admin UI edits are
    written to the files, so `dir` must be writable.
- `--listen` defaults to `:7420`.
- `--public-url` (env `CICORD_PUBLIC_URL`) is the URL browsers use, e.g.
  `https://cicord.example.org`. **Set it for every non-local deployment**: it is the OAuth
  redirect base, the base of portal links, the origin used by the CSRF check, and an
  `https://` value turns on `Secure` cookies. Without it the runtime assumes
  `http://localhost:<port>`, and browser `POST`/`PUT` requests from any other origin are
  rejected with `403`.
- `--cors-origin` (repeatable, env `CICORD_CORS_ORIGINS`, comma-separated): origins of
  external portals allowed to call the API with credentials.
- `--data-dir` defaults to `.cicord-data` (relative to the working directory). With a local
  source inside the repository it must be under `<repo>/.cicord/`.

### Environment

| Variable | Purpose |
|---|---|
| `DISCORD_TOKEN_*` | Bot tokens. The name is each bot's `spec.token.secretRef` (e.g. `DISCORD_TOKEN_MODERATOR`). A missing token leaves that bot `Missing`. |
| `CICORD_GIT_TOKEN` | HTTPS token for fetching a private repository, write-back pushes and GitHub pull requests (needs contents: write, and pull requests: write for `pullRequest` mode). |
| `CICORD_RUNTIME_TOKEN` | Bearer token for the CLI and CI (`cicord plan --remote`, `POST /api/v1/sync`). Acts as admin on every server. Unset: machine endpoints answer `401`. |
| `CICORD_WEBHOOK_SECRET` | Secret of the GitHub webhook (`POST /hooks/github`). Unset: webhooks are rejected. |
| `CICORD_DISCORD_CLIENT_SECRET` | OAuth2 client secret for "Login with Discord" (admin UI, portal). Unset: `/auth/login` answers `503`. |
| `CICORD_DISCORD_CLIENT_ID` | OAuth2 client ID. Default: the application ID of the first bot (by key). |
| `CICORD_LINK_SECRET` | Signing key of portal links (`ctx.portal.link()`). Default: generated once in the data dir (`link.key`). |
| `CICORD_PUBLIC_URL`, `CICORD_CORS_ORIGINS` | Defaults of `--public-url` and `--cors-origin`. |
| `CICORD_CACHE_DIR` | Plugin download cache (`<dir>/plugins`). Default: the user cache dir. The Docker image sets `/data/cache`. |
| `CICORD_SECRET_KEY` | Key of the encrypted secret store (bot tokens, OAuth and registry secrets set from the wizard or the admin). Default: generated once in the data dir (`secret.key`). |
| `CICORD_ALLOW_PRIVATE_REGISTRIES` | `true` lets the Store and plugin downloads reach registries on private networks (default: public addresses only). |

In the Discord developer portal, add `<public-url>/auth/callback` as an OAuth2 redirect
URL of the application used for login.

## 2. Data directory

| Path | Content | Back up? |
|---|---|---|
| `cicord.db` (+ `cicord.db-wal`, `cicord.db-shm`) | SQLite (WAL mode): resource ID map, plugin KV/state, sessions, audit log, sync history. | **Yes** |
| `link.key` | Portal link signing key (when `CICORD_LINK_SECRET` is not set). Losing it invalidates outstanding links. | Yes |
| `secret.key` | Key of the encrypted secret store (when `CICORD_SECRET_KEY` is not set). Losing it makes the stored secrets unreadable. | **Yes**, separately from `cicord.db` |
| `repo.git` | Bare repository of the `embedded` provider: this *is* your configuration. | **Yes** |
| `source/` | Clone/cache of the git source. | No (refetched) |
| `writeback/` | Work tree used to commit and push admin UI edits. | No |
| `snapshots/` | Snapshots of a local source. | No |
| `cache/` (Docker image) | Downloaded plugins. | No |

The resource map in `cicord.db` links YAML keys to Discord IDs. Without it, the next sync
re-adopts resources by name (see spec/README.md §3), so losing it is recoverable but noisy.

Backups: stop the runtime and copy the directory, or take a consistent online copy of the
database with `sqlite3 cicord.db ".backup cicord-backup.db"`; never copy `cicord.db` alone while
the runtime is running (the WAL file holds recent writes). Run a single runtime per data
dir and per set of bot tokens.

## 3. Docker

The root `Dockerfile` builds the UIs (Node 26, pnpm from `packageManager`), builds `cicord`
with `CGO_ENABLED=0`, and ships it on `gcr.io/distroless/static-debian13:nonroot`
(uid/gid 65532, no shell). Defaults: `cicord serve /repo --data-dir /data --listen :7420`,
volume `/data`, port `7420`.

```sh
docker build -t cicord --build-arg VERSION=0.1.0 --build-arg COMMIT=$(git rev-parse --short HEAD) .
```

`/repo` must contain at least `cicord.yaml`. For a git source, mounting only that file is
enough:

```sh
docker run -d --name cicord --restart unless-stopped \
  -p 127.0.0.1:7420:7420 \
  -v cicord-data:/data \
  -v "$PWD/cicord.yaml:/repo/cicord.yaml:ro" \
  --env-file cicord.env \
  -e CICORD_PUBLIC_URL=https://cicord.example.org \
  cicord
```

Arguments after the image name replace the default command, so repeat the defaults when
adding flags:

```sh
docker run ... cicord serve /repo --data-dir /data --listen :7420 --public-url https://cicord.example.org
```

The image `ENTRYPOINT` is `cicord`, so `docker run --rm cicord validate /repo` etc. also work.

Compose:

```yaml
services:
  cicord:
    image: cicord:latest        # or build: .
    restart: unless-stopped
    command: ["serve", "/repo", "--data-dir", "/data", "--listen", ":7420",
              "--public-url", "https://cicord.example.org"]
    ports: ["127.0.0.1:7420:7420"]
    env_file: cicord.env         # DISCORD_TOKEN_*, CICORD_* (keep it out of git)
    volumes:
      - cicord-data:/data
      - ./cicord.yaml:/repo/cicord.yaml:ro
volumes:
  cicord-data:
```

Notes:
- A named volume inherits the ownership of `/data` from the image (65532). For a bind
  mount, `chown 65532:65532` the host directory first.
- For a local source (no `spec.source`), mount the whole repository at `/repo`, writable
  if you want admin UI edits to be saved (`:ro` makes write-back fail).
- Health: `GET /healthz` (process up) and `GET /readyz` (a revision is active, `503`
  before). The image has no shell or curl: use your orchestrator's HTTP probes rather than
  a Docker `HEALTHCHECK`.
- Stop with `SIGTERM` (`docker stop`): the HTTP server and the bots shut down gracefully
  (≈ 25 s).

## 4. Reverse proxy and TLS

Run Cicord behind a TLS-terminating proxy (Caddy, nginx, Traefik) and keep port 7420
private (bind it to `127.0.0.1` or an internal network).

- `--public-url` must be exactly the scheme + host (+ port) browsers see. The CSRF check
  compares the browser's `Origin` with it, so the proxy must pass `Origin` through
  unchanged. `Host` is not used in `serve` mode.
- `Secure` cookies are set when `--public-url` starts with `https://`.
- Request bodies: JSON ≤ 1 MiB, `POST /api/v1/plan` ≤ 32 MiB (set e.g. nginx
  `client_max_body_size 32m`). API handlers time out after 60 s; a config edit may wait up
  to 20 s for the new revision, so keep proxy read timeouts above 60 s.
- Server-Sent Events (`/api/v1/sim/events`) exist only with `cicord dev --sim`. If you proxy
  a dev runtime anyway, disable buffering (the runtime sends `X-Accel-Buffering: no`).
- Webhooks: point the GitHub webhook to `<public-url>/hooks/github`, content type
  `application/json`, secret `CICORD_WEBHOOK_SECRET`, event `push`. Pushes to the tracked
  branch trigger a sync; polling (`pollInterval`) still runs as a fallback.
- External portal on another origin: add it with `--cors-origin https://portal.example.org`;
  the OAuth `redirect` may then point to that origin.

Caddy example:

```
cicord.example.org {
	reverse_proxy 127.0.0.1:7420
}
```

nginx example:

```nginx
server {
  listen 443 ssl;
  server_name cicord.example.org;
  # ssl_certificate ...;
  client_max_body_size 32m;
  location / {
    proxy_pass http://127.0.0.1:7420;
    proxy_set_header Host $host;
    proxy_read_timeout 90s;
  }
}
```

## 5. Security checklist

- [ ] Never expose `cicord dev` (dev login, simulator, working-tree edits). In dev mode the
      runtime only answers to loopback `Host` names (`localhost`, `127.0.0.1`, `::1`,
      `*.localhost`) or the `--public-url` host, and `/auth/dev-login` requires `--sim` or
      the runtime token, but it is still not meant for a network.
- [ ] Serve over HTTPS with an `https://` `--public-url` (Secure cookies, correct OAuth
      redirect, working CSRF origin check).
- [ ] Keep port 7420 private; only the proxy reaches it.
- [ ] Use a long random `CICORD_RUNTIME_TOKEN` (e.g. `openssl rand -hex 32`); it is admin
      everywhere. Store it as a CI secret only.
- [ ] Set `CICORD_WEBHOOK_SECRET` if you use webhooks (signatures are verified with
      HMAC-SHA256).
- [ ] Scope `CICORD_GIT_TOKEN` to the configuration repository (fine-grained token).
- [ ] Keep bot tokens and secrets in an env file or secret store, never in the repository.
- [ ] Only list trusted origins in `--cors-origin` (they can make credentialed calls).
- [ ] Protect the data dir (`0700`, owned by the runtime user) and back it up; it holds
      sessions and plugin data.
- [ ] Review `spec.access` (admins/moderators) of each server and use `writeBack` policies
      with `pullRequest` for structural changes.

What the runtime enforces (spec/api.md §1): every unsafe request (`POST`, `PUT`,
`PATCH`, `DELETE`) needs the `X-Cicord-Request` header or an `Authorization` header, and
an `Origin` that is the public URL or an allowed CORS origin (`/hooks/*` is exempt and
HMAC-authenticated); session cookies are HttpOnly and SameSite=Lax; portal link sessions
are limited to one server and to the member role outside the portal; plugin routes cannot
set cookies, CORS or security headers; the admin UI is served with `X-Frame-Options:
DENY`, and every response has `X-Content-Type-Options: nosniff` and
`Referrer-Policy: same-origin`.
