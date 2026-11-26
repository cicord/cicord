import { createCicordClient } from "@cicord/client";
import { CicordProvider } from "@cicord/react";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { TooltipProvider } from "../src/components/ui/tooltip.tsx";
import { createMockRuntime } from "../src/mock/runtime.ts";
import { DiagnosticsPage } from "../src/pages/Diagnostics.tsx";

afterEach(cleanup);

test("diagnostics follows nextCursor with Load more and drops a resolved task", async () => {
  sessionStorage.clear();
  const rt = createMockRuntime({ latency: 0, persist: false, demoDeadLetters: 150 });
  const client = createCicordClient({ fetch: rt.fetch });
  await client.auth.devLogin("100");
  render(
    <CicordProvider client={client}>
      <TooltipProvider>
        <DiagnosticsPage serverKey="pixel-haven" />
      </TooltipProvider>
    </CicordProvider>,
  );
  const region = await screen.findByRole("region", { name: "Failed tasks and event deliveries" });
  expect(within(region).getByRole("status").textContent).toContain("oldest 100 of 151");
  await userEvent.click(within(region).getByRole("button", { name: "Load more" }));
  await waitFor(() => expect(within(region).getAllByText("remind")).toHaveLength(150));
  expect(within(region).queryByRole("button", { name: "Load more" })).toBeNull();
  expect(within(region).queryByRole("status")).toBeNull();
  expect((await client.admin.deadLetters("pixel-haven", { limit: 151 })).nextCursor).toBeNull();
  const rows = within(region).getAllByRole("listitem");
  expect(rows).toHaveLength(151);
  // The last row came from the second page: resolving it removes it locally.
  await userEvent.click(within(rows.at(-1)!).getByRole("button", { name: "Retry task" }));
  await waitFor(() => expect(within(region).getAllByRole("listitem")).toHaveLength(150));
});

test("the server page warns about actions by the bot that Cicord did not make", async () => {
  sessionStorage.clear();
  localStorage.clear();
  const rt = createMockRuntime({ latency: 0, persist: false, demoUnconfirmedAudit: true });
  const client = createCicordClient({ fetch: rt.fetch });
  window.history.replaceState(null, "", import.meta.env.BASE_URL + "servers/pixel-haven");
  render(<App config={{ client, loginUrl: "/auth/login", mock: true }} />);
  await userEvent.click(await screen.findByRole("button", { name: /Owner/ }));
  expect(await screen.findByText("Unexplained actions by bot moderator")).toBeTruthy();
  expect(
    screen.getByText(/shows 3 actions by this bot that Cicord did not make \(last: role.update\)/),
  ).toBeTruthy();
});
