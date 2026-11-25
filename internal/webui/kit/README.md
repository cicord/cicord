# Portal kit (generated)

`scripts/sync-ui.sh` (run by `make ui`) fills this directory, embedded in the `cicord` binary
for `cicord portal init` and `cicord plugin new`:

- `packages/*.tgz` — `pnpm pack` tarballs of `@cicord/client`, `@cicord/react` and `@cicord/ui`
  (vendored into portals by `cicord portal init --vendor` and `cicord portal upgrade`) and
  `@cicord/sdk` (vendored into new plugins for their unit tests);
- `template/` — the reference portal (`packages/create-cicord-portal`), as written by its
  `create-cicord-portal` bin.

Only this README and `.gitignore` are committed: a `cicord` built without `make ui` still
compiles; `cicord portal init` then explains how to build the kit, and `cicord plugin new`
scaffolds the plugin without its test setup.
