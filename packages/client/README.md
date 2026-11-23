# @codi/client

Typed, framework-agnostic client for the HTTP API v1 of the
[CODI](https://github.com/npjonath/codi) runtime: sign-in (cookie or token mode), the
community portal endpoints, plugin routes, and the admin API. Works in browsers, Node.js
and workers (only `fetch` is needed).

```sh
npm install @codi/client
```

```ts
import { createCodiClient } from "@codi/client";

const client = createCodiClient({ baseUrl: "https://codi.example.org" }); // "" = same origin
const me = await client.meOrNull();
if (!me) client.auth.login(location.href);

const [server] = await client.portal.servers();
const member = await client.portal.me(server.key); // roles, access level…
```

**Plugin routes.** Plugins expose JSON routes to portals. Find the instance by plugin name
in `PortalServer.plugins`, then call its routes, typed by the `src/codi-routes.d.ts` that
`codi portal typegen` generates from the plugins of your repository:

```ts
const levels = server.plugins.find((p) => p.plugin === "levels");
if (levels) {
  const board = await client.routes("levels", server.key, levels).call("GET /leaderboard", {
    query: { limit: 10 },
  });
  board.items[0]?.displayName;
}
```

**Embedding on another site.** `createCodiClient({ baseUrl, auth: "token" })` keeps a
portal-scoped bearer session instead of the cookie (codi.yaml `spec.portal.allowedOrigins`).

Every non-2xx response rejects with `CodiApiError` (`code`, `status`, `message`). See
[docs/portal.md](https://github.com/npjonath/codi/blob/main/docs/portal.md) and the API
reference [spec/api.md](https://github.com/npjonath/codi/blob/main/spec/api.md).

ESM only, with TypeScript declarations. License: Apache-2.0.
