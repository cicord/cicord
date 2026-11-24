# ADR-0004 — UI changes become git commits

**Status:** Accepted

## Context
Owners and moderators edit settings in the admin UI. If those edits bypass git, git stops
being the source of truth and the next sync overwrites them.

## Decision
- Every configuration change made through the UI or API is **committed to git first**, then
  applied from that commit. The runtime never applies an uncommitted change.
- Providers: **GitHub App** (repo-scoped, short-lived tokens), generic git remote, or an
  **embedded repository** (go-git) for users without a forge — same model, git invisible.
- Commits are authored by `cicord[bot]` with trailers:
  ```
  Cicord-Actor: discord:<user id> (<display name>)
  Cicord-Source: admin-ui
  ```
- **Policy by path** (`spec.writeBack.policies` in `cicord.yaml`): `commit` (direct) or
  `pullRequest` (review required). Example: plugin settings → commit; roles/permissions → PR.
- **Format preservation**: YAML is patched at the node level (`yaml.v3`), keeping comments
  and key order.
- **Concurrency**: each edit is based on a SHA. If the branch moved, rebase when the patch
  applies cleanly, otherwise open a PR.
- **Boundary — config in git, data in the DB.** XP, warnings, tickets, member preferences,
  moderation history and audit log are data, never committed.

## Consequences
- Full traceability and rollback of configuration for free (git log / revert).
- Latency of a UI change ≈ commit + activation (seconds); the runtime activates its own commit
  without waiting for the webhook.
- The runtime needs write credentials; they are the most sensitive secret after bot tokens.
