import { CodiProvider } from "@codi/react";
import { useState } from "react";
import type { PortalConfig } from "./config.ts";
import { Home } from "./pages/Home.tsx";
import { NotFound } from "./pages/NotFound.tsx";
import { Onboarding } from "./pages/Onboarding.tsx";
import { Profile } from "./pages/Profile.tsx";
import { ConfigProvider, ServerGate, Shell } from "./portal.tsx";
import { usePathname } from "./router.tsx";

/** Routes. Add pages here (and links in the header / home). */
function Page() {
  const path = usePathname().replace(/\/+$/, "") || "/";
  switch (path) {
    case "/":
      return <Home />;
    case "/onboarding":
      return <Onboarding />;
    case "/me":
      return <Profile />;
    default:
      return <NotFound />;
  }
}

export function App({ config }: { config: PortalConfig }) {
  const [server, setServer] = useState<string | undefined>(undefined);
  return (
    <ConfigProvider value={config}>
      <CodiProvider client={config.client} server={server}>
        <ServerGate onServer={setServer}>
          <Shell>
            <Page />
          </Shell>
        </ServerGate>
      </CodiProvider>
    </ConfigProvider>
  );
}
