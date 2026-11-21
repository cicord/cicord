import "@fontsource-variable/ibm-plex-sans";
import "@fontsource/ibm-plex-mono/latin-400.css";
import "@fontsource/ibm-plex-mono/latin-500.css";
import "./styles/app.css";
import "./schema-form/schema-form.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App, initialTheme } from "./App.tsx";
import { createAdminConfig } from "./config.ts";

document.documentElement.dataset["theme"] = initialTheme();

const config = await createAdminConfig();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App config={config} />
  </StrictMode>,
);
