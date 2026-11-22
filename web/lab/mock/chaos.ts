// Stand-ins for the EPC stub and the fault agent in the mock API. One
// VoLTE UE is attached with both of its bearers, and the agent takes a
// fault, lists it and clears it, without touching anything.
// MOCK_FAULT_AGENT=off answers 502 instead, as the Lab server does when
// the agent is not running.

import type { AttachedUE, Fault, FaultRequest } from "../src/api.ts";

const attached: AttachedUE[] = [
  {
    imsi: "001010000000010",
    pdn_address: "172.28.0.120",
    access_point_name: "ims",
    bearers: [
      { eps_bearer_identity: 5, state: "BEARER CONTEXT ACTIVE", eps_qos: { qci: 5 } },
      { eps_bearer_identity: 6, state: "BEARER CONTEXT ACTIVE", eps_qos: { qci: 1 } },
    ],
    rx_sessions: ["pcscf.ims.mnc001.mcc001.3gppnetwork.org;1764000000;1"],
  },
];

const faults: Fault[] = [];
let nextID = 0x113e54e0;

export type Reply = { status: number; body: unknown } | null;

// handleChaos answers the paths of the EPC stub and the fault agent
// below /api, or null for a path of another panel.
export async function handleChaos(method: string, path: string, body: () => Promise<string>): Promise<Reply> {
  if (method === "GET" && path === "/epc/ues") return { status: 200, body: attached };
  const bearer = /^\/epc\/network-initiated\/ues\/([^/]+)\/bearers\/(\d+)$/.exec(path);
  if (method === "DELETE" && bearer !== null) {
    const ue = attached.find((u) => u.imsi === bearer[1]);
    const ebi = Number(bearer[2]);
    if (ue === undefined) return { status: 404, body: { error: `no such subscriber: ${bearer[1]}` } };
    if (!ue.bearers.some((b) => b.eps_bearer_identity === ebi)) {
      return { status: 404, body: { error: `no such EPS bearer: ${ebi}` } };
    }
    ue.bearers = ue.bearers.filter((b) => b.eps_bearer_identity !== ebi);
    return { status: 204, body: null };
  }
  const detach = /^\/epc\/network-initiated\/ues\/([^/]+)\/detach$/.exec(path);
  if (method === "POST" && detach !== null) {
    const index = attached.findIndex((u) => u.imsi === detach[1]);
    if (index < 0) return { status: 404, body: { error: `no such subscriber: ${detach[1]}` } };
    attached.splice(index, 1);
    return { status: 204, body: null };
  }

  if (path !== "/fault/faults" && !path.startsWith("/fault/faults/")) return null;
  if (process.env["MOCK_FAULT_AGENT"] === "off") return { status: 502, body: { error: "the fault agent did not answer" } };
  if (method === "GET" && path === "/fault/faults") return { status: 200, body: faults };
  if (method === "POST" && path === "/fault/faults") {
    const request = JSON.parse(await body()) as FaultRequest;
    const now = new Date();
    const lasting = request.kind !== "crash" && request.kind !== "recreate";
    const duration = request.duration ?? "5m0s";
    const fault: Fault = {
      id: (nextID++).toString(16),
      target: request.target,
      kind: request.kind,
      network: request.network ?? "",
      delay: request.delay ?? "",
      jitter: request.jitter ?? "",
      loss_percent: request.loss_percent ?? null,
      peers: request.peers ?? [],
      duration: lasting ? duration : "",
      error: "",
      state: lasting ? "active" : "done",
      created_at: now.toISOString(),
      expires_at: lasting ? new Date(now.getTime() + 300000).toISOString() : "",
      ended_at: lasting ? "" : now.toISOString(),
    };
    faults.push(fault);
    return { status: 201, body: fault };
  }
  if (method === "DELETE") {
    const id = path.slice("/fault/faults/".length);
    const fault = faults.find((f) => f.id === id);
    if (fault === undefined) return { status: 404, body: { error: `no such fault: ${id}` } };
    if (fault.state === "active") {
      fault.state = "cleared";
      fault.ended_at = new Date().toISOString();
    }
    return { status: 200, body: fault };
  }
  return null;
}
