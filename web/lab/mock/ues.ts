// Two stand-in UE simulators for the mock API. ue-a and ue-b act out a
// call between them: whichever calls, the other rings and answers by its
// answer policy, and either one hangs up for both. A short message to the
// other's number reaches it, and is accepted and reported; one to any
// other number is refused as an unassigned number. Each keeps the
// supplementary services that its /xcap endpoints set, with every
// service provisioned and off to start with. Nothing goes over SIP or Ut,
// so the calls table and the ladder do not change.

import type { IncomingMessage, ServerResponse } from "node:http";
import {
  barringServices,
  diversionServices,
  type AnswerMode,
  type AnswerPolicy,
  type SupplementaryService,
  type SupplementaryServices,
  type UECall,
  type UESentSMS,
  type UESession,
  type UEStatus,
} from "../src/api.ts";
import { user1, user2 } from "../test/fixtures.ts";

interface MockUE {
  name: string;
  tel: string;
  status: UEStatus;
  streams: Set<ServerResponse>;
  services: SupplementaryServices;
}

const ues: MockUE[] = [
  { name: "ue-a", tel: "tel:+12025550101", impu: user1 },
  { name: "ue-b", tel: "tel:+12025550102", impu: user2 },
].map(({ name, tel, impu }) => ({
  name,
  tel,
  status: {
    impu,
    registered: true,
    call: null,
    // The mock has one call on the line at most.
    get calls() {
      return this.call === null ? [] : [this.call];
    },
    conference: null,
    answer_policy: { mode: "auto", ring_seconds: 1 },
    sms: { sent: [], received: [] },
  },
  streams: new Set(),
  services: {
    etag: '"0"',
    services: [...diversionServices, ...barringServices].map((name) => ({ name, provisioned: true, active: false, target: "" })),
    oir_default_behaviour: "presentation-not-restricted",
    document: "<simservs/>",
  },
}));

type Reply = { status: number; body: unknown };

const conflict = (error: string): Reply => ({ status: 409, body: { error } });

// handleUE answers /ues and /ues/{name}/{endpoint}, or returns false for
// another path.
export async function handleUE(req: IncomingMessage, res: ServerResponse, path: string, body: () => Promise<string>): Promise<boolean> {
  if (path === "/ues" && req.method === "GET") {
    reply(res, { status: 200, body: ues.map((ue) => ({ name: ue.name, reachable: true, status: ue.status })) });
    return true;
  }
  const match = /^\/ues\/([^/]+)\/([a-z/-]+)$/.exec(path);
  if (!match) return false;
  const ue = ues.find((u) => u.name === decodeURIComponent(match[1] ?? ""));
  if (!ue) {
    reply(res, { status: 404, body: { error: "no such UE simulator" } });
    return true;
  }
  const endpoint = `${req.method} ${match[2]}`;
  if (endpoint === "GET events") {
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    res.write(`data: ${JSON.stringify(ue.status)}\n\n`);
    ue.streams.add(res);
    req.on("close", () => ue.streams.delete(res));
    return true;
  }
  const input = (await body()) || "{}";
  reply(res, act(ue, endpoint, JSON.parse(input) as Record<string, unknown>));
  return true;
}

function act(ue: MockUE, endpoint: string, input: Record<string, unknown>): Reply {
  const other = ues.find((u) => u !== ue)!;
  const ok = (): Reply => ({ status: 200, body: ue.status });
  switch (endpoint) {
    case "POST register":
      ue.status.registered = true;
      break;
    case "POST deregister":
      if (ue.status.call) return conflict("there is a call on the line already");
      ue.status.registered = false;
      break;
    case "POST call": {
      if (!ue.status.registered) return conflict("the UE is not registered");
      if (ue.status.call) return conflict("there is a call on the line already");
      const callId = `mock-${Date.now()}@172.28.0.110`;
      ue.status.call = call(callId, "outgoing", String(input["target"]), "calling");
      if (other.status.registered && !other.status.call) {
        other.status.call = call(callId, "incoming", ue.tel, "ringing");
        ue.status.call.state = "ringing";
        const policy = other.status.answer_policy;
        if (policy.mode === "auto") setTimeout(() => answer(other), policy.ring_seconds * 1000);
        if (policy.mode === "reject") setTimeout(() => hangUp(other), 300);
      }
      break;
    }
    case "POST answer":
      if (ue.status.call?.direction !== "incoming" || ue.status.call.state !== "ringing") {
        return conflict("there is no incoming call that rings");
      }
      answer(ue);
      return ok();
    case "POST hangup":
      if (!ue.status.call) return conflict("there is no call on the line");
      hangUp(ue);
      return ok();
    case "POST hold":
    case "POST resume": {
      const session = ue.status.call?.session;
      if (!session) return conflict("there is no call on the line");
      const held = endpoint === "POST hold";
      session.held = held;
      session.direction = held ? "sendonly" : "sendrecv";
      session.peer_direction = held ? "recvonly" : "sendrecv";
      const peer = other.status.call?.session;
      if (peer) Object.assign(peer, { direction: session.peer_direction, peer_direction: session.direction });
      break;
    }
    case "POST dtmf": {
      const session = ue.status.call?.session;
      if (!session) return conflict("there is no call on the line");
      const digits = String(input["digits"]);
      session.dtmf_sent += digits;
      const peer = other.status.call?.session;
      if (peer) peer.dtmf_received += digits;
      break;
    }
    case "PUT answer-policy": {
      const mode = input["mode"] as AnswerMode;
      const policy: AnswerPolicy =
        mode === "auto" ? { mode, ring_seconds: 1 } : mode === "reject" ? { mode, reject_status: 486 } : { mode: "manual" };
      ue.status.answer_policy = policy;
      publish(ue);
      return { status: 200, body: policy };
    }
    case "POST sms":
      if (!ue.status.registered) return conflict("the UE is not registered");
      return { status: 200, body: sendSMS(ue, other, input) };
    case "GET xcap":
      return { status: 200, body: ue.services };
    case "PUT xcap/oir":
      ue.services.oir_default_behaviour = input["default_behaviour"] as SupplementaryServices["oir_default_behaviour"];
      return changed(ue);
    default: {
      const service = /^PUT xcap\/services\/([a-z-]+)$/.exec(endpoint)?.[1] as SupplementaryService | undefined;
      const setting = ue.services.services.find((s) => s.name === service);
      if (setting) {
        const target = typeof input["target"] === "string" ? input["target"] : "";
        const diversion = (diversionServices as readonly string[]).includes(setting.name);
        if (input["active"] === true && diversion && target === "" && setting.target === "") {
          return { status: 400, body: { error: `invalid setting: ${setting.name} needs a target` } };
        }
        setting.active = input["active"] === true;
        if (target !== "") setting.target = target;
        return changed(ue);
      }
      return { status: 404, body: { error: "not found" } };
    }
  }
  publish(ue);
  publish(other);
  return ok();
}

// A change of the mock's settings, with the next entity tag as the HSS
// would give it.
function changed(ue: MockUE): Reply {
  ue.services.etag = `"${Number(ue.services.etag.slice(1, -1)) + 1}"`;
  return { status: 200, body: ue.services };
}

function sendSMS(ue: MockUE, other: MockUE, input: Record<string, unknown>): UESentSMS {
  const sent: UESentSMS = {
    call_id: `mock-sms-${Date.now()}@172.28.0.110`,
    to: String(input["to"]),
    text: String(input["text"]),
    status_report_requested: input["status_report"] === true,
    sent: new Date().toISOString(),
    state: "submitted",
    rp_cause: null,
    failure_cause: null,
    status_report: null,
  };
  ue.status.sms.sent.push(sent);
  publish(ue);
  setTimeout(() => {
    if (`tel:${sent.to}` !== other.tel) {
      Object.assign(sent, { state: "failed", rp_cause: 1, failure_cause: 0xc3 });
      publish(ue);
      return;
    }
    sent.state = "accepted";
    const now = new Date().toISOString();
    other.status.sms.received.push({ call_id: `mock-sms-${Date.now()}@172.28.0.70`, from: ue.tel.slice(4), text: sent.text, received: now });
    ues.forEach(publish);
    if (sent.status_report_requested) {
      setTimeout(() => {
        sent.status_report = { status: 0, discharge_time: now, received: new Date().toISOString() };
        publish(ue);
      }, 400);
    }
  }, 200);
  return sent;
}

function call(callId: string, direction: UECall["direction"], peer: string, state: UECall["state"]): UECall {
  return { call_id: callId, direction, peer, state, started: new Date().toISOString(), waiting: false, session: null };
}

// The session the mock gives an answered call: AMR-WB with preconditions
// met, as between two UEs over the EPC stub.
function session(): UESession {
  const row = (segment: string, direction: string) => ({ segment, direction, reserved: true, strength: "mandatory", confirm: false });
  return {
    codec: { name: "AMR-WB", payload_type: 96, clock_rate: 16000, payload_format: "bandwidth-efficient", telephone_event: 100 },
    direction: "sendrecv",
    peer_direction: "sendrecv",
    preconditions: { met: true, rows: [row("local", "send"), row("local", "recv"), row("remote", "send"), row("remote", "recv")] },
    media_resources: true,
    reservation_needed: true,
    held: false,
    ringback: "",
    dtmf_sent: "",
    dtmf_received: "",
  };
}

function answer(ue: MockUE): void {
  for (const u of ues) {
    if (u.status.call?.call_id === ue.status.call?.call_id && u.status.call) Object.assign(u.status.call, { state: "active", session: session() });
  }
  ues.forEach(publish);
}

function hangUp(ue: MockUE): void {
  const callId = ue.status.call?.call_id;
  for (const u of ues) if (u.status.call?.call_id === callId) u.status.call = null;
  ues.forEach(publish);
}

function publish(ue: MockUE): void {
  for (const stream of ue.streams) stream.write(`data: ${JSON.stringify(ue.status)}\n\n`);
}

function reply(res: ServerResponse, { status, body }: Reply): void {
  res.writeHead(status, { "Content-Type": "application/json" }).end(JSON.stringify(body));
}
