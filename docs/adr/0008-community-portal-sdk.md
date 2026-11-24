# ADR-0008 — Community portal as an SDK

**Status:** Accepted (updated: portal hosting, embedding and plugin UI decided)

## Context
The community portal is the public face of a server (onboarding, profiles, leaderboards,
tickets…). Each community wants its own look and flows — sometimes inside a site it already
has (a game fan site with its own domain and design). A fixed app cannot cover that.

## Decision
- The portal is a set of packages usable in **any React app** — or any web page:
  - `@cicord/client` — typed, framework-agnostic API client (Discord OAuth, members, roles,
    plugin routes), with a cookie mode and a **token mode** for other origins;
  - `@cicord/react` — hooks and headless components (`useMember`, `useRoles`, `<OnboardingFlow>`);
  - `@cicord/ui` — optional styled components using the server theme (`spec.portal.theme`).
- Cicord ships `create-cicord-portal`, a reference portal that is a **complete showcase** of the
  SDK (every portal endpoint, cookie and token auth, profile, onboarding, plugin routes) and
  is meant to be forked (`cicord portal init`).
- **No-code customization** (`spec.portal`: theme, title, tagline, Markdown description,
  banner, links, page toggles, locale) is part of the API (`PortalServer`), so every portal
  built on the SDK gets it.
- **Hosting**: a fork lives in the repository (`portal/`); its build output is **committed**
  and served by the runtime from the active revision (`cicord.yaml spec.portal.path`), in place
  of the default portal. The runtime has no Node; CI checks that the committed build matches
  the sources. Otherwise the runtime serves the default portal.
- **Embedding / external hosting**: a site on another origin uses token mode — Discord login
  that redirects back with a one-time code bound to the site's origin, exchanged for a
  portal-scoped bearer session token. Origins are allowed by the repository
  (`spec.portal.allowedOrigins`, token mode only) or by the operator (`--cors-origin`, cookies
  too).
- **Plugin UI = plugin routes consumed through the SDK** (`client.plugin(...)`,
  `usePluginQuery`), rendered by each portal. Plugins may ship optional React components
  imported at build time and typed route contracts. No server-driven UI, and never plugin
  JavaScript injected into a portal at runtime.
- Bot → portal links use signed, short-lived URLs (`ctx.portal.link()`).

## Consequences
- Total freedom for community developers; Cicord maintains API stability of the SDK packages.
- The runtime HTTP API becomes a public contract and must be versioned.
- A custom portal is code running on the runtime's origin: it is reviewed like code (the CI
  build check makes the sources the thing to review).
- Token mode moves session storage to third-party sites: tokens are therefore scoped to the
  portal (at most `member` outside it) and short-lived (24 h).
- See [docs/portal.md](../portal.md).
