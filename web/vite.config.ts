import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vitest/config"

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  build: {
    // The Go binary embeds this directory (internal/webui).
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  worker: { format: "es" },
  server: {
    // Development: `codeg serve -dev-origin http://localhost:5173` and
    // `npm run dev`. The proxy sends /api to the Go server; changeOrigin
    // rewrites Host to the server's own address, which it validates.
    proxy: {
      "/api": {
        target: "http://127.0.0.1:7373",
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
  },
})
