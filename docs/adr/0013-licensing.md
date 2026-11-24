# ADR-0013 — Licensing

**Status:** Accepted (2026-11-24) — everything is Apache-2.0.

## Context
The project is open source and monetized through hosting (SaaS). The SDKs must be easy to
adopt, and the runtime should be as easy to self-host, audit and contribute to.

## Options considered
- **AGPL-3.0** for the runtime and CLI, Apache-2.0 for the SDKs (the 2026-11-21 proposal):
  protects the hosted offering against re-hosting without contributing back, but deters
  companies from adopting or contributing to the runtime.
- **FSL / BSL** (source-available, converts to open source after N years): protection, but
  not OSI-approved, which may hurt community trust.
- **Apache-2.0 everywhere**: maximum adoption, an explicit patent grant, one license for the
  whole repository.

## Decision
**Apache-2.0 for every part of Cicord** (owner decision, 2026-11-24), superseding the AGPL
proposal and the interim "runtime not licensed" state of 2026-11-21:

- the runtime, the CLI and the admin console (`cicord`, `packages/admin`);
- the SDKs and authoring tools: `@cicord/sdk`, `@cicord/client`, `@cicord/react`,
  `@cicord/ui`, `create-cicord-portal`, the JSON Schemas and the specification;
- the official plugins, the examples, the landing page and the documentation sites.

The `LICENSE` file at the root covers the repository; each published npm package carries its
own copy. `NOTICE` names the copyright holder ("The Cicord Authors").

Third-party plugins choose their own license (`metadata.license` in `plugin.yaml`, an SPDX
identifier).

## Consequences
- Anyone may host Cicord, including commercially; the hosted offering competes on service,
  not on license terms.
- Contributions are accepted under Apache-2.0 (inbound = outbound, section 5 of the license).
  Whether to add a DCO sign-off or a CLA is left for later.
