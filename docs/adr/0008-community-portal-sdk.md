# ADR-0008 — Community portal as an SDK

**Status:** Accepted

## Context
The community portal is the public face of a server (onboarding, profiles, leaderboards,
tickets…). Each community wants its own look and flows; a fixed app cannot cover that.

## Decision
- The portal is a set of packages usable in **any React app**:
  - `@codi/client` — typed, framework-agnostic API client (Discord OAuth, members, roles, plugin routes);
  - `@codi/react` — hooks and headless components (`useMember`, `useRoles`, `<OnboardingFlow>`);
  - `@codi/ui` — optional styled components using the server theme (`spec.portal.theme`).
- CODI ships `create-codi-portal`, a reference portal meant to be forked.
- Plugins can ship client packages (React components) next to their server code, with
  generated types for their API routes.
- Hosting: `portal/` in the repo is built by CI and served by the runtime; otherwise the
  runtime serves the default portal; external hosting is supported via CORS.
- Bot → portal links use signed, short-lived URLs (`ctx.portal.link()`).

## Consequences
- Total freedom for community developers; CODI maintains API stability of the SDK packages.
- The runtime HTTP API becomes a public contract and must be versioned.
