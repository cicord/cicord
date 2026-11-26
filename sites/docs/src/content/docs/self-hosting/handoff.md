---
title: Handing over an instance
description: Running Cicord for a customer so that they own it and can leave - a dedicated instance, customer-owned Discord application and repository, scoped credentials, and the handoff procedure (export, transfer, rotate every credential, run it alone).
---

This guide is for an operator who runs Cicord for someone else (a customer, a community, a
client team). The goal: **the customer owns everything and can leave** with their state, at
any time, without the operator keeping a way in.

## Principles

- **One dedicated instance per customer.** Its own server or VM ([Production on a
  VPS](../production-vps/)), its own data volume, state database and `secret.key`. Never serve
  two customers from one runtime: the runtime token is **admin on every server of its
  runtime** (see [Access and roles](../../concepts/access-and-roles/#signing-in)), and the
  secret store, the backups and the alert webhook are per runtime too.
- **The customer owns the accounts.** The Discord application (and its bot) belongs to the
  customer's Discord account or [team](https://discord.com/developers/docs/topics/teams),
  with the operator as a team member while they run it. The configuration repository is in the
  customer's GitHub organization; the GitHub App that Cicord uses is the customer's (or is
  installed on that repository only). The Discord servers are owned by the customer.
- **Credentials scoped to that deployment.** Every secret of the instance serves this instance
  only: its own bot token, OAuth client secret, GitHub App key (or a fine-grained token on that
  one repository), webhook secret, runtime token (stored as a CI secret of that repository
  only), alert webhook (a channel the customer can see) and backup remote (a bucket or path of
  that instance, with a key restricted to it). Nothing is reused across customers, so one
  leak or one departure touches one instance.
- **Documented and reproducible.** The instance is the scripts of `deploy/vps/` plus `.env`,
  the configuration repository, the backups and the key: nothing lives only in the operator's
  head or laptop.

## Running the instance for the customer

While the operator runs it:

1. Install it with [Production on a VPS](../production-vps/), on a machine the customer pays
   for or that can be transferred (a provider project in the customer's account is simplest).
2. Store the secrets in the instance's encrypted store (`cicord secret set`, the setup
   wizard); keep `secret.key` in a password-manager vault **shared with the customer**.
3. Configure [alerts](../monitoring/#alerts) to a channel both can see, off-host
   [backups](../backups-and-secrets/#off-host-copies) to a remote the customer owns, and run
   the [recovery drill](../backups-and-secrets/#recovery-drill) once.

## The handoff

### 1. Export the state and the keys

On the instance:

```sh
sudo systemctl start cicord-backup.service             # a fresh archive (and its off-host copy)
ls -l /var/backups/cicord/                               # the archive to hand over
sudo docker exec cicord cicord secret ls --data-dir /data   # the secrets to rotate (names only)
```

Also collect `/opt/cicord/.env` and `compose.yaml` (no secrets), and `secret.key` and `privacy.key` from the
password manager (see [Keep the key apart](../backups-and-secrets/#what-to-back-up)).

### 2. Transfer them

- Send the archive and the key **through two different channels**: the archive as a file (or
  give access to the backup remote), the key through the shared vault or a one-time secret
  link. Never both in the same message or place.
- Either the customer restores onto their own machine with
  [`07-restore.sh`](../backups-and-secrets/#restore) (then the old instance is stopped and
  destroyed), or the machine itself is transferred (the provider's project transfer).
- Transfer what is not in the archive: the Discord application (team ownership), the GitHub
  repository and App (GitHub's repository and App transfer), the DNS records, the tailnet
  (the customer's own [Tailscale](../production-vps/#2-tailscale) account: re-run
  `02-tailscale.sh` with their auth key).

### 3. The customer rotates every credential

Everything the operator ever saw is replaced. On the instance (`sudo docker exec -i cicord …`),
each value read from stdin:

| Credential | Regenerate it | Then |
|---|---|---|
| Bot token (`DISCORD_TOKEN_<BOT>`) | Developer Portal → the application → **Bot → Reset Token** | `cicord secret set DISCORD_TOKEN_<BOT> --data-dir /data` |
| OAuth client secret | Developer Portal → **OAuth2 → Reset Secret** | `cicord secret set CICORD_DISCORD_CLIENT_SECRET --data-dir /data` |
| GitHub App private key | App settings → **Generate a private key**, then delete every older key | `cicord secret set CICORD_GITHUB_APP_PRIVATE_KEY --data-dir /data < new.pem` |
| or the git token | revoke the operator's token, create a fine-grained one on the repository | `cicord secret set CICORD_GIT_TOKEN --data-dir /data` |
| Webhook secret | `openssl rand -hex 32`, set in the App's (or repository's) webhook settings | `cicord secret set CICORD_WEBHOOK_SECRET --data-dir /data` |
| Runtime token | `openssl rand -hex 32`, update the repository's CI secret | `cicord secret set CICORD_RUNTIME_TOKEN --data-dir /data` |
| Alert webhook | delete the old webhook, create one in the customer's channel | `cicord secret set CICORD_ALERT_WEBHOOK --data-dir /data` |
| Registry tokens, if any | at the registry | `cicord secret set CICORD_REGISTRY_<NAME>_TOKEN --data-dir /data` |
| The store key (`secret.key`) | `cicord secret rotate-key --data-dir /data` re-encrypts every secret with a new key | save the new `secret.key` in the customer's vault, delete the shared one |
| `link.key` | `sudo rm /var/lib/docker/volumes/cicord_cicord-data/_data/link.key` (regenerated at the next start) | portal links already sent stop working |
| Update token (`/etc/cicord/github-token`) | a new fine-grained token of the customer | write the file (`0600`) |
| Backup remote | new credentials for the remote (`sudo rclone config`, or a new SSH key) | `sudo systemctl start cicord-backup.service` |

Then restart the runtime (`sudo docker restart cicord`): the bot token, the runtime token, the
webhook secret and the alert webhook are read at startup. `rotate-key` needs the key file
(not `CICORD_SECRET_KEY`).

After `rotate-key`, take a backup at once (`sudo systemctl start cicord-backup.service`):
archives made before the rotation only decrypt with the **old** key. The customer keeps the
old key next to the new one for as long as they keep those older archives (14 days by
default), then deletes it. The operator's copies of old archives and of the old key give
nothing away once every secret inside them has been rotated above, but delete them anyway.

Finally, remove the operator's access: their Discord accounts from the roles that grant admin
or moderator in each server (admin console access follows Discord roles), their membership in
the Discord team, the GitHub organization and the tailnet, and their SSH keys on the machine
(`/home/*/.ssh/authorized_keys`).

### 4. The customer runs it alone

Check that the instance works with the new credentials only:

```sh
sudo docker exec cicord cicord secret ls --data-dir /data          # every UPDATED date is after the handoff
sudo docker exec cicord cicord healthcheck --service --data-dir /data   # revision, servers, bots, plugins
sudo docker exec cicord cicord alert test --data-dir /data         # alerts reach the customer
cicord plan --remote --detailed-exitcode                           # from the repository: 0, no changes
```

The automatic [updates](../production-vps/#9-updates), [backups](../backups-and-secrets/) and
[alerts](../monitoring/#alerts) keep running without the operator. The operator then deletes
their copies: local backups, the old key, any export, and the old machine.
