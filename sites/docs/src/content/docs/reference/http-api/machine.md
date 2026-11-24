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
or wrong token answers `401`.

## Plan archive

- `Content-Type`: `application/gzip`, `application/x-gzip`, `application/x-tar+gzip`,
  `application/octet-stream` or none, otherwise `415 invalid`.
- `cicord.yaml` at the root of the archive or inside a single top-level directory.
- Only regular files and directories: absolute paths, `..`, backslashes, symbolic links, hard
  links and special files answer `400 invalid`.
- At most 5000 files, 16 MiB per file and 64 MiB extracted; a body over 32 MiB answers `413`.
- An invalid repository is `200` with `valid: false`, its diagnostics and `plans: []`. Planning
  errors (Discord unreachable…) answer `503`.

The [plan shape](../servers/#plan) is the same as `GET /api/v1/servers/{server}/plan`.

## GitHub webhook

- No secret configured, or a bad signature: `401`.
- `ping`: `200 {"status": "pong"}`.
- `push` on `refs/heads/<tracked branch>`: `202 {"status": "accepted"}`. The tracked branch is
  `cicord.yaml` `spec.source.branch`, default `main`.
- Other branches and events: `200 {"status": "ignored", "reason": "…"}`.
- An unparsable push payload: `400`. Payloads are limited to 25 MiB.

Setting up the webhook: [Webhooks](../../../self-hosting/deploy/#webhooks).
