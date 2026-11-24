---
title: Reconciliation
description: How Cicord plans and applies changes, adopts existing resources, decides what it owns, handles removed keys, explains blocked changes, and detects drift.
---

The **server reconciler** turns the YAML of a server into Discord state. It runs inside the
runtime on every new revision and whenever drift is detected, and behind `cicord plan` and
`cicord apply` on the command line.

## Plan and apply

A **plan** is the ordered list of changes between the YAML and the live server. Its actions
are `create`, `update`, `delete`, `adopt`, `reorder` and `unmanage`. Operations Cicord refuses
are reported separately as **blocked**, with a reason; they never fail the plan.

**Apply** runs the plan in a fixed order, aware of Discord's rate limits:

1. server basics and `@everyone`;
2. roles (adopt, create, update), then role positions;
3. categories, then channels;
4. server channel references and the Community toggle (system, AFK, rules, updates and
   safety alerts channels), then channels that need Community;
5. channel positions, widget, welcome screen, native onboarding;
6. AutoMod rules, then emojis, stickers and soundboard sounds;
7. deletions (AutoMod, expressions, channels, categories, roles) and `unmanage`.

Apply continues past failures. Changes that depend on a failed one fail as `unresolved`, and
the next sync resumes. A failed apply on one server does not block the others.

In production you never run apply yourself: the runtime applies every revision it
activates. `cicord apply` exists for development and bootstrapping.

## Lifecycle of a revision

```text
commit ─▶ fetch ─▶ validate ─▶ build ─▶ plan ─▶ apply servers ─▶ activate bots ─▶ report
```

- The **active revision** only changes when validation and the plugin build succeed. A
  rejected revision leaves the previous one running.
- Bots are **hot-swapped**: new plugin VMs start, are validated, then traffic switches per
  bot. There is no gateway reconnect and no lost event; plugin storage persists across
  revisions.
- Every activation, rejection and server sync is recorded in the **sync history** (admin
  console, `GET /api/v1/history`).

Each server and bot reports a status, ArgoCD style:

| Sync | Health |
|---|---|
| `Synced`, `OutOfSync`, `Unknown` | `Healthy`, `Progressing`, `Degraded`, `Missing` |

## Adoption by name

Pointing Cicord at an existing server does not duplicate it. A declared key without a mapping
**adopts** an unmapped live resource of the same kind (role, category, or channel of the same
type) with exactly the same display name. Differing fields are updated by the same change.

- Text-like channel names are compared after Discord's normalization (lowercase, runs of
  whitespace become `-`): `name: Général Chat` matches `#général-chat`.
- A live resource is adopted at most once. Resources already mapped, managed roles
  (bot and integration roles) and `@everyone` are never candidates.
- Mappings are written as soon as a create succeeds, so a resource created by a crashed
  apply is simply adopted on the next run.
- Declared keys are processed in key order, and candidates in Discord ID order.
- A display name change is an `update` (a rename), never a recreate. A key whose mapped ID no
  longer exists in Discord is created again, and its mapping replaced.
- Changing a key while keeping the display name and kind keeps the Discord resource: the plan
  shows an `adopt` under the new key and an `unmanage` (`move … mapping`) for the old one. See
  [Keys and display names](../repository-layout/#keys-and-display-names).

The setup wizard and `cicord import` rely on this: the first plan after an import is empty.
After losing the state database, `cicord adopt` rebuilds the local resource map by name, with
confirmation.

## What Cicord owns

- **Only mapped resources are ever modified.** Roles, channels and categories that were
  never declared are ignored.
- **Plugin-owned resources** (for example ticket channels created by the `tickets` plugin)
  are recorded as owned by that plugin instance. The reconciler never touches or prunes
  them, even with `prune: true`.
- **Managed roles** (bot and integration roles) are never touched. `@everyone` is only
  changed through `spec.everyone`.
- **Permission overwrites** of a managed category or channel are owned as a whole:
  overwrites for targets that are not in the YAML are removed.
- **Plugin-owned mappings**: a YAML key whose mapping belongs to a plugin is blocked
  (`ownership:`).

## Removed keys: prune and deletion protection

`spec.syncPolicy` of each server decides what happens when a managed key disappears from the
YAML:

| `prune` | `deletionProtection` | Result |
|---|---|---|
| `false` (default) | any | `unmanage`: the mapping is dropped, the Discord resource is left as is. |
| `true` | `true` (default) | Blocked (`deletionProtection`); the mapping is kept, so turning protection off later deletes it. |
| `true` | `false` | `delete`. |

`unmanage` is also used, whatever the policy, when the removed resource is already gone from
Discord. Deletions always run last: channels, then categories, then roles (after AutoMod rules
and expressions), and a delete only drops the mapping when it still points to the deleted ID. `cicord validate` warns when `prune: true` is combined with
`deletionProtection: false`.

## Blocked changes and their explanations

Cicord refuses, ahead of time, what Discord would refuse at apply time, and says why. The admin
console and `cicord plan` explain each reason:

| Reason | Example |
|---|---|
| `boost:` | `banner requires boost level 2; server is level 0 (0 boost(s))`. Banners, invite splash, animated icons, role icons and gradient role colors are boost-gated. |
| `community:` | Announcement and stage channels, rules/updates/safety alerts channels, the welcome screen and native onboarding need the Community feature. |
| `limit:` | Emoji, sticker and sound slots of the boost level; at most 6 keyword AutoMod rules and 1 of each other trigger. |
| `deletionProtection` | A removed key while `prune: true` and protection is on. |
| `hierarchy:` | A managed role at or above the bot's highest role. |
| `ownership:` | A YAML key whose mapping belongs to a plugin. |
| `managed:` | A declared role that is a bot or integration role. |
| `type change:` | A channel whose type differs from the live one (replaced only with `prune: true` and `deletionProtection: false`). |
| `recreate` | The trigger of an AutoMod rule or the file of an emoji cannot change in place: declare a new key. |

Blocked items keep the server `OutOfSync`, and are applied by the first sync after the
condition is met (the boost level is reached, Community is enabled…). A blocked change carries
the `fields` that could not be applied, when known.

A channel whose type changed is **replaced** when `prune: true` and
`deletionProtection: false`: the new channel is created, and the old one deleted at the end of
the apply. Otherwise the change is blocked (`type change:`).

## Ordering

- **Roles**: `hierarchy` lists role keys from highest to lowest; declared roles that are not
  listed follow, in key order. Managed roles permute among the slots they occupy in the live
  order; unmanaged roles keep their place. Roles created by a plan start at the bottom. Only
  roles below the bot's highest role can be ordered, edited or deleted: a managed role at or
  above it is blocked (`hierarchy:`).
- **Categories and channels**: categories among categories, channels within their category
  (channels without a category first), by `position` ascending (missing positions after
  explicit ones, ties broken by key). Like in Discord, text-like and voice-like (voice,
  stage) channels of a category are two separate groups, voice below text.
- A `reorder` change (key `hierarchy`, `categories`, a category key, or `(none)` for the
  channels without a category) is emitted only when the relative order of managed items
  differs. Apply recomputes positions from a fresh fetch, so the final order is right even if
  Discord placed new items differently than predicted.

## What is compared

- **Always managed**, the YAML zero value included: role name, color (none when unset),
  hoist, mentionable and permissions; category and channel name and overwrites; channel
  `topic` (text, announcement, forum, media), `nsfw`, `slowmode` (text, voice, stage, forum,
  media) and `userLimit` (voice, stage).
- **Managed only when set**: channel `bitrate` (above 0), each `profile` field, the server
  icon, banner and splash, `everyone` (when present), each `systemChannelFlags` toggle
  (undeclared toggles keep their live value), `afk`, the `widget` (as a whole), `community`,
  role `colors`, `icon` and `emoji`, and the channel extras (`voiceRegion`, `videoQuality`,
  `threadArchiveAfter`, `threadSlowmode`, forum `tags`, `defaultReaction`, `sortOrder`,
  `layout`). A role without `colors` is solid: a live gradient is drift.
- **Images** (server icon, banner and splash, role icons) are files relative to the server
  YAML. Discord only exposes URLs, so the reconciler remembers the hash of the last uploaded
  file and the URL seen after the upload, and uploads again when either changes.
- **Forum tags** are compared as a whole, in key order; live tag IDs are kept by name, then by
  position, so a renamed tag stays the same tag.
- **`welcomeScreen` and `onboarding`** are singletons compared as a whole. Onboarding prompt
  and option IDs are kept by title, so members' answers stay attached.
- **AutoMod rules, emojis, stickers and soundboard sounds** are keyed like roles: resource
  map, adoption by name (AutoMod: name and trigger), `prune`, `deletionProtection` and
  `unmanage` for removed keys. The trigger of a rule and the file of an expression cannot
  change in place (blocked: `type change` or `recreate`): declare a new key. Their Discord
  endpoints need Manage Server or Manage Expressions and are only called when the YAML
  declares the block, or when removed keys of that kind are still mapped.

## Permissions

- `spec.everyone.permissions` sets the `@everyone` role.
- `overwrites` are keyed by target: `@everyone`, `@bot`, a role key or `member:<id>`. Entries
  with empty `allow` and `deny` are ignored, and the order does not matter. `@bot` resolves to
  the managing bot's own managed role, or to a member overwrite for the bot user when it has
  none.
- A channel with `syncWithCategory` gets exactly its category's desired overwrites, written
  explicitly (a channel without a category gets none). `syncWithCategory` defaults to `true`
  when the channel has no `overwrites`.
- **Permission preflight**: when the bot is neither Administrator nor the owner, the plan
  checks what it would send the way Discord does, before anything is applied: the
  permissions each call needs, the permissions a role gains, every overwrite bit that changes
  (Discord only lets a bot grant what it holds, and only administrators put `ManageRoles` in
  an overwrite), and that the bot keeps View Channel and Manage Channels in every channel it
  edits. A failing plan names the missing permissions and their configuration paths, and
  nothing is applied. Unchanged resources are not checked. See
  [Bot permissions](../../self-hosting/bot-permissions/).

## Drift and self-heal

The bot host forwards gateway events (`CHANNEL_UPDATE`, `ROLE_DELETE`, server updates,
AutoMod, emoji, sticker and soundboard events…) on managed resources to the reconciler, so a manual change in Discord is detected
in real time. Per server, `syncPolicy.selfHeal` decides:

- `false` (default): the drift is **reported**; the server shows `OutOfSync`.
- `true`: the drift triggers a debounced sync that **reverts** it.

The widget, the welcome screen and native onboarding have no Discord events: changes to them
are only seen at the next plan.

## Moving the resource map

Keys are mapped to Discord IDs in the runtime's database, never in git. The command line
keeps its own map in `.cicord/state.db` (used by `cicord plan`, `cicord apply`, `cicord import`,
`cicord adopt` and `cicord mv`). If the CLI and a runtime apply to the same servers without
sharing that database, the runtime simply adopts the resources by name on its next sync.
