import { defineConfig } from "vite";
import { mockApi } from "./mock/api.ts";

// `npm run dev` serves the UI with the mock API. With
// DASHBOARD_URL=http://localhost:8080 it proxies /api to a running
// dashboard server instead.
const dashboard = process.env["DASHBOARD_URL"];

export default defineConfig({
  // Relative asset paths, so that the built page works wherever the Go
  // server serves it.
  base: "./",
  plugins: dashboard ? [] : [mockApi()],
  server: dashboard ? { proxy: { "/api": dashboard } } : {},
  build: { outDir: "dist", emptyOutDir: true },
});
