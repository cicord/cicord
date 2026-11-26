---
title: Releasing
description: How a Cicord release is cut - make release, the v<version> tag and its workflows, pre-releases, verifying binaries, images and npm packages, and the repository settings releases rely on.
---

A release is one git tag `v<version>` (`v0.2.0`; `v0.2.0-rc.1` for a pre-release), on a
commit prepared by `make release`. Pushing the tag starts two workflows:

| Workflow | Publishes |
| --- | --- |
| [`release.yml`](https://github.com/cicord/cicord/blob/main/.github/workflows/release.yml) | the `cicord` binaries (GitHub Release), the container image `ghcr.io/cicord/cicord`, the Homebrew cask (tap `cicord/homebrew-tap`) |
| [`release-npm.yml`](https://github.com/cicord/cicord/blob/main/.github/workflows/release-npm.yml) | `@cicord/sdk`, `@cicord/client`, `@cicord/react`, `@cicord/ui`, and the binaries as `@cicord/cli` with its platform packages `@cicord/cli-<os>-<arch>`, on npm |

The install script `https://cicord.dev/install.sh` (source:
`sites/landing/public/install.sh`, tested by `sites/landing/install_test.go`) is deployed
with the site (`sites.yml`), not by a release: it installs the latest GitHub Release.

`create-cicord-portal` (the reference portal, forked with `cicord portal init`) and
`@cicord/admin` (the admin console, embedded in `cicord`) are not published. Versions are
semver, `0.x` while the API is `v1alpha1`.

Between releases the source tree carries the last released version. `make release
VERSION=<version>` prepares the next one: the script requires a strictly greater version, a
clean tree and the `main` branch.

## What the workflows do

**`release.yml`** (tag `v*`; Actions → release → Run workflow is a dry run that publishes
nothing):

1. `meta`: reads the version from the tag (`v0.2.0` → `0.2.0`); a `-suffix` makes it a
   pre-release.
2. `build` (read-only): builds the web UIs once (`pnpm` build + `scripts/sync-ui.sh`: admin
   console, default portal, portal kit) and checks that all of them are there, checks that
   `internal/version` is the tag version, then runs GoReleaser
   ([`.goreleaser.yaml`](https://github.com/cicord/cicord/blob/main/.goreleaser.yaml)) with `--skip=publish`: `CGO_ENABLED=0`,
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
   then pushes a GitHub build provenance attestation of the image digest to the registry. On
   a dry run, `image-dry-run` builds the same image without pushing it.
4. `release`: creates a build provenance attestation for every file listed in
   `checksums.txt` (archives and SBOMs), then the GitHub Release with the files, an
   install/verify header that gives the image digest (the
   [automatic VPS updates](../../self-hosting/production-vps/#9-updates) only install that
   digest) and GitHub-generated notes; pre-release (not "Latest") when the tag has a suffix.
   It runs only when `build` and `image` succeeded.
5. `homebrew` (releases only, never a pre-release): GoReleaser wrote the cask
   `dist/homebrew/Casks/cicord.rb` in `build` (`homebrew_casks` with `skip_upload`: darwin
   and linux, amd64 and arm64, the quarantine attribute removed after install on macOS).
   The job checks its version, that every URL is an asset of this release and every
   SHA-256 the one of `checksums.txt`, then commits it to
   [`cicord/homebrew-tap`](https://github.com/cicord/homebrew-tap) with the contents API.
   Only this step gets the tap token (the environment `homebrew`'s secret
   `HOMEBREW_TAP_TOKEN`); without it, the job logs a notice and succeeds, so a release never
   fails on the tap. See [Homebrew tap](#homebrew-tap).

**`release-npm.yml`** (same tag; Run workflow is a dry run by default): installs, builds
and tests the packages, checks that the five versions (`cli` included) are equal and equal to the tag, packs
each package with `pnpm pack` (which rewrites the `workspace:` and `catalog:` specifiers;
the job fails if one is left), then `npm publish <tarball> --access public` in dependency
order (sdk, client, react, ui), under the dist-tag `latest`, or `next` for a version with a
suffix. Versions already on npm are skipped, so a failed run can be re-run. It uses npm
**trusted publishing**: no npm token, npm trusts the GitHub OIDC identity of this workflow
in the environment `npm`, and attaches provenance automatically. Never run `npm publish`
from a package directory: `workspace:*` would be published verbatim.

Then, when the repository variable `CICORD_NPM_CLI` is `true` (see
[New npm packages](#new-npm-packages)), `@cicord/cli`, laid out like esbuild: one package per platform,
`@cicord/cli-<os>-<arch>` (`darwin-arm64`, `darwin-x64`, `linux-arm64`, `linux-x64`,
`win32-x64`; `os` and `cpu` fields, the binary in `bin/`), and `@cicord/cli` itself, whose
`cicord` command (`packages/cli/bin/cicord.js`) runs the binary of the installed platform
package; every platform package is an optional dependency of it at the same version. The
binaries are never rebuilt: the job waits for the assets of the GitHub Release of the tag
(up to 45 minutes; a manual dry run whose version has no release skips `@cicord/cli`),
checks `checksums.txt` and the build provenance attestation of every archive
(`--signer-workflow` `release.yml`, `--source-ref` the tag), runs the linux/amd64 binary,
then `packages/cli/scripts/build-platform-packages.mjs` writes the packages from
`packages/cli` and `platforms.json`. The platform packages are published first, then
`@cicord/cli`, with the same skip and dist-tag rules.

Every action is pinned to a commit SHA (tag in the comment). Only GitHub-owned actions and
`pnpm/action-setup` are used; GoReleaser, syft and BuildKit are pinned releases (checksums or
digest verified).

## Each release

1. **Changelog.** Entries go under `## [Unreleased]` in
   [CHANGELOG.md](https://github.com/cicord/cicord/blob/main/CHANGELOG.md) as changes land.
2. **Prepare the release commit** on an up-to-date, clean `main`:
   ```sh
   make release VERSION=<version>      # scripts/release.sh <version>
   ```
   The script refuses a version that is not greater than the current one, an existing tag,
   another branch, a dirty tree and an empty `[Unreleased]`. It sets the version in
   `internal/version/version.go` (the engine version of `go install` builds; the release
   workflow fails if it differs from the tag), the `Makefile`, the `Dockerfile`, the cicord
   version that `examples/basic/.github/workflows/cicord.yml` installs (the `cicord init`
   workflow, pinned to the release that generated it) and the `package.json` of the seven packages (`packages/cli` included); turns `[Unreleased]` into `## [<version>] - <date>`
   with its compare link; commits `chore(release): v<version>`; and lists the other files
   that still name the previous version (install examples, docs). It never pushes nor tags.
3. **Review** the commit (`git show`), update the files it listed and amend. When the build
   or the packaging changed, check locally: `make all && make check`, `pnpm run check`,
   `goreleaser check` and a snapshot
   (`goreleaser release --snapshot --clean --skip=publish,sbom`, then
   `dist/cicord_<os>_<arch>*/cicord version`); for the packages,
   `cd packages/<p> && pnpm pack --dry-run`.
4. **Push** to `main` and wait for CI.
5. **Dry runs**, when a release workflow changed: Actions → release → Run workflow, and
   Actions → release-npm → Run workflow ("dry-run" ticked). The first one is the only place
   where syft, the multi-arch builder and the SBOM files run before a tag. Read the archive
   list and the `npm publish --dry-run` output.
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

## Pre-releases

A release candidate is `x.y.z-rc.N` (`0.3.0-rc.1`, `0.3.0-rc.2`, then `0.3.0`), cut like a
release: `make release VERSION=0.3.0-rc.1` prepares the commit, then tag `v0.3.0-rc.1`.

- **Versions.** `scripts/release.sh` accepts `x.y.z` and `x.y.z-rc.N` (numbers without
  leading zeros) and refuses a version that is not greater than the current one by semver
  precedence: `0.2.0-rc.2 < 0.2.0-rc.10 < 0.2.0`, so it refuses an rc after its release.
- **What is published.** A GitHub prerelease (never "Latest"), the npm packages under the
  dist-tag `next` (`latest` does not move; `@cicord/cli` included), and the image tags
  `:0.2.0-rc.1` and `:sha-<commit>` only (no `:0.2`, no `:latest`). The Homebrew tap is not
  updated, and the install script only installs it when asked
  (`CICORD_VERSION=0.2.0-rc.1`). The automatic updates of the
  [VPS install](../../self-hosting/production-vps/#9-updates) follow the latest release, so
  they never install a pre-release, and a server running one moves to the release.
- **Changelog.** A pre-release needs entries under `[Unreleased]` but leaves them there: they
  become the `0.2.0` section when `0.2.0` is released, with a compare link from the previous
  release (`v0.1.11...v0.2.0`). The GitHub prerelease has the generated notes.
- **Engine ranges.** The `spec.engine` ranges of `cicord.yaml` and plugin manifests are
  checked by plain semver precedence, pre-releases included: `0.2.0-rc.1` sorts after every
  `0.1.x` and before `0.2.0`. So `0.2.0-rc.1` satisfies `>=0.1.0` and `>=0.1.0 <0.2.0`
  (like `0.1.x`, it is below `0.2.0`), and not `>=0.2.0`. A range that is meant for the
  `0.2` release candidates starts at `>=0.2.0-0`, which `0.2.0` satisfies too; repositories
  and plugins created by a pre-release binary get `>=0.2.0-0 <0.3.0`. Caret and tilde ranges
  compare the version numbers instead: `^0.1.0` and `~0.1.0` mean `0.1.x` and do not admit
  `0.2.0-rc.1`. Build metadata (`+…`) is ignored.
- **Plugin versions.** The version ranges of registry plugins (in `bot.yaml`) keep the npm
  rule: a plugin's pre-release tag is only resolved by a range that names a pre-release
  (`^2.0.0-0`), never by `*` or `^1.0.0`. `cicord plugin publish` requires a version greater
  than every earlier tag by the same precedence (`1.0.0` after `1.0.0-rc.2`, not the
  reverse).

## Verifying a release

**Binaries.** Download an archive and `checksums.txt` from the GitHub Release, then:

```sh
sha256sum --ignore-missing -c checksums.txt           # macOS: shasum -a 256 --ignore-missing -c checksums.txt
gh attestation verify cicord_0.1.9_linux_amd64.tar.gz --repo cicord/cicord
```

`gh attestation verify` checks the Sigstore-signed build provenance: the file was built by a
workflow of `cicord/cicord` (`.github/workflows/release.yml`) from the tagged commit.
`--signer-workflow cicord/cicord/.github/workflows/release.yml` makes the workflow check
explicit. The SBOM of an archive is `<archive>.sbom.json` next to it.

**Container image.**

```sh
docker buildx imagetools inspect ghcr.io/cicord/cicord:0.2.0                     # index: amd64, arm64, attestation manifests
docker buildx imagetools inspect ghcr.io/cicord/cicord:0.2.0 --format '{{ json .Provenance }}'
docker buildx imagetools inspect ghcr.io/cicord/cicord:0.2.0 --format '{{ json .SBOM }}'
gh attestation verify oci://ghcr.io/cicord/cicord:0.2.0 --repo cicord/cicord
```

The GitHub attestation is also pushed to the registry, as a Sigstore bundle attached to the
image digest. `docker run --rm ghcr.io/cicord/cicord:0.2.0 version` must print
`cicord 0.2.0 (<commit>)`.

**npm.** `npm view @cicord/client@0.2.0`; the package page on npmjs.com shows the
**provenance** badge (built and signed on GitHub Actions, linked to the commit and the
workflow run). In a project: `npm audit signatures` verifies the registry signatures and
provenance attestations of the installed packages. In an empty directory:
`npm install @cicord/sdk @cicord/client @cicord/react @cicord/ui`, and, once `@cicord/cli`
is published, `npx @cicord/cli@<version> version` must print `cicord <version> (<commit>)`.

**Homebrew.** The tap's `Casks/cicord.rb` has the release's version and checksums;
`brew install cicord/tap/cicord && cicord version`.

**Install script.** `curl -fsSL https://cicord.dev/install.sh | sh` installs the new release
(it verifies the checksum, and the attestation when `gh` is logged in).

## Repository settings

The release workflows rely on these settings of `cicord/cicord`, on GitHub and npm; keep
them when changing the workflows.

- **Actions.** Only GitHub-owned actions and `pnpm/action-setup` are used, so there is nothing
  to allow-list. The default `GITHUB_TOKEN` permissions stay read-only (Settings → Actions →
  General): each job asks for what it needs.
- **Release tags.** A tag ruleset on `v*` (Settings → Rules → Rulesets) restricts creation,
  update and deletion to maintainers, so only they can start a release, and forbids moving a
  tag.
- **Container package.** The package `cicord` on ghcr.io is public and linked to the
  repository (the image's `org.opencontainers.image.source`).
- **npm.** The organization `cicord` owns the packages (`sdk`, `client`, `react`, `ui`, `cli`
  and the five `cli-<os>-<arch>`). Each one has a trusted publisher
  (npmjs.com → the package → Settings → Trusted Publisher → GitHub Actions: organization
  `cicord`, repository `cicord`, workflow `release-npm.yml`, environment `npm`) and
  Publishing access set to **Require two-factor authentication and disallow tokens**; no npm
  token is kept anywhere. A new package must be published once by hand (with 2FA) before a
  trusted publisher can be configured on it ([new npm packages](#new-npm-packages)).
- **Environment `npm`** (Settings → Environments): the owner as required reviewer, so every
  publication waits for approval, and deployments limited to the tags `v*` and the branch
  `main`. It holds no secret.
- **Environment `homebrew`**: deployments limited to the tags `v*`, and the secret
  `HOMEBREW_TAP_TOKEN` ([Homebrew tap](#homebrew-tap)).

### Homebrew tap

One-time setup, before the first release that should update the tap (until then the
`homebrew` job only logs a notice):

1. **Create the tap**, a public repository `cicord/homebrew-tap` (the `homebrew-` prefix is
   what makes `brew install cicord/tap/cicord` find it), default branch `main`, with a
   README; the workflow creates `Casks/cicord.rb` on the first release:
   ```sh
   gh repo create cicord/homebrew-tap --public --add-readme \
     --description "Homebrew tap of Cicord: brew install cicord/tap/cicord"
   ```
   Suggested README: the install command, and that `Casks/cicord.rb` is written by the
   release workflow of `cicord/cicord` and must not be edited by hand. Disable what the tap
   does not need (wiki, projects).
2. **Create the token**: GitHub → Settings → Developer settings → Fine-grained personal
   access tokens → Generate new token. Resource owner `cicord` (the organization must allow
   fine-grained tokens, and may ask its owner to approve it), Repository access **Only select
   repositories** → `cicord/homebrew-tap`, Permissions → Repository → **Contents: Read and
   write** (Metadata: read-only is added automatically), nothing else. An expiration of up
   to a year: note the date and rotate it.
3. **Store it** in an environment of `cicord/cicord`, so only release runs can read it:
   Settings → Environments → New environment `homebrew` → Deployment branches and tags →
   Selected → add the tag rule `v*`; then Environment secrets → `HOMEBREW_TAP_TOKEN`. Or:
   ```sh
   gh secret set HOMEBREW_TAP_TOKEN --env homebrew --repo cicord/cicord
   ```
4. **Check** at the next release: the `homebrew` job prints the commit, and
   `brew install cicord/tap/cicord && cicord version` works. A release that ran without the
   token can be caught up within the artifact retention (7 days): re-run its `homebrew` job.

### New npm packages

`@cicord/cli` and its five platform packages are published by `release-npm.yml` once the
repository variable `CICORD_NPM_CLI` is `true`; until then a release publishes the four SDK
packages only. npm only lets a trusted publisher be configured on a package that
exists, and a new trusted publisher configuration expires if it has not published within
2 days. So, shortly before that release:

1. **Publish a placeholder** of each package from your machine (`npm login` first, with 2FA),
   so the first real version is published by CI, with provenance:
   ```sh
   for p in cli cli-darwin-arm64 cli-darwin-x64 cli-linux-arm64 cli-linux-x64 cli-win32-x64; do
     d="$(mktemp -d)"
     printf '{"name":"@cicord/%s","version":"0.0.0-bootstrap.0","description":"Placeholder: see https://github.com/cicord/cicord","license":"Apache-2.0","repository":{"type":"git","url":"git+https://github.com/cicord/cicord.git","directory":"packages/cli"}}\n' "$p" > "$d/package.json"
     (cd "$d" && npm publish --access public --tag bootstrap)
   done
   ```
2. **Trusted publisher, per package** (npmjs.com → the package → Settings → Trusted
   Publisher → GitHub Actions): organization or user `cicord`, repository `cicord`, workflow
   filename `release-npm.yml`, environment `npm`. With npm 11.10 or later, the same from
   the command line:
   ```sh
   for p in cli cli-darwin-arm64 cli-darwin-x64 cli-linux-arm64 cli-linux-x64 cli-win32-x64; do
     npm trust github "@cicord/$p" --file release-npm.yml --repo cicord/cicord --env npm --allow-publish
   done
   ```
   Then, on each package page, Publishing access → **Require two-factor authentication and
   disallow tokens**.
3. **Release** within 2 days (tag, approve the `npm` environment), then retire the
   placeholders:
   `npm deprecate "@cicord/<p>@0.0.0-bootstrap.0" "placeholder, use a real version"` and
   `npm dist-tag rm @cicord/<p> bootstrap` for each package.
4. **Turn the publish steps on**: the repository variable gates the `@cicord/cli` steps of
   `release-npm.yml`, so set it before you tag the release of step 3:
   ```sh
   gh variable set CICORD_NPM_CLI --body true --repo cicord/cicord
   ```

## Vendored or npm packages

`cicord portal init` depends on `@cicord/client`, `@cicord/react` and `@cicord/ui` from npm,
at the exact version of the binary (`"0.2.0"`, no range); with `--vendor` (offline or
air-gapped builds) it vendors the tarballs embedded in the binary instead (`vendor/`, `file:`
dependencies), and a development build, whose version is not on npm, always does.
`cicord portal upgrade` sets a portal's packages to the version of the running `cicord`, in
the mode it uses; `--check` (run by `cicord-portal.yml`) fails when they differ, and
`--to-npm` moves a vendored portal to npm. `cicord portal vendor` is its deprecated alias.
`cicord plugin new` still vendors `@cicord/sdk` by default; `--from-npm` (or
`CICORD_FROM_NPM=true`) depends on `"^<version>"` from npm. The tarballs and the npm packages
come from the same tag, so a `cicord` release and the npm packages of that version always
match: a release must publish them (`release-npm.yml`) for new portals to install. See
[Custom portal](../../portal/custom-portal/) and [Writing a plugin](../../plugins/writing/).
