// Every internal URL is built from Astro's base so the site works under a path prefix.
// BASE_URL is "/" by default and "/cicord" (no trailing slash) with base "/cicord".
const base = import.meta.env.BASE_URL.replace(/\/?$/, "/");

/** Returns `path` (without a leading slash) under the site's base. */
export function url(path = ""): string {
  return base + path.replace(/^\//, "");
}

export const links = {
  home: url(),
  docs: url("docs/"),
  install: url("docs/getting-started/installation/"),
  quickStart: url("docs/getting-started/quick-start/"),
  plugins: url("docs/plugins/overview/"),
  deploy: url("docs/self-hosting/deploy/"),
  github: "https://github.com/cicord/cicord",
} as const;
