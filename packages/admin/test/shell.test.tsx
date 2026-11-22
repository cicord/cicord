import { createCodiClient, type FetchLike } from "@codi/client";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.documentElement.className = "";
});

type Intercept = (url: URL, init: RequestInit) => Response | undefined;

async function start(path = "/", intercept?: Intercept) {
  const runtime = createMockRuntime({ latency: 0, persist: false });
  const fetch: FetchLike = async (input, init) =>
    intercept?.(new URL(input, window.location.origin), init) ?? runtime.fetch(input, init);
  const config: AdminConfig = {
    client: createCodiClient({ fetch: watch401(fetch) }),
    loginUrl: "/auth/login",
    mock: true,
  };
  window.history.replaceState(null, "", `${import.meta.env.BASE_URL.replace(/\/$/, "")}${path}`);
  render(<App config={config} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Owner/ }, { timeout: 3000 }));
  return { user, runtime };
}

describe("layout", () => {
  test("sidebar sections, server switcher, breadcrumb", async () => {
    const { user } = await start();
    const nav = await screen.findByRole("complementary", { name: "Main navigation" });
    for (const name of [
      "Overview",
      "Roles & channels",
      "Server settings",
      "Plugins",
      "Plan",
      "History",
      "Drift",
      "Audit log",
      "Simulator",
      "Settings",
    ])
      expect(
        within(nav).getByRole("link", { name: new RegExp(`^${name.replace("&", "\\&")}`) }),
      ).toBeTruthy();
    const order = within(nav)
      .getAllByRole("link")
      .map((a) => a.textContent?.replace(/dev$/, "").replace(/\d+$/, ""))
      .filter((x) => x && x !== "codiadmin");
    expect(order).toEqual([
      "Overview",
      "Roles & channels",
      "Server settings",
      "Plugins",
      "Store",
      "Plan",
      "History",
      "Drift",
      "Audit log",
      "Diagnostics",
      "Simulator",
      "Settings",
    ]);
    expect(within(nav).getByText("Configuration")).toBeTruthy();
    expect(within(nav).getByText("Sync")).toBeTruthy();
    // Drift count badge
    expect(within(nav).getByLabelText("2 drifted fields")).toBeTruthy();

    await user.click(within(nav).getByRole("link", { name: "Plan" }));
    const crumbs = await screen.findByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByText("Ma Commu")).toBeTruthy();
    expect(within(crumbs).getByText("Sync")).toBeTruthy();
    expect(within(crumbs).getByText("Plan").getAttribute("aria-current")).toBe("page");
    expect(within(nav).getByRole("link", { name: "Plan" }).getAttribute("aria-current")).toBe(
      "page",
    );

    await user.click(within(nav).getByRole("button", { name: /Server: Ma Commu/ }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByText("ma-commu")).toBeTruthy();
  });

  test("⌘K palette jumps to pages and plugins", async () => {
    const { user } = await start();
    await screen.findByRole("heading", { level: 1, name: "Ma Commu" });
    await user.keyboard("{Meta>}k{/Meta}");
    const dialog = await screen.findByRole("dialog", { name: "Command palette" });
    await user.type(within(dialog).getByRole("combobox"), "audit");
    await user.click(within(dialog).getByRole("option", { name: /Audit log/ }));
    expect(await screen.findByRole("heading", { level: 1, name: "Audit log" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Search pages, servers and plugins" }));
    const again = await screen.findByRole("dialog", { name: "Command palette" });
    await user.click(await within(again).findByRole("option", { name: /Welcome/ }));
    await waitFor(() =>
      expect(window.location.pathname).toMatch(/\/servers\/ma-commu\/plugins\/moderator\/welcome$/),
    );
    expect(await screen.findByRole("region", { name: "Welcome settings" })).toBeTruthy();
  });

  test("theme toggle is applied and persisted; settings page", async () => {
    const { user } = await start("/settings");
    expect(await screen.findByRole("heading", { level: 1, name: "Settings" })).toBeTruthy();
    // jsdom has no dark system preference: light by default.
    await waitFor(() => expect(document.documentElement.classList.contains("light")).toBe(true));
    await user.click(screen.getByRole("radio", { name: "Dark" }));
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(localStorage.getItem("codi-admin:theme")).toBe("dark");
    await user.click(screen.getByRole("button", { name: "Switch to light theme" }));
    expect(document.documentElement.classList.contains("light")).toBe(true);
    expect(screen.getByText("0.1.0-dev")).toBeTruthy();
  });

  test("history page groups failures; status legend explains states", async () => {
    const { user } = await start("/servers/ma-commu/history");
    expect(await screen.findByText("Failed 4 times")).toBeTruthy();
    await user.click(screen.getByRole("radio", { name: "Bots" }));
    await waitFor(() => expect(screen.queryByText("Failed 4 times")).toBeNull());
    expect(screen.getByText("bot reloaded (2 plugins)")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Status legend" }));
    const legend = await screen.findByRole("dialog");
    expect(within(legend).getByText("What the statuses mean")).toBeTruthy();
    expect(within(legend).getByText(/Discord differs from the active revision/)).toBeTruthy();
  });

  test("server settings and drift pages read the server spec", async () => {
    const { user } = await start("/servers/ma-commu/settings");
    expect(await screen.findByRole("heading", { level: 1, name: "Server settings" })).toBeTruthy();
    expect(screen.getByText("Deletion protection")).toBeTruthy();
    await user.click(screen.getByRole("link", { name: /^Drift/ }));
    expect(await screen.findByRole("heading", { level: 1, name: "Drift" })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Actual (Discord)" })).toBeTruthy();
  });

  test("an explained sync failure becomes a friendly toast, never raw JSON", async () => {
    const raw =
      'sync: reconcile ma-commu: 1 change(s) failed: create channel news: discord CreateChannel: discord: bad request (HTTP 400 50035: Invalid Form Body {"type":{"_errors":[]}})';
    const { user } = await start("/servers/ma-commu/plan", (url, init) =>
      init.method === "POST" && url.pathname.endsWith("/sync")
        ? new Response(
            JSON.stringify({
              error: {
                code: "unavailable",
                message: "Announcement and stage channels need the Community feature",
                hint: "Enable Community in Discord (Server Settings → Enable Community), then sync again.",
                details: raw,
                reason: "community_required",
              },
            }),
            { status: 503, headers: { "Content-Type": "application/json" } },
          )
        : undefined,
    );
    await screen.findByRole("region", { name: "Roles" });
    await user.click(screen.getByRole("button", { name: "Sync now" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Sync now" }));
    const toastEl = (await screen.findByText("Sync failed")).closest("li") as HTMLElement;
    expect(
      within(toastEl).getByText("Announcement and stage channels need the Community feature"),
    ).toBeTruthy();
    expect(within(toastEl).getByText(/^Enable Community in Discord/)).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/_errors/);
  });
});
