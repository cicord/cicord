import { createCicordClient } from "@cicord/client";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
});
afterEach(cleanup);

function setup(path: string) {
  const runtime = createMockRuntime({ latency: 0, persist: false });
  const config: AdminConfig = {
    client: createCicordClient({ fetch: watch401(runtime.fetch) }),
    loginUrl: "/auth/login?redirect=/admin/",
    mock: true,
  };
  window.history.replaceState(null, "", path.replace(/^\/admin\//, import.meta.env.BASE_URL));
  render(<App config={config} />);
  return { runtime, user: userEvent.setup() };
}

describe("pause enforcement", () => {
  test("a moderator pauses from the overview; every page shows the banner; resume shows the plan", async () => {
    const { user, runtime } = setup("/admin/servers/pixel-haven");
    await user.click(await screen.findByRole("button", { name: /Mina/ }));
    await user.click(await screen.findByRole("button", { name: "Pause enforcement" }));
    const dialog = await screen.findByRole("dialog", { name: "Pause enforcement?" });
    await user.type(within(dialog).getByRole("textbox", { name: "Reason (optional)" }), "raid");
    await user.click(within(dialog).getByRole("button", { name: "Pause enforcement" }));
    expect(await screen.findByText("Enforcement paused")).toBeTruthy();

    const banner = await screen.findByTestId("pause-banner");
    expect(within(banner).getByText("Enforcement is paused")).toBeTruthy();
    expect(within(banner).getByText(/Paused by Mina/)).toBeTruthy();
    expect(within(banner).getByText(/raid/)).toBeTruthy();
    expect(within(banner).getByRole("link", { name: "2 fields differ from git." })).toBeTruthy();
    expect(runtime.state.paused?.reason).toBe("raid");
    expect(runtime.state.audit[0]?.action).toBe("enforcement.pause");
    expect(runtime.state.history[0]?.status).toBe("paused");

    // the banner is on every page of the server
    await user.click(within(banner).getByRole("link", { name: "2 fields differ from git." }));
    expect(await screen.findByRole("heading", { level: 1, name: "Drift" })).toBeTruthy();
    const onDrift = await screen.findByTestId("pause-banner");

    // Resume: the pending plan first, then confirm (moderators cannot sync)
    await user.click(within(onDrift).getByRole("button", { name: "Resume…" }));
    const resume = await screen.findByRole("dialog", { name: "Resume enforcement?" });
    expect(await within(resume).findByText("Pending plan")).toBeTruthy();
    expect(await within(resume).findByText("valorant")).toBeTruthy();
    expect(within(resume).queryByRole("button", { name: "Resume and sync now" })).toBeNull();
    await user.click(within(resume).getByRole("button", { name: "Resume enforcement" }));
    expect(await screen.findByText("Enforcement resumed")).toBeTruthy();
    expect(runtime.state.paused).toBeUndefined();
    expect(runtime.state.history[0]?.status).toBe("resumed");
    await waitFor(() => expect(screen.queryByTestId("pause-banner")).toBeNull());
  });

  test("an admin resumes and syncs at once; Sync now is replaced while paused", async () => {
    const { user, runtime } = setup("/admin/servers/pixel-haven/plan");
    await user.click(await screen.findByRole("button", { name: /Owner/ }));
    await screen.findByRole("button", { name: "Sync now" });
    // from the command palette
    await user.keyboard("{Control>}k{/Control}");
    await user.click(
      await screen.findByRole("option", { name: /Pause enforcement on Pixel Haven/ }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Pause enforcement?" });
    await user.click(within(dialog).getByRole("button", { name: "Pause enforcement" }));
    await screen.findByTestId("pause-banner");
    expect(screen.queryByRole("button", { name: "Sync now" })).toBeNull();

    // a sync while paused is refused by the runtime with a clear error
    const res = await runtime.fetch("/api/v1/servers/pixel-haven/sync", {
      method: "POST",
      headers: { "X-Cicord-Request": "1" },
    });
    expect(res.status).toBe(409);

    const banner = screen.getByTestId("pause-banner");
    await user.click(within(banner).getByRole("button", { name: "Resume…" }));
    const resume = await screen.findByRole("dialog", { name: "Resume enforcement?" });

    await within(resume).findByText("Pending plan");
    await user.click(within(resume).getByRole("button", { name: "Resume and sync now" }));
    expect(await screen.findByText(/Enforcement resumed: \d+ changes? applied/)).toBeTruthy();
    expect(runtime.state.paused).toBeUndefined();
    expect(runtime.state.drift).toBe(false);
  });
});
