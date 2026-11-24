# Cicord — CI/CD for Discord

[![Discord: join the community](docs/badges/discord.svg)](https://discord.gg/qTt5S2Fe4m)
[![CI](https://github.com/cicord/cicord/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/cicord/cicord/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-cicord.dev-blueviolet.svg)](https://cicord.dev/docs/)
[![Go](https://img.shields.io/badge/go-1.27-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![Status: pre-release](docs/badges/status.svg)](#project-status)

**GitOps for Discord.** Declare your Discord servers and bots in a git repository; the Cicord
runtime reconciles them continuously, runs your bots on a shared engine, and extends them
with sandboxed TypeScript plugins.

```
git push ──▶ cicord serve ──▶ Discord
             ├─ reconciles servers (roles, categories, channels, permissions)
             ├─ runs N bots × M servers with TypeScript plugins
             ├─ admin UI (every change is a git commit or a pull request)
             └─ API for your community portal (React SDK)
```

> Status: **v1alpha1, pre-release.** The runtime, the reconciler, the plugin host, the
> simulator, the admin UI and the portal SDK work end to end; formats and APIs may still
> change. See [Project status](#project-status).

## Getting started

### Install

**Release binaries** (linux and macOS on amd64/arm64, windows/amd64) are on the
[GitHub Releases](https://github.com/cicord/cicord/releases) page: one static `cicord` with
the admin UI, the default portal and the portal kit embedded. Download an archive and
`checksums.txt`, check both, and put `cicord` on your `PATH`:

```sh
VERSION=0.1.0 OS=linux ARCH=amd64     # OS: linux | darwin, ARCH: amd64 | arm64
gh release download "v$VERSION" --repo cicord/cicord \
  --pattern "cicord_${VERSION}_${OS}_${ARCH}.tar.gz" --pattern checksums.txt
sha256sum --ignore-missing -c checksums.txt          # macOS: shasum -a 256 --ignore-missing -c checksums.txt
gh attestation verify "cicord_${VERSION}_${OS}_${ARCH}.tar.gz" --repo cicord/cicord
tar -xzf "cicord_${VERSION}_${OS}_${ARCH}.tar.gz" cicord && sudo install cicord /usr/local/bin/
cicord version                                       # cicord 0.1.0 (<commit>)
```

`gh attestation verify` checks the signed build provenance: the archive was built by this
repository's release workflow from the tagged commit. Each archive also has an SPDX SBOM
(`<archive>.sbom.json`). See [RELEASING.md](RELEASING.md#verifying-a-release).

**Container image** (linux/amd64, linux/arm64; distroless, non-root, with SBOM and
provenance attestations): `ghcr.io/cicord/cicord:0.1.0` (also `:0.1` and `:latest`).

```sh
docker run --rm ghcr.io/cicord/cicord:0.1.0 version
gh attestation verify oci://ghcr.io/cicord/cicord:0.1.0 --repo cicord/cicord
```

Running it for real (volumes, environment, reverse proxy) is in [docs/deploy.md](docs/deploy.md).
A complete, hardened install on one server (Docker, Caddy for the public portal, the admin
console on a Tailscale tailnet only, automatic updates, daily backups) is in
[`deploy/vps/`](deploy/vps/) and the tutorial
[Production on a VPS](https://cicord.dev/docs/self-hosting/production-vps/).

**`go install`** — `go install github.com/cicord/cicord/cmd/cicord@latest` builds the
command line tool **without** the embedded UIs (the admin console and the portal are
placeholders, `cicord portal init` has no kit, `cicord version` shows the commit `dev`).
It is enough for CI (`cicord validate`, `cicord plan --remote`), not for running the runtime:
use a release binary, the image, or build from source.

### Build from source

- **Go 1.27** to build the `cicord` binary (a single static binary, `CGO_ENABLED=0`).
- **Node 26 + pnpm** only to build the embedded UIs (admin UI, default portal). Plugins
  are bundled by the runtime itself: running Cicord needs no Node.js.

```sh
make all     # pnpm install + build the JS packages, copy the UIs into internal/webui, build bin/cicord
make build   # Go binary only (embeds whatever UI build is in internal/webui; placeholders otherwise)
make test    # go test -race ./... and the JS tests
```

`make build VERSION=0.1.0` sets the version printed by `cicord version` (the commit comes
from git). `docker build .` builds the same image as the release (see
[docs/deploy.md](docs/deploy.md)).

### Set up with the wizard (recommended)

Start the runtime in an empty directory: with no `cicord.yaml` it enters **setup mode** and
prints a one-time link to the setup wizard.

```sh
mkdir my-community && cd my-community
cicord serve --public-url https://cicord.example.org      # or: cicord dev (writes the files here)
#   open the setup wizard: https://cicord.example.org/admin/setup?token=…
```

The wizard walks you through every step in the browser and checks each one live:

1. Create the Discord application and paste the bot token. Cicord derives the application
   ID and tells you exactly which privileged intents to turn on.
2. Invite the bot. The server shows up by itself; permissions and the role position are
   checked ("drag the role Cicord above Admin").
3. Choose where the configuration lives: **Managed by Cicord** (a git repository in the data
   dir, no git knowledge needed) or **GitHub**. Cicord imports the server as it is. The first
   plan must be empty: everything is adopted, nothing changes on Discord. Cicord then
   activates it without a restart.
4. Optionally set up Discord login for the admin console.
5. Run the verification checklist (token, intents, permissions, hierarchy, commands, live
   events, plan, write-back).
6. Install a first plugin from the bundled catalog.

Tokens and secrets you paste are stored encrypted in the data dir (`CICORD_SECRET_KEY` or a
generated `secret.key`); environment variables still take precedence. To try the whole
flow offline, against a simulated Discord, run:

```sh
cicord dev --sim --setup ./my-community
```

Details: [docs/onboarding-wizard.md](docs/onboarding-wizard.md). The same checks are
available later on the admin **Diagnostics** page.

### Create a repository by hand

```sh
bin/cicord init my-community --server-id <discord server id> --application-id <discord application id>
cd my-community
```

This writes `cicord.yaml`, `servers/<server>.yaml` and `bots/<bot>/bot.yaml` (see
`cicord init --help` for `--server`, `--bot`, `--provider`, `--repo`). Check it offline:

```sh
cicord validate        # schemas + semantic checks; -o json for CI
```

[`examples/basic/`](examples/basic/) is a complete repository with two plugins
(`welcome`, `onboarding`).

### Try it offline: `cicord dev --sim`

```sh
cicord dev --sim
```

`cicord dev` watches the working directory (every saved change becomes a new revision) and
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
cicord plugin new plugins/hello     # plugin.yaml, config.schema.json, tsconfig.json, src/
```

Enable it in `bots/<bot>/bot.yaml` (`spec.plugins`, with the capabilities it needs in
`grants`) and `cicord dev --sim` reloads it. The guide is [docs/plugins.md](docs/plugins.md);
the SDK contract is [`spec/sdk/cicord-sdk.d.ts`](spec/sdk/cicord-sdk.d.ts) (`@cicord/sdk`, with
test helpers in `@cicord/sdk/testing`).

```ts
// plugins/welcome/src/index.ts
import { definePlugin } from "@cicord/sdk";

export default definePlugin({
  events: {
    async memberJoin(ctx, member) {
      await ctx.server.channel(ctx.config.channel).send(`Bienvenue ${member.mention} !`);
    },
  },
});
```

### Go live (manual path)

The setup wizard above does all of this for you. By hand:

1. **Create the bot** in the [Discord developer portal](https://discord.com/developers/applications):
   a new application, then *Bot* → reset and copy the token. Under *Privileged Gateway
   Intents*, enable what your plugins declare (`ServerMembers` for `memberJoin`,
   `MessageContent` to read message text); `cicord validate` warns about privileged intents.
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
   cicord serve --local --public-url https://cicord.example.org
   ```

   `--local` serves the directory as-is. Without it, `cicord serve` follows
   `cicord.yaml` `spec.source`: for a GitHub repository set `provider: github`, `repo` and
   `branch`, and `CICORD_GIT_TOKEN` or a GitHub App (fetch, write-back commits, pull
   requests and `cicord/sync` commit statuses; see [docs/deploy.md](docs/deploy.md)). The
   source is polled every minute; for instant syncs add a GitHub webhook (push events) to
   `<public-url>/hooks/github` with the secret in `CICORD_WEBHOOK_SECRET`.
4. **Admin UI login** (`<public-url>/admin/`): set `CICORD_DISCORD_CLIENT_SECRET` (the
   application's OAuth2 client secret; the client ID defaults to the first bot's
   application ID, `CICORD_DISCORD_CLIENT_ID` overrides it) and add the redirect URL
   **`<public-url>/auth/callback`** in the application's OAuth2 settings. Server owners are
   admins; `spec.access` grants admin / moderator to roles or users.
5. **CI / CLI access**: `CICORD_RUNTIME_TOKEN` enables the machine endpoints (`POST
   /api/v1/sync`, `POST /api/v1/plan`) used by `cicord plan --remote` and CI.

Always set `--public-url` to the URL users reach (OAuth redirects, portal links, Secure
cookies and the CSRF origin check depend on it). Data lives in `--data-dir`
(`.cicord-data` by default). Docker, reverse proxy and the security checklist:
[docs/deploy.md](docs/deploy.md).

### CI

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs, on every push to `main` and
every pull request: `go vet`, `go test -race`, a static build and `cicord validate
examples/basic` (Go job), and `pnpm install`, `pnpm -r build`, `pnpm -r test` (JS job).
Locally, `pnpm test` and `pnpm check` (format, lint, types, example plugins) run the same
JS checks. For a Cicord configuration repository, run `cicord validate` on pull requests and
`cicord plan --remote` against your runtime (with `CICORD_RUNTIME_TOKEN`).

## CLI

| Command | Purpose |
|---|---|
| `cicord init` | Scaffold a repository. |
| `cicord validate` | Schema + semantic validation, offline. |
| `cicord dev [--sim] [--setup]` | Local runtime with hot reload, real Discord or the simulator; setup wizard in a directory without `cicord.yaml`. |
| `cicord serve` | Production runtime (setup wizard when there is no `cicord.yaml` yet). |
| `cicord secret ls / set / rm / rotate-key` | Manage the encrypted secrets of a data dir (bot tokens, OAuth / git secrets; values read from stdin). |
| `cicord plugin new / sdk-types / add` | Scaffold a plugin, write SDK types for editors, add a registry plugin (`cicord.lock`), e.g. `cicord plugin add cicord/welcome`. |
| `cicord registry` | Plugin sources: `list / add / remove / update / search`, and `keygen / sign / verify` for signed indexes. |
| `cicord healthcheck` | Exit 0 when a runtime answers `/healthz` (container health checks). |
| `cicord plan [--remote --comment]` | Diff with Discord (locally, or through the runtime; PR comment in GitHub Actions). |
| `cicord apply / status [--wait] / sync` | Apply locally; runtime status (CI waits for a revision); trigger a sync. |
| `cicord import / adopt / mv` | Generate YAML from an existing server; rebuild the resource map; rename a key everywhere. |
| `cicord plugin build / typegen / test` | Bundle and inspect a plugin; generate config types; smoke-test it on the simulator. |

## Documentation

| Document | Content |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Components, sync lifecycle, CI/CD flow, UIs, security, roadmap |
| [docs/plugins.md](docs/plugins.md) | Plugin author guide |
| [docs/deploy.md](docs/deploy.md) | Self-hosting: data, Docker, reverse proxy, security checklist |
| [deploy/vps/](deploy/vps/) | A hardened single-server deployment: scripts, Compose, Caddy, install log |
| [docs/adr/](docs/adr/README.md) | Architecture decisions |
| [spec/](spec/README.md) | Config format semantics, reconcile rules, plugin capabilities, SDK semantics |
| [spec/api.md](spec/api.md) | Runtime HTTP API v1 |
| [spec/schemas/](spec/schemas/) | JSON Schemas: `Project`, `Server`, `Bot`, `Plugin` |
| [spec/sdk/cicord-sdk.d.ts](spec/sdk/cicord-sdk.d.ts) | Plugin SDK contract (TypeScript) |
| [examples/basic/](examples/basic/) | A complete example repository |

## A taste of the format

```yaml
# servers/ma-commu.yaml
apiVersion: cicord.dev/v1alpha1
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

**v1alpha1, pre-release.** Releases (binaries, the image `ghcr.io/cicord/cicord` and the
`@cicord/*` npm packages) are published from tags `v*` ([RELEASING.md](RELEASING.md),
[CHANGELOG.md](CHANGELOG.md)); formats and APIs may still change between `0.x` versions.

Done:
- Config loader/validator; local and git sources (GitHub, plain git, embedded repo) with
  revisions and hot swap.
- Server reconciler with Discord settings parity (plan / apply / drift / self-heal, adoption,
  deletion protection, live drift for roles, channels, AutoMod, emojis, stickers, sounds).
- Bot host over disgo; plugin host (QuickJS-ng on WebAssembly, hard memory and CPU limits) with capabilities, KV storage, commands, components,
  modals, schedules, tasks, cross-plugin events and typed portal routes.
- SQLite or PostgreSQL state (one conformance suite for both), git write-back (commits and
  GitHub pull requests), GitHub access with a token or a GitHub App (short-lived installation
  tokens) and `cicord/sync` commit statuses, HTTP API v1.
- Self-hosting: a separate private admin URL (admin console and admin API answer only there),
  `cicord healthcheck`, and a hardened single-server deployment ([`deploy/vps/`](deploy/vps/)).
- Admin console (data tables, per-plugin pages, Store, simulator panel), setup wizard
  (imports an existing server with its files, empty first plan).
- Plugin Store: the 9 official plugins, bundled and from the signed official registry
  [cicord/cicord-plugins](https://github.com/cicord/cicord-plugins), git registries (private
  ones with tokens, monorepos), computed trust, permission review, install / update /
  uninstall, `cicord plugin publish`, `cicord registry keygen / sign / verify`.
- Portal SDK (`@cicord/client`, `@cicord/react`, `@cicord/ui`, `create-cicord-portal`), published
  to npm with provenance by the release workflow.
- Release pipeline: reproducible cross-platform binaries, multi-arch image, SBOMs and build
  provenance attestations, npm trusted publishing.
- CLI: `init`, `validate`, `plan`, `apply`, `status`, `sync`, `healthcheck`, `import`,
  `adopt`, `mv`, `dev`, `serve`, `secret`, `registry`, `plugin` (new / build / typegen / test /
  add / update / publish), `portal`.

A real-world configuration: the official community server's repository,
[cicord/cicord-community](https://github.com/cicord/cicord-community).

Next: the first release (v0.1.0), production hardening (metrics), then the hosted offering. See [TODO.md](TODO.md) and the
[roadmap](ARCHITECTURE.md#12-roadmap).

Websites: the landing page (`sites/landing`) and the documentation (`sites/docs`) are Astro
sites, deployed together by `.github/workflows/sites.yml`.

## Contributing

Questions, ideas or a plugin to show? [Join the Cicord community on Discord](https://discord.gg/qTt5S2Fe4m).

Contributions are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md). Report security issues privately as described in
[SECURITY.md](SECURITY.md).

## License

Cicord is licensed under the [Apache License 2.0](LICENSE): the runtime, the CLI, the admin
console, the SDKs, the official plugins and the sites. See [NOTICE](NOTICE) and
[ADR-0013](docs/adr/0013-licensing.md). Cicord is an independent project, not affiliated with
Discord.
