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
| `link.key` | Portal link signing key, unless you set `CICORD_LINK_SECRET`. Regenerated when missing: only the portal links already sent stop working. |

The rest of the data directory (`source/`, `writeback/`, `snapshots/`, `cache/`) is rebuilt
automatically.

**Keep the key apart from the backups.** The database backups hold the secrets encrypted; the
key (`secret.key` or `CICORD_SECRET_KEY`) decrypts them. Store the key once, in a password
manager or a vault, and leave it out of the routine backups: a stolen backup then reveals no
secret, and a restore needs both. Copy the key without displaying it, for example on macOS:

```sh
ssh <server> 'sudo cat /var/lib/docker/volumes/<project>_cicord-data/_data/secret.key' | pbcopy
# paste it into the password manager, then clear the clipboard:
pbcopy < /dev/null
```

### SQLite

- **Offline**: stop the runtime and copy the whole data directory.
- **Online**: take a consistent copy of the database with SQLite's backup command, then copy
  the key files and `repo.git`:

  ```sh
  sqlite3 cicord.db ".backup cicord-backup.db"
  ```

  Never copy `cicord.db` alone while the runtime is running: the WAL file holds recent writes.
- **Restore**: stop the runtime, put the copy in place as `cicord.db`, delete any leftover
  `cicord.db-wal` and `cicord.db-shm`, put `secret.key` back from where you keep it, restore
  `repo.git`, then start the runtime.

### PostgreSQL

- **Online**: `pg_dump` takes a consistent snapshot while the runtime runs. Use a `pg_dump` at
  least as recent as the server; add `--schema=<schema>` if Cicord uses a dedicated schema.
  Snapshots and point-in-time recovery of a managed database work as well.

  ```sh
  pg_dump --format=custom --file=cicord.dump --dbname="$CICORD_DATABASE_URL"
  ```

  Back up `repo.git` from the data directory too; keep `secret.key` apart (above).
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

The setup wizard stores what you paste (bot tokens, the GitHub token or GitHub App, the OAuth client secret)
in an encrypted store inside the state store (SQLite or PostgreSQL), instead of asking you to
manage environment variables:

| Secret | Name |
|---|---|
| Bot token | `DISCORD_TOKEN_<BOT>`, the `token.secretRef` of the generated `bot.yaml` |
| GitHub token | `CICORD_GIT_TOKEN` |
| GitHub App ID | `CICORD_GITHUB_APP_ID` |
| GitHub App private key (PEM) | `CICORD_GITHUB_APP_PRIVATE_KEY` |
| GitHub App installation ID | `CICORD_GITHUB_APP_INSTALLATION_ID` (discovered by the wizard) |
| OAuth client secret | `CICORD_DISCORD_CLIENT_SECRET` |
| Private registry token | `CICORD_REGISTRY_<NAME>_TOKEN` (also from the Store) |

- **Encryption**: AES-256-GCM with a random nonce per write; the secret's name is
  authenticated too, so a ciphertext moved to another name fails to decrypt.
- **Key**: `CICORD_SECRET_KEY` (base64 or hex of exactly 32 bytes, e.g.
  `openssl rand -base64 32`), otherwise `<data dir>/secret.key`, generated with mode 0600. A
  wrong key fails at startup.
- **Resolution**: the runtime reads `token.secretRef`, `CICORD_GIT_TOKEN`, the
  `CICORD_GITHUB_APP_*` secrets and the OAuth client secret from the **environment first, then
  the store**. An environment variable always overrides a stored secret. A configured GitHub
  App takes precedence over `CICORD_GIT_TOKEN` (see
  [GitHub access](../deploy/#github-access-token-or-github-app)).
- The API never returns a secret value; secrets are listed by name and date only.

### `cicord secret`

Manage the store from a terminal. Values are read from **stdin**, never from arguments, and
never printed.

```sh
cicord secret ls --data-dir /data                          # names only
cicord secret set DISCORD_TOKEN_CICORD --data-dir /data      # paste the token, then Enter
cicord secret set CICORD_GITHUB_APP_PRIVATE_KEY --data-dir /data < app.private-key.pem
cicord secret rm DISCORD_TOKEN_CICORD --data-dir /data
cicord secret rotate-key --data-dir /data                  # re-encrypt everything with a new key
```

`set` reads one line, or a whole PEM block when the first line starts with `-----BEGIN` (a
GitHub App private key, read through its `-----END` line).

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

### Rotating a leaked credential

If a secret may have leaked (a server compromise, a token pasted in the wrong place), make the
old value useless at its source, then store the new one. Each value is shown once by its
provider: paste it straight into `cicord secret set`.

| Secret | Where to regenerate it | Then |
|---|---|---|
| `DISCORD_TOKEN_<BOT>` (bot token) | Discord Developer Portal → the application → **Bot → Reset Token** | `cicord secret set DISCORD_TOKEN_<BOT>`, restart the runtime |
| `CICORD_DISCORD_CLIENT_SECRET` | Developer Portal → **OAuth2 → Reset Secret** | `cicord secret set CICORD_DISCORD_CLIENT_SECRET` |
| `CICORD_GITHUB_APP_PRIVATE_KEY` | GitHub App settings → **Private keys → Generate a private key**, then **delete the old key** | `cicord secret set CICORD_GITHUB_APP_PRIVATE_KEY < new.pem` |
| `CICORD_GIT_TOKEN` (token instead of an App) | GitHub → Settings → Developer settings → the token → **Regenerate**, or revoke it | `cicord secret set CICORD_GIT_TOKEN` |
| `CICORD_WEBHOOK_SECRET` | a new random value (`openssl rand -hex 32`), set in the GitHub App (or repository) webhook settings too | `cicord secret set CICORD_WEBHOOK_SECRET` |
| `CICORD_RUNTIME_TOKEN` (CI) | a new random value | update the CI secret and the runtime |
| the store's key (`secret.key`) | `cicord secret rotate-key` re-encrypts every secret with a new key | store the new key in the password manager, delete the old copy |
| `link.key` | delete the file (or change `CICORD_LINK_SECRET`) and restart | portal links already sent stop working |
