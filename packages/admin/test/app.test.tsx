import { createCicordClient, type FetchLike } from "@cicord/client";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test } from "vite-plus/test";
import { App } from "../src/App.tsx";
import { watch401, type AdminConfig } from "../src/config.ts";
import { createMockRuntime } from "../src/mock/runtime.ts";
import { formatPath } from "../src/pages/Plugins.tsx";
import { groupChanges } from "../src/pages/Sync.tsx";
import { normalizeOption } from "../src/sim/CommandRunner.tsx";

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
  window.history.replaceState(null, "", import.meta.env.BASE_URL);
});
afterEach(cleanup);

type Intercept = (url: URL, init: RequestInit) => Response | undefined;

function setup(path = "/admin/", intercept?: Intercept) {
  const runtime = createMockRuntime({ latency: 0, persist: false });
  const fetch: FetchLike = async (input, init) => {
    const res = intercept?.(new URL(input, window.location.origin), init);
    return res ?? runtime.fetch(input, init);
  };
  const config: AdminConfig = {
    client: createCicordClient({ fetch: watch401(fetch) }),
    loginUrl: "/auth/login?redirect=/admin/",
    mock: true,
  };
  // Vitest runs with base "/", the app is served under BASE_URL.
  window.history.replaceState(null, "", path.replace(/^\/admin\//, import.meta.env.BASE_URL));
  render(<App config={config} />);
  return { runtime, user: userEvent.setup() };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

async function loginAsOwner(user: ReturnType<typeof userEvent.setup>) {
  const owner = await screen.findByRole("button", { name: /Owner/ }, { timeout: 3000 });
  await user.click(owner);
}

describe("login", () => {
  test("shows Discord login and the simulator's dev users", async () => {
    const { user } = setup();
    expect(
      await screen.findByRole("heading", { name: "Sign in to the operator console" }),
    ).toBeTruthy();
    expect(screen.getByRole("link", { name: "Continue with Discord" }).getAttribute("href")).toBe(
      "/auth/login?redirect=/admin/",
    );
    expect(await screen.findByRole("button", { name: /Alice/ })).toBeTruthy();
    await loginAsOwner(user);
    // "/" lands on the overview of the first server.
    expect(await screen.findByRole("heading", { level: 1, name: "Ma Commu" })).toBeTruthy();
    expect(window.location.pathname).toMatch(/\/servers\/ma-commu$/);
  });

  test("a member without staff role gets an explanation", async () => {
    const { user, runtime } = setup();
    // alice joins as a plain member, then logs in
    await screen.findByRole("button", { name: /Alice/ });
    await runtime.fetch("/api/v1/sim/servers/112233445566778899/members", {
      method: "POST",
      body: JSON.stringify({ userId: "101" }),
      headers: { "X-Cicord-Request": "1" },
    });
    await user.click(screen.getByRole("button", { name: /Alice/ }));
    expect(await screen.findByRole("heading", { name: "No server to manage" })).toBeTruthy();
  });
});

describe("overview & server", () => {
  test("overview shows revision, rejected revision, explained failure, bots and history", async () => {
    const { user } = setup();
    await loginAsOwner(user);
    expect(await screen.findByText("4f0c2d9", { selector: ".rev-id" })).toBeTruthy();
    expect(screen.getByText("The latest revision was rejected")).toBeTruthy();
    expect(screen.getByRole("heading", { level: 1, name: "Ma Commu" })).toBeTruthy();
    expect(screen.getAllByText("Out of sync").length).toBeGreaterThan(0);
    expect(screen.getByText("Gardien")).toBeTruthy();
    expect(await screen.findByText("bot reloaded (2 plugins)")).toBeTruthy();
    // The Discord failure is explained, grouped, and its raw JSON stays in the collapsed details.
    const alerts = screen.getAllByRole("alert");
    const failure = alerts.find((a) =>
      a.textContent?.includes("Announcement and stage channels need the Community feature"),
    )!;
    expect(failure).toBeTruthy();
    expect(within(failure).getByText(/Enable Community in Discord/)).toBeTruthy();
    expect(within(failure).getByText(/Failed 4 times/)).toBeTruthy();
    expect(await screen.findByText("Failed 4 times")).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/_errors|BASE_TYPE_CHOICES/);
    await user.click(within(failure).getByRole("button", { name: "Technical details" }));
    expect(within(failure).getByText(/BASE_TYPE_CHOICES/)).toBeTruthy();
    expect(within(failure).getByRole("button", { name: "Copy" })).toBeTruthy();
  });

  test("plan groups changes, shows field diffs and blocked items; sync applies it", async () => {
    const { user } = setup("/admin/servers/ma-commu/plan");
    await loginAsOwner(user);
    const roles = await screen.findByRole("region", { name: "Roles" });
    expect(within(roles).getByText("valorant")).toBeTruthy();
    expect(within(roles).getByText("#e91e63")).toBeTruthy();
    const blocked = screen.getByRole("region", { name: "Blocked" });
    expect(within(blocked).getByText(/deletion protection is on/)).toBeTruthy();
    expect(within(blocked).getByText(/syncPolicy.deletionProtection: false/)).toBeTruthy();
    // A refused role assignment reads as what it is, not as its key.
    expect(within(blocked).getByText(/give role "Admin" to @Léa/)).toBeTruthy();
    expect(within(blocked).queryByText("admin/112233445566778900")).toBeNull();
    const notes = screen.getByRole("region", { name: "Notes" });
    expect(within(notes).getByText(/is not a member of the server/)).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Sync now" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Apply the plan to Discord?" });
    await user.click(within(confirm).getByRole("button", { name: "Sync now" }));
    expect(await screen.findByText("Applied 4 changes")).toBeTruthy();
    expect(await screen.findByText("Nothing to change")).toBeTruthy();
  });

  test("resources and audit log", async () => {
    const { user } = setup("/admin/servers/ma-commu/resources");
    await loginAsOwner(user);
    expect(await screen.findByText("Notifs événements")).toBeTruthy();
    expect(screen.getAllByText("unmanaged").length).toBeGreaterThan(0);
    await user.click(screen.getByRole("radio", { name: "Unmanaged" }));
    expect(screen.queryByText("Notifs événements")).toBeNull();
    await user.click(screen.getByRole("link", { name: "Audit log" }));
    expect(await screen.findByText("moderator/onboarding")).toBeTruthy();
  });

  test("server settings: boost, community, Discord-only list and image upload", async () => {
    const uploads: { url: string; type: string | null }[] = [];
    const { user } = setup("/admin/servers/ma-commu/settings", (url, init) => {
      if (init.method !== "PUT" || !url.pathname.includes("/assets/")) return undefined;
      uploads.push({
        url: url.pathname + url.search,
        type: new Headers(init.headers).get("Content-Type"),
      });
      return json({
        mode: "pullRequest",
        revision: "",
        pullRequestUrl: "https://github.com/o/r/pull/9",
        path: "servers/assets/ma-commu-banner.png",
        value: "./assets/ma-commu-banner.png",
        format: "png",
        width: 300,
        height: 100,
        warnings: ["the image is 300×100: Discord recommends a 16:9 image of at least 960×540"],
      });
    });
    await loginAsOwner(user);
    expect(await screen.findByRole("heading", { name: "Server boost" })).toBeTruthy();
    expect(screen.getByText("Level 1 · 3 boosts")).toBeTruthy();
    expect(screen.getByText("4 more boosts to level 2")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Community Disabled" })).toBeTruthy();
    expect(screen.getByText("Discord UI only")).toBeTruthy();
    expect(screen.getByText("Server tag")).toBeTruthy();
    // The banner needs level 2: the warning explains the plan will block it.
    expect(screen.getByText("Banner requires boost level 2; the server is level 1.")).toBeTruthy();
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "b.png", {
      type: "image/png",
    });
    await user.upload(screen.getByLabelText("Upload a new banner"), file);
    expect(await screen.findByRole("link", { name: /Open pull request/ })).toBeTruthy();
    expect(screen.getByText(/Discord recommends a 16:9 image/)).toBeTruthy();
    expect(uploads[0]?.url).toMatch(/^\/api\/v1\/servers\/ma-commu\/assets\/banner/);
    expect(uploads[0]?.type).toBe("image/png");
  });

  test("plan helpers", () => {
    const groups = groupChanges([
      { action: "create", kind: "channel", key: "a", summary: "" },
      { action: "update", kind: "role", key: "b", summary: "" },
      { action: "delete", kind: "zzz", key: "c", summary: "" },
    ]);
    expect(groups.map(([k]) => k)).toEqual(["role", "channel", "zzz"]);
    expect(formatPath(["steps", 0, "options", 1, "role"])).toBe("steps[0].options[1].role");
  });
});

describe("plugin settings", () => {
  test("edit, review the diff and commit", async () => {
    const { user, runtime } = setup("/admin/servers/ma-commu/plugins/moderator/welcome");
    await loginAsOwner(user);
    const card = await screen.findByRole("region", { name: "Welcome settings" });
    const message = within(card).getByRole("textbox", { name: "Message" });
    await user.clear(message);
    await user.type(message, "Coucou {{member}");
    await user.click(within(card).getByRole("button", { name: "Review changes" }));
    const review = within(card).getByText("Review changes").closest(".review") as HTMLElement;
    expect(within(review).getByText("message")).toBeTruthy();
    expect(within(review).getByText("Coucou {member}")).toBeTruthy();
    await user.click(within(card).getByRole("button", { name: "Save to git" }));
    expect(
      await within(card).findByText("is now the active revision", { exact: false }),
    ).toBeTruthy();
    expect(runtime.state.instances[0]!.override["message"]).toBe("Coucou {member}");
    // The form reloads with the saved value and no pending change.
    await waitFor(() =>
      expect(within(card).queryByRole("button", { name: "Review changes" })).toBeNull(),
    );
  });

  test("client validation blocks review; 422 diagnostics land on fields", async () => {
    const { user } = setup("/admin/servers/ma-commu/plugins/moderator/onboarding", (url, init) => {
      if (init.method === "PUT" && url.pathname.endsWith("/onboarding/config"))
        return json(
          {
            error: {
              code: "invalid",
              message: "the configuration is invalid",
              diagnostics: [
                {
                  severity: "error",
                  path: "/spec/plugins/1/overrides/ma-commu/config/buttonLabel",
                  message: "[server ma-commu] label already used by another plugin",
                },
              ],
            },
          },
          422,
        );
      return undefined;
    });
    await loginAsOwner(user);
    const card = await screen.findByRole("region", { name: "Onboarding settings" });
    const title = within(card).getByRole("textbox", { name: "Title" });
    await user.clear(title);
    await user.click(within(card).getByRole("button", { name: "Review changes" }));
    const alerts = await within(card).findAllByRole("alert");
    expect(alerts.some((a) => a.textContent === "Required")).toBe(true);
    expect(within(card).queryByRole("button", { name: "Save to git" })).toBeNull();

    await user.type(title, "Salut");
    const label = within(card).getByRole("textbox", { name: "Button label" });
    await user.clear(label);
    await user.type(label, "Go");
    await user.click(within(card).getByRole("button", { name: "Review changes" }));
    await user.click(within(card).getByRole("button", { name: "Save to git" }));
    expect(await within(card).findByText("label already used by another plugin")).toBeTruthy();
    expect(within(card).getByText("The runtime rejected the configuration")).toBeTruthy();
  });

  test("409 conflict offers a reload; pull request mode links the PR", async () => {
    let mode: "conflict" | "pr" = "conflict";
    const { user } = setup("/admin/servers/ma-commu/plugins/moderator/welcome", (_url, init) => {
      if (init.method !== "PUT") return undefined;
      return mode === "conflict"
        ? json(
            { error: { code: "conflict", message: "the configuration changed since revision x." } },
            409,
          )
        : json({
            mode: "pullRequest",
            revision: "",
            pullRequestUrl: "https://github.com/o/r/pull/7",
          });
    });
    await loginAsOwner(user);
    const card = await screen.findByRole("region", { name: "Welcome settings" });
    await user.click(within(card).getByRole("switch", { name: "Dm onboarding" }));
    await user.click(within(card).getByRole("button", { name: "Review changes" }));
    await user.click(within(card).getByRole("button", { name: "Save to git" }));
    expect(
      await within(card).findByText("The configuration changed since you opened it"),
    ).toBeTruthy();
    expect(within(card).getByRole("button", { name: "Reload settings" })).toBeTruthy();

    mode = "pr";
    await user.click(within(card).getByRole("button", { name: "Review changes" }));
    await user.click(within(card).getByRole("button", { name: "Save to git" }));
    const link = await within(card).findByRole("link", { name: /Pull request opened/ });
    expect(link.getAttribute("href")).toBe("https://github.com/o/r/pull/7");
  });

  test("base scope edits spec.plugins[].config, not the effective values", async () => {
    let sent: { config: Record<string, unknown>; scope: string } | undefined;
    const { user, runtime } = setup(
      "/admin/servers/ma-commu/plugins/moderator/welcome",
      (_url, init) => {
        if (init.method === "PUT") sent = JSON.parse(init.body as string) as typeof sent;
        return undefined;
      },
    );
    await loginAsOwner(user);
    const card = await screen.findByRole("region", { name: "Welcome settings" });
    expect(within(card).getByText("message", { selector: ".editor-note code" })).toBeTruthy();
    await user.click(within(card).getByRole("radio", { name: "Base config" }));
    const message = within(card).getByRole<HTMLTextAreaElement>("textbox", { name: "Message" });
    expect(message.value).toBe("Bienvenue {member} sur {server} ! 👋");
    expect(within(card).getByText(/the override still wins for/)).toBeTruthy();
    await user.click(within(card).getByRole("switch", { name: "Dm onboarding" }));
    // Switching scope is blocked while there are unsaved changes.
    expect(
      within(card).getByRole<HTMLButtonElement>("radio", { name: "This server's override" })
        .disabled,
    ).toBe(true);
    await user.click(within(card).getByRole("button", { name: "Review changes" }));
    await user.click(within(card).getByRole("button", { name: "Save to git" }));
    await within(card).findByText("is now the active revision", { exact: false });
    expect(sent?.scope).toBe("base");
    expect(sent?.config["message"]).toBe("Bienvenue {member} sur {server} ! 👋");
    expect(sent?.config["dmOnboarding"]).toBe(false);
    expect(runtime.state.instances[0]!.override["message"]).toMatch(/^Salut/);
  });

  test("moderators see admin-only fields locked and 403 is explained", async () => {
    const { user } = setup("/admin/servers/ma-commu/plugins/moderator/welcome", (_url, init) =>
      init.method === "PUT"
        ? json({ error: { code: "forbidden", message: "you may not change: channel" } }, 403)
        : undefined,
    );
    await user.click(await screen.findByRole("button", { name: /Mina/ }));
    const card = await screen.findByRole("region", { name: "Welcome settings" });
    expect(
      within(card).getByRole<HTMLButtonElement>("combobox", { name: "Channel" }).disabled,
    ).toBe(true);
    const message = within(card).getByRole("textbox", { name: "Message" });
    await user.type(message, "!");
    await user.click(within(card).getByRole("button", { name: "Review changes" }));
    await user.click(within(card).getByRole("button", { name: "Save to git" }));
    expect(await within(card).findByText("Your role cannot save this change")).toBeTruthy();
  });

  test("a 401 on any call returns to the login page", async () => {
    let expired = false;
    const { user } = setup("/admin/servers/ma-commu/plugins", (url) =>
      expired && url.pathname.startsWith("/api/v1/")
        ? json({ error: { code: "unauthenticated", message: "login required" } }, 401)
        : undefined,
    );
    await loginAsOwner(user);
    await screen.findByRole("link", { name: /Welcome/ });
    expired = true;
    await user.click(screen.getByRole("link", { name: "Overview" }));
    expect(
      await screen.findByRole("heading", { name: "Sign in to the operator console" }),
    ).toBeTruthy();
  });
});

describe("plugins table and instance page", () => {
  test("rows link to the instance page; overview, breadcrumb and back link", async () => {
    const { user } = setup("/admin/servers/ma-commu/plugins");
    await loginAsOwner(user);
    const table = await screen.findByRole("table", { name: "Plugin instances" });
    const welcome = await within(table).findByRole("link", { name: /Welcome/ });
    expect(welcome.getAttribute("href")).toMatch(
      /\/servers\/ma-commu\/plugins\/moderator\/welcome$/,
    );
    const row = welcome.closest("tr") as HTMLElement;
    expect(within(row).getByText("welcome@0.1.0")).toBeTruthy();
    expect(within(row).getByText("Official")).toBeTruthy();
    expect(within(row).getByText("2")).toBeTruthy();
    const onboarding = within(table)
      .getByRole("link", { name: /Onboarding/ })
      .closest("tr")!;
    expect(within(onboarding as HTMLElement).getByText("Local")).toBeTruthy();
    expect(
      within(table)
        .getByRole("columnheader", { name: /Plugin/ })
        .getAttribute("aria-sort"),
    ).toBe("ascending");

    // A click anywhere on the row opens the instance's page.
    await user.click(within(row).getByText("welcome", { selector: "code" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Welcome" })).toBeTruthy();
    expect(window.location.pathname).toMatch(/\/plugins\/moderator\/welcome$/);
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: "Plugins" })).toBeTruthy();
    expect(within(crumbs).getByText("Welcome")).toBeTruthy();
    // Settings first, then the overview in the sub-navigation.
    expect(screen.getByRole("region", { name: "Welcome settings" })).toBeTruthy();
    const sections = screen.getByRole("navigation", { name: "Welcome sections" });
    await user.click(within(sections).getByRole("link", { name: "Overview" }));
    expect(window.location.search).toBe("?section=overview");
    expect(await screen.findByText("Send DMs")).toBeTruthy();
    expect(screen.getByText("welcome-preview")).toBeTruthy();
    expect(screen.getByText("ServerMembers")).toBeTruthy();

    await user.click(screen.getByRole("link", { name: "All plugins" }));
    expect(await screen.findByRole("table", { name: "Plugin instances" })).toBeTruthy();
  });

  test("an unknown instance shows a not-found state", async () => {
    const { user } = setup("/admin/servers/ma-commu/plugins/moderator/nope");
    await loginAsOwner(user);
    expect(await screen.findByText("This plugin instance does not exist")).toBeTruthy();
    expect(screen.getByText(/no instance moderator\/nope on this server/)).toBeTruthy();
  });

  test("uninstall: confirm, result on the table, the instance is gone", async () => {
    const { user, runtime } = setup(
      "/admin/servers/ma-commu/plugins/moderator/welcome?section=danger",
    );
    await loginAsOwner(user);
    await user.click(await screen.findByRole("button", { name: "Uninstall…" }));
    const dialog = await screen.findByRole("alertdialog", {
      name: "Uninstall Welcome from Ma Commu?",
    });
    // What happens is spelled out before confirming.
    expect(within(dialog).getByText(/only Ma Commu is removed from it/)).toBeTruthy();
    expect(within(dialog).getByText(/its cicord.lock entry/)).toBeTruthy();
    expect(within(dialog).getByText(/stored data \(key-value storage\) is kept/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "Uninstall" }));

    // Back on the table, with the result.
    const table = await screen.findByRole("table", { name: "Plugin instances" });
    expect(window.location.pathname).toMatch(/\/servers\/ma-commu\/plugins$/);
    const notice = screen
      .getByText("Welcome was uninstalled from Ma Commu", { selector: "[role=status] p" })
      .closest("[role=status]")!;
    expect(within(notice as HTMLElement).getByText("plugins/welcome/")).toBeTruthy();
    expect(within(notice as HTMLElement).getByText("is now the active revision")).toBeTruthy();
    await waitFor(() => expect(within(table).queryByRole("link", { name: /Welcome/ })).toBeNull());
    expect(runtime.state.instances.map((i) => i.instance)).toEqual(["onboarding"]);
    expect(runtime.state.audit[0]).toMatchObject({
      action: "plugin.uninstall",
      target: "moderator/welcome",
    });
  });

  test("uninstall errors: 422 dependency, 409 conflict, 403", async () => {
    let mode: "mock" | "conflict" | "forbidden" = "mock";
    const { user } = setup(
      "/admin/servers/ma-commu/plugins/moderator/onboarding?section=danger",
      (_url, init) =>
        init.method !== "DELETE" || mode === "mock"
          ? undefined
          : mode === "conflict"
            ? json(
                {
                  error: {
                    code: "conflict",
                    message: "The repository changed meanwhile.",
                    hint: "Try again.",
                  },
                },
                409,
              )
            : json(
                { error: { code: "forbidden", message: "write-back is disabled in cicord.yaml" } },
                403,
              ),
    );
    await loginAsOwner(user);
    const confirm = async () => {
      await user.click(await screen.findByRole("button", { name: "Uninstall…" }));
      const dialog = await screen.findByRole("alertdialog");
      await user.click(within(dialog).getByRole("button", { name: "Uninstall" }));
    };
    // welcome's DM links to the onboarding portal: the runtime refuses.
    await confirm();
    expect(await screen.findByText("Something depends on this plugin")).toBeTruthy();
    const problems = screen.getByRole("list", { name: "Problems" });
    expect(within(problems).getByText(/dmOnboarding links to the onboarding portal/)).toBeTruthy();
    expect(screen.getByText("Another plugin or setting may depend on it.")).toBeTruthy();
    expect(window.location.pathname).toMatch(/\/onboarding$/);

    mode = "conflict";
    await confirm();
    expect(
      await screen.findByText("The repository changed meanwhile", { selector: "p" }),
    ).toBeTruthy();
    mode = "forbidden";
    await confirm();
    expect(await screen.findByText("You cannot uninstall this plugin")).toBeTruthy();
    expect(screen.getByText("write-back is disabled in cicord.yaml")).toBeTruthy();
  });

  test("moderators have no danger zone", async () => {
    const { user } = setup("/admin/servers/ma-commu/plugins/moderator/welcome?section=danger");
    await user.click(await screen.findByRole("button", { name: /Mina/ }));
    // Falls back to the settings.
    expect(await screen.findByRole("region", { name: "Welcome settings" })).toBeTruthy();
    const sections = screen.getByRole("navigation", { name: "Welcome sections" });
    expect(within(sections).queryByRole("link", { name: "Danger zone" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Uninstall…" })).toBeNull();
  });
});

describe("simulator", () => {
  test("join a user: welcome message, role, DM with the portal link; run a command; click a button", async () => {
    const { user } = setup("/admin/simulator");
    await loginAsOwner(user);
    expect(await screen.findByRole("heading", { name: "général" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "alice joins the server" }));
    expect(await screen.findByText(/bienvenue sur Ma Commu/)).toBeTruthy();
    const members = screen.getByRole("complementary", { name: "Members" });
    expect(await within(members).findByText("Alice")).toBeTruthy();

    await user.click(within(members).getByRole("button", { name: "DMs of alice" }));
    const link = await screen.findByRole("link", { name: /Commencer/ });
    expect(link.getAttribute("href")).toMatch(/\/onboarding\?cicord_token=/);
    expect(link.getAttribute("target")).toBe("_blank");

    // Slash command as the owner
    await user.click(screen.getByRole("button", { name: /général/ }));
    await user.click(screen.getByRole("tab", { name: "Slash command" }));
    await user.selectOptions(await screen.findByLabelText("Command"), "moderator/welcome-preview");
    await user.click(screen.getByRole("button", { name: "Run /welcome-preview" }));
    const result = await screen.findByRole("status", { name: "Interaction result" });
    expect(within(result).getByText("replied")).toBeTruthy();
    expect(within(result).getByText("ephemeral")).toBeTruthy();

    // Buttons call the component endpoint
    await user.click(screen.getByRole("button", { name: /règlement/ }));
    await user.click(await screen.findByRole("button", { name: /En savoir plus/ }));
    await waitFor(() => expect(screen.getByText("Clicked “En savoir plus”")).toBeTruthy());
    expect(screen.getByText("1 followup")).toBeTruthy();
  });

  test("send a message as a member and create a user", async () => {
    const { user } = setup("/admin/simulator");
    await loginAsOwner(user);
    const input = await screen.findByRole("textbox", { name: "Message" });
    await user.type(input, "Salut **tout le monde**{Enter}");
    expect(await screen.findByText("tout le monde")).toBeTruthy();
    await user.type(screen.getByLabelText("New user name"), "dave");
    await user.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByRole("button", { name: "dave joins the server" })).toBeTruthy();
  });

  test("command options accept string or numeric types", () => {
    expect(normalizeOption({ name: "member", type: 6, required: true })).toMatchObject({
      name: "member",
      type: "user",
      required: true,
    });
    expect(
      normalizeOption({ name: "n", type: "INTEGER", choices: [{ name: "One", value: 1 }] }),
    ).toMatchObject({ type: "integer", choices: [{ name: "One", value: 1 }] });
    expect(normalizeOption("nope")).toBeNull();
  });
});
