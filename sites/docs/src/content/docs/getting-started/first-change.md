---
title: Your first change
description: Edit the YAML, preview it with cicord plan, ship it with a push or a sync, and see admin console edits come back as commits.
---

The everyday loop with Cicord is the GitOps loop: **edit, plan, merge, the runtime applies**.
This page walks through it once.

## 1. Edit the YAML

Add a role and rename a channel in `servers/<key>.yaml`:

```yaml title="servers/my-community.yaml" ins={5-8} "general-chat"
spec:
  roles:
    moderator: { name: Moderator, color: "#e67e22", hoist: true, permissions: [KickMembers, ModerateMembers, ManageMessages] }
    member: { name: Member, color: "#3498db" }
    events:
      name: Events
      color: "#f1c40f"
      mentionable: true
  hierarchy: [moderator, member, events]
  channels:
    general:
      name: general-chat   # the key "general" stays: this is a rename, not a new channel
      type: text
      category: community
```

Check it offline. `cicord validate` runs the JSON Schemas and the semantic checks (references
resolve, keys are unique, plugin configs match their schemas, capabilities are granted):

```sh
cicord validate
```

## 2. Preview the change with `cicord plan`

`cicord plan` computes the diff between the repository and the live servers. Nothing is
applied.

```text
Server my-community: 2 change(s)
  + create role "Events"
      color:       "#f1c40f"
      mentionable: true
      name:        "Events"
  ~ rename channel #general → #general-chat
      name: "general" → "general-chat"

Plan: 1 to create, 1 to update, 0 to delete.
```

The symbols are `+` create, `~` update, `-` delete, `>` reorder, `=` adopt (an existing
resource with the same name), `#` stop managing (left in Discord) and `!` blocked, with the
reason. Blocked operations never fail the plan: they explain what Cicord refuses to do and
why, for example a deletion under `deletionProtection` or a banner that needs a boost level
the server does not have. See [Reconciliation](../../concepts/reconciliation/).

There are two ways to plan:

- **Through the runtime** (recommended, and what CI does): `cicord plan --remote` uploads the
  repository to your runtime, which plans it with its own state and bots. It needs
  `CICORD_RUNTIME_URL` and `CICORD_RUNTIME_TOKEN`.
- **Locally**: `cicord plan` without `--remote` uses the bot tokens from your environment
  (`token.secretRef`) and the local state database `.cicord/state.db`.

## 3. Ship it

How the change reaches Discord depends on where the runtime reads the repository from:

- **GitHub or another git remote** (`spec.source.provider: github` or `git`): merge to the
  tracked branch. The runtime polls every minute, or immediately with a GitHub webhook on
  `<public-url>/hooks/github`. To trigger a fetch by hand: `cicord sync`.
- **Managed by Cicord** (`provider: embedded`): changes are made through the admin console,
  which commits to the embedded repository.
- **A local directory** (`cicord serve --local`, or `cicord dev`): saving the file is enough;
  every change becomes a new revision.

Wait until the runtime has applied a specific commit:

```sh
cicord status --wait --revision $(git rev-parse HEAD) --timeout 10m
```

`cicord status` shows the active revision and every server's sync and health:

```text
Revision 3f1c2ab  "Add the Events role" by alice, 2026-11-23T03:53:17Z

Servers (1)
  my-community         Synced     Healthy     synced 2026-11-23T03:53:17Z via bot
```

## 4. Edits from the admin console come back as commits

Everyone with access to the admin console (`<public-url>/admin/`) edits **the same files**.
When a moderator changes a plugin setting or an admin edits a role, Cicord patches the YAML
(comments and key order are kept) and, depending on `cicord.yaml` `spec.writeBack`:

- **commits** it on the tracked branch, then activates its own commit immediately, or
- opens a **pull request**, and the change waits for review like any other.

The repository written by `cicord init` commits plugin settings (`bots/**`) directly and opens
pull requests for structural changes (`servers/**`). Commits are authored by `cicord[bot]`,
with the Discord user who made the change in a `Cicord-Actor` trailer. Pull them like any
other commit:

```sh
git pull
git log -1
```

See [Write-back](../../concepts/write-back/) for the policies.

## Run it in CI

`cicord init` writes `.github/workflows/cicord.yml`. On every pull request it runs
`cicord validate` and posts `cicord plan --remote --comment` on the pull request; on every push
to `main` it runs `cicord status --wait` for the merged commit. Configure two repository
settings:

| Setting | Value |
|---|---|
| `secrets.CICORD_RUNTIME_TOKEN` | The runtime's bearer token (`CICORD_RUNTIME_TOKEN` of `cicord serve`). |
| `vars.CICORD_RUNTIME_URL` | The runtime's `https://` URL. |

The URL is a repository variable on purpose: it is never read from `cicord.yaml`, so a pull
request cannot choose where the token is sent.

```yaml title=".github/workflows/cicord.yml (excerpt)"
  plan:
    if: github.event_name == 'pull_request'
    needs: validate
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go install github.com/npjonath/cicord/cmd/cicord@latest
      - run: cicord plan --remote --comment
        env:
          CICORD_RUNTIME_URL: ${{ vars.CICORD_RUNTIME_URL }}
          CICORD_RUNTIME_TOKEN: ${{ secrets.CICORD_RUNTIME_TOKEN }}
          GITHUB_TOKEN: ${{ github.token }}
```

Next: learn the [repository layout](../../concepts/repository-layout/).
