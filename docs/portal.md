# Community portal

The community portal is the public face of a server: home page, sign-in with Discord,
onboarding, member profile, leaderboards, tickets… In Cicord it is **an SDK, not a fixed app**
([ADR-0008](adr/0008-community-portal-sdk.md)):

| Package | Role |
|---|---|
| `@cicord/client` | Typed, framework-agnostic client of the runtime API ([spec/api.md](../spec/api.md) §1, §6): auth (cookie or token mode), portal endpoints, plugin routes. |
| `@cicord/react` | Provider, data hooks (`useMe`, `useMember`, `usePortalServer`, `usePluginQuery`, `useSession`…) and headless components (`<OnboardingFlow>`). |
| `@cicord/ui` | Optional styled components driven by the server theme (`ThemeProvider`, `MemberCard`, `Markdown`, `LinkIcon`…). |
| `create-cicord-portal` | The reference portal: a complete showcase of the SDK, meant to be forked. |

The runtime serves the reference portal at `/` out of the box. From the least to the most
effort, a community can:

1. [theme it](#1-theme) from YAML;
2. [customize it without code](#2-no-code-options) (texts, banner, links, pages, language);
3. [fork it](#3-fork-the-reference-portal) with `cicord portal init` and change anything, then
   [build and serve it](#4-build-and-serve-a-custom-portal) from the repository, with a
   [hot-reloading dev loop](#5-dev-loop);
4. [embed it in another site](#6-embedding-in-another-site) (a fan site with its own design)
   or host it elsewhere.

---

## 1. Theme

```yaml
# servers/<server>.yaml
spec:
  portal:
    theme:
      primary: "#5865f2"      # buttons, links, focus
      accent: "#eb459e"       # highlights
      logo: ../assets/logo.svg  # relative to this file; png, jpg, gif, webp, avif or svg
      mode: dark              # light | dark | system (default)
```

Declaring `spec.portal` publishes the server on the portal. `@cicord/ui` derives every color
from `primary` / `accent` with a contrast check for the chosen mode. The logo is served by
`GET /api/v1/portal/<server>/logo` (versioned by revision, `Content-Security-Policy:
sandbox` so an SVG can never run script). `cicord validate` reports a logo outside the
repository, behind a symbolic link or of an unsupported type (errors), and a missing file
(warning).

## 2. No-code options

```yaml
spec:
  portal:
    title: Bienvenue sur Ma Commu           # home headline (default: the server name)
    tagline: La commu des soirées jeux.     # one sentence under it
    description: |                          # Markdown subset, see below
      Ici, on joue **ensemble**.

      ## Ce que tu trouveras ici
      - des soirées jeux chaque vendredi
      - des tournois mensuels

      Lis le [règlement](https://ma-commu.example/regles).
    banner: ../assets/banner.jpg            # wide header image, same rules as the logo
    links:                                  # up to 12, http(s) only
      - { label: Site web, url: "https://ma-commu.example", icon: website }
      - { label: YouTube, url: "https://www.youtube.com/@ma-commu", icon: youtube }
    pages:                                  # pages of the reference portal (default: true)
      onboarding: true
      profile: true
      leaderboard: true                     # needs the `levels` plugin
      tickets: false                        # needs the `tickets` plugin (moderators only)
    locale: fr                              # en | fr (default: the browser language)
```

- `description` supports paragraphs, `#`–`###` headings, `-` / `1.` lists, `>` quotes,
  `**bold**`, `*italic*`, `` `code` `` and `[links](https://…)`. It is rendered as React
  elements by `<Markdown>` (`@cicord/ui`): raw HTML shows as text, non-http(s) links as plain
  text.
- `icon`: `link` (default), `website`, `discord`, `youtube`, `twitch`, `x`, `bluesky`,
  `mastodon`, `instagram`, `tiktok`, `github`, `reddit`, `steam`, `rules`, `calendar`, `shop`.
- A disabled page disappears from the navigation and its URL answers "not found". Pages
  that need a plugin (`leaderboard` → `levels`, `tickets` → `tickets`, `onboarding` →
  `onboarding`) only appear when the server runs that plugin.

All of it is served by `GET /api/v1/portal/<server>` (`PortalServer`, spec/api.md §6), so a
custom portal gets the same options for free. [examples/basic/servers/ma-commu.yaml](../examples/basic/servers/ma-commu.yaml)
uses every option.

## 3. Fork the reference portal

```sh
cd my-community            # a Cicord repository (cicord.yaml)
cicord portal init           # → portal/ (or: cicord portal init <dir>)
```

`cicord portal init` copies the reference portal (React 19 + Vite+) into `portal/`:

- `src/` — pages (`Home`, `Onboarding`, `Profile`, `Leaderboard`, `Tickets`, `Developers`),
  `portal.tsx` (server selection, theme, navigation, sign-in), `i18n/` (en, fr), `mock/` (an
  in-browser fake runtime for `pnpm dev`);
- `vendor/*.tgz` — `@cicord/client`, `@cicord/react` and `@cicord/ui`, embedded in the `cicord`
  binary, referenced as `file:` dependencies (+ `pnpm.overrides`, because `@cicord/react`
  depends on `@cicord/client` by version). The packages are not on npm yet (see
  [npm packages](#npm-packages) below);
- `src/cicord-routes.d.ts` — the types of the repository's plugin routes
  ([typed plugin routes](#typed-plugin-routes));
- `.github/workflows/cicord-portal.yml` (when run inside a Cicord repository) — see §4.

### npm packages

Publication of `@cicord/sdk`, `@cicord/client`, `@cicord/react` and `@cicord/ui` is prepared
(package metadata, READMEs, ESM + `.d.ts` builds, the manual/tag-triggered
`release-npm.yml` workflow with npm provenance) but not done yet: the owner's steps are in
[RELEASING.md](../RELEASING.md). Until then the default stays the embedded tarballs. Once
published:

- `cicord portal init --from-npm` (or `CICORD_FROM_NPM=true cicord portal init`) writes
  `"@cicord/client": "^<version of cicord>"` (same for react and ui), with no `vendor/` and no
  `@cicord/*` overrides; `cicord plugin new --from-npm` does the same for `@cicord/sdk`;
- an existing portal switches with a one-line change per package (`"^0.1.0"` instead of
  `"file:vendor/…"`), then delete the `@cicord/*` overrides and `vendor/`.

The `Developers` page of the portal is a live map of the SDK: session (cookie or token),
every portal endpoint with the SDK call that uses it, every plugin route of the server (GET
routes can be called from the page) and the raw `PortalServer`. Its navigation link is
shown to moderators and admins and in development (mock mode, `cicord dev --sim`); the URL
`/developers` always works. Delete it in your fork if you do not want it.

Plugins are consumed through their routes: find the instance in `PortalServer.plugins` by
plugin name (never hard-code bot / instance keys), then call its routes with the typed
helpers below, or untyped with `client.plugin(server, bot, instance).get("/leaderboard")`
/ `usePluginQuery(ref, "/leaderboard")`.

### Typed plugin routes

`cicord portal init` and `cicord portal build` write `src/cicord-routes.d.ts` (or run
`cicord portal typegen` yourself): the routes of **every plugin installed in the
repository** (every plugin instance of every bot, local or from a registry), keyed by
plugin name then route key, with their access level, their path parameters and the
query, body and response types of the plugin's route contract (plugin.yaml
`spec.routes.types`, [docs/plugins.md](plugins.md#typed-routes-the-route-contract)). Routes
of plugins without a contract are typed as untyped JSON. The file is a module
augmentation of `PluginRouteRegistry` (`@cicord/client`); commit it.

```tsx
import { useRouteQuery, useRouteMutation } from "@cicord/react";

// The instance is found by plugin name in PortalServer.plugins; no instance → idle query.
const board = useRouteQuery("levels", "GET /leaderboard", { query: { limit: 50 } });
board.data?.items[0]?.displayName;                       // typed from the levels contract
const poll = useRouteQuery("polls", "GET /polls/:id", { params: { id } });
// With the polls contract of docs/plugins.md (the showcase polls has no POST route):
const vote = useRouteMutation("polls", "POST /polls/:id/votes");
vote.mutate({ params: { id }, body: { choice: 2 } });
```

```ts
const levels = server.plugins.find((p) => p.plugin === "levels");
if (!levels) return;
const top = await client.routes("levels", server.key, levels).call("GET /leaderboard");
client.routes("polls", server.key, polls).url("GET /polls/:id", { params: { id } });
```

An unknown route, a missing or extra path parameter, a wrong query or body and a wrong use
of the response are type errors, so a portal build (`tsc --noEmit && vp build`) fails when
a plugin changes its contract. `cicord portal typegen --check` (a step of the generated
`cicord-portal.yml`, which also runs on changes of `plugins/`, `bots/` and `cicord.lock`) and
`cicord portal build --check` fail when `cicord-routes.d.ts` is out of date with the
repository's plugins.

A plugin the generated file does not list falls back to the contracts of the official
plugins (`levels`, `polls`, `tickets`, `onboarding`, `OfficialPluginRoutes` in
`@cicord/client`), else to untyped JSON: the reference portal type-checks in a repository
without them. The reference portal's own `cicord-routes.d.ts` is generated from
`examples/showcase`, and its `Leaderboard` and `Profile` pages use `useRouteQuery`.

## 4. Build and serve a custom portal

```yaml
# cicord.yaml
spec:
  portal:
    path: ./portal/dist      # the build output, relative to cicord.yaml
```

```sh
cicord portal build          # = cd portal && pnpm install (if needed) && pnpm build
git add portal             # sources, pnpm-lock.yaml, vendor/ AND dist/
```

The runtime serves `<active revision>/<path>` at `/` with an SPA fallback, in place of the
default portal, and swaps it with every new revision (no restart). `/api`, `/auth`,
`/hooks`, `/admin`, `/healthz` and `/readyz` are never shadowed. Hashed files under
`assets/` are cached for a year, everything else is revalidated (spec/api.md §9). Files are
read through an `os.Root` confined to the portal directory: `..` and symbolic links leading
outside are refused.

**Who builds the dist: you (or CI), and it is committed.** The runtime has no Node and only
sees what is in the git revision it applies, so the build output must be in that revision:

- *Committed dist (chosen).* Reproducible (the reviewed commit is exactly what is served),
  works with every source provider (github, git, embedded, local), keeps the runtime
  Node-free and needs no artifact storage. The generated `cicord-portal.yml` workflow
  rebuilds the portal on every pull request and push, runs its tests and **fails when the
  committed `dist/` differs from a fresh build**, so reviewers only need to read `src/`
  (the minified output cannot drift from the sources unnoticed). Vite builds are
  deterministic for the same lockfile (the same hashes were produced on macOS inside and
  outside the monorepo).
- *CI builds without committing* would need a side channel (artifact upload to the runtime,
  object storage) outside GitOps: a revision would no longer describe what is served.
- *The runtime builds* would put Node, package installs and their supply chain inside the
  runtime. Rejected.

If you prefer CI to do the commit, replace the check step of `cicord-portal.yml` by a build +
commit on `main` (the bot commit becomes a new revision).

`cicord validate` checks `spec.portal.path`: outside the repository, through a symbolic link
or not a directory → **error**; missing, or without `index.html` → **warning** (the
runtime serves the default portal until the build is committed; a missing build must not
block the reconciliation of servers and bots). The portal is code running on the runtime's
origin, like the admin UI: review it like code (the CI check above makes `src/` the thing
to review).

## 5. Dev loop

Two terminals:

```sh
cicord dev --sim                       # runtime + Discord simulator on http://127.0.0.1:7420
cd portal && pnpm dev:runtime        # Vite dev server on http://localhost:5174, hot reload
```

`pnpm dev:runtime` proxies `/api` and `/auth` to `CICORD_RUNTIME_URL` (default
`http://127.0.0.1:7420`, the default of `cicord dev`; e.g.
`CICORD_RUNTIME_URL=http://127.0.0.1:7433 pnpm dev:runtime` with `cicord dev --listen
127.0.0.1:7433`). The page and the API are then same-origin, so cookie sessions work. Two
details handled by `vite.config.ts` (same approach as the admin UI):

- `cicord dev` only answers loopback `Host` headers (DNS-rebinding protection): the proxy
  keeps `Host: localhost:5174`, which is loopback;
- unsafe requests must carry the runtime's own `Origin` (CSRF protection): the proxy
  rewrites `Origin` to the runtime's origin. It is a trusted same-machine proxy.

Signing in: `cicord dev --sim` has no Discord OAuth (`/auth/login` answers 503). The portal's
sign-in block lists the simulated users when the runtime runs the simulator (it reads
`GET /api/v1/sim/state`) and signs in with `POST /auth/dev-login`. From a shell:
`curl -X POST -H 'X-Cicord-Request: 1' -H 'Content-Type: application/json' -d
'{"userId":"<id printed by cicord dev>"}' http://127.0.0.1:7420/auth/dev-login`.

Other loops:

- `pnpm dev` (mock mode) needs no runtime at all: `src/mock` fakes the API in the browser
  (`?role=moderator` previews the moderator pages, `?mode=light` the light palette);
- `cicord dev` also serves the committed build: run `pnpm build` (or `cicord portal build`)
  and the next revision serves it at `http://127.0.0.1:7420/`;
- `pnpm test` runs the portal's tests (Vitest + Testing Library, against the mock).

## 6. Embedding in another site

A community can show its Discord on a site it already has — a game fan site with its own
domain and design. The SDK works from any origin; only the authentication differs, because
the `cicord_session` cookie (SameSite=Lax) is not sent across sites.

**Token mode.** Declare the site's origin, then create the client in token mode:

```yaml
# cicord.yaml — reviewed in git like any change
spec:
  portal:
    allowedOrigins: ["https://nightraid.example"]
```

```ts
import { createCicordClient } from "@cicord/client";

const client = createCicordClient({
  baseUrl: "https://cicord.example.org", // the runtime
  auth: "token",                       // bearer session token instead of the cookie
  tokenStore: "local",                 // "local" (default) | "session" | "memory" | custom
});

await client.auth.exchangeLinkToken(); // back from Discord (?cicord_code=) or a bot link (?cicord_token=)
const me = await client.meOrNull();
if (!me) client.auth.login(location.href); // Discord → back here with ?cicord_code=
const [server] = await client.portal.servers();
const member = await client.portal.me(server.key);              // roles…
const levels = server.plugins.find((p) => p.plugin === "levels");
const board = await client.plugin(server.key, levels.bot, levels.instance).get("/leaderboard");
```

How it works (spec/api.md §1 "Token mode"): the login redirects back with a one-time code
(60 s, single use, bound to the site's origin), redeemed by `POST /auth/token` for a
`cicord_st_…` token valid 24 h. The client sends it as `Authorization: Bearer`, uses
`credentials: "omit"`, forgets it on `401` and on `logout()`. With `@cicord/react`,
`<CicordProvider>` logs the viewer out everywhere when the token expires, `useSession()`
exposes it and `useLinkTokenLogin()` handles both `cicord_token` and `cicord_code`.

Security model of token mode:

- a token is **portal-scoped**: full role on the portal endpoints and plugin routes (a
  moderator still sees the tickets), at most `member` on everything else — a token stolen
  from a third-party site never reaches the admin endpoints;
- repository origins (`allowedOrigins`) get CORS **without credentials**: they can never use
  the cookie session, even of an admin. Cookie sessions across origins (same site, e.g.
  `portal.example.org` and `cicord.example.org`) need the operator's `--cors-origin`;
- the token lives where you put it: `localStorage` survives reloads but is readable by any
  script of the site (XSS); `"memory"` is safer but logs out on reload. Revoke with
  `client.auth.logout()`.

The reference portal switches to token mode by itself when built with `VITE_CICORD_API_URL`
on another origin (or `VITE_CICORD_AUTH=token`). For links sent by bots to point to the
external site, set cicord.yaml `spec.portal.url` (base of `ctx.portal.link()`).

**Example.** [examples/embed](../examples/embed) is a plain HTML page (no framework, no
build) on its own origin, with its own design, that signs in (Discord, or the simulator's
users), shows the member's roles and the public `levels` leaderboard:

```sh
# allow the fan site's origin: --cors-origin, or spec.portal.allowedOrigins in cicord.yaml
cicord dev --sim examples/showcase --cors-origin http://localhost:5180
node examples/embed/serve.mjs --runtime http://127.0.0.1:7420
open http://localhost:5180
```

---

## Decision: plugin UI is consumed through routes

Plugins expose **routes** (`definePlugin({routes})`, served under
`/api/v1/portal/<server>/plugins/<bot>/<instance>/…` with an access level). Portals — the
reference one, a fork, an embedding site — call them with the SDK (`client.plugin(...)`,
`usePluginQuery`) and render them however they like; the reference portal's `Leaderboard`
and `Tickets` pages are examples.

Considered and **not** chosen:

- *Server-driven UI* (plugin routes return a Cicord UI JSON spec rendered by the default
  portal): no third-party JS in the portal and installs without a rebuild, but every
  community would get the same look, the UI vocabulary would become a large versioned
  contract, and it contradicts "the community designs its own portal".
- *Plugin JS injected into the default portal at runtime*: never — it would run untrusted
  code on the runtime's origin, next to the admin session.

Plugins may still ship optional React components (a package imported at build time by the
portals that want them, so they are reviewed and pinned like any dependency). Their route
types reach the portals through the route contract (`spec.routes.types`), copied by
`cicord portal typegen` into `src/cicord-routes.d.ts`.

Considered for the route types and **not** chosen:

- *Importing the plugin sources into the portal* (`typeof import("../plugins/levels/src")`)
  so that tsc infers the handlers' types: every plugin augments `@cicord/sdk`'s
  `PluginConfig`, so two plugins in one program collide; registry plugins live in a cache
  outside the repository; and the portal would type-check the plugins' code with its own
  settings.
- *JSON Schemas on the routes, converted by Go*: self-contained, but authors would describe
  their responses twice (TypeScript and JSON Schema).

The contract is plain TypeScript that the plugin's handlers are checked against
(`defineRoutes<Routes>()`), and a type-only file can be copied anywhere.
