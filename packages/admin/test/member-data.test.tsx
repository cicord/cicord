import { createCicordClient } from "@cicord/client";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
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

test("an uninstalled installation's data is reported and purged after typing its name", async () => {
  sessionStorage.clear();
  localStorage.clear();
  const rt = createMockRuntime({ latency: 0, persist: false }),
    user = userEvent.setup();
  const client = createCicordClient({ fetch: rt.fetch });
  window.history.replaceState(null, "", import.meta.env.BASE_URL + "servers/pixel-haven/settings");
  render(<App config={{ client, loginUrl: "/auth/login", mock: true }} />);
  await user.click(await screen.findByRole("button", { name: /Owner/ }));
  const id = await screen.findByRole("textbox", { name: "Discord member ID" });
  await user.type(id, "7");
  expect(screen.getByRole("button", { name: "Export data" }).getAttribute("disabled")).not.toBe(
    null,
  );
  await user.clear(id);
  await user.type(id, "111111111111111111");
  await user.click(screen.getByRole("button", { name: "Export data" }));
  expect(await screen.findByText("Uninstalled, data kept")).toBeTruthy();
  expect(screen.getByText(/Purge its data below to complete the erasure/)).toBeTruthy();

  const title = await screen.findByText("Data of uninstalled plugins");
  const card = title.closest<HTMLElement>("[aria-labelledby]");
  if (!card) throw new Error("orphaned data card missing");
  expect(within(card).getByText("moderator/old-tickets")).toBeTruthy();
  expect(within(card).getByText(/14 records, 3 ranking entries/)).toBeTruthy();
  await user.click(within(card).getByRole("button", { name: "Purge data" }));
  const dlg = await screen.findByRole("alertdialog");
  const purge = within(dlg).getByRole("button", { name: "Purge data" });
  expect(purge.getAttribute("disabled")).not.toBe(null);
  await user.type(within(dlg).getByRole("textbox"), "moderator/old");
  expect(purge.getAttribute("disabled")).not.toBe(null);
  await user.type(within(dlg).getByRole("textbox"), "-tickets");
  expect(purge.getAttribute("disabled")).toBe(null);
  await user.click(purge);
  await waitFor(() => expect(screen.queryByText("Data of uninstalled plugins")).toBeNull());
  expect(await client.admin.orphanedData("pixel-haven")).toEqual([]);
  const audit = await client.admin.audit("pixel-haven");
  expect(
    audit.some((a) => a.action === "plugin.data.purge" && a.target === "moderator/old-tickets"),
  ).toBe(true);
});
