import react from "@vitejs/plugin-react";
import { defineConfig } from "vite-plus";

// `vp dev` proxies the API to a local `codi dev --sim examples/basic` (CODI_RUNTIME_URL,
// default http://127.0.0.1:7420, the runtime's default public URL). `vp dev --mode mock` runs
// without the Go runtime: src/mock answers the admin and simulator endpoints in the browser.
const runtime = process.env["CODI_RUNTIME_URL"] ?? "http://127.0.0.1:7420";
const runtimeOrigin = new URL(runtime).origin;

// The runtime rejects unsafe requests whose Origin is not its public URL (CSRF protection).
// The dev server is a trusted same-machine proxy: present requests as same-origin.
const proxyTo = {
  target: runtime,
  changeOrigin: false,
  configure: (proxy: { on: (event: "proxyReq", fn: (req: ProxyRequest) => void) => void }) => {
    proxy.on("proxyReq", (req) => {
      if (req.getHeader("origin")) req.setHeader("origin", runtimeOrigin);
    });
  },
};

interface ProxyRequest {
  getHeader: (name: string) => unknown;
  setHeader: (name: string, value: string) => void;
}

export default defineConfig(({ mode }) => ({
  // Served by the runtime under /admin/ with an SPA fallback to index.html.
  base: "/admin/",
  plugins: [react()],
  server: {
    port: 5175,
    ...(mode === "mock"
      ? {}
      : {
          proxy: {
            "/api": proxyTo,
            "/auth": proxyTo,
            "/hooks": proxyTo,
          },
        }),
  },
  build: {
    outDir: "dist",
    assetsDir: "assets",
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
  test: {
    include: ["test/**/*.test.{ts,tsx}"],
    environment: "jsdom",
  },
}));
