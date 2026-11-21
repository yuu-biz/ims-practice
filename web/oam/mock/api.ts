// Serves the mock OAM API (oam.ts) from the Vite dev server, under the
// path where the OAM serves its API.

import type { Plugin } from "vite";
import { createMockOAM } from "./oam.ts";

export function mockApi(): Plugin {
  const handle = createMockOAM();
  return {
    name: "ims-mock-oam",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use("/oam/v1", (req, res) => {
        handle(req, res, req.url ?? "/").catch((error: unknown) => {
          res.writeHead(500, { "Content-Type": "application/json" }).end(JSON.stringify({ error: String(error) }));
        });
      });
    },
  };
}
