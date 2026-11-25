---
title: Customize from YAML
description: Theme the reference portal and change its texts, banner, links, pages and language from the server YAML, without writing code.
---

Everything on this page is declared in a server's YAML under `spec.portal`, reviewed in git
like any change, and served by `GET /api/v1/portal/<server>` (the `PortalServer` object), so a
custom portal built on the SDK gets the same options.

## Theme

```yaml title="servers/my-community.yaml"
spec:
  portal:
    theme:
      primary: "#5865f2"        # buttons, links, focus
      accent: "#eb459e"         # highlights
      logo: ../assets/logo.svg  # relative to this file: png, jpg, gif, webp, avif or svg
      mode: dark                # light | dark | system (follows the visitor); unset: dark
```

`@cicord/ui` derives every color from `primary` and `accent`, with a contrast check for the
chosen mode. The logo (10 MB at most) is served by `GET /api/v1/portal/<server>/logo`,
versioned by revision (a new commit busts the cache), with `Content-Security-Policy: sandbox`,
so an SVG can never run script.

`cicord validate` reports a logo outside the repository, behind a symbolic link or of an
unsupported type as an **error**, and a missing file as a **warning** (the portal does not show
it).

## Texts, banner, links, pages and language

```yaml title="servers/my-community.yaml"
spec:
  portal:
    title: Welcome to My Community          # home headline (default: the server name)
    tagline: The community for game nights. # one sentence under it
    description: |                          # Markdown subset, see below
      Here we play **together**.

      ## What you'll find here
      - game nights every Friday
      - monthly tournaments

      Read the [rules](https://my-community.example/rules).
    banner: ../assets/banner.jpg            # wide header image, same rules as the logo
    links:                                  # up to 12, http(s) only
      - { label: Website, url: "https://my-community.example", icon: website }
      - { label: YouTube, url: "https://www.youtube.com/@my-community", icon: youtube }
    pages:                                  # pages of the reference portal (default: true)
      onboarding: true
      profile: true
      leaderboard: true                     # needs the levels plugin
      tickets: false                        # needs the tickets plugin (moderators only)
    locale: en                              # en | fr (default: the browser language)
```

- **`description`** supports paragraphs, `#` to `###` headings, `-` and `1.` lists, `>`
  quotes, `**bold**`, `*italic*`, `` `code` `` and `[links](https://…)`. It is rendered as
  React elements: raw HTML shows as text, and non-http(s) links as plain text.
- **`links[].icon`**: `link` (default), `website`, `discord`, `youtube`, `twitch`, `x`,
  `bluesky`, `mastodon`, `instagram`, `tiktok`, `github`, `reddit`, `steam`, `rules`,
  `calendar`, `shop`.
- **`pages`**: a disabled page disappears from the navigation and its URL answers "not
  found". Pages that need a plugin (`leaderboard` → `levels`, `tickets` → `tickets`,
  `onboarding` → `onboarding`) only appear when the server runs that plugin. Home and
  Developers cannot be turned off.
- **`locale`**: the interface language of the reference portal, `en` or `fr`. Without it the
  browser language is used when supported, else English.

[`examples/basic/servers/my-community.yaml`](https://github.com/cicord/cicord/blob/main/examples/basic/servers/my-community.yaml)
uses these options. The full field list is in the [Server reference](../../configuration/server/#portal).

## Several servers

Each server has its own `spec.portal`. The portal lists every server that declares one
(`GET /api/v1/portal/servers`) and lets visitors pick.
