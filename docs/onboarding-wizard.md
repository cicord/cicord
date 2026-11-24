# Setup wizard (admin onboarding)

Getting from "nothing" to "a server Cicord can sync" took ~10 manual steps during the first
real test: create an application, copy IDs, reset the token, intents, invite with the right
permissions, move the bot role, env vars, repo, first sync, first plugin. The setup wizard
guides a newcomer through all of it in the admin UI and **verifies each step
automatically**. This page describes the wizard as built. The API is in
[spec/api.md §10](../spec/api.md#10-setup-wizard-catalog-and-diagnostics).

Code: `internal/setup` (state machine, probe, checks, repository, installer, simulator
environment), `internal/secrets` (secret store), `internal/catalog` + `examples/catalog.go`
(bundled plugins), `internal/api/setup.go` (HTTP), `internal/cli/setup.go` (boot and
activation), `packages/admin/src/setup` (UI), `packages/admin/src/pages/Diagnostics.tsx`.

## Entry point: setup mode

| Start | Result |
|---|---|
| `cicord.yaml` in the directory | Normal boot. Setup mode is never entered; `cicord dev --setup` fails. |
| No `cicord.yaml`, and the setup state of the data dir says *activated* (`cicord serve`, embedded or GitHub repository) | Boots from the repository the wizard created. If the wizard was not finished, a new setup link is printed so it resumes. |
| No `cicord.yaml`, no setup state, but the data dir has a sync history or a `repo.git` | **Refused**: the data dir belongs to an existing deployment, so the wizard must not rewrite its resource map. Run `cicord serve` in its repository, or use another `--data-dir`. |
| Otherwise | **Setup mode** |

In setup mode the runtime serves only these:

- the admin UI;
- `/api/v1/setup/*`, `/api/v1/catalog`;
- `/healthz` (and `/readyz`, which answers `503 setup mode`).

Every other `/api/*` and `/auth/*` path answers `503 setup_required`, and `/` redirects to
`/admin/setup`. The admin console notices `setup_required` and shows the wizard.

The runtime prints a **one-time setup link** in the logs, Jupyter style:

```
Cicord is not set up yet: setup mode on http://127.0.0.1:7420
  open the setup wizard: http://127.0.0.1:7420/admin/setup?token=…
```

The UI exchanges the token for a setup session (`POST /api/v1/setup/session`), then removes
it from the address bar and history (`history.replaceState`). A restart prints a new link.

The wizard state (step, answers, never secret values) is stored in the state store, KV
scope `cicord:setup`. The wizard therefore **resumes after a restart**: the same data dir and
a new link continue where you left off.

- `cicord serve [dir]`: data dir `--data-dir` (default `.cicord-data`). Repository choices:
  **Managed by Cicord** (default) and **GitHub**.
- `cicord dev [dir]`: data dir `<dir>/.cicord/dev`. Repository choices: **This folder** (the
  files are written into `dir`, hot reload) and **Managed by Cicord**.
- `cicord dev --sim --setup [dir]`: setup mode against the **Discord simulator**. The world
  contains:
  - an `owner` user;
  - a server "Pixel Pals" that already has roles (Admin, Moderator, Member), a category,
    text and voice channels, and members, so the import adopts something real.

  Buttons marked *Simulate* stand in for discord.com: create a bot, turn the privileged
  intents on, authorize the invite (optionally with the bot role at the bottom), drag the
  bot role to the top, send a message. The store is in memory, as with `cicord dev --sim`.
  Once the files are written, `cicord dev --sim dir` runs that repository normally.

### Activation without restart

The HTTP server holds a swappable handler. When the Repository step activates the
configuration, the CLI does the following, then the wizard continues on the full API:

1. builds the source, write-back and runtime (`runtimeEnv.build`);
2. starts the runtime;
3. waits for the first revision to be active;
4. swaps the setup handler for the full API, with the setup endpoints still mounted.

## Security

- **Setup token**:
  - 32 random bytes, minted on every start in setup mode (and when a finished activation
    left the wizard unfinished);
  - only its SHA-256 is kept in memory, compared in constant time;
  - consumed by the first successful exchange;
  - exchanged by `POST` (CSRF rules apply: `X-Cicord-Request`), never in a GET API call.
- **Setup session**:
  - stored in the session store (`UserID "setup"`, method `setup`, 24 h);
  - sent as its own cookie `cicord_setup` (HttpOnly, `SameSite=Strict`, Secure behind
    HTTPS);
  - never accepted as a `cicord_session` cookie.

  Until the wizard completes, the setup session is an **admin principal** on the whole API,
  like the runtime bearer token. It takes precedence over a Discord login made in the same
  browser: "Test login" with an account that is not the server owner does not lock the
  wizard out. The wizard can then use the normal endpoints (server
  resources for the schema form pickers, plan, status) before any Discord login exists.
  **Finish** marks the setup complete, revokes every setup session, and makes every setup
  endpoint (except status) answer `410 gone`. In dev mode the loopback-only Host rules still
  apply.
- **Secret store** (`internal/secrets`):
  - **Encryption.** AES-256-GCM with a random nonce per write. The secret name is the
    additional authenticated data, so a ciphertext moved to another name fails to decrypt.
  - **Key.** From `CICORD_SECRET_KEY`, the base64 or hex encoding of exactly 32 bytes
    (`openssl rand -base64 32`). Otherwise from `<data dir>/secret.key`, generated with
    mode 0600. A sentinel ciphertext makes a wrong key fail at startup.
  - **Storage.** Ciphertexts live in KV scope `cicord:secrets`.
  - **Rotation.** `Set` rotates a value. `RotateKey` re-encrypts everything under a new
    key. `Delete` removes a secret. After setup, `cicord secret ls | set NAME | rm NAME |
    rotate-key --data-dir <dir>` does the same from a terminal; values are read from
    stdin, never from arguments. A changed bot token is used from the next restart.
  - **API.** It never returns a value; secrets are listed by name and dates only.
  - **Resolution.** The runtime resolves `token.secretRef`, `CICORD_GIT_TOKEN`, the
    `CICORD_GITHUB_APP_*` secrets and the OAuth client secret **environment first, then the
    store**, so an environment variable always overrides a stored secret.
  - **What the wizard stores.** The bot token as `DISCORD_TOKEN_<BOT>` (the `secretRef` of
    the generated `bot.yaml`), the GitHub token as `CICORD_GIT_TOKEN` or the GitHub App as
    `CICORD_GITHUB_APP_ID` / `CICORD_GITHUB_APP_PRIVATE_KEY` /
    `CICORD_GITHUB_APP_INSTALLATION_ID`, and the OAuth client secret as
    `CICORD_DISCORD_CLIENT_SECRET`.
  - **Tests.** They assert that the bot token and the setup token never appear in an API
    response, in the logs or in the repository.

## Steps

Each step is validated by the runtime before moving on.

1. **Welcome**: what will happen, and a choice between "existing server" and "new
   community". For a new community, the wizard explains how to create a server first.
2. **Discord application**: numbered instructions with a link to the developer portal (New
   Application → Bot → Reset Token → Copy). The user pastes **only the token**.
   - The runtime checks it with `GET /users/@me` and `GET /oauth2/applications/@me`. These
     return the bot name and avatar, the **application ID** and the application
     **flags**.
   - Flags `GATEWAY_GUILD_MEMBERS` / `GATEWAY_MESSAGE_CONTENT` / `GATEWAY_PRESENCE` and
     their `*_LIMITED` variants (an application in fewer than 100 servers) count as
     enabled.
   - When Server Members or Message Content is off, the wizard names the exact toggles
     ("Server Members Intent", "Message Content Intent") and links to
     `/developers/applications/<id>/bot`. A **Re-check** button re-reads the flags.
   - Missing intents are a warning here (only some plugins need them). Installing a plugin
     that needs one is refused with the same guidance.
   - Bad tokens are explained: invalid or revoked, a user token, or a pasted Application ID
     or client secret.
3. **Server**: the invite URL uses the scopes `bot applications.commands` and the
   Administrator permission (`permissions=8`).
   - **Live detection.** The UI polls `GET /setup/discord/servers` every 2.5 s, which
     calls `GET /users/@me/guilds`. The server appears once *Authorize* is clicked; there is
     no ID to copy.
   - **Why REST polling, not gateway `GUILD_CREATE`:**
     - polling works before the privileged intents are on, whereas an identify with
       disallowed intents is closed with 4014;
     - it spends no identify quota;
     - it leaves no second gateway session behind when the runtime connects;
     - it cannot miss a join that happened while Cicord was stopped.

     So the Discord contract needed no new event.
   - **Checks** (with Re-check): the bot is a member; its permissions are Administrator
     or the list Cicord needs; and its **highest role is above every role Cicord will
     manage**. On failure the wizard says, for example: drag the role "Cicord" above "Admin"
     (the highest role in the way) in Server Settings → Roles. Moving on is refused while
     a check fails.
4. **Repository**: **Managed by Cicord** is a bare repository at `<data dir>/repo.git`,
   where every change is a commit. **GitHub** takes an HTTPS URL, a branch and the
   credentials, either:
   - a **personal access token** (fine-grained, on this repository: Contents, Pull requests
     and Commit statuses: Read and write), stored as `CICORD_GIT_TOKEN`; choosing it removes
     stored `CICORD_GITHUB_APP_*` secrets (they would take precedence), and it is refused
     while `CICORD_GITHUB_APP_ID` is set in the environment;
   - or a **GitHub App**: its App ID, its private key (the `.pem` content) and, optionally,
     the installation ID. The wizard signs the App JWT, discovers the installation of the
     repository on github.com (`GET /repos/{owner}/{repo}/installation`) when the ID is
     empty, and mints an installation token: GitHub rejecting the App ID or key, and an App
     not installed on the repository, are explained with what to fix. The secrets are
     stored as `CICORD_GITHUB_APP_ID`, `CICORD_GITHUB_APP_PRIVATE_KEY` and
     `CICORD_GITHUB_APP_INSTALLATION_ID` (the discovered ID, so restarts do not look it up
     again). `cicord serve --github-app-key-file` provides the key as a file instead. How
     to create the App: [deploy.md](deploy.md#github-access-token-or-github-app).

   Then:
   - the remote is checked (reachable with the credentials, no `cicord.yaml` yet);
   - an empty repository is initialized;
   - an existing branch (README…) gets the setup commit on top.

   The setup state records the method (`auth: token | githubApp`); a repository set up with
   the App refuses to start without its secrets instead of falling back to a token.

   `dev` also offers **This folder**. Then **Import and activate** runs these steps:

   1. reads the server (`reconcile.Fetch`) and runs the importer;
   2. generates the repository files:
      - `cicord.yaml`, whose write-back policy depends on the repository: everything is
        committed for embedded / local / git; on GitHub, `bots/**` and `plugins/**` are
        committed and `servers/**` opens a pull request;
      - `servers/<key>.yaml`;
      - `bots/<bot>/bot.yaml`, with the `secretRef`, the server, `intents:
        [ServerMessages]` (for the live-event check) and no plugins;
      - `README.md` and `.gitignore`;
   3. validates them and records the resource map;
   4. computes a **preflight plan** with a plan-only reconciler, before anything touches
      Discord. If it is not empty, the changes are shown and must be confirmed;
   5. closes the wizard's REST session;
   6. writes the **first commit**;
   7. activates the runtime without a restart.

   The first plan of the running runtime is shown as proof ("nothing to change").
5. **Admin login** (optional): instructions for the OAuth2 page (deep link), the
   **redirect URL** to add (copy button), and the client secret.
   - The secret is checked with Discord (client-credentials grant) before it is stored.
   - The application's registered redirects are read with the bot token: if the redirect
     URL is missing, saving fails with `redirect_missing` (Discord would otherwise answer
     "invalid redirect_uri" at the first login).
   - **Test login** opens `/auth/login` in a new tab; the wizard detects the Discord session
     (`GET /setup/oauth/test`) and shows "Signed in as …".
   - The API resolves the client secret on every login, so no restart is needed.
   - In the simulator, dev login replaces it.
   - Skipping is allowed, with a warning in `serve`.
6. **Verify**: a checklist run by the runtime (`setup.Checker`), polled every 2 s until
   everything is green:

   | Check | What it verifies |
   |---|---|
   | token | Valid; same application as `bot.yaml`; bot connected |
   | intents | Application flags vs the intents the config needs (fail) and the recommended ones (warning) |
   | member | Bot is in the server |
   | permissions | Bot's server permissions |
   | hierarchy | Bot role is above every managed role |
   | commands | Slash command registration result (recorded by the runtime) |
   | events | A member's message received through the gateway since the step started: the user is asked to send one |
   | plan | The plan is empty |
   | writeback | A no-op write-back succeeds: clone/fetch with the credentials, no commit. For `cicord dev`, the working directory is only checked for write access |

   The slower results (token and application lookup, plan, write-back probe) are cached for
   15 s per server, so polling stays cheap for Discord and git. The live checks (member,
   permissions, hierarchy, commands, events) run every time.

7. **First plugin**: the **bundled catalog** (`examples/catalog.go` embeds the official
   plugins at build time: welcome, onboarding, tickets, polls, levels, moderation,
   reaction-roles, audit-log, announcements).
   - Pick a plugin to review it:
     - its capabilities, in plain words;
     - its gateway intents and the websites it may call;
     - its slash commands, found by building and inspecting the plugin.
   - Configure it with the existing schema form (pickers fed by the live server).
   - **Install**:
     1. the plugin's privileged intents are checked against the application flags;
     2. the whole repository plus the plugin is validated on a copy (schema, references,
        build);
     3. write-back copies the sources to `plugins/<name>/` and appends the instance, with
        every declared capability granted, to `bots/<bot>/bot.yaml`;
     4. the wizard waits for the revision and shows the instance health, then suggests the
        slash command to try (`/poll`, `/rank`…).
8. **Done**: a summary, links to the admin console, the portal and the CLI (`cicord plan
   --remote`), and **Finish**, which completes the setup.

## Diagnostics

`GET /api/v1/servers/{server}/diagnostics` (admin) runs the same `setup.Checker` at any
time, wizard or not. The admin console has a **Diagnostics** page per server
(`/admin/servers/<key>/diagnostics`).

For the runtime side of the checks, `internal/runtime/diagnostics.go` records:

- the last member message seen per server;
- the last slash-command registration result per (bot, server).

## Plugin catalog

`internal/catalog` reads the embedded plugins. Embed patterns are explicit (`plugin.yaml`,
`config.schema.json`, `tsconfig.json`, `src/*.ts`), so `node_modules` or build output can
never end up in the binary. `tsconfig.json` is made portable (its `paths` entry pointing to
the Cicord repository is dropped).

`GET /api/v1/catalog` lists the plugins for admins. This is the seam where registries
(`spec.registries`, the Store page) will add sources.

## Known gaps

- After a restart, the persisted boot uses the public URL the wizard ran on
  (`--public-url` / `CICORD_PUBLIC_URL` override it).
- If the first activation fails, **Import** can be clicked again: it reuses the running
  runtime, which keeps following the repository.
- In `cicord dev`, a restart between the first commit and the activation resumes the
  wizard as activated, since the runtime now runs the files.

- GitHub authentication is a personal access token or a GitHub App created by hand (no
  App manifest flow yet); no webhook setup step (polling every minute).
- The catalog lists the official plugins only; versions follow the binary.
- `cicord serve` restarted in a directory that still has no `cicord.yaml` boots from the
  persisted embedded / GitHub repository. Moving that runtime elsewhere means copying the
  data dir (it holds the repository, the state and `secret.key`).
- The wizard manages one server and one bot. Add more by editing the repository.
