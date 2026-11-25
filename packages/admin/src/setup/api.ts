/** Setup wizard API (https://cicord.dev/docs/reference/http-api/setup/): typed calls on top of the generic client. */
import type { CicordClient, Plan } from "@cicord/client";

export type CheckStatus = "ok" | "warn" | "fail" | "pending" | "skip";

export interface Check {
  id: string;
  status: CheckStatus;
  message: string;
  hint?: string;
  link?: string;
  data?: Record<string, unknown>;
}

export type StepId =
  | "welcome"
  | "discord"
  | "server"
  | "repository"
  | "login"
  | "verify"
  | "plugin"
  | "done";

export type Provider = "local" | "embedded" | "github";

/** The invite choice of the Server step. */
export type Access = "admin" | "inspect";

/** How the GitHub repository is accessed. */
export type RepoAuth = "token" | "githubApp";

/** The Repository step form. */
export interface RepoRequest {
  provider: Provider;
  url?: string;
  branch?: string;
  auth?: RepoAuth;
  /** Personal access token (auth "token"). */
  token?: string;
  /** GitHub App (auth "githubApp"): its ID, private key (PEM) and optional installation. */
  appId?: string;
  privateKey?: string;
  installationId?: string;
}

interface SetupBot {
  key: string;
  secretRef: string;
  applicationId: string;
  applicationName: string;
  userId: string;
  username: string;
  avatarUrl?: string;
  flags: number;
  tokenStored: boolean;
  portalUrl: string;
  intents: Check;
}

interface SetupServer {
  id: string;
  name: string;
  iconUrl?: string;
  key: string;
}

export interface SetupView {
  step: StepId;
  steps: StepId[];
  completed: boolean;
  mode: "serve" | "dev";
  simulator: boolean;
  version: string;
  intent?: "new" | "existing";
  providers: Provider[];
  publicUrl: string;
  bot?: SetupBot;
  /** How the bot is invited: Administrator (default) or View Channels only, to inspect first. */
  access: Access;
  inviteUrl?: string;
  inspectInviteUrl?: string;
  server?: SetupServer;
  repo?: {
    provider: Provider;
    url?: string;
    branch: string;
    tokenStored?: boolean;
    /** GitHub authentication: a personal access token or a GitHub App. */
    auth?: RepoAuth;
    appId?: string;
  };
  activated: boolean;
  revision?: string;
  firstPlan?: Plan;
  login: {
    configured: boolean;
    skipped: boolean;
    testedAs?: string;
    clientId?: string;
    redirectUrl: string;
    portalUrl: string;
    fromEnv: boolean;
  };
  plugin?: { name: string; instance: string; command?: string; skipped?: boolean };
}

export interface SetupStatus {
  active: boolean;
  completed: boolean;
  authenticated: boolean;
  simulator?: boolean;
  view?: SetupView;
}

export interface ServerSummary {
  id: string;
  name: string;
  iconUrl?: string;
}

export interface ServerResult {
  view: SetupView;
  checks: Check[];
  ok: boolean;
}

export interface ImportResult {
  view: SetupView;
  plan: Plan | null;
  needsConfirm: boolean;
  files?: string[];
  warnings: string[];
}

/** A category or channel as the bot sees it. */
export interface ChannelAccess {
  id: string;
  key?: string;
  name: string;
  type: string;
  categoryId?: string;
  category?: string;
  /** @everyone cannot view it. */
  private: boolean;
  /** The bot can view it. */
  visible: boolean;
}

/** The read-only inspection of the chosen server (nothing is changed on Discord). */
interface InspectReport {
  serverId: string;
  serverName: string;
  key: string;
  botName: string;
  administrator: boolean;
  permissions: string[];
  rolePermissions: string[];
  categories: ChannelAccess[];
  channels: ChannelAccess[];
  /** Channels the server refers to that Discord did not list (hidden from the bot). */
  unlisted: { id: string; referencedBy: string[] }[];
  unreadable: { part: string; name: string; needs?: string; error: string }[];
  /** What the bot lacks to apply changes, with the YAML paths that need each permission. */
  needs: { permission: string; sources: string[]; why: string }[];
  hierarchy: Check;
  ready: boolean;
  upgradeInviteUrl?: string;
  adminInviteUrl?: string;
  warnings: string[];
  reads: number;
  writes: number;
}

export interface InspectResult {
  report: InspectReport;
  plan: Plan;
}

export interface Report {
  server: string;
  bot: string;
  checks: Check[];
  ranAt: string;
  ok: boolean;
  plan?: Plan;
}

export interface CatalogPlugin {
  name: string;
  displayName: string;
  description: string;
  version: string;
  keywords: string[];
  source: string;
  capabilities: string[];
  intents: string[];
  privilegedIntents: string[];
  network: string[];
  schema: Record<string, unknown>;
  commands: { name: string; description: string }[];
}

export interface InstallResult {
  instance: string;
  health: string;
  message?: string;
  command?: string;
  revision?: string;
  mode: string;
  pullRequestUrl?: string;
}

export interface LoginTest {
  ok: boolean;
  user?: { id: string; username: string; displayName: string; avatarUrl: string };
  servers?: { key: string; name: string; role: string }[];
}

const P = "/api/v1/setup";

/** Wizard calls. */
export function setupApi(client: CicordClient) {
  const get = <T>(path: string, signal?: AbortSignal) =>
    client.request<T>("GET", `${P}${path}`, signal ? { signal } : {});
  const post = <T>(path: string, body: unknown = {}) =>
    client.request<T>("POST", `${P}${path}`, { body });
  return {
    status: (signal?: AbortSignal) => get<SetupStatus>("/status", signal),
    exchange: (token: string) => post<SetupStatus>("/session", { token }),
    intent: (intent: "new" | "existing") => post<SetupView>("/intent", { intent }),
    step: (step: StepId) => post<SetupView>("/step", { step }),
    submitToken: (token: string) => post<SetupView>("/discord/token", { token }),
    recheckDiscord: () => post<SetupView>("/discord/recheck"),
    servers: (signal?: AbortSignal) =>
      get<{ servers: ServerSummary[] }>("/discord/servers", signal).then((r) => r.servers),
    selectServer: (serverId: string) => post<ServerResult>("/server", { serverId }),
    checkServer: () => post<ServerResult>("/server/check"),
    access: (access: Access) => post<SetupView>("/access", { access }),
    inspect: () => post<InspectResult>("/inspect"),
    repo: (body: RepoRequest) => post<SetupView>("/repo", body),
    import: (confirm = false) => post<ImportResult>("/import", { confirm }),
    saveOAuth: (clientSecret: string) => post<SetupView>("/oauth", { clientSecret }),
    skipLogin: () => post<SetupView>("/oauth/skip"),
    testLogin: (signal?: AbortSignal) => get<LoginTest>("/oauth/test", signal),
    verify: (signal?: AbortSignal) => get<Report>("/verify", signal),
    catalog: (signal?: AbortSignal) =>
      get<{ plugins: CatalogPlugin[] }>("/plugins", signal).then((r) => r.plugins),
    install: (plugin: string, config: Record<string, unknown>) =>
      post<{ result: InstallResult; view: SetupView }>("/plugins/install", { plugin, config }),
    finish: () => post<SetupView>("/finish"),
    sim: {
      info: () => get<{ servers: { id: string; name: string }[] }>("/sim"),
      createBot: (name?: string) => post<{ token: string }>("/sim/bot", name ? { name } : {}),
      intents: (enabled: boolean) => post<void>("/sim/intents", { enabled }),
      invite: (low: boolean) => post<void>("/sim/invite", { low }),
      raiseRole: () => post<void>("/sim/raise-role"),
      grantAdmin: () => post<void>("/sim/grant-admin"),
      message: () => post<void>("/sim/message"),
    },
  };
}

export type SetupApi = ReturnType<typeof setupApi>;

/** Diagnostics of a server (same checks as the Verify step). */
export function diagnostics(client: CicordClient, serverKey: string, signal?: AbortSignal) {
  return client.request<Report>(
    "GET",
    `/api/v1/servers/${encodeURIComponent(serverKey)}/diagnostics`,
    signal ? { signal } : {},
  );
}

/** Reads the token from "…/admin/setup?token=…" or a pasted link. */
export function tokenFrom(input: string): string {
  const s = input.trim();
  try {
    const u = new URL(s);
    return u.searchParams.get("token") ?? s;
  } catch {
    return s.replace(/^.*[?&]token=/, "");
  }
}
