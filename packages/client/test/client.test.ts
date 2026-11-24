import { describe, expect, test, vi } from "vite-plus/test";
import {
  CicordApiError,
  codeForStatus,
  createCicordClient,
  onboardingApi,
  SseParser,
  type FetchLike,
  type ServerSentEvent,
} from "../src/index.ts";

interface Call {
  url: string;
  method: string;
  headers: Headers;
  body: unknown;
  credentials: RequestCredentials | undefined;
}

function mockFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = [];
  const fetch: FetchLike = async (url, init) => {
    const call: Call = {
      url,
      method: init.method ?? "GET",
      headers: new Headers(init.headers),
      body: typeof init.body === "string" ? JSON.parse(init.body) : init.body,
      credentials: init.credentials,
    };
    calls.push(call);
    return handler(call);
  };
  return { fetch, calls };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const me = {
  user: { id: "1", username: "alice", displayName: "Alice", avatarUrl: "" },
  servers: [{ key: "ma-commu", name: "Ma Commu", role: "admin" }],
};

describe("transport", () => {
  test("sends credentials, JSON headers and base URL", async () => {
    const { fetch, calls } = mockFetch(() => json(me));
    const client = createCicordClient({ baseUrl: "https://cicord.example/", fetch });
    await expect(client.me()).resolves.toEqual(me);
    expect(calls[0]?.url).toBe("https://cicord.example/api/v1/me");
    expect(calls[0]?.credentials).toBe("include");
    expect(calls[0]?.headers.get("Accept")).toBe("application/json");
    expect(calls[0]?.headers.get("X-Cicord-Request")).toBe("1");
  });

  test("bearer token", async () => {
    const { fetch, calls } = mockFetch(() => json({}));
    await createCicordClient({ fetch, token: () => "secret" }).admin.status();
    expect(calls[0]?.headers.get("Authorization")).toBe("Bearer secret");
    expect(calls[0]?.url).toBe("/api/v1/status");
  });

  test("uses the global fetch at call time", async () => {
    const spy = vi.fn(async () => json(me));
    const client = createCicordClient();
    vi.stubGlobal("fetch", spy);
    try {
      await client.me();
      expect(spy).toHaveBeenCalledOnce();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  test("API errors become CicordApiError with diagnostics", async () => {
    const diagnostics = [{ severity: "error", path: "/message", message: "too long" }];
    const { fetch } = mockFetch(() =>
      json({ error: { code: "invalid", message: "config is invalid", diagnostics } }, 422),
    );
    const client = createCicordClient({ fetch });
    const error = await client.admin
      .updatePluginConfig("ma-commu", "moderator", "welcome", { config: {}, revision: "abc" })
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(CicordApiError);
    expect(error).toMatchObject({
      code: "invalid",
      status: 422,
      message: "config is invalid",
      diagnostics,
    });
  });

  test("explained errors carry hint, details and reason", async () => {
    const { fetch } = mockFetch(() =>
      json(
        {
          error: {
            code: "unavailable",
            message: "Announcement and stage channels need the Community feature",
            hint: "Enable Community",
            details: "sync: raw",
            reason: "community_required",
          },
        },
        503,
      ),
    );
    await expect(createCicordClient({ fetch }).admin.sync("ma-commu")).rejects.toMatchObject({
      code: "unavailable",
      hint: "Enable Community",
      details: "sync: raw",
      reason: "community_required",
    });
  });

  test("non-JSON errors map the status to a code", async () => {
    const { fetch } = mockFetch(
      () => new Response("404 page not found", { status: 404, statusText: "Not Found" }),
    );
    await expect(createCicordClient({ fetch }).portal.server("x")).rejects.toMatchObject({
      code: "not_found",
      status: 404,
      message: "404 page not found",
    });
  });

  test("status fallbacks match the runtime's codes", () => {
    expect(codeForStatus(413)).toBe("invalid");
    expect(codeForStatus(415)).toBe("invalid");
    expect(codeForStatus(421)).toBe("forbidden");
    expect(codeForStatus(418)).toBe("http_418");
    expect(codeForStatus(502)).toBe("internal");
  });

  test("network errors", async () => {
    const client = createCicordClient({
      fetch: async () => {
        throw new TypeError("Failed to fetch");
      },
    });
    await expect(client.me()).rejects.toMatchObject({ code: "network", status: 0 });
  });

  test("meOrNull resolves null on 401", async () => {
    const { fetch } = mockFetch(() =>
      json({ error: { code: "unauthenticated", message: "login" } }, 401),
    );
    await expect(createCicordClient({ fetch }).meOrNull()).resolves.toBeNull();
  });

  test("204 resolves undefined", async () => {
    const { fetch, calls } = mockFetch(() => new Response(null, { status: 204 }));
    await expect(createCicordClient({ fetch }).auth.logout()).resolves.toBeUndefined();
    expect(calls[0]).toMatchObject({ method: "POST", url: "/auth/logout" });
  });
});

describe("auth", () => {
  test("loginUrl", () => {
    const client = createCicordClient({ baseUrl: "https://cicord.example" });
    expect(client.auth.loginUrl("/onboarding?x=1")).toBe(
      "https://cicord.example/auth/login?redirect=%2Fonboarding%3Fx%3D1",
    );
    expect(createCicordClient({ authPath: "/api/v1/auth/" }).auth.loginUrl("/")).toBe(
      "/api/v1/auth/login?redirect=%2F",
    );
  });

  test("exchangeLinkToken cleans the URL, posts the token once and shares the exchange", async () => {
    const { fetch, calls } = mockFetch(() => json(me));
    const client = createCicordClient({ fetch });
    const location = {
      href: "https://portal.example/onboarding?server=ma-commu&cicord_token=abc.def#top",
    };
    const history = {
      state: null,
      replaceState: vi.fn((_s: unknown, _u: string, url?: string | URL | null) => {
        location.href = new URL(String(url), location.href).toString();
      }),
    };
    const first = client.auth.exchangeLinkToken({ location, history });
    const second = client.auth.exchangeLinkToken({ location, history });
    expect(history.replaceState).toHaveBeenCalledWith(null, "", "/onboarding?server=ma-commu#top");
    await expect(first).resolves.toEqual(me);
    await expect(second).resolves.toEqual(me);
    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({
      method: "POST",
      url: "/auth/link",
      body: { token: "abc.def" },
    });
    // Settled: no token → null.
    await expect(client.auth.exchangeLinkToken({ location, history })).resolves.toBeNull();
  });

  test("devLogin", async () => {
    const { fetch, calls } = mockFetch(() => json(me));
    await createCicordClient({ fetch }).auth.devLogin("42");
    expect(calls[0]).toMatchObject({
      method: "POST",
      url: "/auth/dev-login",
      body: { userId: "42" },
    });
  });
});

describe("portal & plugins", () => {
  test("portal calls", async () => {
    const server = { key: "ma-commu", name: "Ma Commu", iconUrl: "", theme: {}, plugins: [] };
    const { fetch, calls } = mockFetch((c) =>
      c.url.endsWith("/servers") ? json({ items: [server] }) : json(server),
    );
    const client = createCicordClient({ fetch });
    await expect(client.portal.servers()).resolves.toEqual([server]);
    await client.portal.server("ma-commu");
    await client.portal.me("ma commu");
    expect(calls.map((c) => c.url)).toEqual([
      "/api/v1/portal/servers",
      "/api/v1/portal/ma-commu",
      "/api/v1/portal/ma%20commu/me",
    ]);
  });

  test("plugin routes", async () => {
    const { fetch, calls } = mockFetch(() => json({ ok: true }));
    const routes = createCicordClient({ fetch }).plugin("ma-commu", "moderator", "onboarding");
    await routes.get("/steps", { lang: "fr" });
    await routes.post("complete", { answers: { jeux: ["minecraft"] } });
    await routes.delete("/x");
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/portal/ma-commu/plugins/moderator/onboarding/steps?lang=fr",
      "POST /api/v1/portal/ma-commu/plugins/moderator/onboarding/complete",
      "DELETE /api/v1/portal/ma-commu/plugins/moderator/onboarding/x",
    ]);
    expect(calls[1]?.headers.get("Content-Type")).toBe("application/json");
    expect(calls[1]?.body).toEqual({ answers: { jeux: ["minecraft"] } });
  });

  test("onboardingApi", async () => {
    const { fetch, calls } = mockFetch(() => json({ completed: true }));
    const api = onboardingApi(createCicordClient({ fetch }).plugin("s", "b", "i"));
    await api.complete({ step: ["role"] });
    expect(calls[0]).toMatchObject({ method: "POST", body: { answers: { step: ["role"] } } });
  });
});

describe("admin & machine", () => {
  test("admin endpoints", async () => {
    const { fetch, calls } = mockFetch(() => json({ items: [] }));
    const client = createCicordClient({ fetch });
    await client.admin.history({ limit: 10 });
    await client.admin.history({ target: "server:s" });
    await client.admin.server("s");
    await client.admin.resources("s");
    await client.admin.plan("s");
    await client.admin.sync("s");
    await client.admin.audit("s", { before: 12, limit: 50 });
    await client.admin.plugins("s");
    await client.admin.updatePluginConfig("s", "b", "i", {
      config: { a: 1 },
      scope: "base",
      revision: "r",
    });
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/history?limit=10",
      "GET /api/v1/history?target=server%3As",
      "GET /api/v1/servers/s",
      "GET /api/v1/servers/s/resources",
      "GET /api/v1/servers/s/plan",
      "POST /api/v1/servers/s/sync",
      "GET /api/v1/servers/s/audit?before=12&limit=50",
      "GET /api/v1/servers/s/plugins",
      "PUT /api/v1/servers/s/plugins/b/i/config",
    ]);
    expect(calls[8]?.body).toEqual({ config: { a: 1 }, scope: "base", revision: "r" });
  });

  test("paged plugin list, single instance and uninstall", async () => {
    const page = { items: [], total: 0, offset: 0, limit: 25, sort: "name", order: "asc" };
    const { fetch, calls } = mockFetch((c) =>
      c.method === "DELETE"
        ? json({ removed: true, cleaned: ["cicord.lock: acme/xp"], mode: "commit", revision: "r2" })
        : c.url.includes("?")
          ? json(page)
          : json({ bot: "b", instance: "i" }),
    );
    const client = createCicordClient({ fetch });
    await expect(
      client.admin.pluginsPage("s", {
        q: "  wel come ",
        sort: "health",
        order: "desc",
        offset: 25,
        limit: 25,
      }),
    ).resolves.toEqual(page);
    // Empty values are left out of the query.
    await client.admin.pluginsPage("s", { q: " ", limit: 50 });
    await client.admin.plugin("s", "b", "my instance");
    await expect(client.admin.uninstallPlugin("s", "b", "xp")).resolves.toEqual({
      removed: true,
      cleaned: ["cicord.lock: acme/xp"],
      mode: "commit",
      revision: "r2",
    });
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/servers/s/plugins?q=wel+come&sort=health&order=desc&offset=25&limit=25",
      "GET /api/v1/servers/s/plugins?limit=50",
      "GET /api/v1/servers/s/plugins/b/my%20instance",
      "DELETE /api/v1/servers/s/plugins/b/xp",
    ]);
    expect(calls[3]?.headers.get("X-Cicord-Request")).toBe("1");
  });

  test("server asset upload", async () => {
    const { fetch, calls } = mockFetch(() => json({ mode: "commit", warnings: [] }));
    const client = createCicordClient({ fetch });
    const image = new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], { type: "image/png" });
    await client.admin.uploadServerAsset("s", "banner", image, { revision: "r1" });
    await client.admin.uploadServerAsset("s", "icon", new Uint8Array([1]));
    expect(calls[0]).toMatchObject({
      method: "PUT",
      url: "/api/v1/servers/s/assets/banner?revision=r1",
    });
    expect(calls[0]?.headers.get("Content-Type")).toBe("image/png");
    expect(calls[0]?.body).toBeInstanceOf(Blob);
    expect(calls[1]?.headers.get("Content-Type")).toBe("application/octet-stream");
    expect(client.admin.serverAssetUrl("s", "icon")).toBe("/api/v1/servers/s/assets/icon");
  });

  test("machine endpoints", async () => {
    const { fetch, calls } = mockFetch((c) =>
      c.url === "/readyz"
        ? new Response("not ready", { status: 503 })
        : new Response("ok", { status: 200 }),
    );
    const client = createCicordClient({ fetch, token: "t" });
    await expect(client.machine.healthz()).resolves.toBe(true);
    await expect(client.machine.readyz()).resolves.toBe(false);
    await client.machine.plan(new Uint8Array([1, 2, 3]));
    expect(calls[2]?.url).toBe("/api/v1/plan");
    expect(calls[2]?.headers.get("Content-Type")).toBe("application/gzip");
    expect(calls[2]?.body).toBeInstanceOf(Uint8Array);
  });
});

describe("store & registries", () => {
  const source = {
    name: "acme",
    kind: "git",
    url: "https://github.com/acme/cicord-plugins.git",
    trust: "unofficial",
    plugins: [],
  };

  test("store endpoints", async () => {
    const { fetch, calls } = mockFetch((c) =>
      c.url.endsWith("/store") || c.url.endsWith("/store/refresh")
        ? json({ sources: [source] })
        : json({ instance: "xp", health: "Healthy", mode: "commit", revision: "abc" }),
    );
    const client = createCicordClient({ fetch });
    await expect(client.store.list()).resolves.toEqual([source]);
    await expect(client.store.refresh()).resolves.toEqual([source]);
    await client.store.detail("acme", "x/p");
    const request = {
      source: "acme",
      plugin: "xp",
      instance: "levels",
      config: { channel: "general" },
      version: "1.2.0",
      commit: "c0ffee",
      integrity: "sha256-abc",
      confirmUnofficial: true,
    };
    await expect(client.store.install("ma commu", request)).resolves.toMatchObject({
      instance: "xp",
      mode: "commit",
    });
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/store",
      "POST /api/v1/store/refresh",
      "GET /api/v1/store/acme/x%2Fp",
      "POST /api/v1/servers/ma%20commu/store/install",
    ]);
    expect(calls[3]?.body).toEqual(request);
    // CSRF header on the state-changing calls.
    expect(calls[1]?.headers.get("X-Cicord-Request")).toBe("1");
    expect(calls[3]?.headers.get("X-Cicord-Request")).toBe("1");
    expect(calls[3]?.headers.get("Content-Type")).toBe("application/json");
  });

  test("store summary and paged plugins with filters", async () => {
    const summary = { ...source, private: true, credentials: "missing", pluginCount: 3 };
    const page = { items: [], total: 0, offset: 50, limit: 50, sort: "installed", order: "desc" };
    const { fetch, calls } = mockFetch((c) =>
      c.url.includes("/store/plugins") ? json(page) : json({ sources: [summary] }),
    );
    const client = createCicordClient({ fetch });
    await expect(client.store.sources()).resolves.toEqual([summary]);
    await client.store.sources({ summary: false });
    await expect(
      client.store.plugins({
        q: "xp",
        sort: "installed",
        order: "desc",
        offset: 50,
        limit: 50,
        source: "acme",
        trust: "unofficial",
        installed: false,
      }),
    ).resolves.toEqual(page);
    await client.store.plugins();
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/store?summary=1",
      "GET /api/v1/store",
      "GET /api/v1/store/plugins?q=xp&sort=installed&order=desc&offset=50&limit=50&source=acme&trust=unofficial&installed=false",
      "GET /api/v1/store/plugins",
    ]);
  });

  test("plugin updates", async () => {
    const result = { version: "1.1.0", mode: "commit", revision: "r2", instances: [] };
    const { fetch, calls } = mockFetch((c) =>
      c.method === "POST" ? json(result) : json({ available: true, items: [], total: 0 }),
    );
    const client = createCicordClient({ fetch });
    await client.store.updateReview("acme", "xp");
    const request = {
      version: "1.1.0",
      commit: "c0ffee",
      integrity: "sha256-abc",
      confirmUnofficial: true,
    };
    await expect(client.store.update("acme", "xp", request)).resolves.toEqual(result);
    await client.store.update("cicord", "welcome", { version: "0.2.0", force: true });
    await client.store.plugins({ updates: true });
    await client.admin.pluginsPage("ma-commu", { updates: true, limit: 25 });
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/store/acme/xp/update",
      "POST /api/v1/store/acme/xp/update",
      "POST /api/v1/store/cicord/welcome/update",
      "GET /api/v1/store/plugins?updates=true",
      "GET /api/v1/servers/ma-commu/plugins?limit=25&updates=true",
    ]);
    expect(calls[1]?.body).toEqual(request);
    expect(calls[2]?.body).toEqual({ version: "0.2.0", force: true });
    expect(calls[1]?.headers.get("X-Cicord-Request")).toBe("1");
  });

  test("private registry tokens", async () => {
    const { fetch, calls } = mockFetch(() => json({ mode: "commit", revision: "r" }));
    const client = createCicordClient({ fetch });
    await client.registries.add({ name: "acme", url: "https://git.acme.dev/p.git", token: "tok" });
    await client.registries.setToken("acme", "new-token");
    await client.registries.removeToken("acme");
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "POST /api/v1/registries",
      "PUT /api/v1/registries/acme/token",
      "DELETE /api/v1/registries/acme/token",
    ]);
    expect(calls[0]?.body).toEqual({
      name: "acme",
      url: "https://git.acme.dev/p.git",
      token: "tok",
    });
    expect(calls[1]?.body).toEqual({ token: "new-token" });
    expect(calls.every((c) => c.headers.get("X-Cicord-Request") === "1")).toBe(true);
  });

  test("registry endpoints", async () => {
    const { fetch, calls } = mockFetch(() => json({ mode: "pullRequest", pullRequestUrl: "u" }));
    const client = createCicordClient({ fetch });
    await expect(
      client.registries.add({ name: "acme", url: "https://github.com/acme/cicord-plugins.git" }),
    ).resolves.toEqual({ mode: "pullRequest", pullRequestUrl: "u" });
    await client.registries.remove("acme");
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "POST /api/v1/registries",
      "DELETE /api/v1/registries/acme",
    ]);
    expect(calls[0]?.body).toEqual({
      name: "acme",
      url: "https://github.com/acme/cicord-plugins.git",
    });
    expect(calls.every((c) => c.headers.get("X-Cicord-Request") === "1")).toBe(true);
  });

  test("abort signals are passed through", async () => {
    const signals: (AbortSignal | null | undefined)[] = [];
    const fetch: FetchLike = async (_url, init) => {
      signals.push(init.signal);
      return json({ sources: [] });
    };
    const client = createCicordClient({ fetch });
    const ctrl = new AbortController();
    await client.store.list({ signal: ctrl.signal });
    await client.store.detail("cicord", "welcome", { signal: ctrl.signal });
    expect(signals).toEqual([ctrl.signal, ctrl.signal]);
  });

  test("store errors keep their code, hint and body", async () => {
    const { fetch } = mockFetch((c) =>
      c.url.includes("/install")
        ? json(
            {
              error: {
                code: "changed",
                message: "xp now resolves to 1.3.0 (was 1.2.0)",
                hint: "Review the plugin again.",
              },
            },
            409,
          )
        : c.method === "DELETE"
          ? json(
              {
                error: {
                  code: "in_use",
                  message: "registry acme is in use",
                  details: "bots/moderator/bot.yaml: xp",
                },
              },
              409,
            )
          : json({ error: { code: "registry_unavailable", message: "fetch failed" } }, 502),
    );
    const client = createCicordClient({ fetch });
    const changed = await client.store
      .install("s", { source: "acme", plugin: "xp" })
      .catch((e: unknown) => e);
    expect(changed).toBeInstanceOf(CicordApiError);
    expect(changed).toMatchObject({
      code: "changed",
      status: 409,
      hint: "Review the plugin again.",
    });
    await expect(client.registries.remove("acme")).rejects.toMatchObject({
      code: "in_use",
      details: "bots/moderator/bot.yaml: xp",
    });
    await expect(client.store.detail("acme", "xp")).rejects.toMatchObject({
      code: "registry_unavailable",
      status: 502,
    });
  });
});

describe("simulator", () => {
  test("sim endpoints", async () => {
    const { fetch, calls } = mockFetch(() => json({ items: [] }));
    const client = createCicordClient({ fetch });
    await client.sim.state();
    await client.sim.createUser("bob");
    await client.sim.join("srv", "u1");
    await client.sim.leave("srv", "u1");
    await client.sim.messages("c1", { limit: 20 });
    await client.sim.dms("u1");
    await client.sim.dms("u1", { limit: 5 });
    await client.sim.sendMessage({ userId: "u1", channelId: "c1", content: "hi" });
    await client.sim.command({
      userId: "u1",
      serverId: "srv",
      channelId: "c1",
      bot: "moderator",
      name: "onboarding",
    });
    await client.sim.component({ userId: "u1", channelId: "c1", messageId: "m", customId: "x" });
    await client.sim.commands("srv");
    await client.sim.modal({
      userId: "u1",
      serverId: "srv",
      channelId: "c1",
      bot: "moderator",
      customId: "c:welcome:form",
      fields: { name: "Alice" },
    });
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/sim/state",
      "POST /api/v1/sim/users",
      "POST /api/v1/sim/servers/srv/members",
      "DELETE /api/v1/sim/servers/srv/members/u1",
      "GET /api/v1/sim/channels/c1/messages?limit=20",
      "GET /api/v1/sim/users/u1/dms",
      "GET /api/v1/sim/users/u1/dms?limit=5",
      "POST /api/v1/sim/messages",
      "POST /api/v1/sim/commands",
      "POST /api/v1/sim/components",
      "GET /api/v1/sim/commands?serverId=srv",
      "POST /api/v1/sim/modals",
    ]);
    expect(calls[8]?.body).toMatchObject({ options: {}, name: "onboarding" });
    expect(calls[9]?.body).toMatchObject({ values: [] });
    expect(calls[11]?.body).toEqual({
      userId: "u1",
      serverId: "srv",
      channelId: "c1",
      bot: "moderator",
      customId: "c:welcome:form",
      fields: { name: "Alice" },
    });
  });

  test("SSE subscription over fetch", async () => {
    const encoder = new TextEncoder();
    let calls = 0;
    const fetch: FetchLike = async (_url, init) => {
      calls++;
      expect(new Headers(init.headers).get("Accept")).toBe("text/event-stream");
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(encoder.encode(": hello\n\nevent: change\ndata: {}\n\n"));
          controller.enqueue(encoder.encode("event: other\ndata: x\n\nevent: chan"));
          controller.enqueue(encoder.encode("ge\nid: 7\ndata: a\ndata: b\n\n"));
          controller.close();
        },
      });
      return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
    };
    const client = createCicordClient({ fetch });
    const events: ServerSentEvent[] = [];
    const stream = client.sim.subscribe((e) => events.push(e), { once: true });
    await stream.closed;
    expect(calls).toBe(1);
    expect(events).toEqual([
      { event: "change", data: "{}", id: "" },
      { event: "change", data: "a\nb", id: "7" },
    ]);
  });

  test("SSE reconnects after errors until closed", async () => {
    let calls = 0;
    const client = createCicordClient({
      fetch: async () => {
        calls++;
        return new Response("down", { status: 503 });
      },
    });
    const errors: unknown[] = [];
    const stream = client.events("/sim/events", {
      onEvent() {},
      onError: (e) => errors.push(e),
      retryMs: 1,
    });
    await vi.waitFor(() => expect(calls).toBeGreaterThanOrEqual(3));
    stream.close();
    await stream.closed;
    expect(errors.length).toBeGreaterThanOrEqual(3);
  });

  test("SseParser handles CRLF split across chunks", () => {
    const out: ServerSentEvent[] = [];
    const p = new SseParser((e) => out.push(e));
    p.push("data: one\r");
    p.push("\n\r\n");
    expect(out).toEqual([{ event: "message", data: "one", id: "" }]);
  });
});

describe("analytics", () => {
  const status = { enabled: true, presence: false, since: null, retentionDays: 400, missing: [] };

  test("summary, series and catalog URLs and query encoding", async () => {
    const { fetch, calls } = mockFetch(() => json({ status }));
    const client = createCicordClient({ fetch });
    await client.analytics.summary("ma commu", "90d");
    await client.analytics.summary("s");
    await client.analytics.series("s", {
      metrics: ["messages.sent", "members.joined"],
      from: new Date("2026-11-01T00:00:00.123Z"),
      to: "2026-11-24T00:00:00Z",
      step: "day",
      top: 5,
    });
    await client.analytics.series("s", { metrics: ["voice.seconds"], dim: "ch:1" });
    await client.analytics.catalog("s");
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/v1/servers/ma%20commu/analytics/summary?period=90d",
      "GET /api/v1/servers/s/analytics/summary?period=30d",
      "GET /api/v1/servers/s/analytics/series?metrics=messages.sent%2Cmembers.joined&from=2026-11-01T00%3A00%3A00Z&to=2026-11-24T00%3A00%3A00Z&step=day&top=5",
      "GET /api/v1/servers/s/analytics/series?metrics=voice.seconds&dim=ch%3A1",
      "GET /api/v1/servers/s/analytics/catalog",
    ]);
  });

  test("returns the response and forwards the abort signal", async () => {
    const summary = {
      server: "s",
      period: "7d",
      from: "2026-11-17T00:00:00Z",
      to: "2026-11-24T00:00:00Z",
      kpis: [{ metric: "members.total", value: 120, previous: null, spark: [[1, 120]] }],
      rankings: {},
      heatmap: [],
      labels: {},
      status,
    };
    let signal: AbortSignal | null | undefined;
    const fetch: FetchLike = async (_url, init) => {
      signal = init.signal;
      return json(summary);
    };
    const controller = new AbortController();
    const client = createCicordClient({ fetch });
    await expect(
      client.analytics.summary("s", "7d", { signal: controller.signal }),
    ).resolves.toEqual(summary);
    expect(signal).toBe(controller.signal);
  });

  test("rejects with CicordApiError", async () => {
    const { fetch } = mockFetch(() =>
      json({ error: { code: "invalid", message: "at most 2000 points per series" } }, 400),
    );
    const error = await createCicordClient({ fetch })
      .analytics.series("s", { metrics: ["messages.sent"], step: "hour" })
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(CicordApiError);
    expect((error as CicordApiError).code).toBe("invalid");
    expect((error as CicordApiError).status).toBe(400);
  });
});
