---
title: Embedding the portal
description: Use the portal SDK from another origin - a fan site with its own design - in token mode, with portal-scoped bearer sessions.
---

A community can show its Discord on a site it already has, such as a game fan site with its
own domain and design. The SDK works from any origin; only authentication differs, because the
`cicord_session` cookie (`SameSite=Lax`) is not sent across sites.

## Token mode

Declare the site's origin in `cicord.yaml` (reviewed in git like any change):

```yaml title="cicord.yaml"
spec:
  portal:
    allowedOrigins: ["https://nightraid.example"]
```

Then create the client in token mode:

```ts
import { createCicordClient } from "@cicord/client";

const client = createCicordClient({
  baseUrl: "https://cicord.example.org", // the runtime
  auth: "token",                       // bearer session token instead of the cookie
  tokenStore: "local",                 // "local" (default) | "session" | "memory" | custom
});

await client.auth.exchangeLinkToken(); // back from Discord (?cicord_code=) or a bot link (?cicord_token=)
const me = await client.meOrNull();
if (!me) client.auth.login(location.href); // Discord, then back here with ?cicord_code=
const [server] = await client.portal.servers();
const member = await client.portal.me(server.key);              // roles…
const levels = server.plugins.find((p) => p.plugin === "levels");
const board = await client.plugin(server.key, levels.bot, levels.instance).get("/leaderboard");
```

How it works:

1. The login redirects back to your site with a one-time code (`cicord_code`), valid 60 seconds,
   single use and bound to your site's origin.
2. The client redeems it with `POST /auth/token` for a `cicord_st_…` session token valid 24 hours.
3. It sends the token as `Authorization: Bearer`, uses `credentials: "omit"`, and forgets it
   on `401` and on `logout()`.

With `@cicord/react`, `<CicordProvider>` logs the viewer out everywhere when the token expires,
`useSession()` exposes it and `useLinkTokenLogin()` handles both `cicord_token` and `cicord_code`.

## Security model

- A token is **portal-scoped**: the user's full role on the portal endpoints and plugin routes
  (a moderator still sees the tickets), at most `member` everywhere else. A token stolen from a
  third-party site never reaches the admin endpoints.
- Repository origins (`allowedOrigins`) get CORS **without credentials**: they can never use
  the cookie session, even an admin's. Cookie sessions across origins (same site, for example
  `portal.example.org` and `cicord.example.org`) need the operator's `--cors-origin` flag, which
  a pull request cannot change.
- The token lives where you put it: `localStorage` survives reloads but is readable by any
  script of the site (XSS); `"memory"` is safer but logs out on reload. Revoke it with
  `client.auth.logout()`.

## Hosting the reference portal elsewhere

The reference portal switches to token mode by itself when built with `VITE_CICORD_API_URL`
pointing to a runtime on another origin (or with `VITE_CICORD_AUTH=token`). For links sent by
bots to open the external site, set `cicord.yaml` `spec.portal.url`: it becomes the base of
`ctx.portal.link()`.

## Example

[`examples/embed`](https://github.com/cicord/cicord/tree/main/examples/embed) is a plain HTML
page (no framework, no build) on its own origin, with its own design, that signs in (Discord,
or the simulator's users), shows the member's roles and the public `levels` leaderboard:

```sh
# allow the fan site's origin: --cors-origin, or spec.portal.allowedOrigins in cicord.yaml
cicord dev --sim examples/showcase --cors-origin http://localhost:5180
node examples/embed/serve.mjs --runtime http://127.0.0.1:7420
open http://localhost:5180
```
