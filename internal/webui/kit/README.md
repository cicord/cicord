# Portal kit (generated)

`scripts/sync-ui.sh` (run by `make ui`) fills this directory, embedded in the `codi` binary
for `codi portal init`:

- `packages/*.tgz` — `pnpm pack` tarballs of `@codi/client`, `@codi/react` and `@codi/ui`,
  vendored into new portals until the packages are published on npm;
- `template/` — the reference portal (`packages/create-codi-portal`), as written by its
  `create-codi-portal` bin.

Only this README and `.gitignore` are committed: a `codi` built without `make ui` still
compiles, and `codi portal init` then explains how to build the kit.
