// The Lab server's HTTP API. Every response is parsed here, so the
// rest of the frontend can trust these types.

export type Protocol = "sip" | "diameter";
// Empty when the server cannot tell, as for a message a node sends to
// itself, where both the source and the destination are its own address.
export type Direction = "send" | "recv" | "";

export interface SpecRef {
  spec: string;
  // Empty when the clause has not been verified against the spec.
  clause: string;
  title: string;
}

// One message as one node saw it. A message that both its sender and its
// receiver report arrives twice, once per node.
export interface Message {
  // The order in which the server received the copies. Copies from
  // different nodes arrive in no fixed order, so the capture order is
  // time first, then id (see captureOrder in ladder.ts).
  id: number;
  // RFC 3339 in UTC, with up to nine fractional digits.
  time: string;
  protocol: Protocol;
  transport: string;
  src: string;
  dst: string;
  // The role name the node reported, or empty when it gave none.
  node: string;
  // Relative to node.
  direction: Direction;
  summary: string;
  // The Call-ID for SIP, the Session-Id for Diameter, empty for Diameter
  // base messages (CER, DWR, DPR).
  correlation_id: string;
  // The Call-ID for SIP. For a Cx or Rx message, the Call-ID of the SIP
  // message the server found by its rule that the Diameter session was
  // started for, or empty when there was none: the message itself does
  // not say (see inferredLink in ladder.ts).
  call_id: string;
  payload: string;
  refs: SpecRef[];
  // The body of a MESSAGE of SMS over IP, decoded by the server; null for
  // every other message.
  sms: ShortMessage | null;
}

export interface ShortMessage {
  // The body octet by octet, such as "00 00 00 07 91".
  hex: string;
  // The RP fields, then the TP fields, in the order of their octets.
  fields: SMSField[];
  // Empty when the body carries no text, or it could not be decoded.
  text: string;
  // Why the decoding stopped; empty when it did not.
  error: string;
}

// Named as TS 24.011 (RP-) or TS 23.040 (TP-) names it.
export interface SMSField {
  name: string;
  value: string;
}

export const callStates = ["calling", "early", "confirmed", "terminated", "failed", "cancelled"] as const;
export type CallState = (typeof callStates)[number];

// One call. A call that passes routeing B2BUAs, such as the MMTel AS,
// is a dialog between each two of the UEs and the ASs on its way, each
// with a Call-ID of its own; the server puts them together by the
// icid-value of P-Charging-Vector. The fields other than legs and
// messages are those of the caller's dialog.
export interface Call {
  call_id: string;
  // The Call-IDs of the dialogs, the caller's first, which is call_id.
  legs: string[];
  from: string;
  to: string;
  state: CallState;
  // The final response code, when there is one.
  status: number | null;
  started: string;
  ended: string | null;
  messages: number;
}

export const registrationStates = ["registered", "unregistered"] as const;
export type RegistrationState = (typeof registrationStates)[number];

export interface Registration {
  impu: string;
  impi: string;
  contact: string;
  expires: string;
  state: RegistrationState;
  scscf: string;
  // The Call-ID of the last REGISTER, whose ladder shows the
  // registration. Empty when the server saw only NOTIFYs for it.
  call_id: string;
}

// The contract leaves the subscriber shape to the HSS provisioning API,
// which the Lab server forwards these endpoints to. The fields here are
// the ones the screen reads; the answer of the HSS holds more.
export interface Subscriber {
  private_identity: string;
  public_identities: string[];
  // Those of the public identities that are barred: the S-CSCF lets
  // them register and keeps them out of every other IMS communication
  // (TS 29.228 B.2.1).
  barred_identities: string[];
  // The mark of a subscription that the operator keeps for trying a
  // feature out. It picks the service profile of a test user, and the
  // admission policy of the HSS can hold the network to the marked
  // subscriptions.
  test_user: boolean;
  // The service profile the subscription names, empty where it names
  // none.
  service_profile: string;
  // The profile it gets: service_profile where it names one, and
  // otherwise the one the settings of the HSS name for its kind of
  // subscription. Only the HSS can say which that is.
  effective_service_profile: string;
  // The customized alerting tone that the callers of the user hear
  // (TS 24.182), null when the operator provisioned none.
  customized_alerting_tone: AlertingTone | null;
}

// What a PATCH of a subscription carries: the two fields the HSS takes
// without the credentials of the subscription, which it hands out to
// nobody. A field left out stays as it is.
export interface SubscriberPatch {
  test_user?: boolean;
  service_profile?: string;
}

export interface AlertingTone {
  // The announcement of the MRFP that plays, such as cat1.
  tone: string;
  active: boolean;
}

// The tones that the MRFP of the Compose project has, besides the
// announcement notice (deploy/compose/mrfp.yaml).
export const alertingTones = ["cat1", "cat2"] as const;

export interface NewSubscriber extends Pick<Subscriber, "private_identity" | "public_identities"> {
  password: string;
}

// The status of one UE simulator, as its control API (ue.Phone.Handler)
// reports it. Its call states are the UE's own view, not the Lab's
// callStates.
export const ueCallStates = ["calling", "ringing", "active"] as const;
export type UECallState = (typeof ueCallStates)[number];

export interface UECall {
  // Empty until the INVITE is out.
  call_id: string;
  direction: "outgoing" | "incoming";
  // The callee for an outgoing call, the caller for an incoming one.
  peer: string;
  state: UECallState;
  started: string;
  // An incoming call that rings while another call is on the line
  // (TS 24.615).
  waiting: boolean;
  // Null until the UE has a session for the call.
  session: UESession | null;
}

// The session of a call as the UE sees it (ue.SessionStatus).
export interface UESession {
  // Null until the offer and the answer have settled one.
  codec: UECodec | null;
  // The direction of the audio stream in this UE's last SDP and in the
  // peer's (RFC 3264 6.1).
  direction: string;
  peer_direction: string;
  // Null when the call does not use preconditions.
  preconditions: UEPreconditions | null;
  // The access network carries the media (IR.92 2.4.3.1).
  media_resources: boolean;
  // The access network reserves those resources, as the EPC sets up a
  // dedicated bearer (IR.92 2.4.1). When it does not, as on the LAN of a
  // softphone, media_resources is always true (TS 24.229 6.1.2).
  reservation_needed: boolean;
  // This UE has put the call on hold.
  held: boolean;
  // How the caller heard the call ring before the answer: "local" (its
  // own ringing tone) or "network" (early media, such as a customized
  // alerting tone); empty before any ringing and for the callee.
  ringback: string;
  dtmf_sent: string;
  dtmf_received: string;
}

export interface UECodec {
  name: string;
  payload_type: number;
  clock_rate: number;
  // bandwidth-efficient or octet-aligned for AMR and AMR-WB (RFC 4867
  // 4.3, 4.4), "" for G.711.
  payload_format: string;
  // The payload type of DTMF, null for none.
  telephone_event: number | null;
}

// The precondition table of the call (RFC 3312 5).
export interface UEPreconditions {
  // Every mandatory precondition is met.
  met: boolean;
  rows: UEPreconditionRow[];
}

export interface UEPreconditionRow {
  segment: string; // local or remote
  direction: string; // send or recv
  reserved: boolean;
  strength: string;
  confirm: boolean;
}

export const answerModes = ["auto", "manual", "reject"] as const;
export type AnswerMode = (typeof answerModes)[number];

export type AnswerPolicy =
  | { mode: "auto"; ring_seconds: number }
  | { mode: "manual" }
  | { mode: "reject"; reject_status: number };

// How far a short message that the UE sent has got (ue.SubmitState):
// submitted once its MESSAGE has the 202, then accepted or failed by the
// submit report, an RP-ACK or an RP-ERROR (TS 24.341 5.3.1.2).
export const smsSubmitStates = ["submitted", "accepted", "failed"] as const;
export type SMSSubmitState = (typeof smsSubmitStates)[number];

export interface UESentSMS {
  // The Call-ID of the MESSAGE that carried the SMS-SUBMIT.
  call_id: string;
  to: string;
  text: string;
  status_report_requested: boolean;
  sent: string;
  state: SMSSubmitState;
  // From the RP-ERROR of a failed submission: RP-Cause, and the TP-FCS
  // of the SMS-SUBMIT-REPORT in it; null when there is none.
  rp_cause: number | null;
  failure_cause: number | null;
  status_report: UEStatusReport | null;
}

// What the SMS-STATUS-REPORT said (TS 23.040 9.2.2.3).
export interface UEStatusReport {
  // TP-ST: 0 means the recipient received the message (9.2.3.15).
  status: number;
  discharge_time: string;
  received: string;
}

export interface UEReceivedSMS {
  // The Call-ID of the MESSAGE that brought the SMS-DELIVER.
  call_id: string;
  from: string;
  text: string;
  received: string;
}

export interface UESMSHistory {
  sent: UESentSMS[];
  received: UEReceivedSMS[];
}

export interface UEStatus {
  impu: string;
  registered: boolean;
  call: UECall | null;
  // Every call on the line, oldest first: two with a waiting or a held
  // call.
  calls: UECall[];
  // The conference that the UE takes part in (TS 24.147).
  conference: UEConference | null;
  answer_policy: AnswerPolicy;
  // Oldest first, as the UE keeps them.
  sms: UESMSHistory;
}

export interface UEConference {
  uri: string;
  // The users that the last NOTIFY of the conference event package
  // listed (RFC 4575).
  users: { entity: string; display_text: string; status: string }[];
}

export interface OutgoingSMS {
  // A number in international format, such as +12025550102 (TP-DA).
  to: string;
  text: string;
  status_report: boolean;
}

export type UESimulator =
  | { name: string; reachable: true; status: UEStatus }
  | { name: string; reachable: false; error: string };

// The supplementary services that the UE sets over Ut with XCAP
// (ue.SupplementaryServices.Handler), by the names of GSMA IR.92 2.3.8
// and 2.3.9.
export const diversionServices = ["cfu", "cfb", "cfnr", "cfnrc", "cfnl"] as const;
export const barringServices = ["baic", "bic-roam", "acr", "baoc", "boic", "boic-exhc"] as const;
export type SupplementaryService = (typeof diversionServices)[number] | (typeof barringServices)[number];
const supplementaryServices: readonly SupplementaryService[] = [...diversionServices, ...barringServices];

export interface ServiceSetting {
  name: SupplementaryService;
  // false when the operator put no rule for it in the document.
  provisioned: boolean;
  active: boolean;
  // Where a diversion goes; "" when none is registered.
  target: string;
}

export const oirDefaults = ["presentation-restricted", "presentation-not-restricted"] as const;
export type OIRDefault = (typeof oirDefaults)[number];

export interface SupplementaryServices {
  // The entity tag of the simservs document, the HSS's Sequence-Number.
  etag: string;
  services: ServiceSetting[];
  oir_default_behaviour: OIRDefault | null;
  // The simservs document as the XCAP server sent it.
  document: string;
}

// What the EPC stub holds of one UE that is attached (epc.Status). The
// Lab passes the stub's answers on and keeps none of this.
export interface AttachedUE {
  imsi: string;
  pdn_address: string;
  access_point_name: string;
  bearers: EPSBearer[];
  // The Session-Ids of the Rx sessions bound to the UE's IP-CAN
  // session, one per call leg of the P-CSCF (TS 29.213 5.2).
  rx_sessions: string[];
}

// One EPS bearer of the UE (epc.BearerStatus). The bit rates of the QoS
// are left out: the panel shows the class, which is what tells the
// bearers apart.
export interface EPSBearer {
  eps_bearer_identity: number;
  // The state on the network side (TS 24.301 6.1.3.3), such as
  // "BEARER CONTEXT ACTIVE".
  state: string;
  eps_qos: { qci: number };
}

// The faults that the fault agent can inject (internal/fault). The agent
// checks each request and the panel shows what it says, so these are the
// kinds the form offers, not a second set of rules.
export const faultKinds = ["stop", "kill", "pause", "crash", "recreate", "delay", "loss", "partition"] as const;
export type FaultKind = (typeof faultKinds)[number];

export const faultStates = ["applying", "active", "clearing", "done", "cleared", "expired", "failed"] as const;
export type FaultState = (typeof faultStates)[number];

// What to inject. Only the fields of the kind may be set; durations are
// in the form of Go, such as "200ms" or "5m0s".
export interface FaultRequest {
  target: string;
  kind: FaultKind;
  network?: string;
  delay?: string;
  jitter?: string;
  loss_percent?: number;
  peers?: string[];
  duration?: string;
}

// A fault the agent holds, with the fields of its kind filled in.
export interface Fault {
  id: string;
  target: string;
  kind: FaultKind;
  state: FaultState;
  network: string;
  delay: string;
  jitter: string;
  loss_percent: number | null;
  peers: string[];
  // Empty for a crash and a recreate, which happen once and last no
  // time.
  duration: string;
  // Why the fault failed; empty unless its state is "failed".
  error: string;
  created_at: string;
  // When the fault ends by itself, and when it ended. Empty when it has
  // no such time yet, or none at all.
  expires_at: string;
  ended_at: string;
}

export class HttpError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export function fetchCalls(): Promise<Call[]> {
  return getList("/api/calls", parseCall);
}

export function fetchRegistrations(): Promise<Registration[]> {
  return getList("/api/registrations", parseRegistration);
}

// The SIP messages with these Call-IDs and the Cx messages they caused.
export function fetchCallMessages(callIds: readonly string[]): Promise<Message[]> {
  const query = callIds.map((id) => `call_id=${encodeURIComponent(id)}`).join("&");
  return getList(`/api/messages?${query}`, parseMessage);
}

// The Call-IDs whose messages make the ladder of callId: every dialog of
// the call that callId is one dialog of, or callId alone, such as the
// Call-ID of a REGISTER or of a call the list does not have yet.
export function ladderCallIds(calls: readonly Call[], callId: string): string[] {
  return calls.find((c) => c.legs.includes(callId))?.legs ?? [callId];
}

export function fetchSubscribers(): Promise<Subscriber[]> {
  return getList("/api/subscribers", parseSubscriber);
}

export async function addSubscriber(subscriber: NewSubscriber): Promise<void> {
  await send("/api/subscribers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(subscriber),
  });
}

// The HSS pushes the user profile that follows from the change to the
// S-CSCF of a registered user before it answers.
export async function patchSubscriber(privateIdentity: string, patch: SubscriberPatch): Promise<void> {
  await send(`/api/subscribers/${encodeURIComponent(privateIdentity)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
  });
}

export async function deleteSubscriber(privateIdentity: string): Promise<void> {
  // A private identity has the form user@domain, so it must be escaped.
  await send(`/api/subscribers/${encodeURIComponent(privateIdentity)}`, { method: "DELETE" });
}

// The operator provisions the tone in the HSS; the MMTel AS reads it
// over Sh for each call to the user.
export async function putAlertingTone(privateIdentity: string, tone: AlertingTone): Promise<void> {
  await send(`/api/subscribers/${encodeURIComponent(privateIdentity)}/customized-alerting-tone`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(tone),
  });
}

// The UEs that are attached to the EPC stub, with their bearers.
export function fetchAttachedUEs(): Promise<AttachedUE[]> {
  return getList("/api/epc/ues", parseAttachedUE);
}

// The procedures that the network initiates, which the stub starts on
// the UE's event stream (TS 24.301 6.4.4.2 and 5.5.2.3). Each answers
// with no content.
export async function releaseBearer(imsi: string, ebi: number): Promise<void> {
  await send(`/api/epc/network-initiated/ues/${encodeURIComponent(imsi)}/bearers/${ebi}`, { method: "DELETE" });
}

export async function detachUE(imsi: string): Promise<void> {
  await send(`/api/epc/network-initiated/ues/${encodeURIComponent(imsi)}/detach`, { method: "POST" });
}

// The faults the agent holds, the oldest first, with the ones that
// ended for an hour after they ended.
export function fetchFaults(): Promise<Fault[]> {
  return getList("/api/fault/faults", parseFault);
}

// Answers once the agent has applied the fault, which takes seconds for
// a node that is asked to stop.
export async function injectFault(request: FaultRequest): Promise<Fault> {
  const response = await send("/api/fault/faults", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  return parseFault(await response.json());
}

export async function clearFault(id: string): Promise<Fault> {
  return parseFault(await (await send(`/api/fault/faults/${encodeURIComponent(id)}`, { method: "DELETE" })).json());
}

// The UE simulators that the Lab controls, each with its status or
// the reason it did not answer.
export function fetchUESimulators(): Promise<UESimulator[]> {
  return getList("/api/ues", parseUESimulator);
}

export type UEAction = "register" | "deregister" | "answer" | "hangup" | "hold" | "resume" | "swap" | "merge";

// Each action answers with the UE's status after it.
export async function postUEAction(name: string, action: UEAction): Promise<UEStatus> {
  return parseUEStatus(await (await send(uePath(name, action), { method: "POST" })).json());
}

export async function postUECall(name: string, target: string): Promise<UEStatus> {
  const response = await send(uePath(name, "call"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ target }),
  });
  return parseUEStatus(await response.json());
}

// The UE sends the digits as telephone events in its RTP (RFC 4733).
export async function postUEDTMF(name: string, digits: string): Promise<UEStatus> {
  const response = await send(uePath(name, "dtmf"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ digits }),
  });
  return parseUEStatus(await response.json());
}

// Answers once the MESSAGE has its 202; the submit report and the status
// report come later, in the UE's events.
export async function postUESMS(name: string, sms: OutgoingSMS): Promise<UESentSMS> {
  const response = await send(uePath(name, "sms"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(sms),
  });
  return parseSentSMS(await response.json());
}

// The UE fills in the default ring time or reject status of the mode.
export async function putAnswerPolicy(name: string, mode: AnswerMode): Promise<AnswerPolicy> {
  const response = await send(uePath(name, "answer-policy"), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mode }),
  });
  return parseAnswerPolicy(await response.json());
}

// Each of these makes the UE send XCAP requests over Ut, and answers
// with the settings after them.
export async function fetchUEServices(name: string): Promise<SupplementaryServices> {
  return parseSupplementaryServices(await (await send(uePath(name, "xcap"))).json());
}

export async function putUEService(
  name: string,
  service: SupplementaryService,
  active: boolean,
  target: string,
): Promise<SupplementaryServices> {
  const response = await send(uePath(name, `xcap/services/${service}`), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(target === "" ? { active } : { active, target }),
  });
  return parseSupplementaryServices(await response.json());
}

export async function putUEOIRDefault(name: string, behaviour: OIRDefault): Promise<SupplementaryServices> {
  const response = await send(uePath(name, "xcap/oir"), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ default_behaviour: behaviour }),
  });
  return parseSupplementaryServices(await response.json());
}

// The server-sent events of the UE: each one's data is its status.
export function ueEventsUrl(name: string): string {
  return uePath(name, "events");
}

function uePath(name: string, endpoint: string): string {
  return `/api/ues/${encodeURIComponent(name)}/${endpoint}`;
}

async function send(path: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(path, init);
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new HttpError(response.status, errorText(text) || `${response.status} ${response.statusText}`);
  }
  return response;
}

// The Lab and the UE simulators answer errors as {"error": text}.
function errorText(body: string): string {
  try {
    const parsed: unknown = JSON.parse(body);
    if (typeof parsed === "object" && parsed !== null && typeof (parsed as JsonObject)["error"] === "string") {
      return (parsed as JsonObject)["error"] as string;
    }
  } catch {
    // Not JSON: the text is the message.
  }
  return body;
}

async function getList<T>(path: string, parse: (value: unknown) => T): Promise<T[]> {
  const body: unknown = await (await send(path)).json();
  // Go encodes an empty (nil) slice as null.
  if (body === null) return [];
  if (!Array.isArray(body)) {
    throw new Error(`${path}: expected a JSON array`);
  }
  return body.map(parse);
}

export function parseMessage(value: unknown): Message {
  const o = object(value);
  return {
    id: number(o, "id"),
    time: utcTime(o, "time"),
    protocol: oneOf(o, "protocol", ["sip", "diameter"] as const),
    transport: string(o, "transport"),
    src: string(o, "src"),
    dst: string(o, "dst"),
    node: optionalString(o, "node") ?? "",
    direction: oneOf(o, "direction", ["send", "recv", ""] as const),
    summary: string(o, "summary"),
    correlation_id: optionalString(o, "correlation_id") ?? "",
    call_id: optionalString(o, "call_id") ?? "",
    payload: string(o, "payload"),
    refs: array(o, "refs").map(parseSpecRef),
    sms: o["sms"] === undefined || o["sms"] === null ? null : parseShortMessage(o["sms"]),
  };
}

function parseShortMessage(value: unknown): ShortMessage {
  const o = object(value);
  return {
    hex: string(o, "hex"),
    fields: array(o, "fields").map((f) => {
      const field = object(f);
      return { name: string(field, "name"), value: string(field, "value") };
    }),
    text: optionalString(o, "text") ?? "",
    error: optionalString(o, "error") ?? "",
  };
}

function parseSpecRef(value: unknown): SpecRef {
  const o = object(value);
  return {
    spec: string(o, "spec"),
    clause: optionalString(o, "clause") ?? "",
    title: optionalString(o, "title") ?? "",
  };
}

function parseCall(value: unknown): Call {
  const o = object(value);
  const callId = string(o, "call_id");
  const legs = array(o, "legs").map((leg) => {
    if (typeof leg !== "string") throw new Error("legs: expected strings");
    return leg;
  });
  return {
    call_id: callId,
    legs: legs.length === 0 ? [callId] : legs,
    from: string(o, "from"),
    to: string(o, "to"),
    state: oneOf(o, "state", callStates),
    status: o["status"] === undefined || o["status"] === null ? null : number(o, "status"),
    started: string(o, "started"),
    ended: optionalString(o, "ended"),
    messages: number(o, "messages"),
  };
}

function parseRegistration(value: unknown): Registration {
  const o = object(value);
  return {
    impu: string(o, "impu"),
    impi: optionalString(o, "impi") ?? "",
    contact: optionalString(o, "contact") ?? "",
    expires: optionalString(o, "expires") ?? "",
    state: oneOf(o, "state", registrationStates),
    scscf: optionalString(o, "scscf") ?? "",
    call_id: string(o, "call_id"),
  };
}

function parseSubscriber(value: unknown): Subscriber {
  const o = object(value);
  return {
    private_identity: string(o, "private_identity"),
    public_identities: strings(o, "public_identities"),
    barred_identities: strings(o, "barred_identities"),
    // Go leaves out a mark that is not set (omitempty).
    test_user: o["test_user"] === undefined ? false : boolean(o, "test_user"),
    service_profile: optionalString(o, "service_profile") ?? "",
    effective_service_profile: string(o, "effective_service_profile"),
    customized_alerting_tone:
      o["customized_alerting_tone"] === undefined || o["customized_alerting_tone"] === null
        ? null
        : parseAlertingTone(o["customized_alerting_tone"]),
  };
}

function parseAlertingTone(value: unknown): AlertingTone {
  const o = object(value);
  return { tone: string(o, "tone"), active: boolean(o, "active") };
}

function parseAttachedUE(value: unknown): AttachedUE {
  const o = object(value);
  return {
    imsi: string(o, "imsi"),
    pdn_address: string(o, "pdn_address"),
    access_point_name: string(o, "access_point_name"),
    bearers: array(o, "bearers").map((v) => {
      const b = object(v);
      return {
        eps_bearer_identity: number(b, "eps_bearer_identity"),
        state: string(b, "state"),
        eps_qos: { qci: number(object(b["eps_qos"]), "qci") },
      };
    }),
    rx_sessions: array(o, "rx_sessions").map((v) => {
      if (typeof v !== "string") throw new Error("rx_sessions: expected strings");
      return v;
    }),
  };
}

export function parseFault(value: unknown): Fault {
  const o = object(value);
  return {
    id: string(o, "id"),
    target: string(o, "target"),
    kind: oneOf(o, "kind", faultKinds),
    state: oneOf(o, "state", faultStates),
    network: optionalString(o, "network") ?? "",
    delay: optionalString(o, "delay") ?? "",
    jitter: optionalString(o, "jitter") ?? "",
    loss_percent: optionalNumber(o, "loss_percent"),
    peers: array(o, "peers").map((v) => {
      if (typeof v !== "string") throw new Error("peers: expected strings");
      return v;
    }),
    duration: optionalString(o, "duration") ?? "",
    error: optionalString(o, "error") ?? "",
    created_at: string(o, "created_at"),
    expires_at: optionalString(o, "expires_at") ?? "",
    ended_at: optionalString(o, "ended_at") ?? "",
  };
}

function parseUESimulator(value: unknown): UESimulator {
  const o = object(value);
  const name = string(o, "name");
  if (o["reachable"] === true) return { name, reachable: true, status: parseUEStatus(o["status"]) };
  if (o["reachable"] === false) return { name, reachable: false, error: optionalString(o, "error") ?? "" };
  throw new Error("reachable: expected a boolean");
}

export function parseUEStatus(value: unknown): UEStatus {
  const o = object(value);
  return {
    impu: string(o, "impu"),
    registered: boolean(o, "registered"),
    call: o["call"] === undefined || o["call"] === null ? null : parseUECall(o["call"]),
    calls: o["calls"] === undefined || o["calls"] === null ? [] : array(o, "calls").map(parseUECall),
    conference: o["conference"] === undefined || o["conference"] === null ? null : parseUEConference(o["conference"]),
    answer_policy: parseAnswerPolicy(o["answer_policy"]),
    sms: o["sms"] === undefined || o["sms"] === null ? { sent: [], received: [] } : parseSMSHistory(o["sms"]),
  };
}

function parseSMSHistory(value: unknown): UESMSHistory {
  const o = object(value);
  return { sent: array(o, "sent").map(parseSentSMS), received: array(o, "received").map(parseReceivedSMS) };
}

function parseSentSMS(value: unknown): UESentSMS {
  const o = object(value);
  return {
    call_id: string(o, "call_id"),
    to: string(o, "to"),
    text: string(o, "text"),
    status_report_requested: boolean(o, "status_report_requested"),
    sent: string(o, "sent"),
    state: oneOf(o, "state", smsSubmitStates),
    rp_cause: optionalNumber(o, "rp_cause"),
    failure_cause: optionalNumber(o, "failure_cause"),
    status_report: o["status_report"] === undefined || o["status_report"] === null ? null : parseStatusReport(o["status_report"]),
  };
}

function parseStatusReport(value: unknown): UEStatusReport {
  const o = object(value);
  return { status: number(o, "status"), discharge_time: string(o, "discharge_time"), received: string(o, "received") };
}

function parseReceivedSMS(value: unknown): UEReceivedSMS {
  const o = object(value);
  return { call_id: string(o, "call_id"), from: string(o, "from"), text: string(o, "text"), received: string(o, "received") };
}

function parseUEConference(value: unknown): UEConference {
  const o = object(value);
  return {
    uri: string(o, "uri"),
    users:
      o["users"] === undefined || o["users"] === null
        ? []
        : array(o, "users").map((v) => {
            const u = object(v);
            return { entity: string(u, "entity"), display_text: optionalString(u, "display_text") ?? "", status: optionalString(u, "status") ?? "" };
          }),
  };
}

function parseUECall(value: unknown): UECall {
  const o = object(value);
  return {
    call_id: optionalString(o, "call_id") ?? "",
    direction: oneOf(o, "direction", ["outgoing", "incoming"] as const),
    peer: string(o, "peer"),
    state: oneOf(o, "state", ueCallStates),
    started: string(o, "started"),
    waiting: o["waiting"] === true,
    session: o["session"] === undefined || o["session"] === null ? null : parseUESession(o["session"]),
  };
}

function parseUESession(value: unknown): UESession {
  const o = object(value);
  return {
    codec: o["codec"] === undefined || o["codec"] === null ? null : parseUECodec(o["codec"]),
    direction: optionalString(o, "direction") ?? "",
    peer_direction: optionalString(o, "peer_direction") ?? "",
    preconditions: o["preconditions"] === undefined || o["preconditions"] === null ? null : parsePreconditions(o["preconditions"]),
    media_resources: boolean(o, "media_resources"),
    reservation_needed: boolean(o, "reservation_needed"),
    held: boolean(o, "held"),
    ringback: optionalString(o, "ringback") ?? "",
    dtmf_sent: optionalString(o, "dtmf_sent") ?? "",
    dtmf_received: optionalString(o, "dtmf_received") ?? "",
  };
}

function parseUECodec(value: unknown): UECodec {
  const o = object(value);
  return {
    name: string(o, "name"),
    payload_type: number(o, "payload_type"),
    clock_rate: number(o, "clock_rate"),
    payload_format: optionalString(o, "payload_format") ?? "",
    telephone_event: optionalNumber(o, "telephone_event"),
  };
}

function parsePreconditions(value: unknown): UEPreconditions {
  const o = object(value);
  return {
    met: boolean(o, "met"),
    rows: array(o, "rows").map((v) => {
      const r = object(v);
      return {
        segment: string(r, "segment"),
        direction: string(r, "direction"),
        reserved: boolean(r, "reserved"),
        strength: string(r, "strength"),
        confirm: r["confirm"] === true,
      };
    }),
  };
}

export function parseSupplementaryServices(value: unknown): SupplementaryServices {
  const o = object(value);
  return {
    etag: string(o, "etag"),
    services: array(o, "services").map((v) => {
      const s = object(v);
      return {
        name: oneOf(s, "name", supplementaryServices),
        provisioned: boolean(s, "provisioned"),
        active: boolean(s, "active"),
        target: optionalString(s, "target") ?? "",
      };
    }),
    oir_default_behaviour: o["oir_default_behaviour"] === undefined || o["oir_default_behaviour"] === null ? null : oneOf(o, "oir_default_behaviour", oirDefaults),
    document: string(o, "document"),
  };
}

function parseAnswerPolicy(value: unknown): AnswerPolicy {
  const o = object(value);
  const mode = oneOf(o, "mode", answerModes);
  switch (mode) {
    case "auto":
      return { mode, ring_seconds: number(o, "ring_seconds") };
    case "reject":
      return { mode, reject_status: number(o, "reject_status") };
    case "manual":
      return { mode };
  }
}

type JsonObject = Record<string, unknown>;

function object(value: unknown): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("expected a JSON object");
  }
  return value as JsonObject;
}

// The capture order compares these digit by digit, which needs UTC and
// at most nine fractional digits, as Go writes them.
function utcTime(o: JsonObject, key: string): string {
  const v = string(o, key);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$/.test(v)) {
    throw new Error(`${key}: expected an RFC 3339 time in UTC, got ${JSON.stringify(v)}`);
  }
  return v;
}

function string(o: JsonObject, key: string): string {
  const v = o[key];
  if (typeof v !== "string") throw new Error(`${key}: expected a string`);
  return v;
}

function optionalString(o: JsonObject, key: string): string | null {
  return o[key] === undefined || o[key] === null ? null : string(o, key);
}

// Go leaves out a zero cause (omitempty).
function optionalNumber(o: JsonObject, key: string): number | null {
  return o[key] === undefined || o[key] === null ? null : number(o, key);
}

function boolean(o: JsonObject, key: string): boolean {
  const v = o[key];
  if (typeof v !== "boolean") throw new Error(`${key}: expected a boolean`);
  return v;
}

function number(o: JsonObject, key: string): number {
  const v = o[key];
  if (typeof v !== "number") throw new Error(`${key}: expected a number`);
  return v;
}

function strings(o: JsonObject, key: string): string[] {
  return array(o, key).map((v) => {
    if (typeof v !== "string") throw new Error(`${key}: expected strings`);
    return v;
  });
}

// A missing or null list is empty, as Go encodes a nil slice.
function array(o: JsonObject, key: string): unknown[] {
  const v = o[key];
  if (v === undefined || v === null) return [];
  if (!Array.isArray(v)) throw new Error(`${key}: expected an array`);
  return v;
}

function oneOf<T extends string>(o: JsonObject, key: string, values: readonly T[]): T {
  const v = string(o, key);
  if (!(values as readonly string[]).includes(v)) {
    throw new Error(`${key}: unexpected value ${JSON.stringify(v)}`);
  }
  return v as T;
}
