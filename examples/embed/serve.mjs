#!/usr/bin/env node
// Serves the embedding example: a third-party "fan site" on its own origin that uses the
// Cicord portal SDK in token mode (https://cicord.dev/docs/portal/custom-portal/, "Embedding in another site").
//
//   node examples/embed/serve.mjs [--port 5180] [--runtime http://127.0.0.1:7420]
//
// /cicord-client.mjs is the built @cicord/client (packages/client/dist, or CICORD_CLIENT_BUNDLE).
// The runtime must allow this origin: cicord.yaml spec.portal.allowedOrigins:
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
const runtime = arg("runtime", process.env.CICORD_RUNTIME_URL ?? "http://127.0.0.1:7420");
const bundle =
  process.env.CICORD_CLIENT_BUNDLE ?? resolve(here, "../../packages/client/dist/index.mjs");

createServer((req, res) => {
  const path = new URL(req.url ?? "/", "http://x").pathname;
  try {
    if (path === "/cicord-client.mjs") {
      res.writeHead(200, { "Content-Type": "text/javascript; charset=utf-8" });
      res.end(readFileSync(bundle));
      return;
    }
    // Every other path is the page (it handles ?cicord_code= after a Discord login).
    const html = readFileSync(join(here, "index.html"), "utf8").replace(
      "__CICORD_RUNTIME_URL__",
      runtime,
    );
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-cache" });
    res.end(html);
  } catch (error) {
    // Details stay in this terminal: a page never shows a stack trace or a file path.
    console.error(error);
    res.writeHead(500, { "Content-Type": "text/plain" });
    res.end("Internal error");
  }
}).listen(port, "localhost", () => {
  console.log(`fan site on http://localhost:${port} (Cicord runtime: ${runtime})`);
});
