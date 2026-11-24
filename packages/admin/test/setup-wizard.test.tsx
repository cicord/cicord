import { createCicordClient, type FetchLike } from "@cicord/client";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import type { AdminConfig } from "../src/config.ts";
import type { Check, SetupView } from "../src/setup/api.ts";
import { tokenFrom } from "../src/setup/api.ts";

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
});
afterEach(cleanup);

const json = (body: unknown, status = 200) =>
  new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
const apiError = (status: number, code: string, message: string, hint?: string) =>
  json({ error: { code, message, ...(hint ? { hint } : {}) } }, status);

const BOT_TOKEN = "MTIz.Abc.fake-token-for-the-ui-test";

/** A fake runtime in setup mode: just enough of /api/v1/setup/* for the UI. */
function fakeSetup() {
  const calls: string[] = [];
  let authed = false;
  let invited = false;
  let raised = false;
  let intentsOn = false;
  const view: SetupView = {
    step: "welcome",
    steps: ["welcome", "discord", "server", "repository", "login", "verify", "plugin", "done"],
    completed: false,
    mode: "serve",
    simulator: false,
    version: "0.1.0",
    providers: ["embedded", "github"],
    publicUrl: "https://cicord.example.org",
    activated: false,
    login: {
      configured: false,
      skipped: false,
      redirectUrl: "https://cicord.example.org/auth/callback",
      portalUrl: "",
      fromEnv: false,
    },
  };
  const intents = (): Check =>
    intentsOn
      ? { id: "intents", status: "ok", message: "The privileged intents Cicord uses are enabled." }
      : {
          id: "intents",
          status: "warn",
          message: "“Message Content Intent” and “Server Members Intent” are turned off.",
          hint: "In the Discord Developer Portal, open your application → Bot → Privileged Gateway Intents, turn them on.",
          link: "https://discord.com/developers/applications/999/bot",
          data: { missing: ["MessageContent", "ServerMembers"], enabled: [] },
        };
  const serverChecks = (): Check[] => [
    { id: "member", status: "ok", message: "Cicord is a member of Pixel Pals." },
    { id: "permissions", status: "ok", message: "The bot has the Administrator permission." },
    raised
      ? {
          id: "hierarchy",
          status: "ok",
          message: "The role “Cicord” is above every role Cicord manages.",
        }
      : {
          id: "hierarchy",
          status: "fail",
          message: "The role “Cicord” is below 3 role(s) Cicord must manage.",
          hint: "In Discord, open Server Settings → Roles and drag the role “Cicord” above “Admin” (to the top of the list), then re-check.",
        },
  ];
  const fetch: FetchLike = async (input, init) => {
    const url = new URL(input, window.location.origin);
    const p = url.pathname;
    const body = init?.body ? (JSON.parse(init.body as string) as Record<string, unknown>) : {};
    calls.push(`${init?.method ?? "GET"} ${p}`);
    if (p === "/api/v1/me") return apiError(503, "setup_required", "Cicord is not set up yet.");
    if (p === "/api/v1/setup/status")
      return json({
        active: true,
        completed: false,
        authenticated: authed,
        ...(authed ? { view } : {}),
      });
    if (p === "/api/v1/setup/session") {
      if (body["token"] !== "the-setup-token")
        return apiError(
          401,
          "unauthenticated",
          "This setup link is invalid or was already used.",
          "Restart Cicord to get a new one.",
        );
      authed = true;
      return json({ active: true, completed: false, authenticated: true, view });
    }
    if (!authed) return apiError(401, "unauthenticated", "Your setup session expired.");
    switch (p) {
      case "/api/v1/setup/intent":
        view.step = "discord";
        view.intent = body["intent"] as "existing";
        return json(view);
      case "/api/v1/setup/discord/token":
        if (body["token"] !== BOT_TOKEN)
          return apiError(
            422,
            "invalid_token",
            "Discord rejected this token.",
            "Click “Reset Token”, copy the new token and paste it here.",
          );
        view.bot = {
          key: "cicord",
          secretRef: "DISCORD_TOKEN_CICORD",
          applicationId: "999",
          applicationName: "Cicord",
          userId: "1",
          username: "Cicord",
          flags: 0,
          tokenStored: true,
          portalUrl: "https://discord.com/developers/applications/999/bot",
          intents: intents(),
        };
        view.inviteUrl =
          "https://discord.com/oauth2/authorize?client_id=999&permissions=8&scope=bot+applications.commands";
        return json(view);
      case "/api/v1/setup/discord/recheck":
        intentsOn = true;
        view.bot!.intents = intents();
        return json(view);
      case "/api/v1/setup/step":
        view.step = body["step"] as SetupView["step"];
        return json(view);
      case "/api/v1/setup/discord/servers":
        calls.push("poll");
        if (calls.filter((c) => c === "poll").length >= 2) invited = true;
        return json({ servers: invited ? [{ id: "42", name: "Pixel Pals" }] : [] });
      case "/api/v1/setup/server":
        view.server = { id: "42", name: "Pixel Pals", key: "pixel-pals" };
        view.step = "server";
        return json({ view, checks: serverChecks(), ok: raised });
      case "/api/v1/setup/server/check":
        raised = true;
        return json({ view, checks: serverChecks(), ok: true });
    }
    return apiError(404, "not_found", "not found");
  };
  return { fetch, calls, view };
}

function renderAt(path: string, fetch: FetchLike) {
  const config: AdminConfig = {
    client: createCicordClient({ fetch }),
    loginUrl: "/auth/login",
    mock: false,
  };
  window.history.replaceState(null, "", path.replace(/^\/admin\//, import.meta.env.BASE_URL));
  render(<App config={config} />);
  return userEvent.setup();
}

describe("setup wizard", () => {
  test("the one-time link opens the wizard and is removed from the address bar", async () => {
    const f = fakeSetup();
    renderAt("/admin/setup?token=the-setup-token", f.fetch);
    expect(
      await screen.findByRole("heading", { name: /Let's get your Discord server running/ }),
    ).toBeTruthy();
    expect(window.location.search).toBe("");
    expect(window.location.pathname).toMatch(/\/setup$/);
    expect(screen.getByRole("navigation", { name: "Setup steps" })).toBeTruthy();
  });

  test("a runtime in setup mode sends the console to the wizard; a bad link is explained", async () => {
    const f = fakeSetup();
    const user = renderAt("/admin/", f.fetch);
    expect(await screen.findByRole("heading", { name: "Open the setup link" })).toBeTruthy();
    await user.type(
      screen.getByLabelText("Setup link or token"),
      "http://x/admin/setup?token=nope",
    );
    await user.click(screen.getByRole("button", { name: "Open the wizard" }));
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("This setup link is invalid or was already used.")).toBeTruthy();
    expect(within(alert).getByText("Restart Cicord to get a new one.")).toBeTruthy();
    await user.clear(screen.getByLabelText("Setup link or token"));
    await user.type(
      screen.getByLabelText("Setup link or token"),
      "http://x/admin/setup?token=the-setup-token",
    );
    await user.click(screen.getByRole("button", { name: "Open the wizard" }));
    expect(
      await screen.findByRole("heading", { name: /Let's get your Discord server running/ }),
    ).toBeTruthy();
  });

  test("Discord step: token errors have a hint, intents tell which toggle, server is detected live", async () => {
    const f = fakeSetup();
    const user = renderAt("/admin/setup?token=the-setup-token", f.fetch);
    await user.click(await screen.findByRole("button", { name: "Start" }));
    expect(await screen.findByRole("heading", { name: "Create your bot" })).toBeTruthy();
    expect(
      screen.getByRole("link", { name: /Open the Developer Portal/ }).getAttribute("href"),
    ).toBe("https://discord.com/developers/applications");
    const input = screen.getByLabelText("Bot token");
    expect(input.getAttribute("type")).toBe("password");
    await user.type(input, "wrong.token.value-xxxxxxxxx");
    await user.click(screen.getByRole("button", { name: "Check the token" }));
    expect(await screen.findByText("Discord rejected this token.")).toBeTruthy();
    expect(screen.getByText(/Click “Reset Token”/)).toBeTruthy();

    await user.clear(input);
    await user.type(input, BOT_TOKEN);
    await user.click(screen.getByRole("button", { name: "Check the token" }));
    expect(await screen.findByText(/Token accepted/)).toBeTruthy();
    expect(screen.getByText("Stored encrypted as DISCORD_TOKEN_CICORD")).toBeTruthy();
    expect(document.body.textContent).not.toContain(BOT_TOKEN);
    expect(screen.getByText("Server Members Intent")).toBeTruthy();
    expect(screen.getByText("Message Content Intent")).toBeTruthy();
    expect(screen.getByRole("link", { name: /Open the Bot page/ }).getAttribute("href")).toBe(
      "https://discord.com/developers/applications/999/bot",
    );
    expect(screen.getByRole("button", { name: "Continue anyway" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Re-check" }));
    await user.click(await screen.findByRole("button", { name: "Continue" }));

    // Server: invite link, then the server appears without any ID to copy.
    expect(await screen.findByRole("heading", { name: "Add the bot to your server" })).toBeTruthy();
    expect(
      screen.getByRole("link", { name: /Open the invite link/ }).getAttribute("href"),
    ).toContain("permissions=8");
    expect(screen.getByText("The bot is not in any server yet.")).toBeTruthy();
    const pick = await screen.findByRole(
      "button",
      { name: "Manage this server" },
      { timeout: 6000 },
    );
    await user.click(pick);
    const hierarchy = await screen.findByText(/drag the role “Cicord” above “Admin”/);
    expect(hierarchy).toBeTruthy();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "Continue" }).disabled).toBe(true);
    await user.click(screen.getAllByRole("button", { name: "Re-check" })[0]!);
    await waitFor(() =>
      expect(screen.getByRole<HTMLButtonElement>("button", { name: "Continue" }).disabled).toBe(
        false,
      ),
    );
  });

  test("verify waits for a live event, then a first plugin is reviewed and installed", async () => {
    let messageSent = false;
    let verifies = 0;
    let installedConfig: unknown;
    const view: Partial<SetupView> = {};
    const base = fakeSetup();
    Object.assign(base.view, {
      step: "verify",
      activated: true,
      server: { id: "42", name: "Pixel Pals", key: "pixel-pals" },
      revision: "abc1234",
    });
    Object.assign(view, base.view);
    const fetch: FetchLike = async (input, init) => {
      const p = new URL(input, window.location.origin).pathname;
      const body = init?.body ? (JSON.parse(init.body as string) as Record<string, unknown>) : {};
      if (p === "/api/v1/setup/verify") {
        messageSent = ++verifies >= 2; // the second live check sees the message
        const events: Check = messageSent
          ? {
              id: "events",
              status: "ok",
              message: "A message from the server reached Cicord through the gateway.",
            }
          : {
              id: "events",
              status: "pending",
              message: "Waiting for a message: send any message in a channel of the server.",
            };
        return json({
          server: "pixel-pals",
          bot: "cicord",
          ranAt: new Date().toISOString(),
          ok: messageSent,
          checks: [{ id: "token", status: "ok", message: "Connected as Cicord." }, events],
        });
      }
      if (p === "/api/v1/setup/plugins")
        return json({
          plugins: [
            {
              name: "polls",
              displayName: "Polls",
              description: "Polls with buttons.",
              version: "0.1.0",
              keywords: [],
              source: "official",
              capabilities: ["discord.messages.send", "scheduler"],
              intents: [],
              privilegedIntents: [],
              network: [],
              commands: [{ name: "poll", description: "Create a poll" }],
              schema: {
                type: "object",
                properties: {
                  defaultHours: { type: "integer", default: 24, description: "Default duration" },
                },
              },
            },
          ],
        });
      if (p === "/api/v1/servers/pixel-pals/resources")
        return json({ roles: [], categories: [], channels: [] });
      if (p === "/api/v1/setup/plugins/install") {
        installedConfig = body["config"];
        base.view.plugin = { name: "polls", instance: "polls", command: "poll" };
        return json({
          result: {
            instance: "polls",
            health: "Healthy",
            command: "poll",
            mode: "commit",
            revision: "def5678",
          },
          view: base.view,
        });
      }
      return base.fetch(input, init);
    };
    const user = renderAt("/admin/setup?token=the-setup-token", fetch);
    expect(
      await screen.findByRole("heading", { name: "Let's check everything works" }),
    ).toBeTruthy();
    expect(await screen.findByText(/Send a message in any channel of Pixel Pals/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Continue anyway" })).toBeTruthy();
    expect(await screen.findByText("Everything works.", {}, { timeout: 8000 })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("heading", { name: "Add your first plugin" })).toBeTruthy();
    await user.click(await screen.findByRole("button", { name: "Choose Polls" }));
    expect(screen.getByText("Run tasks on a schedule")).toBeTruthy();
    expect(screen.getByText("/poll")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Install Polls" }));
    expect(await screen.findByText("Polls is active")).toBeTruthy();
    expect(screen.getByText("Try it now: type /poll in a channel of Pixel Pals.")).toBeTruthy();
    expect(installedConfig).toEqual({}); // defaults are not written to bot.yaml
  }, 20000);

  test("tokenFrom accepts a full link or a bare token", () => {
    expect(tokenFrom("http://localhost:7493/admin/setup?token=abc")).toBe("abc");
    expect(tokenFrom("  abc  ")).toBe("abc");
  });
});
