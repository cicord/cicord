import { createCodiClient } from "@codi/client";
import { cleanup, render, screen } from "@testing-library/react";
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
    client: createCodiClient({ fetch: runtime.fetch }),
    login: () => window.history.replaceState(null, "", runtime.signedLink("/onboarding")),
    mock: true,
  };
}

test("home shows the server and a login button when logged out", async () => {
  render(<App config={mockConfig()} />);
  expect(await screen.findByRole("heading", { level: 1, name: "Ma Commu" })).toBeTruthy();
  expect(await screen.findByRole("button", { name: "Se connecter avec Discord" })).toBeTruthy();
});

test("a signed bot link logs in and the onboarding flow assigns roles", async () => {
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/onboarding?codi_token=signed");
  render(<App config={mockConfig()} />);

  expect(
    await screen.findByRole("heading", { name: "À quoi tu joues ?" }, { timeout: 4000 }),
  ).toBeTruthy();
  expect(window.location.search).toBe("");

  await user.click(screen.getByRole("checkbox", { name: "Minecraft" }));
  await user.click(screen.getByRole("button", { name: "Suivant" }));
  expect(await screen.findByRole("heading", { name: "Tu joues surtout sur…" })).toBeTruthy();
  expect((screen.getByRole("button", { name: "Suivant" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  await user.click(screen.getByRole("radio", { name: "PC" }));
  await user.click(screen.getByRole("button", { name: "Suivant" }));
  expect(
    await screen.findByRole("heading", { name: "On te prévient pour les événements ?" }),
  ).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Valider mes rôles" }));

  expect(
    await screen.findByRole("heading", { name: "Tes rôles sont à jour" }, { timeout: 4000 }),
  ).toBeTruthy();
  expect(screen.getByText("2 nouveaux rôles.")).toBeTruthy();
  const roles = screen.getAllByRole("listitem").map((li) => li.textContent);
  expect(roles).toEqual(["Membre", "Minecraft", "PC"]);
}, 15000);

test("unknown pages render the not-found page", async () => {
  window.history.replaceState(null, "", "/nope");
  render(<App config={mockConfig()} />);
  expect(await screen.findByRole("heading", { name: "Page introuvable" })).toBeTruthy();
});
