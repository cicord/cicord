# Cicord machine-readable contracts

This directory holds the files that tools read; the documentation is at
**https://cicord.dev/docs/**.

- [`schemas/`](schemas/): the JSON Schemas (draft 2020-12) of `cicord.yaml`, `servers/<key>.yaml`, `bots/<key>/bot.yaml` and `plugin.yaml`, used by `cicord validate` and by editors (`# yaml-language-server: $schema=…`).
- [`sdk/cicord-sdk.d.ts`](sdk/cicord-sdk.d.ts): the plugin SDK type contract implemented by `@cicord/sdk`.

Their semantics are documented in the [configuration reference](https://cicord.dev/docs/configuration/project/), the [HTTP API reference](https://cicord.dev/docs/reference/http-api/) and the [plugin docs](https://cicord.dev/docs/plugins/overview/).
