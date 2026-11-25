---
title: Backups and secrets
description: What to back up (SQLite or PostgreSQL state store, data directory), how to back up, copy off-host and restore it, a timed recovery drill, and how to manage and rotate the encrypted secrets with cicord secret.
---

## What to back up

The configuration lives in git; the runtime's **state store** and **data directory** hold
everything else. The state store is SQLite in the data directory (`cicord.db`) by default, or
a PostgreSQL database with `--database-url` / `CICORD_DATABASE_URL` (see
[Deploy](../deploy/#state-store-sqlite-or-postgresql)).

| What | Why it matters |
|---|---|
| The state store: `cicord.db` (+ `-wal`, `-shm`), or the PostgreSQL database | The resource map (which Discord ID each key is), plugin data (XP, warnings, tickets…), scheduled tasks, sessions, the audit log, the sync history, the [analytics](../analytics/) aggregates and the encrypted secrets. |
| `secret.key` | The key of the secret store, unless you set `CICORD_SECRET_KEY`. Without it, stored secrets cannot be decrypted. Needed with PostgreSQL too. |
| `repo.git` | With the `embedded` provider ("Managed by Cicord"), this **is** your configuration. |
| `link.key` | Portal link signing key, unless you set `CICORD_LINK_SECRET`. Regenerated when missing: only the portal links already sent stop working. |

The rest of the data directory (`source/`, `writeback/`, `snapshots/`, `cache/`) is rebuilt
automatically.

**What a backup of the state store covers**: the resource map (so the next plan after a
restore is empty instead of adopting by name), plugin storage (tickets, XP, warnings, polls…),
scheduled tasks, admin and portal sessions, the audit log, the sync history, the analytics
aggregates, the setup state (which repository the runtime follows) and the encrypted secrets.
**What it does not cover**: the configuration itself (it is in git, except with the embedded
provider: back up `repo.git`), the key `secret.key` (kept apart, below), Discord itself
(messages, members, anything Cicord does not manage: Discord keeps it), `.env` and
`compose.yaml` (keep them with your server notes), the Docker images (pulled again by digest),
and the plugin download cache (fetched again).

**Keep the key apart from the backups.** The database backups hold the secrets encrypted; the
key (`secret.key` or `CICORD_SECRET_KEY`) decrypts them. The key must live in exactly two
places: the runtime's data directory, and a password manager or vault that the backups never
reach. It is never in a backup archive nor in the off-host copies: a stolen backup then reveals
no secret, and a restore needs both. Copy the key without displaying it, for example on macOS:

```sh
ssh <server> 'sudo cat /var/lib/docker/volumes/<project>_cicord-data/_data/secret.key' | pbcopy
# paste it into the password manager, then clear the clipboard:
pbcopy < /dev/null
```

### On the VPS: backups, off-host copies, restore

On a server installed with [Production on a VPS](../production-vps/#8-secrets-and-backups),
[`deploy/vps/scripts/05-backup.sh`](https://github.com/cicord/cicord/blob/main/deploy/vps/scripts/05-backup.sh)
installs `cicord-backup` and a daily systemd timer (03:30 UTC). Each run:

1. takes the online SQLite copy below and checks it with `PRAGMA integrity_check`;
2. adds `repo.git`, the write-back clone and a `MANIFEST` (image, schema version, checksum);
   never `secret.key` or `link.key` (the run refuses an archive that contains one);
3. writes `/var/backups/cicord/cicord-<UTC time>.tar.zst` (mode `0600`, kept
   `CICORD_BACKUP_KEEP_DAYS`, default 14), after checking that it decompresses;
4. copies it off the server when `CICORD_BACKUP_REMOTE` is set.

A run **fails loudly**: when `cicord.db` is missing (a wrong volume, a runtime that never
started), the copy fails its integrity check, or the off-host copy fails, it exits non-zero
(the systemd unit is `failed`: `systemctl --failed`, `journalctl -t cicord-backup`) and sends a
**Backup failed** alert through the runtime's [alert webhook](../monitoring/#alerts) when one
is configured. A PostgreSQL state (`CICORD_DATABASE_URL` in `.env`) is not in the archive: back
it up with `pg_dump` (below).

#### Off-host copies

A local backup does not survive the loss of the server. Set `CICORD_BACKUP_REMOTE` in
`/opt/cicord/.env`, then run `sudo sh /opt/cicord/scripts/05-backup.sh` again (it installs
`rclone` when needed) and a backup now (`sudo systemctl start cicord-backup.service`):

| `CICORD_BACKUP_REMOTE` | Copy | Setup |
|---|---|---|
| `rclone:<remote>:<path>`, e.g. `rclone:b2:acme-cicord/backups` | `rclone copyto`, then checked with `rclone lsf` | `sudo rclone config` creates the remote (S3, Backblaze B2, SFTP, Google Drive…) in root's rclone configuration. Use a key restricted to that bucket, with write access and no delete if the provider allows it (object lock or versioning keeps old copies). |
| `rsync:[<user>@]<host>:<path>`, e.g. `rsync:backup@nas.example.org:/srv/cicord` | `rsync` over SSH with root's key, no prompt (`BatchMode`) | `sudo ssh-keygen -t ed25519` on the server, add `/root/.ssh/id_ed25519.pub` to the target user's `authorized_keys` (restrict it, for example with `rrsync`), connect once to accept the host key: `sudo ssh backup@nas.example.org true`. |

Retention on the remote is the remote's job (a bucket lifecycle rule, a cron job on the
target). Only the archive is copied: the key stays out of it.

#### Restore

[`07-restore.sh`](https://github.com/cicord/cicord/blob/main/deploy/vps/scripts/07-restore.sh)
restores an archive onto this server, a new one or the same:

```sh
sudo sh /opt/cicord/scripts/07-restore.sh /var/backups/cicord/cicord-<time>.tar.zst   # paste the key, Enter, Ctrl-D
sudo sh /opt/cicord/scripts/07-restore.sh <archive> --key-file /root/secret.key      # or from a file (delete it afterwards)
sudo sh /opt/cicord/scripts/07-restore.sh <archive> --keep-key                       # same server: keep the volume's key
```

It checks the archive (decompression, integrity, checksum), refuses a key that is not one,
stops the runtime, moves the current state aside to `/var/backups/cicord/pre-restore-<time>/`
(never deletes it), puts `cicord.db`, `repo.git` and `writeback/` in place, writes
`secret.key` (`0600`), gives the files to the runtime's user, starts it, waits for its health
check and runs the [service check](../monitoring/#health-checks). `--no-start` stops after the
files. Use the same image as the backup (`MANIFEST` and the script say which) or a newer one:
an older binary refuses a newer database.

### Recovery drill

Rehearse a full recovery before you need one, on a fresh machine, and time it: the restore
script prints how long the restore and its checks took, and the time to prepare the machine
is the rest of your recovery time.

1. **Prepare a fresh machine** with steps 1 to 4 of [Production on a VPS](../production-vps/),
   `/opt/cicord` with `compose.yaml` and `.env` (the same `CICORD_IMAGE` as the backup), and
   `05-backup.sh`. Keep its Caddy ports closed, or use a test tailnet: during the drill, only
   one runtime may run with the same bot tokens (two runtimes would both reconcile the same
   servers). Stop the production runtime for the drill, or restore into a staging setup with
   its own Discord application.
2. **Restore** the latest off-host copy with the key from the password manager:
   `sudo sh 07-restore.sh <archive>`. It ends with `restore verified in N s`.
3. **Verify the data**: open the admin console; the servers are `Synced` and `Healthy`, the
   plugins `Healthy`, a known ticket (or other plugin data, such as a member's XP) is there,
   and the audit log and sync history go back to the backup time.
4. **Verify the resource map**: the next plan must be empty. From the configuration
   repository, with the runtime token:
   `CICORD_RUNTIME_URL=<admin URL> cicord plan --remote --detailed-exitcode` exits `0`
   ("No changes."). Any `=` (adopt) or `+` (create) line means the map was not restored.
5. **Verify alerts**: `docker exec cicord cicord alert test --data-dir /data`.
6. **Write down** the time each step took, then start the production runtime again (or
   destroy the drill machine).

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
  `repo.git`, then start the runtime (on the VPS, `07-restore.sh` does all of it).

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
so nothing is duplicated; but plugin data, sessions, history and analytics are gone, and
renamed resources may not match. For a local state database (`.cicord/state.db` of the CLI),
`cicord adopt` rebuilds the map by name, with confirmation.

Moving a runtime to another machine means copying the data directory: it holds the SQLite state
and the secret store, their key, and, for the embedded provider, the repository. With
PostgreSQL, point the new machine at the same database and copy the data directory for its
keys and repository.

## The secret store

The setup wizard stores what you paste (bot tokens, the GitHub token or GitHub App, the OAuth
client secret) in an encrypted store inside the state store (SQLite or PostgreSQL), instead of
asking you to manage environment variables:

| Secret | Name |
|---|---|
| Bot token | `DISCORD_TOKEN_<BOT>`, the `token.secretRef` of the generated `bot.yaml` |
| GitHub token | `CICORD_GIT_TOKEN` |
| GitHub App ID | `CICORD_GITHUB_APP_ID` |
| GitHub App private key (PEM) | `CICORD_GITHUB_APP_PRIVATE_KEY` |
| GitHub App installation ID | `CICORD_GITHUB_APP_INSTALLATION_ID` (discovered by the wizard) |
| OAuth client secret | `CICORD_DISCORD_CLIENT_SECRET` |
| GitHub webhook secret | `CICORD_WEBHOOK_SECRET` (`cicord secret set`) |
| Runtime token of the CLI and CI | `CICORD_RUNTIME_TOKEN` (`cicord secret set`) |
| Alert webhook URL | `CICORD_ALERT_WEBHOOK` (`cicord secret set`, see [Alerts](../monitoring/#alerts)) |
| Private registry token | `CICORD_REGISTRY_<NAME>_TOKEN` (also from the Store) |

- **Encryption**: AES-256-GCM with a random nonce per write; the secret's name is
  authenticated too, so a ciphertext moved to another name fails to decrypt.
- **Key**: `CICORD_SECRET_KEY` (base64 or hex of exactly 32 bytes, e.g.
  `openssl rand -base64 32`), otherwise `<data dir>/secret.key`, generated with mode 0600. A
  wrong key fails at startup.
- **Resolution**: the runtime reads `token.secretRef`, `CICORD_GIT_TOKEN`, the
  `CICORD_GITHUB_APP_*` secrets, the OAuth client secret, `CICORD_WEBHOOK_SECRET`,
  `CICORD_RUNTIME_TOKEN` and `CICORD_ALERT_WEBHOOK` from the **environment first, then the
  store** (the last three at startup). An environment variable always overrides a stored secret. A configured GitHub
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
| `CICORD_WEBHOOK_SECRET` | a new random value (`openssl rand -hex 32`), set in the GitHub App (or repository) webhook settings too | `cicord secret set CICORD_WEBHOOK_SECRET`, restart the runtime |
| `CICORD_RUNTIME_TOKEN` (CI) | a new random value (`openssl rand -hex 32`) | update the CI secret, `cicord secret set CICORD_RUNTIME_TOKEN`, restart the runtime |
| `CICORD_ALERT_WEBHOOK` | delete the webhook in the Discord channel settings (or at the receiver) and create a new one | `cicord secret set CICORD_ALERT_WEBHOOK`, restart the runtime, `cicord alert test` |
| the store's key (`secret.key`) | `cicord secret rotate-key` re-encrypts every secret with a new key | store the new key in the password manager, delete the old copy |
| `link.key` | delete the file (or change `CICORD_LINK_SECRET`) and restart | portal links already sent stop working |
