import { createCicordClient, type FetchLike } from "@cicord/client";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";

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
type Intercept = (
  url: URL,
  init: RequestInit,
  runtime: ReturnType<typeof createMockRuntime>,
) => Response | Promise<Response | undefined> | undefined;

/** The admin on the mock runtime with installed plugins that have updates. */
function setup(path: string, intercept?: Intercept) {
  const runtime = createMockRuntime({ latency: 0, persist: false, demoUpdates: true });
  const sent: Sent[] = [];
  const fetch: FetchLike = async (input, init) => {
    const url = new URL(input, window.location.origin);
    sent.push({
      method: (init.method ?? "GET").toUpperCase(),
      path: url.pathname,
      query: url.searchParams.toString(),
      body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
    });
    return (await intercept?.(url, init, runtime)) ?? runtime.fetch(input, init);
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

async function login(user: ReturnType<typeof userEvent.setup>, who = /Owner/) {
  await user.click(await screen.findByRole("button", { name: who }, { timeout: 3000 }));
}

const updates = (sent: Sent[]) =>
  sent.filter((s) => s.method === "POST" && s.path.endsWith("/update"));

const SERVER = "/admin/servers/ma-commu";

describe("plugin updates", () => {
  test("tables show the update badge and filter on it", async () => {
    const { user, sent } = setup(`${SERVER}/plugins`);
    await login(user);
    const table = await screen.findByRole("table", { name: /plugin/i });
    expect(await within(table).findByText("Update 0.1.0")).toBeTruthy();
    expect(within(table).getByText("Update 0.4.1")).toBeTruthy();
    await user.selectOptions(screen.getByRole("combobox", { name: "Updates" }), "true");
    expect(await screen.findByText(/of 2$/)).toBeTruthy();
    expect(sent.some((s) => s.path.endsWith("/plugins") && s.query.includes("updates=true"))).toBe(
      true,
    );

    cleanup();
    const store = setup(`${SERVER}/store`);
    await login(store.user);
    const storeTable = await screen.findByRole("table", { name: "Store plugins" });
    await store.user.selectOptions(screen.getByRole("combobox", { name: "Updates" }), "true");
    expect(await within(storeTable).findByText("Update 0.4.1")).toBeTruthy();
    expect(within(storeTable).getByText("Update 0.1.0")).toBeTruthy();
    expect(
      store.sent.some(
        (s) => s.path === "/api/v1/store/plugins" && s.query.includes("updates=true"),
      ),
    ).toBe(true);
  });

  test("an official copy: banner on the instance, review, update", async () => {
    const { user, sent } = setup(`${SERVER}/plugins/moderator/welcome`);
    await login(user);
    const banner = (await screen.findByText("Update available: 0.0.9 → 0.1.0")).closest(
      "[role=status]",
    ) as HTMLElement;
    await user.click(within(banner).getByRole("link", { name: "Review the update" }));
    const main = screen.getByRole("main");
    expect(await within(main).findByRole("heading", { name: /Update Welcome/ })).toBeTruthy();
    // The capability the new version adds is highlighted.
    const added = within(main).getByRole("list", { name: "Capabilities" });
    expect(within(added).getByText("New")).toBeTruthy();
    expect(within(main).getByText("Files of plugins/welcome/")).toBeTruthy();
    expect(within(main).queryByRole("checkbox")).toBeNull(); // official, unmodified
    await user.click(within(main).getByRole("button", { name: "Update to 0.1.0" }));
    expect(await within(main).findByText("Welcome is updated to 0.1.0")).toBeTruthy();
    expect(updates(sent)[0]).toMatchObject({
      path: "/api/v1/store/cicord/welcome/update",
      body: { version: "0.1.0" },
    });
  });

  test("an unofficial registry plugin: the confirmation restates what is newly granted", async () => {
    const { user, sent } = setup(`${SERVER}/store/acme/giveaways?update=1`);
    await login(user);
    const main = screen.getByRole("main");
    const submit = await within(main).findByRole("button", { name: "Update to 0.4.1" });
    expect(submit).toHaveProperty("disabled", true);
    const confirm = within(main).getByRole("checkbox", { name: /I trust acme/ });
    const restated = document.getElementById(confirm.getAttribute("aria-describedby")!)!;
    expect(restated.textContent).toMatch(/new capability: Run tasks on a schedule/);
    expect(within(main).getByText(/grants \+ scheduler/)).toBeTruthy();
    await user.click(confirm);
    await user.click(submit);
    expect(await within(main).findByText("Giveaways is updated to 0.4.1")).toBeTruthy();
    expect(updates(sent)[0]!.body).toEqual({
      version: "0.4.1",
      commit: "8e1d2c3b4a5f60718293a4b5c6d7e8f9a0b1c2d3",
      integrity: "sha256-Zk3pQ8wR1vT6yU2iO9aS5dF4gH7jK0lZxC3vB6nM8qE=",
      confirmUnofficial: true,
    });
  });

  test("errors: changed, invalid config with diagnostics", async () => {
    let answer: Response | undefined = json(
      {
        error: { code: "changed", message: "Giveaways changed since you reviewed it (now 0.5.0)." },
      },
      409,
    );
    const { user } = setup(`${SERVER}/store/acme/giveaways?update=1`, (url, init) =>
      init.method === "POST" && url.pathname.endsWith("/update") ? answer : undefined,
    );
    await login(user);
    const main = screen.getByRole("main");
    await user.click(await within(main).findByRole("checkbox", { name: /I trust acme/ }));
    await user.click(within(main).getByRole("button", { name: "Update to 0.4.1" }));
    expect(await within(main).findByText("The plugin changed since you reviewed it")).toBeTruthy();
    expect(within(main).getByRole("button", { name: "Reload and review" })).toBeTruthy();

    answer = json(
      {
        error: {
          code: "invalid",
          message: "The configuration is not valid with Giveaways 0.4.1: missing channel",
          diagnostics: [
            {
              severity: "error",
              file: "bots/moderator/bot.yaml",
              message: "[server ma-commu] missing channel",
            },
          ],
        },
      },
      422,
    );
    await user.click(within(main).getByRole("checkbox", { name: /I trust acme/ }));
    await user.click(within(main).getByRole("button", { name: "Update to 0.4.1" }));
    expect(
      await within(main).findByText("The configuration is not valid with the new version"),
    ).toBeTruthy();
    expect(within(main).getByText("[server ma-commu] missing channel")).toBeTruthy();
  });

  test("a plugin that also runs where the viewer is not admin cannot be updated", async () => {
    const { user } = setup(
      `${SERVER}/store/acme/giveaways?update=1`,
      async (url, init, runtime) => {
        if (init.method !== "GET" || url.pathname !== "/api/v1/store/acme/giveaways/update")
          return undefined;
        const review = (await (await runtime.fetch(url.toString(), init)).json()) as object;
        return json({
          ...review,
          servers: ["annexe", "ma-commu"],
          missingAdmin: ["annexe"],
          restricted: [{ server: "annexe", instances: 2 }],
        });
      },
    );
    await login(user);
    const main = screen.getByRole("main");
    expect(await within(main).findByText("You cannot apply this update")).toBeTruthy();
    expect(within(main).getByText("annexe: 2 instances")).toBeTruthy();
    await user.click(within(main).getByRole("checkbox", { name: /I trust acme/ }));
    expect(within(main).getByRole("button", { name: "Update to 0.4.1" })).toHaveProperty(
      "disabled",
      true,
    );
  });
});
