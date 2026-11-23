---
title: SDK packages
description: "@codi/client, @codi/react, @codi/ui and create-codi-portal: what each package does, and how to use them before they are published on npm."
---

The portal SDK is a set of layered packages. Use only `@codi/client` from a plain web page, add
`@codi/react` in a React app, and `@codi/ui` if you want the themed components of the reference
portal. All of them are **Apache-2.0**.

:::caution[Not on npm yet]
Publication of `@codi/sdk`, `@codi/client`, `@codi/react` and `@codi/ui` is prepared (package
metadata, ESM and `.d.ts` builds, a release workflow with npm provenance) but not done. Until
then, `codi portal init` and `codi plugin new` **vendor** the packages embedded in the `codi`
binary as `file:` dependencies. Once they are published, `--from-npm` (or
`CODI_FROM_NPM=true`) depends on the registry instead.
:::

## `@codi/client`

A typed, framework-agnostic client of the runtime's HTTP API (see the
[HTTP API reference](../../reference/http-api/)).

```ts
import { createCodiClient } from "@codi/client";

const client = createCodiClient();                         // same origin, cookie session
const me = await client.meOrNull();
if (!me) client.auth.login(location.pathname);
const servers = await client.portal.servers();             // servers with a portal
const profile = await client.portal.me(servers[0].key);    // the viewer's member and roles
```

- **Auth**: `auth.login()`, `auth.loginUrl()`, `auth.link(token)` (portal links),
  `auth.exchangeLinkToken()` (reads `codi_token` / `codi_code` from the URL), `auth.redeem()`,
  `auth.devLogin()` (dev mode), `auth.logout()`. Cookie mode by default; `auth: "token"` for
  [token mode](../embedding/).
- **Portal**: `portal.servers()`, `portal.server(key)`, `portal.me(key)`.
- **Plugin routes**: `client.plugin(server, bot, instance).get("/leaderboard")` (untyped), or
  `client.routes(plugin, server, instance).call("GET /leaderboard")` with the
  [generated route types](../custom-portal/#typed-plugin-routes).
- Errors are `CodiApiError` with the API's error `code`. Every state-changing call sends the
  `X-Codi-Request` header the runtime requires.

## `@codi/react`

A provider, data hooks and headless components for React 19.

| Export | Purpose |
|---|---|
| `<CodiProvider>` | Holds the client, the query cache and the session. |
| `useMe`, `useSession`, `useLogout`, `useLinkTokenLogin` | The viewer and their session. |
| `usePortalServers`, `usePortalServer`, `useServerKey` | Servers and their `PortalServer` options. |
| `useMember`, `useRoles` | The viewer's member and roles on a server. |
| `usePortalPlugin`, `usePluginQuery`, `usePluginMutation` | Plugin routes, untyped. |
| `useRouteQuery`, `useRouteMutation` | Plugin routes, typed from route contracts. |
| `<OnboardingFlow>`, `useOnboarding` | A headless onboarding flow on top of the onboarding plugin. |

```tsx
import { useRouteQuery } from "@codi/react";

function Leaderboard() {
  const board = useRouteQuery("levels", "GET /leaderboard", { query: { limit: 10 } });
  if (!board.data) return null;
  return <ol>{board.data.items.map((m) => <li key={m.userId}>{m.displayName}</li>)}</ol>;
}
```

## `@codi/ui`

Optional styled components driven by the server's theme (`spec.portal.theme`):
`ThemeProvider` (derives every color from `primary` and `accent`), `MemberCard`, `RoleChip`,
`Avatar`, `Button`, `LinkButton`, `Card`, `Notice`, `Stepper`, `ChoiceGroup`, `ProgressBar`,
`Spinner`, `Markdown` (the safe Markdown subset of `spec.portal.description`) and `LinkIcon`.

## `create-codi-portal`

The reference portal served at `/` by default: a complete showcase of the SDK (every portal
endpoint, cookie and token auth, profile, onboarding, leaderboard, tickets, a developers page,
English and French). It is not published as a package; fork it with `codi portal init` (see
[Custom portal](../custom-portal/)).

## `@codi/sdk`

Not a portal package, but part of the same family: the plugin authoring API
(`definePlugin`, `command`, `defineRoutes`) and `@codi/sdk/testing`. See
[Writing a plugin](../../plugins/writing/).
