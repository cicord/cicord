# Setup wizard (admin onboarding) — design

Getting from "nothing" to "a server CODI can sync" took ~10 manual steps during the first
real test (create an application, copy IDs, reset the token, intents, invite with the right
permissions, move the bot role, env vars, repo, first sync, first plugin). The admin UI must
guide a newcomer through all of it and **verify each step automatically**.

## Entry point: setup mode
- `codi serve` (or `codi dev`) started **without a configured repository** (no `codi.yaml`
  and no state) enters **setup mode**: only the admin UI and `/api/v1/setup/*` are served.
- Setup endpoints are protected by a **one-time setup token** printed in the logs
  (`http://<host>/admin/setup?token=…`, Jupyter style), exchanged for an admin session. In
  dev mode, loopback-only rules still apply.
- Setup state (current step, answers) is persisted in the state store, so the wizard
  resumes after a restart.

## Steps (each one validated by the runtime before moving on)
1. **Welcome** — what CODI will do; choose "new community" or "existing server".
2. **Discord application** — link to the developer portal with exact instructions (create the
   application, Bot tab, reset token). The user pastes the **token** only:
   - the runtime validates it (`GET /users/@me`, `GET /applications/@me`) and derives the
     **application ID** itself (no copy/paste of IDs);
   - it reads the application **flags** to check the privileged intents (Server Members,
     Message Content) and shows a precise "enable this toggle" message until they are on;
   - the token is stored in the **runtime secret store** (encrypted at rest in the data dir;
     key from `CODI_SECRET_KEY` or a generated key file), referenced by `secretRef` as today.
     Environment variables keep working and take precedence.
3. **Server** — the wizard generates the **invite URL** (scopes `bot applications.commands`,
   Administrator) and listens on the gateway: as soon as the bot joins a server it appears in
   the list (no server ID to copy). Option: "Create a new Discord server" (link + steps).
   Checks: bot is in the server, has the required permissions, its role is high enough
   (explains how to drag it to the top, re-checks live).
4. **Repository** — where the configuration lives:
   - **Managed by CODI** (default, no git knowledge needed): embedded repository in the data
     dir; every change is still a commit (history in the admin UI);
   - **GitHub**: repository URL + token (GitHub App later); the wizard can create the repo
     content and push the first commit; optional webhook instructions.
   The wizard runs `codi import` on the chosen server → `servers/<key>.yaml`, `bots/<bot>/bot.yaml`,
   `codi.yaml` → first commit → the runtime activates it. The first plan must be empty
   (everything adopted): shown as proof.
5. **Admin login** (optional) — OAuth client secret (stored in the secret store) + the redirect
   URL to add in the developer portal (copy button); "test login" button.
6. **Verify** — a checklist run by the runtime: token, intents, bot in server, permissions,
   role hierarchy, commands registration, gateway events received (asks the user to send a
   message in a channel and detects it), plan empty, write-back works.
7. **First plugin** — a catalog of official plugins (bundled in the binary until the registry
   exists: welcome, onboarding, tickets, polls, levels, moderation): pick one, review its
   capabilities, configure it with the schema form, install (commit) → active; suggest
   trying its slash command in Discord.
8. **Done** — dashboard; links to the portal, docs, CLI usage (`codi plan`, CI).

## API (sketch)
`/api/v1/setup/status`, `/setup/discord/token` (validate + store), `/setup/discord/invite`,
`/setup/discord/servers` (SSE of servers the bot is in), `/setup/server/check`,
`/setup/repo` (embedded | github), `/setup/import`, `/setup/oauth`, `/setup/verify`,
`/api/v1/catalog` (plugins available to install), `/setup/plugins/install`.

## Also needed
- Secret store in `internal/state` (+ resolver: env first, then store).
- Plugin catalog (embedded official plugins) — shared with the future Store page.
- Re-runnable checks from the admin UI later ("Diagnostics" page) — same checks as step 6.
