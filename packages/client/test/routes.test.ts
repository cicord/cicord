import { describe, expect, expectTypeOf, test } from "vite-plus/test";
import {
  createCodiClient,
  resolveRoute,
  type FetchLike,
  type Json,
  type Leaderboard,
  type RouteEntry,
  type RouteParams,
} from "../src/index.ts";

// What `codi portal typegen` generates for a plugin with a contract (see
// internal/typegen/routes.go and packages/create-codi-portal/src/codi-routes.d.ts).
declare namespace codi_demo {
  interface Item {
    id: string;
    title: string;
  }
  interface Routes {
    "GET /items": { query: { q?: string; limit?: number }; response: { items: Item[] } };
    "GET /items/:id": { response: Item };
    "POST /items/:id/votes/:choice": { body: { weight: number }; response: { votes: number } };
    "DELETE /items/:id": {};
  }
}

declare module "../src/routes.ts" {
  interface PluginRouteRegistry {
    demo: {
      "GET /items": RouteEntry<"public", codi_demo.Routes, "GET /items">;
      "GET /items/:id": RouteEntry<"public", codi_demo.Routes, "GET /items/:id">;
      "POST /items/:id/votes/:choice": RouteEntry<
        "member",
        codi_demo.Routes,
        "POST /items/:id/votes/:choice"
      >;
      "DELETE /items/:id": RouteEntry<"admin", codi_demo.Routes, "DELETE /items/:id">;
      "GET /raw": RouteEntry<"public">;
    };
  }
}

function recorder() {
  const calls: { url: string; method: string; body: unknown }[] = [];
  const fetch: FetchLike = async (url, init) => {
    calls.push({
      url,
      method: init.method ?? "GET",
      body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
    });
    return init.method === "DELETE"
      ? new Response(null, { status: 204 })
      : new Response(JSON.stringify({ ok: true }), {
          headers: { "Content-Type": "application/json" },
        });
  };
  return { calls, client: createCodiClient({ fetch }) };
}

const at = { bot: "helper", instance: "demo-1" };

describe("typed plugin routes", () => {
  test("fills path parameters, query and body", async () => {
    const { calls, client } = recorder();
    const demo = client.routes("demo", "ma-commu", at);
    await demo.call("GET /items", { query: { q: "a b", limit: 5 } });
    await demo.call("GET /items/:id", { params: { id: "x/1" } });
    await demo.call("POST /items/:id/votes/:choice", {
      params: { id: "7", choice: "yes" },
      body: { weight: 2 },
    });
    await expect(demo.call("DELETE /items/:id", { params: { id: "7" } })).resolves.toBeUndefined();
    const base = "/api/v1/portal/ma-commu/plugins/helper/demo-1";
    expect(calls).toEqual([
      { url: `${base}/items?q=a+b&limit=5`, method: "GET", body: undefined },
      { url: `${base}/items/x%2F1`, method: "GET", body: undefined },
      { url: `${base}/items/7/votes/yes`, method: "POST", body: { weight: 2 } },
      { url: `${base}/items/7`, method: "DELETE", body: undefined },
    ]);
    expect(demo.url("GET /items/:id", { params: { id: "3" } })).toBe(`${base}/items/3`);
  });

  test("resolveRoute rejects missing parameters and bad keys", () => {
    expect(resolveRoute("GET /a/:id/b", { id: 4 })).toEqual(["GET", "/a/4/b"]);
    expect(() => resolveRoute("GET /a/:id")).toThrow(/missing parameter id/);
    expect(() => resolveRoute("FETCH /a")).toThrow(/invalid route key/);
  });

  test("types come from the registry", () => {
    const demo = createCodiClient().routes("demo", "s", at);
    expectTypeOf(demo.call<"GET /items">).returns.resolves.toEqualTypeOf<{
      items: codi_demo.Item[];
    }>();
    expectTypeOf(demo.call<"GET /items/:id">).returns.resolves.toEqualTypeOf<codi_demo.Item>();
    expectTypeOf(demo.call<"DELETE /items/:id">).returns.resolves.toEqualTypeOf<undefined>();
    expectTypeOf(demo.call<"GET /raw">).returns.resolves.toEqualTypeOf<Json>();
    expectTypeOf<RouteParams<"POST /a/:x/b/:y">>().toEqualTypeOf<{ x: string } & { y: string }>();

    // Unknown routes, missing or extra parameters and wrong bodies do not compile.
    const typeErrors = () => {
      // @ts-expect-error unknown route
      void demo.call("GET /nope");
      // @ts-expect-error params are required
      void demo.call("GET /items/:id");
      // @ts-expect-error unknown parameter
      void demo.call("GET /items/:id", { params: { key: "1" } });
      // @ts-expect-error the body is required
      void demo.call("POST /items/:id/votes/:choice", { params: { id: "1", choice: "a" } });
      void demo.call("POST /items/:id/votes/:choice", {
        params: { id: "1", choice: "a" },
        // @ts-expect-error wrong body type
        body: { weight: "2" },
      });
      // @ts-expect-error wrong query type
      void demo.call("GET /items", { query: { limit: "5" } });
    };
    expect(typeErrors).toBeTypeOf("function");
  });

  test("official plugins are typed without a generated registry entry", () => {
    const levels = createCodiClient().routes("levels", "s", at);
    expectTypeOf(levels.call<"GET /leaderboard">).returns.resolves.toEqualTypeOf<Leaderboard>();
    const other = createCodiClient().routes("not-installed", "s", at);
    expectTypeOf(other.call<"GET /anything/:id">).returns.resolves.toEqualTypeOf<Json>();
  });
});
