---
title: Community portal
description: The public face of a server - sign-in with Discord, onboarding, profiles, leaderboards - built as an SDK rather than a fixed app, with four levels of customization.
---

The community portal is the public face of a server: a home page, sign-in with Discord,
onboarding, member profiles, leaderboards, tickets for staff… In Cicord it is **an SDK, not a
fixed app**, so every community can have its own.

The runtime serves a complete **reference portal** at `/` out of the box. Declaring
`spec.portal` in a server's YAML publishes that server on it.

## Four levels of customization

From the least to the most effort, a community can:

1. **Theme it** from YAML: primary and accent colors, logo, light or dark mode.
2. **Customize it without code**: title, tagline, Markdown description, banner, links, pages,
   language. See [Customize from YAML](../customize/).
3. **Fork it** with `cicord portal init`, change anything, and have the runtime serve your build
   from the repository, with a hot-reloading dev loop. See [Custom portal](../custom-portal/).
4. **Embed it in another site** (a fan site with its own domain and design), in token mode. See
   [Embedding](../embedding/).

## The pages of the reference portal

| Page | Shows | Needs |
|---|---|---|
| Home | Title, tagline, description, banner, links | |
| Onboarding | Step-by-step role picking | The [onboarding plugin](../../plugins/official/#onboarding) |
| Profile | The member's roles and data | Sign-in |
| Leaderboard | XP ranking | The [levels plugin](../../plugins/official/#levels) |
| Tickets | Open tickets, for moderators | The [tickets plugin](../../plugins/official/#tickets) |
| Developers | A live map of the SDK: session, every portal endpoint, every plugin route | Shown to moderators and admins, and in development |

Pages that need a plugin only appear when the server runs that plugin; each page can be turned
off with `spec.portal.pages`. The interface is in English and French.

## How members get in

- **Sign in with Discord** (OAuth2). The runtime needs `CICORD_DISCORD_CLIENT_SECRET` and the
  redirect URL `<public-url>/auth/callback` registered in the Discord application.
- **Links from bots.** A plugin calls `ctx.portal.link("/onboarding", { member })` and sends
  the result, typically in a DM with a button. Opening it signs that member in, restricted to
  that one server, for a short time (15 minutes by default, 24 hours at most).

A typical onboarding: a member joins → the `welcome` plugin sends a DM with a button → the
themed portal opens, signed in → the member picks roles → the `onboarding` plugin applies them.

## Plugins on the portal

Plugins expose **routes** (`definePlugin({ routes })`, served under
`/api/v1/portal/<server>/plugins/<bot>/<instance>/…` with an access level). Portals (the
reference one, a fork, an embedding site) call them through the SDK and render them however
they like. There is no server-driven UI, and plugin JavaScript is never injected into a portal
at run time: the portal stays code you review.

## The SDK

| Package | Role |
|---|---|
| `@cicord/client` | Typed, framework-agnostic client of the runtime API: auth (cookie or token mode), portal endpoints, plugin routes. |
| `@cicord/react` | Provider, data hooks and headless components such as `<OnboardingFlow>`. |
| `@cicord/ui` | Optional styled components driven by the server theme. |
| `create-cicord-portal` | The reference portal, meant to be forked. |

See [SDK packages](../sdk/). The packages are **not on npm yet**: `cicord portal init` vendors
them from the `cicord` binary.

:::note[Inside Discord?]
Running the portal inside the Discord client as an Activity is a proposed option
([ADR-0016](https://github.com/cicord/cicord/blob/main/docs/adr/0016-portal-as-discord-activity.md)),
not implemented.
:::
