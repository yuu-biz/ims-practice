// The fault panel: the faults that the fault agent holds, a form that
// injects one, and a button that clears one. The agent runs only with
// deploy/chaos/agent.yaml, so the panel says what to start when it does
// not answer.

import {
  clearFault,
  faultKinds,
  fetchFaults,
  HttpError,
  injectFault,
  type Fault,
  type FaultKind,
} from "./api.ts";
import { clock, el, fillTable } from "./dom.ts";
import { canClear, describeFault, faultFields, faultRequest, type FaultForm } from "./faults.ts";

// How often the panel asks for the faults again, to show a fault that
// has expired or that another client injected. The agent clears the
// faults whose time is up once a second.
const refreshMs = 5000;

// startAgent is the command that brings the agent up beside a running
// IMS; it recreates the Lab, which joins the agent's network.
const startAgent = "docker compose -f compose.yaml -f deploy/chaos/agent.yaml up -d";

export function mountFaultPanel(container: HTMLElement, table: HTMLTableElement, note: HTMLParagraphElement): void {
  const target = el("input", { name: "fault_target", required: true, placeholder: "pcscf", spellcheck: false });
  const kind = el("select", { name: "fault_kind" }, ...faultKinds.map((k) => el("option", { value: k, textContent: k })));
  const network = el("input", { name: "fault_network", placeholder: "every interface", spellcheck: false });
  const delay = el("input", { name: "fault_delay", placeholder: "200ms", spellcheck: false });
  const jitter = el("input", { name: "fault_jitter", placeholder: "20ms", spellcheck: false });
  const lossPercent = el("input", { name: "fault_loss_percent", type: "number", min: "1", max: "100", placeholder: "10" });
  const peers = el("textarea", { name: "fault_peers", rows: 2, placeholder: "icscf\nscscf-*", spellcheck: false });
  const duration = el("input", { name: "fault_duration", placeholder: "5m", spellcheck: false });
  const inject = el("button", { type: "submit", textContent: "Inject" });

  const field = (text: string, input: HTMLElement) => el("label", {}, text, input);
  const fields = {
    network: field("Network", network),
    delay: field("Delay", delay),
    jitter: field("Jitter", jitter),
    lossPercent: field("Loss (%)", lossPercent),
    peers: field("Peers, one per line", peers),
    duration: field("Duration", duration),
  };
  const form = el(
    "form",
    { className: "add-form" },
    el(
      "fieldset",
      {},
      el("legend", {}, "Inject a fault"),
      field("Target", target),
      field("Kind", kind),
      fields.network,
      fields.delay,
      fields.jitter,
      fields.lossPercent,
      fields.peers,
      fields.duration,
      inject,
    ),
  );
  container.append(form);
  const fieldset = form.querySelector("fieldset")!;

  // The panel stops asking when the agent is not there, so that the Lab
  // does not log a failed request every few seconds; the button loads
  // again once a learner has started the agent.
  let available = true;
  let busy = false;
  const reload = el("button", { type: "button", className: "link", textContent: "reload" });

  const show = (text: string | null, isError = true) => {
    note.hidden = text === null;
    note.textContent = "";
    if (text !== null) note.append(text, " ", reload);
    note.classList.toggle("error", isError);
  };
  const failed = (what: string, error: unknown) => {
    show(`Could not ${what}: ${error instanceof Error ? error.message : String(error)}`);
  };

  // Each kind takes its own fields, and a field of another kind is
  // refused by the agent.
  const showFields = () => {
    const offered = faultFields(kind.value as FaultKind);
    fields.network.hidden = !offered.network;
    fields.delay.hidden = !offered.delay;
    fields.jitter.hidden = !offered.delay;
    fields.lossPercent.hidden = !offered.lossPercent;
    fields.peers.hidden = !offered.peers;
    fields.duration.hidden = !offered.duration;
  };

  const row = (fault: Fault): (string | Node)[] => {
    const clear = el("button", { type: "button", className: "link danger", textContent: "Clear" });
    clear.disabled = !canClear(fault);
    clear.addEventListener("click", () => {
      void run(`clear the fault ${fault.id}`, async () => {
        await clearFault(fault.id);
      });
    });
    const state = fault.error === "" ? [fault.state] : [fault.state, el("br"), el("span", { className: "muted" }, fault.error)];
    return [
      fault.id,
      fault.target,
      describeFault(fault),
      el("span", {}, ...state),
      clock(fault.created_at),
      fault.ended_at === "" ? (fault.expires_at === "" ? "" : clock(fault.expires_at)) : clock(fault.ended_at),
      clear,
    ];
  };

  const load = async () => {
    let faults: Fault[];
    try {
      faults = await fetchFaults();
    } catch (error) {
      available = false;
      fieldset.disabled = true;
      fillTable(table, [], "Not available.");
      if (error instanceof HttpError && error.status === 503) {
        show("No fault agent is configured. Set lab.fault_agent in the Lab config to inject faults from here.");
      } else if (error instanceof HttpError && error.status === 502) {
        show(`The fault agent is not running. Start it with: ${startAgent}`);
      } else {
        failed("load the faults", error);
      }
      return;
    }
    available = true;
    fieldset.disabled = busy;
    show(null);
    fillTable(table, faults.map(row), "The agent holds no fault.");
  };

  // run keeps a second click from overtaking a request: the agent takes
  // seconds to stop a node or to put a network fault in place. The list
  // is loaded again either way, and the error goes over its note.
  const run = async (what: string, request: () => Promise<void>) => {
    if (busy) return;
    busy = true;
    fieldset.disabled = true;
    try {
      await request();
      busy = false;
      await load();
    } catch (error) {
      busy = false;
      await load();
      failed(what, error);
    }
  };

  kind.addEventListener("change", showFields);
  reload.addEventListener("click", () => void load());
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const typed: FaultForm = {
      target: target.value,
      kind: kind.value as FaultKind,
      network: network.value,
      delay: delay.value,
      jitter: jitter.value,
      lossPercent: lossPercent.value,
      peers: peers.value,
      duration: duration.value,
    };
    void run(`inject a ${typed.kind} on ${typed.target.trim()}`, async () => {
      await injectFault(faultRequest(typed));
    });
  });

  showFields();
  void load();
  setInterval(() => {
    if (available && !busy) void load();
  }, refreshMs);
}
