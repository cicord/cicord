---
title: Contributing
description: How to report bugs and propose changes, build Cicord from source, run the Go and JavaScript tests, and find your way around the repository.
---

Bug reports, ideas, documentation fixes, plugins and code are all welcome.

## Before you start

- **Questions and ideas**: ask on the [community Discord](https://discord.gg/qTt5S2Fe4m), or
  open a [discussion](https://github.com/cicord/cicord/discussions) or a feature request issue.
- **Bugs**: open a bug report with the steps to reproduce, the version (`cicord version`) and
  the relevant logs (remove tokens and secrets first).
- **Security issues**: never in a public issue: report them privately through
  [GitHub security advisories](https://github.com/cicord/cicord/security/advisories/new)
  (see [SECURITY.md](https://github.com/cicord/cicord/blob/main/SECURITY.md)).
- **Larger changes** (a new capability, a format change, a new dependency): open an issue
  first so the design can be discussed. Past decisions are recorded in
  [Design decisions](../decisions/).

## Prerequisites

Go 1.27 (`go.mod`), Node 26 (`.node-version`) and pnpm 12 (pinned by `packageManager` in the
root `package.json`). See [Installation → Build from source](../../getting-started/installation/#build-from-source).

## Make targets

| Target | What it does |
|---|---|
| `make all` | `make ui` then `make build`: the complete binary in `bin/cicord`. |
| `make ui` | `pnpm install --frozen-lockfile`, builds the JS packages, then `scripts/sync-ui.sh` copies the admin console, the reference portal and the portal kit (`@cicord/*` tarballs and template) into `internal/webui`, where `go build` embeds them. |
| `make build` | The Go binary only (`CGO_ENABLED=0`, version and commit in the ldflags). Embeds whatever UI build is in `internal/webui`. |
| `make test` | `make test-go` and `make test-js`. |
| `make test-go` | `go test -race ./...` |
| `make test-js` | `pnpm -r test` |
| `make vet` | `go vet ./...` |
| `make check` | `vet` and `test`. |
| `make lint` | The static analysis CI runs (below): golangci-lint, govulncheck, `pnpm run check`, Knip, jscpd, Semgrep, Gitleaks. |
| `make hooks` | Once per clone: a pre-commit hook runs Gitleaks on the staged changes and refuses a commit that adds a secret. |
| `make release VERSION=x.y.z` | Prepares a release commit ([Releasing](../releasing/)). |
| `make clean` | Removes `bin/`. |

`VERSION` defaults to the current release; `make build VERSION=0.2.0` overrides it.

## JavaScript workspace

The JS side is a pnpm workspace (`packages/*` and `sites/*`) using Vite+ (`vp`) for building,
testing, linting and formatting.

```sh
pnpm test        # builds the packages, runs every package's tests and the example plugins' unit tests
pnpm run check   # builds, then format, lint, types, and type-checks every example plugin
pnpm exec knip   # unused files, exports and dependencies
pnpm exec vp test examples/showcase/plugins/polls   # one plugin's unit tests
```

## What CI runs

On every push to `main` and every pull request:

- **Go**: `gofmt -l` (must be empty), `scripts/check-docs.sh` (no Markdown documentation
  outside `sites/docs`), `go vet`, `go test -race`, a static build, and `cicord validate` on
  `examples/basic` and `examples/showcase`.
- **PostgreSQL**: the state conformance suite and the CLI's PostgreSQL tests against a
  PostgreSQL 19 service (the Go job runs them on SQLite only).
- **JS**: `pnpm install --frozen-lockfile`, `pnpm run check` (builds every package and site,
  then format, lint, types and the example plugins) and `pnpm test`.
- **Docker**: the container image is built (not pushed) and smoke-tested (`cicord version`).
- **Static analysis**: golangci-lint (`.golangci.yml`: staticcheck, errcheck, gosec, dupl,
  errorlint, noctx…) and govulncheck for Go; oxlint (correctness, suspicious and perf
  categories, React hooks, jsx-a11y), [Knip](https://knip.dev) (unused files, exports and
  dependencies) and jscpd (duplication) for TypeScript; Semgrep (Go, React, TypeScript,
  JavaScript and security-audit rules) on everything; Gitleaks on the whole git history.
  `make lint` runs the same tools locally.

CI fails on any finding. A justified exception is written next to the code, with its reason:
`//nolint:<linter> // <reason>` (Go), `// oxlint-disable-next-line <rule> -- <reason>`
(TypeScript) or `// nosemgrep: <rule-id> -- <reason>`.

`sites.yml` deploys https://cicord.dev to GitHub Pages on every push to `main` that touches
`sites/` or `packages/`, and on demand.
Dependabot proposes weekly, grouped updates of Go modules, npm packages and GitHub Actions.

## How things are tested

- The **Discord simulator** (`internal/sim`) is an in-process implementation of Cicord's Discord
  interfaces. Reconciler, runtime and API tests run against it, fast and without tokens.
- `examples/showcase` is the end-to-end fixture: its seven plugins are driven through the real
  runtime on the simulator (`internal/runtime/showcase_test.go`), their portal routes through
  the HTTP API, and `cicord plugin build`, `typegen --check` and `cicord plugin test` run on each.
  [Coverage on real Discord](../../plugins/coverage/) maps every SDK feature to the test that exercises it.
- The real Discord adapter (disgo) is tested against an HTTP fake of the REST API and gateway.
- Plugin unit tests use `@cicord/sdk/testing`.

## Repository layout

| Path | Content |
|---|---|
| `cmd/cicord/` | The single binary: CLI and `cicord serve`. |
| `internal/` | The runtime, one package per component ([source layout](../architecture/#source-layout)). |
| `packages/` | `sdk`, `client`, `react`, `ui`, `admin` (the admin console), `create-cicord-portal` (the reference portal). |
| `spec/` | The machine-readable contracts: the JSON Schemas of the YAML files and the SDK types. |
| `deploy/vps/` | The hardened single-server install ([Production on a VPS](../../self-hosting/production-vps/)). |
| `examples/` | `basic`, `showcase` and `embed`. |
| `scripts/` | `release.sh`, `sync-ui.sh`, `build-quickjs-wasm.sh`, `check-docs.sh`, the official registry tooling. |
| `sites/` | The landing page and this documentation (Astro): the project's only documentation. |

`spec/` is normative: when the behavior and a document disagree, the schemas and
`spec/sdk/cicord-sdk.d.ts` win.

## This documentation

The site lives in `sites/docs` (Astro Starlight). Every page has an **Edit page** link.

```sh
pnpm -C sites/docs dev       # live preview
pnpm -C sites/docs build     # static build into sites/docs/dist
```

## Pull requests

- Keep a pull request focused on one change, with tests for new behavior and bug fixes.
- Run the checks above; CI runs the same ones.
- Update this documentation (`sites/docs`) when behavior or formats change: it is the only
  documentation of the project.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat(store): …`, `fix(plugin): …`, `docs: …`).
- Code, comments and documentation are written in English.

Cicord is licensed under the [Apache License 2.0](../license/). By contributing, you agree
that your contributions are licensed under the same license (section 5 of the license).
Everyone taking part in the project follows the
[Code of Conduct](https://github.com/cicord/cicord/blob/main/CODE_OF_CONDUCT.md).
