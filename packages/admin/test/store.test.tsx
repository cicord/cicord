import {
  CodiApiError,
  createCodiClient,
  type FetchLike,
  type StorePlugin,
  type StoreSource,
} from "@codi/client";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";
import { inUseItems, matchesQuery, validateRegistry } from "../src/pages/Store.tsx";

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
  window.history.replaceState(null, "", import.meta.env.BASE_URL);
});
afterEach(cleanup);

interface Sent {
  method: string;
  path: string;
  body: unknown;
}
type Intercept = (url: URL, init: RequestInit) => Response | undefined;

function setup(path = "/admin/servers/ma-commu/store", intercept?: Intercept) {
  const runtime = createMockRuntime({ latency: 0, persist: false });
  const sent: Sent[] = [];
  const fetch: FetchLike = async (input, init) => {
    const url = new URL(input, window.location.origin);
    sent.push({
      method: (init.method ?? "GET").toUpperCase(),
      path: url.pathname,
      body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
    });
    const res = intercept?.(url, init);
    return res ?? runtime.fetch(input, init);
  };
  const config: AdminConfig = {
    client: createCodiClient({ fetch: watch401(fetch) }),
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

async function openPlugin(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("button", { name: `View ${name}` }));
  return screen.findByRole("dialog", { name });
}

const installs = (sent: Sent[]) =>
  sent.filter((s) => s.method === "POST" && s.path.endsWith("/store/install"));

describe("store: browse", () => {
  test("sources are grouped with trust, fetch errors are explained, search filters", async () => {
    const { user } = setup();
    await login(user);
    expect(await screen.findByRole("heading", { level: 1, name: "Store" })).toBeTruthy();

    const codi = await screen.findByRole("region", { name: "Source codi" });
    expect(within(codi).getAllByText("Official").length).toBeGreaterThan(1);
    expect(within(codi).getByRole("button", { name: "View Tickets" })).toBeTruthy();
    expect(within(codi).getByText("6 plugins")).toBeTruthy();

    const acme = screen.getByRole("region", { name: "Source acme" });
    expect(within(acme).getByText("https://github.com/acme/codi-plugins.git")).toBeTruthy();
    // Unofficial on the source header and on each card.
    expect(within(acme).getAllByText("Unofficial")).toHaveLength(3);
    expect(within(acme).getByRole("button", { name: "View XP & levels" })).toBeTruthy();

    const broken = screen.getByRole("region", { name: "Source community" });
    const alert = within(broken).getByRole("alert");
    expect(within(alert).getByText("Could not fetch this source")).toBeTruthy();
    expect(within(alert).getByText("Could not fetch the registry index")).toBeTruthy();
    expect(within(alert).getByText(/Check the URL and that the repository is public/)).toBeTruthy();
    // Raw git output stays in the collapsed details.
    expect(document.body.textContent).not.toMatch(/fatal: repository not found/);

    // Search on descriptions and keywords, across sources.
    const search = screen.getByRole("searchbox", { name: "Search plugins" });
    await user.type(search, "leaderboard");
    expect(screen.getByRole("button", { name: "View Levels" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "View XP & levels" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "View Tickets" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Source community" })).toBeNull();
    expect(screen.getByText("2 plugins", { selector: "[role=status]" })).toBeTruthy();

    await user.clear(search);
    await user.type(search, "zzz-nothing");
    expect(screen.getByText("No plugin matches “zzz-nothing”")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Clear search" }));
    expect(screen.getByRole("button", { name: "View Tickets" })).toBeTruthy();
  });

  test("detail: permissions review, privileged intents, network, installed instances", async () => {
    const { user } = setup();
    await login(user);
    const sheet = await openPlugin(user, "Moderation");
    expect(await within(sheet).findByText("What this plugin requests")).toBeTruthy();
    // Human capability labels with the raw names.
    expect(within(sheet).getByText(/Time out, kick and ban members/)).toBeTruthy();
    expect(within(sheet).getByText("discord.members.moderate")).toBeTruthy();
    // ServerMembers is privileged: highlighted, with the developer portal hint.
    expect(within(sheet).getByTitle("Server Members Intent").textContent).toMatch(/Privileged/);
    expect(
      within(sheet).getByText(/must be enabled for the bot in the Discord developer portal/),
    ).toBeTruthy();
    expect(within(sheet).getByText("hooks.slack.com")).toBeTruthy();
    expect(within(sheet).getByText("ban")).toBeTruthy();
    expect(within(sheet).getByText("Not installed yet.")).toBeTruthy();
    await user.click(within(sheet).getByRole("button", { name: "Close" }));

    const welcome = await openPlugin(user, "Welcome");
    expect(await within(welcome).findByText("moderator/welcome on ma-commu")).toBeTruthy();

    await user.click(within(welcome).getByRole("button", { name: "Close" }));
    const xp = await openPlugin(user, "XP & levels");
    expect(await within(xp).findByText("Unofficial source: acme")).toBeTruthy();
    expect(within(xp).getByText("Known after install")).toBeTruthy();
    expect(within(xp).getByText("api.acme.dev")).toBeTruthy();
  });

  test("moderators get an explanation instead of the store", async () => {
    const { user, runtime } = setup();
    await screen.findByRole("button", { name: /Mina/ }, { timeout: 3000 });
    void runtime;
    await user.click(screen.getByRole("button", { name: /Mina/ }));
    expect(await screen.findByText("Admins only")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Source codi" })).toBeNull();
  });
});

describe("store: install", () => {
  test("an official plugin installs without version pinning and shows the command", async () => {
    const { user, sent } = setup();
    await login(user);
    const sheet = await openPlugin(user, "Polls");
    await user.click(await within(sheet).findByRole("button", { name: "Install on Ma Commu" }));
    expect(within(sheet).queryByRole("checkbox")).toBeNull();
    expect(within(sheet).getByLabelText("Instance name")).toHaveProperty("value", "polls");
    await user.click(within(sheet).getByRole("button", { name: "Install" }));
    expect(await within(sheet).findByText("Polls is installed")).toBeTruthy();
    expect(within(sheet).getByText("/poll")).toBeTruthy();
    const body = installs(sent)[0]!.body as Record<string, unknown>;
    expect(body).toMatchObject({ source: "codi", plugin: "polls", instance: "polls" });
    expect(body).not.toHaveProperty("version");
    expect(body).not.toHaveProperty("confirmUnofficial");
    expect(body).not.toHaveProperty("bot");
  });

  test("unofficial plugins need an explicit confirmation before install is enabled", async () => {
    const { user, sent } = setup();
    await login(user);
    const sheet = await openPlugin(user, "Giveaways");
    await user.click(await within(sheet).findByRole("button", { name: "Install on Ma Commu" }));
    expect(within(sheet).getAllByText("Unofficial").length).toBeGreaterThan(0);
    const install = within(sheet).getByRole("button", { name: "Install" });
    expect(install).toHaveProperty("disabled", true);
    const confirm = within(sheet).getByRole("checkbox", { name: /I trust acme/ });
    // The confirmation restates what is granted.
    const grants = document.getElementById(confirm.getAttribute("aria-describedby")!)!;
    expect(grants.textContent).toMatch(/Add and remove reactions/);
    expect(grants.textContent).toMatch(/Run tasks on a schedule/);
    expect(grants.textContent).toMatch(/No network access/);
    await user.click(confirm);
    expect(install).toHaveProperty("disabled", false);
    await user.click(install);
    expect(await within(sheet).findByText("Giveaways is installed")).toBeTruthy();
    expect(installs(sent)[0]!.body).toMatchObject({
      source: "acme",
      plugin: "giveaways",
      version: "0.4.1",
      commit: "8e1d2c3b4a5f60718293a4b5c6d7e8f9a0b1c2d3",
      integrity: "sha256-Zk3pQ8wR1vT6yU2iO9aS5dF4gH7jK0lZxC3vB6nM8qE=",
      confirmUnofficial: true,
    });
  });

  test("409 changed offers to reload the detail and review again", async () => {
    const { user, sent, runtime } = setup();
    await login(user);
    const sheet = await openPlugin(user, "Giveaways");
    await user.click(await within(sheet).findByRole("button", { name: "Install on Ma Commu" }));
    // A new version is published between the review and the install.
    const giveaways = runtime.state.registries[0]!.plugins.find((p) => p.name === "giveaways")!;
    Object.assign(giveaways, {
      version: "0.5.0",
      commit: "feedfacefeedfacefeedfacefeedfacefeedface",
      integrity: "sha256-new",
      network: ["stats.acme.dev"],
    });
    await user.click(within(sheet).getByRole("checkbox", { name: /I trust acme/ }));
    await user.click(within(sheet).getByRole("button", { name: "Install" }));
    expect(
      await within(sheet).findByText("This plugin changed since you reviewed it"),
    ).toBeTruthy();
    expect(installs(sent)[0]!.body).toMatchObject({ version: "0.4.1" });

    await user.click(within(sheet).getByRole("button", { name: "Reload and review" }));
    // Back on the review of the new version, with a note.
    expect(await within(sheet).findByText("What this plugin requests")).toBeTruthy();
    expect(within(sheet).getByText("This plugin changed since you reviewed it")).toBeTruthy();
    expect(await within(sheet).findByText("acme/giveaways@0.5.0")).toBeTruthy();
    expect(within(sheet).getByText("stats.acme.dev")).toBeTruthy();

    // The confirmation must be given again for the new code, then the reviewed values are sent.
    await user.click(within(sheet).getByRole("button", { name: "Install on Ma Commu" }));
    const confirm = within(sheet).getByRole("checkbox", { name: /I trust acme/ });
    expect(confirm).toHaveProperty("checked", false);
    expect(within(sheet).getByRole("button", { name: "Install" })).toHaveProperty("disabled", true);
    await user.click(confirm);
    await user.click(within(sheet).getByRole("button", { name: "Install" }));
    expect(await within(sheet).findByText("Giveaways is installed")).toBeTruthy();
    expect(installs(sent)[1]!.body).toMatchObject({
      version: "0.5.0",
      commit: "feedfacefeedfacefeedfacefeedfacefeedface",
      integrity: "sha256-new",
      confirmUnofficial: true,
    });
  });

  test("privileged intents, diagnostics and taken instance names are explained", async () => {
    const { user } = setup();
    await login(user);
    // xp needs MessageContent, which is off on the mock application.
    const xp = await openPlugin(user, "XP & levels");
    await user.click(await within(xp).findByRole("button", { name: "Install on Ma Commu" }));
    expect(within(xp).getByText(/Needs Message Content Intent enabled/)).toBeTruthy();
    await user.click(within(xp).getByRole("checkbox", { name: /I trust acme/ }));
    // The required channel is missing: client-side validation blocks the install.
    await user.click(within(xp).getByRole("button", { name: "Install" }));
    expect((await screen.findAllByText("Fix the highlighted fields first")).length).toBeGreaterThan(
      0,
    );
    await user.click(within(xp).getByRole("button", { name: "Close" }));

    // Taken instance name on an official plugin: 409 conflict.
    const polls = await openPlugin(user, "Polls");
    await user.click(await within(polls).findByRole("button", { name: "Install on Ma Commu" }));
    const name = within(polls).getByLabelText("Instance name");
    await user.clear(name);
    await user.type(name, "welcome");
    await user.click(within(polls).getByRole("button", { name: "Install" }));
    expect(await within(polls).findByText("That instance name is taken")).toBeTruthy();
    expect(within(polls).getByText(/already has an instance named "welcome"/)).toBeTruthy();
    expect(within(polls).getByText(/Pick another instance name/)).toBeTruthy();

    // An instance of this plugin already on this server: offered as "another instance".
    await user.click(within(polls).getByRole("button", { name: "Close" }));
    const welcome = await openPlugin(user, "Welcome");
    expect(
      await within(welcome).findByRole("button", { name: "Install another instance" }),
    ).toBeTruthy();
  });

  test("intents error links to the developer portal", async () => {
    const { user } = setup(undefined, (url, init) =>
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
    const sheet = await openPlugin(user, "Polls");
    await user.click(await within(sheet).findByRole("button", { name: "Install on Ma Commu" }));
    await user.click(within(sheet).getByRole("button", { name: "Install" }));
    expect(await within(sheet).findByText("A privileged intent is off")).toBeTruthy();
    expect(
      within(sheet).getByText("Turn it on in the developer portal, then install again."),
    ).toBeTruthy();
    const links = within(sheet).getAllByRole("link", { name: /Open the developer portal/ });
    expect(links.map((l) => l.getAttribute("href"))).toContain(
      "https://discord.com/developers/applications/900/bot",
    );
  });
});

describe("store: sources", () => {
  test("add a registry with inline validation, remove one, in_use is explained", async () => {
    const { user, runtime } = setup();
    await login(user);
    await user.click(await screen.findByRole("tab", { name: /Sources/ }));
    const list = await screen.findByRole("list", { name: "Sources" });
    // The bundled source cannot be removed, the registries can.
    expect(within(list).queryByRole("button", { name: "Remove source codi" })).toBeNull();
    expect(within(list).getByRole("button", { name: "Remove source acme" })).toBeTruthy();
    expect(within(list).getByText("Fetch failed")).toBeTruthy();
    expect(screen.getByText(/installing their plugins is at your own risk/)).toBeTruthy();

    const name = screen.getByLabelText("Name");
    const url = screen.getByLabelText("Git URL (https)");
    await user.type(name, "codi");
    await user.type(url, "http://example.com/x.git");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    expect(screen.getByText("codi is reserved for the built-in source")).toBeTruthy();
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
    await user.type(url, "https://github.com/friends/codi-plugins.git");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    const note = await screen.findByText("Registry friends added", { selector: "[role=status] *" });
    expect(
      within(note.closest("[role=status]") as HTMLElement).getByText("Committed"),
    ).toBeTruthy();
    expect(await within(list).findByRole("button", { name: "Remove source friends" })).toBeTruthy();
    expect(runtime.state.registries.map((r) => r.name)).toEqual(["acme", "community", "friends"]);

    // Remove a registry used by an instance: in_use lists the instances.
    runtime.state.storeInstalls.push({
      source: "acme",
      plugin: "giveaways",
      server: "ma-commu",
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

  test("403 on a project change is explained", async () => {
    const { user } = setup(undefined, (url, init) =>
      init.method === "POST" && url.pathname === "/api/v1/registries"
        ? json(
            { error: { code: "forbidden", message: "requires the admin role on every server" } },
            403,
          )
        : undefined,
    );
    await login(user);
    await user.click(await screen.findByRole("tab", { name: /Sources/ }));
    await user.type(await screen.findByLabelText("Name"), "friends");
    await user.type(screen.getByLabelText("Git URL (https)"), "https://example.com/p.git");
    await user.click(screen.getByRole("button", { name: "Add registry" }));
    expect(await screen.findByText("You cannot change the sources")).toBeTruthy();
    expect(screen.getByText(/needs the admin role on every server CODI manages/)).toBeTruthy();
  });
});

describe("store helpers", () => {
  const plugin: StorePlugin = {
    source: "acme",
    name: "xp",
    displayName: "XP & levels",
    description: "Levels and a portal leaderboard",
    keywords: ["experience", "ranks"],
    verified: false,
    trust: "unofficial",
    installed: [],
  };

  test("matchesQuery: every word, any field, case-insensitive", () => {
    expect(matchesQuery(plugin, "")).toBe(true);
    expect(matchesQuery(plugin, "LEADER")).toBe(true);
    expect(matchesQuery(plugin, "ranks portal")).toBe(true);
    expect(matchesQuery(plugin, "ranks tickets")).toBe(false);
    expect(matchesQuery(plugin, "xp")).toBe(true);
  });

  test("validateRegistry follows the spec rules", () => {
    const sources: StoreSource[] = [
      { name: "codi", kind: "bundled", trust: "official", plugins: [] },
      {
        name: "acme",
        kind: "git",
        url: "https://a.example/x.git",
        trust: "unofficial",
        plugins: [],
      },
    ];
    const v = (name: string, url: string) => validateRegistry(name, url, sources);
    expect(v("friends", "https://b.example/y.git")).toEqual({});
    expect(v("", "")).toEqual({ name: "nameRequired", url: "urlRequired" });
    expect(v("-x", "x").name).toBe("namePattern");
    expect(v("a".repeat(33), "x").name).toBe("namePattern");
    expect(v("a".repeat(32), "x").name).toBeUndefined();
    expect(v("codi", "x").name).toBe("nameReserved");
    expect(v("acme", "x").name).toBe("nameTaken");
    expect(v("x", "not a url").url).toBe("urlInvalid");
    expect(v("x", "file:///tmp/reg").url).toBe("urlHttps");
    expect(v("x", "https://a.example/x.git").url).toBe("urlTaken");
  });

  test("inUseItems accepts text or a list", () => {
    const err = (details: unknown) =>
      new CodiApiError({
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
