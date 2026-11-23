# @codi/react

React bindings for [CODI](https://github.com/npjonath/codi) community portals: a provider,
cached data hooks and headless components, on top of
[`@codi/client`](https://www.npmjs.com/package/@codi/client).

```sh
npm install @codi/react @codi/client react
```

```tsx
import { createCodiClient } from "@codi/client";
import { CodiProvider, useMe, useMember, useRouteQuery } from "@codi/react";

const client = createCodiClient();

export function App() {
  return (
    <CodiProvider client={client} server="my-server">
      <Leaderboard />
    </CodiProvider>
  );
}

function Leaderboard() {
  const me = useMe();
  // The levels instance is found by plugin name; types come from src/codi-routes.d.ts
  // (`codi portal typegen`).
  const board = useRouteQuery("levels", "GET /leaderboard", { query: { limit: 50 } });
  if (!board.data) return <p>Loading…</p>;
  return (
    <ol>
      {board.data.items.map((e) => (
        <li key={e.userId}>{e.displayName}</li>
      ))}
    </ol>
  );
}
```

Hooks: `useMe`, `useSession`, `useLogout`, `useLinkTokenLogin`, `usePortalServers`,
`usePortalServer`, `useMember`, `useRoles`, `usePortalPlugin`, `useRouteQuery`,
`useRouteMutation`, `usePluginQuery`, `usePluginMutation`, `useCodiQuery`. Component:
`<OnboardingFlow>` (headless onboarding of the `onboarding` plugin). See
[docs/portal.md](https://github.com/npjonath/codi/blob/main/docs/portal.md).

Peer dependency: React 18.3 or 19. ESM only, with TypeScript declarations. License:
Apache-2.0.
