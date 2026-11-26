---
title: SDK packages
description: "@cicord/client, @cicord/react, @cicord/ui and create-cicord-portal: what each package does, from npm (the default) or vendored by the cicord binary."
---

The portal SDK is a set of layered packages. Use only `@cicord/client` from a plain web page, add
`@cicord/react` in a React app, and `@cicord/ui` if you want the themed components of the reference
portal. All of them are **Apache-2.0**.

:::note[npm or vendored]
`@cicord/sdk`, `@cicord/client`, `@cicord/react` and `@cicord/ui` are published on npm with each
Cicord release, at the same version as the binary (ESM and `.d.ts` builds, with
[npm provenance](../../getting-started/installation/#verify-a-download)):
`pnpm add @cicord/client`. `cicord portal init` depends on them at the exact version of the
`cicord` binary (`"@cicord/client": "0.2.0"`), and `cicord portal upgrade` follows cicord
upgrades. `cicord portal init --vendor` (offline or air-gapped builds) **vendors** the copies
embedded in the binary as `file:` dependencies instead, which needs no registry access;
`cicord portal upgrade --to-npm`
[moves such a portal to npm](../custom-portal/#from-the-vendored-tarballs-to-npm).
`cicord plugin new` still vendors `@cicord/sdk` unless given `--from-npm` (or
`CICORD_FROM_NPM=true`).
:::

## `@cicord/client`

A typed, framework-agnostic client of the runtime's HTTP API (see the
[HTTP API reference](../../reference/http-api/)).

```ts
import { createCicordClient } from "@cicord/client";

const client = createCicordClient();                         // same origin, cookie session
const me = await client.meOrNull();
if (!me) client.auth.login(location.pathname);
const servers = await client.portal.servers();             // servers with a portal
const profile = await client.portal.me(servers[0].key);    // the viewer's member and roles
```

- **Auth**: `auth.login()`, `auth.loginUrl()`, `auth.link(token)` (portal links),
  `auth.exchangeLinkToken()` (reads `cicord_token` / `cicord_code` from the URL, and starts the Discord sign-in of a [case link](../../reference/http-api/authentication/#case-links)'s `cicord_scope`), `auth.redeem()`,
  `auth.devLogin()` (dev mode), `auth.logout()`. Cookie mode by default; `auth: "token"` for
  [token mode](../embedding/).
- **Portal**: `portal.servers()`, `portal.server(key)`, `portal.me(key)`,
  `portal.analytics(key, "7d" | "30d" | "90d")` (the server activity shared by
  [`spec.analytics.portal`](../../self-hosting/analytics/#configuration); `404` when it is
  `off`).
- **Plugin routes**: `client.plugin(server, bot, instance).get("/leaderboard")` (untyped), or
  `client.routes(plugin, server, instance).call("GET /leaderboard")` with the
  [generated route types](../custom-portal/#typed-plugin-routes).
- **Admin console**: `client.admin` (status, history, plans, sync, pause and resume, audit, plugin settings
  and secrets, overlays, member data and purges, dead letters, interop,
  `subscribe()` to the [event stream](../../reference/http-api/events/)), `client.analytics`
  (summary, series, catalog) and `client.store`, for moderators and admins.
- Errors are `CicordApiError` with the API's error `code`, and `retryAfter` (seconds, from
  `Retry-After` on `429` and `503`). `partialPrivacyReport(error)` returns the steps of an
  erasure that failed part-way. Every state-changing call sends the `X-Cicord-Request` header
  the runtime requires.

## `@cicord/react`

A provider, data hooks and headless components for React 19.

| Export | Purpose |
|---|---|
| `<CicordProvider>`, `useCicord`, `useCicordClient` | Holds the client, the query cache and the session. |
| `useMe`, `useSession`, `useLogout`, `useLinkTokenLogin` | The viewer and their session. |
| `usePortalServers`, `usePortalServer`, `useServerKey` | Servers and their `PortalServer` options. |
| `useMember`, `useRoles` | The viewer's member and roles on a server. |
| `usePortalAnalytics` | The server activity shared with the portal (`spec.analytics.portal`). |
| `usePortalPlugin`, `usePluginQuery`, `usePluginMutation` | Plugin routes, untyped. |
| `useRouteQuery`, `useRouteMutation` | Plugin routes, typed from route contracts. |
| `<OnboardingFlow>`, `useOnboarding` | A headless onboarding flow on top of the onboarding plugin. |
| `useCicordQuery`, `cicordKeys`, `QueryCache` | The query cache itself: custom queries, cache keys, optimistic updates. |

```tsx
import { useRouteQuery } from "@cicord/react";

function Leaderboard() {
  const board = useRouteQuery("levels", "GET /leaderboard", { query: { limit: 10 } });
  if (!board.data) return null;
  return <ol>{board.data.items.map((m) => <li key={m.userId}>{m.displayName}</li>)}</ol>;
}
```

## `@cicord/ui`

Optional styled components driven by the server's theme (`spec.portal.theme`):
`ThemeProvider` (derives every color from `primary` and `accent`; `useCicordTheme`,
`useResolvedMode`), `MemberCard`, `RoleChip`, `Avatar`, `Button`, `LinkButton`, `Card`,
`Notice`, `Stepper`, `ChoiceGroup`, `ChoiceTile`, `ProgressBar`, `Spinner`, `Markdown` (the
safe Markdown subset of `spec.portal.description`) and `LinkIcon`.

## `create-cicord-portal`

The reference portal served at `/` by default: a showcase of the SDK (cookie and token auth,
profile, onboarding, leaderboard, tickets, a developers page, English and French). It is not
published on npm; fork it with `cicord portal init` (see [Custom portal](../custom-portal/)).

## `@cicord/sdk`

Not a portal package, but part of the same family: the plugin authoring API
(`definePlugin`, `command`, `defineRoutes`) and `@cicord/sdk/testing`. See
[Writing a plugin](../../plugins/writing/).
