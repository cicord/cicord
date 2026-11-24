---
title: Upgrading
description: How to upgrade a Cicord runtime while it is pre-release, and what can change between versions.
---

:::caution[Pre-release: expect breaking changes]
Cicord is **v1alpha1**. File formats, the HTTP API and the plugin SDK may still change between
`0.x` releases. Pin the version you deploy (a release binary or `ghcr.io/cicord/cicord:<version>`,
not `latest`) and read the [changelog](https://github.com/cicord/cicord/blob/main/CHANGELOG.md)
before moving to a newer one.
:::

## Upgrading a runtime

1. Back up the data directory (see [Backups and secrets](../backups-and-secrets/)).
2. Get the new version: download the release binary (and verify it, see
   [Installation](../../getting-started/installation/#verify-a-download)), pull
   `ghcr.io/cicord/cicord:<version>`, or update your checkout and rebuild
   (`git pull && make all`, or `docker build` again).
3. Validate your configuration repository with the new binary **before** restarting the
   runtime:

   ```sh
   cicord validate path/to/your-repo
   ```

4. Restart the runtime (`docker stop` sends `SIGTERM`; the HTTP server and the bots shut down
   gracefully in about 25 seconds).
5. Check `GET /readyz` and `cicord status`: the revision should be active and every server
   `Synced` and `Healthy`.

On a server installed with [Production on a VPS](../production-vps/#9-updates), the same
steps are: run the backup service, get the new image, set `CICORD_IMAGE_TAG` in `.env` and run
`docker compose up -d` again.

## What to watch for

- **`engine` ranges.** Your `cicord.yaml` declares `spec.engine` (for example
  `">=0.1.0 <0.2.0"`), and each plugin declares its own. A runtime refuses a revision whose
  range excludes its version. Widen the range in the same change as the upgrade if the new
  version is compatible.
- **`apiVersion`.** Every file uses `cicord.dev/v1alpha1`. A breaking change of the format is
  meant to bump it.
- **Official plugins.** Copies of the official plugins in `plugins/<name>/` are not changed by
  an upgrade. A newer binary may bundle newer versions: the admin console shows "Update
  available", and `cicord plugin update` lists them. A copy you modified is only overwritten when
  you force it. Official plugins installed from the
  [official registry](../../plugins/publishing/#the-official-registry) (`cicord/<name>` in
  `cicord.lock`) update like any registry plugin, independently of the binary; check their
  `engine` range.
- **Vendored SDK packages.** Plugins scaffolded by `cicord plugin new` and portals scaffolded by
  `cicord portal init` vendor the `@cicord/*` tarballs of the binary that created them. Re-run the
  scaffolding in a scratch directory to pick up the new tarballs, or switch to npm once the
  packages are published.
- **The state database** is migrated by the runtime itself; keep the backup until the new
  version has run for a while.

## Planned

A `go install` that embeds the web UIs. See [Status and roadmap](../../project/status/).
