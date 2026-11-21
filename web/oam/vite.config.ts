import { defineConfig } from "vite";
import { mockApi } from "./mock/api.ts";

// `npm run dev` serves the UI with the mock OAM API. With
// OAM_URL=http://localhost:8090 it proxies /oam/v1 to a running OAM
// instead.
const oam = process.env["OAM_URL"];

export default defineConfig({
  // Relative asset paths, so that the built page works wherever the Go
  // server serves it.
  base: "./",
  plugins: oam ? [] : [mockApi()],
  server: oam ? { proxy: { "/oam/v1": oam } } : {},
  build: { outDir: "dist", emptyOutDir: true },
});
