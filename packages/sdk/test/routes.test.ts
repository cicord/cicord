import { describe, expect, expectTypeOf, test } from "vite-plus/test";
import { definePlugin, defineRoutes } from "../src/index.ts";
import { callRoute, createTestContext } from "../src/testing.ts";

interface Poll {
  id: string;
  votes: number;
}

// A route contract (plugin.yaml spec.routes.types).
interface Routes {
  "GET /polls/:id": { query: { full?: boolean }; response: Poll };
  "POST /polls/:id/votes/:choice": { body: { weight: number }; response: { votes: number } };
  "DELETE /polls/:id": {};
}

const plugin = definePlugin({
  routes: defineRoutes<Routes>({
    "GET /polls/:id": {
      access: "public",
      handler(_ctx, request) {
        expectTypeOf(request.params).toEqualTypeOf<Readonly<{ id: string }>>();
        expectTypeOf(request.query.full).toEqualTypeOf<string | undefined>();
        if (request.params.id === "none") return { status: 404 };
        return { id: request.params.id, votes: request.query.full ? 2 : 1 };
      },
    },
    "POST /polls/:id/votes/:choice": {
      access: "member",
      handler(_ctx, request) {
        expectTypeOf(request.params.choice).toBeString();
        expectTypeOf(request.body).toEqualTypeOf<{ weight: number } | undefined>();
        return { votes: request.body?.weight ?? 0 };
      },
    },
    "DELETE /polls/:id": { access: "admin", handler() {} },
  }),
});

describe("defineRoutes", () => {
  test("is the identity and the routes run like untyped ones", async () => {
    const t = createTestContext({
      members: [
        { id: "1", username: "alice" },
        { id: "2", username: "bob" },
      ],
      access: { admins: { users: ["2"] } },
    });
    expect(await callRoute(plugin, t, "GET /polls/42", { query: { full: "1" } })).toMatchObject({
      status: 200,
      body: { id: "42", votes: 2 },
    });
    expect((await callRoute(plugin, t, "GET /polls/none")).status).toBe(404);
    const vote = await callRoute(plugin, t, "POST /polls/1/votes/yes", {
      viewer: "1",
      body: { weight: 3 },
    });
    expect(vote.body).toEqual({ votes: 3 });
    // An admin route refuses a member, like internal/api/portal.go.
    expect((await callRoute(plugin, t, "DELETE /polls/1", { viewer: "1" })).status).toBe(403);
    expect((await callRoute(plugin, t, "DELETE /polls/1", { viewer: "2" })).status).toBe(204);
  });

  test("handlers that do not follow the contract do not compile", () => {
    const typeErrors = () =>
      defineRoutes<Routes>({
        "GET /polls/:id": {
          access: "public",
          // @ts-expect-error the response misses `votes`
          handler: (_ctx, request) => ({ id: request.params.id }),
        },
        "POST /polls/:id/votes/:choice": {
          access: "member",
          // @ts-expect-error `key` is not a parameter of the route
          handler: (_ctx, request) => ({ votes: request.params.key.length }),
        },
        "DELETE /polls/:id": { access: "admin", handler() {} },
      });
    const missing = () =>
      // @ts-expect-error a route of the contract is missing
      defineRoutes<Routes>({ "DELETE /polls/:id": { access: "admin", handler() {} } });
    expect([typeErrors, missing]).toHaveLength(2);
  });
});

// internal/api/portal.go:302-457 and internal/plugin/instance.go:1380-1414.
describe("callRoute behaves like the portal API", () => {
  const routes = definePlugin({
    routes: {
      "GET /headers": {
        access: "public",
        handler: (_ctx, r) => ({
          status: 200,
          body: r.query["empty"] ? undefined : { ok: true },
          headers: {
            "Set-Cookie": "a=1",
            Location: "https://evil.test",
            "Access-Control-Allow-Origin": "*",
            "X-Cicord-Instance": "spoof",
            "X-Frame-Options": "ALLOW",
            "X-Total": "3",
            ETag: '"v1"',
            "Cache-Control": "public, max-age=60",
            "Content-Type": "text/html",
          },
        }),
      },
      "GET /private": {
        access: "member",
        handler: () => ({ status: 200, headers: { "cache-control": "max-age=60", "x-a": "1" } }),
      },
      "GET /status/:code": {
        access: "public",
        handler: (_ctx, r) => ({ status: Number(r.params.code), body: { code: r.params.code } }),
      },
      "GET /bad-header": {
        access: "public",
        handler: () => ({ status: 200, headers: { "x-n": 5 as unknown as string } }),
      },
      "GET /huge": { access: "public", handler: () => ({ data: "x".repeat(4 << 20) }) },
      "GET /mod": { access: "moderator", handler: () => ({ ok: true }) },
      "GET /admin": { access: "admin", handler: () => ({ ok: true }) },
    },
  });
  const t = createTestContext({
    roles: { staff: { name: "Staff" } },
    members: [{ id: "1" }, { id: "2", roles: ["staff"] }, { id: "3" }, { id: "4" }],
    server: { ownerId: "4" },
    access: { moderators: { roles: ["staff"] }, admins: { users: ["3"] } },
  });

  test("filters plugin headers and adds the core's", async () => {
    const r = await callRoute(routes, t, "GET /headers");
    expect(r.headers).toEqual({
      "x-total": "3",
      etag: '"v1"',
      // A public anonymous answer keeps no-store for JSON bodies (portal.go:456).
      "cache-control": "no-store",
      "content-type": "application/json; charset=utf-8",
      vary: "Cookie, Authorization",
      "x-content-type-options": "nosniff",
    });
    // Without a body, a public anonymous route may let caches keep it; 200 becomes 204.
    const empty = await callRoute(routes, t, "GET /headers?empty=1");
    expect(empty.status).toBe(204);
    expect(empty.headers["cache-control"]).toBe("public, max-age=60");
    expect(empty.headers["content-type"]).toBeUndefined();
    const signedIn = await callRoute(routes, t, "GET /headers?empty=1", { viewer: "1" });
    expect(signedIn.headers["cache-control"]).toBe("no-store");
    // A non-public route cannot set cache-control.
    const priv = await callRoute(routes, t, "GET /private", { viewer: "1" });
    expect(priv.headers).toMatchObject({ "cache-control": "no-store", "x-a": "1" });
  });

  test("an invalid status, header or size fails the route with a 500", async () => {
    const failed = {
      status: 500,
      body: { error: { code: "internal", message: "the plugin route failed" } },
    };
    for (const code of ["101", "199", "600", "0"])
      expect(await callRoute(routes, t, `GET /status/${code}`)).toMatchObject(failed);
    expect((await callRoute(routes, t, "GET /status/200.5")).status).toBe(500);
    expect((await callRoute(routes, t, "GET /status/599")).status).toBe(599);
    expect(await callRoute(routes, t, "GET /bad-header")).toMatchObject(failed);
    expect(await callRoute(routes, t, "GET /huge")).toMatchObject(failed);
  });

  test("moderator and admin routes check the viewer's access level", async () => {
    const status = async (route: string, viewer?: string) =>
      (await callRoute(routes, t, route, viewer ? { viewer } : {})).status;
    expect(await status("GET /mod")).toBe(401);
    expect(await status("GET /mod", "9")).toBe(403); // not a member
    expect(await status("GET /mod", "1")).toBe(403);
    expect(await status("GET /mod", "2")).toBe(200);
    expect(await status("GET /mod", "3")).toBe(200);
    expect(await status("GET /admin", "2")).toBe(403);
    expect(await status("GET /admin", "3")).toBe(200);
    expect(await status("GET /admin", "4")).toBe(200); // the owner
    expect((await callRoute(routes, t, "GET /admin", { viewer: "2" })).body).toEqual({
      error: { code: "forbidden", message: "this route requires the admin role" },
    });
  });
});
