---
title: Backups and secrets
description: What to back up (SQLite or PostgreSQL state store, data directory), how to back up and restore it, and how to manage the encrypted secret store with cicord secret.
---

## What to back up

The configuration lives in git; the runtime's **state store** and **data directory** hold
everything else. The state store is SQLite in the data directory (`cicord.db`) by default, or
a PostgreSQL database with `--database-url` / `CICORD_DATABASE_URL` (see
[Deploy](../deploy/#state-store-sqlite-or-postgresql)).

| What | Why it matters |
|---|---|
| The state store: `cicord.db` (+ `-wal`, `-shm`), or the PostgreSQL database | The resource map (which Discord ID each key is), plugin data (XP, warnings, tickets…), scheduled tasks, sessions, the audit log, the sync history and the encrypted secrets. |
| `secret.key` | The key of the secret store, unless you set `CICORD_SECRET_KEY`. Without it, stored secrets cannot be decrypted. Needed with PostgreSQL too. |
| `repo.git` | With the `embedded` provider ("Managed by Cicord"), this **is** your configuration. |
| `link.key` | Portal link signing key, unless you set `CICORD_LINK_SECRET`. |

The rest of the data directory (`source/`, `writeback/`, `snapshots/`, `cache/`) is rebuilt
automatically. Keep the key (`secret.key` or `CICORD_SECRET_KEY`) in a different place than the
database backups it protects.

### SQLite

- **Offline**: stop the runtime and copy the whole data directory.
- **Online**: take a consistent copy of the database with SQLite's backup command, then copy
  the key files and `repo.git`:

  ```sh
  sqlite3 cicord.db ".backup cicord-backup.db"
  ```

  Never copy `cicord.db` alone while the runtime is running: the WAL file holds recent writes.
- **Restore**: stop the runtime, put the copy in place as `cicord.db`, delete any leftover
  `cicord.db-wal` and `cicord.db-shm`, restore `secret.key`, `link.key` and `repo.git`, then
  start the runtime.

### PostgreSQL

- **Online**: `pg_dump` takes a consistent snapshot while the runtime runs. Use a `pg_dump` at
  least as recent as the server; add `--schema=<schema>` if Cicord uses a dedicated schema.
  Snapshots and point-in-time recovery of a managed database work as well.

  ```sh
  pg_dump --format=custom --file=cicord.dump --dbname="$CICORD_DATABASE_URL"
  ```

  Back up `secret.key`, `link.key` and `repo.git` from the data directory too.
- **Restore**: stop the runtime, create an empty database owned by the runtime's user, restore
  the dump, put the key files and `repo.git` back in the data directory, then start the
  runtime:

  ```sh
  createdb --owner=cicord cicord
  pg_restore --no-owner --role=cicord --dbname="$CICORD_DATABASE_URL" cicord.dump
  ```

  A dump made by an older Cicord is migrated at startup; an older binary refuses a database
  migrated by a newer one.

### Losing the database

Losing the state store is recoverable but noisy. On the next sync, the runtime adopts the existing
Discord resources **by name** again (see [adoption](../../concepts/reconciliation/#adoption-by-name)),
so nothing is duplicated; but plugin data, sessions and history are gone, and renamed
resources may not match. For a local state database (`.cicord/state.db` of the CLI),
`cicord adopt` rebuilds the map by name, with confirmation.

Moving a runtime to another machine means copying the data directory: it holds the SQLite state
and the secret store, their key, and, for the embedded provider, the repository. With
PostgreSQL, point the new machine at the same database and copy the data directory for its
keys and repository.

## The secret store

The setup wizard stores what you paste (bot tokens, the GitHub token, the OAuth client secret)
in an encrypted store inside the state store (SQLite or PostgreSQL), instead of asking you to
manage environment variables:

| Secret | Name |
|---|---|
| Bot token | `DISCORD_TOKEN_<BOT>`, the `token.secretRef` of the generated `bot.yaml` |
| GitHub token | `CICORD_GIT_TOKEN` |
| OAuth client secret | `CICORD_DISCORD_CLIENT_SECRET` |
| Private registry token | `CICORD_REGISTRY_<NAME>_TOKEN` (also from the Store) |

- **Encryption**: AES-256-GCM with a random nonce per write; the secret's name is
  authenticated too, so a ciphertext moved to another name fails to decrypt.
- **Key**: `CICORD_SECRET_KEY` (base64 or hex of exactly 32 bytes, e.g.
  `openssl rand -base64 32`), otherwise `<data dir>/secret.key`, generated with mode 0600. A
  wrong key fails at startup.
- **Resolution**: the runtime reads `token.secretRef`, `CICORD_GIT_TOKEN` and the OAuth client
  secret from the **environment first, then the store**. An environment variable always
  overrides a stored secret.
- The API never returns a secret value; secrets are listed by name and date only.

### `cicord secret`

Manage the store from a terminal. Values are read from **stdin**, never from arguments, and
never printed.

```sh
cicord secret ls --data-dir /data                          # names only
cicord secret set DISCORD_TOKEN_CICORD --data-dir /data      # paste the token, then Enter
cicord secret rm DISCORD_TOKEN_CICORD --data-dir /data
cicord secret rotate-key --data-dir /data                  # re-encrypt everything with a new key
```

`--data-dir` defaults to `.cicord-data`. With PostgreSQL, pass the runtime's database too
(`--database-url`, or `CICORD_DATABASE_URL` in the environment) and its data directory, which
holds `secret.key`; `cicord secret` refuses to run without an existing key rather than generate
one that would not decrypt the stored secrets. `rotate-key` works with the key file only (not
with `CICORD_SECRET_KEY`). A changed bot token is used from the next restart of the runtime.

With Docker, run it inside the container, which has the data volume mounted (and the
runtime's `CICORD_DATABASE_URL`, if any):

```sh
docker exec -i cicord cicord secret set DISCORD_TOKEN_CICORD --data-dir /data
```
