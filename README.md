# CODI — CI/CD Oriented Discord Integration

**GitOps for Discord.** Declare your Discord servers and bots in a git repository; the CODI
runtime reconciles them continuously, runs your bots on a shared engine, and extends them
with sandboxed TypeScript plugins.

```
git push ──▶ codi serve ──▶ Discord
             ├─ reconciles servers (roles, categories, channels, permissions)
             ├─ runs N bots × M servers with TypeScript plugins
             ├─ admin UI (every change is a git commit or a pull request)
             └─ API for your community portal (React SDK)
```

> Status: **v1alpha1, pre-release.** The runtime, the reconciler, the plugin host, the
> simulator, the admin UI and the portal SDK work end to end; formats and APIs may still
> change. See [Project status](#project-status).

## Getting started

### Prerequisites

- **Go 1.27** to build the `codi` binary (a single static binary, `CGO_ENABLED=0`).
- **Node 24 + pnpm** only to build the embedded UIs (admin UI, default portal). Plugins
  are bundled by the runtime itself: running CODI needs no Node.js.

### Build

```sh
make all     # pnpm install + build the JS packages, copy the UIs into internal/webui, build bin/codi
make build   # Go binary only (embeds whatever UI build is in internal/webui; placeholders otherwise)
make test    # go test -race ./... and the JS tests
```

A container image is described in [docs/deploy.md](docs/deploy.md) (`docker build .`).

### Create a repository

```sh
bin/codi init my-community --server-id <discord server id> --application-id <discord application id>
cd my-community
```

This writes `codi.yaml`, `servers/<server>.yaml` and `bots/<bot>/bot.yaml` (see
`codi init --help` for `--server`, `--bot`, `--provider`, `--repo`). Check it offline:

```sh
codi validate        # schemas + semantic checks; -o json for CI
```

[`examples/basic/`](examples/basic/) is a complete repository with two plugins
(`welcome`, `onboarding`).

### Try it offline: `codi dev --sim`

```sh
codi dev --sim
```

`codi dev` watches the working directory (every saved change becomes a new revision) and
writes admin UI edits back to your files. With `--sim`, Discord is replaced by an
in-memory simulator seeded from your YAML: each server (with its `discordId`) owned by a
user `owner`, each bot invited with Administrator, and the users `alice`, `bob` and
`charlie`. No token is needed.

Open **http://127.0.0.1:7420/admin/** (use `127.0.0.1`, not `localhost`: the default
public URL is `http://127.0.0.1:7420` and state-changing requests from another origin are
rejected; pass `--public-url http://localhost:7420` to use `localhost`), sign in as a
simulated user, and use the
**Simulator** panel to join servers, send messages, run slash commands, click buttons and
submit modals while watching the reconciler and your plugins react. Dev mode only answers
loopback hosts and is not meant to be exposed (see
[ADR-0017](docs/adr/0017-dev-mode-with-simulator.md)).

### Write a plugin

```sh
codi plugin new plugins/hello     # plugin.yaml, config.schema.json, tsconfig.json, src/
```

Enable it in `bots/<bot>/bot.yaml` (`spec.plugins`, with the capabilities it needs in
`grants`) and `codi dev --sim` reloads it. The guide is [docs/plugins.md](docs/plugins.md);
the SDK contract is [`spec/sdk/codi-sdk.d.ts`](spec/sdk/codi-sdk.d.ts) (`@codi/sdk`, with
test helpers in `@codi/sdk/testing`).

```ts
// plugins/welcome/src/index.ts
import { definePlugin } from "@codi/sdk";

export default definePlugin({
  events: {
    async memberJoin(ctx, member) {
      await ctx.server.channel(ctx.config.channel).send(`Bienvenue ${member.mention} !`);
    },
  },
});
```

### Go live

1. **Create the bot** in the [Discord developer portal](https://discord.com/developers/applications):
   a new application, then *Bot* → reset and copy the token. Under *Privileged Gateway
   Intents*, enable what your plugins declare (`ServerMembers` for `memberJoin`,
   `MessageContent` to read message text); `codi validate` warns about privileged intents.
   Put the application ID in `bot.yaml` (`spec.applicationId`) and the server ID in the
   server YAML (`spec.discordId`).
2. **Invite it** with the `bot` and `applications.commands` scopes and the *Administrator*
   permission (the reconciler manages roles, channels and permissions), e.g.
   `https://discord.com/oauth2/authorize?client_id=<application id>&scope=bot+applications.commands&permissions=8`.
   Put the bot's role high enough in the hierarchy: it can only manage roles below its own.
3. **Run the runtime** with the token in the variable named by `token.secretRef`
   (`DISCORD_TOKEN_<BOT>`):

   ```sh
   export DISCORD_TOKEN_MODERATOR=...                # one per bot
   codi serve --local --public-url https://codi.example.org
   ```

   `--local` serves the directory as-is. Without it, `codi serve` follows
   `codi.yaml` `spec.source`: for a GitHub repository set `provider: github`, `repo` and
   `branch`, and `CODI_GIT_TOKEN` (fetch, write-back commits and pull requests). The
   source is polled every minute; for instant syncs add a GitHub webhook (push events) to
   `<public-url>/hooks/github` with the secret in `CODI_WEBHOOK_SECRET`.
4. **Admin UI login** (`<public-url>/admin/`): set `CODI_DISCORD_CLIENT_SECRET` (the
   application's OAuth2 client secret; the client ID defaults to the first bot's
   application ID, `CODI_DISCORD_CLIENT_ID` overrides it) and add the redirect URL
   **`<public-url>/auth/callback`** in the application's OAuth2 settings. Server owners are
   admins; `spec.access` grants admin / moderator to roles or users.
5. **CI / CLI access**: `CODI_RUNTIME_TOKEN` enables the machine endpoints (`POST
   /api/v1/sync`, `POST /api/v1/plan`) used by `codi plan --remote` and CI.

Always set `--public-url` to the URL users reach (OAuth redirects, portal links, Secure
cookies and the CSRF origin check depend on it). Data lives in `--data-dir`
(`.codi-data` by default). Docker, reverse proxy and the security checklist:
[docs/deploy.md](docs/deploy.md).

### CI

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs, on every push to `main` and
every pull request: `go vet`, `go test -race`, a static build and `codi validate
examples/basic` (Go job), and `pnpm install`, `pnpm -r build`, `pnpm -r test` (JS job).
Locally, `pnpm test` and `pnpm check` (format, lint, types, example plugins) run the same
JS checks. For a CODI configuration repository, run `codi validate` on pull requests and
`codi plan --remote` against your runtime (with `CODI_RUNTIME_TOKEN`).

## CLI

| Command | Purpose |
|---|---|
| `codi init` | Scaffold a repository. |
| `codi validate` | Schema + semantic validation, offline. |
| `codi dev [--sim]` | Local runtime with hot reload, real Discord or the simulator. |
| `codi serve` | Production runtime. |
| `codi plugin new / sdk-types / add` | Scaffold a plugin, write SDK types for editors, add a registry plugin (`codi.lock`). |
| `codi plan / apply / status / sync / import / adopt / mv` | Being added: see [ARCHITECTURE.md §7](ARCHITECTURE.md#7-cli). |
| `codi plugin build / typegen / test` | Being added. |

## Documentation

| Document | Content |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Components, sync lifecycle, CI/CD flow, UIs, security, roadmap |
| [docs/plugins.md](docs/plugins.md) | Plugin author guide |
| [docs/deploy.md](docs/deploy.md) | Self-hosting: data, Docker, reverse proxy, security checklist |
| [docs/adr/](docs/adr/README.md) | Architecture decisions |
| [spec/](spec/README.md) | Config format semantics, reconcile rules, plugin capabilities, SDK semantics |
| [spec/api.md](spec/api.md) | Runtime HTTP API v1 |
| [spec/schemas/](spec/schemas/) | JSON Schemas: `Project`, `Server`, `Bot`, `Plugin` |
| [spec/sdk/codi-sdk.d.ts](spec/sdk/codi-sdk.d.ts) | Plugin SDK contract (TypeScript) |
| [examples/basic/](examples/basic/) | A complete example repository |

## A taste of the format

```yaml
# servers/ma-commu.yaml
apiVersion: codi.dev/v1alpha1
kind: Server
metadata:
  name: ma-commu
spec:
  discordId: "112233445566778899"
  roles:
    modo: { name: Modo, color: "#e67e22", permissions: [KickMembers, ManageMessages] }
  channels:
    general: { name: général, type: text }
```

## Project status

Done: config loader/validator, local and git sources (GitHub, plain git, embedded repo)
with revisions and hot swap, server reconciler (plan / apply / drift / self-heal, adoption,
deletion protection), bot host over disgo, Sobek plugin host with capabilities, KV storage,
commands, components, modals, schedules, tasks and portal routes, SQLite state, git
write-back (commits and GitHub pull requests), HTTP API v1, admin UI with schema-generated
settings and the simulator panel, portal SDK (`@codi/client`, `@codi/react`, `@codi/ui`,
`create-codi-portal`), `codi plugin add` against git registries.

In progress: the remaining CLI commands (`plan`, `apply`, `status`, `sync`, `import`,
`adopt`, `mv`, `plugin build | typegen | test`). Not started: registry publishing, SaaS.
See the [roadmap](ARCHITECTURE.md#12-roadmap).

License: see [ADR-0013](docs/adr/0013-licensing.md).
