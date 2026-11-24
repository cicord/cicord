---
title: Write-back
description: How edits made in the admin console become git commits or pull requests, the per-path policies, and how each repository provider handles them.
---

The admin console is a convenience, not a second source of truth. Every configuration change
made in it (or through the HTTP API) is **written to git first**, then applied from that
commit. The runtime never applies a change that is not committed.

```text
admin edit ─▶ validated against the schemas ─▶ YAML patched (comments and key order kept)
          ─▶ commit on the latest branch head (author cicord[bot])
          ─▶ policy: commit       → the runtime activates its own commit immediately
                     pullRequest  → the change waits for review and merge, then syncs normally
          ─▶ conflict: re-applied on the latest head; if it no longer applies → 409
```

## Policies

`cicord.yaml` `spec.writeBack` decides, per file, whether a change is committed directly or
proposed as a pull request:

```yaml title="cicord.yaml"
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

Commits are authored by `cicord[bot]` and carry trailers that say who made the change and
from where:

```text
Cicord-Actor: discord:<user id> (<display name>)
Cicord-Source: admin-ui | api | cli
```

So `git log` and `git blame` show every change, and a bad edit is reverted like any commit.

## Providers

How write-back reaches the repository depends on `cicord.yaml` `spec.source`:

| Source | Commit mode | Pull request mode |
|---|---|---|
| `provider: github` | Pushed to the tracked branch with `CICORD_GIT_TOKEN` or the GitHub App. | Pushed to a `cicord/change-…` branch and opened as a pull request through the GitHub API. |
| `provider: git` (any HTTPS remote) | Pushed with `CICORD_GIT_TOKEN`. | Pushed to a `cicord/change-…` branch (no pull request is opened). |
| `provider: embedded` (Managed by Cicord) | Committed to the bare repository in the data directory. | Same as `git`. |
| No source, `cicord serve --local` or `cicord dev` | Written straight into the files of the directory (no commit). | Same. |

Outside GitHub, a `pullRequest` policy leaves the change on a branch that someone has to merge
by hand, so prefer `commit` policies for `git` and `embedded` sources (the setup wizard does).

The runtime authenticates with a fine-grained personal access token (`CICORD_GIT_TOKEN`) or,
preferably for organizations, a **GitHub App** (`CICORD_GITHUB_APP_ID`,
`CICORD_GITHUB_APP_PRIVATE_KEY`, optional `CICORD_GITHUB_APP_INSTALLATION_ID`): the App takes
precedence when configured, its short-lived installation tokens are renewed automatically, and
its commits and pull requests appear as `<app-name>[bot]`. Either way the repository
permissions are **Contents: Read and write**, **Pull requests: Read and write** (pull request
mode), **Commit statuses: Read and write** and **Metadata: Read**. How to create the App:
[GitHub access](../../self-hosting/deploy/#github-access-token-or-github-app).

## Commit statuses

On GitHub, the runtime reports what it did with each commit of the tracked branch, including
its own write-back commits, as a `cicord/sync` commit status: `pending` while it applies the
revision, `success` once every server is in sync, `failure` when the revision is rejected or a
server failed (the description gives the reason). A status that cannot be posted is logged and
never blocks the sync. See [Commit statuses](../../self-hosting/deploy/#commit-statuses).

The setup wizard writes policies that match the repository it creates: everything is
committed for the embedded repository and local folders; on GitHub, `bots/**` and
`plugins/**` are committed and `servers/**` opens a pull request.

## What is never written back

Configuration lives in git; **data** lives in the runtime's database. XP, warnings, tickets,
member preferences, moderation history and the audit log are never committed. See
[Repository layout](../repository-layout/#configuration-in-git-data-in-the-database).
