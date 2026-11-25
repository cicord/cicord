import { CicordApiError, createCicordClient, type FetchLike } from "@cicord/client";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";
import { inUseItems, validateRegistry, type RegistryRef } from "../src/pages/Store.tsx";

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
  window.history.replaceState(null, "", import.meta.env.BASE_URL);
});
afterEach(cleanup);

interface Sent {
  method: string;
  path: string;
  query: string;
  body: unknown;
}
type Intercept = (url: URL, init: RequestInit) => Response | undefined;

function setup(path = "/admin/servers/pixel-haven/store", intercept?: Intercept) {
  const runtime = createMockRuntime({ latency: 0, persist: false });
  const sent: Sent[] = [];
  const fetch: FetchLike = async (input, init) => {
    const url = new URL(input, window.location.origin);
    sent.push({
      method: (init.method ?? "GET").toUpperCase(),
      path: url.pathname,
      query: url.searchParams.toString(),
      body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
    });
    const res = intercept?.(url, init);
    return res ?? runtime.fetch(input, init);
  };
  const config: AdminConfig = {
    client: createCicordClient({ fetch: watch401(fetch) }),
    loginUrl: "/auth/login?redirect=/admin/",
    mock: true,
  };
  window.history.replaceState(null, "", path.replace(/^\/admin\//, import.meta.env.BASE_URL));
  render(<App config={config} />);
  return { runtime, sent, user: userEvent.setup() };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

async function login(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: /Owner/ }, { timeout: 3000 }));
}

/** The detail page of a Store plugin (the main landmark), once loaded. */
async function pluginPage(name: string) {
  await screen.findByRole("heading", { level: 1, name });
  return screen.getByRole("main");
}

const installs = (sent: Sent[]) =>
  sent.filter((s) => s.method === "POST" && s.path.endsWith("/store/install"));

const tableCalls = (sent: Sent[]) =>
  sent.filter((s) => s.method === "GET" && s.path === "/api/v1/store/plugins");

const STORE = "/admin/servers/pixel-haven/store";

describe("store: browse", () => {
  test("server-side table: count, filters, search, sort, pages; fetch errors are explained", async () => {
    const { user, sent } = setup();
    await login(user);
    expect(await screen.findByRole("heading", { level: 1, name: "Store" })).toBeTruthy();
    const table = await screen.findByRole("table", { name: "Store plugins" });
    // 6 official + 2 acme + 120 hub plugins (community cannot be fetched).
    expect(await screen.findByText("1–50 of 128")).toBeTruthy();
    expect(tableCalls(sent).at(-1)!.query).toBe("sort=name&order=asc&offset=0&limit=50");

    const alert = screen.getByText("Could not fetch the source community").closest("[role=alert]")!;
    expect(
      within(alert as HTMLElement).getByText("Could not fetch the registry index"),
    ).toBeTruthy();
    expect(
      within(alert as HTMLElement).getByText(/Check the URL and that the repository is public/),
    ).toBeTruthy();
    // Raw git output stays in the collapsed details.
    expect(document.body.textContent).not.toMatch(/fatal: repository not found/);

    // Filter on a source: the server filters, the URL keeps it.
    await user.selectOptions(screen.getByLabelText("Source"), "acme");
    expect(await within(table).findByRole("link", { name: /XP & levels/ })).toBeTruthy();
    await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(3));
    expect(within(table).getAllByText("Unofficial")).toHaveLength(2);
    expect(window.location.search).toBe("?source=acme");
    expect(tableCalls(sent).at(-1)!.query).toContain("source=acme");
    await user.selectOptions(screen.getByLabelText("Source"), "");

    // Search: words on names, descriptions and keywords, across sources (debounced).
    await user.type(screen.getByRole("searchbox", { name: "Search plugins" }), "leaderboard");
    await waitFor(() => expect(screen.getByText("1–2 of 2")).toBeTruthy());
    expect(within(table).getByRole("link", { name: /^Levels/ })).toBeTruthy();
    expect(within(table).getByRole("link", { name: /XP & levels/ })).toBeTruthy();
    expect(tableCalls(sent).filter((c) => c.query.includes("q="))).toHaveLength(1);
    await user.clear(screen.getByRole("searchbox", { name: "Search plugins" }));
    await user.type(screen.getByRole("searchbox", { name: "Search plugins" }), "zzz-nothing");
    expect(await screen.findByText("No results")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Clear search and filters" }));
    expect(await screen.findByText("1–50 of 128")).toBeTruthy();

    // Sort by installed: most used first.
    const installed = within(table).getByRole("columnheader", { name: /Installed/ });
    await user.click(within(installed).getByRole("button"));
    await waitFor(() => expect(installed.getAttribute("aria-sort")).toBe("descending"));
    await waitFor(() =>
      expect(within(within(table).getAllByRole("row")[1]!).getByText("Welcome")).toBeTruthy(),
    );
    expect(within(table).getAllByRole("row")[1]!.textContent).toMatch(/Here1$/);

    // Next page.
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("51–100 of 128")).toBeTruthy();
    expect(new URLSearchParams(window.location.search).get("page")).toBe("2");
    expect(tableCalls(sent).at(-1)!.query).toBe("sort=installed&order=desc&offset=50&limit=50");

    // A row opens the plugin's page.
    const first = within(table).getAllByRole("link")[0]!;
    const name = first.querySelector(".font-medium")!.textContent;
    await user.click(first);
    expect(await screen.findByRole("heading", { level: 1, name })).toBeTruthy();
    expect(window.location.pathname).toMatch(/\/servers\/pixel-haven\/store\/[^/]+\/[^/]+$/);
  });

  test("detail page: permissions review, privileged intents, network, installed instances", async () => {
    const { user } = setup(`${STORE}/cicord/moderation`);
    await login(user);
    const page = await pluginPage("Moderation");
    expect(await within(page).findByText("What this plugin requests")).toBeTruthy();
    // Human capability labels with the raw names.
    expect(within(page).getByText(/Time out, kick and ban members/)).toBeTruthy();
    expect(within(page).getByText("discord.members.moderate")).toBeTruthy();
    // ServerMembers is privileged: highlighted, with the developer portal hint.
    expect(within(page).getByTitle("Server Members Intent").textContent).toMatch(/Privileged/);
    expect(
      within(page).getByText(/must be enabled for the bot in the Discord developer portal/),
    ).toBeTruthy();
    expect(within(page).getByText("hooks.slack.com")).toBeTruthy();
    expect(within(page).getByText("ban")).toBeTruthy();
    expect(within(page).getByText("Not installed yet.")).toBeTruthy();
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: "Store" })).toBeTruthy();

    await user.click(screen.getByRole("link", { name: "Back to the Store" }));
    expect(await screen.findByRole("table", { name: "Store plugins" })).toBeTruthy();
  });

  test("detail page of an installed and of an unofficial plugin; unknown plugin", async () => {
    const { user } = setup(`${STORE}/cicord/welcome`);
    await login(user);
    const welcome = await pluginPage("Welcome");
    expect(await within(welcome).findByText("moderator/welcome on pixel-haven")).toBeTruthy();
    expect(within(welcome).getByRole("button", { name: "Install another instance" })).toBeTruthy();
    cleanup();

    const second = setup(`${STORE}/acme/xp`);
    await login(second.user);
    const xp = await pluginPage("XP & levels");
    expect(await within(xp).findByText("Unofficial source: acme")).toBeTruthy();
    expect(within(xp).getByText("Known after install")).toBeTruthy();
    expect(within(xp).getByText("api.acme.dev")).toBeTruthy();
    cleanup();

    const third = setup(`${STORE}/acme/nope`);
    await login(third.user);
    expect(await screen.findByText("This plugin is not in the Store")).toBeTruthy();
  });

  test("moderators get an explanation instead of the store", async () => {
    const { user } = setup();
    await user.click(await screen.findByRole("button", { name: /Mina/ }, { timeout: 3000 }));
    expect(await screen.findByText("Admins only")).toBeTruthy();
    expect(screen.queryByRole("table", { name: "Store plugins" })).toBeNull();
  });
});

describe("store: install", () => {
  test("an official plugin installs without version pinning, then links to its page", async () => {
    const { user, sent } = setup(`${STORE}/cicord/polls`);
    await login(user);
    const page = await pluginPage("Polls");
    await user.click(await within(page).findByRole("button", { name: "Install on Pixel Haven" }));
    expect(within(page).queryByRole("checkbox")).toBeNull();
    expect(within(page).getByLabelText("Instance name")).toHaveProperty("value", "polls");
    await user.click(within(page).getByRole("button", { name: "Install" }));
    expect(await within(page).findByText("Polls is installed")).toBeTruthy();
    expect(within(page).getByText("/poll")).toBeTruthy();
    const body = installs(sent)[0]!.body as Record<string, unknown>;
    expect(body).toMatchObject({ source: "cicord", plugin: "polls", instance: "polls" });
    expect(body).not.toHaveProperty("version");
    expect(body).not.toHaveProperty("confirmUnofficial");
    expect(body).not.toHaveProperty("bot");

    // The new instance has its own page.
    await user.click(within(page).getByRole("link", { name: "Open its settings" }));
    expect(await screen.findByRole("region", { name: "Polls settings" })).toBeTruthy();
    expect(window.location.pathname).toMatch(/\/plugins\/moderator\/polls$/);
  });

  test("unofficial plugins need an explicit confirmation before install is enabled", async () => {
    const { user, sent } = setup(`${STORE}/acme/giveaways`);
    await login(user);
    const page = await pluginPage("Giveaways");
    await user.click(await within(page).findByRole("button", { name: "Install on Pixel Haven" }));
    expect(within(page).getAllByText("Unofficial").length).toBeGreaterThan(0);
    const install = within(page).getByRole("button", { name: "Install" });
    expect(install).toHaveProperty("disabled", true);
    const confirm = within(page).getByRole("checkbox", { name: /I trust acme/ });
    // The confirmation restates what is granted.
    const grants = document.getElementById(confirm.getAttribute("aria-describedby")!)!;
    expect(grants.textContent).toMatch(/Add and remove reactions/);
    expect(grants.textContent).toMatch(/Run tasks on a schedule/);
    expect(grants.textContent).toMatch(/No network access/);
    await user.click(confirm);
    expect(install).toHaveProperty("disabled", false);
    await user.click(install);
    expect(await within(page).findByText("Giveaways is installed")).toBeTruthy();
    expect(installs(sent)[0]!.body).toMatchObject({
      source: "acme",
      plugin: "giveaways",
      version: "0.4.1",
      commit: "8e1d2c3b4a5f60718293a4b5c6d7e8f9a0b1c2d3",
      integrity: "sha256-Zk3pQ8wR1vT6yU2iO9aS5dF4gH7jK0lZxC3vB6nM8qE=",
      confirmUnofficial: true,
    });
    // It is a registry plugin instance now: listed as unofficial on the Plugins table.
    await user.click(within(page).getByRole("link", { name: "Open its settings" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Giveaways" })).toBeTruthy();
    expect(screen.getByText("Unofficial")).toBeTruthy();
  });

  test("409 changed offers to reload the detail and review again", async () => {
    const { user, sent, runtime } = setup(`${STORE}/acme/giveaways`);
    await login(user);
    const page = await pluginPage("Giveaways");
    await user.click(await within(page).findByRole("button", { name: "Install on Pixel Haven" }));
    // A new version is published between the review and the install.
    const giveaways = runtime.state.registries[0]!.plugins.find((p) => p.name === "giveaways")!;
    Object.assign(giveaways, {
      version: "0.5.0",
      commit: "feedfacefeedfacefeedfacefeedfacefeedface",
      integrity: "sha256-new",
      network: ["stats.acme.dev"],
    });
    await user.click(within(page).getByRole("checkbox", { name: /I trust acme/ }));
    await user.click(within(page).getByRole("button", { name: "Install" }));
    expect(await within(page).findByText("This plugin changed since you reviewed it")).toBeTruthy();
    expect(installs(sent)[0]!.body).toMatchObject({ version: "0.4.1" });

    await user.click(within(page).getByRole("button", { name: "Reload and review" }));
    // Back on the review of the new version, with a note.
    expect(await within(page).findByText("What this plugin requests")).toBeTruthy();
    expect(within(page).getByText("This plugin changed since you reviewed it")).toBeTruthy();
    expect(await within(page).findByText("acme/giveaways@0.5.0")).toBeTruthy();
    expect(within(page).getByText("stats.acme.dev")).toBeTruthy();

    // The confirmation must be given again for the new code, then the reviewed values are sent.
    await user.click(within(page).getByRole("button", { name: "Install on Pixel Haven" }));
    const confirm = within(page).getByRole("checkbox", { name: /I trust acme/ });
    expect(confirm).toHaveProperty("checked", false);
    expect(within(page).getByRole("button", { name: "Install" })).toHaveProperty("disabled", true);
    await user.click(confirm);
    await user.click(within(page).getByRole("button", { name: "Install" }));
    expect(await within(page).findByText("Giveaways is installed")).toBeTruthy();
    expect(installs(sent)[1]!.body).toMatchObject({
      version: "0.5.0",
      commit: "feedfacefeedfacefeedfacefeedfacefeedface",
      integrity: "sha256-new",
      confirmUnofficial: true,
    });
  });

  test("privileged intents and diagnostics are explained", async () => {
    const { user } = setup(`${STORE}/acme/xp`);
    await login(user);
    // xp needs MessageContent, which is off on the mock application.
    const xp = await pluginPage("XP & levels");
    await user.click(await within(xp).findByRole("button", { name: "Install on Pixel Haven" }));
    expect(within(xp).getByText(/Needs Message Content Intent enabled/)).toBeTruthy();
    await user.click(within(xp).getByRole("checkbox", { name: /I trust acme/ }));
    // The required channel is missing: client-side validation blocks the install.
    await user.click(within(xp).getByRole("button", { name: "Install" }));
    expect((await screen.findAllByText("Fix the highlighted fields first")).length).toBeGreaterThan(
      0,
    );
  });

  test("a taken instance name is explained (409 conflict)", async () => {
    const { user } = setup(`${STORE}/cicord/polls`);
    await login(user);
    const polls = await pluginPage("Polls");
    await user.click(await within(polls).findByRole("button", { name: "Install on Pixel Haven" }));
    const name = within(polls).getByLabelText("Instance name");
    await user.clear(name);
    await user.type(name, "welcome");
    await user.click(within(polls).getByRole("button", { name: "Install" }));
    expect(await within(polls).findByText("That instance name is taken")).toBeTruthy();
    expect(within(polls).getByText(/already has an instance named "welcome"/)).toBeTruthy();
    expect(within(polls).getByText(/Pick another instance name/)).toBeTruthy();
  });

  test("intents error links to the developer portal", async () => {
    const { user } = setup(`${STORE}/cicord/polls`, (url, init) =>
      init.method === "POST" && url.pathname.endsWith("/store/install")
        ? json(
            {
              error: {
                code: "intents",
                message: "Levels needs the MessageContent privileged intent, which is off",
                hint: "Turn it on in the developer portal, then install again.",
                link: "https://discord.com/developers/applications/900/bot",
              },
            },
            409,
          )
        : undefined,
    );
    await login(user);
    const page = await pluginPage("Polls");
    await user.click(await within(page).findByRole("button", { name: "Install on Pixel Haven" }));
    await user.click(within(page).getByRole("button", { name: "Install" }));
    expect(await within(page).findByText("A privileged intent is off")).toBeTruthy();
    expect(
      within(page).getByText("Turn it on in the developer portal, then install again."),
    ).toBeTruthy();
    const links = within(page).getAllByRole("link", { name: /Open the developer portal/ });
    expect(links.map((l) => l.getAttribute("href"))).toContain(
      "https://discord.com/developers/applications/900/bot",
    );
  });
});

describe("store: sources", () => {
  test("add a registry with inline validation, remove one, in_use is explained", async () => {
    const { user, runtime } = setup(`${STORE}?tab=sources`);
    await login(user);
    const list = await screen.findByRole("list", { name: "Sources" });
    // The bundled source cannot be removed, the registries can.
    expect(within(list).queryByRole("button", { name: "Remove source cicord" })).toBeNull();
    expect(within(list).getByRole("button", { name: "Remove source acme" })).toBeTruthy();
    expect(within(list).getByText("Fetch failed")).toBeTruthy();
    expect(within(list).getByText(/120 plugins/)).toBeTruthy();
    expect(screen.getByText(/installing their plugins is at your own risk/)).toBeTruthy();

    const name = screen.getByLabelText("Name");
    const url = screen.getByLabelText("Git URL (https)");
    await user.type(name, "cicord");
    await user.type(url, "http://example.com/x.git");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    expect(screen.getByText("cicord is reserved for the built-in source")).toBeTruthy();
    expect(screen.getByText("Only https:// URLs are accepted")).toBeTruthy();
    await user.clear(name);
    await user.type(name, "Bad_Name");
    expect(screen.getByText(/use only a-z, 0-9 and -/)).toBeTruthy();
    await user.clear(name);
    await user.type(name, "acme");
    expect(screen.getByText("A source with this name already exists")).toBeTruthy();

    await user.clear(name);
    await user.type(name, "friends");
    await user.clear(url);
    await user.type(url, "https://github.com/friends/cicord-plugins.git");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    const note = await screen.findByText("Registry friends added", { selector: "[role=status] *" });
    expect(
      within(note.closest("[role=status]") as HTMLElement).getByText("Committed"),
    ).toBeTruthy();
    expect(await within(list).findByRole("button", { name: "Remove source friends" })).toBeTruthy();
    expect(runtime.state.registries.map((r) => r.name)).toEqual([
      "acme",
      "community",
      "hub",
      "friends",
    ]);

    // Remove a registry used by an instance: in_use lists the instances.
    runtime.state.storeInstalls.push({
      source: "acme",
      plugin: "giveaways",
      server: "pixel-haven",
      bot: "moderator",
      instance: "giveaways",
    });
    await user.click(within(list).getByRole("button", { name: "Remove source acme" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remove the source acme?" });
    await user.click(within(dialog).getByRole("button", { name: "Remove source" }));
    expect(await screen.findByText("acme is still in use")).toBeTruthy();
    expect(screen.getByText("bots/moderator/bot.yaml: giveaways (acme/giveaways)")).toBeTruthy();

    // An unused one goes away.
    await user.click(within(list).getByRole("button", { name: "Remove source community" }));
    const dialog2 = await screen.findByRole("alertdialog", {
      name: "Remove the source community?",
    });
    await user.click(within(dialog2).getByRole("button", { name: "Remove source" }));
    expect(
      await screen.findByText("Source community removed", { selector: "[role=status] *" }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(within(list).queryByRole("button", { name: "Remove source community" })).toBeNull(),
    );
  });

  test("private registries: missing token, set, replace and remove a token", async () => {
    const { user, runtime, sent } = setup(`${STORE}?tab=sources`);
    await login(user);
    const list = await screen.findByRole("list", { name: "Sources" });
    const row = (name: string) =>
      within(list).getByText(name, { selector: "span.font-mono" }).closest("li") as HTMLElement;
    expect(within(row("hub")).getByText("Private")).toBeTruthy();
    expect(within(row("community")).getByText("Private")).toBeTruthy();
    expect(within(row("acme")).queryByText("Private")).toBeNull();
    // community's secretRef points at a secret this runtime does not have.
    expect(within(row("community")).getByText("The token is missing on this runtime")).toBeTruthy();
    expect(within(row("hub")).queryByText("The token is missing on this runtime")).toBeNull();

    // Set it: required, checked by the runtime, never shown.
    await user.click(
      within(row("community")).getByRole("button", {
        name: "Replace the access token of community",
      }),
    );
    let dialog = await screen.findByRole("dialog", {
      name: "Replace the access token of community",
    });
    expect(within(dialog).getByText(/only sent to git.example.org/)).toBeTruthy();
    const input = within(dialog).getByLabelText("Access token");
    expect(input.getAttribute("type")).toBe("password");
    await user.click(within(dialog).getByRole("button", { name: "Save token" }));
    expect(within(dialog).getByText("Enter the token")).toBeTruthy();
    await user.type(input, "bad");
    await user.click(within(dialog).getByRole("button", { name: "Save token" }));
    expect(await within(dialog).findByText(/authentication failed/)).toBeTruthy();
    await user.clear(input);
    await user.type(input, "s3cret-value");
    await user.click(within(dialog).getByRole("button", { name: "Save token" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const saved = await screen.findByText("Token of community saved", {
      selector: "[role=status] *",
    });
    // Replacing a token changes no file.
    expect(saved.closest("[role=status]")!.textContent).toMatch(/cicord.yaml did not change/);
    await waitFor(() =>
      expect(
        within(row("community")).queryByText("The token is missing on this runtime"),
      ).toBeNull(),
    );
    expect(runtime.state.secretNames).toContain("CICORD_REGISTRY_COMMUNITY_TOKEN");
    expect(sent.filter((s) => s.method === "PUT").map((s) => s.body)).toEqual([
      { token: "bad" },
      { token: "s3cret-value" },
    ]);
    expect(document.body.textContent).not.toMatch(/s3cret-value/);

    // Set a token on a public registry: it becomes private (committed).
    await user.click(
      within(row("acme")).getByRole("button", { name: "Set the access token of acme" }),
    );
    dialog = await screen.findByRole("dialog", { name: "Set the access token of acme" });
    await user.type(within(dialog).getByLabelText("Access token"), "acme-token");
    await user.click(within(dialog).getByRole("button", { name: "Save token" }));
    const acmeNote = await screen.findByText("Token of acme saved", {
      selector: "[role=status] *",
    });
    expect(
      within(acmeNote.closest("[role=status]") as HTMLElement).getByText("Committed"),
    ).toBeTruthy();
    expect(await within(row("acme")).findByText("Private")).toBeTruthy();

    // Remove the hub's token.
    await user.click(
      within(row("hub")).getByRole("button", { name: "Remove the access token of hub" }),
    );
    const confirm = await screen.findByRole("alertdialog", {
      name: "Remove the access token of hub?",
    });
    await user.click(within(confirm).getByRole("button", { name: "Remove token" }));
    expect(
      await screen.findByText("Token of hub removed", { selector: "[role=status] *" }),
    ).toBeTruthy();
    await waitFor(() => expect(within(row("hub")).queryByText("Private")).toBeNull());
    expect(runtime.state.secretNames).not.toContain("CICORD_REGISTRY_HUB_TOKEN");
    expect(
      sent.some((s) => s.method === "DELETE" && s.path === "/api/v1/registries/hub/token"),
    ).toBe(true);
  });

  test("a registry can be added with an access token", async () => {
    const { user, sent, runtime } = setup(`${STORE}?tab=sources`);
    await login(user);
    await user.type(await screen.findByLabelText("Name"), "secret");
    await user.type(screen.getByLabelText("Git URL (https)"), "https://git.acme.dev/p.git");
    const token = screen.getByLabelText(/Access token \(private registry\)/);
    expect(token.getAttribute("type")).toBe("password");
    expect(screen.getByText(/only sent to git.acme.dev, never shown again/)).toBeTruthy();
    await user.type(token, "tok-123");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    expect(
      await screen.findByText("Registry secret added", { selector: "[role=status] *" }),
    ).toBeTruthy();
    expect(sent.find((s) => s.method === "POST" && s.path === "/api/v1/registries")!.body).toEqual({
      name: "secret",
      url: "https://git.acme.dev/p.git",
      token: "tok-123",
    });
    expect(runtime.state.registries.at(-1)).toMatchObject({
      name: "secret",
      secretRef: "CICORD_REGISTRY_SECRET_TOKEN",
    });
    // Without a token, none is sent.
    await user.type(screen.getByLabelText("Name"), "open");
    await user.type(screen.getByLabelText("Git URL (https)"), "https://git.acme.dev/q.git");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    await screen.findByText("Registry open added", { selector: "[role=status] *" });
    expect(sent.findLast((s) => s.path === "/api/v1/registries")!.body).toEqual({
      name: "open",
      url: "https://git.acme.dev/q.git",
    });
  });

  test("403 on a project change is explained", async () => {
    const { user } = setup(`${STORE}?tab=sources`, (url, init) =>
      init.method === "POST" && url.pathname === "/api/v1/registries"
        ? json(
            { error: { code: "forbidden", message: "requires the admin role on every server" } },
            403,
          )
        : undefined,
    );
    await login(user);
    await user.type(await screen.findByLabelText("Name"), "friends");
    await user.type(screen.getByLabelText("Git URL (https)"), "https://example.com/p.git");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    expect(await screen.findByText("You cannot change the sources")).toBeTruthy();
    expect(screen.getByText(/needs the admin role on every server Cicord manages/)).toBeTruthy();
  });
});

describe("store helpers", () => {
  test("validateRegistry follows the spec rules", () => {
    const sources: RegistryRef[] = [
      { name: "cicord" },
      { name: "acme", url: "https://a.example/x.git" },
    ];
    const v = (name: string, url: string) => validateRegistry(name, url, sources);
    expect(v("friends", "https://b.example/y.git")).toEqual({});
    expect(v("", "")).toEqual({ name: "nameRequired", url: "urlRequired" });
    expect(v("-x", "x").name).toBe("namePattern");
    expect(v("a".repeat(33), "x").name).toBe("namePattern");
    expect(v("a".repeat(32), "x").name).toBeUndefined();
    expect(v("cicord", "x").name).toBe("nameReserved");
    expect(v("acme", "x").name).toBe("nameTaken");
    expect(v("x", "not a url").url).toBe("urlInvalid");
    expect(v("x", "file:///tmp/reg").url).toBe("urlHttps");
    expect(v("x", "https://a.example/x.git").url).toBe("urlTaken");
  });

  test("inUseItems accepts text or a list", () => {
    const err = (details: unknown) =>
      new CicordApiError({
        code: "in_use",
        status: 409,
        message: "in use",
        body: { error: { code: "in_use", message: "in use", details } } as never,
      });
    expect(inUseItems(err("a\nb"))).toEqual(["a", "b"]);
    expect(inUseItems(err([{ server: "s", bot: "b", instance: "i" }, "x"]))).toEqual([
      "b/i (s)",
      "x",
    ]);
    expect(inUseItems(new Error("x"))).toEqual([]);
  });
});
