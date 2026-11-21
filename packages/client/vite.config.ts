import { defineConfig } from "vite-plus";

export default defineConfig({
  pack: {
    entry: ["src/index.ts"],
    format: ["esm"],
    dts: { tsgo: true },
    exports: true,
    sourcemap: true,
  },
  test: {
    include: ["test/**/*.test.ts"],
  },
});
