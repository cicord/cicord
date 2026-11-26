import { createCicordClient } from "@cicord/client";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { createMockRuntime } from "../src/mock/runtime.ts";

afterEach(cleanup);
test("server admins can export a member report and confirm an erasure", async () => {
  sessionStorage.clear();
  localStorage.clear();
  const rt = createMockRuntime({ latency: 0, persist: false }),
    user = userEvent.setup();
  const client = createCicordClient({ fetch: rt.fetch });
  window.history.replaceState(null, "", import.meta.env.BASE_URL + "servers/pixel-haven/settings");
  render(<App config={{ client, loginUrl: "/auth/login", mock: true }} />);
  await user.click(await screen.findByRole("button", { name: /Owner/ }));
  const id = await screen.findByRole("textbox", { name: "Discord member ID" });
  expect(screen.getByRole("button", { name: "Export data" }).getAttribute("disabled")).not.toBe(
    null,
  );
  await user.type(id, "111111111111111111");
  await user.click(screen.getByRole("button", { name: "Export data" }));
  expect(await screen.findByText("Some plugins could not verify this request")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Download JSON report" })).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Erase data" }));
  const dlg = await screen.findByRole("alertdialog");
  expect(within(dlg).getByText("Erase data for member 111111111111111111?")).toBeTruthy();
  await user.click(within(dlg).getByRole("button", { name: "Erase data" }));
  expect(await screen.findByText("Some plugins could not verify this request")).toBeTruthy();
  await user.clear(id);
  expect(screen.queryByRole("button", { name: "Download JSON report" })).toBeNull();
});
