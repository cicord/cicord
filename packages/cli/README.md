# @cicord/cli

The [Cicord](https://cicord.dev) command line tool and runtime (`cicord`), from npm. It is
the same static binary as the [GitHub releases](https://github.com/cicord/cicord/releases):
npm installs the platform package that matches your machine (`@cicord/cli-<os>-<arch>`, an
optional dependency at the same version), and the `cicord` command runs it. Node.js is only
used to start it.

```sh
npm i -g @cicord/cli
cicord version

npx @cicord/cli version          # without installing
```

Platforms: macOS (arm64, x64), Linux (x64, arm64), Windows (x64). Optional dependencies
must be installed: `--no-optional` or `--omit=optional` leave `cicord` without its binary.
Every package is published by the release workflow with npm provenance
(`npm audit signatures`).

Other installs (script, Homebrew, container image, verified downloads):
[Installation](https://cicord.dev/docs/getting-started/installation/).
