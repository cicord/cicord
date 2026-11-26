# Cicord — CI/CD for Discord

[![Discord: join the community](docs/badges/discord.svg)](https://discord.gg/qTt5S2Fe4m)
[![CI](https://github.com/cicord/cicord/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/cicord/cicord/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-cicord.dev-blueviolet.svg)](https://cicord.dev/docs/)
[![Go](https://img.shields.io/badge/go-1.27-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![Status: pre-release](docs/badges/status.svg)](https://cicord.dev/docs/project/status/)

**GitOps for Discord.** Declare your Discord servers and bots in a git repository; the Cicord
runtime reconciles them continuously, runs your bots on a shared engine, and extends them
with sandboxed TypeScript plugins.

```
git push ──▶ cicord serve ──▶ Discord
             ├─ reconciles servers (roles, categories, channels, permissions, AutoMod…)
             ├─ reports drift or reverts it (selfHeal); pause enforcement in an emergency
             ├─ runs N bots × M servers with TypeScript plugins
             ├─ admin UI (every change is a git commit or a pull request)
             ├─ alerts to a Discord or JSON webhook
             └─ API for your community portal (React SDK)
```

> **v1alpha1, pre-release:** everything works end to end, but formats and APIs may still
> change between `0.x` versions.

## Documentation

Everything is on **[cicord.dev/docs](https://cicord.dev/docs/)**:

| | |
|---|---|
| [Installation](https://cicord.dev/docs/getting-started/installation/) | Install script, Homebrew, npm, the container image `ghcr.io/cicord/cicord`, verified downloads, building from source |
| [Quick start](https://cicord.dev/docs/getting-started/quick-start/) | The setup wizard, or a repository by hand; `cicord dev --sim` to try it offline; [inspect an existing server](https://cicord.dev/docs/getting-started/evaluate-a-server/) read-only first |
| [Configuration](https://cicord.dev/docs/configuration/project/) | `cicord.yaml`, servers, bots |
| [Plugins](https://cicord.dev/docs/plugins/overview/) | The official plugins, writing, testing and publishing your own |
| [Self-hosting](https://cicord.dev/docs/self-hosting/deploy/) | Deploying, a hardened single-server setup, security, backups and restores, monitoring and alerts, upgrading, handing an instance over |
| [Reference](https://cicord.dev/docs/reference/cli/) | CLI, HTTP API, Discord parity |
| [Project](https://cicord.dev/docs/project/status/) | Status, architecture, design decisions, contributing, releasing |

Install (Linux or macOS; the script checks the SHA-256 of the release archive and, with the
[GitHub CLI](https://cli.github.com/) logged in, its build provenance):

```sh
curl -fsSL https://cicord.dev/install.sh | sh
```

Or `brew install cicord/tap/cicord` (macOS, Linux), or `npm i -g @cicord/cli` (also
Windows). Verified manual downloads, `go install` and building
from source: [Installation](https://cicord.dev/docs/getting-started/installation/).

Or the image: `docker run --rm ghcr.io/cicord/cicord:0.2.2 version`. A real-world
configuration: the official community server's repository,
[cicord/cicord-community](https://github.com/cicord/cicord-community).

## Community and contributing

Questions, ideas or a plugin to show? [Join the Cicord community on Discord](https://discord.gg/qTt5S2Fe4m).
Contributions are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md). Report security issues privately as described in
[SECURITY.md](SECURITY.md). Release notes: [CHANGELOG.md](CHANGELOG.md).

## License

Copyright 2026 Cicord team. Cicord is licensed under the [Apache License 2.0](LICENSE): the
runtime, the CLI, the admin console, the SDKs, the official plugins and the sites. See
[NOTICE](NOTICE). Cicord is an independent project, not affiliated with Discord.

Cicord is provided "AS IS", WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied, including, without limitation, any warranties or conditions of title,
non-infringement, merchantability or fitness for a particular purpose. In no event shall the
Cicord team or its contributors be liable for any claim, damages or other liability, direct or
indirect, arising from the use of or the inability to use the software (including the loss of
data, of Discord servers or of access to them), even if advised of the possibility of such
damages. You are solely responsible for determining whether using Cicord is appropriate and
assume all risks of using it. See sections 7 and 8 of the [License](LICENSE).
