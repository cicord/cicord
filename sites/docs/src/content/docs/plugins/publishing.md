---
title: Publishing plugins
description: Release a plugin with cicord plugin publish, list it in a git registry's index.yaml, run private registries, and how the signed official index works.
---

Cicord has no plugin server: a **plugin** is a git repository with semver tags (or a
directory of a repository that holds several plugins), and a **registry** is a git repository
with an `index.yaml` that lists plugins, like Go modules or Homebrew taps.

## Release a plugin

1. Put the plugin in its own git repository, with `plugin.yaml` at the root.
2. Set `metadata.version` in `plugin.yaml` to the version to release, and commit.
3. Check everything without changing anything:

   ```sh
   cicord plugin publish --dry-run
   ```

4. Release it and update a local checkout of the registry:

   ```sh
   cicord plugin publish --push --registry ../cicord-plugins --publisher acme
   ```

5. Commit the registry change there and open a pull request.

### Several plugins in one repository

A repository can hold several plugins, one directory each (`plugins/xp/plugin.yaml`), like
the [official registry](#the-official-registry). Each plugin is released with its own tags,
`<name>/v<semver>` (`xp/v1.2.0`), and its index entry names its directory with `path`:

```sh
cicord plugin publish plugins/xp --push --registry ../cicord-plugins
# or from the repository root
cicord plugin publish --path plugins/xp --push --registry ../cicord-plugins
```

Only that directory must be committed, so the index of a repository that is also its own
registry (`--registry .`) can stay uncommitted while you publish several plugins in a row.

`cicord plugin publish [plugin-dir]`:

1. builds and inspects the plugin like `cicord plugin build` (any error stops it);
2. refuses a dirty working tree (the tag must point at the validated code), a `metadata.name`
   that is not a valid registry key, and a version that is not a semver greater than every
   earlier release tag of the plugin (local tags; with `--push`, the remote's too);
3. creates the annotated tag `v<version>` on `HEAD` (`<name>/v<version>` for a plugin in a
   subdirectory, with `path` in its entry), and pushes it with `--push` (to `--remote`,
   default `origin`, with your git credentials);
4. computes the index entry: `repo` from `--repo` or the remote URL (`git@host:a/b.git`
   becomes `https://host/a/b.git`; URLs with credentials are refused), `displayName`,
   `description` and `keywords` from `plugin.yaml`, `publisher`, `version` and `commit`.
   With `--registry <dir>` it adds or updates the entry in that `index.yaml`, keeping
   comments and the fields it does not manage; an existing entry keeps its `verified` flag
   and `publisher`, and its `repo` and `path` only change with `--replace-repo`. Without `--registry`
   (or with an https URL) it prints the entry and the steps.

Everything is checked before the tag is created. `publish` never sets `verified`: registry
maintainers do. If the registry's index is signed, it warns that the signature is now stale.

:::note
`cicord plugin publish` validates the working tree it runs in. Building from the tagged tree is
planned.
:::

You can also tag releases by hand (`v1.2.0`, or `xp/v1.2.0` in a monorepo) and edit
`index.yaml` yourself.

## The registry index

A registry is a git repository with `index.yaml` at its root:

```yaml title="index.yaml"
plugins:
  xp:
    repo: https://github.com/acme/cicord-xp.git    # plugin repository, semver tags v1.2.0
    # path: plugins/xp                           # monorepo: plugin directory, tags xp/v1.2.0
    displayName: XP & levels                     # optional
    description: Levels and a portal leaderboard
    keywords: [xp, levels]
    publisher: acme
    verified: false                              # only honoured from the signed official index
    version: 1.2.0                               # optional: latest release (written by publish)
    commit: 3f1c…                                # optional: its tag's commit (a moved tag is refused)
```

With `path`, only that directory is downloaded and hashed (the hash in `cicord.lock` is the same
as for the plugin in its own repository), and only the tags `<name>/v<semver>` are releases. A
`path` is a relative directory: no `..`, no leading `/`, no hidden segments.

An `https` registry may only list `https` plugin repositories. Users add it and install from
it:

```sh
cicord registry add acme https://github.com/acme/cicord-plugins.git
cicord registry search xp
cicord plugin add acme/xp --bot moderator
```

or from the admin console: **Store** → Sources → Add, then Install on a plugin. Installing pins
the version in `cicord.lock` (commit and content hash) and adds `source: acme/xp` with
`version: ^1.2.0` to the bot.

To ship an update, publish a new tag: Cicord compares each locked version with the highest
release tag of the plugin repository and offers the update. Keep the settings of a minor
version compatible (a new required field makes every existing instance invalid, and the
update is refused until admins fix it) and release breaking changes as a new major version.
New capabilities, intents or network hosts are shown to admins before they update.

`cicord registry list|add|remove|update|search` manages sources from the command line; there
`file://` URLs and local paths are allowed, for development registries (the admin console
only accepts `https://`).

## Private registries

A registry can require an access token (a private GitHub or GitLab repository). Its secret is
always named `CICORD_REGISTRY_<NAME>_TOKEN`, after the registry's own name, so that a
`cicord.yaml` edit can never send another secret (a bot token, another registry's token) to a
registry host.

```sh
CICORD_REGISTRY_ACME_TOKEN=… cicord registry add acme https://github.com/acme/private-plugins.git --private
```

This writes `secretRef: CICORD_REGISTRY_ACME_TOKEN` to `cicord.yaml` (only the name, never the
token). On the runtime, provide the token as an environment variable or with
`cicord secret set CICORD_REGISTRY_ACME_TOKEN`, or paste it in the admin console (Store →
Sources), which stores it encrypted. The token is sent, as HTTP basic auth over https, to the
registry's host only (its index and the plugin repositories on that host).

By default the Store and plugin downloads refuse private network addresses; set
`CICORD_ALLOW_PRIVATE_REGISTRIES=true` on the runtime to allow a registry on your internal
network.

## The official registry

The official registry is [github.com/cicord/cicord-plugins](https://github.com/cicord/cicord-plugins),
a monorepo maintained by the Cicord project: one directory per plugin (`plugins/<name>/`,
tags `<name>/v<semver>`) and a **signed** `index.yaml` at its root. Its plugins join source
`cicord`, without any `cicord.yaml` entry, and only it can mark publishers as `verified`.
Community plugins are submitted there by pull request (see its `CONTRIBUTING.md`).

:::caution[Not live yet]
The production signing key is not embedded in current builds yet. Such a build cannot verify
the official index, so it does not fetch it: source `cicord` lists the plugins bundled in your
binary, with a notice. The bundled copies stay in every build, for offline and first installs.
:::

`index.yaml.sig`, next to the index, is a signature envelope followed by one ed25519
signature per signing key:

```text title="index.yaml.sig"
# Signatures of index.yaml (cicord registry sign). Do not edit.
format: cicord-registry-signature-v1
registry: https://github.com/cicord/cicord-plugins   # normalized URL of the registry
serial: 1764000000                               # increases with every signature
expires: 2026-12-23T00:00:00Z                    # the signature is refused after this
index-sha256: 9f86d081…                          # SHA-256 of the exact bytes of index.yaml
ed25519 <key-id> <base64 signature>
```

A runtime only uses an index read from an official URL when a signature by one of the official
public keys embedded in the binary verifies and the hash matches, `registry` is the official
URL, it has not expired, and `serial` is not lower than the highest serial this runtime has
already seen (stored in its state database, so a replayed older index is refused). Otherwise
the source is an **error** source: nothing is listed or installable from it, and the bundled
plugins remain.

When a plugin is both bundled and in the official registry, the registry version is used only
if it is a valid semver **greater** than the bundled one. Installing it locks it in
`cicord.lock` as `cicord/<name>`.

### For registry maintainers

| Command | Purpose |
|---|---|
| `cicord registry keygen --out <file>` | Create an ed25519 key pair. The private key file is mode 0600 and never overwritten; the public key is printed. |
| `cicord registry sign <dir> --key <file\|env:NAME> --registry-url <url>` | Write `index.yaml.sig`. The serial defaults to the current Unix time; the validity to 30 days (`--expires-in`, at most 180 days). Repeat `--key` to sign with several keys during a rotation. |
| `cicord registry verify <dir> [--public-key <b64>] [--registry-url <url>]` | Check the signature, hash, registry and expiry, for the registry's CI. |

Sign again after every change to `index.yaml`, **and on a schedule** (for example a weekly CI
job), so the signature never expires; a lapsed signature makes runtimes stop listing the
registry's plugins until it is signed again. Serials only go up: `sign` refuses a `--serial`
that is not above the current one.
