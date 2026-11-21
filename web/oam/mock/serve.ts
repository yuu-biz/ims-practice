// Serves the built page (dist/) and the mock OAM API (oam.ts) on one
// origin, as the OAM role will, for a check of the page in a browser:
//
//	npm run build && node mock/serve.ts
//
// PORT chooses the port (8090 by default). Only the loopback address
// listens.

import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";
import { createMockOAM } from "./oam.ts";

const root = join(import.meta.dirname, "..", "dist");
const port = Number(process.env["PORT"] ?? "8090");
const types: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".css": "text/css",
};
const handle = createMockOAM();

createServer((req, res) => {
  const url = new URL(req.url ?? "/", "http://localhost");
  if (url.pathname.startsWith("/oam/v1/")) {
    handle(req, res, url.pathname.slice("/oam/v1".length) + url.search).catch((error: unknown) => {
      res.writeHead(500, { "Content-Type": "application/json" }).end(JSON.stringify({ error: String(error) }));
    });
    return;
  }
  const file = normalize(join(root, url.pathname === "/" ? "index.html" : url.pathname));
  if (!file.startsWith(root)) {
    res.writeHead(404).end();
    return;
  }
  readFile(file).then(
    (data) => res.writeHead(200, { "Content-Type": types[extname(file)] ?? "application/octet-stream" }).end(data),
    () => res.writeHead(404).end("not found"),
  );
}).listen(port, "127.0.0.1", () => console.log(`http://127.0.0.1:${port}/`));
