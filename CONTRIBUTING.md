# Contributing to Cicord

Thanks for your interest in Cicord! Bug reports, ideas, documentation fixes, plugins and code
are all welcome.

## Before you start

- **Questions and ideas**: open a [discussion](https://github.com/npjonath/cicord/discussions)
  or a feature request issue.
- **Bugs**: open a bug report with the steps to reproduce, the version (`cicord version`) and
  the relevant logs (remove tokens and secrets first).
- **Security issues**: never in a public issue, see [SECURITY.md](SECURITY.md).
- **Larger changes** (a new capability, a format change, a new dependency): open an issue first
  so the design can be discussed. Architecture decisions are recorded in [docs/adr](docs/adr).

## Development

The full guide (prerequisites, make targets, the JS workspace, how things are tested, the
repository layout) is in the documentation:
[Contributing](https://cicord.dev/docs/project/contributing/)
([source](sites/docs/src/content/docs/project/contributing.md)).

In short, with Go 1.27, Node 24 and pnpm:

```sh
make all          # JS packages + embedded UIs + bin/cicord
make test         # go test -race ./... and the JS tests
pnpm run check    # format, lint, types, example plugins
gofmt -l .        # must print nothing
make hooks        # once per clone: gitleaks checks staged changes before each commit
```

Secrets never go into the repository: CI scans the whole git history with
[Gitleaks](https://github.com/gitleaks/gitleaks) (`.gitleaks.toml`), and the pre-commit hook
refuses a commit that adds one.

## Pull requests

- Keep a pull request focused on one change, with tests for new behavior and bug fixes.
- Run the checks above; CI runs the same ones.
- Update the documentation (`sites/docs`, `docs/`, `spec/`) when behavior or formats change.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat(store): …`, `fix(plugin): …`, `docs: …`).
- Code, comments and documentation are written in English.

## License

Cicord is licensed under the [Apache License 2.0](LICENSE). By contributing, you agree that
your contributions are licensed under the same license (section 5 of the license).

## Code of conduct

Everyone taking part in the project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
