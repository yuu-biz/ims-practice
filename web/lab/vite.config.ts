import { defineConfig } from "vite";
import { mockApi } from "./mock/api.ts";

// `npm run dev` serves the UI with the mock API. With
// LAB_URL=http://localhost:8080 it proxies /api to a running
// Lab server instead.
const lab = process.env["LAB_URL"];

export default defineConfig({
  // Relative asset paths, so that the built page works wherever the Go
  // server serves it.
  base: "./",
  plugins: lab ? [] : [mockApi()],
  server: lab ? { proxy: { "/api": lab } } : {},
  build: { outDir: "dist", emptyOutDir: true },
});
