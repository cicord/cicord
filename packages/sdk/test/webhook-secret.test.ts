import { expect, test } from "vite-plus/test";
import { createTestContext } from "../src/testing.ts";

test("opaque webhook sends expose status alone in the SDK harness", async () => {
  const t = createTestContext({
    secrets: ["HOOK"],
    fetch: () => ({
      status: 201,
      headers: { "x-reflected": "fixture" },
      body: { token: "fixture" },
    }),
  });
  const ref = t.ctx.secrets.ref("HOOK");
  await expect(t.ctx.http.fetch(ref, { method: "GET" })).rejects.toThrow("webhook");
  const response = await t.ctx.http.fetch(ref, { method: "POST", json: { content: "test" } });
  expect(response.status).toBe(201);
  expect(response.headers).toEqual({});
  expect(await response.text()).toBe("");
  expect(await response.bytes()).toHaveLength(0);
  expect(t.requests).toHaveLength(1);
});
