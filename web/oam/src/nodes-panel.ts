// The node panel: the state of every node and role, with its alarms,
// and the operations on them: the drain, which takes a role out of
// service without cutting its calls, and the log level.

import {
  administrativeStates,
  fetchState,
  logLevels,
  putAdministrativeState,
  putLogLevel,
  type AdministrativeState,
  type Endpoint,
  type FunctionState,
} from "./api.ts";
import { confirmAction, describe, el, fillSelect, fillTable, showNote, type Child } from "./dom.ts";
import { scopePicker } from "./scope-picker.ts";
import {
  administrativeActions,
  describeCounts,
  describeFanOutTotals,
  describeScope,
  fanOutLines,
  formatTime,
  nodeRows,
  roleSummaries,
  scopeNodes,
  type NodeRow,
} from "./view.ts";

// How often the panel reads the state again while auto refresh is on,
// so that a drain can be watched.
const refreshMs = 5000;

export interface NodesPanel {
  refresh(): Promise<void>;
}

export function mountNodesPanel(
  root: { roles: HTMLTableElement; nodes: HTMLTableElement; note: HTMLElement; read: HTMLElement; refresh: HTMLButtonElement; auto: HTMLInputElement; operations: HTMLElement },
  inventory: readonly Endpoint[],
  operator: () => string,
): NodesPanel {
  let reading = false;

  async function refresh(): Promise<void> {
    if (reading) return;
    reading = true;
    try {
      const state = await fetchState();
      const rows = nodeRows(inventory, state);
      showNote(root.note, state.failed > 0 ? `${state.failed} of ${state.nodes.length} nodes did not answer: they are shown as failed.` : null);
      fillTable(root.roles, roleSummaries(rows).map(roleRow), "The inventory has no roles.");
      renderNodes(root.nodes, rows);
      root.read.textContent = `Read at ${formatTime(new Date().toISOString())}`;
    } catch (error) {
      showNote(root.note, `Could not read the state: ${describe(error)}`);
    } finally {
      reading = false;
    }
  }

  root.refresh.addEventListener("click", () => void refresh());
  window.setInterval(() => {
    if (root.auto.checked && document.visibilityState === "visible") void refresh();
  }, refreshMs);

  root.operations.append(administrativeForm(inventory, operator, refresh), logLevelForm(inventory, operator, refresh));
  return { refresh };
}

function roleRow(s: ReturnType<typeof roleSummaries>[number]): Child[] {
  const failed = s.failed.length === 0 ? "" : `\nfailed: ${s.failed.join(", ")}`;
  return [
    s.role,
    `${s.nodes.length} (${s.nodes.join(", ")})${failed}`,
    describeCounts(s.administrative),
    describeCounts(s.operational),
    s.users === null ? "" : String(s.users),
    s.generations.length > 1 ? `${s.generations.join(", ")} (differ)` : s.generations.join(""),
  ];
}

// One row per role of each node; the node's own columns are on its first
// row. A node that failed has one row with why.
function renderNodes(table: HTMLTableElement, rows: readonly NodeRow[]): void {
  const body = table.tBodies[0] ?? table.createTBody();
  const columns = table.tHead?.rows[0]?.cells.length ?? 1;
  const trs: HTMLTableRowElement[] = [];
  for (const row of rows) {
    const name = el("th", { scope: "row", textContent: row.name });
    if (row.state === null) {
      const tr = el("tr", { className: "failed" }, name, el("td", { textContent: row.roles.join(", ") }), el("td", { colSpan: columns - 2, textContent: `failed: ${row.error ?? ""}` }));
      trs.push(tr);
      continue;
    }
    const s = row.state;
    const build = [s.build.version, s.build.revision, s.build.go_version].filter((v) => v !== "").join(" ");
    const nodeCells = [
      s.instance === "" ? s.node : `${s.node} (${s.instance})`,
      String(s.configuration_generation),
      s.log_level,
      build,
      formatTime(s.start_time),
    ];
    s.functions.forEach((f, i) => {
      const tr = el("tr", {});
      if (i === 0) {
        name.rowSpan = s.functions.length;
        tr.append(name);
      }
      tr.append(...functionCells(f).map((c) => el("td", {}, c)));
      if (i === 0) {
        for (const c of nodeCells) tr.append(el("td", { rowSpan: s.functions.length, textContent: c }));
      }
      trs.push(tr);
    });
    if (s.functions.length === 0) trs.push(el("tr", {}, name, el("td", { colSpan: columns - 1, textContent: "no roles" })));
  }
  body.replaceChildren(...trs);
}

function functionCells(f: FunctionState): Child[] {
  return [
    f.role,
    el("span", { className: `state-${f.administrative_state}`, textContent: f.administrative_state }),
    el("span", { className: `state-${f.operational_state}`, textContent: f.operational_state }),
    f.users === null ? "" : String(f.users),
    f.shutdown_deadline === null ? "" : formatTime(f.shutdown_deadline),
    f.summary === null ? "" : JSON.stringify(f.summary),
  ];
}

function resultList(lines: { node: string; ok: boolean; text: string }[]): HTMLUListElement {
  return el("ul", { className: "steps" }, ...lines.map((l) => el("li", { className: l.ok ? "" : "result-failed", textContent: `${l.node}: ${l.text}` })));
}

function administrativeForm(inventory: readonly Endpoint[], operator: () => string, refresh: () => Promise<void>): HTMLFormElement {
  const picker = scopePicker("admin", inventory);
  const role = el("select", { name: "role" });
  const state = el("select", { name: "administrative_state" });
  state.replaceChildren(...administrativeStates.map((s) => el("option", { value: s, textContent: administrativeActions[s].label, selected: s === "SHUTTINGDOWN" })));
  const timeout = el("input", { type: "number", name: "timeout_seconds", min: "0", step: "1", value: "300", inputMode: "numeric" });
  const submit = el("button", { type: "submit", textContent: "Apply…" });
  const result = el("div", { className: "result" });
  const note = el("p", { className: "note", hidden: true });

  // On one node the role is one of the node's; on a role, the role.
  const syncRole = () => {
    const scope = picker.scope();
    const roles = scope.kind === "node" ? (inventory.find((e) => e.name === scope.node)?.roles ?? []) : [scope.role];
    fillSelect(role, roles);
    role.disabled = scope.kind === "role";
    timeout.disabled = state.value !== "SHUTTINGDOWN";
  };
  picker.onChange(syncRole);
  state.addEventListener("change", syncRole);
  syncRole();

  const form = el(
    "form",
    { className: "operation" },
    el("h3", { textContent: "Drain, cancel the drain, or lock" }),
    picker.root,
    el(
      "div",
      { className: "controls" },
      el("label", {}, "Role", role),
      el("label", {}, "Administrative state", state),
      el("label", {}, "Drain timeout (seconds, 0 for none)", timeout),
      submit,
    ),
    note,
    result,
  );
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void (async () => {
      const scope = picker.scope();
      const to = state.value as AdministrativeState;
      const seconds = Number(timeout.value);
      if (!Number.isInteger(seconds) || seconds < 0) {
        showNote(note, "The timeout is a whole number of seconds, 0 or more.");
        return;
      }
      showNote(note, null);
      const scopeLines = [describeScope(inventory, scope), `Role ${role.value}`, `To ${to}: ${administrativeActions[to].effect}`];
      if (to === "SHUTTINGDOWN") scopeLines.push(seconds > 0 ? `Locked after ${seconds} s even with users left` : "No timeout: locked only when no user is left");
      if (!(await confirmAction(`${administrativeActions[to].label}?`, scopeLines, `Set ${to}`))) return;
      submit.disabled = true;
      try {
        const f = await putAdministrativeState(scope, role.value, to, seconds, operator());
        result.replaceChildren(
          el("p", { className: "note", textContent: describeFanOutTotals(f) }),
          resultList(fanOutLines(f, (r) => `${r.role} ${r.administrative_state}${r.users === null ? "" : `, ${r.users} users`}`)),
        );
      } catch (error) {
        showNote(note, `Could not set the administrative state: ${describe(error)}`);
      } finally {
        submit.disabled = false;
      }
      await refresh();
    })();
  });
  return form;
}

function logLevelForm(inventory: readonly Endpoint[], operator: () => string, refresh: () => Promise<void>): HTMLFormElement {
  const picker = scopePicker("log", inventory);
  const level = el("select", { name: "level" });
  fillSelect(level, logLevels, "INFO");
  const submit = el("button", { type: "submit", textContent: "Set…" });
  const result = el("div", { className: "result" });
  const note = el("p", { className: "note", hidden: true });
  const form = el(
    "form",
    { className: "operation" },
    el("h3", { textContent: "Log level" }),
    el("p", { className: "note", textContent: "The level belongs to the node: a node with several roles changes for all of them." }),
    picker.root,
    el("div", { className: "controls" }, el("label", {}, "Level", level), submit),
    note,
    result,
  );
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void (async () => {
      const scope = picker.scope();
      const nodes = scopeNodes(inventory, scope);
      if (!(await confirmAction(`Set the log level to ${level.value}?`, [describeScope(inventory, scope), `${nodes.length} ${nodes.length === 1 ? "node" : "nodes"} log at ${level.value} from now on`], "Set the level"))) return;
      submit.disabled = true;
      try {
        const f = await putLogLevel(scope, level.value, operator());
        showNote(note, null);
        result.replaceChildren(
          el("p", { className: "note", textContent: describeFanOutTotals(f) }),
          resultList(fanOutLines(f, (r) => `${r.previous} -> ${r.level}`)),
        );
      } catch (error) {
        showNote(note, `Could not set the log level: ${describe(error)}`);
      } finally {
        submit.disabled = false;
      }
      await refresh();
    })();
  });
  return form;
}
