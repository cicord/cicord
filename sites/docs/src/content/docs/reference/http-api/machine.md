---
title: "HTTP API: machine endpoints"
description: The endpoints used by the CLI and CI - immediate sync, remote plans of a repository archive, the GitHub webhook - and the health checks.
---

Part of the [HTTP API reference](../). These endpoints are used by `cicord sync`,
`cicord plan --remote` and webhooks; see the [CLI reference](../../cli/).

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/api/v1/sync` | bearer | Fetch the source now and sync (used by `cicord sync`, CI, webhooks without GitHub). `202 {"status": "accepted"}`. |
| POST | `/api/v1/plan` | bearer | Body: a `.tar.gz` of a repository. Validates it and returns `{"valid": bool, "diagnostics": [Diagnostic], "plans": [Plan]}` against live Discord, without applying (`cicord plan --remote`). |
| POST | `/hooks/github` | HMAC | GitHub webhook (`X-Hub-Signature-256` verified with `CICORD_WEBHOOK_SECRET`). A `push` on the tracked branch triggers a sync. |
| GET | `/healthz` | none | `200 ok` (text) when the process is up. |
| GET | `/readyz` | none | `200 ready` once a revision is active, `503 no active revision` otherwise (text; `503 setup mode` in setup mode). |

`/metrics` is not part of the API: it answers `404` on every host. The Prometheus metrics are
served on a separate listener, `cicord serve --metrics-listen` (see
[Monitoring](../../../self-hosting/monitoring/)).

## Bearer authentication

The bearer endpoints need the runtime token, `Authorization: Bearer <CICORD_RUNTIME_TOKEN>`.
Without `CICORD_RUNTIME_TOKEN` they answer `401` ("machine endpoints are disabled"); a missing
or wrong token answers `401` ("a valid runtime bearer token is required").

## Plan archive

- `Content-Type`: `application/gzip`, `application/x-gzip`, `application/x-tar+gzip`,
  `application/octet-stream` or none, otherwise `415 invalid`.
- `cicord.yaml` at the root of the archive or inside a single top-level directory.
- Only regular files and directories: absolute paths, `..`, backslashes, symbolic links, hard
  links and special files answer `400 invalid`.
- macOS metadata entries (AppleDouble `._*` files and `.DS_Store`) are ignored.
- At most 5000 files, 16 MiB per file and 64 MiB extracted; a body over 32 MiB answers `413`.
- An invalid repository is `200` with `valid: false`, its diagnostics and `plans: []`. Planning
  errors (Discord unreachable…) answer `503`.

The [plan shape](../servers/#plan) is the same as `GET /api/v1/servers/{server}/plan`, without
the explained `error` on `blocked[]` entries.

`cicord plan --remote -o json` uploads the repository with the right exclusions (`.git`,
`node_modules`, `.cicord`, `dist`, `.env` files, macOS metadata, the root `.gitignore` patterns,
but always the portal build at `spec.portal.path`; see the
[CLI reference](../../cli/#cicord-plan)) and prints the same `{valid, diagnostics, plans}` body. Prefer it to a
hand-made archive, which can carry secrets such as `.env` files:

```sh
CICORD_RUNTIME_TOKEN=… cicord plan --remote=http://127.0.0.1:7420 -o json
```

```json
{ "valid": true,
  "diagnostics": [ { "severity": "warning", "file": "servers/showcase.yaml", "line": 24, "path": "/spec/syncPolicy",
                     "message": "prune is enabled without deletion protection: resources removed from this file will be deleted" } ],
  "plans": [ { "server": "showcase",
               "changes": [ { "action": "update", "kind": "role", "key": "mod", "id": "1575125354564026369",
                              "summary": "update role \"Moderator\": color",
                              "fields": { "color": { "from": "#e67e22", "to": "#f1c40f" } } }, … ],
               "blocked": [ { "action": "update", "kind": "server", "key": "showcase", "id": "223344556677889900",
                              "reason": "boost: banner requires boost level 2; server is level 0 (0 boost(s))",
                              "fields": { "banner": { "from": null, "to": "./assets/banner.png" } } } ] } ] }
```

## GitHub webhook

- No secret configured, or a bad signature: `401`.
- `ping`: `200 {"status": "pong"}`.
- `push` on `refs/heads/<tracked branch>`: `202 {"status": "accepted"}`. The tracked branch is
  `cicord.yaml` `spec.source.branch`, default `main`.
- Other branches and events: `200 {"status": "ignored", "reason": "…"}`.
- An unparsable push payload: `400`. Payloads are limited to 25 MiB.

Setting up the webhook: [Webhooks](../../../self-hosting/deploy/#webhooks).
