# CODI specification — v1alpha1

The **schemas** in [`schemas/`](schemas/) and the **SDK types** in [`sdk/codi-sdk.d.ts`](sdk/codi-sdk.d.ts)
are normative. This document explains the semantics that a schema cannot express.

| File | Kind | Schema |
|---|---|---|
| `codi.yaml` | `Project` | [project.schema.json](schemas/project.schema.json) |
| `servers/<key>.yaml` | `Server` | [server.schema.json](schemas/server.schema.json) |
| `bots/<key>/bot.yaml` | `Bot` | [bot.schema.json](schemas/bot.schema.json) |
| `<plugin>/plugin.yaml` | `Plugin` | [plugin.schema.json](schemas/plugin.schema.json) |
| `codi.lock` | — | see [§7](#7-lockfile) |

Editor support: add `# yaml-language-server: $schema=<url or path to schema>` at the top of a file.

---

## 1. Resources and keys

- Every file holds one resource: `apiVersion: codi.dev/v1alpha1`, `kind`, `metadata`, `spec`.
- `metadata.name` is the resource **key**. It must match the file name (`servers/<key>.yaml`)
  or the directory name (`bots/<key>/`).
- Keys match `^[a-z][a-z0-9-]{0,62}$` and **never change**. Display names (`name:` inside
  roles, channels…) can change freely: a changed display name is a rename in Discord.
- Changing a key means *delete + create*. `codi validate` warns when a key disappears and a
  new one with the same display name appears, and suggests `codi mv`.

## 2. References

All references use keys and are checked by `codi validate`:

| Where | Refers to |
|---|---|
| `channels.<k>.category` | `categories.<key>` of the same server |
| `profile.systemChannel`, `profile.rulesChannel` | `channels.<key>` |
| `hierarchy[]`, `access.*.roles[]` | `roles.<key>` |
| overwrite targets | `@everyone`, `roles.<key>`, or `member:<discord id>` |
| `bot.spec.servers[]`, `plugins[].servers[]`, `plugins[].overrides.<k>` | `servers/<key>.yaml` |
| plugin config fields with `x-codi-ref` | a key of the given kind in **every** server the instance runs on |

## 3. Server semantics

### Ordering
- **Roles**: `hierarchy` lists role keys from highest to lowest. Managed roles not listed
  are placed below the listed ones, in key order. Unmanaged roles keep their relative place.
  The bot's own role must stay above every role it manages (validated at plan time).
- **Categories / channels**: `position` ascending; ties broken by key. Channels without a
  category are placed at the top.

### Permissions
- `everyone.permissions` sets the @everyone role.
- `overwrites` on a category or channel are keyed by target; each target has `allow` and
  `deny`. A permission present in neither is *inherited*.
- `syncWithCategory` defaults to `true` when the channel has no `overwrites`, `false` otherwise.

### syncPolicy
| Field | Default | Effect |
|---|---|---|
| `selfHeal` | `false` | `false`: drift is reported (status `OutOfSync`). `true`: drift is reverted immediately. |
| `prune` | `false` | Delete resources that were managed and have been **removed** from the config. |
| `deletionProtection` | `true` | Refuse every deletion, even with `prune: true`. |

Ownership rules, always applied:
1. Resources that were never declared are **never** modified or deleted.
2. Resources created by plugins (`ctx.server.createChannel`) are owned by their plugin
   instance; the reconciler ignores them.
3. Removing a resource from the config without `prune` simply stops managing it.

### access
Admin UI access per server. The server owner is always admin. `admins` can change any
configuration; `moderators` can use moderation features and plugin settings marked
`x-codi-access: moderator` (see §6).

## 4. Bot semantics

- One `Bot` = one Discord application. `token.secretRef` names a secret resolved by the
  runtime (an environment variable when self-hosted). Tokens never appear in YAML.
- **Intents** = union of the intents declared by its plugins' manifests + `spec.intents`
  + what the reconciler needs (`Servers`). Privileged intents (`ServerMembers`,
  `ServerPresences`, `MessageContent`) must also be enabled in the Discord developer portal;
  the runtime reports a `Degraded` health otherwise.
- `profile` (username, avatar, banner) and `presence` are applied by the runtime.

### Plugin instances
- `name` is the instance key, unique within the bot. The same plugin can run twice under
  two names with different configs.
- `source`:
  - **local**: a path relative to this `bot.yaml` (`./plugins/x`, `../../plugins/x`); no `version`;
  - **registry**: `<registry>/<plugin>` resolved through `codi.yaml` registries; `version`
    (semver range) is required and must be locked in `codi.lock`.
- `servers` restricts the instance to a subset of the bot's servers (default: all).
- `grants` must include **every** capability requested by the manifest. A plugin update that
  requests a new capability fails validation until the grant is added.
- `config` is validated against the plugin's `config.schema.json`, after merging
  `overrides.<server>.config` (JSON merge patch, RFC 7386) for each server.

## 5. Project semantics

- `engine`: the runtime refuses a revision whose range excludes its own version.
- `source.provider`: `github` (GitHub App: webhooks, commit statuses, write-back, PRs),
  `git` (any remote, polling), `embedded` (repo hosted inside the runtime).
- `writeBack.policies` are evaluated **in order**; the first policy with a `paths` glob
  matching the changed file decides `commit` or `pullRequest`. Otherwise `defaultMode` applies.
  A UI change touching several files uses the strictest mode among them.
- Commit trailers added by write-back:
  ```
  Codi-Actor: discord:<user id> (<display name>)
  Codi-Source: admin-ui | api | cli
  ```

## 6. Plugin manifest

`plugin.yaml` is **static and reviewable**; everything dynamic (commands, handlers, routes)
comes from the code. The builder checks both agree.

### Capabilities
| Capability | Allows |
|---|---|
| `discord.messages.read` | Read message history of channels |
| `discord.messages.send` | Send messages in server channels, reply to messages |
| `discord.messages.manage` | Edit/delete messages from others, pin |
| `discord.reactions.manage` | Add/remove reactions |
| `discord.members.read` | Fetch members |
| `discord.members.manage` | Nicknames |
| `discord.members.moderate` | Timeout, kick, ban |
| `discord.roles.assign` | Add/remove roles on members |
| `discord.channels.read` | Resolve channels |
| `discord.channels.manage` | Create/delete plugin-owned channels |
| `discord.threads.manage` | Create/archive threads |
| `discord.dm.send` | Direct messages to members |
| `storage.kv` | Instance-scoped KV storage |
| `scheduler` | `schedules`, `tasks`, `ctx.schedule` |
| `http.fetch` | Outbound HTTP to `spec.network.allow` hosts only |
| `portal.routes` | Expose `routes` to the community portal |
| `portal.links` | Generate signed portal links |
| `events.publish` / `events.subscribe` | Cross-plugin events on the same (bot, server) |

Interactions on the plugin's own commands/components (`reply`, `defer`, `followUp`) need no capability.

### Config schema extensions
Plugin `config.schema.json` is standard JSON Schema (2020-12) plus:
| Keyword | Values | Effect |
|---|---|---|
| `x-codi-ref` | `channel`, `category`, `role` | Value is a resource key; rendered as a picker; existence checked per server. |
| `x-codi-widget` | `textarea`, `markdown`, `color`, `duration` | Admin UI rendering hint. |
| `x-codi-access` | `admin` (default), `moderator` | Who may edit the field in the admin UI. |

## 7. Lockfile

`codi.lock` (YAML, generated — do not edit by hand):

```yaml
lockVersion: 1
plugins:
  codi/welcome:
    version: 1.2.3
    repo: https://github.com/codi-plugins/welcome
    commit: 4f0c2d9e8b1a7c6d5e4f3a2b1c0d9e8f7a6b5c4d
    integrity: sha256-Vb3nQ0x2...
    capabilities: [discord.messages.send, discord.members.read, storage.kv]
```

## 8. SDK semantics

Contract: [`sdk/codi-sdk.d.ts`](sdk/codi-sdk.d.ts). A plugin default-exports `definePlugin({...})`.

- **Isolation**: one VM per plugin instance, events processed in order per instance.
- **Budgets**: each handler has a CPU budget and a wall-clock timeout (defaults: 50 ms CPU,
  10 s wall); exceeding them aborts the handler and logs an error.
- **Interaction deadline**: after ~2 s without a reply the runtime defers automatically
  (ephemeral when the command has `ephemeral: true`); a later `reply()` edits the deferred
  response.
- **Errors**: an uncaught error in a handler is logged with the plugin instance and revision;
  for interactions, the user gets a generic ephemeral error message. Repeated failures mark
  the instance `Degraded` in the status.
- **References**: `ctx.server.channel("general")` resolves by key in the active revision;
  `{ id }` addresses unmanaged resources.
- **Config defaults**: `default` values from `config.schema.json` are applied before the
  plugin sees `ctx.config`, so a field with a default is always present.
- **Config typing**: `codi plugin typegen` generates a `PluginConfig` module augmentation
  from `config.schema.json` (fields that are required or have a default are non-optional).
- **No Node.js APIs**: pure-JS libraries can be bundled; all I/O goes through `ctx`.

## 9. Naming map (CODI ↔ Discord API)

CODI uses *server* where Discord's API says *guild* (ADR-0009). Only names that differ are listed.

| CODI | Discord API |
|---|---|
| `Server` | guild |
| `ManageServer` | `MANAGE_GUILD` |
| `ViewServerInsights` | `VIEW_GUILD_INSIGHTS` |
| `ManageExpressions` | `MANAGE_GUILD_EXPRESSIONS` |
| `CreateExpressions` | `CREATE_GUILD_EXPRESSIONS` |
| `SendTTSMessages` | `SEND_TTS_MESSAGES` |
| `UseVAD` | `USE_VAD` |
| `Servers` | `GUILDS` |
| `ServerMembers` | `GUILD_MEMBERS` |
| `ServerModeration` | `GUILD_MODERATION` |
| `ServerExpressions` | `GUILD_EXPRESSIONS` |
| `ServerIntegrations` | `GUILD_INTEGRATIONS` |
| `ServerWebhooks` | `GUILD_WEBHOOKS` |
| `ServerInvites` | `GUILD_INVITES` |
| `ServerVoiceStates` | `GUILD_VOICE_STATES` |
| `ServerPresences` | `GUILD_PRESENCES` |
| `ServerMessages` | `GUILD_MESSAGES` |
| `ServerMessageReactions` | `GUILD_MESSAGE_REACTIONS` |
| `ServerMessageTyping` | `GUILD_MESSAGE_TYPING` |
| `ServerScheduledEvents` | `GUILD_SCHEDULED_EVENTS` |
| `ServerMessagePolls` | `GUILD_MESSAGE_POLLS` |

Other permissions and intents map 1:1 from PascalCase to SCREAMING_SNAKE_CASE.

## 10. Semantic validation (`codi validate`)

Beyond JSON Schema, validation fails when:
1. a key does not match its file/directory name, or is duplicated;
2. a reference (§2) does not resolve;
3. a plugin's requested capability is not in `grants`;
4. a handler needs an intent missing from the manifest (build step);
5. a plugin config does not validate against its schema (after overrides, per server);
6. a registry plugin is missing from `codi.lock`, or its integrity does not match;
7. `engine` excludes the running CLI/runtime version;
8. a command name is invalid or defined twice on the same bot/server;
9. `http.fetch` is requested without `spec.network.allow`.

Warnings: a key disappears while a resource with the same display name appears (probable
rename), privileged intents in use, `prune: true` with `deletionProtection: false`.
