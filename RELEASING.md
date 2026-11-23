# Releasing the npm packages

`@codi/sdk`, `@codi/client`, `@codi/react` and `@codi/ui` are released together, with the
same version, by [`.github/workflows/release-npm.yml`](.github/workflows/release-npm.yml).
Until they are on npm, `codi portal init` and `codi plugin new` vendor the tarballs
embedded in the `codi` binary (`make ui`). `create-codi-portal` (the reference portal) and
`@codi/admin` are not published.

What the workflow does: install, build and test the packages, check that the four
versions are equal (and equal to the tag), `pnpm pack` each package (which rewrites the
`workspace:` and `catalog:` specifiers; the job fails if one is left), then
`npm publish <tarball> --access public --provenance` in dependency order (sdk, client,
react, ui). Versions already on npm are skipped, so a failed run can be re-run. Never run
`npm publish` from a package directory: `workspace:*` would be published verbatim.

## One-time setup (owner)

1. **The `@codi` scope.** `npm view @codi/sdk` must answer 404 (free) or show a package
   you own. Create the npm organization `codi` (npmjs.com → Add Organization, free for
   public packages) with your account as owner. If the scope is taken, pick another one
   and rename the packages: the `name` of `packages/{sdk,client,react,ui}/package.json`,
   the `@codi/*` imports and dependencies in `packages/`, `examples/` and the docs, the
   module name `@codi/sdk` in `internal/build` and `internal/plugin` (the plugin host),
   `sdkPackage` in `internal/cli/plugintests.go`, and the package list of
   `scripts/sync-ui.sh` and of the workflow.
2. **The repository must be public** (npm provenance is only generated for public source
   repositories) and the `repository.url` of the four package.json files must be the
   GitHub repository the workflow runs in (`github.com/npjonath/codi`).
3. **Token.** On npmjs.com → Access Tokens → Generate New Token → Granular Access Token:
   read and write on the packages of the `codi` organization (for the very first publish,
   "all packages" of the organization, since the packages do not exist yet), with 2FA
   bypass for automation if your account enforces 2FA on writes. Add it to the GitHub
   repository as the secret `NPM_TOKEN` (Settings → Secrets and variables → Actions).
4. **Environment (recommended).** The job runs in the GitHub environment `npm`: create it
   (Settings → Environments) with yourself as required reviewer and move the `NPM_TOKEN`
   secret into it, so that every publication waits for your approval.

## Each release

1. Pick the version (semver; `0.x` while the API is `v1alpha1`) and set it in the four
   `packages/{sdk,client,react,ui}/package.json`. `@codi/react` depends on the exact same
   `@codi/client` version (`workspace:*`).
2. Check locally: `pnpm install && pnpm -r --filter "./packages/*" build && pnpm -r test`,
   then `cd packages/<p> && npm pack --dry-run` for each package (only `dist/`,
   `README.md`, `LICENSE` and `package.json`).
3. Commit and push to `main`.
4. **Dry run:** GitHub → Actions → release-npm → Run workflow (the "dry-run" box ticked).
   Read the packed file lists and the `npm publish --dry-run` output.
5. **Publish:** either tag the commit (`git tag npm-v<version> && git push origin
   npm-v<version>`) or run the workflow again with "dry-run" unticked. Approve the `npm`
   environment if you protected it.
6. **Verify:** `npm view @codi/client@<version>` (the npm page shows the provenance
   badge), and in an empty directory `npm install @codi/sdk @codi/client @codi/react
   @codi/ui`.

Publishing by hand (not recommended, no provenance): `pnpm pack` in each package, then
`npm publish codi-<p>-<version>.tgz --access public --provenance=false` (the package.json
files set `publishConfig.provenance`, which only works in CI).

## After the first publication

- New portals and plugins can depend on the registry: `codi portal init --from-npm`,
  `codi plugin new --from-npm` (or `CODI_FROM_NPM=true`), which write
  `"@codi/client": "^<version>"` instead of `file:vendor/…` and no `vendor/`. Once you
  are confident, make it the default (`fromNPMDefault` in `internal/cli/portal.go`) and
  update docs/portal.md and docs/plugins.md.
- Existing portals switch with a one-line change per package (`"^<version>"` instead of
  `"file:vendor/…"`), then delete the `@codi/*` entries of `pnpm.overrides`/`overrides`
  and `vendor/`; existing plugins replace the `@codi/sdk` file: dependency the same way.
- Keep the npm versions and the kit embedded in `codi` in step: a `codi` release embeds
  the packages of its commit, and `--from-npm` asks for `^` that version.
