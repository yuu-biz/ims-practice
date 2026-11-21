import { describe, expect, it } from "vitest";
import type { Message } from "../src/api.ts";
import { columnLabel } from "../src/ladder-svg.ts";
import { buildLadder, captureOrder, hostOf, type Ladder } from "../src/ladder.ts";
import { allMessages, callCallId, hosts, registrationCallId, subscribeCallId } from "./fixtures.ts";

const nameOf = new Map<string, string>(Object.entries(hosts).map(([name, host]) => [host, name]));

// What GET /api/messages?call_id= returns: the SIP messages with that
// Call-ID and the Cx messages the server linked to them.
const withCallId = (callId: string) => allMessages.filter((m) => m.call_id === callId);

const columnNames = (ladder: Ladder) => ladder.columns.map((c) => nameOf.get(c.host) ?? c.host);
const arrowLines = (ladder: Ladder) =>
  ladder.arrows.map((a) => `${nameOf.get(a.from)} -> ${nameOf.get(a.to)}: ${a.summary}`);

describe("registration ladder", () => {
  const ladder = buildLadder(withCallId(registrationCallId));

  it("orders the columns UE, P-CSCF, I-CSCF, S-CSCF, HSS", () => {
    expect(columnNames(ladder)).toEqual(["ue1", "pcscf", "icscf", "scscf", "hss"]);
  });

  it("draws each hop once, with the Cx messages of the registration", () => {
    const registrar = "REGISTER sip:ims.mnc001.mcc001.3gppnetwork.org";
    const round = (final: string, cx: string) => [
      `ue1 -> pcscf: ${registrar}`,
      `pcscf -> icscf: ${registrar}`,
      "icscf -> hss: UAR",
      "hss -> icscf: UAA 2001",
      `icscf -> scscf: ${registrar}`,
      `scscf -> hss: ${cx}R`,
      `hss -> scscf: ${cx}A 2001`,
      `scscf -> icscf: ${final} (REGISTER)`,
      `icscf -> pcscf: ${final} (REGISTER)`,
      `pcscf -> ue1: ${final} (REGISTER)`,
    ];
    expect(arrowLines(ladder)).toEqual([...round("401 Unauthorized", "MA"), ...round("200 OK", "SA")]);
    for (const arrow of ladder.arrows) {
      expect(arrow.messages.map((m) => m.direction).sort()).toEqual(["recv", "send"]);
    }
  });
});

describe("call ladder", () => {
  const ladder = buildLadder(withCallId(callCallId));

  it("orders the columns caller, P-CSCF, I-CSCF, S-CSCF, HSS, callee", () => {
    expect(columnNames(ladder)).toEqual(["ue1", "pcscf", "icscf", "scscf", "hss", "ue2"]);
  });

  it("follows the INVITE through both S-CSCF passes and the LIR", () => {
    const invite = "INVITE tel:+1-202-555-0102";
    expect(arrowLines(ladder).slice(0, 14)).toEqual([
      `ue1 -> pcscf: ${invite}`,
      "pcscf -> ue1: 100 Trying (INVITE)",
      `pcscf -> scscf: ${invite}`,
      "scscf -> pcscf: 100 Trying (INVITE)",
      `scscf -> icscf: ${invite}`,
      "icscf -> scscf: 100 Trying (INVITE)",
      "icscf -> hss: LIR",
      "hss -> icscf: LIA 2001",
      `icscf -> scscf: ${invite}`,
      "scscf -> icscf: 100 Trying (INVITE)",
      `scscf -> pcscf: ${invite}`,
      "pcscf -> scscf: 100 Trying (INVITE)",
      `pcscf -> ue2: ${invite}`,
      "ue2 -> pcscf: 183 Session Progress (INVITE)",
    ]);
  });

  it("has one arrow per hop and no Diameter base messages", () => {
    // INVITE 6, 100 Trying 5, LIR and LIA 2, 183 6, PRACK 4, its 200 4,
    // 180 6, 200 (INVITE) 6, ACK 4, BYE 4, its 200 4.
    expect(ladder.arrows).toHaveLength(51);
    expect(ladder.arrows.filter((a) => a.protocol === "diameter").map((a) => a.summary)).toEqual(["LIR", "LIA 2001"]);
  });

  it("leaves out the LIR of the reg event SUBSCRIBE captured while the call started", () => {
    const callIds = withCallId(callCallId).map((m) => m.id);
    const subscribeLir = allMessages.find((m) => m.call_id === subscribeCallId && m.summary === "LIR");
    expect(subscribeLir?.id).toBeGreaterThan(Math.min(...callIds));
    expect(subscribeLir?.id).toBeLessThan(Math.max(...callIds));
    const lirs = ladder.arrows.filter((a) => a.summary === "LIR");
    expect(lirs.map((a) => a.messages.map((m) => m.correlation_id))).toEqual([["icscf;1;3", "icscf;1;3"]]);
  });

  it("ends with the BYE transaction", () => {
    expect(arrowLines(ladder).slice(-4)).toEqual([
      "ue2 -> pcscf: 200 OK (BYE)",
      "pcscf -> scscf: 200 OK (BYE)",
      "scscf -> pcscf: 200 OK (BYE)",
      "pcscf -> ue1: 200 OK (BYE)",
    ]);
  });
});

let nextId = 1000;
function captured(over: Partial<Message> & Pick<Message, "node" | "direction">): Message {
  return {
    id: nextId++,
    time: "2026-11-19T09:00:00.000000000Z",
    protocol: "sip",
    transport: "udp",
    src: "172.28.0.100:5060",
    dst: "172.28.0.40:5060",
    summary: "REGISTER sip:ims.mnc001.mcc001.3gppnetwork.org",
    correlation_id: "c1",
    call_id: "c1",
    payload: "REGISTER sip:ims.mnc001.mcc001.3gppnetwork.org SIP/2.0\r\n\r\n",
    refs: [],
    sms: null,
    ...over,
  };
}

describe("pairing of captured copies", () => {
  it("pairs a recv that was captured before its send", () => {
    const ladder = buildLadder([captured({ node: "pcscf", direction: "recv" }), captured({ node: "ue", direction: "send" })]);
    expect(ladder.arrows).toHaveLength(1);
    expect(ladder.arrows[0]?.messages).toHaveLength(2);
  });

  it("keeps identical retransmissions as separate arrows", () => {
    const ladder = buildLadder([
      captured({ node: "ue", direction: "send" }),
      captured({ node: "ue", direction: "send" }),
      captured({ node: "pcscf", direction: "recv" }),
      captured({ node: "pcscf", direction: "recv" }),
    ]);
    expect(ladder.arrows.map((a) => a.messages.map((m) => m.direction))).toEqual([
      ["send", "recv"],
      ["send", "recv"],
    ]);
  });

  it("pairs the two copies of a message a node sends to itself", () => {
    const self = { node: "scscf", src: "172.28.0.30:5060", dst: "172.28.0.30:5060", direction: "" as const };
    const ladder = buildLadder([captured(self), captured(self)]);
    expect(ladder.arrows).toHaveLength(1);
    expect(ladder.arrows[0]?.messages).toHaveLength(2);
  });

  it("does not pair copies whose payloads differ", () => {
    const ladder = buildLadder([
      captured({ node: "ue", direction: "send" }),
      captured({ node: "pcscf", direction: "recv", payload: "REGISTER sip:other SIP/2.0\r\n\r\n" }),
    ]);
    expect(ladder.arrows).toHaveLength(2);
  });

  it("draws a message that only one node reported, and puts the silent host where a UE goes", () => {
    const softphone = "172.28.0.1";
    const ladder = buildLadder([
      captured({ node: "pcscf", direction: "recv", src: `${softphone}:40000` }),
      captured({
        node: "pcscf",
        direction: "send",
        src: "172.28.0.40:5060",
        dst: "172.28.0.20:5060",
        payload: "REGISTER forwarded",
      }),
      captured({
        node: "icscf",
        direction: "recv",
        src: "172.28.0.40:5060",
        dst: "172.28.0.20:5060",
        payload: "REGISTER forwarded",
      }),
    ]);
    expect(ladder.arrows.map((a) => a.messages.length)).toEqual([1, 2]);
    expect(ladder.columns).toEqual([
      { host: softphone, node: undefined },
      { host: "172.28.0.40", node: "pcscf" },
      { host: "172.28.0.20", node: "icscf" },
    ]);
  });
});

describe("short message ladder", () => {
  // A short message from UE 1 as the nodes report it (TS 24.341 B.5):
  // to the S-CSCF, on to the IP-SM-GW by the iFC, and the IP-SM-GW's
  // UDR to the HSS. Only the sender's copy of each is needed here.
  const hop = (node: string, src: string, dst: string, protocol: Message["protocol"], summary: string) =>
    captured({ node, direction: "send", src, dst, protocol, summary, payload: summary, call_id: "sms-1" });
  const ladder = buildLadder([
    hop("ue", "172.28.0.100:5060", "172.28.0.40:5060", "sip", "MESSAGE tel:+12025550100"),
    hop("pcscf", "172.28.0.40:5060", "172.28.0.30:5060", "sip", "MESSAGE tel:+12025550100 (S-CSCF)"),
    hop("scscf", "172.28.0.30:5060", "172.28.0.70:5060", "sip", "MESSAGE tel:+12025550100 (IP-SM-GW)"),
    hop("ipsmgw", "172.28.0.70:46000", "172.28.0.10:3868", "diameter", "UDR"),
    hop("hss", "172.28.0.10:3868", "172.28.0.70:46000", "diameter", "UDA 2001"),
    hop("ipsmgw", "172.28.0.70:5060", "172.28.0.30:5060", "sip", "202 Accepted (MESSAGE)"),
  ]);

  it("puts the IP-SM-GW between the S-CSCF and the HSS", () => {
    expect(ladder.columns.map((c) => c.host)).toEqual(["172.28.0.100", "172.28.0.40", "172.28.0.30", "172.28.0.70", "172.28.0.10"]);
  });

  it("labels the column IP-SM-GW", () => {
    expect(ladder.columns.map(columnLabel)).toEqual(["UE", "P-CSCF", "S-CSCF", "IP-SM-GW", "HSS"]);
  });
});

describe("call through the MMTel AS", () => {
  // The leg of UE 1's call between UE 1 and the caller's MMTel AS: the
  // S-CSCF sends the INVITE to the AS by the iFC, and the AS answers it
  // as a UA (TS 24.229 5.7.5). The AS's own leg has a Call-ID of its own.
  const hop = (node: string, src: string, dst: string, summary: string) =>
    captured({ node, direction: "send", src, dst, protocol: "sip", summary, payload: summary, call_id: "call-1" });
  const ladder = buildLadder([
    hop("ue", "172.28.0.100:5060", "172.28.0.40:5060", "INVITE tel:+12025550102"),
    hop("pcscf", "172.28.0.40:5060", "172.28.0.30:5060", "INVITE tel:+12025550102"),
    hop("scscf", "172.28.0.30:5060", "172.28.0.75:5060", "INVITE tel:+12025550102 (MMTel AS)"),
    hop("mmtel", "172.28.0.75:5060", "172.28.0.30:5060", "180 Ringing (INVITE)"),
  ]);

  it("puts the MMTel AS next to the S-CSCF", () => {
    expect(ladder.columns.map((c) => c.host)).toEqual(["172.28.0.100", "172.28.0.40", "172.28.0.30", "172.28.0.75"]);
  });

  it("labels the column MMTel AS", () => {
    expect(ladder.columns.map(columnLabel)).toEqual(["UE", "P-CSCF", "S-CSCF", "MMTel AS"]);
  });
});

describe("call with policy control", () => {
  // The caller's side: the 183 makes the P-CSCF send the PCRF an AAR
  // for the media (TS 29.214 A.1), which the dashboard links to the call.
  const sip = (node: string, src: string, dst: string, summary: string) =>
    captured({ node, direction: "send", src, dst, protocol: "sip", summary, payload: summary, call_id: "call-1" });
  const rx = (node: string, src: string, dst: string, summary: string) =>
    captured({ node, direction: "send", src, dst, protocol: "diameter", summary, payload: summary, call_id: "call-1" });
  const ladder = buildLadder([
    sip("ue", "172.28.0.120:5062", "172.28.0.40:5064", "INVITE tel:+12025550112"),
    sip("pcscf", "172.28.0.40:5060", "172.28.0.30:5060", "INVITE tel:+12025550112"),
    sip("scscf", "172.28.0.30:5060", "172.28.0.40:5060", "183 Session Progress (INVITE)"),
    rx("pcscf", "172.28.0.40:40732", "172.28.0.80:3868", "AAR"),
    rx("epc", "172.28.0.80:3868", "172.28.0.40:40732", "AAA 2001"),
    sip("pcscf", "172.28.0.40:5064", "172.28.0.120:5062", "183 Session Progress (INVITE)"),
  ]);

  it("puts the PCRF next to the P-CSCF", () => {
    expect(ladder.columns.map((c) => c.host)).toEqual(["172.28.0.120", "172.28.0.40", "172.28.0.80", "172.28.0.30"]);
  });

  it("labels the column PCRF", () => {
    expect(ladder.columns.map(columnLabel)).toEqual(["UE", "P-CSCF", "PCRF", "S-CSCF"]);
  });
});

describe("AS leg to the MRFC", () => {
  // The MMTel AS asks the MRFC for an announcement with RFC 4240 netann,
  // on a dialog of its own (TS 24.229 10.2.2.2).
  const hop = (node: string, src: string, dst: string, summary: string) =>
    captured({ node, direction: "send", src, dst, protocol: "sip", summary, payload: summary, call_id: "annc-1" });
  const ladder = buildLadder([
    hop("mmtel", "172.28.0.75:5060", "172.28.0.85:5060", "INVITE sip:annc@mrfc.ims.mnc001.mcc001.3gppnetwork.org"),
    hop("mrfc", "172.28.0.85:5060", "172.28.0.75:5060", "200 OK (INVITE)"),
    hop("mrfc", "172.28.0.85:5060", "172.28.0.75:5060", "BYE sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060"),
  ]);

  it("puts the MRFC after the AS and labels both", () => {
    expect(ladder.columns.map((c) => c.host)).toEqual(["172.28.0.75", "172.28.0.85"]);
    expect(ladder.columns.map(columnLabel)).toEqual(["MMTel AS", "MRFC"]);
  });

  it("labels the MRFP", () => {
    expect(columnLabel({ host: "172.28.0.86", node: "mrfp" })).toBe("MRFP");
  });
});

describe("capture order", () => {
  it("orders by capture time, then by id, not by arrival", () => {
    const at = (id: number, time: string) => captured({ id, time, node: "ue", direction: "send", summary: String(id) });
    const ordered = [
      at(1, "2026-11-19T09:00:00.001Z"),
      at(2, "2026-11-19T09:00:00.0005Z"),
      at(3, "2026-11-19T09:00:00Z"),
      at(5, "2026-11-19T09:00:00.000500000Z"),
      at(4, "2026-11-19T09:00:00.000500000Z"),
    ].sort(captureOrder);
    expect(ordered.map((m) => m.id)).toEqual([3, 2, 4, 5, 1]);
  });

  it("draws the arrows in capture order", () => {
    const ladder = buildLadder([
      captured({ id: 1, time: "2026-11-19T09:00:00.002Z", node: "pcscf", direction: "send", summary: "second", payload: "b" }),
      captured({ id: 2, time: "2026-11-19T09:00:00.001Z", node: "ue", direction: "send", summary: "first", payload: "a" }),
    ]);
    expect(ladder.arrows.map((a) => a.summary)).toEqual(["first", "second"]);
  });
});

describe("hostOf", () => {
  it("drops the port of IPv4 and IPv6 addresses", () => {
    expect(hostOf("172.28.0.40:5060")).toBe("172.28.0.40");
    expect(hostOf("[fd00::40]:5060")).toBe("fd00::40");
  });
});
