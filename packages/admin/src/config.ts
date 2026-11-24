import { createCicordClient, type CicordClient, type FetchLike } from "@cicord/client";

export interface AdminConfig {
  client: CicordClient;
  /** URL of "Continue with Discord" (Discord OAuth2, back to /admin/). */
  loginUrl: string;
  /** In-browser fake runtime (`vp dev --mode mock`). */
  mock: boolean;
}

/** Fired when any API call answers 401: the session expired, show the login page. */
export const UNAUTHENTICATED_EVENT = "cicord-admin:unauthenticated";

const BASE = import.meta.env.BASE_URL;

/** Wraps fetch so a 401 anywhere (except the /me probe itself) sends the app to login. */
export function watch401(fetch: FetchLike): FetchLike {
  return async (input, init) => {
    const response = await fetch(input, init);
    if (response.status === 401 && !/\/api\/v1\/me(\?|$)/.test(input)) {
      window.dispatchEvent(new Event(UNAUTHENTICATED_EVENT));
    }
    return response;
  };
}

/** A mock-only option from the query string, remembered in sessionStorage. */
function mockSwitch<T extends string>(
  key: string,
  value: string | null,
  allowed: readonly T[],
): T | undefined {
  try {
    if (value && (allowed as readonly string[]).includes(value)) sessionStorage.setItem(key, value);
    const saved = sessionStorage.getItem(key);
    return saved && (allowed as readonly string[]).includes(saved) ? (saved as T) : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Builds the API client. In mock mode the client talks to an in-browser fake runtime
 * (src/mock), imported only in that mode so production builds never contain it.
 */
export async function createAdminConfig(): Promise<AdminConfig> {
  if (import.meta.env.MODE === "mock") {
    const { createMockRuntime } = await import("./mock/runtime.ts");
    const { MOCK_ANALYTICS_VARIANTS } = await import("./mock/analytics.ts");
    // Mock-only switches (screenshots, demos): ?mock-analytics=full|noPresence|empty|disabled
    // (kept for the tab), ?mock-theme=light|dark, ?mock-login=1 (signed in as the owner) and
    // ?mock-latency=0 (answers at once, for screenshots).
    const params = new URLSearchParams(window.location.search);
    const variant = mockSwitch(
      "cicord-admin:mock:analytics",
      params.get("mock-analytics"),
      MOCK_ANALYTICS_VARIANTS,
    );
    const theme = params.get("mock-theme");
    if (theme === "light" || theme === "dark") {
      try {
        localStorage.setItem("cicord-admin:theme", theme);
      } catch {
        // storage unavailable
      }
    }
    const latency = Number(params.get("mock-latency") ?? Number.NaN);
    const runtime = createMockRuntime({
      demoInstances: 75,
      demoUpdates: true,
      ...(variant ? { analytics: variant } : {}),
      ...(Number.isFinite(latency) ? { latency } : {}),
    });
    if (params.get("mock-login"))
      await runtime.fetch("/auth/dev-login", {
        method: "POST",
        body: JSON.stringify({ userId: "100" }),
        headers: { "Content-Type": "application/json", "X-Cicord-Request": "1" },
      });
    return {
      client: createCicordClient({ fetch: watch401(runtime.fetch) }),
      loginUrl: `${BASE}login?mock-oauth=1`,
      mock: true,
    };
  }
  const client = createCicordClient({
    baseUrl: import.meta.env.VITE_CICORD_API_URL ?? "",
    fetch: watch401((input, init) => globalThis.fetch(input, init)),
  });
  return { client, loginUrl: client.auth.loginUrl(BASE), mock: false };
}
