import { createCicordClient } from "@cicord/client";
import { CicordProvider } from "@cicord/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vite-plus/test";
import { TooltipProvider } from "../src/components/ui/tooltip.tsx";
import { DiagnosticsPage } from "../src/pages/Diagnostics.tsx";
import { ServerGrantConsent } from "../src/pages/Store.tsx";
import { useState } from "react";

test("server diagnostics exposes directed contracts, missing grants and event payload on demand", async () => {
  let payloadReads = 0;
  const client = createCicordClient({
    fetch: async (input) => {
      const path = new URL(input, "http://localhost").pathname;
      let body: unknown = { ranAt: new Date().toISOString(), checks: [] };
      if (path.endsWith("/interop"))
        body = {
          edges: [
            {
              kind: "event",
              from: "mod/polls",
              to: "rewards/levels",
              contract: "polls.closed v2",
              status: "active",
            },
          ],
          features: { "rewards/levels": { roles: "plugins.call:roles.grant not granted" } },
        };
      if (path.endsWith("/dead-letters"))
        body = {
          items: [
            {
              event: true,
              id: "e1",
              scope: "plugin:rewards/levels/server",
              name: "polls.closed",
              attempts: 5,
              reason: "handler failed",
            },
          ],
          total: 1,
          truncated: false,
        };
      if (path.endsWith("/e1/event")) {
        payloadReads++;
        body = { voters: ["member-1"] };
      }
      return new Response(JSON.stringify(body), {
        headers: { "content-type": "application/json" },
      });
    },
  });
  render(
    <CicordProvider client={client}>
      <TooltipProvider>
        <DiagnosticsPage serverKey="server" />
      </TooltipProvider>
    </CicordProvider>,
  );
  const region = await screen.findByRole("region", { name: "Plugin integrations" });
  expect(within(region).getByText("mod/polls → rewards/levels")).toBeTruthy();
  expect(within(region).getByText(/not granted/)).toBeTruthy();
  expect(payloadReads).toBe(0);
  await userEvent.click(await screen.findByRole("button", { name: "Event payload" }));
  expect(await screen.findByText(/member-1/)).toBeTruthy();
  expect(payloadReads).toBe(1);
});
test("privileged consent names every server capability and needs an explicit selection", async () => {
  function Consent() {
    const [accepted, setAccepted] = useState(false);
    return (
      <ServerGrantConsent
        grants={["server.overlays", "server.sync"]}
        checked={accepted}
        change={setAccepted}
      />
    );
  }
  render(<Consent />);
  const checkbox = screen.getByRole<HTMLInputElement>("checkbox", {
    name: /server.overlays, server.sync/,
  });
  expect(checkbox.checked).toBe(false);
  await userEvent.click(checkbox);
  expect(checkbox.checked).toBe(true);
});
