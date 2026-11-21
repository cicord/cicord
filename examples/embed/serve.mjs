#!/usr/bin/env node
// Serves the embedding example: a third-party "fan site" on its own origin that uses the
// CODI portal SDK in token mode (docs/portal.md, "Embedding in another site").
//
//   node examples/embed/serve.mjs [--port 5180] [--runtime http://127.0.0.1:7420]
//
// /codi-client.mjs is the built @codi/client (packages/client/dist, or CODI_CLIENT_BUNDLE).
// The runtime must allow this origin: codi.yaml spec.portal.allowedOrigins:
// ["http://localhost:5180"] (or --cors-origin).
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 && process.argv[i + 1] ? process.argv[i + 1] : fallback;
};
const port = Number(arg("port", "5180"));
const runtime = arg("runtime", process.env.CODI_RUNTIME_URL ?? "http://127.0.0.1:7420");
const bundle =
  process.env.CODI_CLIENT_BUNDLE ?? resolve(here, "../../packages/client/dist/index.mjs");

createServer((req, res) => {
  const path = new URL(req.url ?? "/", "http://x").pathname;
  try {
    if (path === "/codi-client.mjs") {
      res.writeHead(200, { "Content-Type": "text/javascript; charset=utf-8" });
      res.end(readFileSync(bundle));
      return;
    }
    // Every other path is the page (it handles ?codi_code= after a Discord login).
    const html = readFileSync(join(here, "index.html"), "utf8").replace(
      "__CODI_RUNTIME_URL__",
      runtime,
    );
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-cache" });
    res.end(html);
  } catch (error) {
    res.writeHead(500, { "Content-Type": "text/plain" });
    res.end(String(error));
  }
}).listen(port, "localhost", () => {
  console.log(`fan site on http://localhost:${port} (CODI runtime: ${runtime})`);
});
