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
    const t = createTestContext({ members: [{ id: "1", username: "alice" }] });
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
    expect((await callRoute(plugin, t, "DELETE /polls/1", { viewer: "1" })).status).toBe(204);
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
