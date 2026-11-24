---
title: Upgrading
description: How to upgrade a Cicord runtime while it is pre-release, and what can change between versions.
---

:::caution[Pre-release: expect breaking changes]
Cicord is **v1alpha1**. File formats, the HTTP API and the plugin SDK may still change between
commits, and there are no tagged releases or upgrade notes yet. Pin the commit you deploy and
read the git log before moving to a newer one.
:::

## Upgrading a runtime built from source

1. Back up the data directory (see [Backups and secrets](../backups-and-secrets/)).
2. Update your checkout and rebuild: `git pull && make all` (or `docker build` again).
3. Validate your configuration repository with the new binary **before** restarting the
   runtime:

   ```sh
   cicord validate path/to/your-repo
   ```

4. Restart the runtime (`docker stop` sends `SIGTERM`; the HTTP server and the bots shut down
   gracefully in about 25 seconds).
5. Check `GET /readyz` and `cicord status`: the revision should be active and every server
   `Synced` and `Healthy`.

## What to watch for

- **`engine` ranges.** Your `cicord.yaml` declares `spec.engine` (for example
  `">=0.1.0 <0.2.0"`), and each plugin declares its own. A runtime refuses a revision whose
  range excludes its version. Widen the range in the same change as the upgrade if the new
  version is compatible.
- **`apiVersion`.** Every file uses `cicord.dev/v1alpha1`. A breaking change of the format is
  meant to bump it.
- **Bundled plugins.** Copies of the official plugins in `plugins/<name>/` are not changed by
  an upgrade. A newer binary may bundle newer versions: the admin console shows "Update
  available", and `cicord plugin update` lists them. A copy you modified is only overwritten when
  you force it.
- **Vendored SDK packages.** Plugins scaffolded by `cicord plugin new` and portals scaffolded by
  `cicord portal init` vendor the `@cicord/*` tarballs of the binary that created them. Re-run the
  scaffolding in a scratch directory to pick up the new tarballs, or switch to npm once the
  packages are published.
- **The state database** is migrated by the runtime itself; keep the backup until the new
  version has run for a while.

## Planned

Release binaries (GoReleaser), a published container image, npm publication of the `@cicord/*`
packages and a `go install` that embeds the web UIs are planned, together with a decision on
the runtime license. See [Status and roadmap](../../project/status/).
