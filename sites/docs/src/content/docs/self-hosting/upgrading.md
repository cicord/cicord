---
title: Upgrading
description: How to upgrade a Cicord runtime to a new release (verified image, backup, validation, restart), and what can change between pre-release versions.
---

:::caution[Pre-release: expect breaking changes]
Cicord is **v1alpha1**. File formats, the HTTP API and the plugin SDK may still change between
`0.x` minor releases. Pin the version you deploy (a release binary, or
`ghcr.io/cicord/cicord:<version>` pinned by digest, not `latest`) and read the
[changelog](https://github.com/cicord/cicord/blob/main/CHANGELOG.md) before moving to a newer
one.
:::

## Releases

Each release on [GitHub](https://github.com/cicord/cicord/releases) has archives for every
platform and a multi-arch image (`linux/amd64`, `linux/arm64`) on ghcr.io, tagged
`<version>`, `<major>.<minor>` and `latest`. The release notes give the image's digest. Both
carry build provenance attestations signed by the release workflow: check the image you are
about to run, pinning the workflow and the tag, as the VPS updates do:

```sh
gh attestation verify oci://ghcr.io/cicord/cicord@sha256:<digest> --repo cicord/cicord \
  --signer-workflow cicord/cicord/.github/workflows/release.yml --source-ref refs/tags/v<version>
```

Archives, checksums and SBOMs are covered in
[Verify a download](../../getting-started/installation/#verify-a-download).

## Upgrading a runtime

On a server installed with [Production on a VPS](../production-vps/#9-updates), patch
releases are applied automatically: verified provenance, a check that the running version
is healthy, backup, pull by digest, a copy of the database, then the container's health check
**and** the service check (`cicord healthcheck --service`: revision loaded, servers `Synced`
and not paused, bots and plugins `Healthy`), with a rollback to the previous image digest, and
to the database copy if the new version migrated it, when either fails. Minor releases
(`0.x` → `0.y`) wait for you (`CICORD_AUTO_UPDATE=minor` applies them too). By hand, or on any
other install:

1. Back up the state (see [Backups and secrets](../backups-and-secrets/); on the VPS:
   `sudo systemctl start cicord-backup.service`).
2. Get the new version: pull `ghcr.io/cicord/cicord:<version>@sha256:<digest>` and verify it
   (above), download and verify the release archive, or rebuild from a checkout
   (`git pull && make all`).
3. Validate your configuration repository with the new binary **before** restarting the
   runtime:

   ```sh
   cicord validate path/to/your-repo
   ```

4. Restart the runtime. On the VPS, set the new `CICORD_IMAGE` in `.env` and run
   `docker compose up -d` again. On `SIGTERM` the runtime stops the HTTP server and then the
   bots and plugins, in at most about 35 seconds (`docker stop` waits 10 seconds by default:
   `-t 40` gives it the full time; the VPS `compose.yaml` sets `stop_grace_period: 40s`).
5. Check the service, not only `GET /readyz`: `cicord status --wait --timeout 5m` (or, inside
   the container, `cicord healthcheck --service --data-dir /data`) exits `0` when the revision
   is active, every server `Synced` and `Healthy`, and every bot and plugin `Healthy`.
6. If it fails, go back: the previous binary or image, and, **if the new version migrated the
   state database**, the backup of step 1 too (stop the runtime, restore the database, start
   the previous version). Writes made in between are lost.

## What to watch for

- **`engine` ranges.** Your `cicord.yaml` declares `spec.engine` (for example
  `">=0.1.0 <0.2.0"`), and each plugin declares its own. A runtime refuses a revision whose
  range excludes its version. Widen the range in the same change as the upgrade if the new
  version is compatible. Pre-releases are checked by semver precedence: `0.2.0-rc.1` satisfies
  `>=0.1.0 <0.2.0` and not `>=0.2.0` (write `>=0.2.0-0` to admit the `0.2` release
  candidates); see [Pre-releases](../../project/releasing/#pre-releases).
- **`apiVersion`.** Every file uses `cicord.dev/v1alpha1`. A breaking change of the format is
  meant to bump it.
- **Official plugins.** Copies of the official plugins in `plugins/<name>/` are not changed by
  an upgrade. A newer binary may bundle newer versions: the admin console shows "Update
  available", and `cicord plugin update` lists them. A copy you modified is only overwritten when
  you force it. Official plugins installed from the
  [official registry](../../plugins/publishing/#the-official-registry) (`cicord/<name>` in
  `cicord.lock`) update like any registry plugin, independently of the binary; check their
  `engine` range.
- **A custom portal** vendors the `@cicord/*` packages of the binary that wrote it: run
  `cicord portal vendor`, then `cicord portal build`, and bump the cicord version installed by
  `cicord-portal.yml` (see [Upgrading cicord](../../portal/custom-portal/#upgrading-cicord)).
  A portal on npm (`--from-npm`) bumps its `@cicord/*` versions instead.
- **Plugins scaffolded by `cicord plugin new`** vendor `@cicord/sdk` the same way, unless they
  were created with `--from-npm`. Re-run `cicord plugin new` in a scratch directory to pick up
  the new tarball, or switch to the npm package, published with each release
  ([how](../../portal/custom-portal/#from-the-vendored-tarballs-to-npm)); an upgrade is then a
  version bump in `package.json`.
- **The state database** is migrated by the runtime itself at startup, and an older binary
  refuses a database migrated by a newer one (`database schema version N is newer than this
  binary supports`): a rollback by image alone does not work after a migration. Keep the
  backup until the new version has run for a while. The VPS updater handles it: it compares
  the schema version (`schema_migrations`) before and after, and restores its pre-update copy
  when it rolls back a version that migrated. With PostgreSQL it does not update
  automatically; restore a `pg_dump` taken just before (see
  [Backups and secrets](../backups-and-secrets/#postgresql)).
