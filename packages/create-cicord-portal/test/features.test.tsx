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
