# @cicord/client

Typed, framework-agnostic client for the HTTP API v1 of the
[Cicord](https://github.com/cicord/cicord) runtime: sign-in (cookie or token mode), the
community portal endpoints, plugin routes, and the admin API. Works in browsers, Node.js
and workers (only `fetch` is needed).

```sh
npm install @cicord/client
```

```ts
import { createCicordClient } from "@cicord/client";

const client = createCicordClient({ baseUrl: "https://cicord.example.org" }); // "" = same origin
const me = await client.meOrNull();
if (!me) client.auth.login(location.href);

const [server] = await client.portal.servers();
const member = await client.portal.me(server.key); // roles, access level…
```

**Plugin routes.** Plugins expose JSON routes to portals. Find the instance by plugin name
in `PortalServer.plugins`, then call its routes, typed by the `src/cicord-routes.d.ts` that
`cicord portal typegen` generates from the plugins of your repository:

```ts
const levels = server.plugins.find((p) => p.plugin === "levels");
if (levels) {
  const board = await client.routes("levels", server.key, levels).call("GET /leaderboard", {
    query: { limit: 10 },
  });
  board.items[0]?.displayName;
}
```

**Embedding on another site.** `createCicordClient({ baseUrl, auth: "token" })` keeps a
portal-scoped bearer session instead of the cookie (cicord.yaml `spec.portal.allowedOrigins`).

Every non-2xx response rejects with `CicordApiError` (`code`, `status`, `message`). See the
[portal SDK guide](https://cicord.dev/docs/portal/sdk/) and the
[HTTP API reference](https://cicord.dev/docs/reference/http-api/).

ESM only, with TypeScript declarations. License: Apache-2.0.
