# Releasing Cicord

A release is one git tag `v<version>` (`v0.1.0`; `v0.2.0-rc.1` for a pre-release). Pushing
it starts two workflows:

| Workflow | Publishes |
| --- | --- |
| [`release.yml`](.github/workflows/release.yml) | the `cicord` binaries (GitHub Release), the container image `ghcr.io/cicord/cicord` |
| [`release-npm.yml`](.github/workflows/release-npm.yml) | `@cicord/sdk`, `@cicord/client`, `@cicord/react`, `@cicord/ui` on npm |

`create-cicord-portal` (the reference portal, forked with `cicord portal init`) and
`@cicord/admin` (the admin console, embedded in `cicord`) are not published. Versions are
semver, `0.x` while the API is `v1alpha1`.

Pre-release tags (`v0.2.0-rc.1`) are supported by the workflows, but the engine check of
`spec.engine` and plugin `engine` ranges (Masterminds semver) does not match a pre-release
against a range like `>=0.1.0`: such a binary refuses those repositories and plugins. Until
the check ignores the pre-release part, prefer plain versions.

## What the workflows do

**`release.yml`** (tag `v*`; Actions → release → Run workflow is a dry run that publishes
nothing):

1. `meta`: reads the version from the tag (`v0.1.0` → `0.1.0`); a `-suffix` makes it a
   pre-release.
2. `build` (read-only): builds the web UIs once (`pnpm` build + `scripts/sync-ui.sh`: admin
   console, default portal, portal kit) and checks that all of them are there, checks that
   `internal/version` is the tag version, then runs GoReleaser
   ([`.goreleaser.yaml`](.goreleaser.yaml)) with `--skip=publish`: `CGO_ENABLED=0`,
   `-trimpath`, version and commit in the ldflags, file times set to the commit time
   (reproducible archives) for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and
   windows/amd64. Outputs `cicord_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) with
   `LICENSE`, `NOTICE` and `README.md`, `checksums.txt` (SHA-256) and one SPDX SBOM per
   archive (`<archive>.sbom.json`, syft). Smoke test: `cicord version` must print
   `cicord <version> (<commit>)`.
3. `image`: `scripts/release-image.sh` builds the Dockerfile for linux/amd64 and
   linux/arm64 with buildx (cross-compiled, no emulation), with an SBOM and max-mode
   provenance attached by BuildKit, and pushes `ghcr.io/cicord/cicord:<version>` and
   `:sha-<commit>`, plus `:<major>.<minor>` and `:latest` for a version without suffix. It
   then pushes a GitHub build provenance attestation of the image digest to the registry.
4. `release`: creates a build provenance attestation for every file listed in
   `checksums.txt` (archives and SBOMs), then the GitHub Release with the files, an
   install/verify header and GitHub-generated notes; pre-release (not "Latest") when the tag
   has a suffix. It runs only when `build` and `image` succeeded.

**`release-npm.yml`** (same tag; Run workflow is a dry run by default): installs, builds
and tests the packages, checks that the four versions are equal and equal to the tag, packs
each package with `pnpm pack` (which rewrites the `workspace:` and `catalog:` specifiers;
the job fails if one is left), then `npm publish <tarball> --access public` in dependency
order (sdk, client, react, ui), under the dist-tag `latest`, or `next` for a version with a
suffix. Versions already on npm are skipped, so a failed run can be re-run. It uses npm
**trusted publishing**: no npm token, npm trusts the GitHub OIDC identity of this workflow
in the environment `npm`, and attaches provenance automatically. Never run `npm publish`
from a package directory: `workspace:*` would be published verbatim.

Every action is pinned to a commit SHA (tag in the comment). Only GitHub-owned actions and
`pnpm/action-setup` are used; GoReleaser, syft and BuildKit are pinned releases (checksums or
digest verified).

## Each release

1. **Version.** Set `<version>` in:
   - `internal/version/version.go` (`Version`, the engine version of `go install` builds;
     the release workflow fails if it differs from the tag),
   - `Makefile` (`VERSION ?=`) and `Dockerfile` (the two `ARG VERSION=` defaults),
   - `packages/{sdk,client,react,ui}/package.json` (and, to keep them in step,
     `packages/{admin,create-cicord-portal}/package.json`); `@cicord/react` depends on the
     same `@cicord/client` version (`workspace:*`).
2. **Changelog.** Add a `## [<version>] - <date>` section to [CHANGELOG.md](CHANGELOG.md)
   (and the link at the bottom).
3. **Check locally:** `make all && make check`, `pnpm run check`, then
   `goreleaser check` and a local snapshot
   (`goreleaser release --snapshot --clean --skip=publish,sbom`, then
   `dist/cicord_<os>_<arch>*/cicord version`). For the packages:
   `cd packages/<p> && pnpm pack --dry-run`.
4. **Commit** ("chore(release): v<version>") and push to `main`; wait for CI.
5. **Dry runs** (recommended, required before the first release): Actions → release → Run
   workflow, and Actions → release-npm → Run workflow ("dry-run" ticked). The first one is
   the only place where syft, the multi-arch builder and the SBOM files run before a tag.
   Read the archive list and the `npm publish --dry-run` output.
6. **Tag and push:**
   ```sh
   git tag -a v<version> -m "Cicord <version>"
   git push origin v<version>
   ```
   Approve the `npm` environment deployment only once the `release` run is green: binaries
   and images can be rebuilt, an npm version can never be published again.
7. **Verify** (below), then announce.

A failed `release.yml` run can be re-run from the Actions page. If the GitHub Release was
already created, delete it (not the tag) before re-running the `release` job. Never move a
published tag: make a new patch version.

## Verifying a release

**Binaries.** Download an archive and `checksums.txt` from the GitHub Release, then:

```sh
sha256sum --ignore-missing -c checksums.txt           # macOS: shasum -a 256 --ignore-missing -c checksums.txt
gh attestation verify cicord_0.1.0_linux_amd64.tar.gz --repo cicord/cicord
```

`gh attestation verify` checks the Sigstore-signed build provenance: the file was built by a
workflow of `cicord/cicord` (`.github/workflows/release.yml`) from the tagged commit.
`--signer-workflow cicord/cicord/.github/workflows/release.yml` makes the workflow check
explicit. The SBOM of an archive is `<archive>.sbom.json` next to it.

**Container image.**

```sh
docker buildx imagetools inspect ghcr.io/cicord/cicord:0.1.0                     # index: amd64, arm64, attestation manifests
docker buildx imagetools inspect ghcr.io/cicord/cicord:0.1.0 --format '{{ json .Provenance }}'
docker buildx imagetools inspect ghcr.io/cicord/cicord:0.1.0 --format '{{ json .SBOM }}'
gh attestation verify oci://ghcr.io/cicord/cicord:0.1.0 --repo cicord/cicord
```

The GitHub attestation is also pushed to the registry (a Sigstore bundle attached to the
image digest), so cosign v3 should be able to check it without the GitHub API (not tested
yet; `gh attestation verify` is the reference):

```sh
cosign verify-attestation --type slsaprovenance1 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/cicord/cicord/.github/workflows/release.yml@refs/tags/v' \
  ghcr.io/cicord/cicord:0.1.0
```

`docker run --rm ghcr.io/cicord/cicord:0.1.0 version` must print `cicord 0.1.0 (<commit>)`.

**npm.** `npm view @cicord/client@0.1.0`; the package page on npmjs.com shows the
**provenance** badge (built and signed on GitHub Actions, linked to the commit and the
workflow run). In a project: `npm audit signatures` verifies the registry signatures and
provenance attestations of the installed packages. In an empty directory:
`npm install @cicord/sdk @cicord/client @cicord/react @cicord/ui`.

## One-time setup (owner)

In this order, before the first tag:

1. **Actions policy.** Nothing to allow-list: the release workflows use only GitHub-owned
   actions (`actions/checkout`, `actions/setup-go`, `actions/setup-node`,
   `actions/upload-artifact`, `actions/download-artifact`,
   `actions/attest-build-provenance`, which calls `actions/attest`) and
   `pnpm/action-setup`. Under Settings → Actions → General → Workflow permissions, the
   default `GITHUB_TOKEN` permissions can stay read-only: each job asks for what it needs.
2. **Protect the release tags** (Settings → Rules → Rulesets → New tag ruleset, target
   `v*`): restrict creation, update and deletion to maintainers, so that only they can start
   a release, and forbid moving a tag.
3. **GitHub Packages (ghcr.io).** The organization must allow public packages
   (organization Settings → Packages → Package creation: Public). The first `release.yml`
   run creates the package `cicord` linked to the repository (the image's
   `org.opencontainers.image.source`); if it is created private, make it public: package
   page → Package settings → Change visibility → Public.
4. **npm organization.** Create the npm organization `cicord` (npmjs.com → Add Organization,
   free for public packages) with your account as owner, and enable 2FA on your account.
   `npm view @cicord/sdk` must answer 404 (free) or show a package you own. If the scope is
   taken, pick another one and rename the packages: the `name` of
   `packages/{sdk,client,react,ui}/package.json`, the `@cicord/*` imports and dependencies
   in `packages/`, `examples/` and the docs, the module name `@cicord/sdk` in
   `internal/build` and `internal/plugin`, `sdkPackage` in `internal/cli/plugintests.go`,
   and the package lists of `scripts/sync-ui.sh` and of `release-npm.yml`.
5. **First publication from your machine.** npm trusted publishers can only be configured
   on a package that exists, so publish each package once by hand (`npm login` first; npm
   asks for your 2FA code). Two choices:
   - **Recommended: a placeholder**, so that `0.1.0` is the first version published by CI,
     with provenance:
     ```sh
     for p in sdk client react ui; do
       d="$(mktemp -d)"
       printf '{"name":"@cicord/%s","version":"0.0.0-bootstrap.0","description":"Placeholder: see https://github.com/cicord/cicord","license":"Apache-2.0","repository":{"type":"git","url":"git+https://github.com/cicord/cicord.git","directory":"packages/%s"}}\n' "$p" "$p" > "$d/package.json"
       (cd "$d" && npm publish --access public --tag bootstrap)
     done
     ```
     After the CI has published `0.1.0`:
     `npm deprecate "@cicord/<p>@0.0.0-bootstrap.0" "placeholder, use a real version"` and
     `npm dist-tag rm @cicord/<p> bootstrap` for each package.
   - **Or publish `0.1.0` itself** from the tag: `pnpm install && pnpm -r --filter
     "./packages/*" build`, then in each of sdk, client, react, ui (in that order):
     `pnpm pack` and `npm publish cicord-<p>-0.1.0.tgz --access public --provenance=false`
     (the package.json files set `publishConfig.provenance`, which only works in CI). The
     `release-npm` run of `v0.1.0` then skips them: `0.1.0` has no provenance, the next
     versions do.
6. **Trusted publisher, per package** (npmjs.com → the package → Settings → Trusted
   Publisher → GitHub Actions): organization or user `cicord`, repository `cicord`, workflow
   filename `release-npm.yml`, environment `npm`. Then, on the same page, Publishing access →
   **Require two-factor authentication and disallow tokens**: from then on only this
   workflow (and you, with 2FA) can publish. Delete any npm automation or granular token
   created for Cicord, and the old `NPM_TOKEN` repository or environment secret if one
   exists.
7. **GitHub environment `npm`** (Settings → Environments → New environment `npm`): add
   yourself as required reviewer, so that every publication waits for your approval, and
   limit deployments to the tags `v*` and the branch `main` (Deployment branches and tags →
   Selected). It holds no secret.

## After the first npm publication

- New portals and plugins can depend on the registry: `cicord portal init --from-npm`,
  `cicord plugin new --from-npm` (or `CICORD_FROM_NPM=true`), which write
  `"@cicord/client": "^<version>"` instead of `file:vendor/…` and no `vendor/`. Once you
  are confident, make it the default (`fromNPMDefault` in `internal/cli/portal.go`) and
  update docs/portal.md and docs/plugins.md.
- Existing portals switch with a one-line change per package (`"^<version>"` instead of
  `"file:vendor/…"`), then delete the `@cicord/*` entries of `pnpm.overrides`/`overrides`
  and `vendor/`; existing plugins replace the `@cicord/sdk` file: dependency the same way.
- Keep the npm versions and the kit embedded in `cicord` in step: a `cicord` release embeds
  the packages of its commit, and `--from-npm` asks for `^` that version. Both come from the
  same tag.
