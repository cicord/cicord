import { CodiApiError } from "./errors.ts";
import {
  openEventStream,
  type EventStream,
  type EventStreamOptions,
  type ServerSentEvent,
} from "./sse.ts";
import type {
  AuditEntry,
  AuditQuery,
  InteractionResult,
  Items,
  Json,
  HistoryQuery,
  Me,
  Message,
  Plan,
  PluginInstance,
  PortalMe,
  PortalServer,
  RemotePlanResult,
  RuntimeStatus,
  ServerDetail,
  ServerResources,
  SimCommand,
  SimCommandRequest,
  SimComponentRequest,
  SimModalRequest,
  SimSendMessageRequest,
  SimState,
  SyncRecord,
  SyncResult,
  TokenSession,
  UpdatePluginConfigRequest,
  User,
  WriteBackResult,
} from "./types.ts";

export type FetchLike = (input: string, init: RequestInit) => Promise<Response>;

/**
 * How browser clients authenticate (spec/api.md §1):
 * - `"cookie"` (default): the `codi_session` cookie, for a portal served by the runtime or
 *   on a same-site origin listed in `--cors-origin`;
 * - `"token"`: a bearer session token kept in a `TokenStore` and sent as `Authorization`,
 *   for sites embedding the SDK on another origin (codi.yaml `spec.portal.allowedOrigins`).
 */
export type AuthMode = "cookie" | "token";

/** A token-mode session as stored by the client. */
export interface StoredSession {
  token: string;
  expiresAt: string;
}

/** Where token mode keeps the session token. */
export interface TokenStore {
  get(): StoredSession | null;
  set(session: StoredSession | null): void;
}

/** Keeps the token in memory (lost on reload; safest against XSS persistence). */
export function memoryTokenStore(): TokenStore {
  let value: StoredSession | null = null;
  return { get: () => value, set: (session) => void (value = session) };
}

/** Keeps the token in `localStorage` / `sessionStorage` (falls back to memory when unavailable). */
export function webStorageTokenStore(
  storage: Pick<Storage, "getItem" | "setItem" | "removeItem"> | undefined,
  key = "codi:session",
): TokenStore {
  const memory = memoryTokenStore();
  if (!storage) return memory;
  return {
    get: () => {
      try {
        const raw = storage.getItem(key);
        const parsed = raw ? (JSON.parse(raw) as Partial<StoredSession>) : null;
        return parsed && typeof parsed.token === "string" && typeof parsed.expiresAt === "string"
          ? { token: parsed.token, expiresAt: parsed.expiresAt }
          : null;
      } catch {
        return memory.get();
      }
    },
    set: (session) => {
      try {
        if (session) storage.setItem(key, JSON.stringify(session));
        else storage.removeItem(key);
      } catch {
        memory.set(session);
      }
    },
  };
}

/** True for absolute http(s) URLs (links from the server config, plugin data…). */
export function isSafeUrl(url: string): boolean {
  try {
    const u = new URL(url);
    return u.protocol === "https:" || u.protocol === "http:";
  } catch {
    return false;
  }
}

export interface CodiClientOptions {
  /**
   * Origin (or origin + prefix) of the CODI runtime, e.g. "https://codi.example.org".
   * Default "": same origin as the page.
   */
  baseUrl?: string;
  /** API prefix. Default "/api/v1". */
  apiPath?: string;
  /** Auth prefix (spec/api.md §1). Default "/auth". */
  authPath?: string;
  /** fetch implementation. Default: the global `fetch`, looked up at call time. */
  fetch?: FetchLike;
  /** Bearer token (runtime token) for machine clients; a function is called per request. */
  token?: string | (() => string | undefined | null);
  /** Extra headers sent with every request. */
  headers?: Record<string, string>;
  /**
   * Fetch credentials mode. Default "include" in cookie mode (session cookie, also
   * cross-origin with CORS), "omit" in token mode.
   */
  credentials?: RequestCredentials;
  /** Browser auth mode. Default "cookie". See `AuthMode`. */
  auth?: AuthMode;
  /**
   * Token mode: where the session token lives. "local" (default in browsers) and
   * "session" use Web Storage under `storageKey`; "memory" forgets it on reload.
   */
  tokenStore?: TokenStore | "local" | "session" | "memory";
  /** Web Storage key of token mode. Default "codi:session:<baseUrl>". */
  storageKey?: string;
}

export type Query = Record<string, string | number | boolean | undefined | null>;

export interface RequestOptions {
  query?: Query;
  /** JSON-serialisable body, or a raw body (Blob, ArrayBuffer, Uint8Array, string, FormData…). */
  body?: unknown;
  /** Content type for raw bodies. */
  contentType?: string;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

export interface CallOptions {
  signal?: AbortSignal;
}

/** Calls to the routes of one plugin instance (spec/api.md §6). */
export interface PluginRoutes {
  readonly server: string;
  readonly bot: string;
  readonly instance: string;
  /** Absolute URL of a route path. */
  url: (path: string, query?: Query) => string;
  request<T = Json>(method: string, path: string, options?: RequestOptions): Promise<T>;
  get<T = Json>(path: string, query?: Query, options?: CallOptions): Promise<T>;
  post<T = Json>(path: string, body?: unknown, options?: CallOptions): Promise<T>;
  put<T = Json>(path: string, body?: unknown, options?: CallOptions): Promise<T>;
  patch<T = Json>(path: string, body?: unknown, options?: CallOptions): Promise<T>;
  delete<T = Json>(path: string, options?: CallOptions): Promise<T>;
}

/** Minimal Location / History used by the auth helpers (injectable for tests and SSR). */
export interface LocationLike {
  href: string;
  assign?: (url: string) => void;
}
export interface HistoryLike {
  replaceState: (data: unknown, unused: string, url?: string | URL | null) => void;
  readonly state?: unknown;
}

export interface ExchangeLinkTokenOptions {
  location?: LocationLike;
  history?: HistoryLike;
  /** Query parameter carrying the token. Default "codi_token". */
  param?: string;
  /** Query parameter carrying the one-time code of a token-mode login. Default "codi_code". */
  codeParam?: string;
}

/** Query parameters a login may leave in the URL (removed by `exchangeLinkToken`). */
export const LOGIN_PARAMS = ["codi_token", "codi_code"] as const;

const isRawBody = (body: unknown): boolean =>
  typeof body === "string" ||
  body instanceof ArrayBuffer ||
  ArrayBuffer.isView(body) ||
  (typeof Blob !== "undefined" && body instanceof Blob) ||
  (typeof FormData !== "undefined" && body instanceof FormData) ||
  (typeof URLSearchParams !== "undefined" && body instanceof URLSearchParams) ||
  (typeof ReadableStream !== "undefined" && body instanceof ReadableStream);

const seg = (value: string): string => encodeURIComponent(value);

function trimSlash(value: string): string {
  return value.replace(/\/+$/, "");
}

function withQuery(url: string, query?: Query): string {
  if (!query) return url;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null) params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${url}${url.includes("?") ? "&" : "?"}${qs}` : url;
}

/**
 * Typed client for the CODI runtime HTTP API v1 (spec/api.md). Framework-agnostic: works in
 * browsers, Node 24, workers. Every non-2xx response rejects with `CodiApiError`.
 */
export class CodiClient {
  readonly baseUrl: string;
  readonly apiPath: string;
  readonly authPath: string;
  readonly authMode: AuthMode;
  readonly #options: CodiClientOptions;
  readonly #store: TokenStore;
  readonly #listeners = new Set<() => void>();
  #pendingLink: Promise<Me> | undefined;

  constructor(options: CodiClientOptions = {}) {
    this.#options = options;
    this.baseUrl = trimSlash(options.baseUrl ?? "");
    this.apiPath = trimSlash(options.apiPath ?? "/api/v1");
    this.authPath = trimSlash(options.authPath ?? "/auth");
    this.authMode = options.auth ?? "cookie";
    this.#store = resolveStore(
      options.tokenStore,
      options.storageKey ?? `codi:session:${this.baseUrl}`,
    );
  }

  // ─────────────────────────────────────────────────────────── token-mode session

  /** The token-mode session, or null (cookie mode, logged out or expired). */
  session(): StoredSession | null {
    if (this.authMode !== "token") return null;
    const s = this.#store.get();
    if (s && Date.parse(s.expiresAt) <= Date.now()) {
      this.#setSession(null);
      return null;
    }
    return s;
  }

  /** Called when the token-mode session changes (login, logout, expiry, revocation). */
  onSessionChange(listener: () => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  #setSession(session: StoredSession | null): void {
    this.#store.set(session);
    for (const listener of this.#listeners) listener();
  }

  #adopt(session: TokenSession): Me {
    this.#setSession({ token: session.token, expiresAt: session.expiresAt });
    return session.me;
  }

  // ─────────────────────────────────────────────────────────── transport

  /** Absolute URL of a path relative to the runtime origin (e.g. "/api/v1/me"). */
  url(path: string, query?: Query): string {
    return withQuery(`${this.baseUrl}${path.startsWith("/") ? path : `/${path}`}`, query);
  }

  /** URL of an API path (e.g. "/me" → "/api/v1/me"). */
  apiUrl(path: string, query?: Query): string {
    return this.url(`${this.apiPath}${path.startsWith("/") ? path : `/${path}`}`, query);
  }

  #headers(extra?: Record<string, string>): Headers {
    const headers = new Headers(this.#options.headers);
    // CSRF protection (spec/api.md §1): the runtime rejects cookie-authenticated
    // state-changing requests without this header.
    headers.set("X-Codi-Request", "1");
    const token =
      (typeof this.#options.token === "function" ? this.#options.token() : this.#options.token) ??
      this.session()?.token;
    if (token) headers.set("Authorization", `Bearer ${token}`);
    for (const [k, v] of Object.entries(extra ?? {})) headers.set(k, v);
    return headers;
  }

  #fetch: FetchLike = (input, init) => {
    const impl = this.#options.fetch ?? (globalThis.fetch as FetchLike | undefined);
    if (!impl) throw new Error("@codi/client: no fetch implementation available");
    return impl(input, init);
  };

  /** Sends a request to an absolute runtime path (no API prefix added). */
  async request<T = Json>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
    const headers = this.#headers(options.headers);
    if (!headers.has("Accept")) headers.set("Accept", "application/json");
    let body: BodyInit | undefined;
    if (options.body !== undefined) {
      if (isRawBody(options.body)) {
        body = options.body as BodyInit;
        if (options.contentType) headers.set("Content-Type", options.contentType);
      } else {
        body = JSON.stringify(options.body);
        headers.set("Content-Type", options.contentType ?? "application/json");
      }
    }
    const init: RequestInit = {
      method: method.toUpperCase(),
      headers,
      credentials: this.#credentials(),
      ...(body === undefined ? {} : { body }),
      ...(options.signal ? { signal: options.signal } : {}),
    };

    let response: Response;
    try {
      response = await this.#fetch(this.url(path, options.query), init);
    } catch (cause) {
      if (options.signal?.aborted) throw cause;
      throw new CodiApiError({
        code: "network",
        status: 0,
        message: cause instanceof Error ? cause.message : "network error",
        cause,
      });
    }

    const parsed = await readBody(response);
    if (!response.ok) {
      // A revoked or expired token: forget it so the app shows the logged-out state.
      if (response.status === 401 && this.session()?.token && !this.#options.token)
        this.#setSession(null);
      throw CodiApiError.fromResponse(response.status, response.statusText, parsed);
    }
    return parsed as T;
  }

  #credentials(): RequestCredentials {
    return this.#options.credentials ?? (this.authMode === "token" ? "omit" : "include");
  }

  #api<T>(method: string, path: string, options?: RequestOptions): Promise<T> {
    return this.request<T>(method, `${this.apiPath}${path}`, options);
  }

  // ─────────────────────────────────────────────────────────── me & auth (§1, §2)

  /** `GET /api/v1/me`. Rejects with code `unauthenticated` when there is no session. */
  me(options?: CallOptions): Promise<Me> {
    return this.#api<Me>("GET", "/me", options);
  }

  /** Like `me()`, but resolves `null` instead of rejecting when not logged in. */
  async meOrNull(options?: CallOptions): Promise<Me | null> {
    try {
      return await this.me(options);
    } catch (error) {
      if (error instanceof CodiApiError && error.code === "unauthenticated") return null;
      throw error;
    }
  }

  readonly auth = {
    /**
     * URL that starts the Discord OAuth2 login and comes back to `redirect` (a path on the
     * portal; default: the current page).
     */
    loginUrl: (redirect?: string): string => {
      if (this.authMode === "token") {
        // The runtime redirects back with ?codi_code=… to this absolute URL, which must be
        // on an allowed origin (spec.portal.allowedOrigins or --cors-origin).
        const here = globalLocation()?.href;
        const target =
          redirect !== undefined && here ? new URL(redirect, here).toString() : (redirect ?? here);
        return this.url(`${this.authPath}/login`, { mode: "token", redirect: target });
      }
      const target = redirect ?? currentPath();
      return this.url(`${this.authPath}/login`, target ? { redirect: target } : undefined);
    },

    /** Navigates the browser to the login URL. */
    login: (redirect?: string, location: LocationLike | undefined = globalLocation()): void => {
      const url = this.auth.loginUrl(redirect);
      if (location?.assign) location.assign(url);
      else if (location) location.href = url;
      else throw new Error("@codi/client: auth.login() needs a browser location");
    },

    /** `POST /auth/link` with an explicit token (stores the session token in token mode). */
    link: async (token: string, options?: CallOptions): Promise<Me> => {
      if (this.authMode !== "token")
        return this.request<Me>("POST", `${this.authPath}/link`, { body: { token }, ...options });
      return this.#adopt(
        await this.request<TokenSession>("POST", `${this.authPath}/link`, {
          body: { token, mode: "token" },
          ...options,
        }),
      );
    },

    /** Token mode: `POST /auth/token` — redeems the one-time code of an OAuth login. */
    redeem: async (code: string, options?: CallOptions): Promise<Me> =>
      this.#adopt(
        await this.request<TokenSession>("POST", `${this.authPath}/token`, {
          body: { code },
          ...options,
        }),
      ),

    /**
     * Reads `codi_token` from the current URL (a signed link from `ctx.portal.link()`),
     * removes it from the address bar right away (so it is neither bookmarked nor sent twice),
     * and exchanges it for a session. Resolves `null` when the URL has no token.
     * Calls made while an exchange is in flight share it (safe under React StrictMode).
     */
    exchangeLinkToken: (options: ExchangeLinkTokenOptions = {}): Promise<Me | null> => {
      const location = options.location ?? globalLocation();
      const param = options.param ?? "codi_token";
      const codeParam = options.codeParam ?? "codi_code";
      if (!location) return Promise.resolve(null);
      const url = new URL(location.href);
      const token = url.searchParams.get(param);
      // A token-mode OAuth login comes back with a one-time code.
      const code = this.authMode === "token" ? url.searchParams.get(codeParam) : null;
      if (!token && !code) return this.#pendingLink ?? Promise.resolve(null);

      url.searchParams.delete(param);
      url.searchParams.delete(codeParam);
      const history = options.history ?? globalHistory();
      const clean = `${url.pathname}${url.search}${url.hash}`;
      if (history) history.replaceState(history.state ?? null, "", clean);
      else location.href = url.toString();

      const exchange = token ? this.auth.link(token) : this.auth.redeem(code as string);
      const pending = exchange.finally(() => {
        if (this.#pendingLink === pending) this.#pendingLink = undefined;
      });
      this.#pendingLink = pending;
      return pending;
    },

    /** `POST /auth/dev-login` — `codi dev` only (stores the session token in token mode). */
    devLogin: async (userId: string, options?: CallOptions): Promise<Me> => {
      if (this.authMode !== "token")
        return this.request<Me>("POST", `${this.authPath}/dev-login`, {
          body: { userId },
          ...options,
        });
      return this.#adopt(
        await this.request<TokenSession>("POST", `${this.authPath}/dev-login`, {
          body: { userId, mode: "token" },
          ...options,
        }),
      );
    },

    /** `POST /auth/logout` (token mode: revokes the token and forgets it). */
    logout: async (options?: CallOptions): Promise<void> => {
      try {
        await this.request<void>("POST", `${this.authPath}/logout`, options);
      } finally {
        if (this.authMode === "token") this.#setSession(null);
      }
    },
  };

  // ─────────────────────────────────────────────────────────── portal (§6)

  readonly portal = {
    /** `GET /portal/servers` — servers with a portal. */
    servers: async (options?: CallOptions): Promise<PortalServer[]> =>
      (await this.#api<Items<PortalServer>>("GET", "/portal/servers", options)).items.map((s) =>
        this.#absolutize(s),
      ),
    /** `GET /portal/{server}`. */
    server: async (server: string, options?: CallOptions): Promise<PortalServer> =>
      this.#absolutize(await this.#api<PortalServer>("GET", `/portal/${seg(server)}`, options)),
    /** `GET /portal/{server}/me` — the viewer as a member of the server. */
    me: (server: string, options?: CallOptions): Promise<PortalMe> =>
      this.#api<PortalMe>("GET", `/portal/${seg(server)}/me`, options),
  };

  /** Runtime-relative image URLs (logo, banner) resolved against `baseUrl` (embedded portals). */
  #absolutize(server: PortalServer): PortalServer {
    if (!this.baseUrl) return server;
    const abs = (u: string | undefined) => (u && u.startsWith("/") ? this.url(u) : u);
    return {
      ...server,
      theme: { ...server.theme, logo: abs(server.theme.logo) ?? "" },
      banner: abs(server.banner) ?? "",
    };
  }

  /** Routes of a plugin instance: `/api/v1/portal/{server}/plugins/{bot}/{instance}/{path}`. */
  plugin(server: string, bot: string, instance: string): PluginRoutes {
    const prefix = `${this.apiPath}/portal/${seg(server)}/plugins/${seg(bot)}/${seg(instance)}`;
    const full = (path: string): string => `${prefix}${path.startsWith("/") ? path : `/${path}`}`;
    const request = <T = Json>(
      method: string,
      path: string,
      options?: RequestOptions,
    ): Promise<T> => this.request<T>(method, full(path), options);
    const withBody =
      (method: string) =>
      <T = Json>(path: string, body?: unknown, options?: CallOptions): Promise<T> =>
        request<T>(method, path, { ...options, ...(body === undefined ? {} : { body }) });
    return {
      server,
      bot,
      instance,
      url: (path, query) => this.url(full(path), query),
      request,
      get: <T = Json>(path: string, query?: Query, options?: CallOptions) =>
        request<T>("GET", path, { ...options, ...(query ? { query } : {}) }),
      post: withBody("POST"),
      put: withBody("PUT"),
      patch: withBody("PATCH"),
      delete: <T = Json>(path: string, options?: CallOptions) =>
        request<T>("DELETE", path, options),
    };
  }

  // ─────────────────────────────────────────────────────────── admin (§3–§5)

  readonly admin = {
    /** `GET /status` (admin, moderator) — visible servers only. */
    status: (options?: CallOptions): Promise<RuntimeStatus> =>
      this.#api<RuntimeStatus>("GET", "/status", options),
    /** `GET /history?limit=&target=` (admin, moderator), newest first. */
    history: async (query: HistoryQuery = {}, options?: CallOptions): Promise<SyncRecord[]> =>
      (
        await this.#api<Items<SyncRecord>>("GET", "/history", {
          query: { limit: query.limit, target: query.target },
          ...options,
        })
      ).items,
    /** `GET /servers/{server}` (moderator). */
    server: (server: string, options?: CallOptions): Promise<ServerDetail> =>
      this.#api<ServerDetail>("GET", `/servers/${seg(server)}`, options),
    /** `GET /servers/{server}/resources` (moderator) — roles, categories, channels for pickers. */
    resources: (server: string, options?: CallOptions): Promise<ServerResources> =>
      this.#api<ServerResources>("GET", `/servers/${seg(server)}/resources`, options),
    /** `GET /servers/{server}/plan` (admin). */
    plan: (server: string, options?: CallOptions): Promise<Plan> =>
      this.#api<Plan>("GET", `/servers/${seg(server)}/plan`, options),
    /** `POST /servers/{server}/sync` (admin) — reconcile now. */
    sync: (server: string, options?: CallOptions): Promise<SyncResult> =>
      this.#api<SyncResult>("POST", `/servers/${seg(server)}/sync`, options),
    /** `GET /servers/{server}/audit?before=&limit=` (moderator), newest first. */
    audit: async (
      server: string,
      query: AuditQuery = {},
      options?: CallOptions,
    ): Promise<AuditEntry[]> =>
      (
        await this.#api<Items<AuditEntry>>("GET", `/servers/${seg(server)}/audit`, {
          query: { before: query.before, limit: query.limit },
          ...options,
        })
      ).items,
    /** `GET /servers/{server}/plugins` (moderator). */
    plugins: async (server: string, options?: CallOptions): Promise<PluginInstance[]> =>
      (await this.#api<Items<PluginInstance>>("GET", `/servers/${seg(server)}/plugins`, options))
        .items,
    /**
     * `PUT /servers/{server}/plugins/{bot}/{instance}/config` (moderator, field-restricted).
     * Rejects with `forbidden` (403), `invalid` + `diagnostics` (422) or `conflict` (409).
     */
    updatePluginConfig: (
      server: string,
      bot: string,
      instance: string,
      request: UpdatePluginConfigRequest,
      options?: CallOptions,
    ): Promise<WriteBackResult> =>
      this.#api<WriteBackResult>(
        "PUT",
        `/servers/${seg(server)}/plugins/${seg(bot)}/${seg(instance)}/config`,
        {
          body: request,
          ...options,
        },
      ),
  };

  // ─────────────────────────────────────────────────────────── machine (§7)

  readonly machine = {
    /** `POST /api/v1/sync` — fetch the source now and sync (202). */
    sync: async (options?: CallOptions): Promise<void> => {
      await this.#api<void>("POST", "/sync", options);
    },
    /** `POST /api/v1/plan` with a `.tar.gz` of a repository. */
    plan: (
      archive: Blob | ArrayBuffer | Uint8Array,
      options?: CallOptions,
    ): Promise<RemotePlanResult> =>
      this.#api<RemotePlanResult>("POST", "/plan", {
        body: archive,
        contentType: "application/gzip",
        ...options,
      }),
    /** `GET /healthz` → true when the process is up. */
    healthz: (options?: CallOptions): Promise<boolean> => this.#probe("/healthz", options),
    /** `GET /readyz` → true once a revision is active. */
    readyz: (options?: CallOptions): Promise<boolean> => this.#probe("/readyz", options),
  };

  async #probe(path: string, options?: CallOptions): Promise<boolean> {
    try {
      await this.request("GET", path, {
        ...options,
        headers: { Accept: "text/plain, application/json" },
      });
      return true;
    } catch (error) {
      if (error instanceof CodiApiError) return false;
      throw error;
    }
  }

  // ─────────────────────────────────────────────────────────── simulator (§8)

  readonly sim = {
    /** `GET /sim/state`. */
    state: (options?: CallOptions): Promise<SimState> =>
      this.#api<SimState>("GET", "/sim/state", options),
    /** `POST /sim/users` — creates a simulated user. */
    createUser: (username: string, options?: CallOptions): Promise<User> =>
      this.#api<User>("POST", "/sim/users", { body: { username }, ...options }),
    /** `POST /sim/servers/{id}/members` — the user joins the server. */
    join: async (serverId: string, userId: string, options?: CallOptions): Promise<void> => {
      await this.#api<void>("POST", `/sim/servers/${seg(serverId)}/members`, {
        body: { userId },
        ...options,
      });
    },
    /** `DELETE /sim/servers/{id}/members/{userId}` — the user leaves. */
    leave: async (serverId: string, userId: string, options?: CallOptions): Promise<void> => {
      await this.#api<void>(
        "DELETE",
        `/sim/servers/${seg(serverId)}/members/${seg(userId)}`,
        options,
      );
    },
    /** `GET /sim/channels/{id}/messages?limit=` — oldest first. */
    messages: async (
      channelId: string,
      query: { limit?: number } = {},
      options?: CallOptions,
    ): Promise<Message[]> =>
      (
        await this.#api<Items<Message>>("GET", `/sim/channels/${seg(channelId)}/messages`, {
          query,
          ...options,
        })
      ).items,
    /** `GET /sim/users/{id}/dms?limit=` — DMs received by the user from bots, oldest first. */
    dms: async (
      userId: string,
      { limit, ...options }: CallOptions & { limit?: number } = {},
    ): Promise<Message[]> =>
      (
        await this.#api<Items<Message>>("GET", `/sim/users/${seg(userId)}/dms`, {
          query: { limit },
          ...options,
        })
      ).items,
    /** `POST /sim/messages`. */
    sendMessage: (request: SimSendMessageRequest, options?: CallOptions): Promise<Message> =>
      this.#api<Message>("POST", "/sim/messages", { body: request, ...options }),
    /** `POST /sim/commands` — runs a slash command (waits up to 5 s for the answer). */
    command: (request: SimCommandRequest, options?: CallOptions): Promise<InteractionResult> =>
      this.#api<InteractionResult>("POST", "/sim/commands", {
        body: { options: {}, ...request },
        ...options,
      }),
    /** `POST /sim/components` — clicks a button / picks select values. */
    component: (request: SimComponentRequest, options?: CallOptions): Promise<InteractionResult> =>
      this.#api<InteractionResult>("POST", "/sim/components", {
        body: { values: [], ...request },
        ...options,
      }),
    /**
     * `POST /sim/modals` — submits a modal shown by a previous interaction
     * (`InteractionResult.modal`); `fields` maps text input ids to values.
     */
    modal: (request: SimModalRequest, options?: CallOptions): Promise<InteractionResult> =>
      this.#api<InteractionResult>("POST", "/sim/modals", { body: request, ...options }),
    /** `GET /sim/commands?serverId=` — registered commands (all simulated servers by default). */
    commands: async (serverId?: string, options?: CallOptions): Promise<SimCommand[]> =>
      (
        await this.#api<Items<SimCommand>>("GET", "/sim/commands", {
          query: { serverId },
          ...options,
        })
      ).items,
    /**
     * Subscribes to `GET /sim/events` (SSE). `onChange` fires for every `event: change`
     * (a hint to refresh); reconnects automatically. Call `close()` to stop.
     */
    subscribe: (
      onChange: (event: ServerSentEvent) => void,
      options: Omit<EventStreamOptions, "onEvent"> = {},
    ): EventStream =>
      this.events("/sim/events", {
        ...options,
        onEvent: (e) => e.event === "change" && onChange(e),
      }),
  };

  /** Opens a Server-Sent Events stream on an API path (fetch-based, sends credentials/token). */
  events(path: string, options: EventStreamOptions): EventStream {
    return openEventStream(
      this.#fetch,
      this.apiUrl(path),
      {
        method: "GET",
        headers: this.#headers(),
        credentials: this.#credentials(),
      },
      options,
    );
  }
}

async function readBody(response: Response): Promise<Json | string | undefined> {
  if (response.status === 204 || response.status === 205 || response.status === 304)
    return undefined;
  const text = await response.text();
  if (text === "") return undefined;
  const type = response.headers.get("Content-Type") ?? "";
  if (type.includes("json") || /^\s*[[{]/.test(text)) {
    try {
      return JSON.parse(text) as Json;
    } catch {
      return text;
    }
  }
  return text;
}

function resolveStore(store: CodiClientOptions["tokenStore"], key: string): TokenStore {
  if (store && typeof store === "object") return store;
  if (store === "memory") return memoryTokenStore();
  const storage = (name: "localStorage" | "sessionStorage") => {
    try {
      return (globalThis as Record<string, unknown>)[name] as Storage | undefined;
    } catch {
      return undefined;
    }
  };
  return webStorageTokenStore(
    storage(store === "session" ? "sessionStorage" : "localStorage"),
    key,
  );
}

function globalLocation(): LocationLike | undefined {
  return typeof location === "undefined" ? undefined : location;
}

function globalHistory(): HistoryLike | undefined {
  return typeof history === "undefined" ? undefined : history;
}

function currentPath(): string | undefined {
  const loc = globalLocation();
  if (!loc) return undefined;
  const url = new URL(loc.href);
  return `${url.pathname}${url.search}${url.hash}`;
}

/** Creates a client. See `CodiClientOptions`. */
export function createCodiClient(options?: CodiClientOptions): CodiClient {
  return new CodiClient(options);
}
