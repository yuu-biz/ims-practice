// A stand-in for the Lab server's HTTP API, served by the Vite dev
// server from the test fixtures. MOCK_PROVISIONING=off makes the
// subscriber list answer 503, as the server does when
// lab.hss_provisioning is not set. The UE simulators are in ues.ts, the
// EPC stub and the fault agent in chaos.ts.

import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin } from "vite";
import { allMessages, calls, registrations, subscribers } from "../test/fixtures.ts";
import { handleChaos } from "./chaos.ts";
import { handleUE } from "./ues.ts";

// The last hop of the call (its two captured copies) arrives late: each
// event stream connection holds it back from /api/messages and delivers
// it as message events a few seconds after it opens, so that the live
// path of the page can be watched. A repeated copy follows, which the
// page must not draw twice.
const lateHop = allMessages.slice(-2);
const lateAfterMs = 5000;

export function mockApi(): Plugin {
  const provisioning = process.env["MOCK_PROVISIONING"] !== "off";
  let captured = allMessages;

  function events(req: IncomingMessage, res: ServerResponse): void {
    captured = allMessages.slice(0, -lateHop.length);
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    res.write(": mock event stream\n\n");
    const late = setTimeout(() => {
      captured = allMessages;
      for (const m of [...lateHop, ...lateHop.slice(-1)]) res.write(`event: message\ndata: ${JSON.stringify(m)}\n\n`);
      res.write("event: state\ndata: {}\n\n");
    }, lateAfterMs);
    const keepAlive = setInterval(() => res.write(": keep-alive\n\n"), 15000);
    req.on("close", () => {
      clearTimeout(late);
      clearInterval(keepAlive);
    });
  }

  async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const url = new URL(req.url ?? "/", "http://mock");
    const path = url.pathname;

    if (req.method === "GET" && path === "/calls") return json(res, 200, calls);
    if (req.method === "GET" && path === "/registrations") return json(res, 200, registrations);
    if (req.method === "GET" && path === "/messages") {
      const callIds = url.searchParams.getAll("call_id");
      const correlationId = url.searchParams.get("correlation_id");
      return json(
        res,
        200,
        captured.filter(
          (m) =>
            (callIds.length === 0 || callIds.includes(m.call_id)) &&
            (correlationId === null || m.correlation_id === correlationId),
        ),
      );
    }

    if (req.method === "GET" && path === "/events") return events(req, res);

    // The subscriptions as the HSS holds them. The Lab changes none of
    // them: an operator provisions a subscription through the OAM.
    if (req.method === "GET" && path === "/subscribers") {
      if (!provisioning) return text(res, 503, "provisioning not configured");
      return json(res, 200, subscribers);
    }

    const chaos = await handleChaos(req.method ?? "", path, () => readBody(req));
    if (chaos !== null) {
      if (chaos.body === null) {
        res.writeHead(chaos.status).end();
        return;
      }
      return json(res, chaos.status, chaos.body);
    }

    if (await handleUE(req, res, path, () => readBody(req))) return;

    text(res, 404, "not found");
  }

  return {
    name: "ims-mock-api",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use("/api", (req, res) => {
        handle(req, res).catch((error: unknown) => text(res, 500, String(error)));
      });
    },
  };
}

function json(res: ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { "Content-Type": "application/json" }).end(JSON.stringify(body));
}

function text(res: ServerResponse, status: number, body: string): void {
  res.writeHead(status, { "Content-Type": "text/plain" }).end(body);
}

async function readBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk as Buffer);
  return Buffer.concat(chunks).toString("utf8");
}
