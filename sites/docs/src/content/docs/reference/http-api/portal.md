---
title: "HTTP API: community portal"
description: The public portal endpoints (PortalServer, logo and banner, the viewer), plugin routes with their access levels, and how the admin console and portals are served.
---

Part of the [HTTP API reference](../). For building portals, see the
[Community portal](../../../portal/overview/) guides.

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/portal/servers` | none | `{"items": [PortalServer]}`: the servers with a portal. |
| GET | `/api/v1/portal/{server}` | none | `PortalServer` (`404` when the server has no portal). |
| GET | `/api/v1/portal/{server}/logo` | none | The `portal.theme.logo` image ([below](#logo-and-banner)). |
| GET | `/api/v1/portal/{server}/banner` | none | The `portal.banner` image, same rules as the logo. |
| GET | `/api/v1/portal/{server}/me` | member | `{"user": User, "member": {"nick", "joinedAt", "roles": [{"key", "id", "name", "color"}]}, "role": "member" \| "moderator" \| "admin"}` |
| GET | `/api/v1/portal/{server}/analytics?period=7d\|30d\|90d` | per `spec.analytics.portal` | [Server-wide activity](#activity) (default `30d`). |
| ANY | `/api/v1/portal/{server}/plugins/{bot}/{instance}/{path…}` | per route | [Plugin routes](#plugin-routes). |

All portal endpoints answer `503` before the first revision. `/portal/{server}/me` answers
`401` when anonymous and `403` when the caller is not a member. Member roles have `key: ""`
when they are not declared in YAML.

## Activity

`GET /api/v1/portal/{server}/analytics` shares the server's activity with its community, when
the server opts in with [`spec.analytics.portal`](../../../configuration/server/#analytics):
`members` (signed-in members of the server: `401` anonymous, `403` not a member) or `public`
(anyone). With `off` (the default) or without analytics, it answers `404`.

```json
{
  "server": "showcase", "period": "7d",
  "from": "2026-11-19T00:00:00Z", "to": "2026-11-26T00:00:00Z", "since": "2026-11-25T12:00:00Z",
  "kpis": [
    { "metric": "members.total", "value": 1, "previous": null, "spark": [[1795564800, 1]] },
    { "metric": "messages.sent", "value": 4, "previous": null,
      "spark": [[1795046400, 0], [1795132800, 0], [1795219200, 0], [1795305600, 0], [1795392000, 0], [1795478400, 0], [1795564800, 4]] },
    …
  ],
  "topChannels": [{ "id": "1575125354564026388", "name": "general", "value": 4 }],
  "topBotCommands": [{ "bot": "Cicord", "command": "/rank", "value": 1 }],
  "heatmap": [[0, 0, …], …]   // 7 rows of 24 hours
}
```

- `kpis`, in this order: `members.total` (human members, last value), `members.joined`,
  `members.active` (average per day), `messages.sent`, `voice.seconds`, `reactions.added`,
  `bots.commands` and `members.active.bots` (average per day), each compared with the
  previous period (`previous` is `null` when it is not covered), with one `spark` point per
  day. The periods are whole UTC days ending today, as in the
  [admin summary](../analytics/#summary).
- `topChannels`: the 5 channels with the most messages **among those `@everyone` can see
  now**; private and deleted channels are never named nor counted there (the
  `messages.sent` KPI is server-wide). The channels a plugin created (temporary voice rooms,
  tickets, often named after a member) are never named: those `@everyone` can see count in
  one entry per plugin instance, `{ "id": "plugin:<bot>/<instance>", "name": "<instance>",
  "value": …, "group": true }`.
- `topBotCommands`: the 8 most used commands of every bot of the server.
- `heatmap[weekday][hour]`: messages per weekday (`0` = Monday) and UTC hour.

`@cicord/client`: `client.portal.analytics(server, period)`; `@cicord/react`:
`usePortalAnalytics(period, server?)`.

## PortalServer

"Servers with a portal" are servers that declare `spec.portal` or run a plugin with routes.

```json
{ "key": "my-community", "name": "My Community", "iconUrl": "",
  "theme": { "primary": "#5865f2", "accent": "#eb459e", "logo": "/api/v1/portal/my-community/logo?v=local-b24ab87", "mode": "dark" },
  "title": "Welcome to My Community", "tagline": "The community for game nights, tournaments and live streams.",
  "description": "Here we play **together**: Minecraft, Valorant, League of…", "banner": "/api/v1/portal/my-community/banner?v=local-b24ab87",
  "links": [ { "label": "Website", "url": "https://my-community.example", "icon": "website" }, … ],
  "pages": { "onboarding": true, "profile": true, "leaderboard": true, "tickets": true },
  "locale": "en",
  "plugins": [ { "bot": "moderator", "instance": "onboarding", "plugin": "onboarding", "routes": ["GET /steps", "POST /complete"] } ] }
```

This is `examples/basic` served by `cicord dev --sim`.

- `theme` always has its four fields (`""` = unset). `theme.logo` is the logo endpoint (with
  `?v=<revision id>`, so caches follow new commits) or an absolute URL; `mode` is `light`,
  `dark`, `system` or `""`.
- The no-code fields of `spec.portal` ([Server → portal](../../../configuration/server/#portal))
  are always present: `title` (default: the server name), `tagline`, `description` (Markdown
  subset source, rendered by the portal, never HTML), `banner` (like `theme.logo`), `links`
  (`[{label, url, icon}]`; `url` is http(s) by schema, `icon` defaults to `"link"`), `pages`
  (`{onboarding, profile, leaderboard, tickets}`, unset = `true`) and `locale` (`"en"`, `"fr"`
  or `""`).
- `plugins` lists the instances on that server that declare routes.

### Logo and banner

When `portal.theme.logo` (or `portal.banner`) is a repository file, the endpoint serves it.
The path is relative to the server YAML; the file must stay inside the repository, be reached
without symbolic links, be at most 10 MiB and be a png, jpg, gif, webp, avif or svg. A URL logo
redirects (`302`). Unset or invalid: `404`.

Headers: `Cache-Control: public, max-age=300`,
`Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox` (a repository
SVG never runs script) and `Cross-Origin-Resource-Policy: cross-origin` (embedding sites may
display it).

## Plugin routes

Routes declared with `definePlugin({routes})` (see [Plugin API](../../../plugins/api/)) are
served under `/api/v1/portal/{server}/plugins/{bot}/{instance}/{path…}`. The JSON body and
response are passed through; an unknown route answers `404`.

- **Access** (required on every route; a missing or unknown value fails at load time):
  `public` (anyone; the viewer is still filled in when logged in), `member`, `moderator` or
  `admin`, with the [roles](../authentication/#roles) of the
  server (portal endpoints count the full role of link sessions and session tokens on their
  server). Anonymous: `401`; insufficient role: `403`. The runtime bearer token passes every
  check (no viewer).
- **Request**: the handler receives `request.viewer = {user, member}` (the member is fetched
  from Discord; after a route with an unsafe method the viewer's cached member is dropped, so
  the next `/portal/{server}/me` shows roles the plugin just changed), the query parameters
  (first value of each) and the JSON body (at most 1 MiB; non-JSON: `400`; an empty body is no
  body).
- **Response**: the handler's status (default `200`) and its JSON body as
  `application/json; charset=utf-8` with `Cache-Control: no-store`, plus its headers except
  `Set-Cookie`, `Content-Type`, `Content-Length`, `Transfer-Encoding`, `Connection`,
  `Location`, `Content-Security-Policy`, `Strict-Transport-Security` and `Access-Control-*`.
  No body: `204` (or the handler's status when it is not 200). A thrown error answers
  `500 internal` ("the plugin route failed").

## Static UIs

- `/admin` redirects (`301`) to `/admin/`. `/admin/*` serves the admin console and `/*` the
  portal, both with an SPA fallback to `index.html` (`Cache-Control: no-cache`).
- Hashed files under `assets/` are `Cache-Control: public, max-age=31536000, immutable`; other
  files are `no-cache` (revalidated with `Last-Modified`). A missing file with an extension
  (other than `.html`) is a plain `404`. Content types come from the file extension.
- **Custom portal**: when `cicord.yaml` `spec.portal.path` of the **active revision** points
  to a directory of that revision containing `index.html` (a committed build, see
  [Custom portal](../../../portal/custom-portal/)), it is served at `/` instead of the embedded
  default portal, with the same rules and an `X-Cicord-Portal: repository` header. The
  directory is resolved on every request, so a new revision swaps the files without a
  restart; when it is missing (not built yet) the default portal is served. Files are opened
  through an `os.Root` confined to that directory: `..` and symbolic links leading outside it
  are refused, and a portal path reached through a symbolic link is ignored (`cicord validate`
  reports it).
- `/api/*`, `/auth/*`, `/hooks/*`, `/admin/*`, `/healthz` and `/readyz` are never shadowed by
  the portal: unknown paths there are JSON `404 not_found`.
- Methods other than `GET` and `HEAD` on a static UI answer `405 invalid`. A UI that was not
  built into the binary answers `404`.
