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

## From 0.1.11 to 0.2.0

0.2.0 is a minor release: the VPS updater does not apply it on its own (unless
`CICORD_AUTO_UPDATE=minor`). Follow these steps in order; the
[changelog](https://github.com/cicord/cicord/blob/main/CHANGELOG.md) has the full list of
changes.

1. **Back up first.** The database upgrade is one-way: on first start, 0.2.0 applies
   migrations 0004–0006, 0010 and 0011 (SQLite and PostgreSQL), and a 0.1.11 binary then
   refuses the database (`database schema version 11 is newer than this binary supports (3)`).
   **There is no rollback other than restoring this backup.** Back up the data directory
   (`cicord.db`, `secret.key`, `link.key`) or take a `pg_dump`.
2. **Widen `spec.engine`.** Repositories created by 0.1.x declare `">=0.1.0 <0.2.0"`, which
   0.2.0 refuses. Commit `">=0.1.0 <0.3.0"` (0.1.11 accepts it too) before the upgrade, and
   narrow it to `">=0.2.0 <0.3.0"` afterwards. Change the `cicord@v0.1.11` pins of
   `.github/workflows/cicord.yml` to `@v0.2.0`. A plugin's `secrets.<NAME>.secretRef` in
   `bot.yaml` must now start with `CICORD_`: rename it (and the secret) in the same change.
3. **Upgrade the runtime**, as above (validate, restart, `cicord status --wait`). 0.2.0
   creates `privacy.key` in the data directory for
   [member erasures](../backups-and-secrets/#member-erasures): add it to your backups. A
   plugin task that keeps failing is parked and keeps its instance `Degraded`, which fails
   `status --wait`: retry or discard it in Diagnostics
   ([failed plugin tasks](../monitoring/#failed-plugin-tasks)).
4. **Then update the plugins, never before.** Every official plugin release requires engine
   `>=0.2.0`. On 0.2.0, `cicord plugin update` and the Store only offer releases whose
   `spec.engine` accepts the running engine. Review each update: new versions request new
   grants, intents and Discord permissions (audit-log and moderation need
   `discord.auditlog.read`, the `ServerModeration` intent and View Audit Log). An update that
   widens what a bound credential can reach stops with a review digest: repeat it with
   `--approve-secret-changes <digest>`.
5. **Upgrade a custom portal.** Run `cicord portal upgrade`, `pnpm install` and
   `cicord portal build`, then commit. `portal upgrade` only moves the `@cicord/*` packages: a
   portal forked from the reference portal gets none of the new pages (verification, ticket
   desk, rewards, audit export, appeals). Run `cicord portal init` in a scratch directory and
   merge them by hand.

**Plugin data written by 0.1.x plugins has no compatibility guarantee.** The core keeps it,
but a 0.2 plugin may read it differently. Several official plugins migrate their own 0.1.x
data; their READMEs say what is kept. Plugin storage also gets a quota (1,000,000 keys and
ranking members, 256 MiB per installation): an installation already above it keeps its data,
but further writes fail until it shrinks.

## What to watch for

- **`engine` ranges.** Your `cicord.yaml` declares `spec.engine` (for example
  `">=0.2.0 <0.3.0"`), and each plugin declares its own. A runtime refuses a revision whose
  range excludes its version. Widen the range before the upgrade if the new version is
  compatible. Pre-releases are checked by semver precedence (see
  [Pre-releases](../../project/releasing/#pre-releases)).
- **`apiVersion`.** Every file uses `cicord.dev/v1alpha1`. A breaking change of the format is
  meant to bump it.
- **Official plugins.** Copies of the official plugins in `plugins/<name>/` are not changed by
  an upgrade. A newer binary may bundle newer versions: the admin console shows "Update
  available", and `cicord plugin update` lists them. A copy you modified is only overwritten when
  you force it. Official plugins installed from the
  [official registry](../../plugins/publishing/#the-official-registry) (`cicord/<name>` in
  `cicord.lock`) update like any registry plugin, independently of the binary; check their
  `engine` range.
- **A custom portal** depends on the `@cicord/*` packages of the binary that wrote it, at its
  exact version (from npm, or vendored with `--vendor`): run `cicord portal upgrade`, then
  `pnpm install` in the portal and `cicord portal build`, commit, and bump the cicord version
  installed by `cicord-portal.yml` (see
  [Upgrading cicord](../../portal/custom-portal/#upgrading-cicord)). A vendored portal moves
  to npm with `cicord portal upgrade --to-npm`
  ([how](../../portal/custom-portal/#from-the-vendored-tarballs-to-npm)).
- **Plugins scaffolded by `cicord plugin new`** vendor `@cicord/sdk` the same way, unless they
  were created with `--from-npm`. Re-run `cicord plugin new` in a scratch directory to pick up
  the new tarball, or switch to the npm package, published with each release (in
  `package.json`, `"@cicord/sdk": "<version>"` instead of `file:vendor/…`, then delete
  `vendor/`); an upgrade is then a version bump in `package.json`.
- **The state database** is migrated by the runtime itself at startup, and an older binary
  refuses a database migrated by a newer one (`database schema version N is newer than this
  binary supports`): a rollback by image alone does not work after a migration. It also
  refuses a database whose recorded migration of a given number has another name than its
  own (`database records migration N as "…", but this binary's migration N is "…"`): the
  database was migrated by an incompatible build, so restore the backup taken before that
  upgrade. Keep the
  backup until the new version has run for a while. The VPS updater handles it: it compares
  the schema version (`schema_migrations`) before and after, and restores its pre-update copy
  when it rolls back a version that migrated. With PostgreSQL it does not update
  automatically; restore a `pg_dump` taken just before (see
  [Backups and secrets](../backups-and-secrets/#postgresql)).
