# ADR-0003 — Reconciliation state in the runtime DB

**Status:** Accepted

## Context
Discord resources have no labels or annotations. To know that `channels.general` in YAML is
channel `1182…` in Discord, a persistent mapping is required (the "Terraform state" problem).
Options: state file in git, remote state file, name matching, markers hidden in topics.

## Decision
- **Stable keys**: every resource in YAML has a key distinct from its display name. Renaming
  in YAML is a rename in Discord, never a recreate. All references use keys.
- **Resource map in the runtime DB**: `(server, kind, key) → Discord ID, owner`. No state file
  in git (no commit loops, no merge conflicts on state).
- **Drift detection in real time** from gateway events, plus a periodic full diff.
- **Per-server `syncPolicy`**:
  - `selfHeal` (default `false`): revert manual changes instead of only reporting them;
  - `prune` (default `false`): delete resources that were managed and have been removed from config;
  - `deletionProtection` (default `true`): refuse any deletion, even with `prune`.
- **Ownership**: never-declared resources are never touched. Resources created by plugins at
  runtime are owned by their plugin instance and ignored by the reconciler.
- **Recovery**: `cicord adopt` rebuilds the map by matching display names, with confirmation.
  `cicord import` generates YAML from an existing server and fills the map.

## Consequences
- The DB must be backed up; losing it is recoverable (`adopt`) but needs a human check.
- `cicord plan` in CI needs the runtime (`--remote`) for an exact diff; an offline plan is
  only a schema/semantic check.
