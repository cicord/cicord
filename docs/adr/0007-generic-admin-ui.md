# ADR-0007 — Generic admin UI generated from schemas

**Status:** Accepted

## Context
Building a custom React app per deployment in CI is slow, fragile and requires Node in
every pipeline. Owners need a management UI, not a bespoke one.

## Decision
- One generic **admin UI**, prebuilt and embedded in the `codi` binary.
- Plugin settings pages are **generated from each plugin's `config.schema.json`**.
- CODI JSON Schema extensions drive widgets and validation:
  - `x-codi-ref: channel | category | role` — the value is a resource key, rendered as a picker
    and checked by semantic validation;
  - `x-codi-widget: textarea | color | markdown | duration` — rendering hints;
  - `x-codi-access: admin | moderator` — who may edit the field (default `admin`).
- Every save goes through write-back (ADR-0004).

## Consequences
- No per-deployment build for the admin side.
- Plugins needing richer admin screens are a later extension point (plugin-provided ES
  modules loaded into defined slots).
