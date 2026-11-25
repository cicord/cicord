import { describe, expect, test } from "vite-plus/test";
import { keys } from "../src/api.ts";
import { prefixesFor } from "../src/shell/live.ts";

describe("live refresh", () => {
  test("each hint invalidates what it concerns", () => {
    expect(prefixesFor({ type: "hello", version: "0.1.4", revision: "abc" })).toEqual([["admin"]]);
    expect(prefixesFor({ type: "status" })).toEqual([keys.status()]);
    expect(prefixesFor({ type: "sync", server: "main" })).toEqual([
      keys.status(),
      ["admin", "history"],
      keys.server("main"),
    ]);
    // A revision (sync without server) changes what the Store shows installed and updatable.
    expect(prefixesFor({ type: "sync" })).toEqual([
      keys.status(),
      ["admin", "history"],
      keys.store(),
    ]);
    // A registry refresh: the Store and the update badges of every server's plugins.
    expect(prefixesFor({ type: "store" })).toEqual([keys.store(), ["admin", "server"]]);
    // keys.store() covers the plugin pages and update reviews (stale details).
    for (const key of [
      keys.storeDetail("cicord", "tickets"),
      keys.storeUpdate("cicord", "tickets"),
    ])
      expect(key.slice(0, keys.store().length)).toEqual([...keys.store()]);
    expect(keys.plugins("main").slice(0, 2)).toEqual(["admin", "server"]);
    expect(prefixesFor({ type: "plugins", server: "main" })).toEqual([
      keys.status(),
      keys.plugins("main"),
    ]);
    expect(prefixesFor({ type: "analytics", server: "main" })).toEqual([keys.analytics("main")]);
    // A server's sync hint (a sync record, or drift seen in a gateway event) covers its
    // plan, resources and drift (the server query).
    const [, , serverPrefix] = prefixesFor({ type: "sync", server: "main" });
    for (const key of [keys.plan("main"), keys.resources("main"), keys.server("main")])
      expect(key.slice(0, serverPrefix!.length)).toEqual(serverPrefix);
    // The history prefix covers every history query (limit, target).
    const history = keys.history(30, "server:main");
    expect(history.slice(0, 2)).toEqual(["admin", "history"]);
  });
});
