# @cicord/react

React bindings for [Cicord](https://github.com/cicord/cicord) community portals: a provider,
cached data hooks and headless components, on top of
[`@cicord/client`](https://www.npmjs.com/package/@cicord/client).

```sh
npm install @cicord/react @cicord/client react
```

```tsx
import { createCicordClient } from "@cicord/client";
import { CicordProvider, useMe, useMember, useRouteQuery } from "@cicord/react";

const client = createCicordClient();

export function App() {
  return (
    <CicordProvider client={client} server="my-server">
      <Leaderboard />
    </CicordProvider>
  );
}

function Leaderboard() {
  const me = useMe();
  // The levels instance is found by plugin name; types come from src/cicord-routes.d.ts
  // (`cicord portal typegen`).
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
`useRouteMutation`, `usePluginQuery`, `usePluginMutation`, `useCicordQuery`. Component:
`<OnboardingFlow>` (headless onboarding of the `onboarding` plugin). See the
[portal SDK guide](https://cicord.dev/docs/portal/sdk/).

Peer dependency: React 18.3 or 19. ESM only, with TypeScript declarations. License:
Apache-2.0.
