import { createCicordClient, type PortalPlugin } from "@cicord/client";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { portalServer, user } from "../src/mock/data.ts";
import { navigate } from "../src/router.tsx";

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
  localStorage.clear();
});
type Reply = { status?: number; body: unknown };
function fixture(
  initialPath: string,
  opts: {
    plugins: PortalPlugin[];
    role?: "admin" | "member" | "moderator" | "former";
    handler?: (method: string, path: string, body: unknown, search: URLSearchParams) => Reply;
  },
) {
  const requests: { method: string; path: string; body: unknown }[] = [];
  window.history.replaceState(null, "", initialPath);
  const server = { ...portalServer, plugins: opts.plugins };
  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  const client = createCicordClient({
    fetch: async (input, init) => {
      const url = new URL(input, window.location.origin);
      const p = url.pathname.replace(/^\/api\/v1/, "");
      if (p === "/portal/servers") return json({ items: [server] });
      if (p === "/me")
        return opts.role
          ? json({ user, servers: [] })
          : json({ error: { code: "unauthenticated" } }, 401);
      if (p.endsWith("/me") && !p.includes("/plugins/"))
        return opts.role && opts.role !== "former"
          ? json({ user, member: { roles: [] }, role: opts.role })
          : json({ error: { code: "forbidden" } }, 403);
      const marker = "/plugins/";
      if (p.includes(marker)) {
        const path = p.split(marker)[1]!,
          body = typeof init.body === "string" ? JSON.parse(init.body) : undefined,
          method = init.method ?? "GET";
        requests.push({ method, path, body });
        const r = opts.handler?.(method, path, body, url.searchParams);
        if (r) return json(r.body, r.status);
      }
      return json({ error: { code: "not_found" } }, 404);
    },
  });
  render(<App config={{ client, login: () => {}, mock: false }} />);
  return requests;
}
function plugin(name: string, routes: string[], instance = name): PortalPlugin {
  return { plugin: name, bot: "desk", instance, routes };
}
const mod = plugin("moderation", ["GET /cases", "GET /members/:id"]);
const verification = plugin("verification", [
  "GET /challenge",
  "POST /challenge",
  "GET /queue",
  "POST /queue/:id",
  "GET /return/:id",
]);

test("member navigation omits staff pages and direct URLs issue no staff calls", async () => {
  const r = fixture("/community", { role: "member", plugins: [mod, verification] });
  await screen.findByRole("link", { name: "My moderation history" });
  expect(screen.queryByRole("link", { name: "Moderation cases" })).toBeNull();
  navigate("/moderation/cases");
  await screen.findByText("You do not have access to this page.");
  navigate("/moderation/members/999999");
  await screen.findByRole("heading", { name: "Page not found" });
  expect(r).toHaveLength(0);
});
test("self history requests only the viewer and never renders staff notes or evidence", async () => {
  const r = fixture("/moderation/me", {
    role: "member",
    plugins: [mod],
    handler: () => ({
      body: {
        items: [
          {
            number: 1,
            action: "warn",
            at: "2026-01-01",
            reason: "Keep it civil",
            evidence: "private evidence",
          },
          { number: 2, action: "note", reason: "internal note" },
        ],
      },
    }),
  });
  await screen.findByText("Keep it civil");
  expect(screen.queryByText("private evidence")).toBeNull();
  expect(screen.queryByText("internal note")).toBeNull();
  expect(r[0]?.path).toBe("desk/moderation/members/" + user.id);
});
test("a scoped recovery reads the linked instance for a former member and drops old case content", async () => {
  const r = fixture("/verification/return/case-a?bot=desk&instance=second", {
    role: "former",
    plugins: [verification, { ...verification, instance: "second" }],
    handler: (_m, path) =>
      path.endsWith("case-a")
        ? { body: { result: "passed", at: null, method: "Recovered case A" } }
        : { status: 403, body: { error: { code: "forbidden" } } },
  });
  await screen.findByText("Recovered case A");
  navigate("/verification/return/case-b?bot=desk&instance=second");
  await screen.findByText("You no longer have access to this information.");
  expect(screen.queryByText("Recovered case A")).toBeNull();
  expect(r.map((x) => x.path)).toEqual(["desk/second/return/case-a", "desk/second/return/case-b"]);
});
test("visual challenge sends only the selected response and displays the result", async () => {
  const u = userEvent.setup();
  const r = fixture("/verification/challenge", {
    role: "member",
    plugins: [verification],
    handler: (method) => ({
      body:
        method === "GET"
          ? {
              id: "nonce",
              method: "visual",
              image: "data:image/png;base64,aA==",
              prompt: "How many squares?",
              choices: [1, 2, 3],
            }
          : { verified: true },
    }),
  });
  await u.click(await screen.findByRole("radio", { name: "2" }));
  await u.click(screen.getByRole("button", { name: "Verify my account" }));
  await screen.findByText("Your account is verified.");
  expect(r.at(-1)?.body).toEqual({ id: "nonce", answer: 2 });
});
test("moderator decisions update the queue, and filters reach only the selected instance", async () => {
  let approved = false;
  const u = userEvent.setup();
  const r = fixture("/verification/queue", {
    role: "moderator",
    plugins: [verification],
    handler: (method, path, body) => {
      if (method === "POST") {
        approved =
          path.endsWith("/queue/123") && (body as { decision: string }).decision === "approve";
        return { body: { verified: true } };
      }
      return { body: { items: approved ? [] : [{ userId: "123", joined: 10000 }] } };
    },
  });
  await u.click(await screen.findByRole("button", { name: "Approve" }));
  await screen.findByText("No entries.");
  expect(r.some((x) => x.method === "POST")).toBe(true);
});
test("an uninstalled route and a mismatched explicit instance fail closed", async () => {
  const r = fixture("/moderation/cases?bot=unknown&instance=other", {
    role: "admin",
    plugins: [mod],
  });
  await screen.findByRole("heading", { name: "Page not found" });
  navigate("/verification/queue");
  await waitFor(() => expect(screen.getByRole("heading", { name: "Page not found" })).toBeTruthy());
  expect(r).toHaveLength(0);
});

test("announcement preview sends no publish request and reflects only the current draft", async () => {
  const u = userEvent.setup();
  const calls = fixture("/announcements", {
    role: "moderator",
    plugins: [plugin("announcements", ["POST /preview", "POST /compose"])],
    handler: (_m, p, b) =>
      p.endsWith("/preview")
        ? {
            body: {
              message: {
                embeds: [{ title: "Preview", description: (b as { text: string }).text }],
              },
            },
          }
        : { body: { id: 7 } },
  });
  await u.type(await screen.findByLabelText("Channel ID"), "12345");
  const message = screen.getByLabelText("Message");
  await u.type(message, "Game night");
  await u.click(screen.getByRole("button", { name: "Preview" }));
  await screen.findByRole("region", { name: "Message preview" });
  expect(calls.some((x) => x.path.endsWith("/compose"))).toBe(false);
  await u.type(message, " Friday");
  expect(screen.queryByRole("region", { name: "Message preview" })).toBeNull();
  await u.click(screen.getByRole("button", { name: "Publish announcement" }));
  await screen.findByText("Announcement queued #7");
  expect(calls.at(-1)?.body).toMatchObject({ text: "Game night Friday", channel: "12345" });
});
test("birthday accepts leap day, rejects an invalid date and withdraws only on confirmation", async () => {
  let birthday: { month: number; day: number } | null = null;
  const u = userEvent.setup();
  const calls = fixture("/birthday", {
    role: "member",
    plugins: [plugin("birthdays", ["GET /birthday", "PUT /birthday", "DELETE /birthday"])],
    handler: (m, _p, b) => {
      if (m === "PUT") birthday = b as typeof birthday;
      if (m === "DELETE") birthday = null;
      return { body: m === "GET" ? { birthday } : { saved: true } };
    },
  });
  await u.selectOptions(await screen.findByLabelText("Month"), "2");
  const day = screen.getByLabelText<HTMLInputElement>("Day");
  await u.type(day, "30");
  await u.click(screen.getByRole("button", { name: "Save birthday" }));
  expect(day.validity.valid).toBe(false);
  await u.clear(day);
  await u.type(day, "29");
  await u.click(screen.getByRole("button", { name: "Save birthday" }));
  await u.click(await screen.findByRole("button", { name: "Remove birthday" }));
  expect(calls.some((r) => r.method === "DELETE")).toBe(false);
  await u.click(screen.getByRole("button", { name: "Confirm removal" }));
  await waitFor(() => expect(birthday).toBeNull());
  expect(calls.find((r) => r.method === "PUT")?.body).toEqual({ month: 2, day: 29 });
});
test("members with a delegated staff role can save a local weekly shift", async () => {
  const u = userEvent.setup();
  let stored: unknown;
  const calls = fixture("/staff-shifts", {
    role: "member",
    plugins: [plugin("staff-shifts", ["GET /me", "PUT /me/plan", "GET /coverage"])],
    handler: (m, p, b) => {
      if (m === "PUT") stored = b;
      return {
        body: p.endsWith("/coverage")
          ? { from: 0, to: 1000, gaps: [] }
          : m === "PUT"
            ? { saved: true }
            : { zones: ["UTC", "Europe/Paris"], plan: stored ?? { zone: "UTC", shifts: [] } },
      };
    },
  });
  await u.selectOptions(await screen.findByLabelText("Time zone"), "Europe/Paris");
  await u.click(screen.getByRole("button", { name: "Add shift" }));
  await u.selectOptions(screen.getByLabelText("Weekday"), "2");
  await u.click(screen.getByRole("button", { name: "Save weekly schedule" }));
  await screen.findByText("Schedule saved.");
  expect(calls.find((r) => r.method === "PUT")?.body).toEqual({
    zone: "Europe/Paris",
    shifts: [{ day: 2, start: 540, minutes: 60 }],
  });
});

test("public giveaway displays its commitment and never reads the staff entrant list", async () => {
  const r = fixture("/giveaways/7", {
    plugins: [plugin("giveaways", ["GET /giveaways/:id", "GET /giveaways/:id/entries"])],
    handler: () => ({
      body: {
        id: "7",
        prize: "Game key",
        state: "ended",
        closesAt: 123456,
        commitment: "abc123",
        entrants: 3,
        winners: ["W1"],
        seed: "secret-after-close",
        entrantsHash: "hash77",
        redacted: true,
      },
    }),
  });
  await screen.findByText("Game key");
  expect(screen.getByText("abc123")).toBeTruthy();
  expect(screen.getByText("secret-after-close")).toBeTruthy();
  expect(screen.queryByText("Staff participant list")).toBeNull();
  expect(r.map((x) => x.path)).toEqual(["desk/giveaways/giveaways/7"]);
});
test("polls render only counts, even when a response contains extra identities", async () => {
  fixture("/polls/1", {
    role: "admin",
    plugins: [plugin("polls", ["GET /polls/:id"])],
    handler: () => ({
      body: {
        question: "Game night?",
        status: "closed",
        voters: 4,
        closesAt: "2026-01-01",
        choices: [{ label: "Friday", votes: 4, voterIds: ["private-person"] }],
        voterIds: ["private-person"],
      },
    }),
  });
  await screen.findByText("Friday");
  expect(screen.queryByText("private-person")).toBeNull();
});
test("staff suggestion decisions require a reason and retain status pagination after hidden rows", async () => {
  const u = userEvent.setup();
  let updated = false;
  const r = fixture("/suggestions/moderation", {
    role: "moderator",
    plugins: [
      plugin("suggestions", ["GET /suggestions/moderation", "POST /suggestions/:id/status"]),
    ],
    handler: (m, _p, b, q) => {
      if (m === "POST") {
        updated = (b as { reason: string }).reason === "On the calendar";
        return { body: { id: "7", status: "approved", score: 2 } };
      }
      return {
        body:
          q.get("offset") === "100"
            ? { items: [{ id: "8", score: 0, status: "open", body: "Second page" }] }
            : {
                items: [
                  { id: "7", score: 2, status: updated ? "approved" : "open", body: "Meetup" },
                ],
                nextOffset: 100,
              },
      };
    },
  });
  await u.click(await screen.findByText("Change status"));
  await u.selectOptions(screen.getByLabelText("New status"), "approved");
  expect(screen.getByRole<HTMLButtonElement>("button", { name: "Save decision" }).disabled).toBe(
    true,
  );
  await u.type(screen.getByLabelText("Decision reason"), "On the calendar");
  await u.click(screen.getByRole("button", { name: "Save decision" }));
  await waitFor(() => expect(updated).toBe(true));
  await u.click(await screen.findByRole("button", { name: "Next" }));
  await screen.findByText("Second page");
  expect(r.filter((x) => x.method === "POST")).toHaveLength(1);
});
test("a retried purchase reuses the same UUID and refreshes the wallet after delivery", async () => {
  const u = userEvent.setup();
  let attempts = 0,
    points = 10;
  const r = fixture("/economy", {
    role: "member",
    plugins: [plugin("economy", ["GET /wallet", "GET /shop", "POST /shop/:id/buy", "GET /ledger"])],
    handler: (m, p) => {
      if (m === "POST") {
        if (++attempts === 1) return { status: 503, body: { error: { code: "network" } } };
        points = 5;
        return { body: { itemId: "blue", orderId: "R-1", state: "delivered" } };
      }
      return {
        body: p.endsWith("/shop")
          ? { items: [{ id: "blue", label: "Blue role", price: 5 }] }
          : { balance: points },
      };
    },
  });
  await u.click(await screen.findByRole("button", { name: "Buy Blue role" }));
  await u.click(screen.getByRole("button", { name: /Confirm purchase/ }));
  await screen.findByText("The request could not be completed.");
  await u.click(screen.getByRole("button", { name: /Check purchase/ }));
  await screen.findByText(/Reward delivered/);
  expect(screen.getByText("5 points", { selector: "strong" })).toBeTruthy();
  const buys = r.filter((x) => x.method === "POST");
  expect(buys).toHaveLength(2);
  expect(buys[0]?.body).toEqual(buys[1]?.body);
  expect((buys[0]!.body as { purchaseId: string }).purchaseId).toMatch(/^[a-f0-9-]{36}$/);
  expect(r.some((x) => x.path.endsWith("/ledger"))).toBe(false);
});
test("tag search honors Git ownership and content audience, and saves an editable tag", async () => {
  const u = userEvent.setup();
  const r = fixture("/tags", {
    role: "moderator",
    plugins: [plugin("tags", ["GET /tags", "PUT /tags/:name", "DELETE /tags/:name"])],
    handler: () => ({
      body: {
        canEdit: true,
        entries: [
          { name: "policy", text: "Git policy", aliases: [], keywords: [], editable: false },
          { name: "help", text: "Contact us", aliases: [], keywords: [], editable: true },
        ],
      },
    }),
  });
  await screen.findByText("Git policy");
  expect(screen.queryByRole("button", { name: "Edit policy" })).toBeNull();
  await u.click(screen.getByRole("button", { name: "Edit help" }));
  const content = screen.getByLabelText("Content");
  await u.clear(content);
  await u.type(content, "Ask staff");
  await u.click(screen.getByRole("button", { name: "Save tag" }));
  await screen.findByText("Tags updated.");
  expect(r.find((x) => x.method === "PUT")).toMatchObject({
    path: "desk/tags/tags/help",
    body: { name: "help", text: "Ask staff" },
  });
});
test("audit filters target exact fields, and a denied re-read clears earlier text", async () => {
  const u = userEvent.setup();
  let matched = false;
  const r = fixture("/audit-log", {
    role: "moderator",
    plugins: [plugin("audit-log", ["GET /search", "GET /export"])],
    handler: (_m, _p, _b, q) => {
      if (
        q.get("channel") === "123" &&
        q.get("member") === "456" &&
        q.get("category") === "roles" &&
        q.get("q") === "change"
      ) {
        matched = true;
        return { status: 403, body: { error: { code: "forbidden" } } };
      }
      return {
        body: {
          items: [
            { id: "event-1", at: 10, category: "members", text: "Previously visible record" },
          ],
        },
      };
    },
  });
  await screen.findByText("Previously visible record");
  await u.type(screen.getByLabelText("Channel ID"), "123");
  await u.type(screen.getByLabelText("Member ID"), "456");
  await u.selectOptions(screen.getByLabelText("Event type"), "roles");
  await u.type(screen.getByLabelText("Search audit entries"), "change");
  await u.click(screen.getByRole("button", { name: "Search" }));
  await screen.findByText("You no longer have access to this information.");
  expect(screen.queryByText("Previously visible record")).toBeNull();
  expect(matched).toBe(true);
  expect(r.every((x) => x.path.endsWith("/search"))).toBe(true);
});
test("public calendar subscribes directly to the discovered instance's ICS feed", async () => {
  fixture("/calendar", {
    plugins: [plugin("community-events", ["GET /calendar", "GET /calendar.ics"], "weekends")],
    handler: () => ({
      body: [{ id: "id", name: "Community meetup", start: "2026-12-12T12:00:00Z" }],
    }),
  });
  await screen.findByText("Community meetup");
  const link = screen.getByRole<HTMLAnchorElement>("link", { name: "Download calendar (.ics)" });
  expect(link.href).toContain("/api/v1/portal/my-community/plugins/desk/weekends/calendar.ics");
  expect(link.href).not.toContain("token");
});

test("guard blocklist adds a hash to the selected instance and refreshes confirmed entries", async () => {
  let hashes: string[] = [];
  const u = userEvent.setup();
  const r = fixture("/guard/blocklist?bot=desk&instance=west", {
    role: "moderator",
    plugins: [
      plugin("guard", ["GET /blocklist", "POST /blocklist"], "east"),
      plugin("guard", ["GET /blocklist", "POST /blocklist"], "west"),
    ],
    handler: (method, _p, b) => {
      if (method === "POST") hashes = [(b as { value: string }).value];
      return { body: { domains: [], hashes } };
    },
  });
  await u.selectOptions(await screen.findByLabelText("Type"), "hash");
  await u.type(screen.getByLabelText("Image hash"), "82673f3e3c60c327");
  await u.click(screen.getByRole("button", { name: "Add to blocklist" }));
  await screen.findByText("82673f3e3c60c327", { selector: "code" });
  expect(r.find((x) => x.method === "POST")).toMatchObject({
    path: "desk/west/blocklist",
    body: { action: "add", kind: "hash", value: "82673f3e3c60c327" },
  });
});
test("moderator stats show reported counts and pass the server's opaque cursor", async () => {
  const u = userEvent.setup();
  let second = false;
  fixture("/moderation/statistics", {
    role: "admin",
    plugins: [plugin("moderation", ["GET /stats/moderators"])],
    handler: (_m, _p, _b, q) => {
      second = q.get("cursor") === "opaque";
      return {
        body: {
          items: [
            { moderatorId: second ? "moderator-two" : "moderator-one", count: second ? 3 : 5 },
          ],
          next: second ? null : "opaque",
        },
      };
    },
  });
  await screen.findByText("moderator-one");
  await u.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByText("moderator-two");
  expect(second).toBe(true);
});
test("verification recovery ignores untrusted invite URLs", async () => {
  fixture("/verification/return/case-a", {
    role: "former",
    plugins: [verification],
    handler: () => ({
      body: {
        result: "kicked",
        at: null,
        method: null,
        retryAt: 1760000000000,
        invite: "https://example.net/fake",
      },
    }),
  });
  await screen.findByText("Eligible to return");
  expect(screen.queryByRole("link", { name: "Return to Discord" })).toBeNull();
});
