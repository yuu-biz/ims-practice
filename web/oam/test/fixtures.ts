// Answers of the OAM API as internal/oam encodes them: Go's field names,
// omitempty fields left out, nil slices as null, times in RFC 3339 with
// nanoseconds. The tests parse them, and the mock API (mock/oam.ts)
// starts from them. Identities and numbers are test data:
// the test PLMN 001/01 and fictional numbers of 555-0100 to 555-0199.

export const realm = "ims.mnc001.mcc001.3gppnetwork.org";
export const impi1 = `001010000000001@${realm}`;
export const impi2 = `001010000000002@${realm}`;

export const inventory = {
  nodes: [
    { name: "pcscf-1", roles: ["pcscf"], url: "http://pcscf-1:8085" },
    { name: "pcscf-2", roles: ["pcscf"], url: "http://pcscf-2:8085" },
    { name: "cscf-1", roles: ["icscf", "scscf"], url: "http://cscf-1:8085" },
    { name: "hss-1", roles: ["hss"], url: "http://hss-1:8085" },
    { name: "hss-2", roles: ["hss"], url: "http://hss-2:8085" },
  ],
};

const build = { version: "(devel)", revision: "41a2b91", go_version: "go1.27.4" };

export function nodeState<F>(node: string, instance: string, functions: F[], generation = 3, logLevel = "INFO") {
  return {
    node,
    instance,
    build,
    start_time: "2026-11-20T09:00:00.123456789Z",
    configuration_generation: generation,
    log_level: logLevel,
    functions,
  };
}

// GET /oam/v1/state: hss-2 did not answer, and pcscf-2 drains.
export const state = {
  succeeded: 4,
  failed: 1,
  nodes: [
    {
      node: "pcscf-1",
      url: "http://pcscf-1:8085",
      result: nodeState("pcscf", "pcscf-1", [
        { role: "pcscf", administrative_state: "UNLOCKED", operational_state: "ENABLED", users: 3 },
      ]),
    },
    {
      node: "pcscf-2",
      url: "http://pcscf-2:8085",
      result: nodeState("pcscf", "pcscf-2", [
        {
          role: "pcscf",
          administrative_state: "SHUTTINGDOWN",
          operational_state: "ENABLED",
          users: 2,
          shutdown_deadline: "2026-11-20T09:15:00Z",
        },
      ]),
    },
    {
      node: "cscf-1",
      url: "http://cscf-1:8085",
      result: nodeState(
        "cscf",
        "cscf-1",
        [
          { role: "icscf", administrative_state: "UNLOCKED", operational_state: "ENABLED" },
          { role: "scscf", administrative_state: "UNLOCKED", operational_state: "DISABLED", users: 0 },
        ],
        3,
        "DEBUG",
      ),
    },
    {
      node: "hss-1",
      url: "http://hss-1:8085",
      result: nodeState("hss", "hss-1", [{ role: "hss", administrative_state: "UNLOCKED", operational_state: "ENABLED", users: null }], 5),
    },
    { node: "hss-2", url: "http://hss-2:8085", error: { error: "no answer within 5s" } },
  ],
};

// The configuration view of an HSS node: the node redacts each secret,
// as maintenance.Secret marshals it.
export const hssConfiguration = {
  generation: 5,
  configuration: {
    node: "hss",
    diameter: { listen: "0.0.0.0:3868", origin_host: `hss.${realm}`, origin_realm: realm },
    hss: {
      provisioning: { listen: "0.0.0.0:8081" },
      subscriptions: [
        { private_identity: impi1, public_identities: [`sip:+12025550101@${realm}`, "tel:+12025550101"], password: "REDACTED", aka: null },
        { private_identity: impi2, public_identities: [`sip:+12025550102@${realm}`], password: null, aka: { k: "REDACTED", opc: "REDACTED", amf: "8000" } },
      ],
      service_profiles: { default: { features: ["sms"] }, trial: { features: ["sms", "mmtel"] } },
    },
  },
  changeable: [
    { name: "hss.admission", description: "which users the HSS lets register: all or test_users", value: "all" },
    { name: "hss.default_service_profile", description: "the service profile of a subscription that names none", value: "default" },
    { name: "hss.test_user_service_profile", description: "the service profile of a test user's subscription that names none", value: "trial" },
  ],
};

export const pcscfConfiguration = {
  generation: 3,
  configuration: { node: "pcscf", sip: { listen: "0.0.0.0:5060", domain: realm }, pcscf: { ipsec: false } },
  changeable: [],
};

export const changeRecord = {
  id: "5f2a9c01d4e8b7a3",
  change_set: "c0ffee0011223344",
  node: "hss-1",
  recorded_at: "2026-11-20T09:05:00.5Z",
  change: {
    node: "hss",
    instance: "hss-1",
    generation: 5,
    time: "2026-11-20T09:05:00.4Z",
    operator: "test-operator",
    comment: "trial for test users",
    changes: [{ name: "hss.test_user_service_profile", previous: "default", value: "trial" }],
  },
};

export const rollbackRecord = {
  ...changeRecord,
  id: "77aa00bb11cc22dd",
  change_set: "9988776655443322",
  recorded_at: "2026-11-20T09:06:00Z",
  rollback_of: changeRecord.id,
  forced: true,
  change: {
    ...changeRecord.change,
    generation: 6,
    time: "2026-11-20T09:06:00Z",
    comment: "rollback of 5f2a9c01d4e8b7a3",
    changes: [{ name: "hss.test_user_service_profile", previous: "trial", value: "default" }],
  },
};

// A rollback in conflict (409): ErrorBody with settings.
export const rollbackConflict = {
  error: "settings of change 5f2a9c01d4e8b7a3 changed since; send force to roll back anyway",
  settings: { "hss.test_user_service_profile": "changed again by 0a0b0c0d0e0f1011 (generation 6)" },
};

// A change that one node refused: the node's 400 with settings.
export const refusedChange = {
  succeeded: 0,
  failed: 1,
  nodes: [
    {
      node: "hss-1",
      url: "http://hss-1:8085",
      error: {
        status_code: 400,
        error: "1 of the settings refused, nothing changed: hss.admission",
        settings: { "hss.admission": 'admission: "nobody" is neither all nor test_users' },
      },
    },
  ],
};

export const subscription = {
  private_identity: impi1,
  public_identities: [`sip:+12025550101@${realm}`, "tel:+12025550101"],
  authentication_schemes: ["SIP Digest"],
  registration_state: "NOT_REGISTERED",
  test_user: true,
  // Provisioned and off: the callers hear their own ringing tone until
  // the operator turns this tone on.
  customized_alerting_tone: { tone: "cat1", active: false },
};

// The answer to a bulk provisioning: an outcome per record in the order
// of the lines, then the summary. Line 2 was blank.
export const provisioningLines = [
  { outcome: { line: 1, private_identity: impi1, result: "created" } },
  { outcome: { line: 3, private_identity: impi2, result: "unchanged" } },
  { outcome: { line: 4, result: "rejected", reason: "not a JSON object: syntax error at offset 12" } },
  {
    outcome: {
      line: 5,
      private_identity: `001019999999999@${realm}`,
      result: "rejected",
      reason: "public_identities[0]: test data: not a fictional +1 NXX 555 01XX number or a test IMSI",
    },
  },
  { summary: { lines: 5, created: 1, updated: 0, unchanged: 1, deleted: 0, rejected: 2, failed: 0 } },
];

export const backup = {
  id: "20261120T090000Z-a1b2c3",
  created: "2026-11-20T09:00:00Z",
  operator: "test-operator",
  comment: "before the trial",
  state: "completed",
  sections: ["hss.subscriptions"],
  steps: [
    { name: "export", section: "hss.subscriptions", state: "completed", started: "2026-11-20T09:00:00Z", finished: "2026-11-20T09:00:01Z", records: 2 },
    { name: "store", state: "completed", started: "2026-11-20T09:00:01Z", finished: "2026-11-20T09:00:01Z" },
  ],
  records: { "hss.subscriptions": 2 },
  bytes: 912,
};

export const failedBackup = {
  id: "20261120T085500Z-d4e5f6",
  created: "2026-11-20T08:55:00Z",
  state: "failed",
  sections: ["hss.subscriptions"],
  steps: [
    {
      name: "export",
      section: "hss.subscriptions",
      state: "failed",
      started: "2026-11-20T08:55:00Z",
      finished: "2026-11-20T08:55:05Z",
      error: "upstream failed: export: unexpected EOF",
    },
  ],
  records: { "hss.subscriptions": 0 },
};

// The answer to a dry run of a restore: the steps, an outcome per record
// with its line in the archive, then the summary.
export const restoreLines = [
  { step: { name: "verify", state: "running", started: "2026-11-20T09:10:00Z" } },
  { step: { name: "verify", state: "completed", started: "2026-11-20T09:10:00Z", finished: "2026-11-20T09:10:00.1Z", records: 2 } },
  { step: { name: "diff", state: "running", started: "2026-11-20T09:10:00.1Z" } },
  { outcome: { line: 2, private_identity: impi1, result: "unchanged", dry_run: true } },
  { outcome: { line: 3, private_identity: impi2, result: "created", dry_run: true } },
  { step: { name: "diff", state: "completed", started: "2026-11-20T09:10:00.1Z", finished: "2026-11-20T09:10:00.3Z", records: 2 } },
  { summary: { lines: 2, created: 1, updated: 0, unchanged: 1, deleted: 0, rejected: 0, failed: 0, dry_run: true } },
];

export const testUsers = {
  hss_node: "hss-1",
  admission: "all",
  default_service_profile: "default",
  test_user_service_profile: "trial",
  service_profiles: { default: ["sms"], trial: ["sms", "mmtel"] },
};

export const featureState = {
  private_identity: impi1,
  feature: "mmtel",
  active: true,
  test_user: true,
  service_profile: "trial",
  profile_source: "test_users",
  admission: "all",
  admitted: true,
  hss_node: "hss-1",
};

// GET /oam/v1/alarms, as the real OAM answered with real maintenance
// nodes: hss-2 did not answer, the records of the others are merged,
// the most severe first, then the newest. The store alarm of hss-1 is
// cleared and waits for an acknowledgement; the HEP alarm of pcscf-2 is
// of the node, with no role.
export const alarms = {
  succeeded: 4,
  failed: 1,
  nodes: [
    { node: "pcscf-1", url: "http://pcscf-1:8085", result: { instance: "pcscf-1", num_of_alarm_records: 1, listed: 1, last_modification: "2026-11-20T09:02:00.110060709Z", dropped_records: 0 } },
    { node: "pcscf-2", url: "http://pcscf-2:8085", result: { instance: "pcscf-2", num_of_alarm_records: 1, listed: 1, last_modification: "2026-11-20T09:03:00.11013465Z", dropped_records: 0 } },
    { node: "cscf-1", url: "http://cscf-1:8085", result: { instance: "cscf-1", num_of_alarm_records: 2, listed: 2, last_modification: "2026-11-20T09:01:00.099482434Z", dropped_records: 3 } },
    { node: "hss-1", url: "http://hss-1:8085", result: { instance: "hss-1", num_of_alarm_records: 1, listed: 1, last_modification: "2026-11-20T09:04:30Z", dropped_records: 0 } },
    { node: "hss-2", url: "http://hss-2:8085", error: { error: 'Get "http://hss-2:8085/maintenance/v1/alarms": dial tcp 10.0.0.12:8085: connect: connection refused' } },
  ],
  alarms: [
    {
      node: "pcscf-1",
      alarm_id: "2139b1033e3c088f",
      object_instance: "ManagedElement=pcscf,ManagedFunction=pcscf,Peer=agw-1:8080",
      role: "pcscf",
      peer: "agw-1:8080",
      alarm_raised_time: "2026-11-20T09:02:00.110060709Z",
      alarm_type: "COMMUNICATIONS_ALARM",
      probable_cause: "CONNECTION_ESTABLISHMENT_ERROR",
      specific_problem: "media relay unreachable",
      perceived_severity: "CRITICAL",
      additional_text: "POST /contexts: connection refused",
      ack_state: "UNACKNOWLEDGED",
    },
    {
      node: "cscf-1",
      alarm_id: "73882d6c8bc4723e",
      object_instance: `ManagedElement=cscf,ManagedFunction=icscf,Peer=hss-2.${realm}`,
      role: "icscf",
      peer: `hss-2.${realm}`,
      alarm_raised_time: "2026-11-20T09:01:00.099482434Z",
      alarm_type: "COMMUNICATIONS_ALARM",
      probable_cause: "COMMUNICATIONS_SUBSYSTEM_FAILURE",
      specific_problem: "Diameter peer down",
      perceived_severity: "MAJOR",
      additional_text: "no answer to 3 DWR in 90 s",
      ack_state: "ACKNOWLEDGED",
      ack_time: "2026-11-20T09:05:00Z",
      ack_user_id: "test-operator",
      ack_system_id: "oam",
    },
    {
      node: "cscf-1",
      alarm_id: "a36a6dd01dd1f143",
      object_instance: `ManagedElement=cscf,ManagedFunction=scscf,Peer=hss-2.${realm}`,
      role: "scscf",
      peer: `hss-2.${realm}`,
      alarm_raised_time: "2026-11-20T09:00:00.088132665Z",
      alarm_type: "COMMUNICATIONS_ALARM",
      probable_cause: "COMMUNICATIONS_SUBSYSTEM_FAILURE",
      specific_problem: "Diameter peer down",
      perceived_severity: "MAJOR",
      additional_text: "no answer to 3 DWR in 90 s",
      ack_state: "UNACKNOWLEDGED",
    },
    {
      node: "pcscf-2",
      alarm_id: "8a828216f7c59785",
      object_instance: "ManagedElement=pcscf,Peer=lab:9060",
      peer: "lab:9060",
      alarm_raised_time: "2026-11-20T09:03:00.11013465Z",
      alarm_type: "QUALITY_OF_SERVICE_ALARM",
      probable_cause: "REDUCED_EVENT_REPORTING",
      specific_problem: "HEP copies dropped",
      perceived_severity: "WARNING",
      additional_text: "120 copies dropped in 60 s",
      ack_state: "UNACKNOWLEDGED",
    },
    {
      node: "hss-1",
      alarm_id: "599ee120c58fc287",
      object_instance: "ManagedElement=hss,ManagedFunction=hss,Peer=10.0.0.7:6379",
      role: "hss",
      peer: "10.0.0.7:6379",
      alarm_raised_time: "2026-11-20T09:04:00Z",
      alarm_changed_time: "2026-11-20T09:04:30Z",
      alarm_cleared_time: "2026-11-20T09:04:30Z",
      alarm_type: "PROCESSING_ERROR_ALARM",
      probable_cause: "UNDERLYING_RESOURCE_UNAVAILABLE",
      specific_problem: "store unreachable",
      perceived_severity: "CLEARED",
      ack_state: "UNACKNOWLEDGED",
    },
  ],
};

export function ndjson(lines: readonly unknown[]): string {
  return lines.map((l) => JSON.stringify(l) + "\n").join("");
}
