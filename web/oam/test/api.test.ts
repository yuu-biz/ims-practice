import { afterEach, describe, expect, it, vi } from "vitest";
import {
  batches,
  createBackup,
  deleteSubscription,
  fetchAlarms,
  fetchBackups,
  fetchConfiguration,
  fetchFeature,
  fetchHistory,
  fetchInventory,
  fetchState,
  fetchSubscription,
  fetchTestUsers,
  HttpError,
  patchConfiguration,
  provision,
  putAckState,
  putAdministrativeState,
  putLogLevel,
  putSubscription,
  putTestUser,
  readStream,
  restoreBackup,
  rollbackChange,
  rollbackChangeSet,
  type Outcome,
  type StreamLine,
} from "../src/api.ts";
import {
  alarms,
  backup,
  changeRecord,
  failedBackup,
  featureState,
  hssConfiguration,
  impi1,
  inventory,
  ndjson,
  provisioningLines,
  refusedChange,
  restoreLines,
  rollbackConflict,
  rollbackRecord,
  state,
  subscription,
  testUsers,
} from "./fixtures.ts";

interface Asked {
  url: string;
  method: string;
  body: string | null;
}

// serve answers every request with status and body, and records what
// was asked.
function serve(status: number, body: string | (() => BodyInit)): Asked[] {
  const asked: Asked[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit = {}) => {
    asked.push({ url, method: init.method ?? "GET", body: typeof init.body === "string" ? init.body : null });
    return new Response(typeof body === "string" ? body : body(), { status });
  });
  return asked;
}

// chunks is a body that arrives in the pieces given, each after the one
// before has been read.
function chunks(pieces: readonly (string | Uint8Array)[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  let i = 0;
  return new ReadableStream({
    pull(controller) {
      const piece = pieces[i++];
      if (piece === undefined) {
        controller.close();
        return;
      }
      controller.enqueue(typeof piece === "string" ? encoder.encode(piece) : piece);
    },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("nodes", () => {
  it("reads the inventory", async () => {
    const asked = serve(200, JSON.stringify(inventory));
    expect((await fetchInventory()).map((n) => n.name)).toEqual(["pcscf-1", "pcscf-2", "cscf-1", "hss-1", "hss-2"]);
    expect(asked[0]?.url).toBe("/oam/v1/inventory");
  });

  it("keeps a failed node in the state, with why it failed", async () => {
    serve(200, JSON.stringify(state));
    const f = await fetchState();
    expect([f.succeeded, f.failed, f.nodes.length]).toEqual([4, 1, 5]);
    expect(f.nodes[4]).toEqual({ node: "hss-2", url: "http://hss-2:8085", result: null, error: { status_code: null, error: "no answer within 5s", settings: {} } });
    const drained = f.nodes[1]?.result?.functions[0];
    expect(drained).toMatchObject({ administrative_state: "SHUTTINGDOWN", users: 2, shutdown_deadline: "2026-11-20T09:15:00Z" });
    // A role that counts no users has none, not zero.
    expect(f.nodes[2]?.result?.functions[0]?.users).toBeNull();
    expect(f.nodes[2]?.result?.functions[1]?.users).toBe(0);
  });

  it("reads a node without functions, as Go encodes a nil slice", async () => {
    serve(200, JSON.stringify({ succeeded: 1, failed: 0, nodes: [{ node: "x", url: "http://x", result: { ...state.nodes[0]?.result, functions: null } }] }));
    expect((await fetchState({ kind: "role", role: "pcscf" })).nodes[0]?.result?.functions).toEqual([]);
  });

  it("drains a role with a timeout and the operator", async () => {
    const answer = { succeeded: 1, failed: 0, nodes: [{ node: "pcscf-1", url: "u", result: { role: "pcscf", administrative_state: "LOCKED", operational_state: "ENABLED", users: 0 } }] };
    const asked = serve(200, JSON.stringify(answer));
    const f = await putAdministrativeState({ kind: "role", role: "pcscf" }, "pcscf", "SHUTTINGDOWN", 300, "test-operator");
    expect(f.nodes[0]?.result?.administrative_state).toBe("LOCKED");
    expect(asked[0]).toEqual({
      url: "/oam/v1/roles/pcscf/administrative-state",
      method: "PUT",
      body: JSON.stringify({ administrative_state: "SHUTTINGDOWN", timeout_seconds: 300, operator: "test-operator" }),
    });
  });

  it("cancels the drain of one role of one node, without a timeout", async () => {
    const asked = serve(200, JSON.stringify({ succeeded: 0, failed: 0, nodes: [] }));
    await putAdministrativeState({ kind: "node", node: "cscf-1" }, "scscf", "UNLOCKED", 300, "");
    expect(asked[0]).toEqual({ url: "/oam/v1/nodes/cscf-1/functions/scscf/administrative-state", method: "PUT", body: '{"administrative_state":"UNLOCKED"}' });
  });

  it("sets the log level and reads the level before", async () => {
    const asked = serve(200, JSON.stringify({ succeeded: 1, failed: 0, nodes: [{ node: "pcscf-1", url: "u", result: { level: "DEBUG", previous: "INFO" } }] }));
    const f = await putLogLevel({ kind: "node", node: "pcscf-1" }, "DEBUG", "op");
    expect(f.nodes[0]?.result).toEqual({ level: "DEBUG", previous: "INFO" });
    expect(asked[0]?.url).toBe("/oam/v1/nodes/pcscf-1/log-level");
  });
});

describe("alarms", () => {
  it("reads the merged alarms, a failed node with why and the records with their node", async () => {
    const asked = serve(200, JSON.stringify(alarms));
    const a = await fetchAlarms({ cleared: null, min_severity: null });
    expect(asked[0]?.url).toBe("/oam/v1/alarms");
    expect([a.succeeded, a.failed, a.nodes.length, a.alarms.length]).toEqual([4, 1, 5, 5]);
    expect(a.nodes[4]).toMatchObject({ node: "hss-2", result: null, error: { status_code: null } });
    expect(a.nodes[2]?.result).toEqual({ instance: "cscf-1", num_of_alarm_records: 2, listed: 2, last_modification: "2026-11-20T09:01:00.099482434Z", dropped_records: 3 });
    expect(a.alarms.map((r) => [r.node, r.perceived_severity])).toEqual([
      ["pcscf-1", "CRITICAL"],
      ["cscf-1", "MAJOR"],
      ["cscf-1", "MAJOR"],
      ["pcscf-2", "WARNING"],
      ["hss-1", "CLEARED"],
    ]);
    // Go leaves out what is empty: no role for an alarm of the node, no
    // times that have not happened.
    expect(a.alarms[3]).toMatchObject({ role: "", peer: "dashboard:9060", alarm_changed_time: null, alarm_cleared_time: null, ack_time: null, ack_user_id: "" });
    expect(a.alarms[1]).toMatchObject({ ack_state: "ACKNOWLEDGED", ack_time: "2026-11-20T09:05:00Z", ack_user_id: "test-operator" });
    expect(a.alarms[4]).toMatchObject({ specific_problem: "store unreachable", additional_text: "", alarm_cleared_time: "2026-11-20T09:04:30Z" });
  });

  it("sends the filter as the query", async () => {
    const asked = serve(200, JSON.stringify({ succeeded: 0, failed: 0, nodes: [], alarms: null }));
    expect((await fetchAlarms({ cleared: false, min_severity: "MAJOR" })).alarms).toEqual([]);
    expect(asked[0]?.url).toBe("/oam/v1/alarms?cleared=false&min_severity=MAJOR");
  });

  it("refuses a severity that TS 28.111 does not have", async () => {
    const bad = structuredClone(alarms);
    bad.alarms[0]!.perceived_severity = "SEVERE";
    serve(200, JSON.stringify(bad));
    await expect(fetchAlarms({ cleared: null, min_severity: null })).rejects.toThrow("perceived_severity");
  });

  it("acknowledges an alarm of one node", async () => {
    const record = { ...alarms.alarms[2], node: undefined, ack_state: "ACKNOWLEDGED", ack_time: "2026-11-20T09:06:00Z", ack_user_id: "test-operator" };
    const asked = serve(200, JSON.stringify({ succeeded: 1, failed: 0, nodes: [{ node: "cscf-1", url: "http://cscf-1:8085", result: record }] }));
    const f = await putAckState("cscf-1", "a36a6dd01dd1f143", "ACKNOWLEDGED", "test-operator");
    expect(f.nodes[0]?.result).toMatchObject({ node: "cscf-1", alarm_id: "a36a6dd01dd1f143", ack_state: "ACKNOWLEDGED" });
    expect(asked[0]).toEqual({
      url: "/oam/v1/nodes/cscf-1/alarms/a36a6dd01dd1f143/ack-state",
      method: "PUT",
      body: JSON.stringify({ ack_state: "ACKNOWLEDGED", operator: "test-operator" }),
    });
  });

  it("reports the node's 404 for an alarm that left its list", async () => {
    serve(200, JSON.stringify({ succeeded: 0, failed: 1, nodes: [{ node: "hss-1", url: "u", error: { status_code: 404, error: 'no such alarm in the list: "599ee120c58fc287"' } }] }));
    const f = await putAckState("hss-1", "599ee120c58fc287", "ACKNOWLEDGED", "");
    expect(f.nodes[0]?.error).toEqual({ status_code: 404, error: 'no such alarm in the list: "599ee120c58fc287"', settings: {} });
  });
});

describe("errors", () => {
  it("reports the error body and its status", async () => {
    serve(404, JSON.stringify({ error: 'not found: no node "x" in the inventory' }) + "\n");
    const failure = fetchState({ kind: "node", node: "x" });
    await expect(failure).rejects.toEqual(new HttpError(404, 'not found: no node "x" in the inventory'));
  });

  it("keeps the settings of a rollback in conflict", async () => {
    serve(409, JSON.stringify(rollbackConflict));
    const error = await rollbackChange(changeRecord.id, { operator: "", comment: "", force: false }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(HttpError);
    expect((error as HttpError).status).toBe(409);
    expect((error as HttpError).settings).toEqual(rollbackConflict.settings);
  });

  it("takes a body that is not an ErrorBody as the message", async () => {
    serve(502, "Bad Gateway");
    await expect(fetchInventory()).rejects.toEqual(new HttpError(502, "Bad Gateway"));
  });

  it("refuses an answer of the wrong shape", async () => {
    serve(200, JSON.stringify({ succeeded: 1, failed: 0, nodes: [{ node: "a", result: { role: "pcscf", administrative_state: "DRAINING", operational_state: "ENABLED" } }] }));
    await expect(putAdministrativeState({ kind: "role", role: "pcscf" }, "pcscf", "SHUTTINGDOWN", 0, "")).rejects.toThrow(/administrative_state/);
  });
});

describe("configuration", () => {
  it("shows the configuration with its secrets as the node redacted them", async () => {
    serve(200, JSON.stringify({ succeeded: 1, failed: 0, nodes: [{ node: "hss-1", url: "u", result: hssConfiguration }] }));
    const conf = (await fetchConfiguration({ kind: "node", node: "hss-1" })).nodes[0]?.result;
    expect(conf?.configuration).toEqual(hssConfiguration.configuration);
    expect(JSON.stringify(conf?.configuration)).toContain('"k":"REDACTED"');
    expect(conf?.changeable.map((s) => s.name)).toEqual(["hss.admission", "hss.default_service_profile", "hss.test_user_service_profile"]);
  });

  it("sends a change without a generation, and reads each refused setting", async () => {
    const asked = serve(200, JSON.stringify(refusedChange));
    const f = await patchConfiguration({ kind: "role", role: "hss" }, { "hss.admission": "nobody" }, "op", "why");
    expect(asked[0]).toEqual({ url: "/oam/v1/roles/hss/config", method: "PATCH", body: '{"settings":{"hss.admission":"nobody"},"operator":"op","comment":"why"}' });
    expect(f.nodes[0]?.error).toEqual({
      status_code: 400,
      error: "1 of the settings refused, nothing changed: hss.admission",
      settings: { "hss.admission": 'admission: "nobody" is neither all nor test_users' },
    });
  });

  it("reads the history of a node, the newest first", async () => {
    const asked = serve(200, JSON.stringify({ changes: [rollbackRecord, changeRecord] }));
    const h = await fetchHistory("hss-1", 50);
    expect(asked[0]?.url).toBe("/oam/v1/nodes/hss-1/config/history?limit=50");
    expect(h.map((r) => [r.id, r.rollback_of, r.forced])).toEqual([
      [rollbackRecord.id, changeRecord.id, true],
      [changeRecord.id, "", false],
    ]);
    expect(h[1]?.change.changes).toEqual([{ name: "hss.test_user_service_profile", previous: "default", value: "trial" }]);
  });

  it("forces a rollback only when asked", async () => {
    const asked = serve(200, JSON.stringify(rollbackRecord));
    await rollbackChange(changeRecord.id, { operator: "op", comment: "", force: true });
    await rollbackChange(changeRecord.id, { operator: "", comment: "", force: false });
    expect(asked.map((a) => a.body)).toEqual(['{"operator":"op","force":true}', "{}"]);
    expect(asked[0]?.url).toBe(`/oam/v1/config/history/${changeRecord.id}/rollback`);
  });

  it("rolls back a change set node by node", async () => {
    const answer = {
      succeeded: 1,
      failed: 1,
      nodes: [
        { node: "hss-1", url: "u1", result: rollbackRecord },
        { node: "hss-2", url: "u2", error: { status_code: 409, error: rollbackConflict.error, settings: rollbackConflict.settings } },
      ],
    };
    const asked = serve(200, JSON.stringify(answer));
    const f = await rollbackChangeSet(changeRecord.change_set, { operator: "", comment: "", force: false });
    expect(asked[0]?.url).toBe(`/oam/v1/config/change-sets/${changeRecord.change_set}/rollback`);
    expect(f.nodes[1]?.error?.settings).toEqual(rollbackConflict.settings);
  });
});

describe("subscriptions", () => {
  it("escapes the private identity in the path", async () => {
    const asked = serve(200, JSON.stringify(subscription));
    const s = await fetchSubscription(impi1);
    expect(asked[0]?.url).toBe("/oam/v1/subscriptions/001010000000001%40ims.mnc001.mcc001.3gppnetwork.org");
    expect(s).toEqual({ ...subscription, server_name: "", service_profile: "" });
  });

  it("replaces a subscription on a dry run and reads the outcome", async () => {
    const asked = serve(200, JSON.stringify({ private_identity: impi1, result: "updated", dry_run: true }));
    const o = await putSubscription({ private_identity: impi1, public_identities: ["tel:+12025550101"], password: "test-password" }, true);
    expect(o).toEqual({ line: 0, private_identity: impi1, result: "updated", reason: "", dry_run: true });
    expect(asked[0]?.url).toMatch(/\?dry_run=true$/);
    expect(asked[0]?.method).toBe("PUT");
  });

  it("reports the HSS's refusal of a record", async () => {
    serve(409, JSON.stringify({ error: "public identity tel:+12025550101 belongs to another subscription" }));
    await expect(putSubscription({ private_identity: impi1, public_identities: ["tel:+12025550101"], password: "p" }, false)).rejects.toHaveProperty("status", 409);
  });

  it("deletes, marks a test user and asks for a feature", async () => {
    const asked = serve(200, JSON.stringify({ private_identity: impi1, result: "deleted" }));
    expect((await deleteSubscription(impi1, false)).result).toBe("deleted");
    serve(200, JSON.stringify(subscription));
    expect((await putTestUser(impi1, true, "op")).test_user).toBe(true);
    serve(200, JSON.stringify(featureState));
    expect(await fetchFeature(impi1, "mmtel")).toEqual({ ...featureState, reason: "" });
    expect(asked[0]?.method).toBe("DELETE");
  });

  it("reads the test users' profiles", async () => {
    serve(200, JSON.stringify(testUsers));
    expect(await fetchTestUsers()).toEqual(testUsers);
  });
});

describe("streams", () => {
  it("gives each line as it arrives, across chunks that cut lines and characters", async () => {
    const text = ndjson(provisioningLines);
    const cut = text.indexOf("rejected") + 3;
    // "é" is two octets in UTF-8: the chunks cut it in the middle.
    const accented = new TextEncoder().encode(JSON.stringify({ error: "arrêt" }) + "\n");
    const at = accented.indexOf(0xc3) + 1;
    const seen: StreamLine[] = [];
    const body = chunks([text.slice(0, cut), text.slice(cut), accented.slice(0, at), accented.slice(at)]);
    await readStream(new Response(body), (l) => seen.push(l));
    expect(seen.map((l) => l.kind)).toEqual(["outcome", "outcome", "outcome", "outcome", "summary", "error"]);
    expect(seen[5]).toEqual({ kind: "error", error: "arrêt" });
  });

  it("handles a line before the next one has arrived", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    const encoder = new TextEncoder();
    let sent = 0;
    const body = new ReadableStream<Uint8Array>({
      async pull(controller) {
        if (sent === 0) controller.enqueue(encoder.encode(ndjson(provisioningLines.slice(0, 1))));
        if (sent === 1) {
          await gate;
          controller.enqueue(encoder.encode(ndjson(provisioningLines.slice(1))));
        }
        if (sent === 2) controller.close();
        sent++;
      },
    });
    const seen: string[] = [];
    const done = readStream(new Response(body), (l) => {
      seen.push(l.kind);
      if (seen.length === 1) release();
    });
    await done;
    expect(seen).toEqual(["outcome", "outcome", "outcome", "outcome", "summary"]);
  });

  it("refuses a line that is none of the kinds", async () => {
    await expect(readStream(new Response('{"other": 1}\n'), () => {})).rejects.toThrow(/none of/);
    await expect(readStream(new Response("not json\n"), () => {})).rejects.toThrow(/not JSON/);
  });
});

describe("bulk provisioning", () => {
  it("cuts a source into batches of whole lines, the last without its newline too", async () => {
    const lines = Array.from({ length: 5 }, (_, i) => `{"n":${i}}`);
    const got = [];
    for await (const b of batches(chunks([lines.slice(0, 2).join("\n") + "\n{", lines.slice(2).join("\n").slice(1)]), 2, 1 << 20)) got.push(b);
    expect(got).toEqual([
      { text: `${lines[0]}\n${lines[1]}\n`, lines: 2 },
      { text: `${lines[2]}\n${lines[3]}\n`, lines: 2 },
      { text: `${lines[4]}\n`, lines: 1 },
    ]);
  });

  it("starts a new batch before one would pass the byte bound", async () => {
    const got = [];
    for await (const b of batches(chunks(["aaaa\nbbbb\ncccccccccccc\n"]), 100, 10)) got.push(b);
    expect(got.map((b) => b.text)).toEqual(["aaaa\nbbbb\n", "cccccccccccc\n"]);
  });

  it("sends the lines as they are, and counts the outcomes from the start of the file", async () => {
    // 250 lines go as a batch of 200 and one of 50; the blank line 201
    // counts as a line, as the OAM counts it.
    const lines = Array.from({ length: 250 }, (_, i) => (i === 200 ? "" : JSON.stringify({ private_identity: `00101000000${String(i).padStart(4, "0")}@${"x"}` })));
    const bodies: string[] = [];
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
      expect(url).toBe("/oam/v1/provisioning?dry_run=true");
      const body = String(init.body);
      bodies.push(body);
      const n = body.split("\n").length - 1;
      const answer = [];
      for (let line = 1; line <= n; line++) {
        if (body.split("\n")[line - 1] === "") continue;
        answer.push({ outcome: { line, result: line === 7 ? "rejected" : "created", reason: line === 7 ? "aka.k: want 16 octets in hexadecimal" : undefined, dry_run: true } });
      }
      const created = answer.filter((a) => a.outcome.result === "created").length;
      answer.push({ summary: { lines: n, created, rejected: answer.length - created, dry_run: true } });
      return new Response(ndjson(answer));
    });
    const outcomes: Outcome[] = [];
    const summary = await provision(chunks([lines.join("\n")]), true, (o) => outcomes.push(o));
    expect(bodies.map((b) => b.split("\n").length - 1)).toEqual([200, 50]);
    expect(bodies[0]?.startsWith(`${lines[0]}\n${lines[1]}\n`)).toBe(true);
    expect(outcomes.length).toBe(249);
    expect(outcomes.filter((o) => o.result === "rejected").map((o) => o.line)).toEqual([7, 207]);
    expect(outcomes.at(-1)?.line).toBe(250);
    expect(summary).toEqual({ lines: 250, created: 247, updated: 0, unchanged: 0, deleted: 0, rejected: 2, failed: 0, dry_run: true });
  });

  it("stops at an answer without its summary, or with an error line", async () => {
    serve(200, () => ndjson(provisioningLines.slice(0, 2)));
    await expect(provision(chunks(["{}\n"]), false, () => {})).rejects.toThrow(/before its summary/);
    serve(200, () => ndjson([{ error: "unexpected EOF" }, provisioningLines[4]]));
    await expect(provision(chunks(["{}\n"]), false, () => {})).rejects.toThrow(/unexpected EOF/);
  });

  it("sends nothing for an empty source", async () => {
    const asked = serve(200, "");
    expect((await provision(chunks([]), false, () => {})).lines).toBe(0);
    expect(asked).toEqual([]);
  });
});

describe("backups", () => {
  it("lists the backups with their steps", async () => {
    serve(200, JSON.stringify({ backups: [backup, failedBackup] }));
    const [ok, failed] = await fetchBackups();
    expect(ok).toMatchObject({ id: backup.id, state: "completed", records: { "hss.subscriptions": 2 }, bytes: 912, restores: [] });
    expect(failed?.steps[0]?.error).toMatch(/lacks this operation/);
    expect(failed?.operator).toBe("");
  });

  it("streams the steps of a backup, then its manifest", async () => {
    const lines = [
      { step: { name: "export", section: "hss.subscriptions", state: "running", started: "2026-11-20T09:00:00Z" } },
      { step: backup.steps[0] },
      { step: backup.steps[1] },
      { manifest: backup },
    ];
    const asked = serve(200, () => ndjson(lines));
    const kinds: string[] = [];
    await createBackup("op", "before the trial", (l) => kinds.push(l.kind));
    expect(kinds).toEqual(["step", "step", "step", "manifest"]);
    expect(asked[0]).toEqual({ url: "/oam/v1/backups", method: "POST", body: '{"operator":"op","comment":"before the trial"}' });
  });

  it("streams a dry run of a restore", async () => {
    const asked = serve(200, () => ndjson(restoreLines));
    const seen: StreamLine[] = [];
    await restoreBackup(backup.id, true, "", (l) => seen.push(l));
    expect(asked[0]?.url).toBe(`/oam/v1/backups/${backup.id}/restore?dry_run=true`);
    expect(seen.filter((l) => l.kind === "outcome").map((l) => l.kind === "outcome" && [l.outcome.line, l.outcome.result])).toEqual([
      [2, "unchanged"],
      [3, "created"],
    ]);
    expect(seen.at(-1)).toEqual({ kind: "summary", summary: { lines: 2, created: 1, updated: 0, unchanged: 1, deleted: 0, rejected: 0, failed: 0, dry_run: true } });
  });

  it("refuses to restore a backup that did not complete", async () => {
    serve(409, JSON.stringify({ error: `conflict: backup ${failedBackup.id} is failed, not completed` }));
    await expect(restoreBackup(failedBackup.id, false, "", () => {})).rejects.toHaveProperty("status", 409);
  });
});
