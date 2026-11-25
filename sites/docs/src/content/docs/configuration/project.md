---
title: Project (cicord.yaml)
description: Reference of cicord.yaml - engine, source, runtime, registries, write-back and portal settings.
---

`cicord.yaml` at the root of the repository holds the **Project**: settings that apply to the
whole repository. Schema: [`spec/schemas/project.schema.json`](https://github.com/cicord/cicord/blob/main/spec/schemas/project.schema.json).

The `cicord.yaml` of the [community server](https://github.com/cicord/cicord-community), as
the setup wizard wrote it plus a custom portal:

```yaml title="cicord.yaml"
apiVersion: cicord.dev/v1alpha1
kind: Project
metadata:
  name: cicord
spec:
  engine: ">=0.1.5 <0.2.0"
  source:
    provider: github
    repo: https://github.com/cicord/cicord-community
    branch: main
  runtime:
    url: https://community.cicord.dev
  writeBack:
    defaultMode: pullRequest
    policies:
      - paths: ["bots/**", "plugins/**"]
        mode: commit
      - paths: ["servers/**"]
        mode: pullRequest
  portal:
    path: ./portal/dist
```

## `spec`

| Field        | Type                       | Default | Description                                                                                                                                                                  |
| ------------ | -------------------------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `engine`     | semver range, **required** |         | Cicord versions this repository works with, e.g. `">=0.1.0 <0.2.0"`. The runtime refuses a revision whose range excludes its own version; `cicord validate` checks the CLI's. |
| `source`     | object                     | none    | Where the runtime reads the repository from. Without it, the served directory is used as is.                                                                                 |
| `runtime`    | object                     |         | Public URL of the runtime.                                                                                                                                                   |
| `registries` | list                       | `[]`    | Plugin registries (sources of the Store).                                                                                                                                    |
| `writeBack`  | object                     |         | How edits from the admin console are committed.                                                                                                                              |
| `portal`     | object                     |         | Custom portal and embedding.                                                                                                                                                 |

### `spec.source`

| Field          | Type                                          | Default | Description                                                                                                                                                                |
| -------------- | --------------------------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `provider`     | `github` \| `git` \| `embedded`, **required** |         | `github` and `git` clone and poll a remote repository; `embedded` imports the served directory into a bare repository in the data directory on first start, which then is the source of truth. |
| `repo`         | string                                        |         | Repository URL. Required for `github` and `git`.                                                                                                                           |
| `branch`       | string                                        | `main`  | Tracked branch.                                                                                                                                                            |
| `path`         | string                                        | `.`     | Sub-directory containing `cicord.yaml`, for monorepos.                                                                                                                     |
| `pollInterval` | duration (`^[0-9]+(s\|m\|h)$`)                | `1m`    | Polling interval. Webhooks (`/hooks/github`) and `cicord sync` trigger immediate fetches; polling is the fallback.                                                         |

Private repositories, write-back pushes, GitHub pull requests and commit statuses use
`CICORD_GIT_TOKEN` (an HTTPS token) or a GitHub App (`CICORD_GITHUB_APP_*`, see
[GitHub access](../../self-hosting/deploy/#github-access-token-or-github-app)).
`cicord serve --local` ignores `spec.source` and serves the directory as is.

### `spec.runtime`

| Field | Type | Description                                                                                                                                                                                                                                                     |
| ----- | ---- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `url` | URI  | Public URL of the runtime. `cicord serve` uses it when `--public-url` (`CICORD_PUBLIC_URL`) is not set. The CLI never sends the runtime token to it, since a pull request can change this file: `cicord plan --remote`, `cicord status` and `cicord sync` need `--remote=URL` or `CICORD_RUNTIME_URL`. |

### `spec.registries[]`

| Field       | Type                                    | Description                                                                                                                                                               |
| ----------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`      | key, **required**                       | Registry name, used in plugin references (`<name>/<plugin>`). Unique; `cicord` is reserved for the official plugins.                                                       |
| `url`       | string, **required**                    | Git repository with an `index.yaml`, unique among registries. The admin console accepts `https://` only; the CLI also accepts `file://` URLs and local paths, for development registries. |
| `secretRef` | `CICORD_REGISTRY_<NAME>_TOKEN`          | For a private registry: the secret holding its access token, named after this registry (`-` becomes `_`), so an edit can never send another secret to a registry host.      |

```yaml
registries:
  - name: acme
    url: https://github.com/acme/cicord-plugins.git
    secretRef: CICORD_REGISTRY_ACME_TOKEN # private registry only
```

Registries are **unofficial**: trust is computed from the URL, never declared. Manage them
with `cicord registry add|remove` or from the Store. See [Publishing](../../plugins/publishing/#private-registries).

### `spec.writeBack`

| Field              | Type                                    | Default         | Description                                                                     |
| ------------------ | --------------------------------------- | --------------- | ------------------------------------------------------------------------------- |
| `enabled`          | boolean                                 | `true`          | Allow the admin console and the API to write configuration.                     |
| `branch`           | string                                  | `source.branch` | Target branch.                                                                  |
| `defaultMode`      | `commit` \| `pullRequest`               | `pullRequest`   | Mode when no policy matches.                                                    |
| `policies[]`       | list                                    |                 | Evaluated in order; the first policy whose `paths` match the changed file wins. |
| `policies[].paths` | list of globs, **required** (≥ 1)       |                 | E.g. `["bots/**"]`.                                                             |
| `policies[].mode`  | `commit` \| `pullRequest`, **required** |                 |                                                                                 |

A change touching several files uses the strictest mode. See [Write-back](../../concepts/write-back/).

### `spec.portal`

| Field            | Type                   | Description                                                                                                                                                                                                       |
| ---------------- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `path`           | relative path          | Build output (`index.html` + assets) of a custom portal committed in the repository, e.g. `./portal/dist`. The runtime serves it at `/` instead of the default portal.                                           |
| `url`            | URI                    | External portal URL, when the portal is hosted elsewhere: the base of the links made by `ctx.portal.link()`.                                                                                                    |
| `allowedOrigins` | list of origins (≤ 20) | Origins (`https://site.example`) of third-party sites that embed the portal SDK in **token mode** (CORS without credentials, login redirects, bearer sessions). Cookie sessions across origins need the operator's `--cors-origin` instead. |

The theme, texts and pages of the portal are per server: see [Server → portal](../server/#portal).
Custom portals: [Custom portal](../../portal/custom-portal/), [Embedding](../../portal/embedding/).

## Validation

`cicord validate` (offline, `-o json` for CI) checks this file against the schema, then the
engine range, the registries (reserved or repeated names, repeated URLs, a `secretRef` that is
not the registry's own) and `portal.path` (a directory inside the repository, without symbolic
links; a missing build or `index.html` is a warning, and the default portal is served until
it exists). The same checks run in the runtime before a revision is activated and before any
admin console edit is written.
