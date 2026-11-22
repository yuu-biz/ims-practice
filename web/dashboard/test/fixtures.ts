// Hand-written API data for the tests and the development mock. The
// sequences, addresses and identities follow the Compose network and the
// guides (docs/guide/en/registration.md and call.md); the payloads are
// shortened. Each hop is reported by both nodes, as the dashboard server
// returns it.

import type { Call, Message, Protocol, Registration, SpecRef, Subscriber } from "../src/api.ts";

const domain = "ims.mnc001.mcc001.3gppnetwork.org";

export const hosts = {
  ue1: "172.28.0.100",
  ue2: "172.28.0.101",
  pcscf: "172.28.0.40",
  scscf: "172.28.0.30",
  icscf: "172.28.0.20",
  hss: "172.28.0.10",
} as const;
type Host = keyof typeof hosts;

const nodeName = (h: Host) => (h === "ue1" || h === "ue2" ? "ue" : h);

// A connection between two hosts. The client side of a TCP connection uses
// an ephemeral port; responses come back on the same connection.
interface Leg {
  client: Host;
  server: Host;
  transport: "udp" | "tcp";
  clientPort: number;
  serverPort: number;
}

const udp = (client: Host, server: Host): Leg => ({
  client,
  server,
  transport: "udp",
  clientPort: 5060,
  serverPort: 5060,
});
const tcp = (client: Host, server: Host, clientPort: number, serverPort = 5060): Leg => ({
  client,
  server,
  transport: "tcp",
  clientPort,
  serverPort,
});

// Plain descriptions, not the titles of the clauses.
const refsBySummary: [string, SpecRef][] = [
  ["REGISTER", { spec: "TS 24.229", clause: "5.1.1.2.1", title: "the UE registers" }],
  ["INVITE", { spec: "TS 24.229", clause: "5.1.3.1", title: "the UE starts a session" }],
  ["PRACK", { spec: "RFC 3262", clause: "4", title: "PRACK" }],
  ["UAR", { spec: "TS 29.228", clause: "6.1.1", title: "UAR" }],
  ["MAR", { spec: "TS 29.228", clause: "6.3", title: "MAR" }],
  ["SAR", { spec: "TS 29.228", clause: "", title: "SAR" }],
  ["LIR", { spec: "TS 29.228", clause: "6.1.4", title: "LIR" }],
];

class Capture {
  readonly messages: Message[] = [];
  private nextId: number;
  private micros = 0;

  constructor(firstId: number, private readonly start: string) {
    this.nextId = firstId;
  }

  // One message on the wire, reported by its sender and its receiver.
  hop(leg: Leg, towardsServer: boolean, protocol: Protocol, summary: string, payload: string, callId: string, correlationId: string) {
    const [from, to] = towardsServer ? [leg.client, leg.server] : [leg.server, leg.client];
    const [fromPort, toPort] = towardsServer ? [leg.clientPort, leg.serverPort] : [leg.serverPort, leg.clientPort];
    const src = `${hosts[from]}:${fromPort}`;
    const dst = `${hosts[to]}:${toPort}`;
    const refs = refsBySummary.filter(([prefix]) => summary.startsWith(prefix)).map(([, ref]) => ref);
    for (const [node, direction] of [[from, "send"], [to, "recv"]] as const) {
      this.micros += 350;
      this.messages.push({
        id: this.nextId++,
        time: this.timeAt(this.micros),
        protocol,
        transport: leg.transport,
        src,
        dst,
        node: nodeName(node),
        direction,
        summary,
        correlation_id: correlationId,
        call_id: callId,
        payload,
        refs,
        sms: null,
      });
    }
  }

  sip(leg: Leg, towardsServer: boolean, startLine: string, callId: string, cseq: string, summary: string) {
    const from = towardsServer ? leg.client : leg.server;
    const port = towardsServer ? leg.clientPort : leg.serverPort;
    const payload = [
      startLine,
      `Via: SIP/2.0/${leg.transport.toUpperCase()} ${hosts[from]}:${port};branch=z9hG4bK${this.nextId}`,
      `Call-ID: ${callId}`,
      `CSeq: ${cseq}`,
      "Content-Length: 0",
      "",
      "",
    ].join("\r\n");
    this.hop(leg, towardsServer, "sip", summary, payload, callId, callId);
  }

  request(leg: Leg, method: string, uri: string, callId: string, cseq: number) {
    this.sip(leg, true, `${method} ${uri} SIP/2.0`, callId, `${cseq} ${method}`, `${method} ${uri}`);
  }

  response(leg: Leg, status: string, method: string, callId: string, cseq: number) {
    this.sip(leg, false, `SIP/2.0 ${status}`, callId, `${cseq} ${method}`, `${status} (${method})`);
  }

  // A Cx request and its answer, as the dashboard server renders Diameter,
  // with the Call-ID of the SIP request that caused them.
  cx(leg: Leg, command: string, code: number, short: string, sessionId: string, publicIdentity: string, callId: string) {
    const avps = [
      { name: "Session-Id", code: 263, vendor: 0, value: sessionId },
      { name: "Public-Identity", code: 601, vendor: 10415, value: publicIdentity },
    ];
    const request = { command, code, request: true, application_id: 16777216, hop_by_hop: this.nextId, end_to_end: this.nextId, avps };
    const answer = {
      ...request,
      request: false,
      avps: [
        avps[0],
        {
          name: "Experimental-Result",
          code: 297,
          vendor: 0,
          avps: [{ name: "Experimental-Result-Code", code: 298, vendor: 0, value: 2001 }],
        },
      ],
    };
    this.hop(leg, true, "diameter", `${short}R`, JSON.stringify(request), callId, sessionId);
    this.hop(leg, false, "diameter", `${short}A 2001`, JSON.stringify(answer), callId, sessionId);
  }

  // A Diameter base message: no Session-Id, so no correlation.
  watchdog(leg: Leg) {
    const request = { command: "Device-Watchdog", code: 280, request: true, application_id: 0, hop_by_hop: this.nextId, end_to_end: this.nextId, avps: [] };
    this.hop(leg, true, "diameter", "DWR", JSON.stringify(request), "", "");
    this.hop(leg, false, "diameter", "DWA 2001", JSON.stringify({ ...request, request: false }), "", "");
  }

  private timeAt(micros: number): string {
    const date = new Date(Date.parse(this.start) + Math.floor(micros / 1000));
    const fraction = String((micros % 1_000_000) * 1000).padStart(9, "0");
    return date.toISOString().replace(/\.\d{3}Z$/, `.${fraction}Z`);
  }
}

export const user1 = `sip:+12025550101@${domain}`;
export const user2 = `sip:+12025550102@${domain}`;
export const registrationCallId = "reg-7f3a1c@172.28.0.100";
export const callCallId = "call-2b9e44@172.28.0.100";
export const subscribeCallId = "sub-51d0e2@172.28.0.40";

// REGISTER, 401, REGISTER, 200 with UAR and MAR on the first REGISTER and
// UAR and SAR on the second (docs/guide/en/registration.md).
function registration(): Message[] {
  const c = new Capture(1, "2026-11-19T09:00:00Z");
  const ue = udp("ue1", "pcscf");
  const pi = udp("pcscf", "icscf");
  const is = udp("icscf", "scscf");
  const icscfHss = tcp("icscf", "hss", 40100, 3868);
  const scscfHss = tcp("scscf", "hss", 40200, 3868);
  const id = registrationCallId;
  const registrar = `sip:${domain}`;

  c.request(ue, "REGISTER", registrar, id, 1);
  c.request(pi, "REGISTER", registrar, id, 1);
  c.cx(icscfHss, "User-Authorization", 300, "UA", "icscf;1;1", user1, id);
  c.request(is, "REGISTER", registrar, id, 1);
  c.cx(scscfHss, "Multimedia-Auth", 303, "MA", "scscf;1;1", user1, id);
  c.response(is, "401 Unauthorized", "REGISTER", id, 1);
  c.response(pi, "401 Unauthorized", "REGISTER", id, 1);
  c.response(ue, "401 Unauthorized", "REGISTER", id, 1);

  c.request(ue, "REGISTER", registrar, id, 2);
  c.request(pi, "REGISTER", registrar, id, 2);
  c.cx(icscfHss, "User-Authorization", 300, "UA", "icscf;1;2", user1, id);
  c.request(is, "REGISTER", registrar, id, 2);
  c.cx(scscfHss, "Server-Assignment", 301, "SA", "scscf;1;2", user1, id);
  c.response(is, "200 OK", "REGISTER", id, 2);
  c.response(pi, "200 OK", "REGISTER", id, 2);
  c.response(ue, "200 OK", "REGISTER", id, 2);
  return c.messages;
}

// The P-CSCF subscribes to the reg event of user 1 (docs/guide/en/
// registration.md, step 11). The I-CSCF's LIR for it is captured while
// the call starts, as in a Compose run of `ue call`; the server links it
// to the SUBSCRIBE, so it stays off the call's ladder.
function regEventSubscribe(c: Capture): void {
  const id = subscribeCallId;
  const pi = udp("pcscf", "icscf");
  const is = udp("icscf", "scscf");
  c.request(pi, "SUBSCRIBE", user1, id, 1);
  c.cx(tcp("icscf", "hss", 40100, 3868), "Location-Info", 302, "LI", "icscf;1;4", user1, id);
  c.request(is, "SUBSCRIBE", user1, id, 1);
  c.response(is, "200 OK", "SUBSCRIBE", id, 1);
  c.response(pi, "200 OK", "SUBSCRIBE", id, 1);
}

// UE 1 calls UE 2 (docs/guide/en/call.md). The INVITE goes ue -> pcscf ->
// scscf -> icscf -> scscf -> pcscf -> ue; from the S-CSCF on it is too
// large for UDP and goes over TCP. Requests inside the dialog follow the
// route set, which skips the I-CSCF.
function call(firstId: number): Message[] {
  const c = new Capture(firstId, "2026-11-19T09:01:00Z");
  const id = callCallId;
  const invitePath: Leg[] = [
    udp("ue1", "pcscf"),
    udp("pcscf", "scscf"),
    tcp("scscf", "icscf", 40001),
    tcp("icscf", "scscf", 40002),
    tcp("scscf", "pcscf", 40003),
    tcp("pcscf", "ue2", 40004),
  ];
  const routeSet: Leg[] = [udp("ue1", "pcscf"), udp("pcscf", "scscf"), udp("scscf", "pcscf"), udp("pcscf", "ue2")];
  const back = (legs: Leg[]) => [...legs].reverse();
  const callee = "tel:+1-202-555-0102";

  for (const leg of invitePath) {
    c.request(leg, "INVITE", callee, id, 1);
    if (leg.server !== "ue2") c.response(leg, "100 Trying", "INVITE", id, 1);
    if (leg.client === "ue1") regEventSubscribe(c);
    if (leg.server === "icscf") {
      c.cx(tcp("icscf", "hss", 40100, 3868), "Location-Info", 302, "LI", "icscf;1;3", user2, id);
      // A watchdog on another connection during the call. It belongs to no
      // call and must stay off the ladder.
      c.watchdog(tcp("scscf", "hss", 40200, 3868));
    }
  }
  for (const leg of back(invitePath)) c.response(leg, "183 Session Progress", "INVITE", id, 1);
  for (const leg of routeSet) c.request(leg, "PRACK", `sip:${hosts.ue2}:5060`, id, 2);
  for (const leg of back(routeSet)) c.response(leg, "200 OK", "PRACK", id, 2);
  for (const leg of back(invitePath)) c.response(leg, "180 Ringing", "INVITE", id, 1);
  for (const leg of back(invitePath)) c.response(leg, "200 OK", "INVITE", id, 1);
  for (const leg of routeSet) c.request(leg, "ACK", `sip:${hosts.ue2}:5060`, id, 1);
  for (const leg of routeSet) c.request(leg, "BYE", `sip:${hosts.ue2}:5060`, id, 3);
  for (const leg of back(routeSet)) c.response(leg, "200 OK", "BYE", id, 3);
  return c.messages;
}

export const registrationMessages = registration();
export const callMessages = call(registrationMessages.length + 1);

// Everything the server captured, in capture order. GET /api/messages
// with call_id returns the SIP messages with that Call-ID and the Cx
// messages linked to them.
export const allMessages: Message[] = [...registrationMessages, ...callMessages];

export const calls: Call[] = [
  {
    call_id: callCallId,
    legs: [callCallId],
    from: user1,
    to: "tel:+1-202-555-0102",
    state: "terminated",
    status: 200,
    started: callMessages[0]?.time ?? "",
    ended: callMessages[callMessages.length - 1]?.time ?? null,
    messages: callMessages.filter((m) => m.protocol === "sip" && m.call_id === callCallId).length,
  },
];

export const registrations: Registration[] = [
  {
    impu: user1,
    impi: `001010000000001@${domain}`,
    contact: `sip:+12025550101@${hosts.ue1}:5060`,
    expires: "2026-11-19T10:00:00Z",
    state: "registered",
    scscf: `sip:scscf.${domain}:5060`,
    call_id: registrationCallId,
  },
  {
    impu: "tel:+12025550101",
    impi: `001010000000001@${domain}`,
    contact: `sip:+12025550101@${hosts.ue1}:5060`,
    expires: "2026-11-19T10:00:00Z",
    state: "registered",
    scscf: `sip:scscf.${domain}:5060`,
    call_id: registrationCallId,
  },
];

export const subscribers: Subscriber[] = [1, 2, 3, 4].map((n) => ({
  private_identity: `00101000000000${n}@${domain}`,
  public_identities: [`sip:+120255501${String(n).padStart(2, "0")}@${domain}`, `tel:+120255501${String(n).padStart(2, "0")}`],
  customized_alerting_tone: n === 2 ? { tone: "cat1", active: false } : null,
}));
