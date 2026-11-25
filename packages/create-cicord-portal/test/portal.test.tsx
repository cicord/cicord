import { createCicordClient } from "@cicord/client";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import type { PortalConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

function mockConfig(): PortalConfig {
  const runtime = createMockRuntime();
  return {
    client: createCicordClient({ fetch: runtime.fetch }),
    login: () => window.history.replaceState(null, "", runtime.signedLink("/onboarding")),
    mock: true,
  };
}

test("home shows the no-code customization and a login button when logged out", async () => {
  render(<App config={mockConfig()} />);
  expect(
    await screen.findByRole("heading", { level: 1, name: "Welcome to My Community" }),
  ).toBeTruthy();
  expect(
    screen.getByText("The community for game nights, tournaments and live streams."),
  ).toBeTruthy();
  // description (Markdown)
  expect(screen.getByRole("heading", { name: "What you'll find here" })).toBeTruthy();
  expect(screen.getByText("together").tagName).toBe("STRONG");
  // links
  const links = within(screen.getByRole("list", { name: "Links" })).getAllByRole("link");
  expect(links.map((a) => a.textContent)).toEqual([
    "Website",
    "YouTube channel",
    "Twitch streams",
    "Rules",
  ]);
  expect(links[0]?.getAttribute("rel")).toBe("noopener noreferrer");
  expect(await screen.findByRole("button", { name: "Sign in with Discord" })).toBeTruthy();
});

test("a signed bot link logs in and the onboarding flow assigns roles", async () => {
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/onboarding?cicord_token=signed");
  render(<App config={mockConfig()} />);

  expect(
    await screen.findByRole("heading", { name: "What do you play?" }, { timeout: 4000 }),
  ).toBeTruthy();
  expect(window.location.search).toBe("");

  await user.click(screen.getByRole("checkbox", { name: "Minecraft" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(await screen.findByRole("heading", { name: "Where do you mostly play?" })).toBeTruthy();
  expect(screen.getByRole<HTMLButtonElement>("button", { name: "Next" }).disabled).toBe(true);
  await user.click(screen.getByRole("radio", { name: "PC" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(
    await screen.findByRole("heading", { name: "Should we ping you for events?" }),
  ).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Save my roles" }));

  expect(
    await screen.findByRole("heading", { name: "Your roles are up to date" }, { timeout: 4000 }),
  ).toBeTruthy();
  expect(screen.getByText("2 new roles.")).toBeTruthy();
  const roles = screen.getAllByRole("listitem").map((li) => li.textContent);
  expect(roles).toEqual(["Member", "Minecraft", "PC"]);
}, 15000);

test("unknown pages render the not-found page", async () => {
  window.history.replaceState(null, "", "/nope");
  render(<App config={mockConfig()} />);
  expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
});

test("public leaderboard of the levels plugin", async () => {
  window.history.replaceState(null, "", "/leaderboard");
  render(<App config={mockConfig()} />);
  expect(await screen.findByRole("heading", { level: 1, name: "Leaderboard" })).toBeTruthy();
  const podium = await screen.findByRole("list", { name: "Top 3" }, { timeout: 4000 });
  expect(within(podium).getByText("Alex")).toBeTruthy();
  expect(screen.getByText("Jamie")).toBeTruthy();
});

test("tickets: forbidden for members, listed for moderators", async () => {
  window.history.replaceState(null, "", "/tickets?cicord_token=signed");
  const { unmount } = render(<App config={mockConfig()} />);
  expect(
    await screen.findByText("For the moderation team only.", {}, { timeout: 4000 }),
  ).toBeTruthy();
  unmount();

  sessionStorage.clear();
  window.history.replaceState(null, "", "/tickets?role=moderator&cicord_token=signed");
  render(<App config={mockConfig()} />);
  expect(
    await screen.findByText("Can't access the Valorant channel", {}, { timeout: 4000 }),
  ).toBeTruthy();
  expect(screen.queryByText("Minecraft tournament idea")).toBeNull(); // open by default
  expect(screen.getByRole("link", { name: "Tickets" })).toBeTruthy();
}, 15000);

test("developers page maps the portal endpoints and plugin routes", async () => {
  window.history.replaceState(null, "", "/developers");
  render(<App config={mockConfig()} />);
  expect(await screen.findByRole("heading", { level: 1, name: "Developers" })).toBeTruthy();
  expect(screen.getByText("/api/v1/portal/my-community/banner")).toBeTruthy();
  expect(screen.getByText("cookie")).toBeTruthy();
  expect(screen.getByRole("heading", { level: 3, name: /levels/ })).toBeTruthy();
});

test("pages toggles and locale come from the server config", async () => {
  const runtime = createMockRuntime();
  const patch = (s: Record<string, unknown>) => ({
    ...s,
    locale: "fr",
    pages: { onboarding: true, profile: false, leaderboard: false, tickets: true },
  });
  const fetch: typeof runtime.fetch = async (input, init) => {
    const res = await runtime.fetch(input, init);
    if (!input.includes("/portal/") || input.includes("/plugins/") || input.endsWith("/me"))
      return res;
    const body = (await res.json()) as { items?: Record<string, unknown>[] } & Record<
      string,
      unknown
    >;
    const out = body.items ? { items: body.items.map(patch) } : patch(body);
    return new Response(JSON.stringify(out), { headers: { "Content-Type": "application/json" } });
  };
  window.history.replaceState(null, "", "/me");
  render(
    <App config={{ client: createCicordClient({ fetch }), login: () => undefined, mock: true }} />,
  );
  expect(await screen.findByRole("heading", { name: "Page introuvable" })).toBeTruthy();
  expect(screen.queryByRole("link", { name: "Classement" })).toBeNull();
  expect(document.documentElement.lang).toBe("fr");
});

test("auth mode follows the API origin", async () => {
  const { authModeFor } = await import("../src/config.ts");
  expect(authModeFor(undefined, undefined)).toBe("cookie");
  expect(authModeFor("https://cicord.example", undefined)).toBe("token");
  expect(authModeFor(window.location.origin, undefined)).toBe("cookie");
  expect(authModeFor("https://cicord.example", "cookie")).toBe("cookie");
});

test("a signed link to /leaderboard (levels /rank) signs in on that page", async () => {
  window.history.replaceState(null, "", "/leaderboard?cicord_token=signed");
  render(<App config={mockConfig()} />);
  expect(await screen.findByRole("heading", { level: 1, name: "Leaderboard" })).toBeTruthy();
  expect(await screen.findByRole("link", { name: /Alex/ }, { timeout: 4000 })).toBeTruthy(); // user chip
  expect(window.location.search).toBe("");
});
