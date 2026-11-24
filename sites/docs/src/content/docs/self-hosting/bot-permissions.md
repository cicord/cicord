---
title: Bot permissions
description: Run the bot with Administrator or with a least-privilege role, what Cicord needs in each case, and how to keep a bot without Administrator in private channels.
---

The setup wizard's invite link asks for **Administrator**. It is the simplest choice: Discord
skips every permission check for an administrator. It is also the most exposed one, because
anyone holding the bot token controls the whole server. A **least-privilege bot role** holds
only what your configuration and plugins use, so a leaked token can do less.

| | Administrator | Least-privilege role |
| --- | --- | --- |
| Setup | Invite link as is | Edit the bot's role after the invite |
| A leaked token can | Do anything on the server | Only what the role allows |
| New role permission or plugin in YAML | Works | Grant the new permission to the bot's role first |
| Private channels | Always visible | Need an `"@bot"` overwrite |
| `ManageRoles` in a channel overwrite | Works | Refused by Discord |

Choose **Administrator** if one of your channel overwrites allows or denies `ManageRoles`, or
if nobody will keep the bot's role in line with the YAML. Otherwise prefer a least-privilege
role.

## What Discord checks for a bot without Administrator

- Each call needs its permission: **Manage Roles** (roles, `everyone`, the role order,
  overwrites), **Manage Channels** (categories, channels, their order), **Manage Server**
  (profile, AFK, Community, widget, welcome screen, onboarding, AutoMod) and **Manage
  Expressions** (emojis, stickers, soundboard; uploading also works with Create Expressions).
- A bot can only **grant permissions it has itself**. A role with `BanMembers` or an overwrite
  allowing `ManageWebhooks` needs the bot to hold `BanMembers` or `ManageWebhooks`.
- Only an administrator can put **`ManageRoles` in a channel overwrite**.
- The bot only edits roles **below its own role**. The wizard checks this, and the plan
  reports the roles it cannot touch as blocked.
- The bot cannot see, edit or delete a channel where it lacks **View Channel**. A private
  category (`"@everyone": { deny: [ViewChannel] }`) hides itself from the bot too, unless an
  overwrite lets the bot in.

## What the bot needs

Cicord computes the list from your configuration:

| In the server YAML | The bot needs |
| --- | --- |
| `roles.<key>` | Manage Roles, plus every permission in its `permissions` |
| `everyone` | Manage Roles, plus every permission in its `permissions` |
| `categories` / `channels` | View Channel and Manage Channels |
| `overwrites` | Manage Roles, plus every permission they allow or deny (`ManageRoles` itself: Administrator) |
| `profile`, `afk`, `community`, `widget`, `welcomeScreen`, `automod` | Manage Server |
| `onboarding` | Manage Server and Manage Roles |
| `emojis`, `stickers`, `soundboard` | Manage Expressions |

Plugins add what their **granted** capabilities use:

| Grant | The bot needs |
| --- | --- |
| `discord.messages.read` | View Channel, Read Message History |
| `discord.messages.send` | View Channel, Send Messages, Embed Links, Attach Files |
| `discord.messages.manage` | View Channel, Manage Messages |
| `discord.reactions.manage` | View Channel, Add Reactions, Read Message History |
| `discord.members.manage` | Manage Nicknames |
| `discord.members.moderate` | Timeout Members, Kick Members, Ban Members |
| `discord.roles.assign` | Manage Roles |
| `discord.channels.read` | View Channel |
| `discord.channels.manage` | View Channel, Manage Channels, Manage Roles |
| `discord.threads.manage` | View Channel, Create Public Threads, Create Private Threads, Manage Threads, Send Messages in Threads |

The other grants (`discord.members.read`, `discord.dm.send`, storage, scheduler, events,
portal, `http.fetch`) need no permission on the server.

## How Cicord checks

- **Verify step and Diagnostics.** The _Permissions_ check compares the bot's role with the
  exact list of the server (configuration and plugins) and says what needs each missing
  permission, for example `BanMembers (roles.moderator.permissions)`. With Administrator, it
  shows the list the bot would need without it. Before a configuration exists, the wizard
  checks a default list (server management plus what the official plugins use).
- **Before every plan and sync.** When the bot is not Administrator, Cicord checks what the
  plan is about to send before applying anything. If a permission is missing, nothing is
  applied and `cicord plan`, `cicord apply`, the admin _Sync_ page and the server status show
  an error such as:

  ```text
  reconcile ma-commu: preflight: the bot lacks permissions this sync needs: BanMembers
    needs BanMembers (roles.moderator.permissions)
  ```

  Only changes are checked. A role that already has `BanMembers` on Discord does not block a
  topic edit, but changing that role's permissions does.
- The same check refuses a plan that would leave the bot unable to see or manage a channel it
  manages. See the next section.

## Private channels: the `@bot` target

Give the bot's own role access in the overwrites of every private category or channel (staff,
mod-log, tickets…):

```yaml
categories:
  staff:
    name: Staff
    overwrites:
      "@everyone": { deny: [ViewChannel] }
      mod: { allow: [ViewChannel, SendMessages] }
      "@bot": { allow: [ViewChannel] }
```

- `"@bot"` is the role Discord created for the bot when it was invited (named after the bot,
  marked as managed). A bot invited without any permission has no such role: `"@bot"` then
  becomes an overwrite for the bot user. `"member:<bot user id>"` also works.
- Channels with `syncWithCategory` inherit it from their category.
- Allow what the bot does in the channel, such as `SendMessages` for a log channel. **Never
  `ManageRoles`**: only an administrator can set it in an overwrite, and the bot keeps it from
  its role anyway.
- Without `"@bot"`, the first sync would create the channel and every later sync would fail
  with _Missing Access_. The preflight refuses it with the channel's path
  (`the bot cannot see or manage categories.staff`). For a channel the bot already cannot see,
  Cicord cannot fix the overwrite itself: in Discord, add the bot's role to the channel with
  View Channel, then add `"@bot"` to the YAML.
- `cicord import` writes `"@bot"` for the overwrites of the bot's own role.

Channels created by plugins are not in the YAML: the plugin must give the bot access
itself, see [plugin-owned channels](../../plugins/api/#plugin-owned-channels).

## Moving an existing server off Administrator

1. While the bot is still Administrator, add `"@bot"` to the overwrites of every private
   category and channel, and sync.
2. Open **Diagnostics** in the admin. The _Permissions_ check lists what the bot would need
   without Administrator.
3. In Discord, open **Server Settings → Roles** and edit the bot's role (it has the bot's name).
   Turn off **Administrator** and turn on every permission from step 2. Keep the role above
   every role Cicord manages.
4. Re-run Diagnostics and `cicord plan`. A missing permission is named with the YAML path that
   needs it.
5. When you add a role permission, an overwrite or a plugin grant later, give it to the bot's
   role first, or the next sync stops at the preflight.

## Limits

- `ManageRoles` in a channel overwrite always needs Administrator.
- The preflight checks server-level permissions and the channel overwrites Cicord manages. A
  failure it cannot predict, such as a Discord limit, is still reported change by change
  during the sync.
- Plugin requirements come from the grants. A plugin that creates private channels must give
  the bot access to them itself.
