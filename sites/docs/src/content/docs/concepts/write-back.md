---
title: Write-back
description: How edits made in the admin console become git commits or pull requests, the per-path policies, and how each repository provider handles them.
---

The admin console is a convenience, not a second source of truth. Every configuration change
made in it, or through the HTTP API, is **written to git first**, then applied from that
commit. The runtime never applies a change that is not committed.

What the admin console writes:

| Change                                   | Files                                                                                   |
| ---------------------------------------- | --------------------------------------------------------------------------------------- |
| Plugin settings (base config or a server override) | `bots/<bot>/bot.yaml`                                                         |
| Store install, update, uninstall         | `bots/<bot>/bot.yaml`, and `plugins/<name>/` (bundled plugins) or `cicord.lock` (registry plugins); with a custom portal that has typed routes, also its `src/cicord-routes.d.ts` |
| Registries added or removed in the Store | `cicord.yaml`                                                                           |
| Server icon, banner and splash uploads   | `servers/<key>.yaml` and `servers/assets/<key>-<kind>.<ext>`                            |

```text
admin edit ─▶ validated ─▶ YAML patched (comments and key order kept)
          ─▶ applied on the latest branch head, committed as cicord[bot]
          ─▶ commit       → pushed; the runtime activates it right away
             pullRequest  → pushed to a branch; it syncs once merged
          ─▶ no longer applies, or the branch moved during the push → 409, try again
```

## Policies

`cicord.yaml` `spec.writeBack` decides, per file, whether a change is committed directly or
proposed as a pull request. This is what the setup wizard writes for a GitHub repository (the
[community server](https://github.com/cicord/cicord-community) uses it as is):

```yaml title="cicord.yaml"
spec:
  writeBack:
    defaultMode: pullRequest
    policies:
      - paths: ["bots/**", "plugins/**"]
        mode: commit
      - paths: ["servers/**"]
        mode: pullRequest
```

- Policies are evaluated **in order**; the first one with a `paths` glob matching the changed
  file decides (`**` matches across directories, `*` and `?` within one). Otherwise
  `defaultMode` applies (`pullRequest` when unset).
- A change touching several files is **one** commit or **one** pull request, in the
  **strictest** mode among them. With the policies above, installing a registry plugin also
  changes `cicord.lock`, which no policy matches, so it opens a pull request; so does any Store
  action on a repository whose custom portal has typed routes (`portal/src/cicord-routes.d.ts`).
- `enabled: false` turns write-back off: the admin console then refuses to save
  configuration (`403`).
- `branch` is the target branch; it defaults to `spec.source.branch`.

For the embedded repository, a plain git remote and local folders, the setup wizard writes
`defaultMode: commit` instead. Field reference: [Project → `spec.writeBack`](../../configuration/project/#specwriteback).

## Commits

Commits are authored by `cicord[bot]` and carry trailers that say who made the change and
from where:

```text
config(cicord/welcome): update base config

Changed: message

Cicord-Actor: discord:<user id> (<display name>)
Cicord-Source: admin-ui
```

`Cicord-Actor` is `runtime-token` for a call made with the runtime token, and
`Cicord-Source` is `setup` for the setup wizard's commits. `git log` and `git blame` show
every change, and a bad edit is reverted like any commit.

A Store action also says in its message what it regenerated (the portal's route types,
`Regenerated portal/src/cicord-routes.d.ts (cicord portal typegen).`, with the routes it
removes) or could not regenerate (`Warning: …`, also returned in the API response's `warnings`
and shown in the admin console): the rest of the message (everything after the first line)
is also the body of the pull request.

### What a Store action keeps up to date

A plugin install, update or uninstall made in the Store changes `cicord.lock` or
`plugins/<name>/` and `bot.yaml`. Generated files that depend on them are regenerated in the
same commit or pull request, so the repository's CI passes without a manual fix:

- **The custom portal's route types** (`<portal>/src/cicord-routes.d.ts`, when the portal has
  it): they name every plugin with routes and its version. They are written with the code of
  `cicord portal typegen`, from the new revision's plugins, and match what
  `cicord portal typegen --check` expects byte for byte, whatever cicord version CI pins (the
  file never names a cicord version). See [Custom portal → Typed plugin
  routes](../../portal/custom-portal/#kept-up-to-date-by-the-admins-store).
- Nothing else needs it: `cicord portal vendor --check` depends on the cicord release only,
  the committed portal build does not change with types, and `cicord validate` runs on the new
  revision before anything is written. Plugin settings changes never change routes.

## Providers

How write-back reaches the repository depends on `cicord.yaml` `spec.source`:

| Source                                            | Commit mode                                                      | Pull request mode                                                                         |
| ------------------------------------------------- | ---------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `provider: github`                                | Pushed to the tracked branch.                                     | Pushed to a `cicord/change-…` branch and opened as a pull request through the GitHub API. |
| `provider: git` (any HTTPS remote)                | Pushed to the tracked branch.                                     | Pushed to a `cicord/change-…` branch; no pull request is opened.                          |
| `provider: embedded` (Managed by Cicord)          | Committed to the bare repository in the data directory.           | Same as `git`.                                                                            |
| No source, `cicord serve --local` or `cicord dev` | Written straight into the files of the directory (no commit).     | Same: policies do not apply.                                                               |

Outside GitHub, a `pullRequest` policy leaves the change on a branch that someone has to merge
by hand, so prefer `commit` policies for `git` and `embedded` sources.

The runtime pushes with a fine-grained personal access token (`CICORD_GIT_TOKEN`) or,
preferably for organizations, a **GitHub App** (`CICORD_GITHUB_APP_ID`,
`CICORD_GITHUB_APP_PRIVATE_KEY`, optional `CICORD_GITHUB_APP_INSTALLATION_ID`). The App takes
precedence when configured, its short-lived installation tokens are renewed automatically, and
its pull requests are opened by `<app-name>[bot]` (commits keep the author `cicord[bot]`).
Either way the repository permissions are **Contents: Read and write**, **Pull requests: Read
and write** (pull request mode), **Commit statuses: Read and write** and **Metadata: Read**.
How to set it up: [GitHub access](../../self-hosting/deploy/#github-access-token-or-github-app).

## Commit statuses

On GitHub, the runtime reports what it did with each commit of the tracked branch, its own
write-back commits included, as a `cicord/sync` commit status: `pending` while it applies the
revision, `success` once every server is in sync, `failure` when the revision is rejected, a
server failed or is paused, a bot is not connected or a plugin failed to start (the
description gives the reason). A status that cannot be posted is logged and
never blocks the sync. See [Commit statuses](../../self-hosting/deploy/#commit-statuses).

## What is never written back

Configuration lives in git; **data** lives in the runtime's database. XP, warnings, tickets,
member preferences, moderation history and the audit log are never committed. See
[Repository layout](../repository-layout/#configuration-in-git-data-in-the-database).
