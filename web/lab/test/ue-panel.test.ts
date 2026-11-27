import { afterEach, describe, expect, it, vi } from "vitest";
import {
  fetchUESimulators,
  HttpError,
  parseUEStatus,
  postUEAction,
  postUEDTMF,
  postUESMS,
  putAnswerPolicy,
  type UECall,
  type UESentSMS,
  type UECodec,
  type UESession,
  type UESimulator,
} from "../src/api.ts";
import { describeSentSMS, describeSession, enabledControls, suggestedNumber, suggestedTarget } from "../src/ue-panel.ts";
import { subscribers, user1, user2 } from "./fixtures.ts";

function serve(status: number, body: string): void {
  vi.stubGlobal("fetch", async () => new Response(body, { status }));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

const idle = { impu: user1, registered: true, call: null, answer_policy: { mode: "auto", ring_seconds: 1 } };
const ringing = {
  call_id: "call-1@172.28.0.110",
  direction: "incoming",
  peer: "tel:+12025550101",
  state: "ringing",
  started: "2026-11-19T09:00:00.5Z",
};

describe("UE simulator responses", () => {
  it("reads a reachable UE with a call and an unreachable one", async () => {
    serve(
      200,
      JSON.stringify([
        { name: "phone1", reachable: true, status: { ...idle, call: ringing, answer_policy: { mode: "manual" } } },
        { name: "phone2", reachable: false, status: null, error: "connection refused" },
      ]),
    );
    expect(await fetchUESimulators()).toEqual([
      {
        name: "phone1",
        reachable: true,
        status: {
          impu: user1,
          registered: true,
          call: { ...ringing, waiting: false, session: null },
          calls: [],
          conference: null,
          answer_policy: { mode: "manual" },
          sms: { sent: [], received: [] },
        },
      },
      { name: "phone2", reachable: false, error: "connection refused" },
    ]);
  });

  it("reads a call whose INVITE is not out yet, without a Call-ID", async () => {
    const calling = { direction: "outgoing", peer: "tel:+12025550102", state: "calling", started: "2026-11-19T09:00:00Z" };
    serve(200, JSON.stringify([{ name: "phone1", reachable: true, status: { ...idle, call: calling } }]));
    const [ue] = await fetchUESimulators();
    expect(ue?.reachable && ue.status.call?.call_id).toBe("");
  });

  it("keeps the ring time and the reject status of the policy", async () => {
    serve(200, JSON.stringify({ mode: "reject", reject_status: 486 }));
    expect(await putAnswerPolicy("phone1", "reject")).toEqual({ mode: "reject", reject_status: 486 });
    serve(200, JSON.stringify({ mode: "auto" }));
    await expect(putAnswerPolicy("phone1", "auto")).rejects.toThrow(/ring_seconds/);
  });

  it("rejects a status with a call state the UE does not have", async () => {
    serve(200, JSON.stringify([{ name: "phone1", reachable: true, status: { ...idle, call: { ...ringing, state: "early" } } }]));
    await expect(fetchUESimulators()).rejects.toThrow(/state/);
  });

  it("rejects an entry without reachable", async () => {
    serve(200, JSON.stringify([{ name: "phone1", status: idle }]));
    await expect(fetchUESimulators()).rejects.toThrow(/reachable/);
  });

  it("reports the text of a refused action, not its JSON", async () => {
    serve(409, JSON.stringify({ error: "there is no incoming call that rings" }));
    await expect(postUEAction("phone1", "answer")).rejects.toEqual(new HttpError(409, "there is no incoming call that rings"));
  });

  it("asks the UE through the Lab, with the name escaped", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(idle)));
    vi.stubGlobal("fetch", fetch);
    await postUEAction("ue a", "register");
    expect(fetch).toHaveBeenCalledWith("/api/ues/ue%20a/register", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
    });
  });
});

function reachable(registered: boolean, call: Partial<UECall> | null, ...others: Partial<UECall>[]): UESimulator {
  const full = (c: Partial<UECall>, i: number): UECall => ({
    call_id: `c${i}`,
    direction: "outgoing",
    peer: "p",
    state: "active",
    started: "",
    waiting: false,
    session: null,
    ...c,
  });
  const calls = call === null ? [] : [call, ...others].map(full);
  return {
    name: "phone1",
    reachable: true,
    status: {
      impu: user1,
      registered,
      call: calls[0] ?? null,
      calls,
      conference: null,
      answer_policy: { mode: "manual" },
      sms: { sent: [], received: [] },
    },
  };
}

// The session of an answered AMR-WB call with its preconditions met, as
// the UE reports it (ue.SessionStatus).
const answeredSession: UESession = {
  codec: { name: "AMR-WB", payload_type: 96, clock_rate: 16000, payload_format: "bandwidth-efficient", telephone_event: 100 },
  direction: "sendrecv",
  peer_direction: "sendrecv",
  preconditions: {
    met: true,
    rows: [
      { segment: "local", direction: "send", reserved: true, strength: "mandatory", confirm: false },
      { segment: "remote", direction: "recv", reserved: false, strength: "optional", confirm: true },
    ],
  },
  receive_codec: { name: "AMR-WB", payload_type: 97, clock_rate: 16000, payload_format: "octet-aligned", telephone_event: 101 },
  media: { packets_sent: 12, packets_received: 10, frames_sent: 12, frames_received: 10 },
  media_resources: true,
  reservation_needed: true,
  held: false,
  ringback: "",
  dtmf_sent: "12#",
  dtmf_received: "",
};

describe("UE simulator controls", () => {
  const off = {
    register: false,
    deregister: false,
    call: false,
    answer: false,
    hangup: false,
    hold: false,
    resume: false,
    dtmf: false,
    swap: false,
    merge: false,
    answerPolicy: false,
    sms: false,
  };

  it.each([
    ["an unregistered UE", reachable(false, null), { register: true, deregister: false, call: false, answer: false, hangup: false, sms: false }],
    ["a registered UE without a call", reachable(true, null), { register: true, deregister: true, call: true, answer: false, hangup: false, sms: true }],
    [
      "a UE whose outgoing call is being set up",
      reachable(true, { direction: "outgoing", state: "calling" }),
      { register: true, deregister: false, call: false, answer: false, hangup: true, sms: true },
    ],
    [
      "a UE whose incoming call rings",
      reachable(true, { direction: "incoming", state: "ringing" }),
      { register: true, deregister: false, call: false, answer: true, hangup: true, sms: true },
    ],
    [
      "a UE whose outgoing call rings at the callee",
      reachable(true, { direction: "outgoing", state: "ringing" }),
      { register: true, deregister: false, call: false, answer: false, hangup: true, sms: true },
    ],
    [
      "a UE in an answered call",
      reachable(true, { direction: "incoming", state: "active" }),
      { register: true, deregister: false, call: false, answer: false, hangup: true, sms: true },
    ],
  ])("offers %s what its control API allows", (_, ue, want) => {
    expect(enabledControls(ue, false)).toEqual({ hold: false, resume: false, dtmf: false, swap: false, merge: false, ...want, answerPolicy: true });
  });

  it("answers a waiting call, and calls again only while the call is held", () => {
    const waiting = reachable(
      true,
      { direction: "outgoing", state: "active", session: answeredSession },
      { direction: "incoming", state: "ringing", waiting: true },
    );
    expect(enabledControls(waiting, false)).toMatchObject({ answer: true, call: false, swap: false, merge: false });
    const held = reachable(true, { state: "active", session: { ...answeredSession, held: true } });
    expect(enabledControls(held, false)).toMatchObject({ call: true, resume: true });
  });

  it("swaps an answered call and a held one, and merges two answered calls", () => {
    const two = reachable(
      true,
      { state: "active", session: { ...answeredSession, held: true } },
      { state: "active", session: answeredSession },
    );
    expect(enabledControls(two, false)).toMatchObject({ swap: true, merge: true, call: false });
    if (!two.reachable) throw new Error("unreachable");
    const inConference: UESimulator = { ...two, status: { ...two.status, conference: { uri: "sip:conf", users: [] } } };
    expect(enabledControls(inConference, false)).toMatchObject({ merge: false });
  });

  it.each([
    ["an answered call", answeredSession, { hold: true, resume: false, dtmf: true }],
    ["a call this UE holds", { ...answeredSession, held: true }, { hold: false, resume: true, dtmf: true }],
  ])("offers hold, resume and DTMF by the session of %s", (_, session, want) => {
    expect(enabledControls(reachable(true, { state: "active", session }), false)).toMatchObject(want);
  });

  it("offers neither hold nor DTMF before the answer", () => {
    expect(enabledControls(reachable(true, { state: "ringing", session: answeredSession }), false)).toMatchObject({
      hold: false,
      resume: false,
      dtmf: false,
    });
  });

  it("offers nothing for a UE that does not answer", () => {
    expect(enabledControls({ name: "phone1", reachable: false, error: "down" }, false)).toEqual(off);
  });

  it("offers nothing while a request to the UE is under way", () => {
    expect(enabledControls(reachable(true, null), true)).toEqual(off);
  });
});

describe("the session of a call", () => {
  it("reads the codec, the directions and the precondition table, with Go's omitted fields", async () => {
    const session = {
      codec: { name: "AMR", payload_type: 97, clock_rate: 8000 },
      direction: "sendonly",
      peer_direction: "recvonly",
      preconditions: { met: false, rows: [{ segment: "local", direction: "send", reserved: false, strength: "mandatory" }] },
      media_resources: false,
      reservation_needed: true,
      held: true,
      media: { packets_sent: 0, packets_received: 3, rtcp_sent: 0, rtcp_received: 0 },
    };
    serve(200, JSON.stringify([{ name: "phone1", reachable: true, status: { ...idle, call: { ...ringing, state: "active", session } } }]));
    const [ue] = await fetchUESimulators();
    expect(ue?.reachable && ue.status.call?.session).toEqual({
      codec: { name: "AMR", payload_type: 97, clock_rate: 8000, payload_format: "", telephone_event: null },
      receive_codec: null,
      media: { packets_sent: 0, packets_received: 3, frames_sent: 0, frames_received: 0 },
      direction: "sendonly",
      peer_direction: "recvonly",
      preconditions: { met: false, rows: [{ segment: "local", direction: "send", reserved: false, strength: "mandatory", confirm: false }] },
      media_resources: false,
      reservation_needed: true,
      held: true,
      ringback: "",
      dtmf_sent: "",
      dtmf_received: "",
    });
  });

  it("describes the codec, the directions, the preconditions and the DTMF digits", () => {
    expect(describeSession({ ...answeredSession, held: true, direction: "sendonly", peer_direction: "recvonly" })).toEqual([
      "format sent (peer's SDP): AMR-WB/16000, payload type 96, bandwidth-efficient, DTMF payload type 100",
      "format received (this UE's SDP): AMR-WB/16000, payload type 97, octet-aligned, DTMF payload type 101",
      "RTP packets received: 10 (sent 12)",
      "AMR frames received: 10 (sent 12)",
      "sendonly, the peer recvonly, held by this UE",
      "preconditions met: local send reserved (mandatory); remote recv not reserved (optional, confirm)",
      "media resources reserved",
      "DTMF sent 12#, received none",
    ]);
  });

  it("counts RTP packets apart from AMR frames, and gives no frames for a call that is not AMR", () => {
    const pcmu: UECodec = { name: "PCMU", payload_type: 0, clock_rate: 8000, payload_format: "", telephone_event: null };
    const g711: UESession = {
      ...answeredSession,
      codec: pcmu,
      receive_codec: pcmu,
      media: { packets_sent: 5, packets_received: 4, frames_sent: 0, frames_received: 0 },
      preconditions: null,
      dtmf_sent: "",
    };
    expect(describeSession(g711).slice(0, 4)).toEqual([
      "format sent (peer's SDP): PCMU/8000, payload type 0",
      "format received (this UE's SDP): PCMU/8000, payload type 0",
      "RTP packets received: 4 (sent 5)",
      "AMR frames received: not applicable (the codec is not AMR)",
    ]);
  });

  it("says when the codec is not settled and leaves out what is not there", () => {
    const early: UESession = { ...answeredSession, codec: null, direction: "", preconditions: null, media_resources: false, dtmf_sent: "" };
    expect(describeSession(early)).toEqual(["codec not settled yet", "no media resources yet"]);
  });

  it("says that the access network needs no reservation", () => {
    const softphone: UESession = { ...answeredSession, preconditions: null, reservation_needed: false, dtmf_sent: "" };
    expect(describeSession(softphone)).toEqual([
      "format sent (peer's SDP): AMR-WB/16000, payload type 96, bandwidth-efficient, DTMF payload type 100",
      "format received (this UE's SDP): AMR-WB/16000, payload type 97, octet-aligned, DTMF payload type 101",
      "RTP packets received: 10 (sent 12)",
      "AMR frames received: 10 (sent 12)",
      "sendrecv, the peer sendrecv",
      "no reservation of media resources needed on this access network",
    ]);
  });

  it("sends the digits to the UE", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(idle)));
    vi.stubGlobal("fetch", fetch);
    await postUEDTMF("phone1", "12#");
    expect(fetch).toHaveBeenCalledWith("/api/ues/phone1/dtmf", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ digits: "12#" }),
    });
  });
});

describe("the call target", () => {
  const a = reachable(true, null);
  const b: UESimulator = { name: "phone2", reachable: true, status: { ...idle, impu: user2, calls: [], conference: null, answer_policy: { mode: "manual" }, sms: { sent: [], received: [] } } };

  it("is the tel URI of the other UE's subscription", () => {
    expect(suggestedTarget(a, [a, b], subscribers)).toBe("tel:+12025550102");
    expect(suggestedTarget(b, [a, b], subscribers)).toBe("tel:+12025550101");
  });

  it("is the other UE's IMPU without its subscription", () => {
    expect(suggestedTarget(a, [a, b], [])).toBe(user2);
  });

  it("is empty without another UE that answers", () => {
    expect(suggestedTarget(a, [a, { name: "phone2", reachable: false, error: "down" }], subscribers)).toBe("");
  });
});

// A short message as the UE reports it, accepted and reported on.
const sentSMS = {
  call_id: "sms-1@172.28.0.110",
  to: "+12025550102",
  text: "Hello, Bob!",
  message_reference: 0,
  rp_message_reference: 0,
  status_report_requested: true,
  sent: "2026-11-20T03:10:29.157738866Z",
  state: "accepted",
  service_centre_time_stamp: "2026-11-20T03:10:29Z",
  status_report: { status: 0, discharge_time: "2026-11-20T03:10:29Z", received: "2026-11-20T03:10:29.165Z" },
};

describe("short messages", () => {
  it("reads the history in the status, with Go's null for an empty list", () => {
    const status = parseUEStatus({ ...idle, sms: { sent: [sentSMS], received: null } });
    expect(status.sms.received).toEqual([]);
    expect(status.sms.sent).toEqual([
      {
        call_id: "sms-1@172.28.0.110",
        to: "+12025550102",
        text: "Hello, Bob!",
        status_report_requested: true,
        sent: "2026-11-20T03:10:29.157738866Z",
        state: "accepted",
        rp_cause: null,
        failure_cause: null,
        status_report: { status: 0, discharge_time: "2026-11-20T03:10:29Z", received: "2026-11-20T03:10:29.165Z" },
      },
    ]);
  });

  it("rejects a submit state the UE does not have", () => {
    expect(() => parseUEStatus({ ...idle, sms: { sent: [{ ...sentSMS, state: "delivered" }], received: [] } })).toThrow(/state/);
  });

  it("sends the number, the text and the status report request to the UE", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ ...sentSMS, state: "submitted", status_report: undefined })));
    vi.stubGlobal("fetch", fetch);
    const sent = await postUESMS("phone1", { to: "+12025550102", text: "Hello, Bob!", status_report: true });
    expect(sent.state).toBe("submitted");
    expect(fetch).toHaveBeenCalledWith("/api/ues/phone1/sms", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ to: "+12025550102", text: "Hello, Bob!", status_report: true }),
    });
  });

  const sms = (change: Partial<UESentSMS>): UESentSMS => ({
    call_id: "c",
    to: "+12025550102",
    text: "t",
    status_report_requested: false,
    sent: "",
    state: "accepted",
    rp_cause: null,
    failure_cause: null,
    status_report: null,
    ...change,
  });

  it.each([
    ["before the submit report", sms({ state: "submitted" }), "waiting for the submit report"],
    ["accepted, without a status report", sms({}), "accepted by the SC"],
    ["accepted, before the status report", sms({ status_report_requested: true }), "accepted by the SC, waiting for the status report"],
    [
      "delivered",
      sms({ status_report_requested: true, status_report: { status: 0, discharge_time: "", received: "" } }),
      "accepted by the SC; status report: delivered (TP-ST 0x00)",
    ],
    [
      "not delivered, the recipient absent",
      sms({ status_report_requested: true, status_report: { status: 0x62, discharge_time: "", received: "" } }),
      "accepted by the SC; status report: not delivered, the SC has given up (TP-ST 0x62)",
    ],
    [
      "refused for an unassigned number",
      sms({ state: "failed", rp_cause: 1, failure_cause: 0xc3 }),
      "refused by the SC: RP-ERROR, RP-Cause 1, TP-FCS 0xc3",
    ],
  ])("tells what became of a short message %s", (_, m, want) => {
    expect(describeSentSMS(m)).toBe(want);
  });

  it("starts the number from a tel URI target only", () => {
    expect(suggestedNumber("tel:+12025550102")).toBe("+12025550102");
    expect(suggestedNumber(user2)).toBe("");
  });
});
