// A stand-in for the OAM API of internal/oam, with the shapes of
// test/fixtures.ts and enough behaviour to drive every screen: a drain
// that loses a user at each read of the state, configuration changes
// with a history and rollbacks that can conflict, a bulk provisioning
// that answers each line as it reads it, backups and restores that
// stream their steps, and alarm lists in which the media relay alarm of
// pcscf-1 clears 20 s after the first read. hss-2 never answers. It keeps everything in
// memory and is for `npm run dev` and the browser check (mock/serve.ts)
// only. MOCK_DELAY_MS slows each streamed line, so that the streaming
// can be watched.

import type { IncomingMessage, ServerResponse } from "node:http";
import {
  alarms,
  backup,
  changeRecord,
  failedBackup,
  hssConfiguration,
  impi1,
  impi2,
  inventory,
  nodeState,
  pcscfConfiguration,
  realm,
  subscription,
} from "../test/fixtures.ts";

type Json = Record<string, unknown>;

interface MockFunction {
  role: string;
  administrative_state: string;
  operational_state: string;
  users?: number;
  shutdown_deadline?: string;
}

interface MockNode {
  name: string;
  url: string;
  // Null for a node that never answers.
  state: (Json & { functions: MockFunction[]; configuration_generation: number; log_level: string }) | null;
  configuration: { configuration: Json; changeable: { name: string; description: string; value: unknown }[] };
}

interface Record_ {
  id: string;
  change_set: string;
  node: string;
  recorded_at: string;
  rollback_of?: string;
  forced?: boolean;
  change: { node: string; instance: string; generation: number; time: string; operator?: string; comment?: string; changes: { name: string; previous: unknown; value: unknown }[] };
}

const delayMs = Number(process.env["MOCK_DELAY_MS"] ?? "0");
// Long enough to look at the alarms first, short enough to watch the
// periodic refresh show the clear.
const relayClearsAfterMs = 20_000;

export function createMockOAM(): (req: IncomingMessage, res: ServerResponse, path: string) => Promise<void> {
  const nodes: MockNode[] = inventory.nodes.map((n) => ({
    name: n.name,
    url: n.url,
    state: null,
    configuration: n.roles.includes("hss") ? structuredClone(hssConfiguration) : structuredClone(pcscfConfiguration),
  }));
  const at = (name: string) => nodes.find((n) => n.name === name)!;
  at("pcscf-1").state = nodeState<MockFunction>("pcscf", "pcscf-1", [{ role: "pcscf", administrative_state: "UNLOCKED", operational_state: "ENABLED", users: 3 }]);
  at("pcscf-2").state = nodeState<MockFunction>("pcscf", "pcscf-2", [{ role: "pcscf", administrative_state: "UNLOCKED", operational_state: "ENABLED", users: 5 }]);
  at("cscf-1").state = nodeState<MockFunction>("cscf", "cscf-1", [
    { role: "icscf", administrative_state: "UNLOCKED", operational_state: "ENABLED" },
    { role: "scscf", administrative_state: "UNLOCKED", operational_state: "DISABLED", users: 0 },
  ]);
  at("hss-1").state = nodeState<MockFunction>("hss", "hss-1", [{ role: "hss", administrative_state: "UNLOCKED", operational_state: "ENABLED" }], 5);

  const history: Record_[] = [structuredClone(changeRecord)];
  // The credentials of each subscription stay here, as in the HSS; no
  // answer shows them.
  const subscriptions = new Map<string, Json>([
    [impi1, { ...subscription, password: "test-password-1" }],
    [impi2, { private_identity: impi2, public_identities: [`sip:+12025550102@${realm}`], aka: { k: "465b5ce8b199b49faa5f0a2ee238a6bc", opc: "cd63cb71954a9f4e48a5994e37a02baf" } }],
  ]);
  const backups: { manifest: Json; records: Json[] }[] = [
    { manifest: structuredClone(failedBackup), records: [] },
    { manifest: structuredClone(backup), records: [...subscriptions.values()].map((s) => structuredClone(s)) },
  ];

  // The alarm list of each node, from the records of the fixture, which
  // carry their node; a node's own answer has none.
  const alarmLists = new Map<string, Json[]>(
    nodes.map((n) => [
      n.name,
      alarms.alarms
        .filter((a) => a.node === n.name)
        .map((a) => {
          const { node: _node, ...r } = structuredClone(a);
          return r as Json;
        }),
    ]),
  );
  let firstAlarmRead: number | null = null;
  const severities = ["CRITICAL", "MAJOR", "MINOR", "WARNING", "INDETERMINATE", "CLEARED"];

  // readAlarms is GET /oam/v1/alarms with the filter of the maintenance
  // API, merged as the OAM does.
  function readAlarms(query: URLSearchParams): Json {
    firstAlarmRead ??= Date.now();
    if (Date.now() - firstAlarmRead >= relayClearsAfterMs) {
      const relay = alarmLists.get("pcscf-1")?.find((r) => r["specific_problem"] === "media relay unreachable");
      if (relay) {
        const now = new Date().toISOString();
        Object.assign(relay, { perceived_severity: "CLEARED", alarm_changed_time: now, alarm_cleared_time: now });
      }
    }
    for (const key of query.keys()) {
      if (key !== "cleared" && key !== "min_severity") throw { status: 400, error: `invalid request: unknown query parameter ${key}; the filters are cleared and min_severity` };
    }
    const cleared = query.get("cleared");
    const min = query.get("min_severity");
    if (cleared !== null && cleared !== "true" && cleared !== "false") throw { status: 400, error: `invalid request: cleared: ${JSON.stringify(cleared)} is not true or false` };
    if (min !== null && !severities.slice(0, 4).includes(min)) throw { status: 400, error: `invalid request: min_severity: ${JSON.stringify(min)} is none of CRITICAL, MAJOR, MINOR, WARNING` };
    const rank = (r: Json) => severities.indexOf(String(r["perceived_severity"]));
    const keeps = (r: Json) => {
      const isCleared = r["perceived_severity"] === "CLEARED";
      if (cleared !== null && String(isCleared) !== cleared) return false;
      if (min === null) return true;
      if (isCleared) return false;
      return r["perceived_severity"] === "INDETERMINATE" || rank(r) <= severities.indexOf(min);
    };
    const merged: Json[] = [];
    const f = fanOut(nodes, (n) => {
      const list = alarmLists.get(n.name) ?? [];
      const kept = list.filter(keeps);
      merged.push(...kept.map((r) => ({ node: n.name, ...structuredClone(r) })));
      const times = list.flatMap((r) => [r["alarm_raised_time"], r["alarm_changed_time"], r["ack_time"]]).filter((t): t is string => typeof t === "string");
      const last = times.sort().at(-1) ?? String(n.state!["start_time"]);
      const dropped = alarms.nodes.find((x) => x.node === n.name)?.result?.dropped_records ?? 0;
      return { instance: n.state!["instance"], num_of_alarm_records: list.length, listed: kept.length, last_modification: last, dropped_records: dropped };
    });
    merged.sort((a, b) => rank(a) - rank(b) || String(b["alarm_raised_time"]).localeCompare(String(a["alarm_raised_time"])));
    return { ...f, alarms: merged };
  }

  // setAckState is the node's PUT /maintenance/v1/alarms/{id}/ack-state:
  // a cleared alarm leaves the list once acknowledged (TS 28.111 6.14).
  function setAckState(n: MockNode, id: string, body: Json): Json {
    const list = alarmLists.get(n.name) ?? [];
    const r = list.find((x) => x["alarm_id"] === id);
    if (!r) throw { status_code: 404, error: `no such alarm in the list: ${JSON.stringify(id)}` };
    const to = body["ack_state"];
    if (to !== "ACKNOWLEDGED" && to !== "UNACKNOWLEDGED") throw { status_code: 400, error: `invalid request: ack_state ${JSON.stringify(to)} is neither ACKNOWLEDGED nor UNACKNOWLEDGED` };
    if (r["ack_state"] !== to) {
      Object.assign(r, { ack_state: to, ack_time: new Date().toISOString(), ack_user_id: body["operator"] ?? "" });
      if (!r["ack_user_id"]) delete r["ack_user_id"];
      if (to === "ACKNOWLEDGED" && r["perceived_severity"] === "CLEARED") list.splice(list.indexOf(r), 1);
    }
    return structuredClone(r);
  }

  const roleNodes = (role: string) => inventory.nodes.filter((n) => n.roles.includes(role)).map((n) => at(n.name));

  function targets(path: string): MockNode[] | null {
    const m = /^(nodes|roles)\/([^/]+)\//.exec(path);
    if (!m) return null;
    const name = decodeURIComponent(m[2]!);
    if (m[1] === "nodes") return inventory.nodes.some((n) => n.name === name) ? [at(name)] : [];
    return roleNodes(name);
  }

  // fanOut gives the answer of each node, or why it failed.
  function fanOut(ts: MockNode[], op: (n: MockNode) => unknown): Json {
    const results = ts.map((n) => {
      if (n.state === null) return { node: n.name, url: n.url, error: { error: "no answer within 5s" } };
      try {
        return { node: n.name, url: n.url, result: op(n) };
      } catch (e) {
        return { node: n.name, url: n.url, error: e as Json };
      }
    });
    return { succeeded: results.filter((r) => "result" in r).length, failed: results.filter((r) => "error" in r).length, nodes: results };
  }

  // A read of the state is the clock of the drain: each read ends one
  // session of each draining role.
  function readState(n: MockNode): Json {
    for (const f of n.state!.functions) {
      if (f.administrative_state !== "SHUTTINGDOWN") continue;
      f.users = Math.max(0, (f.users ?? 0) - 1);
      if (f.users === 0) {
        f.administrative_state = "LOCKED";
        delete f.shutdown_deadline;
      }
    }
    return structuredClone(n.state!);
  }

  function setAdministrativeState(n: MockNode, role: string, body: Json): MockFunction {
    const f = n.state!.functions.find((x) => x.role === role);
    if (!f) throw { status_code: 404, error: `no such role on this node: ${role}` };
    const to = body["administrative_state"];
    if (to === "SHUTTINGDOWN") {
      if (f.administrative_state === "LOCKED") {
        throw { status_code: 409, error: `invalid administrative state transition: shut down applies only to an UNLOCKED function, and ${role} is LOCKED (X.731 7.1.3.3)` };
      }
      if (f.administrative_state === "UNLOCKED") {
        if ((f.users ?? 0) === 0) f.administrative_state = "LOCKED";
        else {
          f.administrative_state = "SHUTTINGDOWN";
          const timeout = Number(body["timeout_seconds"] ?? 0);
          if (timeout > 0) f.shutdown_deadline = new Date(Date.now() + timeout * 1000).toISOString();
        }
      }
    } else if (to === "UNLOCKED" || to === "LOCKED") {
      f.administrative_state = to;
      delete f.shutdown_deadline;
    } else {
      throw { status_code: 400, error: `administrative_state: ${JSON.stringify(to)} is none of UNLOCKED, SHUTTINGDOWN, LOCKED` };
    }
    return structuredClone(f);
  }

  function configuration(n: MockNode): Json {
    return { generation: n.state!.configuration_generation, ...structuredClone(n.configuration) };
  }

  function validateSetting(n: MockNode, name: string, value: unknown): string | null {
    const s = n.configuration.changeable.find((c) => c.name === name);
    if (!s) return "not a setting that changes while the node runs; change the configuration file and restart the node";
    if (typeof value !== "string") return "json: cannot unmarshal into a string";
    if (name === "hss.admission" && value !== "all" && value !== "test_users") return `admission: ${JSON.stringify(value)} is neither all nor test_users`;
    const profiles = ((n.configuration.configuration["hss"] as Json)["service_profiles"] ?? {}) as Json;
    if (name !== "hss.admission" && !(value in profiles)) return `no service profile ${JSON.stringify(value)}`;
    return null;
  }

  function changeNode(n: MockNode, settings: Json, generation: number, operator: string, comment: string, extra: Partial<Record_> = {}): Record_ {
    const refused: Record<string, string> = {};
    for (const [name, value] of Object.entries(settings)) {
      const why = validateSetting(n, name, value);
      if (why) refused[name] = why;
    }
    const names = Object.keys(refused).sort();
    if (names.length > 0) throw { status_code: 400, error: `${names.length} of the settings refused, nothing changed: ${names.join(", ")}`, settings: refused };
    const changes = [];
    for (const [name, value] of Object.entries(settings)) {
      const s = n.configuration.changeable.find((c) => c.name === name)!;
      if (JSON.stringify(s.value) !== JSON.stringify(value)) changes.push({ name, previous: s.value, value });
      s.value = value;
    }
    n.state!.configuration_generation = generation;
    const now = new Date().toISOString();
    const r: Record_ = {
      id: randomHex(),
      change_set: randomHex(),
      node: n.name,
      recorded_at: now,
      change: { node: String(n.state!["node"]), instance: String(n.state!["instance"]), generation, time: now, ...(operator ? { operator } : {}), ...(comment ? { comment } : {}), changes },
      ...extra,
    };
    history.push(r);
    return r;
  }

  function changeConfiguration(ts: MockNode[], body: Json): Json {
    const settings = (body["settings"] ?? {}) as Json;
    const answered = ts.filter((n) => n.state !== null);
    const generation = Math.max(0, ...answered.map((n) => n.state!.configuration_generation)) + 1;
    const changeSet = randomHex();
    return fanOut(ts, (n) => changeNode(n, settings, generation, String(body["operator"] ?? ""), String(body["comment"] ?? ""), { change_set: changeSet }));
  }

  function rollback(id: string, body: Json): Record_ {
    const index = history.findIndex((r) => r.id === id);
    if (index < 0) throw { status: 404, error: `not found: no change ${JSON.stringify(id)} in the history` };
    const target = history[index]!;
    const n = at(target.node);
    if (n.state === null) throw { status: 502, error: `upstream failed: node ${n.name}: no answer within 5s` };
    const conflicts: Record<string, string> = {};
    for (const later of history.slice(index + 1)) {
      if (later.node !== target.node) continue;
      for (const c of later.change.changes) {
        if (target.change.changes.some((t) => t.name === c.name) && !(c.name in conflicts)) {
          conflicts[c.name] = `changed again by ${later.id} (generation ${later.change.generation})`;
        }
      }
    }
    const settings: Json = {};
    for (const c of target.change.changes) {
      settings[c.name] = c.previous;
      const now = n.configuration.changeable.find((s) => s.name === c.name)?.value;
      if (!(c.name in conflicts) && JSON.stringify(now) !== JSON.stringify(c.value)) {
        conflicts[c.name] = `the node has ${JSON.stringify(now)}, not the ${JSON.stringify(c.value)} that the change set`;
      }
    }
    if (Object.keys(conflicts).length > 0 && body["force"] !== true) {
      throw { status: 409, error: `settings of change ${id} changed since; send force to roll back anyway`, settings: conflicts };
    }
    const forced = Object.keys(conflicts).length > 0;
    return changeNode(n, settings, n.state.configuration_generation + 1, String(body["operator"] ?? ""), String(body["comment"] || `rollback of ${id}`), {
      rollback_of: id,
      ...(forced ? { forced } : {}),
    });
  }

  function publicView(s: Json): Json {
    return {
      private_identity: s["private_identity"],
      public_identities: s["public_identities"],
      authentication_schemes: s["aka"] ? ["Digest-AKAv1-MD5"] : ["SIP Digest"],
      registration_state: "NOT_REGISTERED",
      ...(s["test_user"] ? { test_user: true } : {}),
      ...(s["service_profile"] ? { service_profile: s["service_profile"] } : {}),
    };
  }

  // provisionRecord is what the OAM does with one record: the checks of
  // oam.SubscriptionRecord.validate in short, then the PUT to the HSS.
  function provisionRecord(raw: string, dryRun: boolean): { result: string; private_identity?: string; reason?: string; status?: number } {
    let rec: Json;
    try {
      rec = JSON.parse(raw) as Json;
    } catch {
      return { result: "rejected", reason: "not a JSON object: syntax error at offset 1" };
    }
    const impi = String(rec["private_identity"] ?? "");
    const reject = (reason: string, status = 400) => ({ result: "rejected", private_identity: impi, reason, status });
    if (impi === "") return reject("private_identity: required");
    if (!/^[^@]+@[^@]+$/.test(impi)) return reject("private_identity: not of the form user@realm");
    if (/^\d+@/.test(impi) && !/^00101\d{10}@/.test(impi)) return reject("private_identity: test data: an IMSI must be of the test PLMN 001/01");
    if (rec["delete"] === true) {
      if (!subscriptions.has(impi)) return { result: "unchanged", private_identity: impi, reason: "no such subscription", status: 404 };
      if (!dryRun) subscriptions.delete(impi);
      return { result: "deleted", private_identity: impi };
    }
    const publics = (rec["public_identities"] ?? []) as string[];
    if (publics.length === 0) return reject("public_identities: at least one is required");
    if (!rec["password"] && !rec["aka"]) return reject("password or aka: at least one is required");
    for (const [i, p] of publics.entries()) {
      const tel = /^tel:\+?(\d+)/.exec(p);
      if (tel && !/^1[2-9]\d\d55501\d\d$/.test(tel[1]!)) return reject(`public_identities[${i}]: test data: not a fictional +1 NXX 555 01XX number or a test IMSI`);
    }
    for (const [other, s] of subscriptions) {
      if (other !== impi && (s["public_identities"] as string[]).some((p) => publics.includes(p))) {
        return reject(`HSS: 409: a public identity belongs to ${other}`, 409);
      }
    }
    const before = subscriptions.get(impi);
    const result = before === undefined ? "created" : JSON.stringify(before) === JSON.stringify(rec) ? "unchanged" : "updated";
    if (!dryRun) subscriptions.set(impi, rec);
    return { result, private_identity: impi };
  }

  function outcome(o: ReturnType<typeof provisionRecord>, line: number, dryRun: boolean): Json {
    const { status: _status, ...rest } = o;
    return { ...(line > 0 ? { line } : {}), ...rest, ...(dryRun ? { dry_run: true } : {}) };
  }

  function emptySummary(dryRun: boolean): Record<string, number | boolean> {
    return { lines: 0, created: 0, updated: 0, unchanged: 0, deleted: 0, rejected: 0, failed: 0, ...(dryRun ? { dry_run: true } : {}) };
  }

  // The answer streams while the body is read, a line at a time, as the
  // OAM does.
  async function bulk(req: IncomingMessage, res: ServerResponse, dryRun: boolean): Promise<void> {
    res.writeHead(200, { "Content-Type": "application/x-ndjson" });
    const summary = emptySummary(dryRun);
    let pending = "";
    let line = 0;
    const handle = async (text: string) => {
      line++;
      summary["lines"] = line;
      if (text.trim() === "") return;
      const o = provisionRecord(text.replace(/\r$/, ""), dryRun);
      summary[o.result] = (summary[o.result] as number) + 1;
      await sendLine(res, { outcome: outcome(o, line, dryRun) });
    };
    for await (const chunk of req) {
      const parts = (pending + (chunk as Buffer).toString("utf8")).split("\n");
      pending = parts.pop() ?? "";
      for (const p of parts) await handle(p);
    }
    if (pending !== "") await handle(pending);
    await sendLine(res, { summary });
    res.end();
  }

  async function createBackup(res: ServerResponse, body: Json): Promise<void> {
    res.writeHead(200, { "Content-Type": "application/x-ndjson" });
    const now = new Date();
    const id = now.toISOString().replace(/[-:]/g, "").replace(/\.\d+Z$/, "Z") + "-" + randomHex().slice(0, 6);
    const records = [...subscriptions.values()].map((s) => structuredClone(s));
    const started = now.toISOString();
    const exp = { name: "export", section: "hss.subscriptions", state: "running", started };
    await sendLine(res, { step: exp });
    const exported = { ...exp, state: "completed", finished: new Date().toISOString(), records: records.length };
    await sendLine(res, { step: exported });
    const store = { name: "store", state: "running", started: new Date().toISOString() };
    await sendLine(res, { step: store });
    const stored = { ...store, state: "completed", finished: new Date().toISOString() };
    await sendLine(res, { step: stored });
    const manifest: Json = {
      id,
      created: started,
      ...(body["operator"] ? { operator: body["operator"] } : {}),
      ...(body["comment"] ? { comment: body["comment"] } : {}),
      state: "completed",
      sections: ["hss.subscriptions"],
      steps: [exported, stored],
      records: { "hss.subscriptions": records.length },
      bytes: JSON.stringify(records).length + 200,
    };
    backups.push({ manifest, records });
    await sendLine(res, { manifest });
    res.end();
  }

  async function restore(res: ServerResponse, b: { manifest: Json; records: Json[] }, dryRun: boolean, body: Json): Promise<void> {
    res.writeHead(200, { "Content-Type": "application/x-ndjson" });
    const run: Json = { started: new Date().toISOString(), ...(dryRun ? { dry_run: true } : {}), ...(body["operator"] ? { operator: body["operator"] } : {}) };
    const steps: Json[] = [];
    const step = async (name: string, work: () => Promise<number>) => {
      const s: Json = { name, state: "running", started: new Date().toISOString() };
      await sendLine(res, { step: s });
      const records = await work();
      const done = { ...s, state: "completed", finished: new Date().toISOString(), records };
      steps.push(done);
      await sendLine(res, { step: done });
    };
    const summary = emptySummary(dryRun);
    await step("verify", async () => b.records.length);
    await step(dryRun ? "diff" : "apply", async () => {
      for (const [i, rec] of b.records.entries()) {
        const o = provisionRecord(JSON.stringify(rec), dryRun);
        summary[o.result] = (summary[o.result] as number) + 1;
        await sendLine(res, { outcome: outcome(o, i + 2, dryRun) });
      }
      return b.records.length;
    });
    summary["lines"] = b.records.length;
    const restores = (b.manifest["restores"] ?? []) as Json[];
    b.manifest["restores"] = [...restores, { ...run, finished: new Date().toISOString(), state: "completed", steps, summary }];
    await sendLine(res, { summary });
    res.end();
  }

  function testUsers(): Json {
    const conf = at("hss-1").configuration;
    const value = (name: string) => conf.changeable.find((c) => c.name === name)?.value;
    const profiles = (conf.configuration["hss"] as Json)["service_profiles"] as Record<string, { features: string[] }>;
    return {
      hss_node: "hss-1",
      admission: value("hss.admission"),
      default_service_profile: value("hss.default_service_profile"),
      test_user_service_profile: value("hss.test_user_service_profile"),
      service_profiles: Object.fromEntries(Object.entries(profiles).map(([k, v]) => [k, v.features])),
    };
  }

  return async (req, res, path) => {
    const method = req.method ?? "GET";
    const url = new URL(path, "http://mock/");
    const route = url.pathname.replace(/^\//, "");
    const dryRun = url.searchParams.get("dry_run") === "true";
    const body = async (): Promise<Json> => {
      const text = await readBody(req);
      return text.trim() === "" ? {} : (JSON.parse(text) as Json);
    };
    try {
      if (method === "GET" && route === "inventory") return json(res, 200, inventory);
      if (method === "GET" && route === "state") return json(res, 200, fanOut(nodes, readState));
      if (method === "GET" && route === "alarms") return json(res, 200, readAlarms(url.searchParams));

      const ts = targets(route);
      if (ts !== null) {
        if (ts.length === 0) return json(res, 404, { error: `not found: no such node or role in the inventory: ${route}` });
        const tail = route.replace(/^(nodes|roles)\/[^/]+\//, "");
        if (method === "GET" && tail === "state") return json(res, 200, fanOut(ts, readState));
        if (method === "GET" && tail === "log-level") return json(res, 200, fanOut(ts, (n) => ({ level: n.state!.log_level })));
        if (method === "PUT" && tail === "log-level") {
          const level = String((await body())["level"]);
          return json(
            res,
            200,
            fanOut(ts, (n) => {
              const previous = n.state!.log_level;
              n.state!.log_level = level.toUpperCase();
              return { level: n.state!.log_level, previous };
            }),
          );
        }
        if (method === "GET" && tail === "config") return json(res, 200, fanOut(ts, configuration));
        if (method === "PATCH" && tail === "config") return json(res, 200, changeConfiguration(ts, await body()));
        if (method === "GET" && tail === "config/history") {
          const limit = Number(url.searchParams.get("limit") ?? "50");
          return json(res, 200, { changes: history.filter((r) => r.node === ts[0]!.name).slice(-limit).reverse() });
        }
        if (method === "PUT" && tail === "administrative-state" && route.startsWith("roles/")) {
          const b = await body();
          const role = decodeURIComponent(route.split("/")[1]!);
          return json(res, 200, fanOut(ts, (n) => setAdministrativeState(n, role, b)));
        }
        const ack = /^alarms\/([^/]+)\/ack-state$/.exec(tail);
        if (method === "PUT" && ack && route.startsWith("nodes/")) {
          const b = await body();
          return json(res, 200, fanOut(ts, (n) => setAckState(n, decodeURIComponent(ack[1]!), b)));
        }
        const fn = /^functions\/([^/]+)\/administrative-state$/.exec(tail);
        if (method === "PUT" && fn) {
          const b = await body();
          return json(res, 200, fanOut(ts, (n) => setAdministrativeState(n, decodeURIComponent(fn[1]!), b)));
        }
      }

      let m = /^config\/history\/([^/]+)(\/rollback)?$/.exec(route);
      if (m && method === "GET" && !m[2]) {
        const r = history.find((x) => x.id === m![1]);
        return r ? json(res, 200, r) : json(res, 404, { error: "not found" });
      }
      if (m && method === "POST" && m[2]) return json(res, 200, rollback(m[1]!, await body()));
      m = /^config\/change-sets\/([^/]+)\/rollback$/.exec(route);
      if (m && method === "POST") {
        const b = await body();
        const records = history.filter((r) => r.change_set === m![1]);
        if (records.length === 0) return json(res, 404, { error: `not found: no change set ${JSON.stringify(m[1])} in the history` });
        const results = records.map((r) => {
          try {
            return { node: r.node, url: at(r.node).url, result: rollback(r.id, b) };
          } catch (e) {
            const err = e as Json;
            return { node: r.node, url: at(r.node).url, error: { ...(err["status"] === 409 ? { status_code: 409 } : {}), error: err["error"], ...(err["settings"] ? { settings: err["settings"] } : {}) } };
          }
        });
        return json(res, 200, { succeeded: results.filter((r) => "result" in r).length, failed: results.filter((r) => "error" in r).length, nodes: results });
      }

      if (method === "POST" && route === "provisioning") return await bulk(req, res, dryRun);
      m = /^subscriptions\/([^/]+)(\/test-user|\/features\/([^/]+))?$/.exec(route);
      if (m) {
        const impi = decodeURIComponent(m[1]!);
        const sub = subscriptions.get(impi);
        if (!m[2]) {
          if (method === "GET") return sub ? json(res, 200, publicView(sub)) : json(res, 404, { error: "not found: no such subscription" });
          if (method === "PUT" || method === "DELETE") {
            const rec = method === "PUT" ? { ...(await body()), private_identity: impi } : { private_identity: impi, delete: true };
            const o = provisionRecord(JSON.stringify(rec), dryRun);
            if (o.result === "rejected") return json(res, o.status ?? 400, { error: o.reason });
            if (o.status === 404) return json(res, 404, { error: `not found: ${o.reason}` });
            return json(res, o.result === "created" && !dryRun ? 201 : 200, outcome(o, 0, dryRun));
          }
        }
        if (!sub) return json(res, 404, { error: "not found: no such subscription" });
        if (method === "PUT" && m[2] === "/test-user") {
          const b = await body();
          if (typeof b["test_user"] !== "boolean") return json(res, 400, { error: "invalid request: test_user: required" });
          if (b["test_user"]) sub["test_user"] = true;
          else delete sub["test_user"];
          return json(res, 200, publicView(sub));
        }
        if (method === "GET" && m[3]) {
          const feature = decodeURIComponent(m[3]);
          const t = testUsers() as { admission: string; default_service_profile: string; test_user_service_profile: string; service_profiles: Record<string, string[]> };
          if (!Object.values(t.service_profiles).some((fs) => fs.includes(feature))) {
            return json(res, 404, { error: `not found: no service profile of the HSS has feature ${JSON.stringify(feature)}` });
          }
          const testUser = sub["test_user"] === true;
          const [profile, source] = sub["service_profile"]
            ? [String(sub["service_profile"]), "subscription"]
            : testUser
              ? [t.test_user_service_profile, "test_users"]
              : [t.default_service_profile, "default"];
          const admitted = t.admission !== "test_users" || testUser;
          const has = t.service_profiles[profile]?.includes(feature) ?? false;
          const reason = !admitted ? "the HSS admits test users only" : has ? undefined : `service profile ${JSON.stringify(profile)} does not have it`;
          return json(res, 200, {
            private_identity: impi,
            feature,
            active: admitted && has,
            ...(reason ? { reason } : {}),
            test_user: testUser,
            service_profile: profile,
            profile_source: source,
            admission: t.admission,
            admitted,
            hss_node: "hss-1",
          });
        }
      }

      if (method === "GET" && route === "test-users") return json(res, 200, testUsers());
      if (method === "PUT" && (route === "test-users/service-profile" || route === "test-users/admission")) {
        const b = await body();
        const setting = route.endsWith("admission") ? "hss.admission" : "hss.test_user_service_profile";
        const value = route.endsWith("admission") ? b["admission"] : b["service_profile"];
        return json(res, 200, changeConfiguration(roleNodes("hss"), { settings: { [setting]: value }, operator: b["operator"], comment: b["comment"] }));
      }

      if (route === "backups" && method === "POST") return await createBackup(res, await body());
      if (route === "backups" && method === "GET") {
        return json(res, 200, { backups: [...backups].reverse().map((b) => b.manifest) });
      }
      m = /^backups\/([^/]+)(\/restore)?$/.exec(route);
      if (m) {
        const b = backups.find((x) => x.manifest["id"] === m![1]);
        if (!b) return json(res, 404, { error: `not found: no backup ${JSON.stringify(m[1])}` });
        if (method === "GET" && !m[2]) return json(res, 200, b.manifest);
        if (method === "POST" && m[2]) {
          const reqBody = await body();
          if (b.manifest["state"] !== "completed") return json(res, 409, { error: `conflict: backup ${m[1]} is ${String(b.manifest["state"])}, not completed` });
          return await restore(res, b, dryRun, reqBody);
        }
      }
      json(res, 404, { error: `no such resource: /oam/v1/${route}` });
    } catch (e) {
      const err = e as Json;
      if (typeof err["status"] === "number") return json(res, err["status"], { error: err["error"], ...(err["settings"] ? { settings: err["settings"] } : {}) });
      json(res, 500, { error: String(e) });
    }
  };
}

function randomHex(): string {
  return Array.from({ length: 16 }, () => Math.floor(Math.random() * 16).toString(16)).join("");
}

async function sendLine(res: ServerResponse, line: unknown): Promise<void> {
  res.write(JSON.stringify(line) + "\n");
  if (delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
}

function json(res: ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { "Content-Type": "application/json" }).end(JSON.stringify(body) + "\n");
}

async function readBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk as Buffer);
  return Buffer.concat(chunks).toString("utf8");
}
