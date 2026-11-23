---
title: Plugins overview
description: Using plugins - sources and the Store, trust levels, the permission review, installing, updating and uninstalling, and codi.lock.
---

Everything a CODI bot *does* comes from plugins: greeting members, tickets, polls, levels,
moderation… A plugin is a small TypeScript module with a static manifest. It runs sandboxed
inside the runtime, one **instance** per (bot, server), and can only do what its grants
allow.

## Sources and trust

Plugins are installed from **sources**, like apt sources or Homebrew taps:

| Source | What it is | Trust |
|---|---|---|
| **`codi`** | The [official plugins](../official/) bundled with the binary: welcome, onboarding, tickets, polls, levels, moderation, reaction-roles, audit-log, announcements. Always available, no configuration. | `official` |
| **Registries** | Git repositories with an `index.yaml`, declared in `codi.yaml` `spec.registries`. Community collections, your organization's private plugins, your own development registry. | `unofficial` |
| **Local plugins** | A directory of your repository (`plugins/<name>/`), referenced by path. | `local` |

Trust is **computed from the URL, never declared**: a registry can't call itself official. The
admin console labels unofficial sources everywhere and asks for an explicit confirmation,
restating the capabilities and network access being granted, before installing from them.

:::note[Official registry]
A signed official registry is designed and implemented (its plugins would join source `codi`
automatically), but its repository and signing key do not exist yet. Until then, `codi` is
the plugins bundled in your binary. See [Publishing](../publishing/#the-official-registry).
:::

## The Store

The admin console's **Store** page lists every source and its plugins, with search and trust
badges. Selecting a plugin shows what it requests before anything is installed:

- its **capabilities**, in plain words;
- its gateway **intents**, privileged ones highlighted;
- the **websites** it may call (`http.fetch` allow-list);
- its slash commands (for official plugins; an unofficial plugin's code never runs before
  you install it, so only its `plugin.yaml` is read).

Install then asks for the bot, the instance name and the configuration (a form generated from
the plugin's schema, with pickers for channels and roles). The installed code is exactly the
reviewed version: if a new release appears between the review and the click, the Store asks
you to review again.

Under **Sources**, admins add or remove registries (`https://` only in the admin console),
including private ones with a token. Adding or removing a source changes `codi.yaml`, so it
needs the admin role on every server.

## Installing from the command line

```sh
codi registry add acme https://github.com/acme/codi-plugins.git
codi registry search xp
codi plugin add acme/xp --bot moderator
```

`codi plugin add <registry>/<plugin>[@<range>]` adds an instance to the bot (`--name` sets the
instance key, default the plugin name) and locks the version in `codi.lock`. It works with
the registries declared in `codi.yaml`; the bundled official plugins are installed from the
setup wizard or the Store (`codi plugin add codi/<name>` needs the official registry, which does
not exist yet).

Installing a **bundled** official plugin from the Store or the setup wizard copies its sources
to `plugins/<name>/` and adds a local instance; installing from a registry adds
`source: <registry>/<plugin>` with a `version` range and a `codi.lock` entry. Either way, the
change is one [write-back](../../concepts/write-back/) commit or pull request, and every
capability the plugin requests is granted, as reviewed.

## Updating

CODI compares each locked version with the highest release tag of the plugin's repository
(any major version) and shows **Update available** in the Store, the Plugins table and the
plugin's page. The update review shows:

- what the new version requests compared with the installed one (capabilities, intents,
  network hosts);
- what changes for each instance (version range, grants);
- whether the current settings are still valid with the new config schema.

Updating rewrites the lock entry, widens the instances' version range when needed
(`^1.0.0` → `^2.0.0`) and sets their grants to what the new version requests, for every
instance at once (the version is shared), so it needs the admin role on every server where the
plugin runs.

```sh
codi plugin update                       # list available updates
codi plugin update acme/xp               # update to the latest version, print the capability diff
codi plugin update acme/xp --to ^2.0.0   # or to a range (written to every instance)
codi plugin update codi/welcome          # a copy of an official plugin, from this binary
```

Copies of official plugins in `plugins/<name>/` update from the binary: a newer CODI may bundle
a newer version. A copy you changed is not overwritten unless you force it (admin console
checkbox, or `--force`).

## Uninstalling

From a plugin's page in the admin console, **Uninstall** removes the instance from the bot
(only this server, when the instance runs on several). When no other instance uses the
plugin, its `codi.lock` entry or its copied `plugins/<name>/` directory goes too. Its stored
data is kept.

## `codi.lock`

`codi.lock` pins every registry plugin. It is generated (`codi plugin add`,
`codi plugin update`, the Store); do not edit it by hand.

```yaml title="codi.lock (example)"
lockVersion: 1
plugins:
  codi/welcome:
    version: 1.2.3
    repo: https://github.com/codi-plugins/welcome
    commit: 4f0c2d9e8b1a7c6d5e4f3a2b1c0d9e8f7a6b5c4d
    integrity: sha256-Vb3nQ0x2...
    capabilities: [discord.messages.send, discord.members.read, storage.kv]
```

- The runtime refuses a registry plugin that is missing from `codi.lock`, or whose content
  does not match `integrity`.
- Because the capabilities are recorded, a permission change of a plugin shows up in the diff
  of a pull request.
- Local plugins are not locked: they are part of your repository.

## Configuring plugins

Instances are declared in `bots/<bot>/bot.yaml` (see [Bot](../../configuration/bot/#plugin-instances)).
Their settings are also editable in the admin console, on pages generated from each plugin's
`config.schema.json`: admins edit every field, moderators the fields the plugin marks
`x-codi-access: moderator`. Saves are written back like any other change.
