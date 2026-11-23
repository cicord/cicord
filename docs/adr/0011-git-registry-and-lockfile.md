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
- Signing and verified publishers: see "Update (2026-11-23)".

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

## Update (2026-11-23): signed official index, verified publishers, publishing

- **Signature envelope.** The official registry's `index.yaml` comes with `index.yaml.sig`:
  `format: codi-registry-signature-v1`, `registry` (normalized URL), `serial`, `expires`
  (RFC 3339, UTC) and `index-sha256` (of the exact bytes of `index.yaml`), then one
  `ed25519 <key-id> <base64>` line per signing key. Each signature covers the canonical
  envelope (`codi-registry-signature-v1\nregistry: …\nserial: …\nexpires: …\nindex-sha256: …\n`).
  Signing the bare index bytes was rejected in review: an old, validly signed index could
  be replayed forever (e.g. one still listing a plugin later removed for a vulnerability),
  and a signature would not be bound to its registry.
- **Official = official URL + valid, current signature.** `FetchIndex` refuses
  (`ErrBadSignature`, fail closed) an index from an official URL unless a signature by an
  embedded official key verifies, the hash matches, `registry` is an official URL, the
  envelope has not expired, and `serial` is not lower than the highest serial already seen
  for that registry. The highest serial is stored per registry (trust on first use) in the
  runtime's state store (KV scope `codi:registry-serials`, survives restarts) or, for the
  CLI, in a file next to the plugin cache. Such a source is an error source
  (`signature: "invalid"`), never an unofficial fallback; the Store checks it again
  (defense in depth) whatever fetcher it uses. The embedded keys are a list (rotation:
  sign with old and new keys during the transition).
- **Cadence.** Maintainers re-sign after every index change and on a schedule (weekly CI,
  30-day validity, `codi registry sign` caps it at 180 days): the expiry bounds how long a
  runtime that never saw a newer index can be served an old one. The private key never
  lives in a repository (`codi registry keygen|sign|verify`).
- **Verified publishers** (`verified: true`) are only honoured from a verified official index.
- **Implicit official registry.** When the build has an official URL, the official remote
  registry is part of source `codi`, without a `codi.yaml` entry (the name stays reserved).
  The Store lists the union of bundled and remote plugins; a plugin in both comes from the
  registry only if its index `version` is a semver greater than the bundled one (the bundled
  copy needs no network and is known to work with the binary). Remote installs lock
  `codi/<name>` in `codi.lock`; the runtime resolves it from the lock like any registry
  plugin. The official URL is a package variable (set in tests); `CODI_DEV_OFFICIAL_REGISTRY`
  overrides it only in a development build, i.e. one with a test key embedded at build time
  (`-ldflags -X …devOfficialKey`); release builds ignore it. None of this is reachable from
  `codi.yaml` or the API.
- **Publishing.** `codi plugin publish` validates (build + inspect), requires a clean tree
  and a new semver, tags `v<version>` (pushed with `--push`) and writes the index entry to a
  local registry checkout (or prints it). Index entries gain optional `version` (latest
  release, used by the union rule) and `commit` (its tag's commit: resolving that version
  fails if the tag was moved). Publishing to the official registry is a pull request to
  its repository; its maintainers review, set `verified`, and sign.
- **Not decided here (owner):** the official registry repository, its URL, and the
  production signing key (`registry.OfficialURLs` and `registry.OfficialKeys` are empty
  until then, so today `codi` is the bundled plugins only).
