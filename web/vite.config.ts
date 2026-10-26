import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// `npm run dev` proxies the API to a running `ledger serve` (port 8080). The server refuses
// cross-origin writes, so the proxy drops the dev server's Origin header.
export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        configure: (proxy) => proxy.on("proxyReq", (req) => req.removeHeader("origin")),
      },
    },
  },
  build: { outDir: "dist", emptyOutDir: true },
});
