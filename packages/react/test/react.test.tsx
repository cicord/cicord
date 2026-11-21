import { createCodiClient, type FetchLike, type OnboardingSteps } from "@codi/client";
import { act, cleanup, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode, type ReactNode } from "react";
import { afterEach, describe, expect, test, vi } from "vite-plus/test";
import {
  CodiProvider,
  OnboardingFlow,
  useLinkTokenLogin,
  useMe,
  useMember,
  usePluginMutation,
  usePluginQuery,
} from "../src/index.ts";

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const me = {
  user: { id: "1", username: "alice", displayName: "Alice", avatarUrl: "" },
  servers: [{ key: "ma-commu", name: "Ma Commu", role: "member" }],
};

const portalServer = {
  key: "ma-commu",
  name: "Ma Commu",
  iconUrl: "",
  theme: { primary: "#5865f2", mode: "dark" },
  plugins: [
    {
      bot: "moderator",
      instance: "onboarding",
      plugin: "onboarding",
      routes: ["GET /steps", "POST /complete"],
    },
  ],
};

const steps: OnboardingSteps = {
  title: "Bienvenue",
  intro: "",
  completed: false,
  completedAt: null,
  completedRole: null,
  steps: [
    {
      id: "jeux",
      title: "Jeux",
      description: "",
      multiple: true,
      required: false,
      selected: ["valorant"],
      options: [
        {
          role: "minecraft",
          label: "Minecraft",
          emoji: "⛏️",
          description: "",
          name: "Minecraft",
          color: "#5b8731",
        },
        {
          role: "valorant",
          label: "Valorant",
          emoji: "",
          description: "",
          name: "Valorant",
          color: "#ff4655",
        },
      ],
    },
    {
      id: "plateforme",
      title: "Plateforme",
      description: "",
      multiple: false,
      required: true,
      selected: [],
      options: [
        { role: "pc", label: "PC", emoji: "", description: "", name: "PC", color: "" },
        {
          role: "console",
          label: "Console",
          emoji: "",
          description: "",
          name: "Console",
          color: "",
        },
      ],
    },
  ],
};

type Handler = (method: string, path: string, body: unknown) => Response | undefined;

function setup(handler: Handler) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  const fetch: FetchLike = async (url, init) => {
    const path = url.replace(/^https?:\/\/[^/]+/, "");
    const body = typeof init.body === "string" ? JSON.parse(init.body) : undefined;
    const method = init.method ?? "GET";
    calls.push({ method, path, body });
    return (
      handler(method, path, body) ?? json({ error: { code: "not_found", message: path } }, 404)
    );
  };
  const client = createCodiClient({ fetch });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <StrictMode>
      <CodiProvider client={client} server="ma-commu">
        {children}
      </CodiProvider>
    </StrictMode>
  );
  return { client, calls, wrapper };
}

describe("hooks", () => {
  test("useMe resolves null when logged out, and the user otherwise", async () => {
    let loggedIn = false;
    const { wrapper, client } = setup((_m, path) =>
      path === "/api/v1/me"
        ? loggedIn
          ? json(me)
          : json({ error: { code: "unauthenticated", message: "no session" } }, 401)
        : undefined,
    );
    const { result } = renderHook(() => useMe(), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("success"));
    expect(result.current.data).toBeNull();
    loggedIn = true;
    await act(() => result.current.refetch());
    expect(result.current.data).toEqual(me);
    expect(client).toBeDefined();
  });

  test("useMember shares one request between components", async () => {
    const member = { user: me.user, member: { nick: "", joinedAt: "", roles: [] }, role: "member" };
    const { wrapper, calls } = setup((_m, path) =>
      path === "/api/v1/portal/ma-commu/me" ? json(member) : undefined,
    );
    const { result } = renderHook(() => [useMember(), useMember()] as const, { wrapper });
    await waitFor(() => expect(result.current[0].data).toEqual(member));
    expect(result.current[1].data).toEqual(member);
    expect(calls.filter((c) => c.path.endsWith("/me"))).toHaveLength(1);
  });

  test("useLinkTokenLogin exchanges the token once (StrictMode) and cleans the URL", async () => {
    window.history.replaceState(null, "", "/onboarding?codi_token=signed.token&x=1");
    const { wrapper, calls } = setup((method, path) =>
      method === "POST" && path === "/auth/link"
        ? json(me)
        : path === "/api/v1/me"
          ? json(me)
          : undefined,
    );
    const { result } = renderHook(() => ({ login: useLinkTokenLogin(), me: useMe() }), { wrapper });
    expect(result.current.login.status).toBe("pending");
    await waitFor(() => expect(result.current.login).toEqual({ status: "success", me }));
    expect(window.location.pathname + window.location.search).toBe("/onboarding?x=1");
    expect(calls.filter((c) => c.path === "/auth/link")).toEqual([
      { method: "POST", path: "/auth/link", body: { token: "signed.token" } },
    ]);
    expect(result.current.me.data).toEqual(me);
  });

  test("usePluginQuery + usePluginMutation", async () => {
    let count = 0;
    const { wrapper } = setup((method, path, body) => {
      if (path === "/api/v1/portal/ma-commu/plugins/bot/counter/count") return json({ count });
      if (method === "POST" && path === "/api/v1/portal/ma-commu/plugins/bot/counter/add") {
        count += (body as { by: number }).by;
        return json({ count });
      }
      return undefined;
    });
    const ref = { bot: "bot", instance: "counter" };
    const { result } = renderHook(
      () => ({
        q: usePluginQuery<{ count: number }>(ref, "/count"),
        m: usePluginMutation<{ by: number }, { count: number }>(ref, "/add"),
      }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.q.data).toEqual({ count: 0 }));
    await act(async () => {
      await result.current.m.mutateAsync({ by: 2 });
    });
    expect(result.current.m.data).toEqual({ count: 2 });
    await waitFor(() => expect(result.current.q.data).toEqual({ count: 2 }));
  });
});

function Flow() {
  return (
    <OnboardingFlow>
      {(flow) => {
        if (flow.status === "complete")
          return <p>Terminé: {flow.result?.roles.map((r) => r.key).join(",")}</p>;
        if (flow.status === "unavailable") return <p>Pas d'onboarding</p>;
        if (flow.status === "unauthenticated") return <p>Connecte-toi</p>;
        if (flow.status !== "ready" && flow.status !== "submitting") return <p>Chargement</p>;
        return (
          <section>
            <h2>{flow.step?.title}</h2>
            <p>
              Étape {flow.index + 1}/{flow.total}
            </p>
            {flow.step?.options.map((o) => (
              <button
                key={o.role}
                type="button"
                aria-pressed={flow.isSelected(o.role)}
                onClick={() => flow.toggle(o.role)}
              >
                {o.label}
              </button>
            ))}
            {flow.step && flow.stepErrors[flow.step.id] ? (
              <p role="alert">{flow.stepErrors[flow.step.id]}</p>
            ) : null}
            <button type="button" onClick={flow.back} disabled={flow.isFirst}>
              Retour
            </button>
            <button type="button" onClick={flow.next} disabled={!flow.canContinue}>
              {flow.isLast ? "Terminer" : "Suivant"}
            </button>
          </section>
        );
      }}
    </OnboardingFlow>
  );
}

describe("<OnboardingFlow>", () => {
  function onboardingRuntime(onComplete: (body: unknown) => Response) {
    return setup((method, path, body) => {
      if (path === "/api/v1/portal/ma-commu") return json(portalServer);
      if (path === "/api/v1/portal/ma-commu/plugins/moderator/onboarding/steps") return json(steps);
      if (
        method === "POST" &&
        path === "/api/v1/portal/ma-commu/plugins/moderator/onboarding/complete"
      )
        return onComplete(body);
      return undefined;
    });
  }

  test("walks the steps, enforces required steps and completes", async () => {
    const user = userEvent.setup();
    const onComplete = vi.fn(() =>
      json({
        completed: true,
        completedAt: "2026-11-21T10:00:00Z",
        roles: [
          { key: "minecraft", id: "1", name: "Minecraft", color: "" },
          { key: "valorant", id: "2", name: "Valorant", color: "" },
          { key: "console", id: "3", name: "Console", color: "" },
        ],
        added: ["minecraft", "console"],
        removed: [],
        message: "ok",
      }),
    );
    const { wrapper } = onboardingRuntime(onComplete);
    render(<Flow />, { wrapper });

    expect(await screen.findByRole("heading", { name: "Jeux" })).toBeTruthy();
    // Pre-selected from the member's roles.
    expect(screen.getByRole("button", { name: "Valorant" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Minecraft" }));
    expect(screen.getByRole("button", { name: "Minecraft" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Suivant" }));

    expect(screen.getByRole("heading", { name: "Plateforme" })).toBeTruthy();
    const finish = screen.getByRole("button", { name: "Terminer" }) as HTMLButtonElement;
    expect(finish.disabled).toBe(true); // required step
    await user.click(screen.getByRole("button", { name: "PC" }));
    await user.click(screen.getByRole("button", { name: "Console" })); // single choice: replaces PC
    expect(screen.getByRole("button", { name: "PC" }).getAttribute("aria-pressed")).toBe("false");
    await user.click(finish);

    expect(await screen.findByText("Terminé: minecraft,valorant,console")).toBeTruthy();
    expect(onComplete).toHaveBeenCalledWith({
      answers: { jeux: ["valorant", "minecraft"], plateforme: ["console"] },
    });
  });

  test("maps 422 diagnostics to the step", async () => {
    const user = userEvent.setup();
    const { wrapper } = onboardingRuntime(() =>
      json(
        {
          error: {
            code: "invalid",
            message: "bad",
            diagnostics: [
              { severity: "error", path: "/answers/jeux", message: "rôle indisponible" },
            ],
          },
        },
        422,
      ),
    );
    render(<Flow />, { wrapper });
    await user.click(await screen.findByRole("button", { name: "Suivant" }));
    await user.click(screen.getByRole("button", { name: "PC" }));
    await user.click(screen.getByRole("button", { name: "Terminer" }));
    expect((await screen.findByRole("alert")).textContent).toBe("rôle indisponible");
    expect(screen.getByRole("heading", { name: "Jeux" })).toBeTruthy();
  });

  test("unavailable without an onboarding plugin, unauthenticated on 401", async () => {
    const a = setup((_m, path) =>
      path === "/api/v1/portal/ma-commu" ? json({ ...portalServer, plugins: [] }) : undefined,
    );
    render(<Flow />, { wrapper: a.wrapper });
    expect(await screen.findByText("Pas d'onboarding")).toBeTruthy();
    cleanup();

    const b = setup((_m, path) =>
      path === "/api/v1/portal/ma-commu"
        ? json(portalServer)
        : json({ error: { code: "unauthenticated", message: "login" } }, 401),
    );
    render(<Flow />, { wrapper: b.wrapper });
    expect(await screen.findByText("Connecte-toi")).toBeTruthy();
  });
});
