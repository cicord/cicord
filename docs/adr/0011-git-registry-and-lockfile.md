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

## Update (2026-11-22): implementation decisions

- **Trust is computed, never declared.** A registry is `official` only when its normalized
  URL is the official registry's; any other entry of `spec.registries` is `unofficial`.
  `verified` in an index is ignored outside the official registry.
- **The official source is implicit.** It is not declared in `codi.yaml`; the name `codi` is
  reserved (`codi validate` rejects a registry named `codi`). Until the official registry
  repository exists, the official source is the plugin catalog bundled in the binary;
  installing from it copies the plugin to `plugins/<name>/`.
- **Review = install.** The admin UI shows what a plugin requests from its `plugin.yaml` at
  the resolved version; the install request carries that version, commit and integrity,
  and the runtime refuses (`409 changed`) when the latest version now resolves differently.
- **No code from unofficial sources before consent.** Listing slash commands evaluates the
  plugin, so it is only done for official sources before installation.
- **URLs.** The admin UI accepts `https://` registries only; an https registry may only list
  https plugin repositories. `file://` and local paths are for the CLI (development).
  Store fetches made for admin UI users only connect to public addresses: the check runs in
  the dialer, for every connection (DNS rebinding, redirects), with proxies bypassed.
  `CODI_ALLOW_PRIVATE_REGISTRIES=true` lifts it for registries on the operator's network.
- **Names.** Registry and plugin names match `^[a-z0-9][a-z0-9-]{0,63}$` (they become cache
  directories); index keys that do not are ignored. Registry URLs may not embed
  credentials, and URLs are redacted from errors.
- **Project-level rights.** Adding or removing a source changes `codi.yaml`: bearer, or admin
  on every server of the project. Installing needs admin on the target server.
