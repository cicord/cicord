---
title: License
description: The SDKs are Apache-2.0; the license of the runtime is not decided yet.
---

| Part | License |
|---|---|
| `@cicord/sdk`, `@cicord/client`, `@cicord/react`, `@cicord/ui`, `create-cicord-portal` | **Apache-2.0** (a `LICENSE` file in each package) |
| The runtime, the CLI and the admin console (`cicord`) | **Not decided yet** |
| Plugins | Each plugin declares its own license in `plugin.yaml` (`metadata.license`, an SPDX identifier). The official plugins are Apache-2.0. |

The runtime license will be decided before any public release; it may or may not be an open
source license. Until then the runtime is not licensed (all rights reserved by default). The decision and the options
considered are recorded in
[ADR-0013](https://github.com/npjonath/cicord/blob/main/docs/adr/0013-licensing.md).
