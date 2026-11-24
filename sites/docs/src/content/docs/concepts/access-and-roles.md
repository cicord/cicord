---
title: Access and roles
description: Who can do what in the admin console and the portal - admins, moderators, members - and how Discord login, portal links and tokens are scoped.
---

Cicord derives permissions from **Discord itself**: the roles a user has on a server decide
what they can do in the admin console and on the community portal.

## Roles per server

For each server, a user is one of:

| Role | Who | Can |
|---|---|---|
| `admin` | The server owner (always), and the roles or users in `spec.access.admins` | Change any configuration of that server, run syncs, install and update plugins. |
| `moderator` | The roles or users in `spec.access.moderators` | See the server status, resources and audit log; edit the plugin settings marked `x-cicord-access: moderator`; see moderator portal routes (tickets, warnings…). |
| `member` | Any member of the Discord server | Use the portal (profile, onboarding, member routes). |
| none | Not in the server | Public portal pages and `public` plugin routes only. |

```yaml title="servers/ma-commu.yaml"
spec:
  access:
    admins:
      roles: [admin]                   # role keys of this server
    moderators:
      roles: [modo]
      users: ["112233445566778899"]    # Discord user IDs
```

The user's live roles are fetched from Discord through the server's bot (cached 30 seconds)
and matched against `spec.access`. Admin wins over moderator.

Changing **project-wide** settings (adding or removing a plugin registry in `cicord.yaml`)
requires the admin role on **every** server the runtime manages. Updating a plugin that runs
on several servers requires the admin role on each of them.

## Field-level access for plugin settings

Plugin settings pages are generated from each plugin's `config.schema.json`. By default only
admins may edit a field; a plugin author marks the fields moderators may change:

```json
"message": {
  "type": "string",
  "default": "Welcome {member} to {server}!",
  "x-cicord-widget": "textarea",
  "x-cicord-access": "moderator"
}
```

Every save still goes through [write-back](../write-back/): a commit or a pull request,
according to the policies.

## The admin console

The admin console is served at `<public-url>/admin/` and embedded in the binary. It offers,
per server: an overview with sync and health, the resources, the plan and sync history,
the audit log, plugin instances with their generated settings pages, the **Store**, server
settings (including icon and banner uploads) and **Diagnostics** (the setup wizard's checks
on demand). It has light and dark themes and a command palette. With `cicord dev --sim` it
also shows the **Simulator** panel.

## Signing in

| Method | Used by | Scope |
|---|---|---|
| **Discord login** (OAuth2, scope `identify`) | Admin console, portal | The user's roles on every server. Sessions last 7 days. |
| **Portal link** (`ctx.portal.link()` with a member) | A link sent by a plugin, e.g. in a DM | One server only, and at most `member` outside the portal endpoints, so a forwarded link never opens the admin console. The link is valid 15 minutes by default, 24 hours at most; the session it opens lasts until the link expires, and at least 1 hour. |
| **Session token** (token mode) | Portals embedded on another origin | Full role on the portal endpoints and plugin routes, at most `member` elsewhere. 24 hours. |
| **Runtime token** (`CICORD_RUNTIME_TOKEN`) | CLI and CI (`cicord plan --remote`, `cicord status`, `cicord sync`) | Admin on every server. |
| **Dev login** | `cicord dev` only | Any user; requires `--sim` or the runtime token. |

To enable Discord login, set `CICORD_DISCORD_CLIENT_SECRET` (the OAuth2 client secret of the
application) and add **`<public-url>/auth/callback`** to the application's OAuth2 redirect
URLs. The client ID defaults to the application ID of the first bot (by key);
`CICORD_DISCORD_CLIENT_ID` overrides it. Without a client secret, `/auth/login` answers `503`.

Sessions are HttpOnly, `SameSite=Lax` cookies (`Secure` behind HTTPS). Every state-changing
request needs the `X-Cicord-Request` header or an `Authorization` header, and an `Origin` that
is the public URL or an allowed origin. See the [security checklist](../../self-hosting/security/)
and, for every detail, [HTTP API → Authentication](../../reference/http-api/authentication/).
