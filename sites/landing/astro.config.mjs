import { defineConfig } from "astro/config";

// The landing page is served at the root of the combined site and the docs under
// `<base>/docs/`. SITE_URL is the public origin (canonical and Open Graph URLs) and
// BASE_PATH the path prefix when the site is not served at the root of its host
// (e.g. GitHub Pages: BASE_PATH=/cicord).
export default defineConfig({
  site: process.env.SITE_URL ?? "http://localhost:4321",
  base: process.env.BASE_PATH ?? "/",
});
