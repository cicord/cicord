import { CicordProvider } from "@cicord/react";
import { useState } from "react";
import { FeaturePage } from "./features/FeaturePage.tsx";
import type { PortalConfig } from "./config.ts";
import { Appeals } from "./pages/Appeals.tsx";
import { Developers } from "./pages/Developers.tsx";
import { Home } from "./pages/Home.tsx";
import { Leaderboard } from "./pages/Leaderboard.tsx";
import { NotFound } from "./pages/NotFound.tsx";
import { Onboarding } from "./pages/Onboarding.tsx";
import { Profile } from "./pages/Profile.tsx";
import { Tickets } from "./pages/Tickets.tsx";
import { ConfigProvider, ServerGate, Shell, usePages } from "./portal.tsx";
import { usePathname } from "./router.tsx";

/**
 * Routes. Add pages here (and links in the header / home). Pages turned off with
 * `spec.portal.pages`, or whose plugin is not installed, answer "not found".
 */
function Page() {
  const path = usePathname().replace(/\/+$/, "") || "/";
  const pages = usePages();
  switch (path) {
    case "/":
      return <Home />;
    case "/onboarding":
      return pages.onboarding ? <Onboarding /> : <NotFound />;
    case "/me":
      return pages.profile ? <Profile /> : <NotFound />;
    case "/leaderboard":
      return pages.leaderboard ? <Leaderboard /> : <NotFound />;
    case "/tickets":
      return pages.tickets ? <Tickets /> : <NotFound />;
    case "/appeals":
      return <Appeals />;
    case "/appeals/staff":
      return <Appeals staff />;
    case "/developers":
      return <Developers />;
    default:
      return <FeaturePage path={path} />;
  }
}

export function App({ config }: { config: PortalConfig }) {
  const [server, setServer] = useState<string | undefined>(undefined);
  return (
    <ConfigProvider value={config}>
      <CicordProvider client={config.client} server={server}>
        <ServerGate onServer={setServer}>
          <Shell>
            <Page />
          </Shell>
        </ServerGate>
      </CicordProvider>
    </ConfigProvider>
  );
}
