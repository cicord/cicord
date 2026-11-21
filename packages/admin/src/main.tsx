import "@fontsource-variable/ibm-plex-sans";
import "@fontsource/ibm-plex-mono/latin-400.css";
import "@fontsource/ibm-plex-mono/latin-500.css";
import "./styles/globals.css";
import "./i18n/index.ts";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App.tsx";
import { createAdminConfig } from "./config.ts";
import { applyTheme, resolveTheme, savedPreference } from "./shell/theme.tsx";

applyTheme(resolveTheme(savedPreference()));

const config = await createAdminConfig();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App config={config} />
  </StrictMode>,
);
