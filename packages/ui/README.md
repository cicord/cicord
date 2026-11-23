# @codi/ui

Styled React components for [CODI](https://github.com/npjonath/codi) community portals,
driven by the server's theme (`spec.portal.theme` in `servers/<server>.yaml`: primary and
accent colors, light/dark mode, with a contrast check). Optional: portals can use
[`@codi/react`](https://www.npmjs.com/package/@codi/react) alone with their own design.

```sh
npm install @codi/ui react
```

```tsx
import "@codi/ui/styles.css";
import { Avatar, Button, Markdown, MemberCard, ThemeProvider } from "@codi/ui";

<ThemeProvider theme={server.theme}>
  <MemberCard displayName={me.user.displayName} avatarUrl={me.user.avatarUrl} roles={roles} />
  <Markdown source={server.description} />
  <Button onClick={login}>Sign in with Discord</Button>
</ThemeProvider>;
```

Components include `ThemeProvider`, `Button`, `Notice`, `Spinner`, `Avatar`, `MemberCard`,
`ProgressBar`, `Markdown` (a safe Markdown subset rendered as React elements: raw HTML
shows as text, only http(s) links) and `LinkIcon`. The reference portal
([create-codi-portal](https://github.com/npjonath/codi/tree/main/packages/create-codi-portal))
uses all of them.

Peer dependency: React 18.3 or 19. ESM only, with TypeScript declarations and one
stylesheet (`@codi/ui/styles.css`). License: Apache-2.0.
