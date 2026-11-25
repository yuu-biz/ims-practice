// The OAM API of internal/oam (/oam/v1/), as the OAM screen uses it.
// Every response is parsed here, so the rest of the frontend can trust
// these types. The names are those of the JSON, which are those of the
// Go types in internal/oam and internal/maintenance.

import { lineSplitter } from "./lines.ts";

export const apiPrefix = "/oam/v1/";

// Endpoint (oam.Endpoint): one node of the inventory.
export interface Endpoint {
  name: string;
  roles: string[];
  url: string;
}

// FanOut (oam.FanOut): one entry for every node that the operation was
// for, in the order of the inventory. A node that failed is never left
// out, and the answer is 200 even when some failed.
export interface FanOut<T> {
  succeeded: number;
  failed: number;
  nodes: NodeResult<T>[];
}

// Result is set when the node did what was asked, error when something
// failed; both when the node did it and the OAM failed afterwards.
export interface NodeResult<T> {
  node: string;
  url: string;
  result: T | null;
  error: NodeError | null;
}

export interface NodeError {
  // The status of the node's answer; null when the node did not answer.
  status_code: number | null;
  error: string;
  // Each refused setting of a configuration change, with the reason.
  settings: Record<string, string>;
}

export interface Build {
  version: string;
  revision: string;
  go_version: string;
}

// maintenance.NodeState: the node, the ManagedElement of TS 28.622
// 4.3.3, with each of its roles.
export interface NodeState {
  node: string;
  instance: string;
  build: Build;
  start_time: string;
  configuration_generation: number;
  log_level: string;
  functions: FunctionState[];
}

// The values of TS 28.622 5.3.1; the events that lead to them are those
// of ITU-T X.731 7.1.3.
export const administrativeStates = ["UNLOCKED", "SHUTTINGDOWN", "LOCKED"] as const;
export type AdministrativeState = (typeof administrativeStates)[number];
export const operationalStates = ["ENABLED", "DISABLED"] as const;
export type OperationalState = (typeof operationalStates)[number];

// maintenance.FunctionState: one role of the node, its ManagedFunction.
export interface FunctionState {
  role: string;
  administrative_state: AdministrativeState;
  operational_state: OperationalState;
  // Null for a role that counts no users.
  users: number | null;
  // When a SHUTTINGDOWN role becomes LOCKED even with users left.
  shutdown_deadline: string | null;
  // What the role tells about itself, such as counters.
  summary: unknown;
}

// The names of log/slog. The API also takes and gives one of them with
// an offset, such as INFO+2.
export const logLevels = ["DEBUG", "INFO", "WARN", "ERROR"] as const;

export interface LogLevelChange {
  level: string;
  previous: string;
}

// maintenance.Configuration. The node redacts every secret in
// configuration: the screen shows it as it comes.
export interface Configuration {
  generation: number;
  configuration: unknown;
  changeable: SettingValue[];
}

export interface SettingValue {
  name: string;
  description: string;
  value: unknown;
}

// oam.ChangeRecord: one configuration change of one node.
export interface ChangeRecord {
  id: string;
  // The same for the records of one operation, such as one change of
  // every node of a role.
  change_set: string;
  node: string;
  recorded_at: string;
  rollback_of: string;
  forced: boolean;
  change: ConfigurationChange;
}

export interface ConfigurationChange {
  node: string;
  instance: string;
  generation: number;
  time: string;
  operator: string;
  comment: string;
  changes: SettingChange[];
}

export interface SettingChange {
  name: string;
  previous: unknown;
  value: unknown;
}

// oam.Subscription: a subscription as the HSS shows it, never with a
// credential.
export interface Subscription {
  private_identity: string;
  public_identities: string[];
  authentication_schemes: string[];
  registration_state: string;
  server_name: string;
  test_user: boolean;
  service_profile: string;
  // The tone the callers of the user hear while it rings, null where
  // the operator provisioned none.
  customized_alerting_tone: AlertingTone | null;
}

// oam.AlertingTone: the customized alerting tone of a subscription
// (CAT, TS 24.182). tone names an announcement that the MRFPs have
// provisioned, and active says whether the callers hear it.
export interface AlertingTone {
  tone: string;
  active: boolean;
}

// The tones that the MRFPs of this project's deployments have, besides
// the announcement notice. Any other name that an MRFP has is accepted
// too; the OAM refuses one that is no announcement at all.
export const alertingTones = ["cat1", "cat2"] as const;

// oam.SubscriptionRecord: one line of a bulk provisioning file. The
// screen builds one only for the single-record form; the lines of a
// file go to the OAM as they are, without being parsed here.
export interface SubscriptionRecord {
  private_identity: string;
  public_identities: string[];
  password?: string;
  aka?: { k: string; opc: string; amf?: string; sqn?: string };
  tel?: string;
  ut_password?: string;
  mmtel_services?: string;
  test_user?: boolean;
  service_profile?: string;
}

export const results = ["created", "updated", "unchanged", "deleted", "rejected", "failed"] as const;
export type Result = (typeof results)[number];

// oam.Outcome: the report on one record. The OAM never puts a
// credential in reason.
export interface Outcome {
  // The line in the file or the archive, from 1; 0 for a single record.
  line: number;
  private_identity: string;
  result: Result;
  reason: string;
  dry_run: boolean;
}

// oam.Summary: the counts of a bulk provisioning or a restore.
export interface Summary {
  // Lines read, blank ones included; for a restore, records.
  lines: number;
  created: number;
  updated: number;
  unchanged: number;
  deleted: number;
  rejected: number;
  failed: number;
  dry_run: boolean;
}

export const jobStates = ["running", "completed", "failed"] as const;
export type JobState = (typeof jobStates)[number];

// oam.Step: export and store for a backup; verify, then diff or apply,
// for a restore.
export interface Step {
  name: string;
  section: string;
  state: JobState;
  started: string;
  finished: string | null;
  records: number;
  error: string;
}

// oam.Manifest: what the OAM records about one backup.
export interface Manifest {
  id: string;
  created: string;
  operator: string;
  comment: string;
  // completed only when the archive is whole and stored.
  state: JobState;
  sections: string[];
  steps: Step[];
  records: Record<string, number>;
  bytes: number;
  restores: RestoreRun[];
}

export interface RestoreRun {
  started: string;
  finished: string;
  dry_run: boolean;
  operator: string;
  state: JobState;
  steps: Step[];
  summary: Summary;
}

// oam.StreamLine: one line of an answer that streams, with one of
// these set. An error line says why the operation stopped early.
export type StreamLine =
  | { kind: "step"; step: Step }
  | { kind: "outcome"; outcome: Outcome }
  | { kind: "summary"; summary: Summary }
  | { kind: "manifest"; manifest: Manifest }
  | { kind: "error"; error: string };

export const admissions = ["all", "test_users"] as const;
export type Admission = (typeof admissions)[number];

// oam.TestUsers: how the HSS treats test users now.
export interface TestUsers {
  hss_node: string;
  admission: Admission;
  default_service_profile: string;
  test_user_service_profile: string;
  service_profiles: Record<string, string[]>;
}

// oam.FeatureState: whether a feature is active for one subscriber.
export interface FeatureState {
  private_identity: string;
  feature: string;
  active: boolean;
  reason: string;
  test_user: boolean;
  service_profile: string;
  // subscription, test_users or default.
  profile_source: string;
  admission: Admission;
  admitted: boolean;
  hss_node: string;
}

// --- Alarms: TS 28.111 ---

// perceivedSeverity (TS 28.111 7.4.1), in the order the OAM lists them:
// X.733 8.1.2.3 orders CRITICAL to WARNING; INDETERMINATE and CLEARED
// come after.
export const perceivedSeverities = ["CRITICAL", "MAJOR", "MINOR", "WARNING", "INDETERMINATE", "CLEARED"] as const;
export type PerceivedSeverity = (typeof perceivedSeverities)[number];
// The values of the min_severity filter. It keeps the severity, the
// more severe ones, and INDETERMINATE, which cannot be ranked.
export const minSeverities = ["CRITICAL", "MAJOR", "MINOR", "WARNING"] as const;
export type MinSeverity = (typeof minSeverities)[number];
export const ackStates = ["ACKNOWLEDGED", "UNACKNOWLEDGED"] as const;
export type AckState = (typeof ackStates)[number];

// maintenance.AlarmRecord: one alarm of a node's alarm list (TS 28.111
// 7.3.1), with the node of the inventory whose list holds it
// (oam.NodeAlarmRecord).
export interface AlarmRecord {
  node: string;
  alarm_id: string;
  // Like a DN: ManagedElement=<node>[,ManagedFunction=<role>][,Peer=<peer>].
  object_instance: string;
  // Empty for an alarm of the node as a whole.
  role: string;
  // The far end of a link; empty for an alarm of the role itself.
  peer: string;
  alarm_raised_time: string;
  alarm_changed_time: string | null;
  alarm_cleared_time: string | null;
  alarm_type: string;
  probable_cause: string;
  specific_problem: string;
  perceived_severity: PerceivedSeverity;
  additional_text: string;
  ack_state: AckState;
  ack_time: string | null;
  // Who last changed ack_state, and through which system: the OAM for
  // a change on this screen, or an OSS (TS 28.111 7.4.1).
  ack_user_id: string;
  ack_system_id: string;
}

// oam.AlarmListInfo: what a node said about its alarm list.
export interface AlarmListInfo {
  instance: string;
  // Records in the node's list, whether the filter kept them or not.
  num_of_alarm_records: number;
  // Records of the node that the filter kept.
  listed: number;
  last_modification: string;
  // Records the node removed because its list was full.
  dropped_records: number;
}

// oam.Alarms: an entry for every node, as a FanOut, and the records of
// the nodes that answered, the most severe first, then the newest. A
// node that failed has no alarms here: they are unknown, not none.
export interface Alarms extends FanOut<AlarmListInfo> {
  alarms: AlarmRecord[];
}

// The filter of GET /oam/v1/alarms; null leaves a part out.
export interface AlarmFilter {
  // true for the cleared records only, false for those not cleared.
  cleared: boolean | null;
  min_severity: MinSeverity | null;
}

// The target of an operation on nodes: one node, or every node of a
// role.
export type Scope = { kind: "node"; node: string } | { kind: "role"; role: string };

export class HttpError extends Error {
  readonly status: number;
  // ErrorBody.settings: for a rollback in conflict, each setting with
  // the reason.
  readonly settings: Record<string, string>;

  constructor(status: number, message: string, settings: Record<string, string> = {}) {
    super(message);
    this.status = status;
    this.settings = settings;
  }
}

// --- Nodes ---

export async function fetchInventory(): Promise<Endpoint[]> {
  const o = object(await getJSON("inventory"));
  return array(o, "nodes").map(parseEndpoint);
}

export async function fetchState(scope?: Scope): Promise<FanOut<NodeState>> {
  return parseFanOut(await getJSON(scope ? `${scopePath(scope)}/state` : "state"), parseNodeState);
}

// SHUTTINGDOWN starts the drain, UNLOCKED cancels it, LOCKED locks. On a
// node the role must be given; on a role it is the role of the scope.
export async function putAdministrativeState(
  scope: Scope,
  role: string,
  state: AdministrativeState,
  timeoutSeconds: number,
  operator: string,
): Promise<FanOut<FunctionState>> {
  const path =
    scope.kind === "node"
      ? `nodes/${encodeURIComponent(scope.node)}/functions/${encodeURIComponent(role)}/administrative-state`
      : `${scopePath(scope)}/administrative-state`;
  const body: Record<string, unknown> = { administrative_state: state };
  if (state === "SHUTTINGDOWN" && timeoutSeconds > 0) body["timeout_seconds"] = timeoutSeconds;
  if (operator !== "") body["operator"] = operator;
  return parseFanOut(await sendJSON("PUT", path, body), parseFunctionState);
}

export async function fetchLogLevel(scope: Scope): Promise<FanOut<string>> {
  return parseFanOut(await getJSON(`${scopePath(scope)}/log-level`), (v) => string(object(v), "level"));
}

export async function putLogLevel(scope: Scope, level: string, operator: string): Promise<FanOut<LogLevelChange>> {
  return parseFanOut(await sendJSON("PUT", `${scopePath(scope)}/log-level`, withOperator({ level }, operator)), (v) => {
    const o = object(v);
    return { level: string(o, "level"), previous: string(o, "previous") };
  });
}

// --- Configuration ---

export async function fetchConfiguration(scope: Scope): Promise<FanOut<Configuration>> {
  return parseFanOut(await getJSON(`${scopePath(scope)}/config`), parseConfiguration);
}

// The OAM numbers the generation; a node that refuses a setting fails
// with NodeError.settings.
export async function patchConfiguration(
  scope: Scope,
  settings: Record<string, unknown>,
  operator: string,
  comment: string,
): Promise<FanOut<ChangeRecord>> {
  const body = withOperator({ settings }, operator);
  if (comment !== "") body["comment"] = comment;
  return parseFanOut(await sendJSON("PATCH", `${scopePath(scope)}/config`, body), parseChangeRecord);
}

// The newest first.
export async function fetchHistory(node: string, limit: number): Promise<ChangeRecord[]> {
  const o = object(await getJSON(`nodes/${encodeURIComponent(node)}/config/history?limit=${limit}`));
  return array(o, "changes").map(parseChangeRecord);
}

export interface RollbackRequest {
  operator: string;
  comment: string;
  // Rolls back even the settings that changed since; without it a
  // conflict is a 409 HttpError with settings.
  force: boolean;
}

export async function rollbackChange(id: string, req: RollbackRequest): Promise<ChangeRecord> {
  return parseChangeRecord(await sendJSON("POST", `config/history/${encodeURIComponent(id)}/rollback`, rollbackBody(req)));
}

// Each record of the change set, rolled back on its node; a conflict is
// a node with status_code 409 and settings.
export async function rollbackChangeSet(id: string, req: RollbackRequest): Promise<FanOut<ChangeRecord>> {
  return parseFanOut(await sendJSON("POST", `config/change-sets/${encodeURIComponent(id)}/rollback`, rollbackBody(req)), parseChangeRecord);
}

function rollbackBody(req: RollbackRequest): Record<string, unknown> {
  const body = withOperator({}, req.operator);
  if (req.comment !== "") body["comment"] = req.comment;
  if (req.force) body["force"] = true;
  return body;
}

// --- Subscriptions ---

export async function fetchSubscription(impi: string): Promise<Subscription> {
  return parseSubscription(await getJSON(subscriptionPath(impi)));
}

// Replaces the whole subscription; the HSS keeps no credential that the
// record does not carry.
export async function putSubscription(record: SubscriptionRecord, dryRun: boolean): Promise<Outcome> {
  return parseOutcome(await sendJSON("PUT", subscriptionPath(record.private_identity) + dryRunQuery(dryRun), record));
}

export async function deleteSubscription(impi: string, dryRun: boolean): Promise<Outcome> {
  return parseOutcome(await sendJSON("DELETE", subscriptionPath(impi) + dryRunQuery(dryRun)));
}

export async function putTestUser(impi: string, testUser: boolean, operator: string): Promise<Subscription> {
  return parseSubscription(await sendJSON("PUT", `${subscriptionPath(impi)}/test-user`, withOperator({ test_user: testUser }, operator)));
}

// The operator provisions the tone in the HSS; the MMTel AS of the user
// reads it over Sh for each call to it. The rest of the subscription
// stays as it is.
export async function putAlertingTone(impi: string, tone: AlertingTone, operator: string): Promise<AlertingTone> {
  const path = `${subscriptionPath(impi)}/customized-alerting-tone`;
  return parseAlertingTone(await sendJSON("PUT", path, withOperator({ ...tone }, operator)));
}

export async function fetchFeature(impi: string, feature: string): Promise<FeatureState> {
  return parseFeatureState(await getJSON(`${subscriptionPath(impi)}/features/${encodeURIComponent(feature)}`));
}

// --- Test users ---

export async function fetchTestUsers(): Promise<TestUsers> {
  const o = object(await getJSON("test-users"));
  const profiles: Record<string, string[]> = {};
  for (const [name, features] of Object.entries(object(o["service_profiles"]))) {
    profiles[name] = features === null ? [] : strings(features, name);
  }
  return {
    hss_node: string(o, "hss_node"),
    admission: oneOf(o, "admission", admissions),
    default_service_profile: string(o, "default_service_profile"),
    test_user_service_profile: string(o, "test_user_service_profile"),
    service_profiles: profiles,
  };
}

// A configuration change of every HSS node, in the history.
export async function putTestUserServiceProfile(profile: string, operator: string, comment: string): Promise<FanOut<ChangeRecord>> {
  const body = withOperator({ service_profile: profile }, operator);
  if (comment !== "") body["comment"] = comment;
  return parseFanOut(await sendJSON("PUT", "test-users/service-profile", body), parseChangeRecord);
}

export async function putAdmission(admission: Admission, operator: string, comment: string): Promise<FanOut<ChangeRecord>> {
  const body = withOperator({ admission }, operator);
  if (comment !== "") body["comment"] = comment;
  return parseFanOut(await sendJSON("PUT", "test-users/admission", body), parseChangeRecord);
}

// --- Bulk provisioning ---

// A batch is whole lines of the file, each ending with "\n".
export interface Batch {
  text: string;
  lines: number;
}

// How the screen cuts a file into requests. A browser sends the whole
// body of a request before it reads the answer, while the OAM answers
// each line as it reads it: one request for a long file would fill the
// OAM's send buffer, stop it reading, and stall both. Batches keep each
// answer small. A line longer than maxBatchBytes is a batch of its own,
// which the OAM rejects when it is over its 64 KiB line limit.
export const maxBatchLines = 200;
export const maxBatchBytes = 256 << 10;

// batches reads source a chunk at a time and gives whole lines, at
// most maxLines or about maxBytes characters each, so that a file of any
// length takes bounded memory.
export async function* batches(source: ReadableStream<Uint8Array>, maxLines = maxBatchLines, maxBytes = maxBatchBytes): AsyncGenerator<Batch> {
  const split = lineSplitter();
  let text = "";
  let lines = 0;
  const add = function* (line: string): Generator<Batch> {
    if (lines > 0 && text.length + line.length + 1 > maxBytes) {
      yield { text, lines };
      text = "";
      lines = 0;
    }
    text += line + "\n";
    lines++;
    if (lines >= maxLines) {
      yield { text, lines };
      text = "";
      lines = 0;
    }
  };
  const decoder = new TextDecoder();
  const reader = source.getReader();
  for (;;) {
    const { done, value } = await reader.read();
    const chunk = done ? decoder.decode() : decoder.decode(value, { stream: true });
    for (const line of done ? [...split.push(chunk), ...split.end()] : split.push(chunk)) yield* add(line);
    if (done) break;
  }
  if (lines > 0) yield { text, lines };
}

// provision sends the records of source to POST /oam/v1/provisioning a
// batch at a time and calls onOutcome with each outcome as it arrives,
// with its line counted from the start of source. It returns the
// summary of the whole source. An error line of the OAM stops it, as
// does an answer that ends before its summary.
export async function provision(
  source: ReadableStream<Uint8Array>,
  dryRun: boolean,
  onOutcome: (o: Outcome) => void,
): Promise<Summary> {
  const total: Summary = { lines: 0, created: 0, updated: 0, unchanged: 0, deleted: 0, rejected: 0, failed: 0, dry_run: dryRun };
  for await (const batch of batches(source)) {
    const offset = total.lines;
    const response = await send("POST", "provisioning" + dryRunQuery(dryRun), {
      headers: { "Content-Type": "application/x-ndjson" },
      body: batch.text,
    });
    const got: { summary: Summary | null; failure: string | null } = { summary: null, failure: null };
    await readStream(response, (line) => {
      switch (line.kind) {
        case "outcome":
          onOutcome({ ...line.outcome, line: line.outcome.line + offset });
          break;
        case "summary":
          got.summary = line.summary;
          break;
        case "error":
          got.failure = line.error;
          break;
        default:
          throw new Error(`provisioning: unexpected ${line.kind} line`);
      }
    });
    if (got.summary === null) throw new Error("provisioning: the answer ended before its summary");
    for (const r of results) total[r] += got.summary[r];
    total.lines += batch.lines;
    if (got.failure !== null) throw new Error(`provisioning stopped within lines ${offset + 1} to ${total.lines}: ${got.failure}`);
  }
  return total;
}

// --- Backups ---

// The steps arrive as they start, go on and end, then the manifest.
export async function createBackup(operator: string, comment: string, onLine: (line: StreamLine) => void): Promise<void> {
  const body = withOperator({}, operator);
  if (comment !== "") body["comment"] = comment;
  await readStream(await send("POST", "backups", jsonInit(body)), onLine);
}

export async function fetchBackups(): Promise<Manifest[]> {
  return array(object(await getJSON("backups")), "backups").map(parseManifest);
}

export async function fetchBackup(id: string): Promise<Manifest> {
  return parseManifest(await getJSON(`backups/${encodeURIComponent(id)}`));
}

// The steps, an outcome per record, then the summary. A backup that did
// not complete is a 409 HttpError before the stream starts.
export async function restoreBackup(id: string, dryRun: boolean, operator: string, onLine: (line: StreamLine) => void): Promise<void> {
  const response = await send("POST", `backups/${encodeURIComponent(id)}/restore${dryRunQuery(dryRun)}`, jsonInit(withOperator({}, operator)));
  await readStream(response, onLine);
}

// --- Alarms ---

export async function fetchAlarms(filter: AlarmFilter): Promise<Alarms> {
  const q = new URLSearchParams();
  if (filter.cleared !== null) q.set("cleared", String(filter.cleared));
  if (filter.min_severity !== null) q.set("min_severity", filter.min_severity);
  const query = q.toString();
  return parseAlarms(await getJSON(query === "" ? "alarms" : `alarms?${query}`));
}

// Acknowledges an alarm of one node, or takes the acknowledgement back
// (TS 28.111 6.7). A cleared alarm leaves the list once acknowledged.
// The operator is required either way: the record keeps who changed it,
// and the OAM names itself as the system.
export async function putAckState(node: string, alarmID: string, state: AckState, operator: string): Promise<FanOut<AlarmRecord>> {
  const path = `nodes/${encodeURIComponent(node)}/alarms/${encodeURIComponent(alarmID)}/ack-state`;
  return parseFanOut(await sendJSON("PUT", path, { ack_state: state, operator }), (v) => parseAlarmRecord(v, node));
}

// --- Streams ---

// readStream calls onLine with each line of an application/x-ndjson
// answer as it arrives, holding no more than the line being read.
export async function readStream(response: Response, onLine: (line: StreamLine) => void): Promise<void> {
  if (response.body === null) throw new Error("the answer has no body");
  const split = lineSplitter();
  const decoder = new TextDecoder();
  const reader = response.body.getReader();
  const emit = (text: string) => {
    if (text.trim() === "") return;
    let value: unknown;
    try {
      value = JSON.parse(text);
    } catch {
      throw new Error(`the stream has a line that is not JSON: ${text.slice(0, 80)}`);
    }
    onLine(parseStreamLine(value));
  };
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      for (const line of [...split.push(decoder.decode()), ...split.end()]) emit(line);
      return;
    }
    for (const line of split.push(decoder.decode(value, { stream: true }))) emit(line);
  }
}

export function parseStreamLine(value: unknown): StreamLine {
  const o = object(value);
  if (o["step"] != null) return { kind: "step", step: parseStep(o["step"]) };
  if (o["outcome"] != null) return { kind: "outcome", outcome: parseOutcome(o["outcome"]) };
  if (o["summary"] != null) return { kind: "summary", summary: parseSummary(o["summary"]) };
  if (o["manifest"] != null) return { kind: "manifest", manifest: parseManifest(o["manifest"]) };
  if (typeof o["error"] === "string" && o["error"] !== "") return { kind: "error", error: o["error"] };
  throw new Error("a stream line with none of step, outcome, summary, manifest and error");
}

// --- Requests ---

function scopePath(scope: Scope): string {
  return scope.kind === "node" ? `nodes/${encodeURIComponent(scope.node)}` : `roles/${encodeURIComponent(scope.role)}`;
}

// A private identity has the form user@realm, so it must be escaped.
function subscriptionPath(impi: string): string {
  return `subscriptions/${encodeURIComponent(impi)}`;
}

function dryRunQuery(dryRun: boolean): string {
  return dryRun ? "?dry_run=true" : "";
}

function withOperator(body: Record<string, unknown>, operator: string): Record<string, unknown> {
  if (operator !== "") body["operator"] = operator;
  return body;
}

function jsonInit(body: unknown): RequestInit {
  return { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

async function getJSON(path: string): Promise<unknown> {
  return (await send("GET", path)).json();
}

async function sendJSON(method: string, path: string, body?: unknown): Promise<unknown> {
  return (await send(method, path, body === undefined ? {} : jsonInit(body))).json();
}

async function send(method: string, path: string, init: RequestInit = {}): Promise<Response> {
  const response = await fetch(apiPrefix + path, { ...init, method });
  if (!response.ok) throw await httpError(response);
  return response;
}

// Every error of the OAM is maintenance.ErrorBody, {"error": text}, with
// settings for a rollback in conflict.
async function httpError(response: Response): Promise<HttpError> {
  const text = (await response.text()).trim();
  const status = `${response.status} ${response.statusText}`;
  try {
    const o = object(JSON.parse(text));
    if (typeof o["error"] === "string") return new HttpError(response.status, o["error"] || status, stringMap(o, "settings"));
  } catch {
    // Not an ErrorBody, such as the answer of a proxy: the text is the message.
  }
  return new HttpError(response.status, text || status);
}

// --- Parsing ---

export function parseFanOut<T>(value: unknown, parse: (v: unknown) => T): FanOut<T> {
  const o = object(value);
  return {
    succeeded: number(o, "succeeded"),
    failed: number(o, "failed"),
    nodes: array(o, "nodes").map((v) => {
      const n = object(v);
      return {
        node: string(n, "node"),
        url: optionalString(n, "url") ?? "",
        result: n["result"] == null ? null : parse(n["result"]),
        error: n["error"] == null ? null : parseNodeError(n["error"]),
      };
    }),
  };
}

function parseNodeError(value: unknown): NodeError {
  const o = object(value);
  return {
    status_code: o["status_code"] == null ? null : number(o, "status_code"),
    error: string(o, "error"),
    settings: stringMap(o, "settings"),
  };
}

function parseEndpoint(value: unknown): Endpoint {
  const o = object(value);
  return { name: string(o, "name"), roles: strings(o["roles"], "roles"), url: string(o, "url") };
}

export function parseNodeState(value: unknown): NodeState {
  const o = object(value);
  const b = o["build"] == null ? {} : object(o["build"]);
  return {
    node: string(o, "node"),
    instance: optionalString(o, "instance") ?? "",
    build: {
      version: optionalString(b, "version") ?? "",
      revision: optionalString(b, "revision") ?? "",
      go_version: optionalString(b, "go_version") ?? "",
    },
    start_time: string(o, "start_time"),
    configuration_generation: number(o, "configuration_generation"),
    log_level: string(o, "log_level"),
    functions: array(o, "functions").map(parseFunctionState),
  };
}

function parseFunctionState(value: unknown): FunctionState {
  const o = object(value);
  return {
    role: string(o, "role"),
    administrative_state: oneOf(o, "administrative_state", administrativeStates),
    operational_state: oneOf(o, "operational_state", operationalStates),
    users: o["users"] == null ? null : number(o, "users"),
    shutdown_deadline: optionalString(o, "shutdown_deadline"),
    summary: o["summary"] ?? null,
  };
}

export function parseAlarms(value: unknown): Alarms {
  const o = object(value);
  const f = parseFanOut(o, (v) => {
    const l = object(v);
    return {
      instance: optionalString(l, "instance") ?? "",
      num_of_alarm_records: number(l, "num_of_alarm_records"),
      listed: number(l, "listed"),
      last_modification: string(l, "last_modification"),
      dropped_records: number(l, "dropped_records"),
    };
  });
  return { ...f, alarms: array(o, "alarms").map((v) => parseAlarmRecord(v, string(object(v), "node"))) };
}

// A record that one node answers with has no node field: node is the
// node that answered.
function parseAlarmRecord(value: unknown, node: string): AlarmRecord {
  const o = object(value);
  return {
    node,
    alarm_id: string(o, "alarm_id"),
    object_instance: string(o, "object_instance"),
    role: optionalString(o, "role") ?? "",
    peer: optionalString(o, "peer") ?? "",
    alarm_raised_time: string(o, "alarm_raised_time"),
    alarm_changed_time: optionalString(o, "alarm_changed_time"),
    alarm_cleared_time: optionalString(o, "alarm_cleared_time"),
    alarm_type: string(o, "alarm_type"),
    probable_cause: string(o, "probable_cause"),
    specific_problem: optionalString(o, "specific_problem") ?? "",
    perceived_severity: oneOf(o, "perceived_severity", perceivedSeverities),
    additional_text: optionalString(o, "additional_text") ?? "",
    ack_state: oneOf(o, "ack_state", ackStates),
    ack_time: optionalString(o, "ack_time"),
    ack_user_id: optionalString(o, "ack_user_id") ?? "",
    ack_system_id: optionalString(o, "ack_system_id") ?? "",
  };
}

function parseConfiguration(value: unknown): Configuration {
  const o = object(value);
  return {
    generation: number(o, "generation"),
    configuration: o["configuration"] ?? null,
    changeable: array(o, "changeable").map((v) => {
      const s = object(v);
      return { name: string(s, "name"), description: optionalString(s, "description") ?? "", value: s["value"] ?? null };
    }),
  };
}

export function parseChangeRecord(value: unknown): ChangeRecord {
  const o = object(value);
  const c = object(o["change"]);
  return {
    id: string(o, "id"),
    change_set: string(o, "change_set"),
    node: string(o, "node"),
    recorded_at: string(o, "recorded_at"),
    rollback_of: optionalString(o, "rollback_of") ?? "",
    forced: o["forced"] === true,
    change: {
      node: string(c, "node"),
      instance: optionalString(c, "instance") ?? "",
      generation: number(c, "generation"),
      time: string(c, "time"),
      operator: optionalString(c, "operator") ?? "",
      comment: optionalString(c, "comment") ?? "",
      changes: array(c, "changes").map((v) => {
        const s = object(v);
        return { name: string(s, "name"), previous: s["previous"] ?? null, value: s["value"] ?? null };
      }),
    },
  };
}

function parseSubscription(value: unknown): Subscription {
  const o = object(value);
  return {
    private_identity: string(o, "private_identity"),
    public_identities: strings(o["public_identities"], "public_identities"),
    authentication_schemes: strings(o["authentication_schemes"], "authentication_schemes"),
    registration_state: optionalString(o, "registration_state") ?? "",
    server_name: optionalString(o, "server_name") ?? "",
    test_user: o["test_user"] === true,
    service_profile: optionalString(o, "service_profile") ?? "",
    customized_alerting_tone: o["customized_alerting_tone"] == null ? null : parseAlertingTone(o["customized_alerting_tone"]),
  };
}

function parseAlertingTone(value: unknown): AlertingTone {
  const o = object(value);
  return { tone: string(o, "tone"), active: boolean(o, "active") };
}

export function parseOutcome(value: unknown): Outcome {
  const o = object(value);
  return {
    line: o["line"] == null ? 0 : number(o, "line"),
    private_identity: optionalString(o, "private_identity") ?? "",
    result: oneOf(o, "result", results),
    reason: optionalString(o, "reason") ?? "",
    dry_run: o["dry_run"] === true,
  };
}

function parseSummary(value: unknown): Summary {
  const o = object(value);
  const count = (key: string) => (o[key] == null ? 0 : number(o, key));
  return {
    lines: count("lines"),
    created: count("created"),
    updated: count("updated"),
    unchanged: count("unchanged"),
    deleted: count("deleted"),
    rejected: count("rejected"),
    failed: count("failed"),
    dry_run: o["dry_run"] === true,
  };
}

function parseStep(value: unknown): Step {
  const o = object(value);
  return {
    name: string(o, "name"),
    section: optionalString(o, "section") ?? "",
    state: oneOf(o, "state", jobStates),
    started: string(o, "started"),
    finished: optionalString(o, "finished"),
    records: o["records"] == null ? 0 : number(o, "records"),
    error: optionalString(o, "error") ?? "",
  };
}

export function parseManifest(value: unknown): Manifest {
  const o = object(value);
  const records: Record<string, number> = {};
  if (o["records"] != null) {
    for (const [k, v] of Object.entries(object(o["records"]))) {
      if (typeof v !== "number") throw new Error(`records.${k}: expected a number`);
      records[k] = v;
    }
  }
  return {
    id: string(o, "id"),
    created: string(o, "created"),
    operator: optionalString(o, "operator") ?? "",
    comment: optionalString(o, "comment") ?? "",
    state: oneOf(o, "state", jobStates),
    sections: o["sections"] == null ? [] : strings(o["sections"], "sections"),
    steps: array(o, "steps").map(parseStep),
    records,
    bytes: o["bytes"] == null ? 0 : number(o, "bytes"),
    restores: array(o, "restores").map((v) => {
      const r = object(v);
      return {
        started: string(r, "started"),
        finished: string(r, "finished"),
        dry_run: r["dry_run"] === true,
        operator: optionalString(r, "operator") ?? "",
        state: oneOf(r, "state", jobStates),
        steps: array(r, "steps").map(parseStep),
        summary: parseSummary(r["summary"]),
      };
    }),
  };
}

function parseFeatureState(value: unknown): FeatureState {
  const o = object(value);
  return {
    private_identity: string(o, "private_identity"),
    feature: string(o, "feature"),
    active: boolean(o, "active"),
    reason: optionalString(o, "reason") ?? "",
    test_user: boolean(o, "test_user"),
    service_profile: string(o, "service_profile"),
    profile_source: string(o, "profile_source"),
    admission: oneOf(o, "admission", admissions),
    admitted: boolean(o, "admitted"),
    hss_node: string(o, "hss_node"),
  };
}

type JsonObject = Record<string, unknown>;

function object(value: unknown): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("expected a JSON object");
  }
  return value as JsonObject;
}

function string(o: JsonObject, key: string): string {
  const v = o[key];
  if (typeof v !== "string") throw new Error(`${key}: expected a string`);
  return v;
}

// Go leaves out an empty field (omitempty).
function optionalString(o: JsonObject, key: string): string | null {
  return o[key] == null ? null : string(o, key);
}

function number(o: JsonObject, key: string): number {
  const v = o[key];
  if (typeof v !== "number") throw new Error(`${key}: expected a number`);
  return v;
}

function boolean(o: JsonObject, key: string): boolean {
  const v = o[key];
  if (typeof v !== "boolean") throw new Error(`${key}: expected a boolean`);
  return v;
}

// A missing or null list is empty, as Go encodes a nil slice.
function array(o: JsonObject, key: string): unknown[] {
  const v = o[key];
  if (v == null) return [];
  if (!Array.isArray(v)) throw new Error(`${key}: expected an array`);
  return v;
}

function strings(value: unknown, key: string): string[] {
  if (value == null) return [];
  if (!Array.isArray(value) || value.some((v) => typeof v !== "string")) throw new Error(`${key}: expected strings`);
  return value as string[];
}

function stringMap(o: JsonObject, key: string): Record<string, string> {
  if (o[key] == null) return {};
  const m: Record<string, string> = {};
  for (const [k, v] of Object.entries(object(o[key]))) {
    if (typeof v !== "string") throw new Error(`${key}.${k}: expected a string`);
    m[k] = v;
  }
  return m;
}

function oneOf<T extends string>(o: JsonObject, key: string, values: readonly T[]): T {
  const v = string(o, key);
  if (!(values as readonly string[]).includes(v)) throw new Error(`${key}: unexpected value ${JSON.stringify(v)}`);
  return v as T;
}
