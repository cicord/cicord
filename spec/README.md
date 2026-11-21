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
- Changing a key keeps the Discord resource when the display name and kind are unchanged:
  the reconciler sees the old key removed and the new one added, adopts the resource under
  the new key and drops the old mapping (plan: `adopt` + `unmanage "move … mapping"`).
  `codi mv` renames a key and every reference to it in one step. Changing the key *and* the
  display name in the same revision is a removal plus a creation.

## 2. References

All references use keys and are checked by `codi validate`:

| Where | Refers to |
|---|---|
| `channels.<k>.category` | `categories.<key>` of the same server |
| `profile.systemChannel`, `profile.rulesChannel` (deprecated alias), `community.*Channel`, `afk.channel`, `widget.channel`, `automod.*.actions.alert`, `automod.*.exemptChannels[]`, `onboarding.defaultChannels[]`, `onboarding.prompts[].options[].channels[]`, `welcomeScreen.channels[].channel` | `channels.<key>` |
| `automod.*.exemptRoles[]`, `onboarding.prompts[].options[].roles[]`, `emojis.*.roles[]` | `roles.<key>` |
| `hierarchy[]`, `access.*.roles[]` | `roles.<key>` |
| overwrite targets | `@everyone`, `roles.<key>`, or `member:<discord id>` |
| `bot.spec.servers[]`, `plugins[].servers[]`, `plugins[].overrides.<k>` | `servers/<key>.yaml` |
| plugin config fields with `x-codi-ref` | a key of the given kind in **every** server the instance runs on |

## 3. Server semantics

Implemented by `internal/reconcile` (the package documentation is the detailed reference).
A **plan** is the ordered list of changes between the YAML and the live server; actions are
`create`, `update`, `delete`, `adopt`, `reorder` and `unmanage`. Operations the reconciler
refuses are reported separately as **blocked** (with a `reason` and, when known, the
`fields` that could not be applied); they never fail the plan.

### Identity, adoption and ownership
- YAML keys are mapped to Discord IDs in the runtime DB (resource map, ADR-0003). A display
  name change is an `update` (rename), never a recreate. A key whose mapped ID no longer
  exists in Discord is created again.
- **Adoption by name**: a declared key without a mapping adopts an *unmapped* live resource
  of the same kind (role, category, or channel of the same type) with exactly the same
  display name (`adopt`; differing fields are updated by the same change). Resources mapped
  under any owner, managed (integration/bot) roles and @everyone are never candidates; a
  live resource is adopted at most once; keys are processed in key order, candidates in ID
  order. Because mappings are written as soon as a create succeeds, a resource created by a
  crashed apply is simply adopted on the next run.
- Resources that were never declared (unmapped) are **never** modified or deleted.
- Resources created by plugins (`ctx.server.createChannel`) are owned by their plugin
  instance (`plugin:` owner); the reconciler ignores them, and a YAML key whose mapping is
  owned by a plugin is blocked (`ownership: …`).
- Managed roles (bot / integration roles) are never touched (`managed: …` when declared).
  @everyone is only changed through `spec.everyone`.
- Text-like channel names (text, announcement, forum, media) are compared after Discord's
  normalization (lowercase, whitespace runs → `-`): `name: Général Chat` matches
  `#général-chat` and does not cause a perpetual rename.

### Removed keys and syncPolicy
| Field | Default | Effect |
|---|---|---|
| `selfHeal` | `false` | `false`: drift is reported (status `OutOfSync`). `true`: drift (gateway role/channel/server events on managed resources) triggers a debounced sync that reverts it. |
| `prune` | `false` | Delete resources that were managed and have been **removed** from the config. |
| `deletionProtection` | `true` | Refuse every deletion, even with `prune: true`. |

A managed key that disappears from the YAML produces:
- `delete` when `prune: true` and `deletionProtection: false`;
- blocked `deletionProtection` when `prune: true` and `deletionProtection: true` (the mapping
  is kept, so turning the protection off later deletes it);
- `unmanage` when `prune: false`: the mapping is dropped and the Discord resource is left as
  is. `unmanage` is also used, whatever the policy, when the resource is already gone.

Deletions run last (channels, then categories, then roles).

### Live status, boost and Community gating
The planner reads the live boost level (`premium_tier`), boost count and feature flags.
Settings Discord would refuse are **blocked in the plan with a reason** instead of failing at
apply time (reason prefixes, explained by the admin UI and `codi plan`):

| Setting | Needs | Blocked reason |
|---|---|---|
| `profile.banner` (animated: level 3) | boost level 2 (`BANNER`) | `boost: banner requires boost level 2; server is level 0 (0 boost(s))` |
| `profile.splash` | boost level 1 (`INVITE_SPLASH`) | `boost: invite splash requires boost level 1; …` |
| animated (GIF) `profile.icon` | boost level 1 (`ANIMATED_ICON`) | `boost: an animated icon requires …` |
| `roles.*.icon`, `roles.*.emoji` | boost level 2 (`ROLE_ICONS`) | `boost: a role icon requires …` |
| `roles.*.colors` with `secondary` | boost level 2 (`ENHANCED_ROLE_COLORS`) | `boost: a gradient role color requires …` (the primary color is still applied) |
| announcement / stage channels, rules / updates / safety alerts channels, welcome screen, native onboarding | Community (live, or enabled by the same plan) | `community: … requires the Community feature (set community.enabled: true)` |
| emojis, stickers, soundboard sounds | slots of the boost level (emojis 50/100/150/250 static + as many animated, stickers 5/15/30/60, sounds 8/24/36/48) | `limit: maximum number of stickers reached (5 at boost level 0)` |
| AutoMod rules | 6 keyword rules, 1 of each other trigger | `limit: a server can have at most 1 spam AutoMod rule(s); …` |

A feature flag is enough (partnered/verified servers get some without boosts). Blocked
items keep the server `OutOfSync` and are applied by the first sync after the boost level
(or Community) is reached.

### Community
`community: {enabled, rulesChannel, updatesChannel, safetyAlertsChannel}` toggles the
`COMMUNITY` feature. Enabling it needs a rules channel and an updates channel (text channels,
created by the same plan if needed), `profile.verificationLevel` at least `low` and
`profile.explicitContentFilter: allMembers`; a missing prerequisite blocks the change with
the full list (`codi validate` reports the same). The toggle, the channels and the
verification/filter changes are sent in one request after the channels exist; announcement
and stage channels that need Community are created right after it. Disabling sends the live
mutable features without `COMMUNITY`. `profile.rulesChannel` is a deprecated alias of
`community.rulesChannel` (validation warns; both set to different keys is an error).

### Compared fields
- Always managed (the YAML zero value is applied): role name, color (0 = none), hoist,
  mentionable, permissions; category/channel name and overwrites; channel `topic` (text,
  announcement, forum, media), `nsfw`, `slowmode` (text, voice, stage, forum, media) and
  `userLimit` (voice, stage).
- Managed only when set: channel `bitrate` (> 0), each `profile` field (non-empty), the
  server icon, banner and splash (files relative to the server YAML, re-uploaded when the
  file hash or the live image URL changes), `everyone` (when present), each
  `systemChannelFlags` toggle (true = the message is sent; undeclared toggles keep their
  live value), `afk.channel` / `afk.timeout`, the `widget` (as a whole), role
  `colors` / `icon` / `emoji`, and the channel extras `voiceRegion` (`automatic` = Discord
  picks), `videoQuality`, `threadArchiveAfter` (`1h|24h|3d|1w`), `threadSlowmode`, forum
  `tags` (as a whole, in key order; live tag IDs are kept by name, then by position, so a
  renamed tag stays the same tag), `defaultReaction`, `sortOrder`, `layout`; `guidelines`
  is the forum topic. A role without `colors` is solid: a live gradient is drift.
- `welcomeScreen` and `onboarding` are singletons compared as a whole (onboarding prompt
  and option IDs are kept by title so members' answers stay attached).
- `automod.<key>`, `emojis.<key>`, `stickers.<key>` and `soundboard.<key>` are keyed like
  roles: resource map (kinds `automod`, `emoji`, `sticker`, `sound`), adoption by name
  (AutoMod: name and trigger), `prune` / `deletionProtection` / `unmanage` for removed keys.
  The trigger of an AutoMod rule and the file of an expression cannot change in place
  (blocked: `type change` / `recreate`): declare a new key. These endpoints need Manage
  Server / Manage Expressions and are only called when the YAML declares the block (or
  removed keys of that kind are still mapped).

### Channel types
The type of a channel cannot change in place. When the live type differs from the YAML:
with `prune: true` and `deletionProtection: false` the channel is replaced (created, the old
one deleted at the end); otherwise the change is blocked (`type change: …`). Threads are
ignored.

### Ordering
- **Roles**: `hierarchy` lists role keys from highest to lowest; declared roles not listed
  follow, in key order. Managed roles permute among the slots they occupy in the live order;
  unmanaged roles keep their place; roles created by the plan start at the bottom. Only roles
  below the bot's highest role can be ordered, edited or deleted: a managed role at or above
  it is blocked (`hierarchy: …`).
- **Categories / channels**: categories among categories, channels within their category
  (channels without a category first): `position` ascending, a missing `position` after
  explicit ones, ties broken by key. Like Discord, text-like and voice-like (voice, stage)
  channels of a category are two separate position groups (voice below text).
- A `reorder` change (key `hierarchy`, `categories`, a category key, or `(none)` for
  uncategorized channels) is emitted only when the relative order of managed items differs.
  Apply recomputes positions from a fresh fetch, so the final order is right even if Discord
  placed new items differently than predicted.

### Permissions
- `everyone.permissions` sets the @everyone role.
- `overwrites` on a category or channel are keyed by target (`@everyone`, a role key or
  `member:<id>`); each target has `allow` and `deny`. A permission present in neither is
  *inherited*. Entries with empty `allow` and `deny` are ignored; order does not matter.
- Overwrites of a managed category/channel are owned **as a whole**: overwrites for targets
  not in the YAML are removed.
- `syncWithCategory` defaults to `true` when the channel has no `overwrites`, `false`
  otherwise. A synced channel gets exactly its category's desired overwrites, written
  explicitly (without a category: none).

### Apply order
Server basics and @everyone → roles (adopt/create/update) → role positions → categories →
channels → server channel references and Community toggle (system, AFK, rules, updates,
safety alerts channels) → channels that need Community → channel positions → widget →
welcome screen → onboarding → AutoMod rules → emojis/stickers/sounds → deletions (AutoMod,
expressions, channels, categories, roles) and `unmanage`. Apply continues past failures; changes that depend on a failed one
fail as `unresolved`.

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
  otherwise Discord refuses the gateway connection and the bot is reported `Missing` with
  the reason in its status message.
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

### Network egress (`http.fetch`)
`spec.network.allow` lists **public DNS hostnames** (optional port). The schema rejects IP
literals, single-label names (`localhost`), reserved/internal suffixes (`.local`,
`.internal`, `.arpa`, …) and wildcards on a bare TLD (`*.com`). Name checks alone are not
enough, so the runtime also enforces, on every request:
1. the hostname matches the allow-list (`*.example.com` matches subdomains, not `example.com`);
2. **after DNS resolution**, every resolved address is public: loopback, private (RFC 1918,
   `fc00::/7`), link-local (incl. `169.254.169.254` metadata), CGNAT, multicast,
   unspecified and IPv4-mapped equivalents are refused;
3. the connection is made to the **address that was checked** (no second lookup), which
   defeats DNS rebinding;
4. redirects are followed manually and each hop is re-checked against 1–3;
5. only `https`, no credentials in URLs, request bodies ≤ 1 MiB, responses ≤ 5 MiB, a 10 s
   default timeout.

`codi dev` relaxes the address checks (2–3) and allows plain `http`, so plugins can call
local services; the host allow-list still applies. `codi serve` never relaxes them.

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
Implemented by `internal/plugin` (the package documentation is the detailed reference).

- **Isolation**: one Sobek VM and one event loop per plugin instance (bot × server, ADR-0010).
  Handlers start in arrival order and interleave at `await` points (Node-like).
- **Budgets** (defaults):
  | Limit | Default | Effect |
  |---|---|---|
  | sync budget | 50 ms | One turn of synchronous JS (a handler's synchronous part, or the continuations run when a host call settles). A runaway turn (`while(true)`) is interrupted and its handler aborted; the VM keeps serving other handlers. |
  | startup budget | 2 s | Replaces the sync budget while the module is evaluated. |
  | handler timeout | 10 s | Each handler, wall clock, awaits included. Afterwards its pending host calls are cancelled and new ones fail. |
  | `onStop` | 3 s | Bound of `onStop`. |
  | `http.fetch` | 10 s / 5 MiB | Default timeout and response cap. |
- **Interactions**: an interaction not answered after **2 s** is deferred automatically
  (ephemeral when the command has `ephemeral: true`; components get a silent deferred
  update); a later `reply()` edits the deferred response. Rules:
  - `reply()` is the first response, or edits a deferred response; after a reply, use
    `followUp()` (a second `reply()` throws);
  - `defer()` after a deferral is a no-op; `followUp()` needs a prior response;
  - `update()` (components only) edits the message carrying the component;
  - `showModal()` must be the first response;
  - ephemerality is fixed by the first response (a reply after a deferral keeps its flags);
  - follow-ups are accepted for 15 minutes.
- **Component and modal ids**: the SDK namespaces local ids as `c:<instance>:<id>` (at most
  100 bytes in total, as Discord requires; a longer id is an error, never truncated). The
  handler is looked up by the full local id, then by the part before the first `:`
  (`"vote:42"` → `components.vote`), so ids can carry data.
- **Channel isolation**: every channel ID used by plugin code must belong to the instance's
  server, or be a DM channel the instance opened or received; a plugin cannot reach other
  servers the bot is in.
- **Capabilities**: every host call checks the instance's grants (§6) and throws a
  `CapabilityError` otherwise.
- **Errors**: an uncaught error in a handler is logged with the plugin instance and revision;
  for interactions, the user gets a generic ephemeral error message (or the deferred
  response is edited). Three consecutive failures mark the instance `Degraded` in the status.
- **References**: `ctx.server.channel("general")` resolves by key in the active revision;
  `{ id }` addresses unmanaged resources.
- **Config defaults**: `default` values from `config.schema.json` are applied before the
  plugin sees `ctx.config`: top level, nested objects, every object element of arrays and
  through local `$ref`s; not inside `additionalProperties` maps nor `oneOf`/`anyOf`
  alternatives. An absent optional object is materialized only when its defaults cover its
  `required` fields. A field with an applied default is always present.
- **Config typing**: `codi plugin typegen` generates a `PluginConfig` module augmentation
  from `config.schema.json`, with the same rules (fields that are required or have an
  applied default are non-optional).
- **Message content**: gated per plugin. Without the `MessageContent` intent in its own
  manifest, a plugin sees empty `content` and `attachments` (events and
  `channel.messages()`), except for the bot's own messages, messages mentioning the bot and
  DMs, even when another plugin of the same bot declares the intent.
- **Globals beyond the SDK**: `setTimeout` / `setInterval` / `clearTimeout` /
  `clearInterval`, `queueMicrotask` and `console.*` (mapped to the instance logger). Timers
  belong to the handler that created them and stop with it.
- **No Node.js APIs**: the bundle targets ES2022 (async generators lowered), `@codi/sdk` is
  the only module resolved at run time, Node built-ins cannot be imported; pure-JS libraries
  can be bundled. All I/O goes through `ctx`. Stack traces map to the TypeScript sources
  (inline source map).

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
