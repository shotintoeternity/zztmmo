import { defineConfig } from "vite";

// The dev server proxies the game socket and the JSON API to a zzt-server
// running on 8080, so `npm run dev` on 5173 plays against the real thing. The
// server's origin allowlist accepts any 127.0.0.1 port, which is why the proxy
// needs no origin rewriting.
export default defineConfig({
  server: {
    proxy: {
      "/ws": { target: "ws://127.0.0.1:8080", ws: true },
      "/api": { target: "http://127.0.0.1:8080" },
    },
  },
});
