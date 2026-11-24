---
title: Backups and secrets
description: What to back up from the data directory and how, and how to manage the encrypted secret store with cicord secret.
---

## What to back up

The configuration lives in git; the runtime's **data directory** holds everything else.

| Path | Why it matters |
|---|---|
| `cicord.db` (+ `-wal`, `-shm`) | The resource map (which Discord ID each key is), plugin data (XP, warnings, tickets…), sessions, the audit log, the sync history and the encrypted secrets. |
| `secret.key` | The key of the secret store, unless you set `CICORD_SECRET_KEY`. Without it, stored secrets cannot be decrypted. |
| `repo.git` | With the `embedded` provider ("Managed by Cicord"), this **is** your configuration. |
| `link.key` | Portal link signing key, unless you set `CICORD_LINK_SECRET`. |

The rest (`source/`, `writeback/`, `snapshots/`, `cache/`) is rebuilt automatically.

### How

- **Offline**: stop the runtime and copy the whole directory.
- **Online**: take a consistent copy of the database with SQLite's backup command, then copy
  the key files:

  ```sh
  sqlite3 cicord.db ".backup cicord-backup.db"
  ```

Never copy `cicord.db` alone while the runtime is running: the WAL file holds recent writes.

### Losing the database

Losing `cicord.db` is recoverable but noisy. On the next sync, the runtime adopts the existing
Discord resources **by name** again (see [adoption](../../concepts/reconciliation/#adoption-by-name)),
so nothing is duplicated; but plugin data, sessions and history are gone, and renamed
resources may not match. For a local state database (`.cicord/state.db` of the CLI),
`cicord adopt` rebuilds the map by name, with confirmation.

Moving a runtime to another machine means copying the data directory: it holds the state, the
secret store and its key, and, for the embedded provider, the repository.

## The secret store

The setup wizard stores what you paste (bot tokens, the GitHub token, the OAuth client secret)
in an encrypted store inside the state database, instead of asking you to manage environment
variables:

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

`--data-dir` defaults to `.cicord-data`. `rotate-key` works with the key file only (not with
`CICORD_SECRET_KEY`). A changed bot token is used from the next restart of the runtime.

With Docker, run it inside the container, which has the data volume mounted:

```sh
docker exec -i cicord cicord secret set DISCORD_TOKEN_CICORD --data-dir /data
```
