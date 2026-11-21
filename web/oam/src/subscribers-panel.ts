// The subscriber panel: bulk provisioning from a file of
// subscription records, one subscription at a time, and the test users.
//
// The records carry credentials (password, ut_password, aka.k, aka.opc).
// The screen never shows a line of the file, never logs a record, and
// types each credential into a password field that it empties once the
// record is sent. What it shows of a record comes from the OAM, which
// never puts a credential in an answer.

import {
  admissions,
  deleteSubscription,
  fetchFeature,
  fetchSubscription,
  fetchTestUsers,
  provision,
  putAdmission,
  putSubscription,
  putTestUser,
  putTestUserServiceProfile,
  type Admission,
  type ChangeRecord,
  type FanOut,
  type Subscription,
  type Summary,
  type TestUsers,
} from "./api.ts";
import { confirmAction, describe, el, fillSelect, showNote } from "./dom.ts";
import { outcomeView } from "./outcome-view.ts";
import { describeFanOutTotals, describeTally, fanOutLines, isProblem, outcomeTally, recordFromFields, type RecordFields } from "./view.ts";

export function mountSubscribersPanel(root: HTMLElement, operator: () => string): void {
  const testUsers = testUsersPart(operator);
  root.append(bulkPart(), singlePart(operator, testUsers.features), testUsers.root);
  void testUsers.refresh();
}

function bulkPart(): HTMLElement {
  const fromFile = el("input", { type: "radio", name: "bulk-source", value: "file", checked: true });
  const fromText = el("input", { type: "radio", name: "bulk-source", value: "text" });
  const file = el("input", { type: "file", name: "bulk-file", accept: ".ndjson,.jsonl,.json,.txt,application/x-ndjson" });
  file.setAttribute("aria-label", "File of subscription records");
  const text = el("textarea", { name: "bulk-text", rows: 5, spellcheck: false, autocomplete: "off" });
  text.setAttribute("aria-label", "Subscription records, one JSON object per line");
  text.placeholder = '{"private_identity": "001010000000005@ims.mnc001.mcc001.3gppnetwork.org", "public_identities": ["tel:+12025550105"], "password": "…"}';
  const dry = el("button", { type: "button", textContent: "Dry run" });
  const apply = el("button", { type: "button", className: "danger", textContent: "Apply…", disabled: true });
  const note = el("p", { className: "note", hidden: true });
  const view = outcomeView("Rejected and failed records", "No record was rejected or failed.");
  // The dry run that Apply stands on; any change of the input drops it.
  let dryRun: Summary | null = null;
  let running = false;

  const sync = () => {
    file.disabled = !fromFile.checked;
    text.disabled = !fromText.checked;
    dry.disabled = running;
    apply.disabled = running || dryRun === null;
  };
  const changed = () => {
    dryRun = null;
    sync();
  };
  for (const c of [fromFile, fromText, file, text]) c.addEventListener("change", changed);
  text.addEventListener("input", changed);
  sync();

  // The file is read a chunk at a time; it is never held whole.
  const source = (): { stream: ReadableStream<Uint8Array>; name: string } | null => {
    if (fromFile.checked) {
      const f = file.files?.[0];
      return f === undefined ? null : { stream: f.stream(), name: `file ${f.name} (${f.size} bytes)` };
    }
    return text.value.trim() === "" ? null : { stream: new Blob([text.value]).stream(), name: "the pasted lines" };
  };

  async function run(isDryRun: boolean): Promise<void> {
    const s = source();
    if (s === null) {
      showNote(note, fromFile.checked ? "Choose a file first." : "Paste the records first, one per line.");
      return;
    }
    if (!isDryRun) {
      const d = dryRun;
      const lines = [
        `Every record of ${s.name} goes to the HSS: ${d?.lines ?? 0} lines`,
        `The dry run found: ${d === null ? "" : describeTally(d)}`,
        "Rejected lines change nothing; the others create, replace or delete their subscription",
      ];
      if (!(await confirmAction("Apply the bulk provisioning?", lines, "Apply"))) return;
    }
    showNote(note, null);
    running = true;
    sync();
    const tally = outcomeTally(isProblem);
    const verb = isDryRun ? "Dry run" : "Apply";
    let last = 0;
    try {
      const summary = await provision(s.stream, isDryRun, (o) => {
        tally.add(o);
        last = o.line;
        view.update(tally, `${verb}: up to line ${last}…`);
      });
      view.update(tally, `${verb} done: ${summary.lines} lines, ${describeTally(tally.counts)}.${isDryRun ? " Nothing was changed." : ""}`);
      dryRun = isDryRun && summary.lines > 0 ? summary : null;
      if (summary.lines === 0) showNote(note, "The input has no lines.");
    } catch (error) {
      dryRun = null;
      view.update(tally, `${verb} stopped after line ${last}.`);
      showNote(note, `The provisioning stopped: ${describe(error)}`);
    } finally {
      running = false;
      sync();
    }
  }
  dry.addEventListener("click", () => void run(true));
  apply.addEventListener("click", () => void run(false));

  return el(
    "div",
    {},
    el("h3", { textContent: "Bulk provisioning" }),
    el("p", {
      className: "note",
      textContent:
        "One subscription record per line (JSON Lines), as the OAM takes them. A line with \"delete\": true removes its subscription. Run a dry run first: Apply sends the same input once the dry run has gone through. The screen shows no line of the input; the OAM reports each line by its number.",
    }),
    el(
      "fieldset",
      { className: "controls" },
      el("legend", { textContent: "Records" }),
      el("label", { className: "check" }, fromFile, "From a file", file),
      el("label", { className: "check" }, fromText, "Pasted"),
      el("label", { className: "wide" }, text),
    ),
    el("div", { className: "controls" }, dry, apply),
    note,
    view.root,
  );
}

function singlePart(operator: () => string, features: () => string[]): HTMLElement {
  const impi = el("input", { name: "lookup-impi", placeholder: "001010000000001@ims.mnc001.mcc001.3gppnetwork.org", spellcheck: false });
  const lookup = el("button", { type: "submit", textContent: "Look up" });
  const note = el("p", { className: "note", hidden: true });
  const shown = el("div", {});
  const edit = recordForm((id) => void show(id));

  async function show(id: string): Promise<void> {
    try {
      const s = await fetchSubscription(id);
      showNote(note, null);
      shown.replaceChildren(subscriptionView(s, operator, features, (next) => void show(next.private_identity), () => shown.replaceChildren()));
      edit.fill(s);
    } catch (error) {
      shown.replaceChildren();
      showNote(note, `Could not look up ${id}: ${describe(error)}`);
    }
  }

  const form = el("form", { className: "controls" }, el("label", { className: "wide" }, "Private identity", impi), lookup);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (impi.value.trim() !== "") void show(impi.value.trim());
  });
  return el("div", {}, el("h3", { textContent: "One subscription" }), form, note, shown, edit.root);
}

function subscriptionView(s: Subscription, operator: () => string, features: () => string[], reshow: (s: Subscription) => void, gone: () => void): HTMLElement {
  const note = el("p", { className: "note", hidden: true });
  const mark = el("button", { type: "button", textContent: s.test_user ? "Unmark as test user…" : "Mark as test user…" });
  mark.addEventListener("click", () => {
    void (async () => {
      const to = !s.test_user;
      const lines = [`Subscription ${s.private_identity}`, to ? "Becomes a test user: it gets the test users' service profile unless it names one" : "No longer a test user"];
      if (!(await confirmAction(to ? "Mark as a test user?" : "Unmark as a test user?", lines, to ? "Mark" : "Unmark"))) return;
      try {
        reshow(await putTestUser(s.private_identity, to, operator()));
      } catch (error) {
        showNote(note, `Could not change the mark: ${describe(error)}`);
      }
    })();
  });
  const remove = el("button", { type: "button", className: "danger", textContent: "Delete…" });
  remove.addEventListener("click", () => {
    void (async () => {
      if (!(await confirmAction("Delete the subscription?", [`Subscription ${s.private_identity} and its credentials, from the HSS`], "Delete"))) return;
      try {
        const o = await deleteSubscription(s.private_identity, false);
        gone();
        showNote(note, `${o.private_identity}: ${o.result}.`, false);
      } catch (error) {
        showNote(note, `Could not delete: ${describe(error)}`);
      }
    })();
  });
  const feature = el("input", { name: "feature", placeholder: "mmtel", spellcheck: false });
  feature.setAttribute("list", "features");
  const list = el("datalist", { id: "features" }, ...features().map((f) => el("option", { value: f })));
  const ask = el("button", { type: "submit", textContent: "Is it active?" });
  const answer = el("p", { className: "note" });
  const featureForm = el("form", { className: "controls" }, el("label", {}, "Feature", feature, list), ask);
  featureForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void (async () => {
      try {
        const f = await fetchFeature(s.private_identity, feature.value.trim());
        answer.className = f.active ? "note result-created" : "note result-rejected";
        answer.textContent = `${f.feature} is ${f.active ? "active" : "not active"}${f.reason === "" ? "" : `: ${f.reason}`}. Service profile ${f.service_profile} (from ${f.profile_source}), admission ${f.admission}${f.admitted ? "" : ", not admitted"}; HSS node ${f.hss_node}.`;
      } catch (error) {
        answer.className = "note error";
        answer.textContent = `Could not ask: ${describe(error)}`;
      }
    })();
  });
  const fact = (k: string, v: string) => [el("dt", { textContent: k }), el("dd", { textContent: v })];
  return el(
    "div",
    {},
    el(
      "dl",
      { className: "facts" },
      ...fact("Private identity", s.private_identity),
      ...fact("Public identities", s.public_identities.join("\n")),
      ...fact("Authentication", s.authentication_schemes.join(", ")),
      ...fact("Registration", s.registration_state + (s.server_name === "" ? "" : ` at ${s.server_name}`)),
      ...fact("Test user", s.test_user ? "yes" : "no"),
      ...fact("Service profile", s.service_profile === "" ? "chosen by the HSS" : s.service_profile),
    ),
    el("div", { className: "controls" }, mark, remove),
    note,
    featureForm,
    answer,
  );
}

// The form of PUT /oam/v1/subscriptions/{impi}, which creates or
// replaces the whole subscription. The HSS shows no credential, so the
// form never has one to fill in: each is typed again.
function recordForm(saved: (impi: string) => void): { root: HTMLElement; fill(s: Subscription): void } {
  const text = (name: keyof RecordFields, label: string, props: Partial<HTMLInputElement> = {}) => {
    const input = el("input", { name, spellcheck: false, ...props });
    return { input, label: el("label", {}, label, input) };
  };
  const secret = (name: keyof RecordFields, label: string) => text(name, label, { type: "password", autocomplete: "new-password" });
  const privateIdentity = text("private_identity", "Private identity");
  const publics = el("textarea", { name: "public_identities", rows: 2, spellcheck: false });
  const tel = text("tel", "tel URI (optional)");
  const password = secret("password", "SIP Digest password");
  const k = secret("k", "AKA K (hex)");
  const opc = secret("opc", "AKA OPc (hex)");
  const amf = text("amf", "AMF (hex, optional)");
  const sqn = text("sqn", "SQN (hex, new only)");
  const ut = secret("ut_password", "Ut password (optional)");
  const mmtel = el("textarea", { name: "mmtel_services", rows: 2, spellcheck: false });
  const testUser = el("input", { type: "checkbox", name: "test_user" });
  const profile = text("service_profile", "Service profile (optional)");
  const errors = el("ul", { className: "field-error" });
  const note = el("p", { className: "note", hidden: true });
  const dry = el("button", { type: "button", textContent: "Dry run" });
  const save = el("button", { type: "submit", className: "danger", textContent: "Save…" });
  const secrets = [password, k, opc, ut].map((s) => s.input);

  const fields = (): RecordFields => ({
    private_identity: privateIdentity.input.value,
    public_identities: publics.value,
    tel: tel.input.value,
    password: password.input.value,
    k: k.input.value,
    opc: opc.input.value,
    amf: amf.input.value.trim(),
    sqn: sqn.input.value.trim(),
    ut_password: ut.input.value,
    mmtel_services: mmtel.value,
    test_user: testUser.checked,
    service_profile: profile.input.value,
  });

  async function send(dryRun: boolean): Promise<void> {
    const { record, errors: wrong } = recordFromFields(fields());
    errors.replaceChildren(...Object.entries(wrong).map(([field, why]) => el("li", { textContent: `${field}: ${why}` })));
    if (record === null) return;
    if (!dryRun) {
      const lines = [
        `Subscription ${record.private_identity} in the HSS, created or replaced whole`,
        `Public identities: ${record.public_identities.join(", ")}`,
        "The credentials typed here replace the stored ones",
      ];
      if (!(await confirmAction("Save the subscription?", lines, "Save"))) return;
    }
    try {
      const o = await putSubscription(record, dryRun);
      showNote(note, `${o.private_identity}: ${dryRun ? "would be " : ""}${o.result}${o.reason === "" ? "" : `: ${o.reason}`}.`, false);
      if (!dryRun) {
        for (const s of secrets) s.value = "";
        saved(o.private_identity);
      }
    } catch (error) {
      showNote(note, `${dryRun ? "Dry run" : "Save"} refused: ${describe(error)}`);
    }
  }

  const form = el(
    "form",
    {},
    el("h3", { textContent: "Create or replace a subscription" }),
    el("p", {
      className: "note",
      textContent: "The whole subscription is replaced. The HSS never shows a credential, so a password or the AKA K and OPc must be typed again. Numbers must be test data: a test IMSI of PLMN 001/01 or a fictional +1 NXX 555 01XX number.",
    }),
    el(
      "div",
      { className: "controls" },
      privateIdentity.label,
      el("label", { className: "wide" }, "Public identities, one per line; the first is the default", publics),
      tel.label,
      password.label,
      k.label,
      opc.label,
      amf.label,
      sqn.label,
      ut.label,
      profile.label,
      el("label", { className: "check" }, testUser, "Test user"),
      el("label", { className: "wide" }, "MMTel services document (XML, optional)", mmtel),
    ),
    errors,
    el("div", { className: "controls" }, dry, save),
    note,
  );
  dry.addEventListener("click", () => void send(true));
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void send(false);
  });
  return {
    root: form,
    fill(s) {
      privateIdentity.input.value = s.private_identity;
      // The HSS shows the tel URI among the public identities.
      publics.value = s.public_identities.join("\n");
      tel.input.value = "";
      testUser.checked = s.test_user;
      profile.input.value = s.service_profile;
      for (const x of secrets) x.value = "";
    },
  };
}

function testUsersPart(operator: () => string): { root: HTMLElement; refresh(): Promise<void>; features(): string[] } {
  let current: TestUsers | null = null;
  const note = el("p", { className: "note", hidden: true });
  const facts = el("dl", { className: "facts" });
  const admission = el("select", { name: "admission" });
  fillSelect(admission, admissions);
  const profile = el("select", { name: "test-user-profile" });
  const comment = el("input", { name: "test-users-comment", placeholder: "why, for the history" });
  const setAdmission = el("button", { type: "button", className: "danger", textContent: "Set the admission…" });
  const setProfile = el("button", { type: "button", className: "danger", textContent: "Set the test users' profile…" });
  const result = el("div", {});

  async function refresh(): Promise<void> {
    try {
      const t = await fetchTestUsers();
      current = t;
      showNote(note, null);
      const fact = (k: string, v: string) => [el("dt", { textContent: k }), el("dd", { textContent: v })];
      facts.replaceChildren(
        ...fact("Read from", t.hss_node),
        ...fact("Admission (hss.admission)", t.admission),
        ...fact("Default profile", t.default_service_profile),
        ...fact("Test users' profile", t.test_user_service_profile),
        ...Object.entries(t.service_profiles).flatMap(([name, fs]) => fact(`Profile ${name}`, fs.join(", "))),
      );
      admission.value = t.admission;
      fillSelect(profile, Object.keys(t.service_profiles), t.test_user_service_profile);
    } catch (error) {
      current = null;
      facts.replaceChildren();
      showNote(note, `Could not read the test users' settings: ${describe(error)}`);
    }
  }

  const show = (f: FanOut<ChangeRecord>) =>
    result.replaceChildren(
      el("p", { className: "note", textContent: describeFanOutTotals(f) }),
      el("ul", { className: "steps" }, ...fanOutLines(f, (r) => `generation ${r.change.generation}, record ${r.id}`).map((l) => el("li", { className: l.ok ? "" : "result-failed", textContent: `${l.node}: ${l.text}` }))),
    );

  setAdmission.addEventListener("click", () => {
    void (async () => {
      const to = admission.value as Admission;
      const lines = ["Every HSS node: the setting hss.admission, recorded in the history", to === "test_users" ? "Only test users may register from now on; every other registration is refused" : "Every user may register"];
      if (!(await confirmAction(`Set the admission to ${to}?`, lines, "Set"))) return;
      try {
        show(await putAdmission(to, operator(), comment.value.trim()));
        await refresh();
      } catch (error) {
        showNote(note, `Could not set the admission: ${describe(error)}`);
      }
    })();
  });
  setProfile.addEventListener("click", () => {
    void (async () => {
      const to = profile.value;
      const lines = [
        "Every HSS node: the setting hss.test_user_service_profile, recorded in the history",
        `Test users whose subscription names no profile get ${to}: ${(current?.service_profiles[to] ?? []).join(", ") || "no features"}`,
      ];
      if (!(await confirmAction(`Make ${to} the test users' profile?`, lines, "Set"))) return;
      try {
        show(await putTestUserServiceProfile(to, operator(), comment.value.trim()));
        await refresh();
      } catch (error) {
        showNote(note, `Could not set the profile: ${describe(error)}`);
      }
    })();
  });

  const root = el(
    "div",
    {},
    el("h3", { textContent: "Test users" }),
    el("p", { className: "note", textContent: "A feature is enabled for test users only by making a profile that has it the test users' one." }),
    note,
    facts,
    el(
      "div",
      { className: "controls" },
      el("label", {}, "Admission", admission),
      setAdmission,
      el("label", {}, "Test users' profile", profile),
      setProfile,
      el("label", { className: "wide" }, "Comment", comment),
    ),
    result,
  );
  return { root, refresh, features: () => [...new Set(Object.values(current?.service_profiles ?? {}).flat())] };
}
