// What the UE simulator panel offers for each UE, derived from its status
// alone. The rules follow what the UE's control API refuses with 409:
// no call while unregistered or busy (ErrNotRegistered, ErrBusy), no
// deregistration during a call (ErrBusy), no answer unless an incoming
// call rings (ErrNotRinging), no hang-up without a call (ErrNoCall), no
// short message while unregistered (ErrNotRegistered). Hold, resume and
// DTMF need the session of an answered call (ErrNoCall otherwise). A
// second call goes out only while the first is held (ErrNotHeld); swap
// needs an answered call and a held one, and merge two answered calls
// (ErrNoTwoCalls).

import type {
  AnswerPolicy,
  ServiceSetting,
  Subscriber,
  SupplementaryService,
  UECall,
  UEConference,
  UESentSMS,
  UESession,
  UESimulator,
} from "./api.ts";

export interface UEControls {
  // Registers, or refreshes the registration of a registered UE.
  register: boolean;
  deregister: boolean;
  call: boolean;
  answer: boolean;
  hangup: boolean;
  hold: boolean;
  resume: boolean;
  dtmf: boolean;
  swap: boolean;
  merge: boolean;
  answerPolicy: boolean;
  sms: boolean;
}

const nothing: UEControls = {
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

// busy is set while a request to the UE is under way, so that a second
// click cannot overtake it.
export function enabledControls(ue: UESimulator, busy: boolean): UEControls {
  if (!ue.reachable || busy) return nothing;
  const { registered, call, calls, conference } = ue.status;
  const session = call?.state === "active" ? call.session : null;
  const answered = calls.filter((c) => c.state === "active");
  const held = answered.filter((c) => c.session?.held === true);
  return {
    register: true,
    deregister: registered && call === null,
    call: registered && calls.length < 2 && calls.every((c) => c.session?.held === true),
    answer: calls.some((c) => c.direction === "incoming" && c.state === "ringing"),
    hangup: call !== null,
    hold: session !== null && !session.held,
    resume: session?.held === true,
    dtmf: session !== null,
    swap: answered.length === 2 && held.length === 1,
    merge: answered.length === 2 && conference === null,
    answerPolicy: true,
    sms: registered,
  };
}

// The target the call form starts with: the tel URI of another UE that
// the panel shows, from the HSS subscription that holds its IMPU, or its
// IMPU when the subscriptions are not known.
export function suggestedTarget(ue: UESimulator, all: readonly UESimulator[], subscribers: readonly Subscriber[]): string {
  for (const other of all) {
    if (other.name === ue.name || !other.reachable) continue;
    const impu = other.status.impu;
    const subscription = subscribers.find((s) => s.public_identities.includes(impu));
    return subscription?.public_identities.find((id) => id.startsWith("tel:")) ?? impu;
  }
  return "";
}

// The number the SMS form starts with: that of the call target when it is
// a tel URI. TP-DA holds a number, not a URI (TS 23.040 9.1.2.5).
export function suggestedNumber(target: string): string {
  return target.startsWith("tel:+") ? target.slice("tel:".length) : "";
}

// What has become of a short message that the UE sent: the submit report
// (TS 24.341 5.3.1.2), then the status report it asked for (5.3.1.3).
export function describeSentSMS(sms: UESentSMS): string {
  switch (sms.state) {
    case "submitted":
      return "waiting for the submit report";
    case "failed": {
      const fcs = sms.failure_cause === null ? "" : `, TP-FCS 0x${sms.failure_cause.toString(16)}`;
      return `refused by the SC: RP-ERROR, RP-Cause ${sms.rp_cause ?? "none"}${fcs}`;
    }
    case "accepted":
      if (!sms.status_report_requested) return "accepted by the SC";
      if (sms.status_report === null) return "accepted by the SC, waiting for the status report";
      return `accepted by the SC; status report: ${describeStatus(sms.status_report.status)}`;
  }
}

// TP-ST by its class, bits 6 and 5 (TS 23.040 9.2.3.15).
function describeStatus(status: number): string {
  const tpST = `TP-ST 0x${status.toString(16).padStart(2, "0")}`;
  switch (status >> 5) {
    case 0:
      return `delivered (${tpST})`;
    case 1:
      return `not delivered yet, the SC keeps trying (${tpST})`;
    default:
      return `not delivered, the SC has given up (${tpST})`;
  }
}

// What the hang-up button sends, by RFC 3261: CANCEL for an outgoing
// call that is not answered (9.1), a final response for an incoming call
// that rings, BYE for an answered call (15.1.1).
export function hangUpEffect(call: UECall): string {
  if (call.state === "active") return "sends BYE";
  if (call.direction === "outgoing") return "sends CANCEL";
  return "declines with 603";
}

export function describeCall(call: UECall | null): string {
  if (call === null) return "No call";
  const peer = call.direction === "outgoing" ? `to ${call.peer}` : `from ${call.peer}`;
  const state = call.waiting && call.state === "ringing" ? "waiting" : call.state;
  const held = call.session?.held === true ? ", held" : "";
  return `${call.direction === "outgoing" ? "Outgoing" : "Incoming"} call ${peer}: ${state}${held}`;
}

// The users of a conference, one line each, as its conference event
// package tells them (RFC 4575 5.6).
export function describeConference(conference: UEConference): string[] {
  return [conference.uri, ...conference.users.map((u) => `${u.display_text || u.entity}: ${u.status || "status unknown"}`)];
}

// The session of a call, one line per part: the codec, the SDP
// directions of both sides and hold (RFC 3264 6.1, TS 24.610), the
// precondition table (RFC 3312 5) and the DTMF digits (RFC 4733).
export function describeSession(session: UESession): string[] {
  const lines: string[] = [];
  const { codec } = session;
  if (codec === null) {
    lines.push("codec not settled yet");
  } else {
    const parts = [`${codec.name}/${codec.clock_rate}`, `payload type ${codec.payload_type}`];
    if (codec.payload_format !== "") parts.push(codec.payload_format);
    if (codec.telephone_event !== null) parts.push(`DTMF payload type ${codec.telephone_event}`);
    lines.push(parts.join(", "));
  }
  if (session.direction !== "") {
    const peer = session.peer_direction === "" ? "" : `, the peer ${session.peer_direction}`;
    lines.push(`${session.direction}${peer}${session.held ? ", held by this UE" : ""}`);
  }
  const { preconditions } = session;
  if (preconditions !== null) {
    const rows = preconditions.rows.map(
      (r) => `${r.segment} ${r.direction} ${r.reserved ? "reserved" : "not reserved"} (${r.strength}${r.confirm ? ", confirm" : ""})`,
    );
    lines.push(`preconditions ${preconditions.met ? "met" : "not met"}: ${rows.join("; ")}`);
  }
  lines.push(session.media_resources ? "media resources reserved" : "no media resources yet");
  // TS 24.628 4.7.2.1: a caller plays its own ringing tone unless the
  // network sends early media, such as a customized alerting tone.
  if (session.ringback === "local") lines.push("rang with the UE's own ringing tone");
  if (session.ringback === "network") lines.push("rang with early media from the network");
  if (session.dtmf_sent !== "" || session.dtmf_received !== "") {
    lines.push(`DTMF sent ${session.dtmf_sent || "none"}, received ${session.dtmf_received || "none"}`);
  }
  return lines;
}

export function describePolicy(policy: AnswerPolicy): string {
  switch (policy.mode) {
    case "auto":
      return `answers after ${policy.ring_seconds} s of ringing`;
    case "manual":
      return "rings until you answer or hang up";
    case "reject":
      return `turns calls down with ${policy.reject_status}`;
  }
}

// The names that GSMA IR.92 2.3.8 and 2.3.9 give the services, with the
// condition of the rule that the UE changes for each (TS 24.604 4.9.1.3,
// TS 24.611 4.9.1.4).
const serviceLabels: Record<SupplementaryService, string> = {
  cfu: "CFU (unconditional)",
  cfb: "CFB (busy)",
  cfnr: "CFNR (no reply)",
  cfnrc: "CFNRc (not reachable)",
  cfnl: "CFNL (not logged in)",
  baic: "BAIC (all incoming)",
  "bic-roam": "BIC-Roam (incoming when roaming)",
  acr: "ACR (anonymous incoming)",
  baoc: "BAOC (all outgoing)",
  boic: "BOIC (outgoing international)",
  "boic-exhc": "BOIC-exHC (international except home)",
};

export function serviceLabel(service: SupplementaryService): string {
  return serviceLabels[service];
}

// One line of the supplementary services list of a UE card.
export function describeService(s: ServiceSetting): string {
  if (!s.provisioned) return `${serviceLabels[s.name]}: not provisioned`;
  const target = s.target === "" ? "" : ` to ${s.target}`;
  return `${serviceLabels[s.name]}: ${s.active ? "on" : "off"}${target}`;
}
