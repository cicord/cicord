---
title: Evaluate an existing server safely
description: Inspect a live Discord server with a View Channels bot before granting Cicord broad access. See what the bot can and cannot see, what it could not read, what applying changes would need, and the first plan, with no change on Discord.
---

Before Cicord manages a live server, you can look at what it would manage. The bot joins with
**View Channels** only, Cicord imports the server read-only and plans it, and you stop there
or grant more access. Nothing is changed on Discord.

## 1. Invite the bot with View Channels only

Create a bot as in the [quick start](../quick-start/), then invite it with the inspection
link: the wizard's **Inspect first** choice (Server step), or this URL with your application
ID:

```text
https://discord.com/oauth2/authorize?client_id=<application id>&scope=bot+applications.commands&permissions=1024
```

`1024` is View Channels. The bot can read the server, its roles and the channels it is allowed
in. It cannot change anything.

## 2. Inspect

From the command line, with the bot token in the environment:

```sh
DISCORD_TOKEN=… cicord inspect <server-id> ./pixel-pals
```

In the wizard, pick the server: the inspection runs on its own and shows the same report.

`cicord inspect` writes a new project into the directory (`cicord.yaml`,
`servers/<key>.yaml`, `bots/<bot>/bot.yaml`, icons and emojis) and the resource map into
`.cicord/state.db`. The wizard keeps nothing: it generates a temporary copy and deletes it.
Every Discord call goes through a read-only client, which refuses any change before it
reaches Discord.

Output on the [simulator](../setup-wizard/#offline-against-the-simulator)'s "Pixel Pals"
server, with the channel list filtered like Discord's (`…` marks lines and link parameters left
out here):

```text
Inspected "Pixel Pals" (1575138509038026753) as Cicord, read-only.
Configuration written to ./pixel-pals: cicord.yaml, servers/pixel-pals.yaml, bots/… and 4 other file(s);
resource map in .cicord/state.db.

Bot permissions: ViewChannel from its role, plus what @everyone allows (not Administrator).

Visible to the bot: 1 category, 4 channel(s).
  (no category): #general
  Community: #rules, #announcements, Lounge (voice)
Discord omits the channels a bot cannot view from the channel list: private channels the bot
is not allowed in may be missing above and are not in the configuration.

Not read (left out of the configuration, so Cicord does not manage them):
  Server widget: needs ManageServer
  Welcome screen: needs ManageServer
  AutoMod rules: needs ManageServer
  Grant the permission and inspect again to include them.

To apply changes, the bot would also need:
  Administrator: These roles grant Administrator, and a bot can only give permissions it holds. It is needed only to change their permissions: without it, Cicord manages everything else.
    needed by roles.admin.permissions
  BanMembers: A role or overwrite grants it, and a bot can only give permissions it holds.
    needed by roles.moderator.permissions
  …
  ManageChannels: Edit categories and channels and their order.
    needed by categories.community, channels.announcements, channels.general and 2 more
  ManageRoles: Edit roles, @everyone, the role order and permission overwrites.
    needed by everyone, roles.admin, roles.member and 1 more
  ManageServer: Edit the server settings, AutoMod, onboarding, the widget and the welcome screen.
    needed by afk, profile
  …
  View Channel and Manage Channels in each private channel to manage: in Discord, allow them to
    the bot's role in the channel's permissions, then add "@bot" to its overwrites in the YAML.
  Invite link with exactly these permissions: https://discord.com/oauth2/authorize?client_id=…&permissions=1800973575515767&…
  Invite link with Administrator:            https://discord.com/oauth2/authorize?client_id=…&permissions=8&…
  Role order: The role “Cicord” is above every role Cicord manages.

Plan (computed read-only):
Server pixel-pals: no changes

No changes. Discord matches the configuration.

Nothing was changed on Discord: 16 read call(s), 0 write calls.
You can stop here. To let Cicord manage the server, grant the permissions above, inspect again
into a new directory (the configuration then includes what the bot could not see or read),
and run `cicord plan` there.
```

## 3. Read the report

- **Visible to the bot.** The categories and channels the import covers. `[private]` marks
  one that `@everyone` cannot view but the bot can.
- **Hidden channels.** Discord leaves the channels a bot cannot view out of the channel list,
  so a private staff category does not appear at all, and the import does not include it.
  When the server refers to a hidden channel (the category of a visible channel, the system
  channel, an AutoMod alert channel), the report counts it with what refers to it. In that
  case the plan is not empty and `cicord inspect` exits with 1: do not apply it.
- **Not read.** Discord requires Manage Server to read the widget settings and AutoMod rules,
  and to read the welcome screen while it is disabled. These parts are left out of the YAML, so
  Cicord would not touch them.
- **To apply changes.** Each permission the bot lacks, why, and the YAML paths that need it. The
  list covers everything in the imported configuration. The sync preflight checks only what a
  plan changes. `Administrator` appears when a role grants it: a bot can only give permissions
  it holds, so it needs Administrator only to change that role's permissions.
- **Plan.** The imported configuration planned against the live server. It is empty: Cicord
  would adopt everything as it is.

## 4. Stop, or grant more

To stop, kick the bot from the server. Nothing was changed on Discord, and the wizard saved
nothing.

To continue, authorize one of the two links in the report for the same server. Authorizing
again updates the bot's role:

- **Exactly these permissions**: least privilege. Private channels stay hidden until you allow
  View Channel and Manage Channels to the bot's role in each one (see
  [Bot permissions](../../self-hosting/bot-permissions/#private-channels-the-bot-target)).
- **Administrator**: the fast path. The bot sees and manages everything, and so does anyone
  who holds its token.

Then:

- **Wizard.** Click **Re-check**. The report and the server checks run again, and **Continue**
  unlocks once the checks pass. The Configuration step imports the server again with the new
  access.
- **CLI.** Run `cicord inspect` again into a new directory, so that the configuration includes
  what the bot can now see and read, then run `cicord plan` there.
