import { defineConfig } from "vite-plus";

export default defineConfig({
  pack: {
    entry: ["src/index.ts"],
    format: ["esm"],
    dts: { generator: "tsgo" },
    sourcemap: true,
  },
  test: {
    include: ["test/**/*.test.ts"],
  },
});
