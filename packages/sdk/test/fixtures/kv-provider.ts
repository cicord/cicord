import { definePlugin } from "../../src/index.ts";

// Loaded through `vi.resetModules()` + dynamic import by provider-realm.test.ts.
export default definePlugin({
  queries: {
    search: {
      async run(ctx, input: { q: string }) {
        const page = await ctx.kv.list<string>({ prefix: "tag:" });
        return { names: page.items.map((i) => i.value).filter((n) => n.includes(input.q)) };
      },
    },
  },
});
