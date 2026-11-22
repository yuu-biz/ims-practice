// Turns the messages of one call into a ladder diagram: columns for the
// nodes and arrows between them. Pure functions, so that the tests need
// no browser.

import type { Message, Protocol } from "./api.ts";

// A column is one host. Ports do not identify a node: TCP connections
// use ephemeral source ports, and a node speaks SIP and Diameter from
// different ports.
export interface Column {
  host: string;
  // The role name the node reports in HEP. Undefined for a host that
  // sends no HEP, such as a softphone.
  node: string | undefined;
}

// One message on the wire. Its sender and its receiver may both have
// reported it, so an arrow holds one or two captured copies.
export interface Arrow {
  from: string;
  to: string;
  protocol: Protocol;
  summary: string;
  messages: Message[];
}

export interface Ladder {
  columns: Column[];
  arrows: Arrow[];
}

// The column order of the contract: the caller's UE on the left, then
// the network nodes in this order, then the other UEs. Hosts that send
// no HEP are endpoints too, because every network node reports. The
// PCRF of the epc node, which only the P-CSCF talks to over Rx, stands
// next to it. An application server stands next to the S-CSCF that
// sends it requests by the iFCs, before the HSS that both of them ask,
// and the MRFC next to the AS that drives it over Mr'. The media nodes,
// the IMS-AGW and the MRFP, come last.
const networkOrder: Record<string, number> = {
  pcscf: 1,
  epc: 2,
  icscf: 3,
  scscf: 4,
  ipsmgw: 5,
  mmtel: 5,
  mrfc: 5.5,
  hss: 6,
  agw: 7,
  mrfp: 7,
};
const callerRank = 0;
const otherNetworkRank = 7;
const calleeRank = 8;

// The ladder of the messages with one call_id: the SIP messages of a call
// or a registration, and the Cx messages the server linked to them.
export function buildLadder(messages: Message[]): Ladder {
  const ordered = [...messages].sort(captureOrder);
  const arrows = pairArrows(ordered);
  return { columns: orderColumns(ordered, arrows), arrows };
}

// The order of /api/messages: capture time, then id. The ids alone are
// the order of arrival at the server, which differs across nodes. The
// times are UTC with up to nine fractional digits (see parseMessage), so
// padding the fraction makes them compare as strings.
export function captureOrder(a: Message, b: Message): number {
  const ta = paddedTime(a.time);
  const tb = paddedTime(b.time);
  return ta < tb ? -1 : ta > tb ? 1 : a.id - b.id;
}

// "2026-11-19T09:00:00.001Z" -> "2026-11-19T09:00:00.001000000".
function paddedTime(time: string): string {
  const [seconds = "", fraction = ""] = time.slice(0, -1).split(".");
  return `${seconds}.${fraction.padEnd(9, "0")}`;
}

// A send and a recv with the same payload, src and dst are one message on
// the wire. Each key keeps a queue of arrows still missing a side, so that
// identical retransmissions stay separate arrows, and a recv captured
// before its send still pairs.
export function pairArrows(ordered: Message[]): Arrow[] {
  const arrows: Arrow[] = [];
  const unpaired = new Map<string, Arrow[]>();
  for (const m of ordered) {
    const key = JSON.stringify([m.protocol, m.src, m.dst, m.payload]);
    const waiting = unpaired.get(key) ?? [];
    const match = waiting.findIndex(
      (a) => a.messages.length < 2 && (m.direction === "" || !a.messages.some((seen) => seen.direction === m.direction)),
    );
    const partner = match >= 0 ? waiting[match] : undefined;
    if (partner) {
      partner.messages.push(m);
      waiting.splice(match, 1);
      continue;
    }
    const arrow: Arrow = {
      from: hostOf(m.src),
      to: hostOf(m.dst),
      protocol: m.protocol,
      summary: m.summary,
      messages: [m],
    };
    arrows.push(arrow);
    unpaired.set(key, [...waiting, arrow]);
  }
  return arrows;
}

// How a Diameter message came into the ladder, for its details. A SIP
// message carries the Call-ID that the ladder draws. Nothing in a Cx or
// Rx message names a SIP message, so the server linked its session to
// one by a rule (causeWindow in internal/lab/causes.go): every Diameter
// message with a call_id got it that way.
export function inferredLink(m: Message): string | null {
  if (m.protocol !== "diameter" || m.call_id === "") return null;
  return "Inferred by the Lab, not carried in the message: the request that began its Diameter session was sent by its node at most 2 s after that node received a SIP message of this ladder about the same user.";
}

export function orderColumns(ordered: Message[], arrows: Arrow[]): Column[] {
  const nodeOf = new Map<string, string>();
  for (const m of ordered) if (m.node !== "") nodeOf.set(localHost(m), m.node);

  const firstSeen: string[] = [];
  for (const a of arrows) {
    for (const host of [a.from, a.to]) {
      if (!firstSeen.includes(host)) firstSeen.push(host);
    }
  }

  const isEndpoint = (host: string) => {
    const node = nodeOf.get(host);
    return node === undefined || node === "ue";
  };
  const caller = arrows.map((a) => a.from).find(isEndpoint);
  const rank = (host: string): number => {
    if (host === caller) return callerRank;
    if (isEndpoint(host)) return calleeRank;
    return networkOrder[nodeOf.get(host) ?? ""] ?? otherNetworkRank;
  };

  return firstSeen
    .map((host, seen) => ({ host, seen, rank: rank(host) }))
    .sort((a, b) => a.rank - b.rank || a.seen - b.seen)
    .map(({ host }) => ({ host, node: nodeOf.get(host) }));
}

// The node that reported a message is at its src when it sent it and at
// its dst when it received it.
function localHost(m: Message): string {
  return hostOf(m.direction === "send" ? m.src : m.dst);
}

// "172.28.0.40:5060" -> "172.28.0.40", "[fd00::1]:5060" -> "fd00::1".
export function hostOf(address: string): string {
  if (address.startsWith("[")) {
    const end = address.indexOf("]");
    return end > 0 ? address.slice(1, end) : address;
  }
  const colon = address.lastIndexOf(":");
  return colon >= 0 && address.indexOf(":") === colon ? address.slice(0, colon) : address;
}
