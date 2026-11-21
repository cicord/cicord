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
