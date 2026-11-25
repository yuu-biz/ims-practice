// The alarm panel: the alarm lists of every node, merged by the
// OAM (TS 28.111), and the acknowledgement of an alarm (6.7). The roles
// raise and clear the alarms; the operator only reads and acknowledges.

import { fetchAlarms, minSeverities, putAckState, type AlarmRecord, type Alarms } from "./api.ts";
import { confirmAction, describe, el, fillTable, showNote, type Child } from "./dom.ts";
import { ackAction, ackConfirmation, alarmFilter, alarmViews, describeAck, describeFanOutTotals, droppingNodes, fanOutLines, formatTime, severityCounts, unknownAlarmNodes, type AlarmView } from "./view.ts";

// The same pace as the node state, so that the two agree.
const refreshMs = 5000;

const columns = ["Severity", "Node", "Role", "Peer", "Type", "Probable cause", "Specific problem", "Raised", "Cleared", "Acknowledged", "Additional text", ""];

export interface AlarmsPanel {
  refresh(): Promise<void>;
}

export function mountAlarmsPanel(root: HTMLElement, operator: () => string): AlarmsPanel {
  const view = el("select", { name: "alarm-view" });
  view.replaceChildren(...Object.entries(alarmViews).map(([value, v]) => el("option", { value, textContent: v.label })));
  const severity = el("select", { name: "alarm-min-severity" });
  severity.replaceChildren(el("option", { value: "", textContent: "any" }), ...minSeverities.map((s) => el("option", { value: s, textContent: s })));
  const refreshButton = el("button", { type: "button", textContent: "Refresh" });
  const auto = el("input", { type: "checkbox", checked: true });
  const read = el("span", { className: "note" });
  const note = el("p", { className: "note", hidden: true });
  const unknown = el("ul", { className: "steps", hidden: true });
  const counts = el("ul", { className: "counts" });
  counts.setAttribute("aria-label", "Alarms by severity");
  const table = el(
    "table",
    { id: "alarms" },
    el("caption", { className: "visually-hidden", textContent: "Alarms of every node" }),
    el("thead", {}, el("tr", {}, ...columns.map((h) => el("th", { textContent: h })))),
  );
  const result = el("div", { className: "result" });
  root.append(
    el("p", {
      className: "note",
      textContent:
        "The roles raise and clear their alarms; an operator acknowledges them. A cleared alarm stays in the list until it is acknowledged, or until the node's retention has passed (an hour by default). A minimum severity also shows INDETERMINATE, which cannot be ranked.",
    }),
    el(
      "div",
      { className: "controls" },
      el("label", {}, "Show", view),
      el("label", {}, "Minimum severity", severity),
      refreshButton,
      el("label", { className: "check" }, auto, "Refresh every 5 s"),
      read,
    ),
    note,
    unknown,
    counts,
    el("div", { className: "scroll" }, table),
    result,
  );

  let reading = false;
  async function refresh(): Promise<void> {
    if (reading) return;
    reading = true;
    try {
      const a = await fetchAlarms(alarmFilter(view.value as AlarmView, severity.value));
      render(a);
      read.textContent = `Read at ${formatTime(new Date().toISOString())}`;
    } catch (error) {
      showNote(note, `Could not read the alarms: ${describe(error)}`);
    } finally {
      reading = false;
    }
  }

  function render(a: Alarms): void {
    const failed = unknownAlarmNodes(a);
    const problems = [
      ...failed.map((f) => `${f.node}: ${f.why}`),
      ...droppingNodes(a).map((d) => `${d.node}: dropped ${d.dropped} records because its alarm list was full; the list may lack alarms`),
    ];
    showNote(note, failed.length === 0 ? null : `${failed.length} of ${a.nodes.length} nodes did not answer: their alarms are unknown, not none.`);
    unknown.hidden = problems.length === 0;
    unknown.replaceChildren(...problems.map((p) => el("li", { className: "result-failed", textContent: p })));
    const c = severityCounts(a.alarms);
    counts.replaceChildren(
      ...(c.length === 0 ? [el("li", { textContent: "no alarms" })] : c.map((x) => el("li", { className: `severity-${x.severity}`, textContent: `${x.severity} ${x.count}` }))),
    );
    fillTable(table, a.alarms.map(row), failed.length === 0 ? "No alarm in the lists." : "No alarm from the nodes that answered.");
  }

  function row(r: AlarmRecord): Child[] {
    const action = ackAction(r);
    const button = el("button", { type: "button", textContent: `${action.label}…` });
    button.addEventListener("click", () => void acknowledge(r, button));
    const sev = el("span", { className: `severity-${r.perceived_severity}`, textContent: r.perceived_severity });
    sev.title = r.object_instance;
    return [
      sev,
      r.node,
      r.role === "" ? "(node)" : r.role,
      r.peer,
      r.alarm_type,
      r.probable_cause,
      r.specific_problem,
      // A change other than the clear, such as a new severity, under the
      // time the alarm was raised.
      formatTime(r.alarm_raised_time) +
        (r.alarm_changed_time === null || r.alarm_changed_time === r.alarm_cleared_time ? "" : `\nchanged ${formatTime(r.alarm_changed_time)}`),
      r.alarm_cleared_time === null ? "" : formatTime(r.alarm_cleared_time),
      describeAck(r),
      r.additional_text,
      button,
    ];
  }

  async function acknowledge(r: AlarmRecord, button: HTMLButtonElement): Promise<void> {
    const action = ackAction(r);
    const who = operator();
    const asked = ackConfirmation(r, who);
    if ("refused" in asked) {
      result.replaceChildren(el("p", { className: "note error", textContent: asked.refused }));
      return;
    }
    if (!(await confirmAction(`${action.label} the alarm?`, asked.scope, action.label))) return;
    button.disabled = true;
    try {
      const f = await putAckState(r.node, r.alarm_id, action.to, who);
      result.replaceChildren(
        el("p", { className: "note", textContent: describeFanOutTotals(f) }),
        el(
          "ul",
          { className: "steps" },
          ...fanOutLines(f, (x) => `${x.alarm_id} ${x.ack_state}`).map((l) => el("li", { className: l.ok ? "" : "result-failed", textContent: `${l.node}: ${l.text}` })),
        ),
      );
    } catch (error) {
      result.replaceChildren(el("p", { className: "note error", textContent: `Could not change the acknowledgement: ${describe(error)}` }));
    } finally {
      button.disabled = false;
    }
    await refresh();
  }

  for (const control of [view, severity]) control.addEventListener("change", () => void refresh());
  refreshButton.addEventListener("click", () => void refresh());
  window.setInterval(() => {
    if (auto.checked && document.visibilityState === "visible") void refresh();
  }, refreshMs);
  return { refresh };
}
