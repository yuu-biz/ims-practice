// The configuration panel: the configuration of a node or
// a role as the nodes show it, a change of the settings that change
// while the nodes run, the history of a node, and its rollback.

import {
  fetchConfiguration,
  fetchHistory,
  HttpError,
  patchConfiguration,
  rollbackChange,
  rollbackChangeSet,
  type ChangeRecord,
  type Configuration,
  type Endpoint,
  type FanOut,
  type Scope,
} from "./api.ts";
import { confirmAction, describe, el, fillSelect, fillTable, showNote } from "./dom.ts";
import { scopePicker } from "./scope-picker.ts";
import {
  compactJSON,
  describeChanges,
  describeConflicts,
  describeFanOutTotals,
  describeNodeError,
  describeScope,
  fanOutLines,
  formatTime,
  refusedSettings,
  settingEdits,
} from "./view.ts";

const historyLimit = 50;

export function mountConfigPanel(root: HTMLElement, inventory: readonly Endpoint[], operator: () => string): void {
  const picker = scopePicker("config", inventory);
  const load = el("button", { type: "button", textContent: "Read the configuration" });
  const note = el("p", { className: "note", hidden: true });
  const views = el("div", {});
  const settings = el("div", {});
  const history = historyPart(inventory, operator, () => void read());
  root.append(
    el("p", {
      className: "note",
      textContent: "Each node redacts its secrets (passwords, K, OPc) before it answers: they show as REDACTED, or null when not set.",
    }),
    picker.root,
    el("div", { className: "controls" }, load),
    note,
    views,
    settings,
    history.root,
  );

  let reading = 0;
  async function read(): Promise<void> {
    const scope = picker.scope();
    const mine = ++reading;
    load.disabled = true;
    try {
      const f = await fetchConfiguration(scope);
      if (mine !== reading) return;
      showNote(note, f.failed > 0 ? `${f.failed} of ${f.nodes.length} nodes did not give their configuration.` : null);
      views.replaceChildren(...f.nodes.map((n) => configurationView(n.node, n.result, n.error === null ? null : describeNodeError(n.error))));
      const first = f.nodes.find((n) => n.result !== null)?.result ?? null;
      settings.replaceChildren(first === null ? "" : changeForm(inventory, scope, first, operator, () => void read()));
      if (scope.kind === "node") history.show(scope.node);
    } catch (error) {
      if (mine === reading) showNote(note, `Could not read the configuration: ${describe(error)}`);
    } finally {
      load.disabled = false;
    }
  }
  load.addEventListener("click", () => void read());
}

function configurationView(node: string, c: Configuration | null, error: string | null): HTMLElement {
  if (c === null) return el("p", { className: "note error", textContent: `${node}: failed: ${error ?? ""}` });
  return el(
    "details",
    {},
    el("summary", { textContent: `${node}: generation ${c.generation}` }),
    // As the node answered it: no value is taken out or put in here.
    el("pre", { textContent: JSON.stringify(c.configuration, null, 2) }),
  );
}

// The form lists the changeable settings of the first node that
// answered; the nodes of a role have the same ones.
function changeForm(inventory: readonly Endpoint[], scope: Scope, c: Configuration, operator: () => string, reread: () => void): HTMLElement {
  if (c.changeable.length === 0) {
    return el("p", { className: "note", textContent: "No setting of this node changes while it runs: the rest changes with the configuration file and a restart." });
  }
  const inputs = new Map<string, HTMLInputElement>();
  const errors = new Map<string, HTMLElement>();
  const table = el("table", {}, el("thead", {}, el("tr", {}, ...["Setting", "Description", "Value now", "New value (JSON)"].map((h) => el("th", { textContent: h })))));
  fillTable(
    table,
    c.changeable.map((s) => {
      const input = el("input", { name: s.name, value: compactJSON(s.value), spellcheck: false });
      input.setAttribute("aria-label", `New value of ${s.name}`);
      const error = el("div", { className: "field-error" });
      error.id = `error-${s.name}`;
      input.setAttribute("aria-describedby", error.id);
      inputs.set(s.name, input);
      errors.set(s.name, error);
      return [s.name, s.description, compactJSON(s.value), el("div", {}, input, error)];
    }),
    "",
  );
  const comment = el("input", { name: "comment", placeholder: "why, for the history" });
  const submit = el("button", { type: "submit", textContent: "Change…" });
  const note = el("p", { className: "note", hidden: true });
  const result = el("div", {});
  const form = el(
    "form",
    {},
    el("h3", { textContent: "Change the settings" }),
    el("div", { className: "scroll" }, table),
    el("div", { className: "controls" }, el("label", { className: "wide" }, "Comment", comment), submit),
    note,
    result,
  );
  const showErrors = (byName: Record<string, string>) => {
    for (const [name, box] of errors) box.textContent = byName[name] ?? "";
    for (const [name, input] of inputs) input.setAttribute("aria-invalid", String(name in byName));
  };
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void (async () => {
      const edits = settingEdits(c.changeable, Object.fromEntries([...inputs].map(([name, input]) => [name, input.value])));
      showErrors(edits.errors);
      if (Object.keys(edits.errors).length > 0) return;
      const changed = c.changeable.filter((s) => s.name in edits.settings).map((s) => ({ name: s.name, previous: s.value, value: edits.settings[s.name] }));
      if (changed.length === 0) {
        showNote(note, "No value differs from the value now.");
        return;
      }
      showNote(note, null);
      const lines = [describeScope(inventory, scope), ...describeChanges(changed), "A new generation, one above the highest of these nodes, recorded in the history"];
      if (!(await confirmAction("Change the configuration?", lines, "Change"))) return;
      submit.disabled = true;
      try {
        const f = await patchConfiguration(scope, edits.settings, operator(), comment.value.trim());
        const refused = refusedSettings(f);
        showErrors(Object.fromEntries(Object.entries(refused).map(([name, why]) => [name, why.join("\n")])));
        result.replaceChildren(changeResult(f));
        if (f.failed === 0) reread();
      } catch (error) {
        showNote(note, `Could not change the configuration: ${describe(error)}`);
      } finally {
        submit.disabled = false;
      }
    })();
  });
  return form;
}

function changeResult(f: FanOut<ChangeRecord>): HTMLElement {
  return el(
    "div",
    {},
    el("p", { className: "note", textContent: describeFanOutTotals(f) }),
    el(
      "ul",
      { className: "steps" },
      ...fanOutLines(f, (r) => `generation ${r.change.generation}, record ${r.id}: ${describeChanges(r.change.changes).join("; ")}`).map((l) =>
        el("li", { className: l.ok ? "" : "result-failed", textContent: `${l.node}: ${l.text}` }),
      ),
    ),
  );
}

function historyPart(inventory: readonly Endpoint[], operator: () => string, reread: () => void): { root: HTMLElement; show(node: string): void } {
  const node = el("select", { name: "history-node" });
  node.setAttribute("aria-label", "Node");
  fillSelect(
    node,
    inventory.map((e) => e.name),
  );
  const load = el("button", { type: "button", textContent: "Read the history" });
  const note = el("p", { className: "note", hidden: true });
  const result = el("div", {});
  const table = el(
    "table",
    {},
    el("thead", {}, el("tr", {}, ...["Recorded", "Generation", "Operator", "Comment", "Changes", "Record", ""].map((h) => el("th", { textContent: h })))),
  );
  const root = el(
    "div",
    {},
    el("h3", { textContent: "History of a node" }),
    el("div", { className: "controls" }, el("label", {}, "Node", node), load),
    note,
    el("div", { className: "scroll" }, table),
    result,
  );

  async function show(name: string): Promise<void> {
    node.value = name;
    try {
      const records = await fetchHistory(name, historyLimit);
      showNote(note, records.length === historyLimit ? `The newest ${historyLimit} changes.` : null, false);
      fillTable(table, records.map(row), `No change of ${name} is in the history.`);
    } catch (error) {
      fillTable(table, [], "Not available.");
      showNote(note, `Could not read the history: ${describe(error)}`);
    }
  }

  function row(r: ChangeRecord): (string | Node)[] {
    const one = el("button", { type: "button", textContent: "Roll back…" });
    one.addEventListener("click", () => void rollBack(r, false));
    const set = el("button", { type: "button", textContent: "Roll back the change set…" });
    set.addEventListener("click", () => void rollBackSet(r, false));
    const what = [r.rollback_of === "" ? "" : `rollback of ${r.rollback_of}${r.forced ? " (forced)" : ""}`, ...describeChanges(r.change.changes)].filter((l) => l !== "");
    return [
      formatTime(r.recorded_at),
      String(r.change.generation),
      r.change.operator,
      r.change.comment,
      what.join("\n"),
      `${r.id}\nset ${r.change_set}`,
      el("div", { className: "controls" }, one, set),
    ];
  }

  // A rollback puts the previous values back as a new change. The OAM
  // refuses with 409 and each setting in the way when a later change
  // touched one, unless forced.
  async function rollBack(r: ChangeRecord, force: boolean): Promise<void> {
    const lines = [`Node ${r.node}`, `Record ${r.id}, generation ${r.change.generation}`, ...describeChanges(r.change.changes, true), "A new change with a new generation; the history keeps both"];
    if (force) lines.push("Forced: also the settings that changed since this change");
    if (!(await confirmAction(force ? "Roll back anyway, over the later changes?" : "Roll back this change?", lines, force ? "Roll back anyway" : "Roll back"))) return;
    try {
      const done = await rollbackChange(r.id, { operator: operator(), comment: "", force });
      result.replaceChildren(el("p", { className: "note", textContent: `Rolled back as record ${done.id}, generation ${done.change.generation}${done.forced ? ", forced" : ""}.` }));
      await show(r.node);
      reread();
    } catch (error) {
      if (error instanceof HttpError && error.status === 409) {
        result.replaceChildren(conflict(error.message, describeConflicts(error.settings), () => void rollBack(r, true)));
        return;
      }
      result.replaceChildren(el("p", { className: "note error", textContent: `Could not roll back ${r.id}: ${describe(error)}` }));
    }
  }

  async function rollBackSet(r: ChangeRecord, force: boolean): Promise<void> {
    const lines = [`Change set ${r.change_set}: the record of each node that this one operation changed`, ...describeChanges(r.change.changes, true)];
    if (force) lines.push("Forced: also the settings that changed since this change");
    if (!(await confirmAction(force ? "Roll back the change set anyway?" : "Roll back the change set?", lines, force ? "Roll back anyway" : "Roll back"))) return;
    try {
      const f = await rollbackChangeSet(r.change_set, { operator: operator(), comment: "", force });
      const conflicts = f.nodes.filter((n) => n.error?.status_code === 409).flatMap((n) => describeConflicts(n.error?.settings ?? {}).map((l) => `${n.node}: ${l}`));
      const parts: Node[] = [changeResult(f)];
      if (conflicts.length > 0 && !force) parts.push(conflict("Some nodes refused: their settings changed since.", conflicts, () => void rollBackSet(r, true)));
      result.replaceChildren(...parts);
      await show(r.node);
      reread();
    } catch (error) {
      result.replaceChildren(el("p", { className: "note error", textContent: `Could not roll back change set ${r.change_set}: ${describe(error)}` }));
    }
  }

  load.addEventListener("click", () => void show(node.value));
  return { root, show: (name) => void show(name) };
}

function conflict(message: string, lines: string[], force: () => void): HTMLElement {
  const button = el("button", { type: "button", className: "danger", textContent: "Roll back anyway (force)…" });
  button.addEventListener("click", force);
  return el(
    "div",
    { className: "conflict" },
    el("p", { className: "note error", textContent: `Refused: ${message}` }),
    el("ul", { className: "steps" }, ...lines.map((l) => el("li", { textContent: l }))),
    button,
  );
}
