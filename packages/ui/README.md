# @cicord/ui

Styled React components for [Cicord](https://github.com/npjonath/cicord) community portals,
driven by the server's theme (`spec.portal.theme` in `servers/<server>.yaml`: primary and
accent colors, light/dark mode, with a contrast check). Optional: portals can use
[`@cicord/react`](https://www.npmjs.com/package/@cicord/react) alone with their own design.

```sh
npm install @cicord/ui react
```

```tsx
import "@cicord/ui/styles.css";
import { Avatar, Button, Markdown, MemberCard, ThemeProvider } from "@cicord/ui";

<ThemeProvider theme={server.theme}>
  <MemberCard displayName={me.user.displayName} avatarUrl={me.user.avatarUrl} roles={roles} />
  <Markdown source={server.description} />
  <Button onClick={login}>Sign in with Discord</Button>
</ThemeProvider>;
```

Components include `ThemeProvider`, `Button`, `Notice`, `Spinner`, `Avatar`, `MemberCard`,
`ProgressBar`, `Markdown` (a safe Markdown subset rendered as React elements: raw HTML
shows as text, only http(s) links) and `LinkIcon`. The reference portal
([create-cicord-portal](https://github.com/npjonath/cicord/tree/main/packages/create-cicord-portal))
uses all of them.

Peer dependency: React 18.3 or 19. ESM only, with TypeScript declarations and one
stylesheet (`@cicord/ui/styles.css`). License: Apache-2.0.
