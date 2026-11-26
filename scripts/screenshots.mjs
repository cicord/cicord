#!/usr/bin/env node
// Captures the product screenshots of the docs and the landing page from a running runtime
// (`cicord dev --sim`), with the local Google Chrome driven over the DevTools protocol (no
// dependency). Images are written as WebP at 2x to sites/docs/src/assets/screenshots and
// sites/landing/src/assets/screenshots.
//
//   node scripts/screenshots.mjs --set community [--only admin-plan,portal-custom] [--url …]
//
// Each set (below) says which runtime it expects. Shots of `cicord dev --sim` sign in with
// POST /auth/dev-login. Set CHROME to another Chromium binary if Google Chrome is not in its
// default macOS location, CHROME_PORT to move its DevTools port (default 9333); DEBUG=1
// prints the page text of a shot that times out.
import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parseArgs } from "node:util";

const { values: args } = parseArgs({
  options: {
    set: { type: "string", default: "showcase" },
    url: { type: "string" },
    only: { type: "string" },
    theme: { type: "string", default: "dark" },
  },
});
const root = new URL("..", import.meta.url).pathname;
const out = {
  docs: join(root, "sites/docs/src/assets/screenshots"),
  landing: join(root, "sites/landing/src/assets/screenshots"),
};

/**
 * The screenshot sets, one per source. Each shot: the page, a text to wait for, an optional
 * preparation run in the page, the viewport (default 1440x900) and where it goes (`dest`,
 * default docs). `clip` crops to the elements a CSS selector matches, or that a function run in
 * the page returns, instead of the viewport (with `pad` CSS pixels around them, default 0).
 *
 * - showcase: `cicord dev examples/showcase --sim --listen 127.0.0.1:7490`
 * - community: `cicord dev <cicord-community checkout> --sim --listen 127.0.0.1:7491`
 * - demo: the admin console's demo data (`vp dev --mode mock --port 5190` in packages/admin),
 *   for the pages that need history or rare states: analytics, drift, parked tasks, incident
 *   overlays, the unexplained-actions warning. Each shot starts from fresh demo data, signed
 *   in as the owner. `mock` passes options to the demo runtime (createMockRuntime), `state`
 *   edits its saved state (sessionStorage) before the page loads.
 * - portal: the reference portal's demo data (`vp dev` in packages/create-cicord-portal, port
 *   5174). `?role=moderator` previews the staff pages.
 * - official, official-admin: the official tickets and verification plugins on a real runtime,
 *   for the rendered challenge, the ticket desk and plugin secrets. Copy examples/showcase,
 *   replace plugins/tickets and add plugins/verification from cicord-plugins, and in bot.yaml
 *   grant both plugins their required capabilities (verification also `render`, config
 *   `{panelChannel: verify, verifiedRole: verified, minAccountAgeDays: 0}`, and
 *   `secrets: {TURNSTILE_SECRET: {secretRef: CICORD_TURNSTILE_SECRET}}`); add a `verify`
 *   channel and a `verified` role to the server. Run `cicord dev <copy> --sim --listen
 *   127.0.0.1:7420` (a binary built with version 0.2.0 or later), then the portal's
 *   `vp dev` (packages/create-cicord-portal, port 5174) for `official` and the admin's `vp dev`
 *   (packages/admin, port 5175) for `official-admin`. The `official` seed adds the members and
 *   tickets.
 */
const SETS = {
  showcase: {
    url: "http://127.0.0.1:7490",
    login: "dev",
    shots: [{ id: "portal-home", path: "/", waitText: "Hi " }],
  },
  community: {
    url: "http://127.0.0.1:7491",
    login: "dev",
    // Staff work on the tickets opened by the activity script: the first one is taken, raised
    // and answered from the portal's support queue.
    seed: `(async () => {
      const api = (method, path, body) => fetch("/api/v1/portal/cicord/plugins/cicord/tickets" + path, {
        method, headers: {"Content-Type": "application/json", "X-Cicord-Request": "1"},
        body: body ? JSON.stringify(body) : undefined }).then((r) => r.json());
      const { items } = await api("GET", "/tickets");
      const first = items.find((t) => t.subject === "Spam in #general");
      if (!first || first.assignee) return;
      await api("POST", "/tickets/" + first.id + "/assign", { userId: "me" });
      await api("PATCH", "/tickets/" + first.id, { priority: "high" });
      await api("POST", "/tickets/" + first.id + "/messages", { content: "Thanks Maya, the account is timed out. I am cleaning up the invite links now." });
    })()`,
    shots: [
      {
        id: "admin-overview",
        path: "/admin/servers/cicord",
        waitText: "Synced",
        dest: ["docs", "landing"],
      },
      {
        id: "admin-plan",
        path: "/admin/servers/cicord/plan",
        waitText: "Waiting for boosts",
        settle: 2000,
        dest: ["docs", "landing"],
      },
      {
        id: "admin-plugin-settings",
        path: "/admin/servers/cicord/plugins/cicord/tickets",
        waitText: "Staff roles",
        settle: 3000,
        dest: ["docs", "landing"],
      },
      {
        id: "admin-store",
        path: "/admin/servers/cicord/store",
        waitText: "github-releases",
        settle: 2000,
      },
      {
        id: "admin-diagnostics",
        path: "/admin/servers/cicord/diagnostics",
        waitText: "live checks",
        settle: 6000,
      },
      {
        id: "portal-custom",
        path: "/",
        waitText: "The Cicord community",
        as: "maya",
        dest: ["docs", "landing"],
      },
      {
        id: "portal-analytics",
        path: "/activity",
        waitText: "Messages",
        as: "maya",
        settle: 2500,
        prepare: `[...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "7 days")?.click()`,
      },
      {
        id: "portal-custom-queue",
        path: "/support",
        waitText: "Question about plugin permissions",
        dest: ["docs", "landing"],
      },
    ],
  },
  // `cicord dev <empty dir> --sim --setup --listen 127.0.0.1:7492`; SETUP_LINK is the setup
  // link it prints (path and token).
  setup: {
    url: "http://127.0.0.1:7492",
    login: "none",
    shots: [
      {
        id: "admin-setup-wizard",
        path: process.env.SETUP_LINK?.replace(/^https?:\/\/[^/]+/, "") ?? "/admin/setup",
        waitText: "Welcome",
        settle: 1500,
      },
    ],
  },
  portal: {
    url: "http://localhost:5174",
    login: "none",
    // Signed in: the demo portal keeps its state in sessionStorage.
    session: {
      "cicord-portal:mock": JSON.stringify({
        loggedIn: true,
        memberRoles: ["member"],
        completedAt: null,
      }),
    },
    shots: [
      {
        // The staff view of the tickets: queue, filters, the selected ticket's thread.
        id: "portal-ticket-desk",
        path: "/tickets?role=moderator",
        waitText: "Tickets",
        settle: 1500,
      },
    ],
  },
  official: {
    url: "http://localhost:5174",
    login: "dev",
    // Members who join (river is left to verify) and open tickets; the owner takes the first.
    seed: `(async () => {
      const H = { "Content-Type": "application/json", "X-Cicord-Request": "1" };
      const post = (path, body, method = "POST") =>
        fetch(path, { method, headers: H, body: JSON.stringify(body) }).then((r) => r.ok ? r.json().catch(() => null) : null);
      const state = await fetch("/api/v1/sim/state").then((r) => r.json());
      if (state.users.some((u) => u.username === "river")) return;
      const owner = state.users.find((u) => u.username === "owner").id;
      const server = state.servers[0].id;
      const T = "/api/v1/portal/" + state.servers[0].key + "/plugins/cicord/tickets";
      const ids = {};
      for (const name of ["maya", "sam", "jordan", "river"]) {
        ids[name] = (await post("/api/v1/sim/users", { username: name })).id;
        await post("/api/v1/sim/servers/" + server + "/members", { userId: ids[name] });
      }
      const open = async (who, subject, description) => {
        await post("/auth/dev-login", { userId: ids[who] });
        return (await post(T + "/tickets", { subject, description })).id;
      };
      const first = await open("maya", "Spam in #general", "Two new accounts keep posting invite links in #general.");
      await open("sam", "Bot does not answer /rank", "The /rank command times out since this morning.");
      await open("jordan", "Question about plugin permissions", "Which grants does the polls plugin need to post results?");
      await post("/auth/dev-login", { userId: owner });
      await post(T + "/tickets/" + first + "/assign", { userId: "me" });
      await post(T + "/tickets/" + first, { priority: "high" }, "PATCH");
      await post(T + "/tickets/" + first + "/messages", { content: "Thanks Maya, both accounts are timed out. I am cleaning up the invite links now." });
    })()`,
    shots: [
      {
        // A member's typed-code challenge, rendered by the runtime.
        id: "portal-verification",
        path: "/verification/challenge",
        waitText: "Verify my account",
        as: "river",
        size: [1280, 800],
        settle: 1500,
        dest: ["docs", "landing"],
      },
      {
        id: "portal-ticket-desk",
        path: "/tickets?ticket=1",
        waitText: "Two new accounts",
        // The queue and the selected ticket's header (the thread shows raw Discord mentions).
        clip: () => {
          const h2 = document.querySelector("h2");
          return [document.querySelector("h1"), h2, h2?.nextElementSibling];
        },
        pad: 16,
        settle: 2000,
        dest: ["docs", "landing"],
      },
    ],
  },
  "official-admin": {
    url: "http://localhost:5175",
    login: "dev",
    shots: [
      {
        // The instance's secret fields: one bound in bot.yaml, one not.
        id: "admin-plugin-secrets",
        path: "/admin/servers/showcase/plugins/cicord/verification",
        waitText: "TURNSTILE_SECRET",
        clip: () =>
          [...document.querySelectorAll("label")]
            .filter((l) => l.textContent.endsWith("_SECRET"))
            .map((l) => l.parentElement),
        pad: 24,
        settle: 1500,
      },
    ],
  },
  demo: {
    url: "http://localhost:5190",
    login: "mock",
    shots: [
      {
        id: "admin-analytics",
        path: "/admin/servers/pixel-haven/analytics",
        waitText: "Members over time",
        settle: 2500,
        dest: ["docs", "landing"],
      },
      {
        id: "admin-drift",
        path: "/admin/servers/pixel-haven/drift",
        waitText: "Drift",
        settle: 1500,
      },
      {
        // Parked tasks, paged: the first page and its "Load more".
        id: "admin-dead-letters",
        path: "/admin/servers/pixel-haven/diagnostics",
        waitText: "Load more",
        mock: { demoDeadLetters: 120 },
        size: [1100, 760],
        // The end of the first page of failed tasks and the Load more button.
        clip: () => {
          const more = [...document.querySelectorAll("button")].find(
            (b) => b.textContent.trim() === "Load more",
          );
          const rows = [...(more?.previousElementSibling?.querySelectorAll(":scope > li") ?? [])];
          return [more, ...rows.slice(-4)];
        },
        pad: 10,
        settle: 1500,
      },
      {
        id: "admin-overlays",
        path: "/admin/servers/pixel-haven/plan",
        waitText: "Active incident overlays",
        size: [1100, 760],
        // The page title and the overlay card, readable at the docs' width.
        clip: () => {
          const card = [...document.querySelectorAll("[data-slot=card]")].find((c) =>
            c.textContent.includes("Active incident overlays"),
          );
          return [document.querySelector("main h1"), card];
        },
        pad: 24,
        state: (s) => {
          s.failing = false;
          const hour = 3600 * 1000;
          s.overlays = [
            {
              // What the official guard plugin applies during a raid (guard/src/raid.ts).
              origin: "plugin:moderator/guard",
              id: "raid",
              until: new Date(Date.now() + 2 * hour).toISOString(),
              reason: "guard raid",
              server: { verificationLevel: "high" },
              channels: {
                general: {
                  slowmodeSeconds: 30,
                  overwrites: { "@everyone": { deny: ["SendMessages", "SendMessagesInThreads"] } },
                },
              },
            },
          ];
        },
        settle: 1500,
      },
      {
        id: "admin-unconfirmed-audit",
        path: "/admin/servers/pixel-haven",
        waitText: "Unexplained actions by bot",
        mock: { demoUnconfirmedAudit: true },
        state: (s) => {
          s.failing = false;
        },
        // The warning alone: the demo server has other warnings above it.
        clip: () =>
          document.querySelector("[data-testid=unconfirmed-audit]")?.closest("[role=status]"),
        pad: 16,
        settle: 1000,
      },
      {
        id: "admin-member-data",
        path: "/admin/servers/pixel-haven/settings",
        waitText: "Data of uninstalled plugins",
        mock: { demoInstances: 0 },
        state: (s) => {
          s.orphans = [
            { instance: "moderator/old-tickets", records: 14, rankings: 3, index: 0, tasks: 2 },
          ];
        },
        // A member ID typed in, and the data left by an uninstalled plugin below.
        prepare: `(() => {
          const input = document.getElementById("privacy-member");
          Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(input, "1187436651230184451");
          input.dispatchEvent(new Event("input", { bubbles: true }));
        })()`,
        clip: () => {
          const card = (id) => document.getElementById(id)?.closest("[data-slot=card]");
          const orphans = [...document.querySelectorAll("[data-slot=card]")].find((c) =>
            c.textContent.includes("Data of uninstalled plugins"),
          );
          return [card("member-data-title"), orphans];
        },
        pad: 24,
        settle: 1000,
      },
    ],
  },
};
const set = SETS[args.set];
if (!set) throw new Error(`unknown set ${args.set}: ${Object.keys(SETS).join(", ")}`);
const base = (args.url ?? set.url).replace(/\/$/, "");
const SHOTS = set.shots;

// The screenshots present the product, not the dev setup they are taken on: every mention of
// the simulator (the Simulator nav entry, dev badges naming it…) is hidden, and a capture
// that still shows one fails.
const SCRUB = `(() => {
  const style = document.createElement("style");
  style.textContent = 'a[href$="/simulator"] { display: none !important; }';
  document.head.append(style);
  const re = /simulat|\bmock\b|mock runtime|sample data, no connection/i;
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const hits = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) if (re.test(n.data)) hits.push(n);
  for (const n of hits) {
    // Hide the smallest block that holds the mention: its list item, badge or line.
    const el = n.parentElement.closest("li, [role=listitem], [data-slot=badge], [role=status], p, tr") ?? n.parentElement;
    el.style.setProperty("display", "none", "important");
  }
  return [...document.querySelectorAll("body *")].filter((e) => e.offsetParent !== null
    && [...e.childNodes].some((c) => c.nodeType === 3 && re.test(c.data))).length;
})()`;

const CHROME = process.env.CHROME ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const PORT = Number(process.env.CHROME_PORT ?? 9333);
const profile = mkdtempSync(join(tmpdir(), "cicord-shots-"));
const chrome = spawn(
  CHROME,
  [
    "--headless=new",
    `--remote-debugging-port=${PORT}`,
    `--user-data-dir=${profile}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--hide-scrollbars",
    "--force-color-profile=srgb",
    "--lang=en-US",
    "about:blank",
  ],
  { stdio: "ignore" },
);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function target() {
  for (let i = 0; i < 50; i++) {
    try {
      // nosemgrep: typescript.react.security.react-insecure-request.react-insecure-request -- the DevTools endpoint of the headless Chrome this script started, on loopback
      const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
      const page = list.find((t) => t.type === "page");
      if (page) return page.webSocketDebuggerUrl;
    } catch {
      // Chrome is still starting.
    }
    await sleep(200);
  }
  throw new Error("Chrome did not start");
}

function connect(url) {
  const ws = new WebSocket(url);
  let id = 0;
  const pending = new Map();
  const listeners = new Set();
  ws.addEventListener("message", (e) => {
    const msg = JSON.parse(e.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) reject(new Error(`${msg.error.message}`));
      else resolve(msg.result);
    } else for (const l of listeners) l(msg);
  });
  return new Promise((resolve) =>
    ws.addEventListener("open", () =>
      resolve({
        send: (method, params = {}) =>
          new Promise((res, rej) => {
            const n = ++id;
            pending.set(n, { resolve: res, reject: rej });
            ws.send(JSON.stringify({ id: n, method, params }));
          }),
        on: (l) => listeners.add(l),
        close: () => ws.close(),
      }),
    ),
  );
}

async function evaluate(cdp, expression) {
  const r = await cdp.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? "eval");
  return r.result.value;
}

/**
 * Calls fn in the page with values passed as arguments (Runtime.callFunctionOn), never
 * spliced into the source: fn is a function of this script (or the source of one built from
 * them), values are data.
 */
async function call(cdp, fn, ...values) {
  const { result: global } = await cdp.send("Runtime.evaluate", { expression: "globalThis" });
  const r = await cdp.send("Runtime.callFunctionOn", {
    objectId: global.objectId,
    functionDeclaration: fn.toString(),
    arguments: values.map((value) => ({ value })),
    awaitPromise: true,
    returnByValue: true,
  });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? "call");
  return r.result.value;
}

async function load(cdp, url) {
  const loaded = new Promise((resolve) =>
    cdp.on((m) => m.method === "Page.loadEventFired" && resolve()),
  );
  await cdp.send("Page.navigate", { url });
  await loaded;
}

async function waitText(cdp, text, timeoutMs = 15000) {
  const t0 = Date.now();
  while (Date.now() - t0 < timeoutMs) {
    const ok = await call(cdp, (t) => document.body?.innerText.includes(t) ?? false, text);
    if (ok) return;
    await sleep(200);
  }
  throw new Error(`"${text}" never appeared`);
}

/** Signs in as a simulated user (`cicord dev --sim` only). */
async function devLogin(cdp, username) {
  const state = await (await fetch(`${base}/api/v1/sim/state`)).json();
  const user = state.users.find((u) => u.username === username);
  if (!user) throw new Error(`no simulated user ${username}`);
  const status = await call(
    cdp,
    (userId) =>
      fetch("/auth/dev-login", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Cicord-Request": "1" },
        body: JSON.stringify({ userId }),
      }).then((r) => r.status),
    user.id,
  );
  if (status !== 200) throw new Error(`dev login as ${username}: HTTP ${status}`);
}

/** Options of the demo runtime for the current shot (login "mock"). */
let mockOptions = null;

/**
 * Applies edit (a function of this script, so its source is ours) to the demo runtime's
 * saved state.
 */
async function editMockState(cdp, edit) {
  const ok = await call(
    cdp,
    `async (key) => {
      for (let i = 0; i < 50 && !sessionStorage.getItem(key); i++)
        await new Promise((r) => setTimeout(r, 100));
      const raw = sessionStorage.getItem(key);
      if (!raw) return false;
      const state = JSON.parse(raw);
      (${edit})(state);
      sessionStorage.setItem(key, JSON.stringify(state));
      return true;
    }`,
    "cicord-admin:mock:v3",
  );
  if (!ok) throw new Error("the demo runtime saved no state");
}

async function main() {
  const only = args.only ? new Set(args.only.split(",")) : null;
  const cdp = await connect(await target());
  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");
  // English, whatever the machine's language (the portal follows the browser's).
  await cdp.send("Emulation.setLocaleOverride", { locale: "en-US" });
  await cdp.send("Network.setUserAgentOverride", {
    userAgent: (await cdp.send("Browser.getVersion")).userAgent.replace("HeadlessChrome", "Chrome"),
    acceptLanguage: "en-US,en",
  });
  await cdp.send("Emulation.setEmulatedMedia", {
    features: [{ name: "prefers-color-scheme", value: args.theme }],
  });

  // Sign in as the server owner: the dev login of `cicord dev`, or the first user of the
  // demo's sign-in page.
  await load(cdp, `${base}/admin/`);
  if (set.login === "dev") {
    await devLogin(cdp, "owner");
    if (set.seed) {
      await load(cdp, `${base}/`);
      await evaluate(cdp, set.seed);
    }
  } else if (set.session) {
    await call(
      cdp,
      (items) => {
        for (const [k, v] of Object.entries(items)) sessionStorage.setItem(k, v);
      },
      set.session,
    );
  } else if (set.login === "mock") {
    // Options of the demo runtime are set where src/config.ts creates it: the module is
    // rewritten on its way to the page, for the shots that ask for them.
    await cdp.send("Network.enable");
    await cdp.send("Network.setCacheDisabled", { cacheDisabled: true });
    await cdp.send("Fetch.enable", {
      patterns: [{ urlPattern: "*/src/config.ts*", requestStage: "Response" }],
    });
    cdp.on(async (m) => {
      if (m.method !== "Fetch.requestPaused") return;
      const { requestId, responseStatusCode, responseHeaders } = m.params;
      if (!mockOptions) {
        await cdp.send("Fetch.continueRequest", { requestId });
        return;
      }
      const { body, base64Encoded } = await cdp.send("Fetch.getResponseBody", { requestId });
      const source = base64Encoded ? Buffer.from(body, "base64").toString() : body;
      // The shot's options win over the ones config.ts passes.
      const anchor = "createMockRuntime({";
      if (!source.includes(anchor))
        throw new Error("src/config.ts no longer calls createMockRuntime({");
      const patched = source.replace(
        anchor,
        `((o) => createMockRuntime({ ...o, ...${JSON.stringify(mockOptions)} }))({`,
      );
      await cdp.send("Fetch.fulfillRequest", {
        requestId,
        responseCode: responseStatusCode,
        responseHeaders,
        body: Buffer.from(patched).toString("base64"),
      });
    });
  } else if (set.login === "ui") {
    await waitText(cdp, "Dev login");
    await evaluate(
      cdp,
      `document.querySelector('[aria-labelledby="dev-login-title"] button, [aria-labelledby="dev-login-title"] [role=button]').click()`,
    );
    await sleep(1500);
  }

  for (const shot of SHOTS) {
    if (only && !only.has(shot.id)) continue;
    const [w, h] = shot.size ?? [1440, 900];
    await cdp.send("Emulation.setDeviceMetricsOverride", {
      width: w,
      height: h,
      deviceScaleFactor: 2,
      mobile: false,
    });
    if (set.login === "dev") await devLogin(cdp, shot.as ?? "owner");
    let url = base + shot.path;
    if (set.login === "mock") {
      // Fresh demo data for every shot, signed in as the owner, answers without latency.
      url += (url.includes("?") ? "&" : "?") + "mock-login=1&mock-latency=0";
      mockOptions = shot.mock ?? null;
      await evaluate(cdp, "sessionStorage.clear()");
      if (shot.state) {
        await load(cdp, url);
        await editMockState(cdp, shot.state);
      }
    }
    await load(cdp, url);
    try {
      await waitText(cdp, shot.waitText);
    } catch (e) {
      const page = await evaluate(
        cdp,
        "location.href + ' ' + document.body?.innerText.slice(0, 300)",
      );
      console.error(`${shot.id}: ${e.message}`, process.env.DEBUG ? page : "");
      continue;
    }
    if (shot.prepare) await evaluate(cdp, shot.prepare);
    await sleep(shot.settle ?? 800); // charts and transitions
    if ((await evaluate(cdp, SCRUB)) > 0) {
      console.error(`${shot.id}: a simulator mention is still visible, not captured`);
      continue;
    }
    let clip;
    if (shot.clip) {
      // A function of this script runs in the page; a selector is passed as data.
      const find =
        typeof shot.clip === "function"
          ? `${shot.clip}`
          : "(selector) => document.querySelectorAll(selector)";
      clip = await call(
        cdp,
        `async (selector, pad) => {
          const found = (${find})(selector);
          const els = [...(found?.length !== undefined ? found : [found])].filter(Boolean);
          if (!els.length) throw new Error("clip: nothing matches " + selector);
          els[0].scrollIntoView({ block: "center" });
          await new Promise((r) => setTimeout(r, 300));
          // Page coordinates: the viewport's, plus the scroll.
          const rects = els.map((e) => e.getBoundingClientRect());
          const left = Math.max(0, Math.min(...rects.map((r) => r.left)) - pad);
          const top = Math.max(0, Math.min(...rects.map((r) => r.top)) - pad);
          const right = Math.min(innerWidth, Math.max(...rects.map((r) => r.right)) + pad);
          const bottom = Math.min(innerHeight, Math.max(...rects.map((r) => r.bottom)) + pad);
          const [x, y] = [left + scrollX, top + scrollY];
          return { x, y, width: right - left, height: bottom - top, scale: 1 };
        }`,
        typeof shot.clip === "function" ? shot.id : shot.clip,
        shot.pad ?? 0,
      );
    }
    const { data } = await cdp.send("Page.captureScreenshot", {
      format: "webp",
      quality: 88,
      ...(clip ? { clip } : {}),
    });
    for (const dest of shot.dest ?? ["docs"]) {
      mkdirSync(out[dest], { recursive: true });
      writeFileSync(join(out[dest], `${shot.id}.webp`), Buffer.from(data, "base64"));
    }
    console.log(`${shot.id}`);
  }
  cdp.close();
}

try {
  await main();
} finally {
  chrome.kill();
  await sleep(300);
  rmSync(profile, { recursive: true, force: true });
}
