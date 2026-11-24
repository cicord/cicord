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
             ├─ reconciles servers (roles, categories, channels, permissions)
             ├─ runs N bots × M servers with TypeScript plugins
             ├─ admin UI (every change is a git commit or a pull request)
             └─ API for your community portal (React SDK)
```

> **v1alpha1, pre-release:** everything works end to end, but formats and APIs may still
> change between `0.x` versions.

## Documentation

Everything is on **[cicord.dev/docs](https://cicord.dev/docs/)**:

| | |
|---|---|
| [Installation](https://cicord.dev/docs/getting-started/installation/) | Release binaries, the container image `ghcr.io/cicord/cicord`, verifying them, building from source |
| [Quick start](https://cicord.dev/docs/getting-started/quick-start/) | The setup wizard, or a repository by hand; `cicord dev --sim` to try it offline |
| [Configuration](https://cicord.dev/docs/configuration/project/) | `cicord.yaml`, servers, bots |
| [Plugins](https://cicord.dev/docs/plugins/overview/) | The official plugins, writing, testing and publishing your own |
| [Self-hosting](https://cicord.dev/docs/self-hosting/deploy/) | Deploying, a hardened single-server setup, security, backups, upgrading |
| [Reference](https://cicord.dev/docs/reference/cli/) | CLI, HTTP API, Discord parity |
| [Project](https://cicord.dev/docs/project/status/) | Status, architecture, design decisions, contributing, releasing |

Quick install (Linux or macOS, with the [GitHub CLI](https://cli.github.com/) to verify the
build provenance):

```sh
VERSION=0.1.1 OS=linux ARCH=amd64     # OS: linux | darwin, ARCH: amd64 | arm64
gh release download "v$VERSION" --repo cicord/cicord --pattern "cicord_${VERSION}_${OS}_${ARCH}.tar.gz"
gh attestation verify "cicord_${VERSION}_${OS}_${ARCH}.tar.gz" --repo cicord/cicord
tar -xzf "cicord_${VERSION}_${OS}_${ARCH}.tar.gz" cicord && sudo install cicord /usr/local/bin/
```

Or the image: `docker run --rm ghcr.io/cicord/cicord:0.1.1 version`. A real-world
configuration: the official community server's repository,
[cicord/cicord-community](https://github.com/cicord/cicord-community).

## Community and contributing

Questions, ideas or a plugin to show? [Join the Cicord community on Discord](https://discord.gg/qTt5S2Fe4m).
Contributions are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md). Report security issues privately as described in
[SECURITY.md](SECURITY.md). Release notes: [CHANGELOG.md](CHANGELOG.md).

## License

Cicord is licensed under the [Apache License 2.0](LICENSE): the runtime, the CLI, the admin
console, the SDKs, the official plugins and the sites. See [NOTICE](NOTICE). Cicord is an
independent project, not affiliated with Discord.
