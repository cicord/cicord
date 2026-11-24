# ADR-0013 — Licensing

**Status:** Partially accepted (2026-11-21) — SDKs are Apache-2.0; the runtime license is undecided (it may not be open source).

## Context
The project is open source and monetized through hosting (SaaS). The SDKs must be easy to
adopt; the runtime should not be re-hosted by a competitor without contributing back.

## Proposal
- **Apache-2.0** for SDKs and authoring tools: `@cicord/sdk`, `@cicord/client`, `@cicord/react`,
  `@cicord/ui`, `create-cicord-portal`, JSON Schemas.
- **AGPL-3.0** for the runtime and CLI (`cicord`).
- Plugins choose their own license (declared in `plugin.yaml`).

## Alternatives
- Everything Apache-2.0/MIT: maximum adoption, no protection of the SaaS.
- FSL / BSL (source-available, converts to open source after N years): protection, but not
  OSI-approved, which may hurt community trust.

## To decide
Before the first public release.

## Decision so far (2026-11-21)
- **Apache-2.0** for `@cicord/sdk`, `@cicord/client`, `@cicord/react`, `@cicord/ui` and
  `create-cicord-portal` (`LICENSE` file in each package).
- The **runtime, CLI and admin UI are not licensed yet** (all rights reserved by default);
  whether they become open source is decided later, before any public release.
