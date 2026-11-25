/**
 * The Store page of an installed plugin and the Plugins pages offer the same actions with
 * the same words: "Update to X" (the update review) when there is an update, else opening
 * the instance; installing another instance is secondary and explained. Each side links to
 * the other, and live events keep the Store page in line with the table.
 */
import { createCicordClient, type AdminEvent, type FetchLike } from "@cicord/client";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
  window.history.replaceState(null, "", import.meta.env.BASE_URL);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const SERVER = "/admin/servers/pixel-haven";

function setup(path: string, demoUpdates = false) {
  const runtime = createMockRuntime({ latency: 0, persist: false, demoUpdates });
  const details: string[] = [];
  const fetch: FetchLike = async (input, init) => {
    const url = new URL(input, window.location.origin);
    if ((init.method ?? "GET") === "GET" && /^\/api\/v1\/store\/[^/]+\/[^/]+$/.test(url.pathname))
      details.push(url.pathname);
    return runtime.fetch(input, init);
  };
  const client = createCicordClient({ fetch: watch401(fetch) });
  // The live events of the runtime (GET /events), driven by the test.
  let emit: (e: AdminEvent) => void = () => {};
  vi.spyOn(client.admin, "subscribe").mockImplementation((onEvent, options = {}) => {
    emit = onEvent;
    queueMicrotask(() => options.onOpen?.());
    return { close: () => {}, closed: Promise.resolve() };
  });
  // Not flagged as mock: the console subscribes to the live events (driven above).
  const config: AdminConfig = { client, loginUrl: "/auth/login?redirect=/admin/", mock: false };
  window.history.replaceState(null, "", path.replace(/^\/admin\//, import.meta.env.BASE_URL));
  render(<App config={config} />);
  return { runtime, details, emit: (e: AdminEvent) => emit(e), user: userEvent.setup() };
}

async function login(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: /Owner/ }, { timeout: 3000 }));
}

async function pluginPage(name: string) {
  await screen.findByRole("heading", { level: 1, name });
  return screen.getByRole("main");
}

/** The action bar of a Store page (the last element holding its install button). */
const actionBar = (page: HTMLElement) =>
  within(page)
    .getByRole("button", { name: /Install another instance|Install on/ })
    .closest(".sticky") as HTMLElement;

describe("store page of an installed plugin", () => {
  test("no update: the primary action opens the instance; installing another is secondary", async () => {
    const { user } = setup(`${SERVER}/store/cicord/welcome`);
    await login(user);
    const page = await pluginPage("Welcome");
    const bar = actionBar(await waitFor(() => page));
    const open = within(bar).getByRole("link", { name: "Open" });
    expect(open.getAttribute("href")).toMatch(
      /\/servers\/pixel-haven\/plugins\/moderator\/welcome$/,
    );
    expect(within(bar).queryByRole("link", { name: /Update to/ })).toBeNull();
    // Secondary, explained, not the default look.
    const another = within(bar).getByRole("button", { name: "Install another instance" });
    expect(another.className).toMatch(/border/);
    expect(another.className).not.toMatch(/bg-primary/);
    expect(document.getElementById(another.getAttribute("aria-describedby")!)!.textContent).toBe(
      "Adds the plugin a second time, with its own name, settings and data.",
    );
    // The installed instances link to their pages.
    const installed = within(page).getByRole("list", { name: "Installed" });
    expect(
      within(installed).getByRole("link", { name: "moderator/welcome on pixel-haven" }),
    ).toBeTruthy();

    // Installing another instance is still possible: the install form.
    await user.click(another);
    expect(within(page).getByLabelText("Instance name")).toBeTruthy();
    await user.click(within(page).getByRole("button", { name: "Back to review" }));

    await user.click(within(actionBar(page)).getByRole("link", { name: "Open" }));
    expect(await screen.findByRole("region", { name: "Welcome settings" })).toBeTruthy();
  });

  test("with an update: the primary action is “Update to X”, the update review", async () => {
    const { user } = setup(`${SERVER}/store/cicord/welcome`, true);
    await login(user);
    const page = await pluginPage("Welcome");
    expect(await within(page).findByText("Update available: 0.0.9 → 0.1.0")).toBeTruthy();
    const bar = actionBar(page);
    const updates = within(page).getAllByRole("link", { name: "Update to 0.1.0" });
    expect(updates).toHaveLength(1); // the bar, not the banner too
    expect(bar.contains(updates[0]!)).toBe(true);
    expect(updates[0]!.className).toMatch(/bg-primary/);
    expect(within(bar).queryByRole("link", { name: "Open" })).toBeNull();
    // Another instance would need the new version: the update comes first, and says so.
    const another = within(bar).getByRole("button", { name: "Install another instance" });
    expect(another).toHaveProperty("disabled", true);
    expect(document.getElementById(another.getAttribute("aria-describedby")!)!.textContent).toBe(
      "Update to 0.1.0 first: every instance of a plugin runs the same version.",
    );
    await user.click(updates[0]!);
    expect(await within(page).findByRole("heading", { name: /Update Welcome/ })).toBeTruthy();
    expect(window.location.search).toBe("?update=1");
  });

  test("several instances: each one can be opened from the bar", async () => {
    const { user, runtime } = setup(`${SERVER}/store/acme/giveaways`, true);
    runtime.state.instances.push({
      bot: "moderator",
      instance: "giveaways-vip",
      plugin: "giveaways",
      source: "acme",
      base: {},
      override: {},
    });
    runtime.state.storeInstalls.push({
      source: "acme",
      plugin: "giveaways",
      server: "pixel-haven",
      bot: "moderator",
      instance: "giveaways-vip",
    });
    runtime.state.versions = {};
    await login(user);
    const page = await pluginPage("Giveaways");
    const bar = actionBar(page);
    expect(within(bar).getByRole("link", { name: "Open giveaways" })).toBeTruthy();
    expect(
      within(bar).getByRole("link", { name: "Open giveaways-vip" }).getAttribute("href"),
    ).toMatch(/\/plugins\/moderator\/giveaways-vip$/);
    expect(within(bar).queryByRole("link", { name: "Open" })).toBeNull();
  });

  test("not installed: installing on the server stays the primary action", async () => {
    const { user } = setup(`${SERVER}/store/cicord/polls`);
    await login(user);
    const page = await pluginPage("Polls");
    const install = await within(page).findByRole("button", { name: "Install on Pixel Haven" });
    expect(install.className).toMatch(/bg-primary/);
    expect(within(page).queryByRole("button", { name: "Install another instance" })).toBeNull();
  });
});

describe("plugins pages", () => {
  test("the table and the instance page offer the update with the Store's words", async () => {
    const { user } = setup(`${SERVER}/plugins`, true);
    await login(user);
    const table = await screen.findByRole("table", { name: /plugin/i });
    const badge = await within(table).findByRole("link", { name: "Update to 0.1.0" });
    expect(badge.getAttribute("href")).toMatch(/\/store\/cicord\/welcome\?update=1$/);
    await user.click(badge);
    expect(await screen.findByRole("heading", { name: /Update Welcome/ })).toBeTruthy();

    cleanup();
    const inst = setup(`${SERVER}/plugins/moderator/welcome`, true);
    await login(inst.user);
    const banner = (await screen.findByText("Update available: 0.0.9 → 0.1.0")).closest(
      "[role=status]",
    ) as HTMLElement;
    expect(
      within(banner).getByRole("link", { name: "Update to 0.1.0" }).getAttribute("href"),
    ).toMatch(/\/store\/cicord\/welcome\?update=1$/);
  });

  test("an instance links to its Store page, and back", async () => {
    const { user } = setup(`${SERVER}/plugins/moderator/welcome`);
    await login(user);
    await user.click(await screen.findByRole("link", { name: "View in Store" }));
    const page = await pluginPage("Welcome");
    const installed = within(page).getByRole("list", { name: "Installed" });
    await user.click(
      within(installed).getByRole("link", { name: "moderator/welcome on pixel-haven" }),
    );
    expect(await screen.findByRole("region", { name: "Welcome settings" })).toBeTruthy();
  });

  test("a plugin written in the repository has no Store page", async () => {
    const { user } = setup(`${SERVER}/plugins/moderator/onboarding`);
    await login(user);
    expect(await screen.findByRole("heading", { level: 1, name: "Onboarding" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "View in Store" })).toBeNull();
  });
});

describe("store page freshness", () => {
  test("a store event and a new revision refetch the plugin page (no stale update)", async () => {
    const { user, runtime, details, emit } = setup(`${SERVER}/store/cicord/welcome`);
    await login(user);
    const page = await pluginPage("Welcome");
    expect(await within(page).findByRole("link", { name: "Open" })).toBeTruthy();
    const loads = details.length;

    // A registry refresh on the runtime finds a newer version than the installed one.
    runtime.state.versions = {
      "cicord/welcome": { version: "0.0.9", capabilities: [], intents: [], network: [] },
    };
    act(() => emit({ type: "store" }));
    expect(await within(page).findByRole("link", { name: "Update to 0.1.0" })).toBeTruthy();
    expect(details.length).toBeGreaterThan(loads);

    // The update is merged elsewhere (a pull request): the new revision is a sync event.
    runtime.state.versions = {};
    act(() => emit({ type: "sync" }));
    await waitFor(() => expect(within(page).queryByRole("link", { name: /Update to/ })).toBeNull());
    expect(within(page).getByRole("link", { name: "Open" })).toBeTruthy();
  });

  test("refreshing the sources refetches a plugin page cached before", async () => {
    const { user, runtime, details } = setup(`${SERVER}/store/cicord/welcome`);
    await login(user);
    await pluginPage("Welcome");
    await user.click(screen.getByRole("link", { name: "Back to the Store" }));
    await screen.findByRole("table", { name: "Store plugins" });
    await user.type(screen.getByRole("searchbox", { name: "Search plugins" }), "welcome");
    await waitFor(() => expect(screen.getAllByRole("row").length).toBeLessThan(20));
    expect(
      await within(screen.getByRole("table", { name: "Store plugins" })).findByRole("link", {
        name: /cicord\/welcome/,
      }),
    ).toBeTruthy();
    runtime.state.versions = {
      "cicord/welcome": { version: "0.0.9", capabilities: [], intents: [], network: [] },
    };
    await user.click(screen.getByRole("button", { name: "Refresh sources" }));
    const table = screen.getByRole("table", { name: "Store plugins" });
    expect(await within(table).findByRole("link", { name: "Update to 0.1.0" })).toBeTruthy();
    const before = details.length;
    await user.click(within(table).getByRole("link", { name: /cicord\/welcome/ }));
    const page = await pluginPage("Welcome");
    // The page shows what the table shows.
    expect(await within(page).findByRole("link", { name: "Update to 0.1.0" })).toBeTruthy();
    expect(details.length).toBeGreaterThan(before);
  });
});
