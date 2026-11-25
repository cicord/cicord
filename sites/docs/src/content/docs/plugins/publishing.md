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

   ```text
   ✓ hello 0.1.0 builds (1 command(s), 2.2 KiB bundle)
   ✓ 0.1.0 is a new version (0 earlier release(s) in local tags)
   • would tag v0.1.0 at 1e8b48262c66
   Registry index entry (plugins: in the registry's index.yaml):
     hello:
       repo: https://github.com/acme/cicord-hello.git
       description: Says hello
       version: 0.1.0
       commit: 1e8b48262c669b3191087e909ddee12c5f307af4
   ```

4. Release it and update a local checkout of the registry:

   ```sh
   cicord plugin publish --push --registry ../acme-registry --publisher acme
   ```

5. Commit the registry change there and open a pull request.

To get a plugin into the official registry, open a pull request that adds `plugins/<name>/`
to [cicord/cicord-plugins](https://github.com/cicord/cicord-plugins) instead (its
`CONTRIBUTING.md` lists the review rules): maintainers release it and edit `index.yaml`.

### Several plugins in one repository

A repository can hold several plugins, one directory each (`plugins/xp/plugin.yaml`), like
the [official registry](#the-official-registry). Each plugin is released with its own tags,
`<name>/v<semver>` (`xp/v1.2.0`), and its index entry names its directory with `path`:

```sh
cicord plugin publish plugins/xp --push --registry ../acme-registry
# or from the repository root
cicord plugin publish --path plugins/xp --push --registry ../acme-registry
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
   comments and the fields it does not manage; an existing entry keeps its `verified` flag,
   its `publisher` unless `--publisher` is given, and its `repo` and `path` unless
   `--replace-repo` is given. Without `--registry`
   (or with an https URL) it prints the entry and the steps.

Everything is checked before the tag is created. `publish` never sets `verified`: registry
maintainers do. If the registry's index is signed (`index.yaml.sig`), it warns that the
signature is now stale: the maintainers sign again after merging.

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
`path` is a relative directory of at most 255 characters that contains `plugin.yaml`: no `..`,
no leading or trailing `/`, no hidden segments.

Plugin keys match `[a-z0-9][a-z0-9-]{0,63}`. Tags other than `v<semver>` (or `<name>/v<semver>`
in a monorepo) are ignored, and prereleases are only resolved by version ranges that admit
them.

An `https` registry may only list `https` plugin repositories. Users add it and install from
it:

```sh
cicord registry add acme https://github.com/acme/cicord-plugins.git
cicord registry search xp
cicord plugin add acme/xp --bot moderator
```

or from the admin console: **Store** → Sources → Add, then Install on a plugin. Installing pins
the version in `cicord.lock` (commit, content hash, and `path` for a monorepo) and adds `source: acme/xp` with
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

The ten [official plugins](../official/) live there, each released with its own tags
(`welcome/v0.1.0`). Install one with `cicord plugin add cicord/<name>`, which locks it in
`cicord.lock` like any registry plugin. The binary embeds the registry's public signing key;
`cicord registry list` shows the source as:

```text
NAME    TRUST     URL                                                                              TOKEN
cicord  official  (bundled with cicord) + https://github.com/cicord/cicord-plugins (signed index)  -
```

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

Each signature covers the canonical envelope (the five fields, in this order, as `name: value`
lines after the format line), so it binds the index content to one registry, one serial and
one expiry. A runtime only uses an index read from an official URL when:

- a signature by one of the official public keys embedded in the binary (a list, for key
  rotation) verifies, and the hash matches `index.yaml`;
- `registry` is the official registry's URL (a signature made for another registry, such as a
  staging copy, is refused);
- it has not expired;
- `serial` is not lower than the highest serial this runtime has already seen for the official
  registry. This is trust on first use: the serial is stored in the runtime's state database,
  so it survives restarts (the CLI keeps it next to its plugin cache), and all official URLs
  (aliases, mirrors) share it. An older, validly signed index replayed by a mirror or an
  attacker, through any official URL, is refused.

Otherwise the source is an **error** source (`signature: "invalid"`): nothing is listed or
installable from it, the bundled plugins remain, and it is never downgraded to an unofficial
source with its `verified` flags. `verified: true` (a verified publisher) is only honoured from
a verified official index; in any other registry it is ignored.

### Source `cicord` and the bundled plugins

The official registry needs no `cicord.yaml` entry: its plugins are part of source `cicord`
(the name is reserved), merged with the nine plugins bundled in the binary (every official
plugin except github-releases), for offline and first installs. When a plugin is in both, the
registry version is used only if its index `version` is a valid semver **greater** than the
bundled one; otherwise the bundled copy is used. Installing a bundled plugin (the setup
wizard always does) copies it to `plugins/<name>/`; installing one from the official
registry locks it in `cicord.lock` as `cicord/<name>`, like `cicord plugin add
cicord/<name>`.

If the official index cannot be read or verified, `cicord` still lists the bundled plugins
and shows the error. A build that embeds no official public key cannot verify the index: it
does not fetch it at all, and `cicord` lists the bundled plugins with an explanation (the
Store shows no signature badge; `cicord registry update` prints an informational line and does
not fail). The bundled copies remain in every build, for offline and first installs.

**Development builds.** A development build embeds a test key at build time
(`-ldflags "-X github.com/cicord/cicord/internal/registry.devOfficialKey=<base64>"`); only such
a build honours `CICORD_DEV_OFFICIAL_REGISTRY=<url>`, which points the official registry at
another URL (its index must be signed for that URL). Release builds ignore the variable:
nothing in the environment, `cicord.yaml` or the admin console can change what is official.

### For registry maintainers

| Command | Purpose |
|---|---|
| `cicord registry keygen --out <file>` | Create an ed25519 key pair. The private key file is mode 0600 and never overwritten (keep it out of any repository); the public key to embed in the runtime (`internal/registry` `OfficialKeys`) is printed. |
| `cicord registry sign <dir> --key <file\|env:NAME> [--registry-url <url>]` | Write `index.yaml.sig` (`env:NAME` reads the key from an environment variable, such as a CI secret); commit it together with `index.yaml`. `--registry-url` defaults to the official registry. The serial defaults to the current Unix time (always above the previous one); the validity to 30 days (`--expires-in`, at most 180 days). Repeat `--key` to sign with several keys during a rotation. |
| `cicord registry verify <dir> [--public-key <b64>] [--registry-url <url>]` | Check the signature, hash, registry and expiry, for the registry's CI. |

- **Sign again after every change to `index.yaml`, and on a schedule.** A weekly CI job that
  signs again (new serial, new 30-day expiry) keeps the signature valid with three weeks of
  margin. If it lapses, runtimes stop listing the registry's plugins (the bundled ones remain)
  until it is signed again.
- **Key rotation**: embed the new public key in the runtime, sign with both keys
  (`--key old --key new`) until the runtimes trusting only the old key are gone, then drop it.
- **Serials only go up**: `sign` refuses a `--serial` that is not above the current one, or
  more than a year above the current Unix time (runtimes would then refuse every later
  signature). If a wrong serial was ever published, a runtime recovers when you delete the key
  `official` of the KV scope `cicord:registry-serials` in its state database (for the CLI, the
  `registry-serials.json` file next to its plugin cache).

### The official registry's CI

[cicord/cicord-plugins](https://github.com/cicord/cicord-plugins) signs and checks its index
with these commands, using the production key `10d3620231e083f8` (public key in
`keys/official.pub` and in `internal/registry/sign.go` `OfficialKeys`):

- the **sign** workflow runs `cicord registry sign . --key env:CICORD_REGISTRY_SIGNING_KEY`
  after every change of `index.yaml` on `main`, every week and on demand, and commits
  `index.yaml.sig`. The key is the `CICORD_REGISTRY_SIGNING_KEY` secret of the
  `registry-signing` environment (deployments from `main` only), and the workflow refuses to
  run unless the `CICORD_VERSION` variable names a Cicord release tag, so the job holding the
  key never builds a moving branch;
- the **verify** workflow runs `cicord registry verify` daily and fails when the signature
  expires within 10 days.

It signs whatever is on `main`: branch protection (required reviews) is the real trust root.
