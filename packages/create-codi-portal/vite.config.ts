import react from "@vitejs/plugin-react";
import { defineConfig } from "vite-plus";

// `vp dev --mode mock` (the default `dev` script) runs without the Go runtime: src/mock answers
// the API in the browser. `vp dev` proxies the API to a local `codi dev` instead.
const runtime = process.env["CODI_RUNTIME_URL"] ?? "http://localhost:8080";

export default defineConfig(({ mode }) => ({
  // Served by the runtime at "/" with an SPA fallback to index.html: absolute asset URLs
  // keep working on deep links such as /onboarding.
  base: "/",
  plugins: [react()],
  server: {
    port: 5174,
    ...(mode === "mock"
      ? {}
      : {
          proxy: {
            "/api": { target: runtime, changeOrigin: false },
            "/auth": { target: runtime, changeOrigin: false },
          },
        }),
  },
  build: {
    outDir: "dist",
    assetsDir: "assets",
    sourcemap: true,
  },
  test: {
    include: ["test/**/*.test.{ts,tsx}"],
    environment: "jsdom",
  },
}));
