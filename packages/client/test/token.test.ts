import { describe, expect, test } from "vite-plus/test";
import {
  createCicordClient,
  isSafeUrl,
  levelsApi,
  memoryTokenStore,
  ticketsApi,
  webStorageTokenStore,
  type FetchLike,
} from "../src/index.ts";

interface Call {
  url: string;
  method: string;
  headers: Headers;
  body: unknown;
  credentials: RequestCredentials | undefined;
}

function mockFetch(handler: (call: Call) => Response) {
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
  servers: [{ key: "ma-commu", name: "Ma Commu", role: "member" }],
};
const future = new Date(Date.now() + 3600_000).toISOString();
const session = { token: "cicord_st_abc", expiresAt: future, me };

const base = "https://cicord.example";

describe("token mode", () => {
  test("dev login stores the token and sends it as Authorization, without credentials", async () => {
    const { fetch, calls } = mockFetch((c) =>
      c.url.endsWith("/auth/dev-login") ? json(session) : json(me),
    );
    const store = memoryTokenStore();
    const client = createCicordClient({ baseUrl: base, fetch, auth: "token", tokenStore: store });
    let changes = 0;
    client.onSessionChange(() => changes++);
    await expect(client.auth.devLogin("1")).resolves.toEqual(me);
    expect(calls[0]?.body).toEqual({ userId: "1", mode: "token" });
    expect(calls[0]?.credentials).toBe("omit");
    expect(store.get()).toEqual({ token: "cicord_st_abc", expiresAt: future });
    expect(changes).toBe(1);

    await client.me();
    expect(calls[1]?.headers.get("Authorization")).toBe("Bearer cicord_st_abc");
    expect(calls[1]?.credentials).toBe("omit");
  });

  test("a 401 forgets the token; expired tokens are dropped", async () => {
    const { fetch, calls } = mockFetch(() =>
      json({ error: { code: "unauthenticated", message: "expired" } }, 401),
    );
    const store = memoryTokenStore();
    store.set({ token: "cicord_st_old", expiresAt: future });
    const client = createCicordClient({ baseUrl: base, fetch, auth: "token", tokenStore: store });
    await expect(client.meOrNull()).resolves.toBeNull();
    expect(store.get()).toBeNull();

    store.set({ token: "cicord_st_gone", expiresAt: new Date(Date.now() - 1000).toISOString() });
    expect(client.session()).toBeNull();
    await client.meOrNull();
    expect(calls[1]?.headers.get("Authorization")).toBeNull();
  });

  test("login URL carries mode=token and an absolute redirect", () => {
    const client = createCicordClient({ baseUrl: base, auth: "token", tokenStore: "memory" });
    const url = new URL(client.auth.loginUrl("https://fans.example/members?x=1"));
    expect(url.origin + url.pathname).toBe(`${base}/auth/login`);
    expect(url.searchParams.get("mode")).toBe("token");
    expect(url.searchParams.get("redirect")).toBe("https://fans.example/members?x=1");
  });

  test("exchangeLinkToken redeems cicord_code and cleans the URL", async () => {
    const { fetch, calls } = mockFetch(() => json(session));
    const store = memoryTokenStore();
    const client = createCicordClient({ baseUrl: base, fetch, auth: "token", tokenStore: store });
    let replaced = "";
    await expect(
      client.auth.exchangeLinkToken({
        location: { href: "https://fans.example/cb?cicord_code=one-time&keep=1" },
        history: { replaceState: (_d, _u, url) => void (replaced = String(url)) },
      }),
    ).resolves.toEqual(me);
    expect(calls[0]?.url).toBe(`${base}/auth/token`);
    expect(calls[0]?.body).toEqual({ code: "one-time" });
    expect(replaced).toBe("/cb?keep=1");
    expect(store.get()?.token).toBe("cicord_st_abc");
  });

  test("link tokens in token mode", async () => {
    const { fetch, calls } = mockFetch(() => json(session));
    const client = createCicordClient({
      baseUrl: base,
      fetch,
      auth: "token",
      tokenStore: "memory",
    });
    await client.auth.link("signed");
    expect(calls[0]?.body).toEqual({ token: "signed", mode: "token" });
    expect(client.session()?.token).toBe("cicord_st_abc");
  });

  test("logout revokes and forgets the token even when the request fails", async () => {
    const { fetch, calls } = mockFetch(() =>
      json({ error: { code: "internal", message: "x" } }, 500),
    );
    const store = memoryTokenStore();
    store.set({ token: "cicord_st_abc", expiresAt: future });
    const client = createCicordClient({ baseUrl: base, fetch, auth: "token", tokenStore: store });
    await expect(client.auth.logout()).rejects.toThrow();
    expect(calls[0]?.headers.get("Authorization")).toBe("Bearer cicord_st_abc");
    expect(store.get()).toBeNull();
  });

  test("web storage store", () => {
    const data = new Map<string, string>();
    const storage = {
      getItem: (k: string) => data.get(k) ?? null,
      setItem: (k: string, v: string) => void data.set(k, v),
      removeItem: (k: string) => void data.delete(k),
    };
    const store = webStorageTokenStore(storage, "k");
    store.set({ token: "t", expiresAt: future });
    expect(JSON.parse(data.get("k")!)).toEqual({ token: "t", expiresAt: future });
    expect(store.get()).toEqual({ token: "t", expiresAt: future });
    data.set("k", "{not json");
    expect(store.get()).toBeNull();
    store.set(null);
    expect(data.has("k")).toBe(false);
  });

  test("cookie mode is unchanged", async () => {
    const { fetch, calls } = mockFetch(() => json(me));
    const client = createCicordClient({ fetch });
    await client.auth.devLogin("1");
    expect(calls[0]?.body).toEqual({ userId: "1" });
    expect(calls[0]?.credentials).toBe("include");
    expect(client.session()).toBeNull();
  });
});

describe("portal", () => {
  const server = {
    key: "ma-commu",
    name: "Ma Commu",
    iconUrl: "",
    theme: { primary: "", accent: "", logo: "/api/v1/portal/ma-commu/logo?v=1", mode: "" },
    title: "Ma Commu",
    tagline: "",
    description: "",
    banner: "/api/v1/portal/ma-commu/banner?v=1",
    links: [],
    pages: { onboarding: true, profile: true, leaderboard: true, tickets: true },
    locale: "",
    plugins: [],
  };

  test("image URLs are absolute when the runtime is on another origin", async () => {
    const { fetch } = mockFetch(() => json({ items: [server] }));
    const [s] = await createCicordClient({ baseUrl: base, fetch }).portal.servers();
    expect(s?.theme.logo).toBe(`${base}/api/v1/portal/ma-commu/logo?v=1`);
    expect(s?.banner).toBe(`${base}/api/v1/portal/ma-commu/banner?v=1`);
    const { fetch: f2 } = mockFetch(() => json(server));
    const same = await createCicordClient({ fetch: f2 }).portal.server("ma-commu");
    expect(same.banner).toBe("/api/v1/portal/ma-commu/banner?v=1");
  });

  test("isSafeUrl", () => {
    expect(isSafeUrl("https://x.example/a")).toBe(true);
    expect(isSafeUrl("http://x.example")).toBe(true);
    expect(isSafeUrl("javascript:alert(1)")).toBe(false);
    expect(isSafeUrl("/relative")).toBe(false);
  });

  test("showcase plugin wrappers", async () => {
    const { fetch, calls } = mockFetch(() => json({ items: [] }));
    const client = createCicordClient({ fetch });
    await ticketsApi(client.plugin("showcase", "cicord", "tickets")).list({ status: "open" });
    await levelsApi(client.plugin("showcase", "cicord", "levels")).leaderboard({ limit: 10 });
    expect(calls.map((c) => c.url)).toEqual([
      "/api/v1/portal/showcase/plugins/cicord/tickets/tickets?status=open",
      "/api/v1/portal/showcase/plugins/cicord/levels/leaderboard?limit=10",
    ]);
  });
});
