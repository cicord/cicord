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
// default macOS location; DEBUG=1 prints the page text of a shot that times out.
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
 * default docs). `clip` crops to an element (CSS selector) instead of the viewport.
 *
 * - showcase: `cicord dev examples/showcase --sim --listen 127.0.0.1:7490`
 * - community: `cicord dev <cicord-community checkout> --sim --listen 127.0.0.1:7491`
 * - demo: the admin console's demo data (`vp dev --mode mock --port 5190` in packages/admin),
 *   for the pages that need history: analytics, plan, drift, sync history, audit log.
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
  demo: {
    url: "http://localhost:5190",
    login: "ui",
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
  const re = /simulat|mock runtime/i;
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
const PORT = 9333;
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
    const ok = await evaluate(cdp, `document.body?.innerText.includes(${JSON.stringify(text)})`);
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
  const status = await evaluate(
    cdp,
    `fetch("/auth/dev-login", {method: "POST", headers: {"Content-Type": "application/json", "X-Cicord-Request": "1"},
      body: JSON.stringify({userId: ${JSON.stringify(user.id)}})}).then(r => r.status)`,
  );
  if (status !== 200) throw new Error(`dev login as ${username}: HTTP ${status}`);
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
    await load(cdp, base + shot.path);
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
      clip = await evaluate(
        cdp,
        `(() => { const r = document.querySelector(${JSON.stringify(shot.clip)}).getBoundingClientRect();
           return {x: r.x, y: r.y, width: r.width, height: r.height, scale: 1}; })()`,
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
