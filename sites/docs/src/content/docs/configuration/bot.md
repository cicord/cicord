---
title: Bot
description: Reference of bots/<key>/bot.yaml - application ID, token secretRef, profile, presence, servers, intents and plugin instances with grants, config and per-server overrides.
---

A **Bot** file, `bots/<key>/bot.yaml`, is one Discord application: one token, one identity.
It lists the servers the bot joins and the plugins it runs there. Schema:
[`spec/schemas/bot.schema.json`](https://github.com/cicord/cicord/blob/main/spec/schemas/bot.schema.json).

```yaml title="bots/moderator/bot.yaml (examples/basic)"
apiVersion: cicord.dev/v1alpha1
kind: Bot
metadata:
  name: moderator
spec:
  applicationId: "998877665544332211"
  token:
    secretRef: DISCORD_TOKEN_MODERATOR
  profile:
    username: Warden
  presence:
    status: online
    activity: { type: watching, name: the community }
  servers: [my-community]
  plugins:
    - name: welcome # instance key, unique within the bot
      source: ../../plugins/welcome # a local plugin, relative to this file
      grants: [discord.messages.send, discord.channels.read, discord.members.read,
               discord.roles.assign, discord.dm.send, portal.links, storage.kv]
      config:
        channel: general
        message: "Welcome {member} to {server}! 👋"
        autoRole: member
        dmOnboarding: true
      overrides:
        my-community:
          config: # JSON merge patch over config, for this server
            message: "Hi {member}, welcome to {server}! Take a minute to read the rules."
```

## `spec`

| Field | Type | Description |
|---|---|---|
| `applicationId` | Discord ID, **required** | The application's ID. |
| `token.secretRef` | secret name (`^[A-Z][A-Z0-9_]*$`), **required** | The name of the secret holding the bot token, e.g. `DISCORD_TOKEN_MODERATOR`. Resolved from the environment first, then from the runtime's encrypted secret store. Tokens never appear in YAML. |
| `profile.username` | string (2–32) | Applied by the runtime. |
| `profile.avatar` | relative path | Image, relative to this file. Uploaded again when the file changes. |
| `profile.banner` | relative path | Image, relative to this file. Uploaded again when the file changes. |
| `presence.status` | `online` \| `idle` \| `dnd` \| `invisible` | Default `online`. |
| `presence.activity` | `{ type, name, url? }` | `type`: `playing`, `streaming`, `listening`, `watching`, `competing` or `custom`; `name`: 1–128 characters. |
| `servers` | list of server keys, **required** (≥ 1) | The servers this bot joins. |
| `intents` | list of intents | Extra gateway intents, on top of those its plugins need. See below. |
| `plugins` | list of plugin instances | See below. |

A bot whose token secret is not set is reported `Missing`. `profile.username`,
`profile.avatar` and `presence` are applied by the runtime when the bot connects and on every
revision.

### Gateway intents

A bot connects with the **union** of the intents its plugins declare, plus `spec.intents`
and what the reconciler needs (`Servers`; `AutoModerationConfiguration` when a server
declares `automod`; `ServerExpressions` for emojis, stickers and sounds).

The **privileged** intents (`ServerMembers`, `ServerPresences`, `MessageContent`) must also be
enabled in the Discord developer portal (Bot → Privileged Gateway Intents). The runtime reads
the application's flags when the bot connects; when one is missing it does not open the
gateway and the bot status names the toggle to turn on. `cicord validate` warns about each
privileged intent a plugin's manifest declares. Listing them in `spec.intents` as well keeps
them visible in review, as the community server does:

```yaml title="bots/cicord/bot.yaml (community server)"
spec:
  intents: [ServerMessages, ServerMembers, MessageContent, ServerPresences]
```

Intent names follow the same *server* vocabulary as permissions: `Servers`, `ServerMembers`,
`ServerModeration`, `ServerExpressions`, `ServerIntegrations`, `ServerWebhooks`,
`ServerInvites`, `ServerVoiceStates`, `ServerPresences`, `ServerMessages`,
`ServerMessageReactions`, `ServerMessageTyping`, `DirectMessages`, `DirectMessageReactions`,
`DirectMessageTyping`, `MessageContent`, `ServerScheduledEvents`,
`AutoModerationConfiguration`, `AutoModerationExecution`, `ServerMessagePolls`,
`DirectMessagePolls`. Each `Server…` intent is Discord's `GUILD_…` (`Servers` is `GUILDS`,
`ServerMembers` is `GUILD_MEMBERS`, `ServerMessagePolls` is `GUILD_MESSAGE_POLLS`…); the
others map 1:1 from PascalCase to SCREAMING_SNAKE_CASE (`MessageContent` is
`MESSAGE_CONTENT`). The word *guild* is never used outside the Discord adapter
([ADR-0009](../../project/decisions/0009-server-vocabulary/)).

## Plugin instances

Each entry of `spec.plugins` is a **plugin instance**. One instance runs per
(bot, instance, server): its config, storage, schedules and plugin-owned channels are
separate on each server.

| Field | Type | Description |
|---|---|---|
| `name` | key, **required** | Instance key, unique within the bot. The same plugin can run twice under two names with different configs. |
| `source` | string, **required** | A **local** path relative to this `bot.yaml` (`./plugins/x`, `../../plugins/x`), inside the repository, or a **registry** reference `<registry>/<plugin>` (`cicord/<plugin>` for the official registry). |
| `version` | semver range | **Required** for registry plugins (and locked in `cicord.lock`), **forbidden** for local ones. |
| `servers` | list of server keys (≥ 1) | Restrict the instance to some of the bot's servers. Default: all of them. |
| `grants` | list of capabilities | Must cover **every** capability the plugin's manifest requests. |
| `config` | object | The plugin's configuration, validated against its `config.schema.json`. |
| `overrides.<server>.config` | object | Per-server config, applied as a JSON merge patch (RFC 7386) over `config`. |

```yaml title="A registry plugin (community server)"
plugins:
  - name: github-releases
    source: cicord/github-releases # registry "cicord" (official), plugin "github-releases"
    version: ^0.1.0 # locked in cicord.lock by `cicord plugin add`
    grants: [discord.messages.send, discord.channels.read, storage.kv, scheduler, http.fetch]
    config:
      feeds:
        - { repo: cicord/cicord, channel: changelog, mentionRole: changelog-ping, label: Cicord }
      pollMinutes: 15
```

### Grants

Capabilities are declared by the plugin (`plugin.yaml` `spec.capabilities`) and must be
granted explicitly here. A plugin update that asks for a new capability fails validation
until you add the grant, so a permission change always shows up in review. The full list is
in [Writing a plugin](../../plugins/writing/#capabilities-and-grants).

### Config and overrides

The effective config of an instance on a server is `config`, merged with
`overrides.<server>.config` (JSON merge patch: `null` removes a field), then the schema's
`default`s. It is validated **per server**; fields marked `x-cicord-ref` (`channel`, `category` or
`role`) must name a key that exists on every server the instance runs on.

From the admin console, a moderator or admin edits either the base `config` or the server's
override; the change is written back as the minimal patch.

## Validation

`cicord validate` fails when a capability is not granted, a config does not match its schema
(after overrides, per server), a reference does not resolve (`servers`, `plugins[].servers`,
`overrides` keys, `x-cicord-ref` fields), an instance name or an `applicationId` is used
twice, a local `source` escapes the repository, a registry plugin is missing from
`cicord.lock` or its content does not match the lock, a manifest is invalid (`http.fetch`
without `spec.network.allow`, for example), or a plugin's `engine` range excludes the running
version. It warns about the privileged intents plugins declare.

When it builds a revision, the runtime also refuses a handler that needs an intent its
manifest does not declare, schedules, routes or subscriptions without their capability (both
also checked by `cicord plugin build`), and a command name defined by two instances on the
same bot and server.
