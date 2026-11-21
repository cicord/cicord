# ADR-0011 — Git-based plugin registry and lockfile

**Status:** Accepted

## Context
The store must exist early for adoption but should not require running a backend service.

## Decision
- A plugin is a **git repository** with semver tags. A **registry** is a git repository
  containing an index (name → repo URL, publisher, verified flag), like Go modules or the
  Terraform registry.
- `codi.yaml` lists registries; `source: codi/welcome` resolves through the registry named `codi`.
- `codi.lock` pins, per plugin: resolved version, commit SHA, content integrity hash and
  requested capabilities. `codi plugin add` / `codi plugin update` maintain it; the runtime
  refuses registry plugins missing from the lockfile.
- Local plugins (`source: ./plugins/x`) are not locked; they are part of the repo.

## Consequences
- Publishing = pushing a tag + a PR to the registry index. A web store front can be built
  on top of the index later.
- Signing and verified publishers are future work (index already reserves fields).

## Update (2026-11-21): apt/Homebrew-style sources

Product decision: the store works like `apt` sources / Homebrew taps.
- A project has **several plugin repositories** (`codi.yaml spec.registries`). The **official**
  CODI registry is present by default (`name: codi`, trust `official`); users can add
  **unofficial** repositories (community, their own dev/test repos) from the admin UI or the
  CLI, at their own risk.
- Each registry entry gets a **trust level**: `official` (CODI-maintained, verified
  publishers, signed index — later) or `unofficial`. Unofficial sources are clearly labelled
  everywhere (store, install dialog, plugin cards) and installing from them requires an
  explicit confirmation showing the requested capabilities.
- Adding/removing a registry from the admin UI is a write-back change to `codi.yaml`
  (commit or PR per policy), like any other configuration.
- Plugin references stay `<registry>/<plugin>`; the same plugin name may exist in several
  registries (no implicit priority: the registry is part of the reference). Search in the
  admin UI spans all registries, grouped by source.
- `codi plugin add` / `codi.lock` unchanged; `codi registry add|remove|list|update` mirror the
  admin UI. The runtime refreshes registry indexes periodically and on demand.
