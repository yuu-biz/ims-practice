// The backup panel: back up the persistent data of the network,
// list the backups, see what a restore would change (the dry run), and
// restore, each with its steps as they stream.

import { createBackup, fetchBackup, fetchBackups, restoreBackup, type Manifest, type Step, type StreamLine, type Summary } from "./api.ts";
import { confirmAction, describe, el, fillTable, showNote } from "./dom.ts";
import { outcomeView } from "./outcome-view.ts";
import { describeRecords, describeStep, describeTally, formatTime, isChange, isProblem, mergeStep, outcomeTally } from "./view.ts";

export function mountBackupsPanel(root: HTMLElement, operator: () => string): void {
  const comment = el("input", { name: "backup-comment", placeholder: "why, for the manifest" });
  const create = el("button", { type: "button", textContent: "Back up now" });
  const createSteps = el("ul", { className: "steps" });
  createSteps.setAttribute("aria-live", "polite");
  const note = el("p", { className: "note", hidden: true });
  const table = el(
    "table",
    {},
    el("thead", {}, el("tr", {}, ...["Backup", "Created", "Operator", "Comment", "State", "Records", "Bytes", "Restores", ""].map((h) => el("th", { textContent: h })))),
  );
  const detail = el("div", {});
  const run = el("div", {});
  root.append(
    el("p", {
      className: "note",
      textContent:
        "A backup holds the subscriptions of the HSS with their credentials and SQN_HE. Registrations, dialogs and other state that lives only while a node runs are not backed up: the UEs register again.",
    }),
    el("div", { className: "controls" }, el("label", { className: "wide" }, "Comment", comment), create),
    createSteps,
    note,
    el("h3", { textContent: "Backups" }),
    el("div", { className: "scroll" }, table),
    detail,
    run,
  );

  async function refresh(): Promise<void> {
    try {
      const backups = await fetchBackups();
      fillTable(table, backups.map(row), "No backup yet.");
    } catch (error) {
      fillTable(table, [], "Not available.");
      showNote(note, `Could not list the backups: ${describe(error)}`);
    }
  }

  function row(m: Manifest): (string | Node)[] {
    const details = el("button", { type: "button", textContent: "Details" });
    details.addEventListener("click", () => void showDetail(m.id));
    const diff = el("button", { type: "button", textContent: "Dry run" });
    const restore = el("button", { type: "button", className: "danger", textContent: "Restore…" });
    const restorable = m.state === "completed";
    diff.disabled = restore.disabled = !restorable;
    if (!restorable) diff.title = restore.title = "Only a completed backup can be restored.";
    diff.addEventListener("click", () => void restoreFrom(m, true));
    restore.addEventListener("click", () => void restoreFrom(m, false));
    return [
      m.id,
      formatTime(m.created),
      m.operator,
      m.comment,
      el("span", { className: `job-${m.state}`, textContent: m.state }),
      describeRecords(m.records),
      m.bytes === 0 ? "" : String(m.bytes),
      String(m.restores.length),
      el("div", { className: "controls" }, details, diff, restore),
    ];
  }

  async function showDetail(id: string): Promise<void> {
    try {
      const m = await fetchBackup(id);
      detail.replaceChildren(
        el("h3", { textContent: `Backup ${m.id}` }),
        el("p", { className: "note", textContent: `Sections: ${m.sections.join(", ")}` }),
        stepList(m.steps),
        ...m.restores.map((r) =>
          el(
            "div",
            {},
            el("h4", { textContent: `${r.dry_run ? "Dry run" : "Restore"} at ${formatTime(r.started)}${r.operator === "" ? "" : ` by ${r.operator}`}: ${r.state}` }),
            stepList(r.steps),
            el("p", { className: "note", textContent: describeSummary(r.summary) }),
          ),
        ),
      );
    } catch (error) {
      detail.replaceChildren(el("p", { className: "note error", textContent: `Could not read backup ${id}: ${describe(error)}` }));
    }
  }

  create.addEventListener("click", () => {
    void (async () => {
      create.disabled = true;
      showNote(note, null);
      let steps: Step[] = [];
      try {
        await createBackup(operator(), comment.value.trim(), (line) => {
          if (line.kind === "step") steps = mergeStep(steps, line.step);
          if (line.kind === "manifest") steps = line.manifest.steps;
          if (line.kind === "error") showNote(note, `The backup stopped: ${line.error}`);
          createSteps.replaceChildren(...stepItems(steps));
        });
        comment.value = "";
      } catch (error) {
        showNote(note, `Could not back up: ${describe(error)}`);
      } finally {
        create.disabled = false;
      }
      await refresh();
    })();
  });

  // A restore checks the archive whole first (verify), then says what
  // each record would do (diff) or puts each back (apply).
  async function restoreFrom(m: Manifest, dryRun: boolean): Promise<void> {
    if (!dryRun) {
      const lines = [
        `Backup ${m.id} of ${formatTime(m.created)}: ${describeRecords(m.records).replace(/\n/g, ", ")} records`,
        "Each subscription of the backup replaces the one in the HSS, credentials and SQN_HE included",
        "Subscriptions that are not in the backup are kept",
        "Restored subscriptions are Not Registered: their users register again",
      ];
      if (!(await confirmAction("Restore the backup?", lines, "Restore"))) return;
    }
    const tally = outcomeTally(dryRun ? isChange : isProblem);
    const view = outcomeView(dryRun ? "Records that the restore would change" : "Rejected and failed records", dryRun ? "The restore would change nothing." : "No record was rejected or failed.");
    const steps = el("ul", { className: "steps" });
    const failure = el("p", { className: "note error", hidden: true });
    run.replaceChildren(el("h3", { textContent: `${dryRun ? "Dry run of a restore" : "Restore"} from ${m.id}` }), steps, failure, view.root);
    let shown: Step[] = [];
    let summary: Summary | null = null;
    const onLine = (line: StreamLine) => {
      switch (line.kind) {
        case "step":
          shown = mergeStep(shown, line.step);
          steps.replaceChildren(...stepItems(shown));
          break;
        case "outcome":
          tally.add(line.outcome);
          view.update(tally, `${dryRun ? "Diff" : "Apply"}: up to line ${line.outcome.line} of the archive…`);
          break;
        case "summary":
          summary = line.summary;
          break;
        case "error":
          showNote(failure, `The restore stopped: ${line.error}`);
          break;
        case "manifest":
          break;
      }
    };
    try {
      await restoreBackup(m.id, dryRun, operator(), onLine);
      const s = summary as Summary | null;
      view.update(tally, s === null ? "The answer ended before its summary." : `${dryRun ? "Dry run" : "Restore"} done: ${describeSummary(s)}.${dryRun ? " Nothing was changed." : ""}`);
    } catch (error) {
      showNote(failure, `Could not restore: ${describe(error)}`);
    }
    await refresh();
  }

  void refresh();
}

function stepItems(steps: readonly Step[]): HTMLLIElement[] {
  return steps.map((s) => el("li", { className: `job-${s.state}`, textContent: describeStep(s) }));
}

function stepList(steps: readonly Step[]): HTMLUListElement {
  return el("ul", { className: "steps" }, ...stepItems(steps));
}

function describeSummary(s: Summary): string {
  return `${s.lines} records, ${describeTally(s)}`;
}
