# ADR-0015 — Kubernetes-style YAML resources

**Status:** Accepted

## Context
Configuration must be readable by humans, editable by tools (write-back), validated by
machines, and versioned over time.

## Decision
- Every file is a resource with `apiVersion`, `kind`, `metadata`, `spec`. Current version:
  `codi.dev/v1alpha1`. Kinds: `Project`, `Server`, `Bot`, `Plugin`.
- Each kind has a JSON Schema in `spec/schemas/` (draft 2020-12), usable by editors
  (yaml-language-server) for autocompletion.
- Conventional layout: `codi.yaml`, `servers/<key>.yaml`, `bots/<key>/bot.yaml`; the
  resource key is `metadata.name` and must match the file/directory name.
- Collections of named resources (roles, categories, channels) are **maps keyed by stable
  key**, not lists (ADR-0003).
- Breaking changes bump `apiVersion`; `codi migrate` converts files.

## Consequences
- Familiar to the DevOps audience; for the general public the admin UI hides YAML.
- Status (sync/health) is not stored in files, unlike Kubernetes; it lives in the runtime.
