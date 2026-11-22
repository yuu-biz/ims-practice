import { describe, expect, it } from "vitest";
import { parseAlarms, parseChangeRecord, parseFanOut, parseManifest, parseNodeState, parseOutcome, parseStreamLine, type Endpoint, type Outcome } from "../src/api.ts";
import {
  ackAction,
  alarmFilter,
  describeAlarm,
  droppingNodes,
  severityCounts,
  unknownAlarmNodes,
  describeChanges,
  describeConflicts,
  describeRecords,
  describeStep,
  mergeStep,
  describeScope,
  describeTally,
  fanOutLines,
  formatTime,
  isChange,
  isProblem,
  maxListed,
  nodeRows,
  outcomeTally,
  recordFromFields,
  refusedSettings,
  roleSummaries,
  columnNotes,
  scopeNodes,
  settingEdits,
  type RecordFields,
} from "../src/view.ts";
import {
  alarms,
  backup,
  changeRecord,
  failedBackup,
  hssConfiguration,
  impi1,
  inventory,
  provisioningLines,
  refusedChange,
  restoreLines,
  rollbackConflict,
  state,
} from "./fixtures.ts";

const nodes: Endpoint[] = inventory.nodes;
const rows = nodeRows(nodes, parseFanOut(state, parseNodeState));

describe("node state", () => {
  it("keeps every node of the inventory, a failed one with why", () => {
    expect(rows.map((r) => [r.name, r.state === null, r.error])).toEqual([
      ["pcscf-1", false, null],
      ["pcscf-2", false, null],
      ["cscf-1", false, null],
      ["hss-1", false, null],
      ["hss-2", true, "no answer within 5s"],
    ]);
    // The roles of a failed node come from the inventory.
    expect(rows[4]?.roles).toEqual(["hss"]);
  });

  it("marks a node that the answer left out as failed", () => {
    const partial = parseFanOut({ succeeded: 0, failed: 0, nodes: [] }, parseNodeState);
    expect(nodeRows(nodes.slice(0, 1), partial)[0]?.error).toBe("not in the answer of the OAM");
  });

  it("sums each role over its nodes, counting a failed node apart", () => {
    const byRole = Object.fromEntries(roleSummaries(rows).map((s) => [s.role, s]));
    expect(byRole["pcscf"]).toEqual({
      role: "pcscf",
      nodes: ["pcscf-1", "pcscf-2"],
      failed: [],
      administrative: { UNLOCKED: 1, SHUTTINGDOWN: 1 },
      operational: { ENABLED: 2 },
      users: 5,
      generations: [3],
    });
    expect(byRole["hss"]).toMatchObject({ nodes: ["hss-1", "hss-2"], failed: ["hss-2"], users: null, generations: [5] });
    // A role that counts no users has none; one that counts zero has 0.
    expect([byRole["icscf"]?.users, byRole["scscf"]?.users]).toEqual([null, 0]);
    expect(Object.keys(byRole)).toEqual(["pcscf", "icscf", "scscf", "hss"]);
  });

  it("says what Users counts for each role, and what Generation is", () => {
    expect(columnNotes(roleSummaries(rows))).toEqual([
      {
        column: "Users",
        meaning:
          "What a role still serves on a node; a drain locks the role there once this is 0. The Roles table adds up its nodes. " +
          "pcscf: counted by the role. scscf: the SIP requests it is still serving. icscf, hss: not counted, so a drain locks them at once.",
      },
      {
        column: "Generation",
        meaning:
          'The configuration generation of the node, which each configuration change raises. The nodes of a role keep the same one; "(differ)" marks a node that missed a change.',
      },
    ]);
  });

  it("leaves out of the Users note a role that no node reported", () => {
    const users = columnNotes([
      { role: "scscf", nodes: ["scscf-1"], failed: ["scscf-1"], administrative: {}, operational: {}, users: null, generations: [] },
    ])[0];
    expect(users?.meaning).toBe("What a role still serves on a node; a drain locks the role there once this is 0. The Roles table adds up its nodes.");
  });

  it("shows the generations of a role that differ", () => {
    const answer = structuredClone(state);
    (answer.nodes[1] as { result: { configuration_generation: number } }).result.configuration_generation = 2;
    const byRole = roleSummaries(nodeRows(nodes, parseFanOut(answer, parseNodeState)));
    expect(byRole.find((s) => s.role === "pcscf")?.generations).toEqual([3, 2]);
  });
});

describe("scope", () => {
  it("names every node of a role in the confirmation", () => {
    expect(scopeNodes(nodes, { kind: "role", role: "hss" })).toEqual(["hss-1", "hss-2"]);
    expect(describeScope(nodes, { kind: "role", role: "pcscf" })).toBe("Every node of role pcscf: 2 nodes (pcscf-1, pcscf-2)");
    expect(describeScope(nodes, { kind: "role", role: "icscf" })).toBe("Every node of role icscf: 1 node (cscf-1)");
    expect(describeScope(nodes, { kind: "node", node: "cscf-1" })).toBe("Node cscf-1");
  });

  it("says what each node of a fan-out did, and why one failed", () => {
    const f = parseFanOut(
      {
        succeeded: 1,
        failed: 1,
        nodes: [
          { node: "a", url: "u", result: { level: "DEBUG", previous: "INFO" } },
          { node: "b", url: "u", error: { status_code: 404, error: "no such role on this node" } },
        ],
      },
      (v) => v as { level: string; previous: string },
    );
    expect(fanOutLines(f, (r) => `${r.previous} -> ${r.level}`)).toEqual([
      { node: "a", ok: true, text: "INFO -> DEBUG" },
      { node: "b", ok: false, text: "failed: 404: no such role on this node" },
    ]);
  });
});

describe("configuration", () => {
  const current = hssConfiguration.changeable;

  it("sends only the settings whose typed value differs, and refuses text that is not JSON", () => {
    const typed = { "hss.admission": '"test_users"', "hss.default_service_profile": ' "default" ', "hss.test_user_service_profile": "trial" };
    expect(settingEdits(current, typed)).toEqual({
      settings: { "hss.admission": "test_users" },
      errors: { "hss.test_user_service_profile": 'not JSON: a string needs its quotes, such as "all"' },
    });
    expect(settingEdits(current, { "hss.admission": "" })).toEqual({ settings: {}, errors: {} });
  });

  it("puts each node's refusal next to its setting", () => {
    const f = parseFanOut(refusedChange, parseChangeRecord);
    expect(refusedSettings(f)).toEqual({ "hss.admission": ['hss-1: admission: "nobody" is neither all nor test_users'] });
  });

  it("describes a change, and what its rollback would put back", () => {
    const r = parseChangeRecord(changeRecord);
    expect(describeChanges(r.change.changes)).toEqual(['hss.test_user_service_profile: "default" -> "trial"']);
    expect(describeChanges(r.change.changes, true)).toEqual(['hss.test_user_service_profile: "trial" -> "default"']);
    expect(describeChanges([])).toEqual(["no setting changed"]);
    expect(describeConflicts(rollbackConflict.settings)).toEqual(["hss.test_user_service_profile: changed again by 0a0b0c0d0e0f1011 (generation 6)"]);
  });
});

describe("outcomes", () => {
  const outcome = (line: number, result: Outcome["result"]): Outcome => ({ line, private_identity: "", result, reason: "", dry_run: false });

  it("counts every outcome and lists the problems only", () => {
    const t = outcomeTally(isProblem);
    for (const o of provisioningLines.flatMap((l) => ("outcome" in l ? [parseOutcome(l.outcome)] : []))) t.add(o);
    expect(t.counts).toEqual({ created: 1, updated: 0, unchanged: 1, deleted: 0, rejected: 2, failed: 0 });
    expect(t.listed.map((o) => o.line)).toEqual([4, 5]);
    expect(describeTally(t.counts)).toBe("created 1, unchanged 1, rejected 2");
    expect(describeTally(outcomeTally(isProblem).counts)).toBe("no records");
  });

  it("stops listing at the bound and counts the rest", () => {
    const t = outcomeTally(isChange);
    // Every other line would change something: maxListed + 3 of them.
    for (let i = 1; i <= 2 * (maxListed + 3); i++) t.add(outcome(i, i % 2 === 0 ? "unchanged" : "created"));
    expect(t.listed.length).toBe(maxListed);
    expect(t.unlisted).toBe(3);
    expect(t.counts.unchanged).toBe(maxListed + 3);
    expect(t.listed.every((o) => o.result === "created")).toBe(true);
  });
});

describe("the single-record form", () => {
  const empty: RecordFields = {
    private_identity: "",
    public_identities: "",
    tel: "",
    password: "",
    k: "",
    opc: "",
    amf: "",
    sqn: "",
    ut_password: "",
    mmtel_services: "",
    test_user: false,
    service_profile: "",
  };

  it("builds a record with only the fields typed", () => {
    const { record } = recordFromFields({ ...empty, private_identity: ` ${impi1} `, public_identities: "sip:a@x\n\n tel:+12025550101 \n", password: "test-password" });
    expect(record).toEqual({ private_identity: impi1, public_identities: ["sip:a@x", "tel:+12025550101"], password: "test-password" });
  });

  it("takes AKA credentials with the optional AMF, and the test user mark", () => {
    const { record } = recordFromFields({ ...empty, private_identity: impi1, public_identities: "tel:+12025550101", k: "00".repeat(16), opc: "11".repeat(16), amf: "8000", test_user: true });
    expect(record).toEqual({ private_identity: impi1, public_identities: ["tel:+12025550101"], aka: { k: "00".repeat(16), opc: "11".repeat(16), amf: "8000" }, test_user: true });
  });

  it("asks for the credentials again, as the HSS shows none", () => {
    expect(recordFromFields(empty).errors).toMatchObject({ private_identity: "required", public_identities: "at least one", password: expect.stringMatching(/typed again/) });
    expect(recordFromFields({ ...empty, private_identity: impi1, public_identities: "tel:+12025550101", k: "00" }).errors).toEqual({ k: "AKA needs both K and OPc" });
  });
});

describe("backup steps", () => {
  const lines = restoreLines.flatMap((l) => ("step" in l ? [parseStreamLine(l)] : []));
  const steps = lines.flatMap((l) => (l.kind === "step" ? [l.step] : []));

  it("keeps the latest report of each step, in the order they started", () => {
    const merged = steps.reduce(mergeStep, []);
    expect(merged.map((s) => [s.name, s.state, s.records])).toEqual([
      ["verify", "completed", 2],
      ["diff", "completed", 2],
    ]);
  });

  it("describes a step with its section, records and error", () => {
    expect(describeStep(parseManifest(backup).steps[0]!)).toBe("export hss.subscriptions: completed, 2 records");
    expect(describeStep(steps[0]!)).toBe("verify: running");
    expect(describeStep(parseManifest(failedBackup).steps[0]!)).toMatch(/^export hss.subscriptions: failed: upstream failed/);
    expect(describeRecords({ "hss.subscriptions": 2 })).toBe("hss.subscriptions 2");
  });
});

describe("times", () => {
  it("shows a time in UTC to the second", () => {
    expect(formatTime("2026-11-20T09:00:00.123456789Z")).toBe("2026-11-20 09:00:00 UTC");
    expect(formatTime("2026-11-20T18:00:00+09:00")).toBe("2026-11-20 09:00:00 UTC");
    expect(formatTime("soon")).toBe("soon");
  });
});

describe("alarms", () => {
  const a = parseAlarms(alarms);

  it("counts the records of each severity, in the order of TS 28.111", () => {
    expect(severityCounts(a.alarms)).toEqual([
      { severity: "CRITICAL", count: 1 },
      { severity: "MAJOR", count: 2 },
      { severity: "WARNING", count: 1 },
      { severity: "CLEARED", count: 1 },
    ]);
    expect(severityCounts([])).toEqual([]);
  });

  it("names the nodes whose alarms are unknown, and those whose list dropped records", () => {
    expect(unknownAlarmNodes(a)).toEqual([{ node: "hss-2", why: 'Get "http://hss-2:8085/maintenance/v1/alarms": dial tcp 10.0.0.12:8085: connect: connection refused' }]);
    expect(droppingNodes(a)).toEqual([{ node: "cscf-1", dropped: 3 }]);
  });

  it("builds the filter of the view and the minimum severity", () => {
    expect(alarmFilter("all", "")).toEqual({ cleared: null, min_severity: null });
    expect(alarmFilter("not-cleared", "MAJOR")).toEqual({ cleared: false, min_severity: "MAJOR" });
    expect(alarmFilter("cleared", "CLEARED")).toEqual({ cleared: true, min_severity: null });
  });

  it("says what an acknowledgement does", () => {
    const [critical, acknowledged, , , cleared] = a.alarms;
    expect(ackAction(critical!)).toMatchObject({ to: "ACKNOWLEDGED", label: "Acknowledge" });
    expect(ackAction(critical!).effect).toContain("leaves it when its condition clears");
    expect(ackAction(cleared!).effect).toContain("it leaves the alarm list");
    expect(ackAction(acknowledged!)).toMatchObject({ to: "UNACKNOWLEDGED", label: "Unacknowledge" });
    expect(describeAlarm(acknowledged!)).toBe("MAJOR COMMUNICATIONS_ALARM COMMUNICATIONS_SUBSYSTEM_FAILURE: Diameter peer down");
  });
});
