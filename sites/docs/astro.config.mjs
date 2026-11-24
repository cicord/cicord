import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";

// The combined site serves the landing page at BASE_PATH and these docs at BASE_PATH/docs/
// (BASE_PATH is "/" for a custom domain, "/<repo>" on GitHub Pages project sites).
const prefix = (process.env.BASE_PATH ?? "/").split("/").filter(Boolean).join("/");
const landing = prefix ? `/${prefix}/` : "/";
const site = process.env.SITE_URL ?? "http://localhost:4321";
const repo = "https://github.com/cicord/cicord";

export default defineConfig({
  site,
  base: `${landing}docs`,
  trailingSlash: "always",
  integrations: [
    starlight({
      title: "Cicord",
      description:
        "GitOps for Discord: declare servers and bots in git, reconcile them continuously, extend them with sandboxed TypeScript plugins.",
      logo: { src: "./src/assets/logo.svg", alt: "Cicord" },
      favicon: "/favicon.svg",
      social: [
        { icon: "github", label: "GitHub", href: repo },
        { icon: "discord", label: "Discord", href: "https://discord.gg/qTt5S2Fe4m" },
      ],
      editLink: { baseUrl: `${repo}/edit/main/sites/docs/` },
      customCss: ["./src/styles/theme.css"],
      lastUpdated: false,
      sidebar: [
        // An absolute URL: Starlight would prefix a bare path with the docs base.
        { label: "Cicord website", link: new URL(landing, site).href },
        {
          label: "Getting started",
          items: [
            { label: "Introduction", slug: "getting-started/introduction" },
            { label: "Installation", slug: "getting-started/installation" },
            { label: "Quick start", slug: "getting-started/quick-start" },
            { label: "Setup wizard reference", slug: "getting-started/setup-wizard" },
            { label: "Your first change", slug: "getting-started/first-change" },
          ],
        },
        {
          label: "Concepts",
          items: [
            { label: "Repository layout", slug: "concepts/repository-layout" },
            { label: "Reconciliation", slug: "concepts/reconciliation" },
            { label: "Write-back", slug: "concepts/write-back" },
            { label: "Access and roles", slug: "concepts/access-and-roles" },
          ],
        },
        {
          label: "Configuration reference",
          items: [
            { label: "Project (cicord.yaml)", slug: "configuration/project" },
            { label: "Server", slug: "configuration/server" },
            { label: "Bot", slug: "configuration/bot" },
          ],
        },
        {
          label: "Plugins",
          items: [
            { label: "Overview", slug: "plugins/overview" },
            { label: "Official plugins", slug: "plugins/official" },
            { label: "Writing a plugin", slug: "plugins/writing" },
            { label: "Plugin API", slug: "plugins/api" },
            { label: "Testing and limits", slug: "plugins/testing" },
            { label: "Coverage on real Discord", slug: "plugins/coverage" },
            { label: "Publishing", slug: "plugins/publishing" },
          ],
        },
        {
          label: "Community portal",
          items: [
            { label: "Overview", slug: "portal/overview" },
            { label: "Customize from YAML", slug: "portal/customize" },
            { label: "Custom portal", slug: "portal/custom-portal" },
            { label: "Embedding", slug: "portal/embedding" },
            { label: "SDK packages", slug: "portal/sdk" },
          ],
        },
        {
          label: "Self-hosting",
          items: [
            { label: "Deploy", slug: "self-hosting/deploy" },
            { label: "Production on a VPS", slug: "self-hosting/production-vps" },
            { label: "Security checklist", slug: "self-hosting/security" },
            { label: "Bot permissions", slug: "self-hosting/bot-permissions" },
            { label: "Backups and secrets", slug: "self-hosting/backups-and-secrets" },
            { label: "Upgrading", slug: "self-hosting/upgrading" },
          ],
        },
        {
          label: "Reference",
          items: [
            { label: "CLI", slug: "reference/cli" },
            {
              label: "HTTP API",
              collapsed: true,
              items: [
                { label: "Overview", slug: "reference/http-api" },
                { label: "Authentication", slug: "reference/http-api/authentication" },
                { label: "Status and servers", slug: "reference/http-api/servers" },
                { label: "Plugin instances", slug: "reference/http-api/plugin-instances" },
                { label: "Community portal", slug: "reference/http-api/portal" },
                { label: "Machine endpoints", slug: "reference/http-api/machine" },
                { label: "Simulator", slug: "reference/http-api/simulator" },
                { label: "Setup wizard", slug: "reference/http-api/setup" },
                { label: "Store", slug: "reference/http-api/store" },
              ],
            },
            { label: "Discord parity", slug: "reference/discord-parity" },
            { label: "Glossary", slug: "reference/glossary" },
          ],
        },
        {
          label: "Project",
          items: [
            { label: "Status and roadmap", slug: "project/status" },
            { label: "Architecture", slug: "project/architecture" },
            {
              label: "Design decisions",
              collapsed: true,
              items: [{ autogenerate: { directory: "project/decisions" } }],
            },
            { label: "Contributing", slug: "project/contributing" },
            { label: "Releasing", slug: "project/releasing" },
            { label: "License", slug: "project/license" },
          ],
        },
      ],
    }),
  ],
});
