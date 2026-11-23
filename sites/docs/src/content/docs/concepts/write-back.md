---
title: Write-back
description: How edits made in the admin console become git commits or pull requests, the per-path policies, and how each repository provider handles them.
---

The admin console is a convenience, not a second source of truth. Every configuration change
made in it (or through the HTTP API) is **written to git first**, then applied from that
commit. The runtime never applies a change that is not committed.

```text
admin edit ─▶ validated against the schemas ─▶ YAML patched (comments and key order kept)
          ─▶ commit on the latest branch head (author codi[bot])
          ─▶ policy: commit       → the runtime activates its own commit immediately
                     pullRequest  → the change waits for review and merge, then syncs normally
          ─▶ conflict: re-applied on the latest head; if it no longer applies → 409
```

## Policies

`codi.yaml` `spec.writeBack` decides, per file, whether a change is committed directly or
proposed as a pull request:

```yaml title="codi.yaml"
spec:
  writeBack:
    defaultMode: pullRequest
    policies:
      # Plugin settings edited in the admin console are committed directly…
      - paths: ["bots/**"]
        mode: commit
      # …while structural changes (roles, channels, permissions) need a review.
      - paths: ["servers/**"]
        mode: pullRequest
```

- Policies are evaluated **in order**; the first one whose `paths` glob matches the changed
  file decides. Otherwise `defaultMode` applies (`pullRequest` when unset).
- A change touching several files uses the **strictest** mode among them.
- `enabled: false` turns write-back off: the admin console can then no longer save
  configuration.
- `branch` is the target branch; it defaults to `spec.source.branch`.

## Commits

Commits are authored by `codi[bot]` and carry trailers that say who made the change and
from where:

```text
Codi-Actor: discord:<user id> (<display name>)
Codi-Source: admin-ui | api | cli
```

So `git log` and `git blame` show every change, and a bad edit is reverted like any commit.

## Providers

How write-back reaches the repository depends on `codi.yaml` `spec.source`:

| Source | Commit mode | Pull request mode |
|---|---|---|
| `provider: github` | Pushed to the tracked branch with `CODI_GIT_TOKEN`. | Pushed to a `codi/change-…` branch and opened as a pull request through the GitHub API. |
| `provider: git` (any HTTPS remote) | Pushed with `CODI_GIT_TOKEN`. | Pushed to a `codi/change-…` branch (no pull request is opened). |
| `provider: embedded` (Managed by CODI) | Committed to the bare repository in the data directory. | Same as `git`. |
| No source, `codi serve --local` or `codi dev` | Written straight into the files of the directory (no commit). | Same. |

Outside GitHub, a `pullRequest` policy leaves the change on a branch that someone has to merge
by hand, so prefer `commit` policies for `git` and `embedded` sources (the setup wizard does).

`CODI_GIT_TOKEN` needs `contents: write`, plus `pull requests: write` for the pull request
mode on GitHub. Authentication with a GitHub App instead of a token is planned.

The setup wizard writes policies that match the repository it creates: everything is
committed for the embedded repository and local folders; on GitHub, `bots/**` and
`plugins/**` are committed and `servers/**` opens a pull request.

## What is never written back

Configuration lives in git; **data** lives in the runtime's database. XP, warnings, tickets,
member preferences, moderation history and the audit log are never committed. See
[Repository layout](../repository-layout/#configuration-in-git-data-in-the-database).
