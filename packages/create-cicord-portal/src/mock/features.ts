import type { PortalPlugin } from "@cicord/client";

export const demoPlugins: PortalPlugin[] = [
  {
    bot: "community",
    instance: "events",
    plugin: "community-events",
    routes: ["GET /calendar"],
  },
  {
    bot: "community",
    instance: "suggestions",
    plugin: "suggestions",
    routes: ["GET /suggestions", "GET /suggestions/moderation", "POST /suggestions/:id/status"],
  },
  {
    bot: "community",
    instance: "birthdays",
    plugin: "birthdays",
    routes: ["GET /birthday", "PUT /birthday", "DELETE /birthday"],
  },
  {
    bot: "community",
    instance: "verify",
    plugin: "verification",
    routes: ["GET /challenge", "POST /challenge", "GET /queue", "POST /queue/:id"],
  },
  {
    bot: "community",
    instance: "giveaways",
    plugin: "giveaways",
    routes: ["GET /giveaways/:id", "GET /giveaways/:id/entries"],
  },
  {
    bot: "community",
    instance: "announcements",
    plugin: "announcements",
    routes: ["GET /scheduled", "POST /preview", "POST /compose"],
  },
];
export function mockFeatures() {
  const proposals = [
    {
      id: "18",
      status: "considered",
      score: 32,
      body: "Host a monthly game night for new members.",
      reason: "The events team is checking availability.",
    },
    {
      id: "19",
      status: "open",
      score: 16,
      body: "Add a channel for accessible controller setups.",
      reason: "",
    },
  ];
  const events = [
    {
      id: "winter-cup",
      name: "Winter Cup qualifying rounds",
      start: "2026-12-04T18:00:00Z",
      end: "2026-12-04T20:00:00Z",
    },
    { id: "game-night", name: "Friday community game night", start: "2026-12-11T19:00:00Z" },
  ];
  const entrants = [
    { userId: "1002", weight: 1 },
    { userId: "1005", weight: 2 },
    { userId: "1007", weight: 1 },
  ];
  const draw = {
    id: "7",
    prize: "Winter Cup jersey",
    state: "ended",
    closesAt: 1795363200000,
    commitment: "5f1c0e9a8b7d6c5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e",
    entrants: entrants.length,
    winners: ["1005"],
    seed: "c0ffee",
  };
  // Entrants come in pages of 1,000 with `entriesCursor`, absent on the last page.
  const entryPage = (search: URLSearchParams) => {
    const start = Number(search.get("cursor") ?? 0) || 0;
    const entries = entrants.slice(start, start + 1000);
    return start + 1000 < entrants.length
      ? { entries, entriesCursor: String(start + 1000) }
      : { entries };
  };
  let birthday: { day: number; month: number } | null = null,
    verified = false,
    queued = true;
  const scheduled: {
    id: number;
    at: string;
    channelId: string;
    title: string | null;
    preview: string;
  }[] = [];
  const json = (data: unknown, status = 200) =>
    new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } });
  return (
    method: string,
    path: string,
    body: unknown,
    search: URLSearchParams,
    role: "member" | "moderator" | "admin" | undefined,
  ): Response | undefined => {
    const match = /^\/api\/v1\/portal\/[^/]+\/plugins\/community\/([^/]+)(\/.*)$/.exec(path);
    if (!match) return undefined;
    const [, instance, route] = match,
      staff = role === "moderator" || role === "admin";
    const input = (body ?? {}) as Record<string, unknown>;
    const denied = () =>
      json({ error: { code: role ? "forbidden" : "unauthenticated" } }, role ? 403 : 401);
    if (instance === "events" && route === "/calendar") return json(events);
    if (instance === "suggestions") {
      if (route === "/suggestions" && method === "GET")
        return json({
          items: proposals.filter(
            (s) => !search.get("status") || s.status === search.get("status"),
          ),
        });
      if (!staff) return denied();
      if (route === "/suggestions/moderation") return json({ items: proposals });
      const p = proposals.find((s) => route === "/suggestions/" + s.id + "/status");
      if (
        p &&
        method === "POST" &&
        typeof input.status === "string" &&
        typeof input.reason === "string"
      ) {
        p.status = input.status;
        p.reason = input.reason;
        return json(p);
      }
    }
    if (instance === "giveaways") {
      if (route === "/giveaways/7") return json({ ...draw, ...entryPage(search) });
      if (route === "/giveaways/7/entries") return staff ? json(entryPage(search)) : denied();
    }
    if (!role) return denied();
    if (instance === "birthdays" && route === "/birthday") {
      if (method === "GET") return json({ birthday });
      if (method === "DELETE") {
        birthday = null;
        return json({ erased: 1 });
      }
      if (method === "PUT" && typeof input.day === "number" && typeof input.month === "number") {
        birthday = { day: input.day, month: input.month };
        return json({ saved: true });
      }
    }
    if (instance === "verify") {
      if (route === "/challenge" && method === "GET")
        return json(verified ? { verified: true } : { id: "preview-challenge", method: "button" });
      if (route === "/challenge" && method === "POST" && input.id === "preview-challenge") {
        verified = true;
        return json({ verified: true });
      }
      if (!staff) return denied();
      if (route === "/queue")
        return json({
          items: queued ? [{ userId: "1006", joined: 1795363200000 }] : [],
          cursor: null,
        });
      if (route === "/queue/1006" && method === "POST") {
        queued = false;
        return json({ verified: input.decision === "approve" });
      }
    }
    if (instance === "announcements") {
      if (!staff) return denied();
      if (route === "/scheduled") return json({ timeZone: "UTC", recurring: [], scheduled });
      if (route === "/preview")
        return json({ message: { embeds: [{ title: input.title, description: input.text }] } });
      if (route === "/compose" && typeof input.text === "string") {
        const id = scheduled.length + 1;
        scheduled.push({
          id,
          at: typeof input.at === "string" ? input.at : new Date().toISOString(),
          channelId: typeof input.channel === "string" ? input.channel : "",
          title: typeof input.title === "string" ? input.title : null,
          preview: input.text,
        });
        return json({ id });
      }
    }
    return json({ error: { code: "not_found" } }, 404);
  };
}
