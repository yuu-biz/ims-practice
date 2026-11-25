// What the screen shows, derived from the answers of the OAM alone. Each
// function here is free of the DOM, so that the tests can check it.

import {
  minSeverities,
  perceivedSeverities,
  results,
  type AckState,
  type AdministrativeState,
  type AlarmFilter,
  type AlarmRecord,
  type Alarms,
  type Endpoint,
  type FanOut,
  type MinSeverity,
  type NodeError,
  type NodeState,
  type OperationalState,
  type Outcome,
  type PerceivedSeverity,
  type Result,
  type Scope,
  type Step,
  type SubscriptionRecord,
} from "./api.ts";

// "2026-11-20T09:00:00.123456789Z" -> "2026-11-20 09:00:00 UTC". A time
// with an offset, as a node in another time zone might give, is turned
// to UTC too.
export function formatTime(time: string): string {
  const ms = Date.parse(time);
  if (Number.isNaN(ms)) return time;
  return new Date(ms).toISOString().replace("T", " ").replace(/\.\d+Z$/, " UTC");
}

export function describeNodeError(e: NodeError): string {
  return e.status_code === null ? e.error : `${e.status_code}: ${e.error}`;
}

// One node of the inventory with its state, or why it gave none. A node
// that failed is never left out.
export interface NodeRow {
  name: string;
  url: string;
  roles: string[];
  state: NodeState | null;
  error: string | null;
}

export function nodeRows(inventory: readonly Endpoint[], state: FanOut<NodeState>): NodeRow[] {
  return inventory.map((e) => {
    const r = state.nodes.find((n) => n.node === e.name);
    const error = r === undefined ? "not in the answer of the OAM" : r.error === null ? null : describeNodeError(r.error);
    return { name: e.name, url: e.url, roles: e.roles, state: r?.result ?? null, error };
  });
}

// What the nodes of a role say together. A node that failed counts in
// failed, not in the states.
export interface RoleSummary {
  role: string;
  nodes: string[];
  failed: string[];
  administrative: Partial<Record<AdministrativeState, number>>;
  operational: Partial<Record<OperationalState, number>>;
  // Null when no node of the role counts users.
  users: number | null;
  // The nodes of a role keep one configuration generation; more than
  // one here shows a node that missed a change.
  generations: number[];
}

export function roleSummaries(rows: readonly NodeRow[]): RoleSummary[] {
  const roles = new Map<string, RoleSummary>();
  for (const row of rows) {
    for (const role of row.roles) {
      let s = roles.get(role);
      if (s === undefined) {
        s = { role, nodes: [], failed: [], administrative: {}, operational: {}, users: null, generations: [] };
        roles.set(role, s);
      }
      s.nodes.push(row.name);
      const f = row.state?.functions.find((x) => x.role === role);
      if (row.state === null || f === undefined) {
        s.failed.push(row.name);
        continue;
      }
      s.administrative[f.administrative_state] = (s.administrative[f.administrative_state] ?? 0) + 1;
      s.operational[f.operational_state] = (s.operational[f.operational_state] ?? 0) + 1;
      if (f.users !== null) s.users = (s.users ?? 0) + f.users;
      if (!s.generations.includes(row.state.configuration_generation)) s.generations.push(row.state.configuration_generation);
    }
  }
  return [...roles.values()];
}

// What each role counts as its users, the count of its maintenance
// function: the S-CSCF counts its server transactions
// (internal/scscf/maintenance.go).
const userCounts: Record<string, string> = {
  scscf: "the SIP requests it is still serving",
};

export interface ColumnNote {
  column: string;
  meaning: string;
}

// What the Users and Generation columns mean for the roles shown. A role
// that no node reported is left out: its count is not known.
export function columnNotes(summaries: readonly RoleSummary[]): ColumnNote[] {
  const reported = summaries.filter((s) => s.failed.length < s.nodes.length);
  const counted = reported.filter((s) => s.users !== null).map((s) => `${s.role}: ${userCounts[s.role] ?? "counted by the role"}.`);
  const uncounted = reported.filter((s) => s.users === null).map((s) => s.role);
  const users = ["What a role still serves on a node; a drain locks the role there once this is 0. The Roles table adds up its nodes.", ...counted];
  if (uncounted.length > 0) users.push(`${uncounted.join(", ")}: not counted, so a drain locks ${uncounted.length === 1 ? "it" : "them"} at once.`);
  return [
    { column: "Users", meaning: users.join(" ") },
    {
      column: "Generation",
      meaning: 'The configuration generation of the node, which each configuration change raises. The nodes of a role keep the same one; "(differ)" marks a node that missed a change.',
    },
  ];
}

// "SHUTTINGDOWN 1, UNLOCKED 2"
export function describeCounts(counts: Partial<Record<string, number>>): string {
  return Object.entries(counts)
    .map(([k, n]) => `${k} ${n}`)
    .join(", ");
}

// The nodes that an operation on scope goes to, from the inventory.
export function scopeNodes(inventory: readonly Endpoint[], scope: Scope): string[] {
  return scope.kind === "node" ? [scope.node] : inventory.filter((e) => e.roles.includes(scope.role)).map((e) => e.name);
}

// What a confirmation says an operation acts on.
export function describeScope(inventory: readonly Endpoint[], scope: Scope): string {
  const nodes = scopeNodes(inventory, scope);
  if (scope.kind === "node") return `Node ${scope.node}`;
  return `Every node of role ${scope.role}: ${nodes.length} ${nodes.length === 1 ? "node" : "nodes"} (${nodes.join(", ")})`;
}

// Each value of the administrative state is the event of ITU-T X.731
// 7.1.3 that leads to it.
export const administrativeActions: Record<AdministrativeState, { label: string; effect: string }> = {
  SHUTTINGDOWN: {
    label: "Start the drain (shut down: SHUTTINGDOWN)",
    effect: "The role takes no new registrations or sessions, serves the ones it has, and becomes LOCKED when none is left or the timeout has passed.",
  },
  UNLOCKED: {
    label: "Cancel the drain or unlock (UNLOCKED)",
    effect: "The role takes new registrations and sessions again.",
  },
  LOCKED: {
    label: "Lock (LOCKED)",
    effect: "The role serves no one from now on; it decides what happens to the sessions it still has.",
  },
};

// One line for each node of a FanOut: what it did, or why it failed.
export function fanOutLines<T>(f: FanOut<T>, describeResult: (r: T) => string): { node: string; ok: boolean; text: string }[] {
  return f.nodes.map((n) => {
    const parts: string[] = [];
    if (n.result !== null) parts.push(describeResult(n.result));
    if (n.error !== null) parts.push(`failed: ${describeNodeError(n.error)}`);
    return { node: n.node, ok: n.error === null, text: parts.join("; ") };
  });
}

export function describeFanOutTotals(f: FanOut<unknown>): string {
  return `${f.succeeded} succeeded, ${f.failed} failed`;
}

// JSON on one line, as a setting's value is shown and typed.
export function compactJSON(v: unknown): string {
  return JSON.stringify(v) ?? "null";
}

// settingEdits reads the new value typed for each changeable setting as
// JSON, and keeps only those that differ from the present value. A value
// that is not JSON is an error of that setting.
export function settingEdits(
  current: readonly { name: string; value: unknown }[],
  typed: Readonly<Record<string, string>>,
): { settings: Record<string, unknown>; errors: Record<string, string> } {
  const settings: Record<string, unknown> = {};
  const errors: Record<string, string> = {};
  for (const s of current) {
    const text = typed[s.name];
    if (text === undefined || text.trim() === "") continue;
    let value: unknown;
    try {
      value = JSON.parse(text);
    } catch {
      errors[s.name] = 'not JSON: a string needs its quotes, such as "all"';
      continue;
    }
    if (compactJSON(value) !== compactJSON(s.value)) settings[s.name] = value;
  }
  return { settings, errors };
}

// refusedSettings gathers, for each setting, why each node refused it.
export function refusedSettings(f: FanOut<unknown>): Record<string, string[]> {
  const refused: Record<string, string[]> = {};
  for (const n of f.nodes) {
    for (const [name, reason] of Object.entries(n.error?.settings ?? {})) (refused[name] ??= []).push(`${n.node}: ${reason}`);
  }
  return refused;
}

// "hss.admission: "all" -> "test_users"" for each setting that a change
// changed; reversed for what a rollback would put back.
export function describeChanges(changes: readonly { name: string; previous: unknown; value: unknown }[], rollback = false): string[] {
  if (changes.length === 0) return ["no setting changed"];
  return changes.map((c) =>
    rollback ? `${c.name}: ${compactJSON(c.value)} -> ${compactJSON(c.previous)}` : `${c.name}: ${compactJSON(c.previous)} -> ${compactJSON(c.value)}`,
  );
}

// How many outcomes a tally lists; the rest it only counts, so that a
// file of any length takes bounded memory on the screen too.
export const maxListed = 500;

// OutcomeTally counts the outcomes of a bulk provisioning or a restore
// as they arrive, and lists those that listed() picks: the rejected and
// failed ones, or for the diff of a restore every one that would change
// something.
export interface OutcomeTally {
  counts: Record<Result, number>;
  listed: Outcome[];
  // Picked, but past maxListed.
  unlisted: number;
  add(o: Outcome): void;
}

export function outcomeTally(listed: (o: Outcome) => boolean): OutcomeTally {
  const counts = Object.fromEntries(results.map((r) => [r, 0])) as Record<Result, number>;
  const t: OutcomeTally = {
    counts,
    listed: [],
    unlisted: 0,
    add(o) {
      counts[o.result]++;
      if (!listed(o)) return;
      if (t.listed.length < maxListed) t.listed.push(o);
      else t.unlisted++;
    },
  };
  return t;
}

export const isProblem = (o: Outcome): boolean => o.result === "rejected" || o.result === "failed";
export const isChange = (o: Outcome): boolean => o.result !== "unchanged";

// "created 3, unchanged 1" with every result that happened.
export function describeTally(counts: Readonly<Record<Result, number>>): string {
  const parts = results.filter((r) => counts[r] > 0).map((r) => `${r} ${counts[r]}`);
  return parts.length === 0 ? "no records" : parts.join(", ");
}

// The fields of the single-record form, as typed.
export interface RecordFields {
  private_identity: string;
  public_identities: string;
  tel: string;
  password: string;
  k: string;
  opc: string;
  amf: string;
  sqn: string;
  ut_password: string;
  mmtel_services: string;
  test_user: boolean;
  service_profile: string;
}

// recordFromFields builds the SubscriptionRecord of a PUT, leaving out
// each field left empty. It checks only what the form must have; the
// OAM checks the rest, test data included, and names the field.
export function recordFromFields(f: RecordFields): { record: SubscriptionRecord | null; errors: Partial<Record<keyof RecordFields, string>> } {
  const errors: Partial<Record<keyof RecordFields, string>> = {};
  const publics = f.public_identities
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");
  if (f.private_identity.trim() === "") errors.private_identity = "required";
  if (publics.length === 0) errors.public_identities = "at least one";
  const aka = f.k !== "" || f.opc !== "" || f.amf !== "" || f.sqn !== "";
  if (aka && (f.k === "" || f.opc === "")) errors.k = "AKA needs both K and OPc";
  if (!aka && f.password === "") errors.password = "a password or the AKA K and OPc: the HSS shows neither, so they are typed again";
  if (Object.keys(errors).length > 0) return { record: null, errors };
  const record: SubscriptionRecord = { private_identity: f.private_identity.trim(), public_identities: publics };
  if (f.password !== "") record.password = f.password;
  if (aka) {
    record.aka = { k: f.k, opc: f.opc };
    if (f.amf !== "") record.aka.amf = f.amf;
    if (f.sqn !== "") record.aka.sqn = f.sqn;
  }
  if (f.tel.trim() !== "") record.tel = f.tel.trim();
  if (f.ut_password !== "") record.ut_password = f.ut_password;
  if (f.mmtel_services.trim() !== "") record.mmtel_services = f.mmtel_services;
  if (f.test_user) record.test_user = true;
  if (f.service_profile.trim() !== "") record.service_profile = f.service_profile.trim();
  return { record, errors };
}

// "export hss.subscriptions: completed, 2 records"
export function describeStep(s: Step): string {
  const name = s.section === "" ? s.name : `${s.name} ${s.section}`;
  const records = s.records > 0 ? `, ${s.records} ${s.records === 1 ? "record" : "records"}` : "";
  return `${name}: ${s.state}${records}${s.error === "" ? "" : `: ${s.error}`}`;
}

// steps keeps the latest report of each step, in the order the steps
// started: a backup or a restore sends a step as it starts, goes on and
// ends.
export function mergeStep(steps: readonly Step[], s: Step): Step[] {
  const i = steps.findIndex((x) => x.name === s.name && x.section === s.section && x.started === s.started);
  return i < 0 ? [...steps, s] : steps.map((x, j) => (j === i ? s : x));
}

// "hss.subscriptions 2"
export function describeRecords(records: Readonly<Record<string, number>>): string {
  return Object.entries(records)
    .map(([section, n]) => `${section} ${n}`)
    .join("\n");
}

// "hss.test_user_service_profile: changed again by ... (generation 6)"
export function describeConflicts(settings: Readonly<Record<string, string>>): string[] {
  return Object.entries(settings).map(([name, why]) => `${name}: ${why}`);
}

// --- Alarms ---

// Which records the screen asks for. Every record in an alarm list is
// an active alarm in the words of TS 28.111 6.14, the cleared ones that
// wait for an acknowledgement included.
export const alarmViews = {
  all: { label: "All in the lists", cleared: null },
  "not-cleared": { label: "Not cleared", cleared: false },
  cleared: { label: "Cleared only", cleared: true },
} as const;
export type AlarmView = keyof typeof alarmViews;

// The filter of the view and of a minimum severity, "" for any.
export function alarmFilter(view: AlarmView, minSeverity: string): AlarmFilter {
  const min = (minSeverities as readonly string[]).includes(minSeverity) ? (minSeverity as MinSeverity) : null;
  return { cleared: alarmViews[view].cleared, min_severity: min };
}

// How many records of each severity the answer has, in the order of
// perceivedSeverities, leaving out a severity with none.
export function severityCounts(alarms: readonly AlarmRecord[]): { severity: PerceivedSeverity; count: number }[] {
  return perceivedSeverities.map((severity) => ({ severity, count: alarms.filter((a) => a.perceived_severity === severity).length })).filter((c) => c.count > 0);
}

// The nodes whose alarms the answer could not give, with why. Their
// alarms are unknown, which is not the same as none.
export function unknownAlarmNodes(a: Alarms): { node: string; why: string }[] {
  return a.nodes.filter((n) => n.error !== null).map((n) => ({ node: n.node, why: describeNodeError(n.error!) }));
}

// The nodes whose list was full and dropped records (TS 28.111
// 7.3.2.1): the list no longer holds every alarm.
export function droppingNodes(a: Alarms): { node: string; dropped: number }[] {
  return a.nodes.filter((n) => (n.result?.dropped_records ?? 0) > 0).map((n) => ({ node: n.node, dropped: n.result!.dropped_records }));
}

// "MAJOR COMMUNICATIONS_ALARM COMMUNICATIONS_SUBSYSTEM_FAILURE: Diameter
// peer down", for a confirmation.
export function describeAlarm(r: AlarmRecord): string {
  return `${r.perceived_severity} ${r.alarm_type} ${r.probable_cause}${r.specific_problem === "" ? "" : `: ${r.specific_problem}`}`;
}

// What acknowledging a record, or taking the acknowledgement back, does
// (TS 28.111 6.7, 6.14).
export function ackAction(r: AlarmRecord): { to: AckState; label: string; effect: string } {
  if (r.ack_state === "ACKNOWLEDGED") {
    return { to: "UNACKNOWLEDGED", label: "Unacknowledge", effect: "The alarm shows as not acknowledged again." };
  }
  if (r.perceived_severity === "CLEARED") {
    return { to: "ACKNOWLEDGED", label: "Acknowledge", effect: "The alarm is cleared: acknowledged, it leaves the alarm list of the node." };
  }
  return { to: "ACKNOWLEDGED", label: "Acknowledge", effect: "The alarm stays in the list, acknowledged, and leaves it when its condition clears." };
}

// ackConfirmation is what the alarm panel asks the operator to confirm
// before it changes the acknowledgement of r, or why it asks nothing:
// the record keeps who changed its acknowledgement, so the OAM takes no
// change without an operator.
export function ackConfirmation(r: AlarmRecord, operator: string): { scope: string[] } | { refused: string } {
  const action = ackAction(r);
  if (operator.trim() === "") {
    return { refused: `To ${action.label.toLowerCase()} an alarm, enter your name in Operator first: the alarm record keeps who changed its acknowledgement.` };
  }
  return { scope: [`Node ${r.node}`, `Alarm ${r.alarm_id}: ${describeAlarm(r)}`, `Object ${r.object_instance}`, `Operator ${operator}`, action.effect] };
}

// The acknowledgement of a record, one line each: the state, who last
// changed it and through which system, and when (TS 28.111 7.4.1). The
// record keeps who took an acknowledgement back as well.
export function describeAck(r: AlarmRecord): string {
  const changed = r.ack_time !== null;
  const who = [r.ack_user_id === "" ? "" : `by ${r.ack_user_id}`, r.ack_system_id === "" ? "" : `via ${r.ack_system_id}`].filter((s) => s !== "").join(" ");
  const state = r.ack_state === "ACKNOWLEDGED" ? "yes" : changed ? "no, taken back" : "no";
  return [state, who, changed ? formatTime(r.ack_time!) : ""].filter((s) => s !== "").join("\n");
}
