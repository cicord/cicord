import { defineConfig } from "vite-plus";

export default defineConfig({
  pack: {
    entry: ["src/index.ts", "src/testing.ts"],
    format: ["esm"],
    // Types are not generated: dist/index.d.ts is a verbatim copy of spec/sdk/codi-sdk.d.ts
    // (the normative contract) and dist/testing.d.ts is emitted by tsc (see package.json).
    dts: false,
    sourcemap: true,
  },
  test: {
    include: ["test/**/*.test.ts"],
  },
});
